package main

import (
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
)

//go:embed web
var webFS embed.FS

// Server hosts both the transfer API (peer-to-peer) and the local control UI.
type Server struct {
	id  Identity
	reg *peerRegistry
	trk *tracker

	// pick opens a native file picker and returns chosen absolute paths.
	// Injected by main (it needs the Wails app); nil in headless mode.
	pick func() ([]string, error)
	// onQuit quits the app; injected by main.
	onQuit func()
}

func newServer(id Identity, reg *peerRegistry, trk *tracker) *Server {
	return &Server{id: id, reg: reg, trk: trk}
}

func (s *Server) handler() http.Handler {
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
		io.WriteString(w, s.id.APIToken)
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
	mux.HandleFunc("/api/pick", s.requireToken(s.handlePick))
	mux.HandleFunc("/api/open-folder", s.requireToken(func(w http.ResponseWriter, _ *http.Request) {
		exec.Command("open", downloadDir()).Start()
		io.WriteString(w, "ok")
	}))
	mux.HandleFunc("/api/quit", s.requireToken(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if s.onQuit != nil {
			go s.onQuit()
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
			pairs.Unpair(id)
			// Notify the remote device so it also unpairs us.
			if peer, ok := s.reg.get(id); ok {
				go func() {
					req, _ := http.NewRequest("POST", fmt.Sprintf("http://%s/api/pair/remote-unpair?id=%s", peer.Host, s.id.ID), nil)
					if req != nil {
						transferClient.Do(req)
					}
				}()
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	// Public endpoint: remote device tells us to unpair them.
	mux.HandleFunc("/api/pair/remote-unpair", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if id != "" {
			pairs.Unpair(id)
			log.Printf("remote-unpair: %s unpaired us", id)
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/pair/claim", s.handlePairClaim) // peer presents a PIN (public)

	// Static web UI — inject token into index.html so the embedded webview
	// doesn't need a separate /api/token fetch (which can fail in Wails).
	sub, err := fs.Sub(webFS, "web")
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
				fmt.Sprintf(`let apiToken = %q;`, s.id.APIToken), 1)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, injected)
			return
		}
		staticFS.ServeHTTP(w, r)
	})

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

	name := safeFilename(r.Header.Get("X-Filename"))
	from := r.Header.Get("X-From")
	fromID := r.Header.Get("X-From-ID")
	if from == "" {
		from = "a device"
	}

	// Reject files from unpaired senders.
	if fromID == "" || pairs.IsPaired(fromID) == nil {
		log.Printf("inbox rejected: fromID=%q paired=%v pairedIDs=%v", fromID, pairs.IsPaired(fromID) != nil, pairs.PairedIDs())
		http.Error(w, "not paired — pair first", http.StatusForbidden)
		return
	}

	// Check free disk space before writing (use original file size when encrypted).
	origSize := r.ContentLength
	if v := r.Header.Get("X-File-Size"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			origSize = n
		}
	}
	dlDir := downloadDir()
	if origSize > 0 {
		if free, err := diskFree(dlDir); err == nil && uint64(origSize) > free-100<<20 {
			http.Error(w, "not enough disk space", http.StatusInsufficientStorage)
			return
		}
	}

	dest := uniquePath(filepath.Join(dlDir, name))
	f, err := os.Create(dest)
	if err != nil {
		http.Error(w, "cannot create file", http.StatusInternalServerError)
		log.Printf("inbox create %s: %v", dest, err)
		return
	}

	// Track the incoming transfer so the UI shows receive progress live.
	// Encrypted transfers are chunked (Content-Length = -1), so use the
	// original file size sent in X-File-Size for progress tracking.
	trackSize := r.ContentLength
	if trackSize <= 0 {
		if fs, err := strconv.ParseInt(r.Header.Get("X-File-Size"), 10, 64); err == nil && fs > 0 {
			trackSize = fs
		}
	}
	tr := s.trk.start(name, trackSize, from, "recv")

	// If the sender signals encryption, decrypt using the paired key.
	var body io.Reader = &countingReader{r: r.Body, tr: tr}
	encrypted := r.Header.Get("X-Encrypted") == "aes-gcm"
	senderID := r.Header.Get("X-From-ID")
	if encrypted {
		key := pairs.IsPaired(senderID)
		if key == nil {
			s.trk.finish(tr, fmt.Errorf("not paired"))
			f.Close()
			os.Remove(dest)
			http.Error(w, "not paired with sender", http.StatusForbidden)
			return
		}
		if err := decryptStream(f, body, key); err != nil {
			s.trk.finish(tr, err)
			f.Close()
			os.Remove(dest)
			http.Error(w, "decryption failed", http.StatusBadRequest)
			log.Printf("inbox decrypt %s: %v", dest, err)
			return
		}
		n := atomic.LoadInt64(&tr.Sent)
		closeErr := f.Close()
		s.trk.finish(tr, nil)
		if closeErr != nil {
			log.Printf("inbox close %s: %v", dest, closeErr)
		}
		log.Printf("received %q (%s, encrypted) from %s", filepath.Base(dest), humanSize(n), from)
		notify("SwiftDrop", fmt.Sprintf("Received %s (%s) from %s", filepath.Base(dest), humanSize(n), from))
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "ok")
		return
	}

	n, err := io.Copy(f, body)
	closeErr := f.Close()
	if err != nil {
		s.trk.finish(tr, err)
		os.Remove(dest)
		http.Error(w, "transfer interrupted", http.StatusInternalServerError)
		log.Printf("inbox copy %s: %v", dest, err)
		return
	}
	s.trk.finish(tr, nil)
	if closeErr != nil {
		log.Printf("inbox close %s: %v", dest, closeErr)
	}

	log.Printf("received %q (%s) from %s", filepath.Base(dest), humanSize(n), from)
	notify("SwiftDrop", fmt.Sprintf("Received %s (%s) from %s", filepath.Base(dest), humanSize(n), from))
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, "ok")
}

