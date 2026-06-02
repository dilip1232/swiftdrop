package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// Identity describes this device on the network.
type Identity struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	Port     int    `json:"port"`
	APIToken string `json:"-"` // local-only secret; never sent to peers
}

// Peer is another SwiftDrop device, found via mDNS or added by hand.
type Peer struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	Host     string `json:"host"`   // ip:port reachable for transfers
	Manual   bool   `json:"manual"` // true if added by IP rather than discovered
}

// PeerRegistry is a concurrency-safe set of peers. Peers found via mDNS live
// in `peers` (pruned each browse cycle); peers added by hand live in `manual`
// (kept until explicitly removed) so the two sources don't clobber each other.
type PeerRegistry struct {
	mu       sync.RWMutex
	peers    map[string]Peer
	manual   map[string]Peer
	lastSeen map[string]time.Time // mDNS peers: when last observed
	ignore   map[string]time.Time // ids briefly hidden after the user removed them
	known    map[string]Peer      // every device ever seen; probed to auto-(re)appear
}

func NewPeerRegistry() *PeerRegistry {
	return &PeerRegistry{
		peers:    make(map[string]Peer),
		manual:   make(map[string]Peer),
		lastSeen: make(map[string]time.Time),
		ignore:   make(map[string]time.Time),
		known:    make(map[string]Peer),
	}
}

// Remember records a device (keyed by id) so the prober can keep it visible and
// re-find it after a restart. Keyed by id so an IP change just updates the host.
func (r *PeerRegistry) Remember(p Peer) {
	r.mu.Lock()
	prev, ok := r.known[p.ID]
	r.known[p.ID] = Peer{ID: p.ID, Name: p.Name, Platform: p.Platform, Host: p.Host}
	changed := !ok || prev.Host != p.Host || prev.Name != p.Name
	r.mu.Unlock()
	if changed {
		r.SaveKnown()
	}
}

func (r *PeerRegistry) KnownList() []Peer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Peer, 0, len(r.known))
	for _, p := range r.known {
		out = append(out, p)
	}
	return out
}

func (r *PeerRegistry) IsManual(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.manual[id]
	return ok
}

func (r *PeerRegistry) IsIgnored(id string) bool {
	r.mu.RLock()
	until, ok := r.ignore[id]
	r.mu.RUnlock()
	if !ok {
		return false
	}
	if time.Now().After(until) {
		r.mu.Lock()
		delete(r.ignore, id)
		r.mu.Unlock()
		return false
	}
	return true
}

// RemoveDevice permanently removes a peer from all sources: the active peer
// list, manual peers, and the known-devices cache. The device will only
// reappear if mDNS re-discovers it or the user adds it by IP again.
func (r *PeerRegistry) RemoveDevice(id string) {
	r.mu.Lock()
	delete(r.peers, id)
	delete(r.lastSeen, id)
	delete(r.known, id)
	wasManual := false
	if _, ok := r.manual[id]; ok {
		wasManual = true
		delete(r.manual, id)
	}
	r.ignore[id] = time.Now().Add(30 * time.Second)
	r.mu.Unlock()
	if wasManual {
		r.SaveManual()
	}
	r.SaveKnown()
}

// ClearMDNS drops all discovered peers (keeps manual). Used on network change.
func (r *PeerRegistry) ClearMDNS() {
	r.mu.Lock()
	r.peers = make(map[string]Peer)
	r.lastSeen = make(map[string]time.Time)
	r.mu.Unlock()
}

func (r *PeerRegistry) AddManual(p Peer) {
	r.mu.Lock()
	p.Manual = true
	r.manual[p.ID] = p
	r.peers[p.ID] = p // show immediately (don't wait for the next probe)
	r.lastSeen[p.ID] = time.Now()
	delete(r.ignore, p.ID) // explicit add overrides a prior removal
	r.mu.Unlock()
	r.SaveManual()
	r.Remember(p)
}

func (r *PeerRegistry) RemoveManual(id string) {
	r.mu.Lock()
	delete(r.manual, id)
	r.mu.Unlock()
	r.SaveManual()
}

func ManualPeersPath() string { return ConfigFile("manual-peers.json") }
func KnownPeersPath() string  { return ConfigFile("known-peers.json") }

func ConfigFile(name string) string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "SwiftDrop", name)
}

func (r *PeerRegistry) SaveKnown() {
	path := KnownPeersPath()
	if path == "" {
		return
	}
	r.mu.RLock()
	list := make([]Peer, 0, len(r.known))
	for _, p := range r.known {
		list = append(list, p)
	}
	r.mu.RUnlock()
	if b, err := json.Marshal(list); err == nil {
		os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, b, 0o644)
	}
}

