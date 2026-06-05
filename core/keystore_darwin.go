//go:build darwin

package core

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"
)

const kcService = "SwiftDrop"
const kcAccount = "pair-master-key"

// getOrCreateMasterKey retrieves or generates a 32-byte master key in macOS Keychain.
func getOrCreateMasterKey() ([]byte, error) {
	out, err := exec.Command("security", "find-generic-password",
		"-s", kcService, "-a", kcAccount, "-w").Output()
	if err == nil {
		return hex.DecodeString(strings.TrimSpace(string(out)))
	}
	// Generate new master key and store it.
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	hexKey := hex.EncodeToString(key)
	if err := exec.Command("security", "add-generic-password",
		"-s", kcService, "-a", kcAccount, "-w", hexKey, "-U").Run(); err != nil {
		return nil, fmt.Errorf("keychain add: %w", err)
	}
	return key, nil
}

// platformEncrypt encrypts data with the Keychain-stored master key (AES-256-GCM).
func platformEncrypt(data []byte) ([]byte, error) {
	mk, err := getOrCreateMasterKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(mk)
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
	return gcm.Seal(nonce, nonce, data, nil), nil
}

// platformDecrypt decrypts data encrypted by platformEncrypt.
func platformDecrypt(data []byte) ([]byte, error) {
	mk, err := getOrCreateMasterKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(mk)
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
