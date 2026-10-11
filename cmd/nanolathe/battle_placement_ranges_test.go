package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func TestPlacementRangeUsesShowRangesPrimitives(t *testing.T) {
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	cl, err := client.New(client.Options{Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	cam := &camera.Camera{Zoom: camera.ZoomUnit, ViewW: 640, ViewH: 480, MapW: 4096, MapH: 4096}
	// NOWEAPON must stay absent; the third active slot does not depend on the
	// first slot for a prospective product. Sensor guides are not placement ink.
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"turret": {
		RadarDistance: 600,
		Weapon1Def:    &content.WeaponDef{ID: 0, Range: 16},
		Weapon3Def:    &content.WeaponDef{ID: 2, Range: 300, Coverage: 2000, Interceptor: true},
	}}}
	cl.SetCamera(cam)
	cl.SetEnhanced(true)
	cl.SetFocused(true)
	b := &battleSession{cat: cat, cam: cam, battleUI: ui.NewProductionBattleState()}
	state := b.battleState()
	state.ArmPlacement("turret", 2, 3)
	state.Input.PointerX, state.Input.PointerY = 300, 200
	state.Input.BuildCellX, state.Input.BuildCellZ, state.Input.BuildSiteH = 20, 14, 50
	state.Input.BuildOK = false // Repositioning an invalid site keeps the guide.
	opt := hud.QueueOverlayOptions{Tick: 20,
		Project: func(x, y, z numeric.Fixed) hud.QueuePoint {
			return hud.QueuePoint{X: int32(x >> 16), Y: int32(z>>16) - int32(y>>17)}
		},
		GroundHeight: func(numeric.Fixed, numeric.Fixed) numeric.Fixed { return 60 << 16 },
	}
	x, z := world.PlacementCenter(20, 14, 2, 3)
	want := hud.WeaponRangeOverlay(hud.QueueWorldPoint{X: x, Y: 50 << 16, Z: z}, [3]hud.RangeWeapon{{}, {}, {Enabled: true, Range: 300}}, opt)
	for _, zoom := range []camera.Zoom{camera.ZoomUnit / 2, camera.ZoomUnit, camera.ZoomUnit * 3 / 2, camera.ZoomUnit * 2} {
		cam.Zoom = zoom
		if got := b.placementRangeOverlay(cl, opt); len(got) == 0 || !reflect.DeepEqual(got, want) {
			t.Fatalf("zoom %v: placement differs from shared weapon ring", zoom)
		}
	}
	noGuide := func() {
		t.Helper()
		if got := b.placementRangeOverlay(cl, opt); len(got) != 0 {
			t.Fatal("inactive placement emitted ranges")
		}
	}
	prefs := settings.DefaultPresentation()
	prefs.PlacementWeaponRanges = 0
	b.hostPresentation = &prefs
	noGuide()
	b.dispatchLocalCommand("+showranges")
	noGuide()
	state.Input.ShiftHeld = true
	if len(b.placementRangeOverlay(cl, opt)) == 0 {
		t.Fatal("explicit +showranges with Shift ignored mod default")
	}
	b.dispatchLocalCommand("+showranges")
	noGuide()
	prefs.PlacementWeaponRanges = 1
	cl.SetFocused(false)
	noGuide()
	cl.SetFocused(true)
	cl.SetEnhanced(false)
	noGuide()
	cl.SetEnhanced(true)
	state.Input.PointerX = 10
	noGuide()
	state.Input.PointerX = 300
	state.Input.BuildDef = ""
	noGuide()
	state.Input.BuildDef = "turret"
	b.modernDrag = &battleCommandDrag{}
	noGuide()
	b.modernDrag = nil
	state.Input.BuildDef = ""
	noGuide()
}

// Classic retains the process-only command across battles [07 R-P0-11 §3].
func TestShowRangesShellLifetime(t *testing.T) {
	shell := &gameShell{}
	first := &battleSession{shell: shell}
	first.dispatchLocalCommand("+showranges")
	next := &battleSession{shell: shell}
	if !next.rangesShown() {
		t.Fatal("battle transition lost +showranges")
	}
	next.dispatchLocalCommand("+showranges")
	if first.rangesShown() {
		t.Fatal("second command did not disable ranges")
	}
}

func TestModernShowRangesPersists(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(fmt.Sprint("direct=", direct), func(t *testing.T) {
			t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
			s := settings.Defaults()
			g := &gameShell{}
			g.applySettings(s)
			g.settingsWritable = true
			b := &battleSession{shell: g}
			if direct {
				b.shell = nil
				b.hostPresentation = &s.Presentation
			}
			for _, enabled := range []bool{true, false} {
				b.dispatchLocalCommand("+showranges")
				loaded, err := settings.Load()
				if err != nil {
					t.Fatal(err)
				}
				if loaded.Presentation.ShowRanges != boolInt(enabled) || b.rangesShown() != enabled {
					t.Fatalf("toggle=%v: saved=%d, live=%v", enabled, loaded.Presentation.ShowRanges, b.rangesShown())
				}
				// A new shell and a new direct battle both use the explicit
				// preference even when the content default says the opposite.
				restarted := &gameShell{showRanges: !enabled}
				restarted.applySettings(loaded)
				if (&battleSession{shell: restarted}).rangesShown() != enabled ||
					(&battleSession{hostPresentation: &loaded.Presentation, showRanges: !enabled}).rangesShown() != enabled {
					t.Fatal("restart lost the explicit range preference")
				}
			}
		})
	}
}

func TestShowRangesInheritsContentUntilModernChoice(t *testing.T) {
	p := settings.DefaultPresentation()
	b := &battleSession{hostPresentation: &p, showRanges: true}
	if !b.rangesShown() {
		t.Fatal("unset preference suppressed the content default")
	}
	p.ShowRanges = 0
	if b.rangesShown() {
		t.Fatal("explicit off did not override the content default")
	}
	p.Renderer = "classic"
	if !b.rangesShown() {
		t.Fatal("Classic read the Modern preference")
	}
}

func TestDirectModShowRangesPersistsInModLayer(t *testing.T) {
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	s := settings.Defaults()
	s.ModSettings = map[string]json.RawMessage{"probe": json.RawMessage(`{"presentation":{"showRanges":0,"placementWeaponRanges":0}}`)}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	g := &gameShell{cs: &contentSet{mod: &modlibrary.Mod{ID: "probe"}}}
	loaded := g.effectiveSettings(s)
	b := &battleSession{cs: g.cs, hostPresentation: &loaded.Presentation}
	b.dispatchLocalCommand("+showranges")
	saved, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	effective := g.effectiveSettings(saved)
	if saved.Presentation.ShowRanges != -1 || effective.Presentation.ShowRanges != 1 || effective.Presentation.PlacementWeaponRanges != 0 {
		t.Fatal("direct mod toggle changed the base or failed to preserve the mod layer")
	}
	if !(&battleSession{cs: g.cs, hostPresentation: &effective.Presentation}).rangesShown() {
		t.Fatal("direct mod restart lost the toggle")
	}
}
