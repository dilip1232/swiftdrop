package core

import (
	"os"
	"path/filepath"
	"testing"
)

// withTempSettings points the settings store at a temp file and resets the
// lazy-load guard, so each test starts from a clean, isolated state.
func withTempSettings(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	settingsMu.Lock()
	prevFile := settingsFile
	settingsFile = func() string { return filepath.Join(dir, "settings.json") }
	settingsState = settings{}
	settingsLoaded = false
	settingsMu.Unlock()
	t.Cleanup(func() {
		settingsMu.Lock()
		settingsFile = prevFile
		settingsState = settings{}
		settingsLoaded = false
		settingsMu.Unlock()
	})
}

func TestSetDownloadDir_Persists(t *testing.T) {
	withTempSettings(t)
	target := filepath.Join(t.TempDir(), "incoming")

	resolved, err := SetDownloadDir(target)
	if err != nil {
		t.Fatalf("SetDownloadDir returned error: %v", err)
	}
	if resolved != filepath.Clean(target) {
		t.Errorf("resolved = %q, want %q", resolved, filepath.Clean(target))
	}
	if got := ConfiguredDownloadDir(); got != filepath.Clean(target) {
		t.Errorf("ConfiguredDownloadDir = %q, want %q", got, filepath.Clean(target))
	}
	if got := DownloadDir(); got != filepath.Clean(target) {
		t.Errorf("DownloadDir = %q, want %q", got, filepath.Clean(target))
	}

	// Force a reload from disk to confirm it was persisted.
	settingsMu.Lock()
	settingsState = settings{}
	settingsLoaded = false
	settingsMu.Unlock()
	if got := ConfiguredDownloadDir(); got != filepath.Clean(target) {
		t.Errorf("after reload ConfiguredDownloadDir = %q, want %q", got, filepath.Clean(target))
	}
}

func TestSetDownloadDir_ResetToDefault(t *testing.T) {
	withTempSettings(t)
	target := filepath.Join(t.TempDir(), "incoming")
	if _, err := SetDownloadDir(target); err != nil {
		t.Fatalf("setup SetDownloadDir: %v", err)
	}

	resolved, err := SetDownloadDir("")
	if err != nil {
		t.Fatalf("reset returned error: %v", err)
	}
	if got := ConfiguredDownloadDir(); got != "" {
		t.Errorf("ConfiguredDownloadDir after reset = %q, want empty", got)
	}
	if resolved != DefaultDownloadDir() {
		t.Errorf("resolved after reset = %q, want default %q", resolved, DefaultDownloadDir())
	}
}

func TestSetDownloadDir_RejectsRelativePath(t *testing.T) {
	withTempSettings(t)
	if _, err := SetDownloadDir("relative/path"); err == nil {
		t.Fatal("expected error for relative path, got nil")
	}
	if got := ConfiguredDownloadDir(); got != "" {
		t.Errorf("override should not be set after rejection, got %q", got)
	}
}

func TestSetDownloadDir_CreatesMissingDir(t *testing.T) {
	withTempSettings(t)
	target := filepath.Join(t.TempDir(), "a", "b", "c")

	if _, err := SetDownloadDir(target); err != nil {
		t.Fatalf("SetDownloadDir on missing nested dir: %v", err)
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Errorf("expected %q to be created as a directory (err=%v)", target, err)
	}
}

func TestDownloadDir_FallsBackWhenUnset(t *testing.T) {
	withTempSettings(t)
	if got := DownloadDir(); got != DefaultDownloadDir() {
		t.Errorf("DownloadDir with no override = %q, want default %q", got, DefaultDownloadDir())
	}
}
