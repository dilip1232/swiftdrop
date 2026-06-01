package main

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Transfer is the live state of one outbound file, polled by the UI for
// progress. Sent is updated atomically by the streaming copy.
type Transfer struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Sent   int64  `json:"sent"`
	Status string `json:"status"` // "sending" | "done" | "error" | "canceled"
	Peer   string `json:"peer"`
	Dir    string `json:"dir"` // "send" | "recv"
	Err    string `json:"err,omitempty"`

	cancel context.CancelFunc // aborts the in-flight request
}

// tracker holds recent + active transfers so the UI can render progress even
// after the drawer was closed and reopened mid-transfer.
type tracker struct {
	mu    sync.RWMutex
	items map[string]*Transfer
	order []string
	seq   int64
}

func newTracker() *tracker {
	return &tracker{items: make(map[string]*Transfer)}
}

func (t *tracker) start(name string, size int64, peer, dir string) *Transfer {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seq++
	id := time.Now().Format("150405") + "-" + itoa(t.seq)
	tr := &Transfer{ID: id, Name: name, Size: size, Status: "sending", Peer: peer, Dir: dir}
	t.items[id] = tr
	t.order = append(t.order, id)
	t.trimLocked()
	return tr
}

func (t *tracker) setCancel(tr *Transfer, c context.CancelFunc) {
	t.mu.Lock()
	tr.cancel = c
	t.mu.Unlock()
}

func (t *tracker) finish(tr *Transfer, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if tr.Status == "canceled" {
		return // user already canceled; keep that status
	}
	if err != nil {
		tr.Status = "error"
		tr.Err = err.Error()
	} else {
		tr.Status = "done"
		if tr.Size >= 0 {
			atomic.StoreInt64(&tr.Sent, tr.Size)
		}
	}
}

// cancel aborts an in-flight transfer; no-op if already finished.
func (t *tracker) cancel(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	tr := t.items[id]
	if tr != nil && tr.Status == "sending" {
		if tr.cancel != nil {
			tr.cancel()
		}
		tr.Status = "canceled"
	}
}

// clearFinished drops everything that isn't actively sending.
func (t *tracker) clearFinished() {
	t.mu.Lock()
	defer t.mu.Unlock()
	kept := t.order[:0:0]
	for _, id := range t.order {
		if tr := t.items[id]; tr != nil && tr.Status == "sending" {
			kept = append(kept, id)
		} else {
			delete(t.items, id)
		}
	}
	t.order = kept
}

// list returns a snapshot in insertion order (oldest first).
func (t *tracker) list() []Transfer {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]Transfer, 0, len(t.order))
	for _, id := range t.order {
		tr := t.items[id]
		out = append(out, Transfer{
			ID: tr.ID, Name: tr.Name, Size: tr.Size,
			Sent: atomic.LoadInt64(&tr.Sent), Status: tr.Status,
			Peer: tr.Peer, Dir: tr.Dir, Err: tr.Err,
		})
	}
	return out
}

// trimLocked caps history so the map can't grow unbounded over a long session.
func (t *tracker) trimLocked() {
	const max = 50
	for len(t.order) > max {
		old := t.order[0]
		t.order = t.order[1:]
		delete(t.items, old)
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
