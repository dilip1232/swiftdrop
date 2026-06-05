//go:build !darwin && !windows

package core

// Notify is a no-op on unsupported platforms.
func Notify(title, message string) {}
