package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

const maxMsgsPerPeer = 100

// ChatMsg is a single chat message between this device and a peer.
type ChatMsg struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	From string `json:"from"` // sender device name
	Dir  string `json:"dir"`  // "sent" | "recv"
	Ts   int64  `json:"ts"`   // unix milliseconds
}

// ChatStore keeps per-peer message history in memory (last 100 per peer).
type ChatStore struct {
	mu         sync.RWMutex
	msgs       map[string][]ChatMsg // peerID → messages
	notifyPeer string               // last peer who messaged us
	notifyName string
}

func NewChatStore() *ChatStore {
	return &ChatStore{msgs: make(map[string][]ChatMsg)}
}

func (cs *ChatStore) Add(peerID string, msg ChatMsg) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.msgs[peerID] = append(cs.msgs[peerID], msg)
	if n := len(cs.msgs[peerID]); n > maxMsgsPerPeer {
		cs.msgs[peerID] = cs.msgs[peerID][n-maxMsgsPerPeer:]
	}
}

// Since returns messages for peerID with Ts > since.
func (cs *ChatStore) Since(peerID string, since int64) []ChatMsg {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	var out []ChatMsg
	for _, m := range cs.msgs[peerID] {
		if m.Ts > since {
			out = append(out, m)
		}
	}
	return out
}

func (cs *ChatStore) SetNotify(peerID, peerName string) {
	cs.mu.Lock()
	cs.notifyPeer = peerID
	cs.notifyName = peerName
	cs.mu.Unlock()
}

func (cs *ChatStore) GetNotify() (string, string) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.notifyPeer, cs.notifyName
}

func (cs *ChatStore) ClearNotify() {
	cs.mu.Lock()
	cs.notifyPeer = ""
	cs.notifyName = ""
	cs.mu.Unlock()
}

func chatMsgID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ── HTTP handlers ──────────────────────────────────────────────────────────

// handleChatInbox receives a message from a remote peer (public, no token).
// Body: {"from":"DeviceName","fromId":"device-id","text":"hello"}
func (s *Server) handleChatInbox(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		From   string `json:"from"`
		FromID string `json:"fromId"`
		Text   string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.Text == "" || body.FromID == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if body.From == "" {
		body.From = "Unknown"
	}
	msg := ChatMsg{ID: chatMsgID(), Text: body.Text, From: body.From, Dir: "recv", Ts: time.Now().UnixMilli()}
	s.Chat.Add(body.FromID, msg)
	s.Chat.SetNotify(body.FromID, body.From)

	snippet := body.Text
	if len(snippet) > 80 {
		snippet = snippet[:80] + "…"
	}
	go Notify("Message from "+body.From, snippet)
	w.WriteHeader(http.StatusOK)
}

// handleChatSend sends a message to a specific peer (protected).
// Body: {"peer":"device-id","text":"hello"}
func (s *Server) handleChatSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Peer string `json:"peer"`
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.Peer == "" || body.Text == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	peer, ok := s.Reg.Get(body.Peer)
	if !ok {
		http.Error(w, "peer not found", http.StatusNotFound)
		return
	}
	msg := ChatMsg{ID: chatMsgID(), Text: body.Text, From: s.ID.Name, Dir: "sent", Ts: time.Now().UnixMilli()}
	s.Chat.Add(body.Peer, msg)
	go s.deliverChat(peer, body.Text)
	WriteJSON(w, map[string]interface{}{"ok": true, "id": msg.ID})
}

// handleChatMessages returns messages for a peer since a timestamp (protected).
// GET /api/chat/messages?peer=<id>&since=<ts>
func (s *Server) handleChatMessages(w http.ResponseWriter, r *http.Request) {
	peerID := r.URL.Query().Get("peer")
	since := int64(0)
	if v := r.URL.Query().Get("since"); v != "" {
		fmt.Sscanf(v, "%d", &since)
	}
	msgs := s.Chat.Since(peerID, since)
	if msgs == nil {
		msgs = []ChatMsg{}
	}
	WriteJSON(w, msgs)
}

// handleChatNotify returns the last peer who sent us a message (for notification click).
func (s *Server) handleChatNotify(w http.ResponseWriter, r *http.Request) {
	id, name := s.Chat.GetNotify()
	WriteJSON(w, map[string]string{"peer": id, "name": name})
}

// handleChatNotifyAck clears the pending notification.
func (s *Server) handleChatNotifyAck(w http.ResponseWriter, _ *http.Request) {
	s.Chat.ClearNotify()
	w.WriteHeader(http.StatusOK)
}

// deliverChat POSTs a chat message to the peer's /chat-inbox.
func (s *Server) deliverChat(peer Peer, text string) {
	payload := fmt.Sprintf(`{"from":%q,"fromId":%q,"text":%q}`, s.ID.Name, s.ID.ID, text)
	req, err := http.NewRequest(http.MethodPost, "http://"+peer.Host+"/chat-inbox", strings.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("chat delivery to %s failed: %v", peer.Name, err)
		return
	}
	resp.Body.Close()
}
