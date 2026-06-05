//go:build !darwin && !windows

package core

// platformEncrypt is a no-op on Linux/Android: returns data as-is.
// macOS uses Keychain, Windows uses DPAPI.
func platformEncrypt(data []byte) ([]byte, error) { return data, nil }

// platformDecrypt is a no-op on Linux/Android: returns data as-is.
func platformDecrypt(data []byte) ([]byte, error) { return data, nil }
