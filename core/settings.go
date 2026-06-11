package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Settings holds user-configurable preferences persisted to disk as JSON under
// the user's config directory.  Loaded lazily on first access so platform
// shells don't each need an explicit init call.
type settings struct {
	DownloadDir string `json:"downloadDir,omitempty"` // custom receive dir; "" = default
}

var (
	settingsMu     sync.RWMutex
	settingsState  settings
	settingsLoaded bool

	// settingsFile is the path to the settings JSON.  Overridable in tests.
	settingsFile = func() string { return ConfigFile("settings.json") }
)

// ensureSettingsLoaded reads the settings file once.  Safe to call repeatedly.
func ensureSettingsLoaded() {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	if settingsLoaded {
		return
	}
	settingsLoaded = true
	b, err := os.ReadFile(settingsFile())
	if err != nil {
		return
	}
	var s settings
	if json.Unmarshal(b, &s) == nil {
		settingsState = s
	}
}

// saveSettings persists the current in-memory settings.  Caller must hold no
// lock; it takes its own read lock to snapshot.
func saveSettings() {
	path := settingsFile()
	if path == "" {
		return
	}
	settingsMu.RLock()
	b, err := json.Marshal(settingsState)
	settingsMu.RUnlock()
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, b, 0o644)
}

// ConfiguredDownloadDir returns the user's custom download directory, or ""
// if none is set (meaning the default location is used).
func ConfiguredDownloadDir() string {
	ensureSettingsLoaded()
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	return settingsState.DownloadDir
}

// SetDownloadDir validates and persists a custom download directory.  Pass ""
// to clear the override and fall back to the default.  Returns the resolved
// directory now in effect (via DownloadDir) on success.
func SetDownloadDir(dir string) (string, error) {
	ensureSettingsLoaded()
	if dir != "" {
		if !filepath.IsAbs(dir) {
			return "", fmt.Errorf("download directory must be an absolute path")
		}
		dir = filepath.Clean(dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("cannot create directory: %w", err)
		}
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			return "", fmt.Errorf("not a directory: %s", dir)
		}
		if err := checkWritable(dir); err != nil {
			return "", fmt.Errorf("directory is not writable: %w", err)
		}
	}
	settingsMu.Lock()
	settingsState.DownloadDir = dir
	settingsMu.Unlock()
	saveSettings()
	return DownloadDir(), nil
}

// checkWritable confirms the process can create files in dir by writing and
// removing a temporary probe file.
func checkWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".swiftdrop-write-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return nil
}
