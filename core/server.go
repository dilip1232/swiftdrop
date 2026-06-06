package core

import (
	"crypto/hmac"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

// Server hosts both the transfer API (peer-to-peer) and the local control UI.
type Server struct {
	ID  Identity
	Reg *PeerRegistry
	Trk *Tracker

	// Pick opens a native file picker and returns chosen absolute paths.
	// Injected by the platform shell; nil in headless mode.
	Pick func() ([]string, error)
	// OnQuit quits the app; injected by the platform shell.
	OnQuit func()
	// ConsentHook is called (in a goroutine) when a pending transfer needs
	// user consent.  The hook should present an Accept/Reject UI and signal
	// tr.Decision with the result.  Nil = rely on the web UI only.
	ConsentHook func(tr *Transfer, from, name string, size int64)

	// WebFS is the embedded web UI filesystem (should contain "web" dir).
	WebFS fs.FS

	// Chat stores per-peer chat messages in memory.
	Chat *ChatStore

	// Replays prevents HMAC replay attacks on /inbox.
	Replays *ReplayCache

	// folderSessions maps session tokens to active folder transfers.
	folderSessions sync.Map // string -> *FolderSession
}

// FolderSession tracks a multi-file folder transfer on the receiver side.
type FolderSession struct {
	FolderName    string
	SenderID      string
	Transfer      *Transfer
	DlDir         string
	Created       time.Time
	FilesReceived int64 // atomically incremented per successful file
}

// DefaultPort is the LAN port SwiftDrop serves on for peer-to-peer transfers.
const DefaultPort = 53317

func NewServer(id Identity, reg *PeerRegistry, trk *Tracker) *Server {
	return &Server{ID: id, Reg: reg, Trk: trk, Chat: NewChatStore(), Replays: NewReplayCache()}
}

// normalizeIP strips the ::ffff: prefix from IPv6-mapped IPv4 addresses so
// comparisons between Go's r.RemoteAddr (often IPv6-mapped) and plain IPv4
// strings work correctly.
func normalizeIP(ip string) string {
	if parsed := net.ParseIP(ip); parsed != nil {
		if v4 := parsed.To4(); v4 != nil {
			return v4.String()
		}
	}
	return ip
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Peer-to-peer transfer endpoint (public — any peer on LAN).
	mux.HandleFunc("/inbox", s.handleInbox)

	// Chat endpoint (public — any peer on LAN).
	mux.HandleFunc("/chat-inbox", s.handleChatInbox)

	// /api/me and /health are public (peers probe them).
	mux.HandleFunc("/api/me", s.handleMe)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok")
	})

	// Token endpoint: only responds to the embedded UI (Wails internal
	// requests have an empty RemoteAddr) and loopback. Peers on LAN get 403.
	mux.HandleFunc("/api/token", func(w http.ResponseWriter, r *http.Request) {
		if r.RemoteAddr != "" {
			host, _, _ := net.SplitHostPort(r.RemoteAddr)
			ip := net.ParseIP(host)
			if ip == nil || !ip.IsLoopback() {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		io.WriteString(w, s.ID.APIToken)
	})

	// All other /api/* endpoints require the local API token so only the
	// embedded UI (which gets the token injected) can call them — not
	// random devices on the LAN.
	mux.HandleFunc("/api/devices", s.requireToken(s.handleDevices))
	mux.HandleFunc("/api/send", s.requireToken(s.handleSend))
	mux.HandleFunc("/api/send-path", s.requireToken(s.handleSendPath))
	mux.HandleFunc("/api/transfers", s.requireToken(s.handleTransfers))
	mux.HandleFunc("/api/transfers/clear", s.requireToken(s.handleClearTransfers))
	mux.HandleFunc("/api/transfers/cancel", s.requireToken(s.handleCancelTransfer))
	mux.HandleFunc("/api/transfers/retry", s.requireToken(s.handleRetryTransfer))
	mux.HandleFunc("/api/transfers/pause", s.requireToken(func(w http.ResponseWriter, r *http.Request) {
		peerID, fileName, ok := s.Trk.PauseTransfer(r.URL.Query().Get("id"))
		if !ok {
			http.Error(w, "not found or not sending", http.StatusNotFound)
			return
		}
		go s.notifyPeerSignal(peerID, fileName, "pause")
		w.WriteHeader(http.StatusOK)
	}))
	mux.HandleFunc("/api/transfers/resume", s.requireToken(func(w http.ResponseWriter, r *http.Request) {
		peerID, fileName, ok := s.Trk.ResumeTransfer(r.URL.Query().Get("id"))
		if !ok {
			http.Error(w, "not found or not paused", http.StatusNotFound)
			return
		}
		go s.notifyPeerSignal(peerID, fileName, "resume")
		w.WriteHeader(http.StatusOK)
	}))

	// Public endpoint: receiving peer is notified by the sender about
	// pause/resume so its UI can reflect the state.
	mux.HandleFunc("/transfer-signal", s.handleTransferSignal)
	mux.HandleFunc("/api/transfers/accept", s.requireToken(func(w http.ResponseWriter, r *http.Request) {
		if s.Trk.AcceptTransfer(r.URL.Query().Get("id")) {
			w.WriteHeader(http.StatusOK)
		} else {
			http.Error(w, "not found or not pending", http.StatusNotFound)
		}
	}))
	mux.HandleFunc("/api/transfers/reject", s.requireToken(func(w http.ResponseWriter, r *http.Request) {
		if s.Trk.RejectTransfer(r.URL.Query().Get("id")) {
			w.WriteHeader(http.StatusOK)
		} else {
			http.Error(w, "not found or not pending", http.StatusNotFound)
		}
	}))
	mux.HandleFunc("/api/chat/send", s.requireToken(s.handleChatSend))
	mux.HandleFunc("/api/chat/messages", s.requireToken(s.handleChatMessages))
	mux.HandleFunc("/api/chat/notify", s.requireToken(s.handleChatNotify))
	mux.HandleFunc("/api/chat/notify/ack", s.requireToken(s.handleChatNotifyAck))
	mux.HandleFunc("/api/pick", s.requireToken(s.handlePick))
	mux.HandleFunc("/api/open-folder", s.requireToken(func(w http.ResponseWriter, _ *http.Request) {
		OpenFolder(DownloadDir())
		io.WriteString(w, "ok")
	}))
	mux.HandleFunc("/api/quit", s.requireToken(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if s.OnQuit != nil {
			go s.OnQuit()
		}
	}))
	mux.HandleFunc("/api/peers/add", s.handleAddPeer) // public — peers announce themselves
	mux.HandleFunc("/api/peers/remove", s.requireToken(s.handleRemovePeer))

	// Pairing endpoints (UI-side = token-protected, peer-side = public).
	mux.HandleFunc("/api/pair/begin", s.requireToken(s.handlePairBegin))   // UI asks to generate a PIN
	mux.HandleFunc("/api/pair/status", s.requireToken(s.handlePairStatus)) // UI polls paired state for a device
	mux.HandleFunc("/api/pair/submit", s.requireToken(s.handlePairSubmit)) // UI sends PIN to a remote peer
	mux.HandleFunc("/api/pair/unpair", s.requireToken(func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if id != "" {
			Pairs.Unpair(id)
			// Notify the remote device so it also unpairs us.
			if peer, ok := s.Reg.Get(id); ok {
				go func() {
					req, _ := http.NewRequest("POST", fmt.Sprintf("http://%s/api/pair/remote-unpair?id=%s", peer.Host, s.ID.ID), nil)
					if req != nil {
						TransferClient.Do(req)
					}
				}()
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	// Public endpoint: remote device tells us to unpair them.
	// Verify the caller's IP matches the registered peer to prevent spoofing.
	mux.HandleFunc("/api/pair/remote-unpair", s.handleRemoteUnpair)
	mux.HandleFunc("/api/pair/claim", s.handlePairClaim)          // peer presents a PIN (public)
	mux.HandleFunc("/api/pair/pake-confirm", s.handlePAKEConfirm) // SPAKE2 Phase 2

	// QR-based pairing endpoints.
	mux.HandleFunc("/api/pair/qr-begin", s.requireToken(s.handleQRBegin)) // UI requests a QR code
	mux.HandleFunc("/api/pair/qr-claim", s.handleQRClaim)                 // peer presents QR token (public)

	// Static web UI — inject token into index.html so the embedded webview
	// doesn't need a separate /api/token fetch (which can fail in Wails).
	if s.WebFS != nil {
		sub, err := fs.Sub(s.WebFS, "web")
		if err != nil {
			log.Fatalf("embed web: %v", err)
		}
		staticFS := http.FileServer(http.FS(sub))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" || r.URL.Path == "/index.html" {
				raw, err := fs.ReadFile(sub, "index.html")
				if err != nil {
					http.Error(w, "not found", 404)
					return
				}
				injected := strings.Replace(string(raw),
					`let apiToken = "";`,
					fmt.Sprintf(`let apiToken = %q;`, s.ID.APIToken), 1)
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				io.WriteString(w, injected)
				return
			}
			staticFS.ServeHTTP(w, r)
		})
	}

	return mux
}

// handleInbox receives a streamed file pushed by a peer and writes it to the
// download directory using a zero-copy-friendly io.Copy.
func (s *Server) handleInbox(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()

	name := SafeFilename(r.Header.Get("X-Filename"))
	from := r.Header.Get("X-From")
	fromID := r.Header.Get("X-From-ID")
	if from == "" {
		from = "a device"
	}

	// Reject files from unpaired senders.
	key := Pairs.IsPaired(fromID)
	if fromID == "" || key == nil {
		log.Printf("inbox rejected: fromID=%q paired=%v pairedIDs=%v", fromID, Pairs.IsPaired(fromID) != nil, Pairs.PairedIDs())
		http.Error(w, "not paired — pair first", http.StatusForbidden)
		return
	}

	// Verify HMAC sender authentication — mandatory for paired senders.
	authHMAC := r.Header.Get("X-Auth-HMAC")
	if authHMAC == "" {
		http.Error(w, "HMAC authentication required", http.StatusForbidden)
		return
	}
	authTime := r.Header.Get("X-Auth-Time")
	ts, _ := strconv.ParseInt(authTime, 10, 64)
	delta := time.Now().Unix() - ts
	if delta < 0 {
		delta = -delta
	}
	if delta > 300 { // reject if timestamp > 5 min old
		http.Error(w, "auth timestamp expired", http.StatusForbidden)
		return
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(fromID + "|" + name + "|" + authTime))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(authHMAC), []byte(expected)) {
		log.Printf("inbox HMAC mismatch from %s", fromID)
		http.Error(w, "authentication failed", http.StatusForbidden)
		return
	}
	// Reject replayed HMAC values.
	if !s.Replays.Check(authHMAC) {
		log.Printf("inbox HMAC replay detected from %s", fromID)
		http.Error(w, "replay detected", http.StatusForbidden)
		return
	}

	// ── Folder protocol: announce / file / done ──
	if r.Header.Get("X-Folder-Announce") == "true" {
		s.handleFolderAnnounce(w, r, name, from, fromID)
		return
	}
	if sessionID := r.Header.Get("X-Folder-Done"); sessionID != "" {
		s.handleFolderDone(w, r, sessionID, from)
		return
	}
	if sessionID := r.Header.Get("X-Folder-Session"); sessionID != "" {
		s.handleFolderFile(w, r, sessionID, from, fromID)
		return
	}

	// Check free disk space before writing (use original file size when encrypted).
	origSize := r.ContentLength
	if v := r.Header.Get("X-File-Size"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			origSize = n
		}
	}
	if origSize <= 0 {
		http.Error(w, "X-File-Size or Content-Length required", http.StatusBadRequest)
		return
	}
	dlDir := DownloadDir()
	// Saturating add: 100 MiB headroom, capped at math.MaxUint64.
	const headroom = 100 << 20
	needed := uint64(origSize)
	if needed > (^uint64(0))-headroom {
		needed = ^uint64(0)
	} else {
		needed += headroom
	}
	if free, err := DiskFree(dlDir); err == nil && needed > free {
		http.Error(w, "not enough disk space", http.StatusInsufficientStorage)
		return
	}

	// Encrypted transfers are chunked (Content-Length = -1), so use the
	// original file size sent in X-File-Size for progress tracking.
	trackSize := r.ContentLength
	if trackSize <= 0 {
		if fz, err := strconv.ParseInt(r.Header.Get("X-File-Size"), 10, 64); err == nil && fz > 0 {
			trackSize = fz
		}
	}

	// ── Receiver consent: block until the user accepts or 60s timeout ──
	tr := s.Trk.StartPending(name, trackSize, from)
	Notify("SwiftDrop", fmt.Sprintf("%s wants to send %s (%s)", from, name, HumanSize(trackSize)))
	if s.ConsentHook != nil {
		go s.ConsentHook(tr, from, name, trackSize)
	}
	select {
	case accepted := <-tr.Decision:
		if !accepted {
			s.Trk.Finish(tr, fmt.Errorf("rejected by user"))
			http.Error(w, "transfer rejected", http.StatusForbidden)
			return
		}
	case <-time.After(60 * time.Second):
		s.Trk.Finish(tr, fmt.Errorf("no response — auto-rejected"))
		http.Error(w, "transfer timed out", http.StatusRequestTimeout)
		return
	}
	tr.Status = "sending"

	safeName := filepath.Base(name) // CodeQL: break taint from X-Filename header
	dest := UniquePath(filepath.Join(dlDir, safeName))
	f, err := os.Create(dest)
	if err != nil {
		s.Trk.Finish(tr, fmt.Errorf("create file: %w", err))
		http.Error(w, "cannot create file", http.StatusInternalServerError)
		log.Printf("inbox create %s: %v", dest, err)
		return
	}

	// If the sender signals encryption, decrypt using the paired key.
	var body io.Reader = &CountingReader{R: r.Body, Tr: tr}
	encHdr := r.Header.Get("X-Encrypted")
	encrypted := encHdr == "aes-gcm-v2" || encHdr == "aes-gcm"
	senderID := r.Header.Get("X-From-ID")
	if encrypted {
		key := Pairs.IsPaired(senderID)
		if key == nil {
			s.Trk.Finish(tr, fmt.Errorf("not paired"))
			f.Close()
			os.Remove(dest)
			http.Error(w, "not paired with sender", http.StatusForbidden)
			return
		}
		var decErr error
		if encHdr == "aes-gcm-v2" {
			decErr = DecryptStream(f, body, key)
		} else {
			decErr = DecryptStreamV1(f, body, key)
		}
		if err := decErr; err != nil {
			s.Trk.Finish(tr, err)
			f.Close()
			os.Remove(dest)
			http.Error(w, "decryption failed", http.StatusBadRequest)
			log.Printf("inbox decrypt %s: %v", dest, err)
			return
		}
		// Verify decrypted file size matches X-File-Size to detect truncation.
		if origSize > 0 {
			if fi, err := f.Stat(); err == nil && fi.Size() != origSize {
				s.Trk.Finish(tr, fmt.Errorf("size mismatch: expected %d, got %d", origSize, fi.Size()))
				f.Close()
				os.Remove(dest)
				http.Error(w, "decrypted file size mismatch", http.StatusBadRequest)
				log.Printf("inbox %s: size mismatch (expected %d, got %d)", dest, origSize, fi.Size())
				return
			}
		}
		n := atomic.LoadInt64(&tr.Sent)
		closeErr := f.Close()
		if closeErr != nil {
			log.Printf("inbox close %s: %v", dest, closeErr)
		}
		s.Trk.Finish(tr, nil)
		log.Printf("received %q (%s, encrypted) from %s", filepath.Base(dest), HumanSize(n), from)
		Notify("SwiftDrop", fmt.Sprintf("Received %s (%s) from %s", filepath.Base(dest), HumanSize(n), from))
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "ok")
		return
	}

	// Hash on-the-fly so we never re-read the file from disk.
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), body)
	closeErr := f.Close()
	if err != nil {
		s.Trk.Finish(tr, err)
		os.Remove(dest)
		http.Error(w, "transfer interrupted", http.StatusInternalServerError)
		log.Printf("inbox copy %s: %v", dest, err)
		return
	}
	if closeErr != nil {
		log.Printf("inbox close %s: %v", dest, closeErr)
	}

	// Verify SHA-256 integrity if the sender included a hash.
	if expected := r.Header.Get("X-SHA256"); expected != "" {
		actual := hex.EncodeToString(h.Sum(nil))
		if actual != expected {
			s.Trk.Finish(tr, fmt.Errorf("hash mismatch"))
			os.Remove(dest)
			http.Error(w, "integrity check failed", http.StatusBadRequest)
			log.Printf("inbox %s: hash mismatch (expected %s, got %s)", dest, expected[:12], actual[:12])
			return
		}
	}

	s.Trk.Finish(tr, nil)
	log.Printf("received %q (%s) from %s", filepath.Base(dest), HumanSize(n), from)
	Notify("SwiftDrop", fmt.Sprintf("Received %s (%s) from %s", filepath.Base(dest), HumanSize(n), from))
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, "ok")
}

