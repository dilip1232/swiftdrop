package core

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ── Pairing state ──────────────────────────────────────────────────────────

// PairStore manages shared keys for paired devices.  Keys are persisted to
// disk so pairing survives restarts.
type PairStore struct {
	mu   sync.RWMutex
	keys map[string][]byte // device-id -> 32-byte AES key

	// Pending pairing offer: this device generated a PIN and is waiting for
	// a peer to present it.
	pendPIN string    // 6-digit code shown to user
	pendKey []byte    // random 32-byte key behind the PIN
	pendExp time.Time // PIN expires after 60 s
}

func NewPairStore() *PairStore {
	ps := &PairStore{keys: make(map[string][]byte)}
	ps.Load()
	return ps
}

// IsPaired returns the shared key for deviceID, or nil if not paired.
func (ps *PairStore) IsPaired(id string) []byte {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.keys[id]
}

// GeneratePIN creates a new 6-digit pairing code + secret key.  The PIN is
// valid for 60 seconds and can be claimed exactly once.
func (ps *PairStore) GeneratePIN() string {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(1_000_000))
	ps.pendPIN = fmt.Sprintf("%06d", n.Int64())
	ps.pendKey = key
	ps.pendExp = time.Now().Add(60 * time.Second)
	return ps.pendPIN
}

// ClaimPIN verifies a peer's PIN attempt.  On success returns the shared key
// and stores it under peerID.
func (ps *PairStore) ClaimPIN(pin, peerID string) ([]byte, bool) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.pendPIN == "" || pin != ps.pendPIN || time.Now().After(ps.pendExp) {
		return nil, false
	}
	key := ps.pendKey
	ps.keys[peerID] = key
	ps.pendPIN = ""
	ps.pendKey = nil
	ps.Save()
	return key, true
}

// StoreKey persists a shared key received from a successful pairing (caller
// side).
func (ps *PairStore) StoreKey(peerID string, key []byte) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.keys[peerID] = key
	ps.Save()
}

func (ps *PairStore) Unpair(peerID string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	delete(ps.keys, peerID)
	ps.Save()
}

func (ps *PairStore) PairedIDs() []string {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	ids := make([]string, 0, len(ps.keys))
	for id := range ps.keys {
		ids = append(ids, id)
	}
	return ids
}

// ── persistence ──

func (ps *PairStore) path() string {
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "SwiftDrop", "paired-keys.json")
}

func (ps *PairStore) Save() {
	m := make(map[string]string, len(ps.keys))
	for id, k := range ps.keys {
		m[id] = hex.EncodeToString(k)
	}
	data, _ := json.Marshal(m)
	os.MkdirAll(filepath.Dir(ps.path()), 0o755)
	os.WriteFile(ps.path(), data, 0o600)
}

func (ps *PairStore) Load() {
	data, err := os.ReadFile(ps.path())
	if err != nil {
		return
	}
	var m map[string]string
	if json.Unmarshal(data, &m) != nil {
		return
	}
	for id, h := range m {
		if k, err := hex.DecodeString(h); err == nil && len(k) == 32 {
			ps.keys[id] = k
		}
	}
}

// ── Streaming AES-256-GCM encrypt / decrypt ────────────────────────────────
//
// Format:
//   [12-byte base nonce]
//   [4-byte big-endian chunk ciphertext length][ciphertext (plaintext + 16-byte GCM tag)] ...
//   [4-byte 0x00000000]  ← end marker
//
// Each chunk: up to 64 KiB of plaintext, encrypted with nonce = baseNonce XOR chunkIndex.

const ChunkPlain = 64 * 1024

// EncryptedSize returns the total byte count of the encrypted stream for a
// given plaintext size.  Format: 12-byte nonce + N*(4-byte len + ciphertext) + 4-byte end.
// Each ciphertext = min(ChunkPlain, remaining) + 16 (GCM tag).
func EncryptedSize(plain int64) int64 {
	if plain <= 0 {
		return -1
	}
	fullChunks := plain / ChunkPlain
	rem := plain % ChunkPlain
	n := fullChunks
	if rem > 0 {
		n++
	}
	// 12 (nonce) + n * (4 + chunkData + 16) + 4 (end marker)
	// fullChunks contribute (4 + ChunkPlain + 16) each
	// remainder contributes (4 + rem + 16) if rem > 0
	sz := int64(12) // base nonce
	sz += fullChunks * (4 + ChunkPlain + 16)
	if rem > 0 {
		sz += 4 + rem + 16
	}
	sz += 4 // end marker
	return sz
}

// EncryptStream reads plaintext from r and writes the encrypted stream to w.
func EncryptStream(w io.Writer, r io.Reader, key []byte) error {
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	baseNonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(baseNonce); err != nil {
		return err
	}
	if _, err := w.Write(baseNonce); err != nil {
		return err
	}

	buf := make([]byte, ChunkPlain)
	var idx uint64
	for {
		n, readErr := io.ReadFull(r, buf)
		if n > 0 {
			nonce := chunkNonce(baseNonce, idx)
			ct := gcm.Seal(nil, nonce, buf[:n], nil)
			if err := writeChunk(w, ct); err != nil {
				return err
			}
			idx++
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	// End marker
	return binary.Write(w, binary.BigEndian, uint32(0))
}

// DecryptStream reads the encrypted format from r and writes plaintext to w.
func DecryptStream(w io.Writer, r io.Reader, key []byte) error {
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	baseNonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(r, baseNonce); err != nil {
		return fmt.Errorf("read nonce: %w", err)
	}

	var idx uint64
	for {
		var cLen uint32
		if err := binary.Read(r, binary.BigEndian, &cLen); err != nil {
			return fmt.Errorf("read chunk header: %w", err)
		}
		if cLen == 0 {
			break // end marker
		}
		if cLen > ChunkPlain+uint32(gcm.Overhead())+1024 {
			return fmt.Errorf("chunk too large: %d", cLen)
		}
		ct := make([]byte, cLen)
		if _, err := io.ReadFull(r, ct); err != nil {
			return fmt.Errorf("read chunk: %w", err)
		}
		nonce := chunkNonce(baseNonce, idx)
		pt, err := gcm.Open(nil, nonce, ct, nil)
		if err != nil {
			return fmt.Errorf("decrypt chunk %d: %w", idx, err)
		}
		if _, err := w.Write(pt); err != nil {
			return err
		}
		idx++
	}
	return nil
}

func chunkNonce(base []byte, idx uint64) []byte {
	n := make([]byte, len(base))
	copy(n, base)
	// XOR the last 8 bytes with the counter
	for i := 0; i < 8; i++ {
		n[len(n)-1-i] ^= byte(idx >> (i * 8))
	}
	return n
}

func writeChunk(w io.Writer, data []byte) error {
	if err := binary.Write(w, binary.BigEndian, uint32(len(data))); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

// ── Global pair store ──

var Pairs *PairStore

func InitPairStore() {
	Pairs = NewPairStore()
	log.Printf("pairing: %d stored key(s)", len(Pairs.PairedIDs()))
}
