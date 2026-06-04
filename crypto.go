package core

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
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
	pendPIN   string    // 6-digit code shown to user
	pendKey   []byte    // random 32-byte key behind the PIN
	pendExp   time.Time // PIN expires after 60 s
	pendFails int       // consecutive wrong attempts; PIN voided after 3

	// QR-based pairing: a long random token replaces the short PIN.
	qrToken string    // 64-char hex token embedded in QR code
	qrKey   []byte    // 32-byte AES key behind the token
	qrExp   time.Time // token expires after 120 s

	// Pending SPAKE2 exchange: held between Phase 1 (exchange) and
	// Phase 2 (client confirmation).  Not committed until ConfirmPAKE.
	pakeSpakeKey []byte    // SPAKE2-derived shared key (for verifying client confirm)
	pakePairKey  []byte    // AES pairing key to store on confirmation
	pakePeerID   string    // peer that initiated the exchange
	pakeExp      time.Time // short 30 s window to confirm
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
	ps.pendFails = 0
	return ps.pendPIN
}

// ClaimPIN verifies a peer's PIN attempt.  On success returns the shared key
// and stores it under peerID.
func (ps *PairStore) ClaimPIN(pin, peerID string) ([]byte, bool) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.pendPIN == "" || time.Now().After(ps.pendExp) {
		return nil, false
	}
	if pin != ps.pendPIN {
		ps.pendFails++
		if ps.pendFails >= 3 {
			log.Printf("pairing: PIN invalidated after %d failed attempts", ps.pendFails)
			ps.pendPIN = ""
			ps.pendKey = nil
		}
		return nil, false
	}
	key := ps.pendKey
	ps.keys[peerID] = key
	ps.pendPIN = ""
	ps.pendKey = nil
	ps.pendFails = 0
	ps.Save()
	return key, true
}

// PendingPIN returns the current pending PIN (for SPAKE2 server side).
// Returns "" if no PIN is pending or it has expired.
func (ps *PairStore) PendingPIN() string {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	if ps.pendPIN == "" || time.Now().After(ps.pendExp) {
		return ""
	}
	return ps.pendPIN
}

// HoldPAKE stores SPAKE2 exchange state without committing the pairing.
// The pairing is only finalized when ConfirmPAKE succeeds.
// Does NOT consume the pending PIN (so retries with the correct PIN still work).
func (ps *PairStore) HoldPAKE(peerID string, spakeKey, pairKey []byte) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.pakeSpakeKey = spakeKey
	ps.pakePairKey = pairKey
	ps.pakePeerID = peerID
	ps.pakeExp = time.Now().Add(30 * time.Second)
}

// ConfirmPAKE verifies the client's confirmation MAC and finalizes the pairing.
// Returns true on success.  On failure increments the fail counter.
func (ps *PairStore) ConfirmPAKE(peerID string, clientConfirm []byte) bool {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.pakeSpakeKey == nil || ps.pakePeerID != peerID || time.Now().After(ps.pakeExp) {
		return false
	}
	expected := spakeConfirm(ps.pakeSpakeKey, "client")
	if !hmac.Equal(clientConfirm, expected) {
		ps.pendFails++
		if ps.pendFails >= 3 {
			log.Printf("pairing: PIN invalidated after %d failed PAKE attempts", ps.pendFails)
			ps.pendPIN = ""
			ps.pendKey = nil
		}
		// Clear pending PAKE state so they must re-exchange.
		ps.pakeSpakeKey = nil
		ps.pakePairKey = nil
		ps.pakePeerID = ""
		return false
	}
	// Success — commit the pairing.
	ps.keys[peerID] = ps.pakePairKey
	ps.pendPIN = ""
	ps.pendKey = nil
	ps.pendFails = 0
	ps.pakeSpakeKey = nil
	ps.pakePairKey = nil
	ps.pakePeerID = ""
	// Also consume QR token if this was a QR-based pairing.
	ps.qrToken = ""
	ps.qrKey = nil
	ps.Save()
	return true
}

// ── QR-based pairing ──

// GenerateQRToken creates a one-time pairing token (64 hex chars = 32 bytes
// of entropy) and a corresponding AES key. The token is valid for 120 seconds.
func (ps *PairStore) GenerateQRToken() string {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	tok := make([]byte, 32)
	if _, err := rand.Read(tok); err != nil {
		panic(err)
	}
	ps.qrToken = hex.EncodeToString(tok)
	ps.qrKey = key
	ps.qrExp = time.Now().Add(120 * time.Second)
	return ps.qrToken
}