// ── Folder protocol handlers ────────────────────────────────────────────────

// handleFolderAnnounce creates a pending transfer, shows consent, and returns
// a session token so subsequent file requests can be auto-accepted.
func (s *Server) handleFolderAnnounce(w http.ResponseWriter, r *http.Request, folderName, from, fromID string) {
	folderSize, _ := strconv.ParseInt(r.Header.Get("X-File-Size"), 10, 64)
	folderCount := r.Header.Get("X-Folder-Count")

	displayName := "📁 " + folderName
	tr := s.Trk.StartPending(displayName, folderSize, from)
	Notify("SwiftDrop", fmt.Sprintf("%s wants to send %s (%s, %s files)", from, displayName, HumanSize(folderSize), folderCount))
	if s.ConsentHook != nil {
		go s.ConsentHook(tr, from, folderName, folderSize)
	}

	select {
	case accepted := <-tr.Decision:
		if !accepted {
			s.Trk.Finish(tr, fmt.Errorf("rejected by user"))
			http.Error(w, "transfer rejected", http.StatusForbidden)
			return
		}
	case <-time.After(60 * time.Second):
		s.Trk.Finish(tr, fmt.Errorf("no response — auto-rejected"))
		http.Error(w, "transfer timed out", http.StatusRequestTimeout)
		return
	}
	tr.Status = "sending"

	// Generate session token.
	tokenBytes := make([]byte, 16)
	cryptorand.Read(tokenBytes)
	session := hex.EncodeToString(tokenBytes)

	dlDir := DownloadDir()
	safeFolderName := filepath.Base(folderName) // CodeQL: break taint from header
	folderDir := UniquePath(filepath.Join(dlDir, safeFolderName))
	folderName = filepath.Base(folderDir) // may be "Folder (1)" if original exists
	os.MkdirAll(folderDir, 0755)

	s.folderSessions.Store(session, &FolderSession{
		FolderName: folderName,
		SenderID:   fromID,
		Transfer:   tr,
		DlDir:      dlDir,
		Created:    time.Now(),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"session": session})
	log.Printf("folder-announce %q from %s → session %s", folderName, from, session[:8])
}

