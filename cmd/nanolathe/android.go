//go:build android

package main

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"github.com/nanolathe-gg/nanolathe/internal/platform/ebitenapp"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// androidFailure holds the error text that stopped an Android start, for the
// app to show instead of a black view.
var androidFailure atomic.Value

// StartAndroid runs the game shell inside the Android app, which calls it once
// after showing its EbitenView (docs/ANDROID.md §2). filesDir is the app's
// private storage, gameDir the copied Total Annihilation folder, and
// screenW x screenH the display in physical pixels, long side first. It
// returns once the shell is starting; the window loop then belongs to the
// view.
//
// A command package cannot be imported, so tools/android/build.sh copies this
// package into an importable one for the bound Android package to call.
func StartAndroid(filesDir, gameDir string, screenW, screenH int) error {
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
	writeGameFolderReport(log, gameDir)
	reportMissingGadgetArt(log)
	if err := seedAndroidDisplay(filesDir, screenW, screenH); err != nil {
		fmt.Fprintf(log, "nanolathe: warning: the battle size was not fitted to the screen: %v\n", err)
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

// writeGameFolderReport lists the copied game folder at the head of the log,
// so a shared log shows which archives the player's install supplied.
func writeGameFolderReport(w io.Writer, gameDir string) {
	const most = 200
	listed := 0
	_ = filepath.WalkDir(gameDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if listed == most {
			fmt.Fprintf(w, "nanolathe: game folder: more files not listed\n")
			return filepath.SkipAll
		}
		listed++
		rel, _ := filepath.Rel(gameDir, path)
		size := int64(-1)
		if info, err := d.Info(); err == nil {
			size = info.Size()
		}
		fmt.Fprintf(w, "nanolathe: game folder: %s (%d bytes)\n", rel, size)
		return nil
	})
}

// reportMissingGadgetArt logs each per-gadget picture the window builder
// could not resolve, once per file, for diagnosing blank build buttons.
func reportMissingGadgetArt(w io.Writer) {
	var mu sync.Mutex
	seen := map[string]bool{}
	gadgetArtMissing = func(logical string, err error) {
		mu.Lock()
		defer mu.Unlock()
		if seen[logical] {
			return
		}
		seen[logical] = true
		fmt.Fprintf(w, "nanolathe: build picture missing: logical path %s: %v\n", logical, err)
	}
}

// androidDisplaySeeded marks that the battle size was fitted to the screen
// once, so a size the player later picks in Graphics is kept.
const androidDisplaySeeded = "display-fitted"

// seedAndroidDisplay sets the saved battle size, once per install, to the
// screen's shape at the minimum battle height (docs/ANDROID.md §2). The
// battle then fills the screen without letterbox bars, and the sidebar and
// bars are as large as the authored chrome can be drawn. Menus keep their
// authored 640x480 canvas.
func seedAndroidDisplay(filesDir string, screenW, screenH int) error {
	marker := filepath.Join(filesDir, androidDisplaySeeded)
	if _, err := os.Stat(marker); err == nil || screenW <= 0 || screenH <= 0 {
		return nil
	}
	long, short := max(screenW, screenH), min(screenW, screenH)
	s, err := settings.Load()
	if err != nil {
		return err
	}
	s.Display.Height = settings.MinDisplaymodeHeight
	s.Display.Width = max(settings.MinDisplaymodeWidth, int(math.Round(float64(settings.MinDisplaymodeHeight)*float64(long)/float64(short))))
	if err := s.Save(); err != nil {
		return err
	}
	return os.WriteFile(marker, nil, 0o644)
}

// AndroidTypeText and AndroidPressKey pass the soft keyboard's text and
// editing keys to the input poll (docs/ANDROID.md §3).
func AndroidTypeText(text string) { ebitenapp.InjectText(text) }

func AndroidPressKey(name string) bool { return ebitenapp.InjectKey(name) }

// AndroidSetShift holds Shift while the app's Shift button is pressed.
func AndroidSetShift(held bool) { ebitenapp.SetHostShift(held) }

// AndroidEditorServices rises while a text box is capturing input, which is
// when the app shows the soft keyboard (docs/ANDROID.md §3).
func AndroidEditorServices() int64 { return ui.EditorServices() }

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