// QRTokenAndKey returns the current QR token and its AES key if still valid.
// Does NOT consume the token — that happens in ConfirmPAKE.
func (ps *PairStore) QRTokenAndKey() (string, []byte) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	if ps.qrToken == "" || time.Now().After(ps.qrExp) {
		return "", nil
	}
	return ps.qrToken, ps.qrKey
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
	// Encrypt at rest (macOS: AES-256-GCM with Keychain master key; others: no-op).
	enc, err := platformEncrypt(data)
	if err != nil {
		log.Printf("pairing: encrypt-at-rest failed, saving plaintext: %v", err)
		enc = data
	}
	os.MkdirAll(filepath.Dir(ps.path()), 0o755)
	os.WriteFile(ps.path(), enc, 0o600)
}

func (ps *PairStore) Load() {
	raw, err := os.ReadFile(ps.path())
	if err != nil {
		return
	}
	// Decrypt at rest; if it fails, try as plain JSON (migration from old format).
	data, err := platformDecrypt(raw)
	if err != nil {
		data = raw
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

const ChunkPlain = 256 * 1024

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

// EncryptStream reads plaintext from r and writes the encrypted v2 stream to w.
// Chunk index is bound as GCM additional authenticated data (AAD) to prevent
// chunk reordering/splicing attacks.
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
	nonce := make([]byte, gcm.NonceSize())
	// Pre-allocate ciphertext buffer: max plaintext + GCM tag.
	ctBuf := make([]byte, 0, ChunkPlain+gcm.Overhead())
	aad := make([]byte, 8)
	var idx uint64
	for {
		n, readErr := io.ReadFull(r, buf)
		if n > 0 {
			chunkNonceInPlace(nonce, baseNonce, idx)
			binary.BigEndian.PutUint64(aad, idx)
			ct := gcm.Seal(ctBuf[:0], nonce, buf[:n], aad)
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

// DecryptStream reads the encrypted v2 format (with chunk-index AAD) from r
// and writes plaintext to w. Use DecryptStreamV1 for the legacy format.
func DecryptStream(w io.Writer, r io.Reader, key []byte) error {
	return decryptStreamImpl(w, r, key, true)
}

// DecryptStreamV1 reads the legacy encrypted format (no AAD) from r and writes
// plaintext to w. Used for backward compatibility with older peers.
func DecryptStreamV1(w io.Writer, r io.Reader, key []byte) error {
	return decryptStreamImpl(w, r, key, false)
}

func decryptStreamImpl(w io.Writer, r io.Reader, key []byte, useAAD bool) error {
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

	nonce := make([]byte, gcm.NonceSize())
	// Pre-allocate read buffer for ciphertext chunks.
	ctBuf := make([]byte, ChunkPlain+gcm.Overhead()+1024)
	// Pre-allocate plaintext output buffer.
	ptBuf := make([]byte, 0, ChunkPlain)
	aad := make([]byte, 8)
	var idx uint64
	for {
		var cLen uint32
		if err := binary.Read(r, binary.BigEndian, &cLen); err != nil {
			return fmt.Errorf("read chunk header: %w", err)
		}
		if cLen == 0 {
			break // end marker
		}
		if int(cLen) > len(ctBuf) {
			return fmt.Errorf("chunk too large: %d", cLen)
		}
		if _, err := io.ReadFull(r, ctBuf[:cLen]); err != nil {
			return fmt.Errorf("read chunk: %w", err)
		}
		chunkNonceInPlace(nonce, baseNonce, idx)
		var chunkAAD []byte
		if useAAD {
			binary.BigEndian.PutUint64(aad, idx)
			chunkAAD = aad
		}
		pt, err := gcm.Open(ptBuf[:0], nonce, ctBuf[:cLen], chunkAAD)
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

// chunkNonceInPlace fills dst from base, then XORs the counter into the low 8 bytes.
func chunkNonceInPlace(dst, base []byte, idx uint64) {
	copy(dst, base)
	for i := 0; i < 8; i++ {
		dst[len(dst)-1-i] ^= byte(idx >> (i * 8))
	}
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

// ── Replay cache (#33) ──────────────────────────────────────────────────────

// ReplayCache prevents HMAC replay attacks by remembering recent auth
// signatures for the duration of the timestamp window (5 min + 1 min buffer).
type ReplayCache struct {
	mu   sync.Mutex
	seen map[string]time.Time // hmac-hex → expiry
}

func NewReplayCache() *ReplayCache {
	rc := &ReplayCache{seen: make(map[string]time.Time)}
	go rc.cleanup()
	return rc
}

// Check returns true if this HMAC has not been seen before (first use).
// Returns false on replay.
func (rc *ReplayCache) Check(hmacHex string) bool {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if _, ok := rc.seen[hmacHex]; ok {
		return false
	}
	rc.seen[hmacHex] = time.Now().Add(6 * time.Minute)
	return true
}

func (rc *ReplayCache) cleanup() {
	ticker := time.NewTicker(time.Minute)
	for range ticker.C {
		rc.mu.Lock()
		now := time.Now()
		for k, exp := range rc.seen {
			if now.After(exp) {
				delete(rc.seen, k)
			}
		}
		rc.mu.Unlock()
	}
}

// AESGCMWrap encrypts plaintext under a 32-byte key (one-shot AES-256-GCM).
// Returns nonce || ciphertext.
func AESGCMWrap(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// AESGCMUnwrap decrypts data produced by AESGCMWrap.
func AESGCMUnwrap(key, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(data) < ns {
		return nil, fmt.Errorf("ciphertext too short")
	}
	return gcm.Open(nil, data[:ns], data[ns:], nil)
}

// ── SPAKE2 over P-256 (PAKE for pairing) ────────────────────────────────────
//
// Zero-knowledge PIN-based pairing.  The PIN never crosses the wire — both
// sides prove knowledge of it through blinded elliptic-curve Diffie-Hellman.
//
// Protocol (single HTTP round trip):
//   Client → Server:  pake_msg = Marshal(x·G + w·M)
//   Server → Client:  pake_msg = Marshal(y·G + w·N), confirm, encrypted_key
//   Both derive:      K = xy·G  →  sharedKey = SHA-256(K || msgA || msgB)
//   Server wraps the AES pairing key with sharedKey.
//
// M, N are nothing-up-my-sleeve points derived via hash-to-curve.

var (
	spakeP256        = elliptic.P256()
	spakeMx, spakeMy *big.Int // client blinding point
	spakeNx, spakeNy *big.Int // server blinding point
)

func init() {
	spakeMx, spakeMy = spakeHashToPoint("SwiftDrop-SPAKE2-point-M")
	spakeNx, spakeNy = spakeHashToPoint("SwiftDrop-SPAKE2-point-N")
}

// spakeHashToPoint maps a seed to a point on P-256 via try-and-increment.
func spakeHashToPoint(seed string) (*big.Int, *big.Int) {
	curve := spakeP256
	p := curve.Params().P
	for i := uint32(0); ; i++ {
		h := sha256.New()
		h.Write([]byte(seed))
		var ctr [4]byte
		binary.BigEndian.PutUint32(ctr[:], i)
		h.Write(ctr[:])
		xBytes := h.Sum(nil)
		x := new(big.Int).SetBytes(xBytes)
		x.Mod(x, p)

		// P-256: y² = x³ − 3x + b
		x3 := new(big.Int).Mul(x, x)
		x3.Mul(x3, x)
		x3.Mod(x3, p)
		threeX := new(big.Int).Mul(big.NewInt(3), x)
		threeX.Mod(threeX, p)
		rhs := new(big.Int).Sub(x3, threeX)
		rhs.Add(rhs, curve.Params().B)
		rhs.Mod(rhs, p)

		y := new(big.Int).ModSqrt(rhs, p)
		if y != nil && curve.IsOnCurve(x, y) {
			return x, y
		}
	}
}

// spakePINScalar converts a PIN string to a non-zero scalar mod n.
func spakePINScalar(pin string) *big.Int {
	h := sha256.Sum256([]byte("SwiftDrop-SPAKE2-password:" + pin))
	w := new(big.Int).SetBytes(h[:])
	n1 := new(big.Int).Sub(spakeP256.Params().N, big.NewInt(1))
	w.Mod(w, n1)
	w.Add(w, big.NewInt(1))
	return w
}

func spakeDeriveKey(Kx, Ky *big.Int, msgA, msgB []byte) []byte {
	h := sha256.New()
	h.Write(elliptic.Marshal(spakeP256, Kx, Ky))
	h.Write(msgA)
	h.Write(msgB)
	return h.Sum(nil)
}

func spakeConfirm(key []byte, role string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("SwiftDrop-SPAKE2-confirm-" + role))
	return mac.Sum(nil)
}

// SPAKE2ClientState holds ephemeral state for the SPAKE2 client (PIN submitter).
type SPAKE2ClientState struct {
	x    *big.Int
	w    *big.Int
	msgA []byte
}

// SPAKE2ClientStart begins the client side. Returns the message to send and
// ephemeral state needed to finish.
func SPAKE2ClientStart(pin string) (msgA []byte, state *SPAKE2ClientState, err error) {
	curve := spakeP256
	w := spakePINScalar(pin)

	x, err := rand.Int(rand.Reader, curve.Params().N)
	if err != nil {
		return nil, nil, err
	}
	if x.Sign() == 0 {
		x.SetInt64(1)
	}

	// X = x·G + w·M
	xGx, xGy := curve.ScalarBaseMult(x.Bytes())
	wMx, wMy := curve.ScalarMult(spakeMx, spakeMy, w.Bytes())
	Xx, Xy := curve.Add(xGx, xGy, wMx, wMy)

	msgA = elliptic.Marshal(curve, Xx, Xy)
	return msgA, &SPAKE2ClientState{x: x, w: w, msgA: msgA}, nil
}

// Finish processes the server's response and returns the SPAKE2 shared key.
// Returns an error if the server's confirmation MAC is invalid (wrong PIN).
func (c *SPAKE2ClientState) Finish(msgB, serverConfirm []byte) ([]byte, error) {
	curve := spakeP256

	Yx, Yy := elliptic.Unmarshal(curve, msgB)
	if Yx == nil {
		return nil, fmt.Errorf("invalid SPAKE2 server message")
	}

	// K = x · (Y − w·N)
	wNx, wNy := curve.ScalarMult(spakeNx, spakeNy, c.w.Bytes())
	negY := new(big.Int).Sub(curve.Params().P, wNy) // negate y-coord
	unbX, unbY := curve.Add(Yx, Yy, wNx, negY)
	Kx, Ky := curve.ScalarMult(unbX, unbY, c.x.Bytes())

	key := spakeDeriveKey(Kx, Ky, c.msgA, msgB)

	expected := spakeConfirm(key, "server")
	if !hmac.Equal(serverConfirm, expected) {
		return nil, fmt.Errorf("SPAKE2 confirmation failed — wrong PIN?")
	}
	return key, nil
}

// SPAKE2ServerFinish processes the client's SPAKE2 message and computes the
// server's response.  Returns msgB, confirmation MAC, and the shared key.
func SPAKE2ServerFinish(pin string, msgA []byte) (msgB, confirm, sharedKey []byte, err error) {
	curve := spakeP256
	w := spakePINScalar(pin)

	Ax, Ay := elliptic.Unmarshal(curve, msgA)
	if Ax == nil {
		return nil, nil, nil, fmt.Errorf("invalid SPAKE2 client message")
	}

	y, err := rand.Int(rand.Reader, curve.Params().N)
	if err != nil {
		return nil, nil, nil, err
	}
	if y.Sign() == 0 {
		y.SetInt64(1)
	}

	// Y = y·G + w·N
	yGx, yGy := curve.ScalarBaseMult(y.Bytes())
	wNx, wNy := curve.ScalarMult(spakeNx, spakeNy, w.Bytes())
	Yx, Yy := curve.Add(yGx, yGy, wNx, wNy)
	msgB = elliptic.Marshal(curve, Yx, Yy)

	// K = y · (X − w·M)
	wMx, wMy := curve.ScalarMult(spakeMx, spakeMy, w.Bytes())
	negY := new(big.Int).Sub(curve.Params().P, wMy)
	unbX, unbY := curve.Add(Ax, Ay, wMx, negY)
	Kx, Ky := curve.ScalarMult(unbX, unbY, y.Bytes())

	sharedKey = spakeDeriveKey(Kx, Ky, msgA, msgB)
	confirm = spakeConfirm(sharedKey, "server")
	return msgB, confirm, sharedKey, nil
}