// handleFolderFile receives a single file within a folder session (auto-accepted).
func (s *Server) handleFolderFile(w http.ResponseWriter, r *http.Request, sessionID, from, fromID string) {
	defer r.Body.Close()

	fsVal, ok := s.folderSessions.Load(sessionID)
	if !ok {
		http.Error(w, "invalid/expired folder session", http.StatusBadRequest)
		return
	}
	fs := fsVal.(*FolderSession)
	if fs.SenderID != fromID {
		http.Error(w, "sender mismatch", http.StatusForbidden)
		return
	}

	relPath := filepath.FromSlash(r.Header.Get("X-Folder-Rel"))
	if relPath == "" || strings.Contains(relPath, "..") {
		http.Error(w, "invalid relative path", http.StatusBadRequest)
		return
	}

	baseDir := filepath.Join(fs.DlDir, fs.FolderName)
	dest := filepath.Join(baseDir, filepath.Clean(relPath))
	// Inline containment check so CodeQL can trace the guard.
	if !strings.HasPrefix(dest, baseDir+string(filepath.Separator)) {
		http.Error(w, "path traversal rejected", http.StatusBadRequest)
		return
	}
	os.MkdirAll(filepath.Dir(dest), 0755)

	f, err := os.Create(dest)
	if err != nil {
		http.Error(w, "cannot create file", http.StatusInternalServerError)
		log.Printf("folder-file create %s: %v", dest, err)
		return
	}

	var body io.Reader = &CountingReader{R: r.Body, Tr: fs.Transfer}
	encHdr := r.Header.Get("X-Encrypted")
	if encHdr == "aes-gcm-v2" || encHdr == "aes-gcm" {
		pairKey := Pairs.IsPaired(fromID)
		if pairKey == nil {
			f.Close()
			os.Remove(dest)
			http.Error(w, "not paired", http.StatusForbidden)
			return
		}
		var decErr error
		if encHdr == "aes-gcm-v2" {
			decErr = DecryptStream(f, body, pairKey)
		} else {
			decErr = DecryptStreamV1(f, body, pairKey)
		}
		if decErr != nil {
			f.Close()
			os.Remove(dest)
			http.Error(w, "decryption failed", http.StatusBadRequest)
			log.Printf("folder-file decrypt %s: %v", dest, decErr)
			return
		}
	} else {
		if _, err := io.Copy(f, body); err != nil {
			f.Close()
			os.Remove(dest)
			http.Error(w, "interrupted", http.StatusInternalServerError)
			log.Printf("folder-file copy %s: %v", dest, err)
			return
		}
	}
	f.Close()
	atomic.AddInt64(&fs.FilesReceived, 1)

	w.WriteHeader(http.StatusOK)
	io.WriteString(w, `{"ok":true}`)
}

