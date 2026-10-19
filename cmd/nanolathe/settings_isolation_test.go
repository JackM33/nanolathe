package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// Command and input tests can reach the production write-all settings path
// and the mod library. Keep the entire suite away from the player's
// preferences and installed mods, including when the caller supplied
// NANOLATHE_SETTINGS. Individual tests may override these scratch paths with
// t.Setenv when they need independent persisted state.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "nanolathe-test-settings-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// An online room on an install with *.UFO archives asks the add-on
	// catalogue to name them (online_addons.go); a test never reaches the
	// network, so the catalogue is a loopback address nothing serves unless
	// the test sets its own.
	for name, value := range map[string]string{settings.EnvPath: filepath.Join(dir, "settings.json"), "XDG_DATA_HOME": filepath.Join(dir, "data"), "NANOLATHE_ADDON_CATALOG": "http://127.0.0.1:9/addons/manifest.json"} {
		if err := os.Setenv(name, value); err != nil {
			os.RemoveAll(dir)
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
