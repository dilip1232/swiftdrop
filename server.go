package core

import (
	"crypto/hmac"
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

	// WebFS is the embedded web UI filesystem (should contain "web" dir).
	WebFS fs.FS
}

// DefaultPort is the LAN port SwiftDrop serves on for peer-to-peer transfers.
const DefaultPort = 53317

func NewServer(id Identity, reg *PeerRegistry, trk *Tracker) *Server {
	return &Server{ID: id, Reg: reg, Trk: trk}
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
	mux.HandleFunc("/api/pair/remote-unpair", func(w http.ResponseWriter, r *http.Request) {
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
	})
	mux.HandleFunc("/api/pair/claim", s.handlePairClaim) // peer presents a PIN (public)

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

	// Verify HMAC sender authentication to prevent X-From-ID spoofing.
	if authHMAC := r.Header.Get("X-Auth-HMAC"); authHMAC != "" {
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
	}

	// Check free disk space before writing (use original file size when encrypted).
	origSize := r.ContentLength
	if v := r.Header.Get("X-File-Size"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			origSize = n
		}
	}
	dlDir := DownloadDir()
	if origSize > 0 {
		if free, err := DiskFree(dlDir); err == nil && uint64(origSize)+100<<20 > free {
			http.Error(w, "not enough disk space", http.StatusInsufficientStorage)
			return
		}
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

	dest := UniquePath(filepath.Join(dlDir, name))
	f, err := os.Create(dest)
	if err != nil {
		s.Trk.Finish(tr, fmt.Errorf("create file: %w", err))
		http.Error(w, "cannot create file", http.StatusInternalServerError)
		log.Printf("inbox create %s: %v", dest, err)
		return
	}

	// If the sender signals encryption, decrypt using the paired key.
	var body io.Reader = &CountingReader{R: r.Body, Tr: tr}
	encrypted := r.Header.Get("X-Encrypted") == "aes-gcm"
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
		if err := DecryptStream(f, body, key); err != nil {
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
		s.Trk.Finish(tr, nil)
		if closeErr != nil {
			log.Printf("inbox close %s: %v", dest, closeErr)
		}
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
	if err := SendToPeerWithOpts(r.Context(), peer, s.ID, name, pr, EncryptedSize(r.ContentLength), r.ContentLength, true); err != nil {
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
		path := p
		go SendFileByPath(peer, s.ID, path, s.Trk)
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
		if tok == "" {
			tok = r.URL.Query().Get("token")
		}
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
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", base, i, ext)
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
	payload := fmt.Sprintf(`{"pin":%q,"id":%q,"name":%q}`, req.PIN, s.ID.ID, s.ID.Name)
	resp, err := TransferClient.Post(
		fmt.Sprintf("http://%s/api/pair/claim", peer.Host),
		"application/json",
		strings.NewReader(payload),
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
		Key string `json:"key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		http.Error(w, "bad response", http.StatusBadGateway)
		return
	}
	keyBytes, err := hex.DecodeString(result.Key)
	if err != nil || len(keyBytes) != 32 {
		http.Error(w, "invalid key from peer", http.StatusBadGateway)
		return
	}
	Pairs.StoreKey(req.DeviceID, keyBytes)
	log.Printf("paired with %s (%s)", peer.Name, req.DeviceID)
	WriteJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handlePairClaim(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		PIN  string `json:"pin"`
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	key, ok := Pairs.ClaimPIN(req.PIN, req.ID)
	if !ok {
		http.Error(w, "invalid or expired PIN", http.StatusForbidden)
		return
	}
	log.Printf("pairing accepted for %s (%s)", req.Name, req.ID)
	WriteJSON(w, map[string]string{"key": hex.EncodeToString(key)})
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
		Token string `json:"token"`
		ID    string `json:"id"`
		Name  string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	key, ok := Pairs.ClaimQRToken(req.Token, req.ID)
	if !ok {
		http.Error(w, "invalid or expired token", http.StatusForbidden)
		return
	}
	log.Printf("QR pairing accepted for %s (%s)", req.Name, req.ID)
	WriteJSON(w, map[string]string{"key": hex.EncodeToString(key)})
}

// StartServer launches the peer-facing HTTP server (inbox + api) on the LAN
// port. If the preferred port is taken it tries up to 10 nearby ports so the
// app still starts instead of crashing.
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
		if err := http.Serve(ln, srv.Handler()); err != nil && err != http.ErrServerClosed {
			log.Printf("serve: %v", err)
		}
	}()
}
