package core

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// TextBuffer holds the most recently received text snippet so the UI can poll
// it. Only the latest text is kept — this is a clipboard-style feature, not
// a chat history.
type TextBuffer struct {
	mu   sync.RWMutex
	Text string `json:"text"`
	From string `json:"from"`
	Ts   int64  `json:"ts"` // unix-ms; 0 = empty
}

func (b *TextBuffer) Set(text, from string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Text = text
	b.From = from
	b.Ts = time.Now().UnixMilli()
}

// Get returns the current buffer if newer than `since` (unix-ms).
func (b *TextBuffer) Get(since int64) (text, from string, ts int64) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.Ts > since {
		return b.Text, b.From, b.Ts
	}
	return "", "", 0
}

// handleTextInbox is the public endpoint a peer POSTs to when sharing text.
// Body: {"from":"DeviceName","text":"..."}
func (s *Server) handleTextInbox(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		From string `json:"from"`
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.Text == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if body.From == "" {
		body.From = "Unknown"
	}
	s.RecvText.Set(body.Text, body.From)
	// Show a notification on the receiver.
	snippet := body.Text
	if len(snippet) > 80 {
		snippet = snippet[:80] + "…"
	}
	go Notify("Text from "+body.From, snippet)
	w.WriteHeader(http.StatusOK)
}

// handleSendText is the protected endpoint the local UI calls to share text
// with all paired peers. Body: {"text":"..."}
func (s *Server) handleSendText(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.Text == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// Broadcast to every paired & reachable peer.
	sent := 0
	for _, id := range Pairs.PairedIDs() {
		if peer, ok := s.Reg.Get(id); ok {
			go s.deliverText(peer, body.Text)
			sent++
		}
	}
	WriteJSON(w, map[string]interface{}{"ok": true, "sent": sent})
}

// deliverText POSTs the text to the peer's /text-inbox.
func (s *Server) deliverText(peer Peer, text string) {
	payload := fmt.Sprintf(`{"from":%q,"text":%q}`, s.ID.Name, text)
	req, err := http.NewRequest(http.MethodPost, "http://"+peer.Host+"/text-inbox", strings.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

// handleReceivedText returns the most recently received text if newer than
// the `since` query parameter (unix-ms timestamp).
func (s *Server) handleReceivedText(w http.ResponseWriter, r *http.Request) {
	since := int64(0)
	if v := r.URL.Query().Get("since"); v != "" {
		fmt.Sscanf(v, "%d", &since)
	}
	text, from, ts := s.RecvText.Get(since)
	if ts == 0 {
		WriteJSON(w, map[string]interface{}{"ts": 0})
		return
	}
	WriteJSON(w, map[string]interface{}{"text": text, "from": from, "ts": ts})
}