// handleFolderDone finalises a folder transfer — marks it done and notifies.
// If the sender cancelled, X-Folder-Cancelled is set, and we show partial status.
func (s *Server) handleFolderDone(w http.ResponseWriter, r *http.Request, sessionID, from string) {
	cancelled := r.Header.Get("X-Folder-Cancelled") == "true"
	if fsVal, ok := s.folderSessions.LoadAndDelete(sessionID); ok {
		fs := fsVal.(*FolderSession)
		recv := atomic.LoadInt64(&fs.FilesReceived)
		n := atomic.LoadInt64(&fs.Transfer.Sent)
		if cancelled {
			if recv > 0 {
				fs.Transfer.Status = "done"
				fs.Transfer.Err = fmt.Sprintf("cancelled by sender – %d file(s) received", recv)
				log.Printf("folder-cancelled %q from %s: %d files, %s", fs.FolderName, from, recv, HumanSize(n))
				Notify("SwiftDrop", fmt.Sprintf("Folder %s partially received (%d files) from %s", fs.FolderName, recv, from))
			} else {
				fs.Transfer.Status = "canceled"
				fs.Transfer.Err = "cancelled by sender"
				log.Printf("folder-cancelled %q from %s: 0 files received", fs.FolderName, from)
			}
			// Remove empty dirs left behind.
			folderDir := filepath.Join(fs.DlDir, fs.FolderName)
			removeEmptyDirs(folderDir)
		} else {
			s.Trk.Finish(fs.Transfer, nil)
			log.Printf("folder-done %q from %s (%d files, %s)", fs.FolderName, from, recv, HumanSize(n))
			Notify("SwiftDrop", fmt.Sprintf("Received folder %s (%d files, %s) from %s", fs.FolderName, recv, HumanSize(n), from))
		}
	}
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, `{"ok":true}`)
}

