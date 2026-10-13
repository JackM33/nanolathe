//go:build retail

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// Both interactive entry paths supply the same host record constructor. Its
// configuration must accommodate local Strict Survival with two buddies,
// independently of online admission's one-computer-per-human restriction.
func TestSurvivalBestBattleEntryRetail(t *testing.T) {
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	shell, _ := retailShellForTest(t)
	cat, err := shell.cs.compileCatalog(nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Community39, gameplay.Modern} {
		cfg := session.SurvivalSkirmishConfig("painted desert", 2-i, session.SurvivalOptions{})
		request := headless.FreshBattleRequest{Kind: headless.ScenarioSurvival, Map: cfg.MapName,
			Gameplay: mode, Skirmish: cfg, FS: shell.cs.fs, Catalog: cat, SimulationSeed: 7, CRTSeed: 11}
		fresh, err := headless.ComposeFreshBattle(request)
		if err != nil {
			t.Fatal(err)
		}
		s := fresh.Session
		before, sim, crt := s.UnitStateChecksum(), *s.SimRNG(), *s.CrtRNG()
		run := newSurvivalBestRun(request, shell.cs, s)
		if run == nil || run.key == "" || run.err != nil || run.excluded != "" {
			t.Fatalf("%s: %+v", mode, run)
		}
		b := &battleSession{sess: s, survivalBest: run}
		view := frame.ResultView{Ended: true, Kind: "defeat", Scores: []frame.ResultScore{{Player: 0, Name: "Player", Score: 18420}},
			Survival: &frame.SurvivalResult{Score: 18420, Waves: 7, TicksAlive: 30 * 1213, Damage: 12000, WavePoints: 6420}}
		b.finishSurvivalBest(view)
		if !run.improved || run.err != nil {
			t.Fatalf("%s completion: %+v", mode, run)
		}
		next := newSurvivalBestRun(request, shell.cs, s)
		if !next.exists || next.best.Score != view.Survival.Score {
			t.Fatalf("%s reload: %+v", mode, next)
		}
		if before != s.UnitStateChecksum() || sim != *s.SimRNG() || crt != *s.CrtRNG() {
			t.Fatal("host record altered simulation")
		}
		if dir := os.Getenv("NANOLATHE_SHOT_DIR"); dir != "" && mode == gameplay.Modern {
			b, err = composeBattleEntryDetached(s, s.Catalog, shell.cs, shell, nil)
			if err != nil {
				t.Fatal(err)
			}
			b.survivalBest, b.postBattle = run, endMissionController(t, view)
			c, err := client.New(client.Options{Buffer: frame.NewBuffer(), Width: 640, Height: 480})
			if err != nil {
				t.Fatal(err)
			}
			c.SetPalette(b.hud.pal)
			c.SetFNT(b.hud.console)
			c.SetUIStage(painterBindingStage(func(c *client.Client) { b.hud.drawResultOverlay(c, b, view, false) }))
			b.installEndMissionBackground(c)
			for i := 0; i < 100; i++ {
				b.postBattleClock += 10
				c.ComposeFrame()
			}
			f, err := os.Create(filepath.Join(dir, "survival-personal-best.png"))
			if err != nil {
				t.Fatal(err)
			}
			if err := encodeShot(f, c); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			c.Close()
		}
	}
}