func (s *Server) handleMe(w http.ResponseWriter, _ *http.Request) {
	// Compute IP live (it can change) and emit it alongside the identity.
	resp := struct {
		Identity
		IP string `json:"ip"`
	}{Identity: s.id, IP: localIP()}
	writeJSON(w, resp)
}

func (s *Server) handleDevices(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.reg.list())
}

// handleAddPeer adds a device by host (ip or ip:port). It probes the target's
// /api/me to learn its real identity, so the manual entry behaves exactly like
// a discovered one.
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
		host = fmt.Sprintf("%s:%d", host, defaultPort)
	}

	peer, err := probePeer(host)
	if err != nil {
		http.Error(w, "could not reach device: "+err.Error(), http.StatusBadGateway)
		return
	}
	s.reg.addManual(peer)
	log.Printf("added manual peer %s (%s)", peer.Name, peer.Host)
	writeJSON(w, peer)
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
	pairs.Unpair(body.ID)
	s.reg.removeDevice(body.ID)
	w.WriteHeader(http.StatusOK)
}

// handleSend takes a file streamed from the local UI and relays it to the
// chosen peer. The request body is piped straight through — never fully
// buffered — so even multi-GB files use near-constant memory.
func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()

	to := r.URL.Query().Get("to")
	name := safeFilename(r.URL.Query().Get("name"))

	peer, ok := s.reg.get(to)
	if !ok {
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}
	if pairs.IsPaired(peer.ID) == nil {
		http.Error(w, "pair with this device first", http.StatusForbidden)
		return
	}

	if err := sendToPeerWithOpts(r.Context(), peer, s.id, name, r.Body, r.ContentLength, r.ContentLength, false); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		log.Printf("send to %s: %v", peer.Name, err)
		return
	}

	log.Printf("sent %q to %s", name, peer.Name)
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, "ok")
}

// handleSendPath is the fast path used by the Wails UI: the body lists
// absolute file paths the Go process opens and streams itself (no browser
// upload). Each file is sent in its own goroutine and tracked for progress.
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
	peer, ok := s.reg.get(body.To)
	if !ok {
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}
	if pairs.IsPaired(peer.ID) == nil {
		log.Printf("send-path blocked: peer.ID=%q pairedIDs=%v", peer.ID, pairs.PairedIDs())
		http.Error(w, "pair with this device first", http.StatusForbidden)
		return
	}
	for _, p := range body.Paths {
		path := p
		go sendFileByPath(peer, s.id, path, s.trk)
	}
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, "ok")
}

func (s *Server) handleTransfers(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.trk.list())
}

func (s *Server) handleClearTransfers(w http.ResponseWriter, _ *http.Request) {
	s.trk.clearFinished()
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleCancelTransfer(w http.ResponseWriter, r *http.Request) {
	s.trk.cancel(r.URL.Query().Get("id"))
	w.WriteHeader(http.StatusOK)
}

// handlePick opens the native file dialog and returns the chosen files with
// their sizes so the UI can stage them before sending.
func (s *Server) handlePick(w http.ResponseWriter, r *http.Request) {
	if s.pick == nil {
		http.Error(w, "picker unavailable", http.StatusServiceUnavailable)
		return
	}
	paths, err := s.pick()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, fileInfos(paths))
}

// requireToken wraps a handler and rejects requests that don't carry the local
// API token (via X-API-Token header or ?token= query). This keeps control
// endpoints callable only by the embedded UI, not by random LAN devices.
func (s *Server) requireToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := r.Header.Get("X-API-Token")
		if tok == "" {
			tok = r.URL.Query().Get("token")
		}
		if tok != s.id.APIToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// uniquePath returns path, or path with " (n)" inserted before the extension
// if it already exists, so concurrent/repeat transfers never clobber files.
func uniquePath(path string) string {
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

// handlePairBegin: the local UI asks this device to generate a PIN.
func (s *Server) handlePairBegin(w http.ResponseWriter, _ *http.Request) {
	pin := pairs.GeneratePIN()
	writeJSON(w, map[string]string{"pin": pin})
}

// handlePairStatus: the UI asks whether a given device is paired.
func (s *Server) handlePairStatus(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	paired := pairs.IsPaired(id) != nil
	writeJSON(w, map[string]bool{"paired": paired})
}

// handlePairSubmit: the UI sends a PIN *to a remote peer* by proxying through
// this device's server (avoids CORS issues in the webview).
func (s *Server) handlePairSubmit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceID string `json:"device_id"`
		PIN      string `json:"pin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	peer, ok := s.reg.get(req.DeviceID)
	if !ok {
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}
	// POST the PIN to the remote peer's /api/pair/claim.
	payload := fmt.Sprintf(`{"pin":%q,"id":%q,"name":%q}`, req.PIN, s.id.ID, s.id.Name)
	resp, err := transferClient.Post(
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
	pairs.StoreKey(req.DeviceID, keyBytes)
	log.Printf("paired with %s (%s)", peer.Name, req.DeviceID)
	writeJSON(w, map[string]bool{"ok": true})
}

// handlePairClaim: a remote peer presents a PIN to pair with this device.
// This is a PUBLIC endpoint (no token required).
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
	key, ok := pairs.ClaimPIN(req.PIN, req.ID)
	if !ok {
		http.Error(w, "invalid or expired PIN", http.StatusForbidden)
		return
	}
	log.Printf("pairing accepted for %s (%s)", req.Name, req.ID)
	writeJSON(w, map[string]string{"key": hex.EncodeToString(key)})
}
