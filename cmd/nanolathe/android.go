//go:build android

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync/atomic"
)

// androidFailure holds the error text that stopped an Android start, for the
// app to show instead of a black view.
var androidFailure atomic.Value

// StartAndroid runs the game shell inside the Android app, which calls it once
// before showing its EbitenView (docs/ANDROID.md §2). filesDir is the app's
// private storage and gameDir the copied Total Annihilation folder. It returns
// once the shell is starting; the window loop then belongs to the view.
//
// A command package cannot be imported, so tools/android/build.sh copies this
// package into an importable one for the bound Android package to call.
func StartAndroid(filesDir, gameDir string) error {
	// Go reports /sdcard as an Android home, which the app may not write. Every
	// per-user directory the engine resolves goes to private storage instead.
	for _, dir := range [...]struct{ env, name string }{
		{"HOME", "home"}, {"XDG_CONFIG_HOME", "config"}, {"XDG_DATA_HOME", "data"}, {"XDG_CACHE_HOME", "cache"},
	} {
		path := filepath.Join(filesDir, dir.name)
		if err := os.MkdirAll(path, 0o755); err != nil {
			return fmt.Errorf("nanolathe: android start: logical path %s, providers searched [app files], expected a writable directory: %w", path, err)
		}
		if err := os.Setenv(dir.env, path); err != nil {
			return err
		}
	}
	logPath := filepath.Join(filesDir, "nanolathe.log")
	log, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("nanolathe: android start: logical path %s, providers searched [app files], expected a writable log: %w", logPath, err)
	}
	// A crash ends the process before the app could ask why; keep its trace.
	if err := debug.SetCrashOutput(log, debug.CrashOptions{}); err != nil {
		fmt.Fprintf(log, "nanolathe: warning: crash traces will not be kept: %v\n", err)
	}
	opts, code, ok := mainOptions([]string{"--root", gameDir}, log)
	if !ok {
		return fmt.Errorf("nanolathe: android start: logical path <arguments>, providers searched [--root %s], expected parsed options, exit code %d", gameDir, code)
	}
	go func() {
		if runOptions(opts, log, log) != 0 {
			androidFailure.Store(logTail(logPath))
		}
	}()
	return nil
}

// AndroidFailure is the log tail of a start that failed, or "".
func AndroidFailure() string {
	text, _ := androidFailure.Load().(string)
	return text
}

// logTail is the end of the start-up log, where the failure was written.
func logTail(path string) string {
	const keep = 4 << 10
	data, err := os.ReadFile(path)
	if err != nil {
		return "nanolathe: the game stopped and its log could not be read: " + err.Error()
	}
	if len(data) > keep {
		data = data[len(data)-keep:]
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		}
	}
	return string(data)
}