func (r *PeerRegistry) LoadKnown() {
	b, err := os.ReadFile(KnownPeersPath())
	if err != nil {
		return
	}
	var list []Peer
	if json.Unmarshal(b, &list) != nil {
		return
	}
	r.mu.Lock()
	for _, p := range list {
		r.known[p.ID] = p
	}
	r.mu.Unlock()
}

// SaveManual / LoadManual persist hand-added peers so they survive restarts
// (a manually trusted device shouldn't vanish just because the app relaunched).
func (r *PeerRegistry) SaveManual() {
	path := ManualPeersPath()
	if path == "" {
		return
	}
	r.mu.RLock()
	list := make([]Peer, 0, len(r.manual))
	for _, p := range r.manual {
		list = append(list, p)
	}
	r.mu.RUnlock()
	if b, err := json.Marshal(list); err == nil {
		os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, b, 0o644)
	}
}

func (r *PeerRegistry) LoadManual() {
	path := ManualPeersPath()
	if path == "" {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var list []Peer
	if json.Unmarshal(b, &list) != nil {
		return
	}
	r.mu.Lock()
	for _, p := range list {
		p.Manual = true
		r.manual[p.ID] = p
		if _, ok := r.known[p.ID]; !ok {
			r.known[p.ID] = Peer{ID: p.ID, Name: p.Name, Platform: p.Platform, Host: p.Host}
		}
	}
	r.mu.Unlock()
}

func (r *PeerRegistry) Upsert(p Peer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if until, ok := r.ignore[p.ID]; ok {
		if time.Now().Before(until) {
			return // recently removed by the user; stay hidden
		}
		delete(r.ignore, p.ID)
	}
	r.peers[p.ID] = p
	r.lastSeen[p.ID] = time.Now()
	prev, ok := r.known[p.ID]
	if !ok || prev.Host != p.Host || prev.Name != p.Name {
		r.known[p.ID] = Peer{ID: p.ID, Name: p.Name, Platform: p.Platform, Host: p.Host}
		go r.SaveKnown()
	}
}

func (r *PeerRegistry) Remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.peers, id)
	delete(r.lastSeen, id)
}

// PruneStale drops mDNS peers not seen within maxAge. Using a last-seen window
// (rather than requiring a hit every browse sweep) stops peers from flickering
// in and out when an individual mDNS sweep happens to miss them.
func (r *PeerRegistry) PruneStale(maxAge time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := time.Now().Add(-maxAge)
	for id, seen := range r.lastSeen {
		if seen.Before(cutoff) {
			delete(r.peers, id)
			delete(r.lastSeen, id)
		}
	}
}

func (r *PeerRegistry) Get(id string) (Peer, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if p, ok := r.peers[id]; ok {
		return p, true
	}
	p, ok := r.manual[id]
	return p, ok
}

// List returns the currently-reachable devices. The prober keeps this set in
// sync with reachability, so unreachable devices are hidden automatically and
// reachable ones (re)appear on their own.
func (r *PeerRegistry) List() []Peer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Peer, 0, len(r.peers))
	for _, p := range r.peers {
		out = append(out, p)
	}
	return out
}

// LoadOrCreateIdentity returns a stable device identity, persisting the
// generated ID under the user's config directory so peers recognise this
// device across restarts.
func LoadOrCreateIdentity(port int) Identity {
	name, _ := os.Hostname()
	if name == "" {
		switch runtime.GOOS {
		case "darwin":
			name = "Mac"
		case "windows":
			name = "PC"
		default:
			name = "Device"
		}
	}
	return Identity{
		ID:       loadOrCreateID(),
		Name:     name,
		Platform: runtime.GOOS,
		Port:     port,
		APIToken: RandomID(), // fresh each launch; only the local UI knows it
	}
}

func loadOrCreateID() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return RandomID()
	}
	appDir := filepath.Join(dir, "SwiftDrop")
	idPath := filepath.Join(appDir, "device-id")

	if b, err := os.ReadFile(idPath); err == nil && len(b) >= 8 {
		return string(b)
	}

	id := RandomID()
	if err := os.MkdirAll(appDir, 0o755); err == nil {
		_ = os.WriteFile(idPath, []byte(id), 0o644)
	}
	return id
}

func RandomID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "swiftdrop-unknown"
	}
	return hex.EncodeToString(b)
}

// DownloadDir is where received files land. Created on demand.
func DownloadDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	dir := filepath.Join(home, "Downloads", "SwiftDrop")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}
