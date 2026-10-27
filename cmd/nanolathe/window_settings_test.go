package main

import (
	"io"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/platform/ebitenapp"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

func TestFullscreenFlagOverridesOnlyWhenPresent(t *testing.T) {
	for _, tc := range []struct {
		args        []string
		saved, want bool
	}{
		{nil, true, true}, {nil, false, false},
		{[]string{"--fullscreen"}, false, true},
		{[]string{"--fullscreen=false"}, true, false},
	} {
		opts, err := parseFlags(tc.args, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if got := startupFullscreen(opts, tc.saved); got != tc.want {
			t.Fatalf("%v saved=%v: got %v", tc.args, tc.saved, got)
		}
	}
}

func TestWindowPreferencesPreserveShellAndDirectSettings(t *testing.T) {
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	prefs := settings.Defaults()
	prefs.Fullscreen = true
	prefs.Display.Width, prefs.Display.Height = 1024, 768
	if err := prefs.Save(); err != nil {
		t.Fatal(err)
	}
	shell := &gameShell{}
	shell.applySettings(prefs)
	shell.settingsWritable = true
	options := shell.windowOptions()
	if !options.Fullscreen {
		t.Fatal("saved fullscreen lost")
	}
	if w, h := options.WindowSize(); w != 1024 || h != 768 {
		t.Fatalf("window %dx%d", w, h)
	}
	shell.display.Width, shell.display.Height = 800, 600
	if w, h := options.WindowSize(); w != 1024 || h != 768 {
		t.Fatal("pending slider edit resized the window")
	}
	options.FullscreenChanged(false)
	saved, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Fullscreen || saved.Display.Width != 1024 {
		t.Fatal("fullscreen toggle persisted the pending resolution")
	}
	shell.commitWindowSize()
	shell.saveSettings()
	if w, h := options.WindowSize(); w != 800 || h != 600 {
		t.Fatal("committed resolution did not resize the window")
	}
	saved, err = settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Display.Width != 800 || saved.Display.Height != 600 {
		t.Fatal("committed resolution was not saved")
	}
	saved.ScrollSpeed = 42
	if err := saved.Save(); err != nil {
		t.Fatal(err)
	}
	saveFullscreenSetting(true)
	saved, err = settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Fullscreen || saved.ScrollSpeed != 42 {
		t.Fatal("fullscreen overwrote a later battle setting")
	}
}

func TestEntryZoomAppliesToTheSelectedCanvas(t *testing.T) {
	for i := 0; i < 2; i++ {
		// The entry camera has already adopted the selected canvas by the time
		// the start-up zoom factor is applied [07 "The loading screen"].
		b := &battleSession{cam: &camera.Camera{ViewW: 1024, ViewH: 768, MapW: 4096, MapH: 4096}}
		applyEntryZoom(Options{Zoom: camera.ZoomMax}, b)
		if w, h := b.cam.EffectiveView(); w != 512 || h != 384 {
			t.Fatalf("zoom viewport = %dx%d", w, h)
		}
	}
}

// The window adapter's options are built once, at start-up, but a content
// reload replaces the shell the host holds (docs/DESIGN_MODS_MUTATORS.md
// §4.4). Every callback must reach the shell the host holds when it runs, not
// the start-up one.
func TestHostWindowOptionsFollowTheSwappedShell(t *testing.T) {
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	first := &gameShell{presentation: settings.DefaultPresentation()}
	first.presentation.FPS, first.presentation.WaterSurface, first.presentation.Renderer = 60, 1, "classic"
	host := &shellHost{shell: first}
	options := host.windowOptions()

	second := &gameShell{presentation: settings.DefaultPresentation(), windowSize: retailDisplayMode{1024, 768}}
	second.presentation.FPS, second.presentation.WaterSurface, second.presentation.Renderer = 144, 0, "classic"
	second.opts.Renderer = "classic"
	host.shell = second

	if mode, fps := options.PresentationSettings(); fps != 144 || mode != ebitenapp.RendererClassic {
		t.Fatalf("presentation settings = %v/%d, want the swapped shell's classic/144", mode, fps)
	}
	if options.Effects().WaterSurface {
		t.Fatal("effects read the start-up shell's water surface switch")
	}
	if w, h := options.WindowSize(); w != 1024 || h != 768 {
		t.Fatalf("window size %dx%d, want the swapped shell's 1024x768", w, h)
	}
	options.FullscreenChanged(true)
	if !second.fullscreen || first.fullscreen {
		t.Fatal("the fullscreen change reached the start-up shell")
	}
	options.RendererChanged(ebitenapp.RendererModern)
	if second.presentation.Renderer != "modern" || first.presentation.Renderer != "classic" {
		t.Fatal("the renderer change did not reach the swapped shell")
	}
	if options.ShowFPS() {
		t.Fatal("ShowFPS without a battle")
	}
}

// A desktop at retail's 4:3 default takes the monitor's shape once, scaled
// to at most desktopFitWidth wide; a chosen size is kept, and either way the
// fit is recorded and saved so it never repeats (DESIGN_PRESENTATION_CLIENT
// §2.1).
func TestDesktopFitTakesTheMonitorShapeOnce(t *testing.T) {
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	for _, c := range []struct {
		start, monitor, want retailDisplayMode
		changed              bool
	}{
		{retailDisplayMode{800, 600}, retailDisplayMode{1920, 1080}, retailDisplayMode{1920, 1080}, true},
		{retailDisplayMode{800, 600}, retailDisplayMode{3840, 2160}, retailDisplayMode{2560, 1440}, true},
		{retailDisplayMode{800, 600}, retailDisplayMode{3440, 1440}, retailDisplayMode{2560, 1072}, true},
		{retailDisplayMode{800, 600}, retailDisplayMode{800, 600}, retailDisplayMode{800, 600}, false},
		{retailDisplayMode{1024, 768}, retailDisplayMode{1920, 1080}, retailDisplayMode{1024, 768}, false},
	} {
		g := &gameShell{cs: &contentSet{}}
		g.applySettings(settings.Defaults())
		g.display.Width, g.display.Height = c.start.W, c.start.H
		g.commitWindowSize()
		if got := g.fitDisplayTo(c.monitor.W, c.monitor.H); got != c.changed || g.windowSize != c.want || g.display.Width != c.want.W || g.displayFitted != desktopDisplayFit {
			t.Fatalf("%+v on %+v: %v, %+v", c.start, c.monitor, got, g.windowSize)
		}
		g.settingsWritable = true
		g.saveSettings()
		saved, err := settings.Load()
		if err != nil {
			t.Fatal(err)
		}
		again := &gameShell{cs: &contentSet{}}
		again.applySettings(saved)
		if again.displayFitted != desktopDisplayFit || again.display.Width != c.want.W || again.display.Height != c.want.H {
			t.Fatalf("saved fit %+v", saved.Display)
		}
	}
}
