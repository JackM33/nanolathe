//go:build retail

package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// The skirmish screen's Start buttons on the retail art: each live row
// cycles Auto and the map's starts, skipping one another row holds; the
// battle the screen starts places each commander on its choice under
// Strict 3.1 as under Modern; and the map picker numbers the starts
// (docs/DESIGN_SESSIONS_AI_SAVE.md "Chosen start positions",
// DESIGN_INTERFACE_HUD_INPUT §2.6 "Start positions"). With
// NANOLATHE_SHOT_DIR set, both screens are written there as PNGs.
func TestSkirmishScreenChoosesStartPositionsRetail(t *testing.T) {
	resetSaveLoadScreenState(t)
	shell, _ := retailShellForTest(t)
	shell.settingsWritable = false
	shot := survivalMenuShooter(t, shell)
	caption := func(row string) string {
		p := shell.activePanel()
		return p.TextAt(p.Index(row))
	}
	shell.activateGadget("SINGLE")
	shell.activateGadget("Skirmish")
	shell.setup.NumPlayers = 4
	shell.retailControllers = [session.SkirmishMaxPlayers]int{1, 2, 0, 0}
	shell.openMenu(modeMenuSkirmish)
	choices := shell.skirmishStartChoices()
	if len(choices) < 2 {
		t.Skipf("map %q offers %d starts for two players", shell.setup.MapName, len(choices))
	}
	if got := caption("StartPos0"); got != "Auto" {
		t.Fatalf("a fresh row's Start caption %q, want Auto", got)
	}
	if p := shell.activePanel(); p.ActiveAt(p.Index("StartPos2")) {
		t.Fatal("an open row shows a Start button")
	}
	last := choices[len(choices)-1]
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	click := func(name string, button input.MouseButton) {
		clickRowGadget(t, shell, shell.activePanel(), cl, name, button)
	}
	click("StartPos0", input.MouseButtonLeft)  // Auto -> first start
	click("StartPos1", input.MouseButtonLeft)  // Auto -> second start, skipping row 0's
	click("StartPos1", input.MouseButtonRight) // back to Auto
	click("StartPos1", input.MouseButtonRight) // wraps to the last start
	if got, want := caption("StartPos0"), skirmishStartCaption(choices[0]); got != want {
		t.Fatalf("row 0 Start caption %q, want %q", got, want)
	}
	if got := shell.setup.Players[1].Start; got != last {
		t.Fatalf("row 1 start %d after two right clicks, want the last start %d", got, last)
	}
	shot("skirmish-start-positions")
	starts := mapStartPositions(shell.mapDataFor(shell.setup.MapName), 2)
	at := func(n int) (numeric.Fixed, numeric.Fixed) {
		for _, sp := range starts {
			if int(sp.ID)+1 == n {
				return numeric.Fixed(sp.X) << 16, numeric.Fixed(sp.Z) << 16
			}
		}
		t.Fatalf("no StartPos%d", n)
		return 0, 0
	}
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Modern} {
		cfg := shell.skirmishConfigForStart(shell.setup.MapName)
		opts := shell.opts
		opts.Gameplay = mode
		request, err := skirmishBattleRequest(opts, shell.cs, cfg, headlessScenarioSkirmish, nil, newBattleSeedSource(Options{Seed: 3}))
		if err != nil {
			t.Fatal(err)
		}
		battle, err := headless.ComposeFreshBattle(request.value)
		if err != nil {
			t.Fatalf("%s: the setup does not compose: %v", mode, err)
		}
		for _, u := range battle.Session.Units.Iter() {
			x, z := at(cfg.Players[u.Owner].Start)
			if u.X != x || u.Z != z {
				t.Fatalf("%s: player %d commander at (%d,%d), want StartPos%d (%d,%d)", mode, u.Owner, u.X>>16, u.Z>>16, cfg.Players[u.Owner].Start, x>>16, z>>16)
			}
		}
	}
	shell.activateGadget("SelectMap")
	if shell.frontend.Mode != modeMenuMap {
		t.Fatalf("SelectMap -> mode %d", shell.frontend.Mode)
	}
	shot("map-start-positions")
	shell.activateGadget("PREVMENU")
}
