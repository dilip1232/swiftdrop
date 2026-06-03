//go:build !darwin

package core

// platformEncrypt is a no-op on non-macOS: returns data as-is.
func platformEncrypt(data []byte) ([]byte, error) { return data, nil }

// platformDecrypt is a no-op on non-macOS: returns data as-is.
func platformDecrypt(data []byte) ([]byte, error) { return data, nil }