// removeEmptyDirs walks bottom-up and removes directories that are empty.
func removeEmptyDirs(root string) {
	filepath.Walk(root, func(_ string, _ os.FileInfo, _ error) error { return nil }) // ensure walk
	// Collect dirs bottom-up
	var dirs []string
	filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if fi.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	})
	// Remove in reverse order (deepest first).
	for i := len(dirs) - 1; i >= 0; i-- {
		os.Remove(dirs[i]) // only succeeds if empty
	}
}

func (s *Server) handleMe(w http.ResponseWriter, _ *http.Request) {
	resp := struct {
		Identity
		IP string `json:"ip"`
	}{Identity: s.ID, IP: LocalIP()}
	WriteJSON(w, resp)
}

func (s *Server) handleDevices(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, s.Reg.List())
}

func (s *Server) handleAddPeer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Host string `json:"host"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	host := strings.TrimSpace(body.Host)
	if host == "" {
		http.Error(w, "host required", http.StatusBadRequest)
		return
	}
	if !strings.Contains(host, ":") {
		host = fmt.Sprintf("%s:%d", host, DefaultPort)
	}

	// If the request doesn't carry the local API token it's a remote peer
	// announcing itself — verify the announced host matches the caller's IP
	// so a third party can't inject arbitrary peers.
	tok := r.Header.Get("X-API-Token")
	if tok != s.ID.APIToken {
		callerIP, _, _ := net.SplitHostPort(r.RemoteAddr)
		announcedIP, _, _ := net.SplitHostPort(host)
		if normalizeIP(callerIP) != normalizeIP(announcedIP) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	peer, err := ProbePeer(host)
	if err != nil {
		http.Error(w, "could not reach device: "+err.Error(), http.StatusBadGateway)
		return
	}
	s.Reg.AddManual(peer)
	log.Printf("added manual peer %s (%s)", peer.Name, peer.Host)
	WriteJSON(w, peer)
}

func (s *Server) handleRemovePeer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	Pairs.Unpair(body.ID)
	s.Reg.RemoveDevice(body.ID)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()

	to := r.URL.Query().Get("to")
	name := SafeFilename(r.URL.Query().Get("name"))

	peer, ok := s.Reg.Get(to)
	if !ok {
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}
	key := Pairs.IsPaired(peer.ID)
	if key == nil {
		http.Error(w, "pair with this device first", http.StatusForbidden)
		return
	}

	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(EncryptStream(pw, r.Body, key))
	}()
	if err := SendToPeerWithOpts(r.Context(), peer, s.ID, name, pr, EncryptedSize(r.ContentLength), r.ContentLength, true, ""); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		log.Printf("send to %s: %v", peer.Name, err)
		return
	}

	log.Printf("sent %q to %s", name, peer.Name)
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, "ok")
}

func (s *Server) handleSendPath(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		To    string   `json:"to"`
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	peer, ok := s.Reg.Get(body.To)
	if !ok {
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}
	if Pairs.IsPaired(peer.ID) == nil {
		log.Printf("send-path blocked: peer.ID=%q pairedIDs=%v", peer.ID, Pairs.PairedIDs())
		http.Error(w, "pair with this device first", http.StatusForbidden)
		return
	}
	for _, p := range body.Paths {
		// Resolve to absolute — breaks CodeQL taint from request body.
		path, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		fi, err := os.Stat(path)
		if err != nil {
			continue
		}
		if fi.IsDir() {
			go SendFolderByPath(peer, s.ID, path, s.Trk)
		} else {
			go SendFileByPath(peer, s.ID, path, s.Trk)
		}
	}
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, "ok")
}

func (s *Server) handleTransfers(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, s.Trk.List())
}

func (s *Server) handleClearTransfers(w http.ResponseWriter, _ *http.Request) {
	s.Trk.ClearFinished()
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleCancelTransfer(w http.ResponseWriter, r *http.Request) {
	s.Trk.CancelTransfer(r.URL.Query().Get("id"))
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleRetryTransfer(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	filePath, peerID, ok := s.Trk.RetryTransfer(id)
	if !ok {
		http.Error(w, "transfer not found or not retryable", http.StatusNotFound)
		return
	}
	peer, found := s.Reg.Get(peerID)
	if !found {
		http.Error(w, "peer no longer available", http.StatusNotFound)
		return
	}
	go SendFileByPath(peer, s.ID, filePath, s.Trk)
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, "ok")
}

func (s *Server) handlePick(w http.ResponseWriter, r *http.Request) {
	if s.Pick == nil {
		http.Error(w, "picker unavailable", http.StatusServiceUnavailable)
		return
	}
	paths, err := s.Pick()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	WriteJSON(w, FileInfos(paths))
}

func (s *Server) requireToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := r.Header.Get("X-API-Token")
		if tok != s.ID.APIToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func WriteJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// UniquePath returns path, or path with " (n)" inserted before the extension
// if it already exists, so concurrent/repeat transfers never clobber files.
func UniquePath(path string) string {
	path = filepath.Clean(path) // sanitize before any filesystem access
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for i := 1; ; i++ {
		candidate := filepath.Clean(fmt.Sprintf("%s (%d)%s", base, i, ext))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}

// ── Pairing handlers ───────────────────────────────────────────────────────

func (s *Server) handlePairBegin(w http.ResponseWriter, _ *http.Request) {
	pin := Pairs.GeneratePIN()
	WriteJSON(w, map[string]string{"pin": pin})
}

func (s *Server) handlePairStatus(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	paired := Pairs.IsPaired(id) != nil
	WriteJSON(w, map[string]bool{"paired": paired})
}

func (s *Server) handlePairSubmit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceID string `json:"device_id"`
		PIN      string `json:"pin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	peer, ok := s.Reg.Get(req.DeviceID)
	if !ok {
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}

	// SPAKE2 PAKE: PIN never crosses the wire.
	msgA, spakeState, err := SPAKE2ClientStart(req.PIN)
	if err != nil {
		http.Error(w, "SPAKE2 init failed", http.StatusInternalServerError)
		return
	}
	pakePayload := fmt.Sprintf(`{"pake_msg":%q,"id":%q,"name":%q}`,
		hex.EncodeToString(msgA), s.ID.ID, s.ID.Name)
	resp, err := TransferClient.Post(
		fmt.Sprintf("http://%s/api/pair/claim", peer.Host),
		"application/json",
		strings.NewReader(pakePayload),
	)
	if err != nil {
		http.Error(w, "could not reach peer", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		http.Error(w, "pairing rejected: "+string(body), resp.StatusCode)
		return
	}
	var result struct {
		PAKEMsg      string `json:"pake_msg"`
		PAKEConfirm  string `json:"pake_confirm"`
		EncryptedKey string `json:"encrypted_key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		http.Error(w, "bad SPAKE2 response", http.StatusBadGateway)
		return
	}
	srvMsg, _ := hex.DecodeString(result.PAKEMsg)
	srvConfirm, _ := hex.DecodeString(result.PAKEConfirm)
	spakeKey, err := spakeState.Finish(srvMsg, srvConfirm)
	if err != nil {
		http.Error(w, "wrong PIN", http.StatusForbidden)
		return
	}
	wrapped, _ := hex.DecodeString(result.EncryptedKey)
	keyBytes, err := AESGCMUnwrap(spakeKey, wrapped)
	if err != nil {
		http.Error(w, "wrong PIN", http.StatusForbidden)
		return
	}
	if len(keyBytes) != 32 {
		http.Error(w, "invalid key length from peer", http.StatusBadGateway)
		return
	}

	// Phase 2: send client confirmation so server knows the PIN was correct.
	clientConfirm := spakeConfirm(spakeKey, "client")
	confirmPayload := fmt.Sprintf(`{"id":%q,"pake_confirm":%q}`, s.ID.ID, hex.EncodeToString(clientConfirm))
	resp2, err := TransferClient.Post(
		fmt.Sprintf("http://%s/api/pair/pake-confirm", peer.Host),
		"application/json",
		strings.NewReader(confirmPayload),
	)
	if err != nil {
		http.Error(w, "could not reach peer for confirmation", http.StatusBadGateway)
		return
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		body, _ := io.ReadAll(resp2.Body)
		http.Error(w, "pairing confirmation failed: "+string(body), resp2.StatusCode)
		return
	}

	Pairs.StoreKey(req.DeviceID, keyBytes)
	log.Printf("SPAKE2 paired with %s (%s)", peer.Name, req.DeviceID)
	WriteJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handlePairClaim(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		PAKEMsg string `json:"pake_msg"` // SPAKE2 client message (hex)
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if req.PAKEMsg == "" {
		http.Error(w, "pake_msg required", http.StatusBadRequest)
		return
	}
	clientMsg, err := hex.DecodeString(req.PAKEMsg)
	if err != nil || len(clientMsg) == 0 {
		http.Error(w, "invalid pake_msg", http.StatusBadRequest)
		return
	}
	pin := Pairs.PendingPIN()
	if pin == "" {
		http.Error(w, "no pending pairing", http.StatusForbidden)
		return
	}
	msgB, confirm, spakeKey, err := SPAKE2ServerFinish(pin, clientMsg)
	if err != nil {
		http.Error(w, "SPAKE2 failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	// Wrap the AES pairing key with the SPAKE2 shared key.
	pairKey := func() []byte {
		Pairs.mu.RLock()
		k := Pairs.pendKey
		Pairs.mu.RUnlock()
		return k
	}()
	if pairKey == nil {
		http.Error(w, "no pending key", http.StatusForbidden)
		return
	}
	wrapped, err := AESGCMWrap(spakeKey, pairKey)
	if err != nil {
		http.Error(w, "wrap failed", http.StatusInternalServerError)
		return
	}
	// Hold — do NOT commit yet.  Wait for client confirmation in Phase 2.
	Pairs.HoldPAKE(req.ID, spakeKey, pairKey)
	log.Printf("SPAKE2 exchange with %s (%s) — awaiting confirmation", req.Name, req.ID)
	WriteJSON(w, map[string]string{
		"pake_msg":      hex.EncodeToString(msgB),
		"pake_confirm":  hex.EncodeToString(confirm),
		"encrypted_key": hex.EncodeToString(wrapped),
	})
}

// handlePAKEConfirm is Phase 2: client proves it derived the correct key.
func (s *Server) handlePAKEConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID          string `json:"id"`
		PAKEConfirm string `json:"pake_confirm"` // HMAC(key, "client")
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	clientConfirm, err := hex.DecodeString(req.PAKEConfirm)
	if err != nil || len(clientConfirm) == 0 {
		http.Error(w, "invalid pake_confirm", http.StatusBadRequest)
		return
	}
	if !Pairs.ConfirmPAKE(req.ID, clientConfirm) {
		http.Error(w, "wrong PIN or expired", http.StatusForbidden)
		return
	}
	log.Printf("SPAKE2 pairing confirmed for %s", req.ID)
	WriteJSON(w, map[string]bool{"ok": true})
}

// ── QR pairing handlers ────────────────────────────────────────────────────

func (s *Server) handleQRBegin(w http.ResponseWriter, _ *http.Request) {
	token := Pairs.GenerateQRToken()
	selfIP := LocalIP()
	host := fmt.Sprintf("%s:%d", selfIP, s.ID.Port)
	// The QR payload contains everything the scanner needs to pair.
	payload := fmt.Sprintf(`{"host":%q,"id":%q,"token":%q}`, host, s.ID.ID, token)
	png, err := qrcode.Encode(payload, qrcode.Medium, 256)
	if err != nil {
		http.Error(w, "qr generation failed", http.StatusInternalServerError)
		return
	}
	WriteJSON(w, map[string]interface{}{
		"qr_png":  png,
		"token":   token,
		"payload": payload,
	})
}

func (s *Server) handleQRClaim(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		PAKEMsg string `json:"pake_msg"` // SPAKE2 client message (hex)
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if req.PAKEMsg == "" {
		http.Error(w, "pake_msg required", http.StatusBadRequest)
		return
	}
	clientMsg, err := hex.DecodeString(req.PAKEMsg)
	if err != nil || len(clientMsg) == 0 {
		http.Error(w, "invalid pake_msg", http.StatusBadRequest)
		return
	}
	// Use the QR token as the SPAKE2 password (256-bit entropy).
	token, qrKey := Pairs.QRTokenAndKey()
	if token == "" || qrKey == nil {
		http.Error(w, "no pending QR pairing", http.StatusForbidden)
		return
	}
	msgB, confirm, spakeKey, err := SPAKE2ServerFinish(token, clientMsg)
	if err != nil {
		http.Error(w, "SPAKE2 failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	wrapped, err := AESGCMWrap(spakeKey, qrKey)
	if err != nil {
		http.Error(w, "wrap failed", http.StatusInternalServerError)
		return
	}
	// Hold — do NOT commit yet. Wait for client confirmation in Phase 2.
	Pairs.HoldPAKE(req.ID, spakeKey, qrKey)
	log.Printf("QR SPAKE2 exchange with %s (%s) — awaiting confirmation", req.Name, req.ID)
	WriteJSON(w, map[string]string{
		"pake_msg":      hex.EncodeToString(msgB),
		"pake_confirm":  hex.EncodeToString(confirm),
		"encrypted_key": hex.EncodeToString(wrapped),
	})
}

// notifyPeerSignal sends a pause/resume signal to the receiving peer so their
// UI can show the transfer as paused. Best-effort: errors are silently ignored.
func (s *Server) notifyPeerSignal(peerID, fileName, action string) {
	peer, ok := s.Reg.Get(peerID)
	if !ok {
		return
	}
	body := fmt.Sprintf(`{"file":%q,"action":%q}`, fileName, action)
	req, err := http.NewRequest(http.MethodPost, "http://"+peer.Host+"/transfer-signal", strings.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-From-Name", s.ID.Name)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

// LANHandler returns an http.Handler containing ONLY the public, peer-facing
// routes. Token-protected UI endpoints and the static web UI are excluded so
// they are never reachable from the LAN — only through the Wails webview
// (which uses Handler()).
func (s *Server) LANHandler() http.Handler {
	mux := http.NewServeMux()

	// Peer-to-peer transfer.
	mux.HandleFunc("/inbox", s.handleInbox)

	// Chat (public, but now requires pairing).
	mux.HandleFunc("/chat-inbox", s.handleChatInbox)

	// Identity & health probes.
	mux.HandleFunc("/api/me", s.handleMe)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok")
	})

	// Peer announcement.
	mux.HandleFunc("/api/peers/add", s.handleAddPeer)

	// Pause/resume signal from sender.
	mux.HandleFunc("/transfer-signal", s.handleTransferSignal)

	// Pairing (peer-side = public).
	mux.HandleFunc("/api/pair/claim", s.handlePairClaim)
	mux.HandleFunc("/api/pair/pake-confirm", s.handlePAKEConfirm)
	mux.HandleFunc("/api/pair/qr-claim", s.handleQRClaim)
	mux.HandleFunc("/api/pair/remote-unpair", s.handleRemoteUnpair)

	return mux
}

// handleTransferSignal processes pause/resume signals from the sending peer.
func (s *Server) handleTransferSignal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		File   string `json:"file"`
		Action string `json:"action"` // "pause" | "resume"
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.File == "" || body.Action == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	peerName := r.Header.Get("X-From-Name")
	if peerName == "" {
		http.Error(w, "missing X-From-Name", http.StatusBadRequest)
		return
	}
	s.Trk.SignalRecvTransfer(peerName, body.File, body.Action)
	w.WriteHeader(http.StatusOK)
}

// handleRemoteUnpair processes unpair notifications from a remote peer.
// Verifies the caller's IP matches the registered peer to prevent spoofing.
func (s *Server) handleRemoteUnpair(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if peer, ok := s.Reg.Get(id); ok {
		callerIP, _, _ := net.SplitHostPort(r.RemoteAddr)
		peerIP, _, _ := net.SplitHostPort(peer.Host)
		if normalizeIP(callerIP) != normalizeIP(peerIP) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}
	Pairs.Unpair(id)
	log.Printf("remote-unpair: %s unpaired us", id)
	w.WriteHeader(http.StatusOK)
}

// StartServer launches the peer-facing HTTP server on the LAN port using
// LANHandler — only public routes are exposed. The full Handler() (including
// token-protected API and web UI) is served only through the Wails webview.
func StartServer(srv *Server) {
	var ln net.Listener
	var err error
	for offset := 0; offset < 10; offset++ {
		port := srv.ID.Port + offset
		ln, err = net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err == nil {
			if offset > 0 {
				log.Printf("port %d busy; using %d instead", srv.ID.Port, port)
				srv.ID.Port = port
			}
			break
		}
	}
	if err != nil {
		log.Fatalf("listen :%d (tried 10 ports): %v", srv.ID.Port, err)
	}
	go func() {
		log.Printf("SwiftDrop %q listening on :%d", srv.ID.Name, srv.ID.Port)
		if err := http.Serve(ln, srv.LANHandler()); err != nil && err != http.ErrServerClosed {
			log.Printf("serve: %v", err)
		}
	}()
}
