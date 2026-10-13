package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/survival"
)

// These fields are the effective configuration supplied by battle entry. The
// comparison drops only seed/name/colour identity, never gameplay choices.
func TestSurvivalBestComparableSetups(t *testing.T) {
	setup := func() session.MatchConfigRequest {
		return session.MatchConfigRequest{RuleName: "modern", MapName: "The Pass", UnitLimit: 1000,
			SimulationSeed: 1, CRTSeed: 2, Mapping: 1, LineOfSight: 1,
			Seats: []session.MatchSeat{{Role: session.MatchRoleHuman, Nickname: "Ann", Metal: 1000},
				{Role: session.MatchRoleComputer, ComputerKind: ai.ControllerModern, Difficulty: 2}}}
	}
	key := func(r session.MatchConfigRequest, digest [32]byte) string {
		t.Helper()
		k, err := survivalBestKey(r, digest)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	original := setup()
	want := key(original, [32]byte{1})
	if original.Seats[0].Nickname != "Ann" {
		t.Fatal("key changed caller's setup")
	}
	same := setup()
	same.SimulationSeed, same.CRTSeed = 42, 17
	same.Seats[0].Nickname, same.Seats[0].Color = "Ben", 3
	same.Seats[0].Participant[0] = 4
	same.MapName = "the pass"
	if key(same, [32]byte{1}) != want {
		t.Fatal("repeat attempt split by cosmetic identity or seed")
	}
	for name, change := range map[string]func(*session.MatchConfigRequest){
		"map":     func(r *session.MatchConfigRequest) { r.MapName = "arena" },
		"rules":   func(r *session.MatchConfigRequest) { r.RuleName = "strict-3.1" },
		"mod":     func(r *session.MatchConfigRequest) { r.Mod.Version = "2" },
		"mutator": func(r *session.MatchConfigRequest) { r.Mutators.Health = content.Factor{Num: 2, Den: 1} },
		"restriction": func(r *session.MatchConfigRequest) {
			r.UnitRestrictions = []session.MatchUnitRestriction{{Unit: "scout", Limit: 0}}
		},
		"buddy count": func(r *session.MatchConfigRequest) { r.Seats = r.Seats[:1] },
		"buddy AI":    func(r *session.MatchConfigRequest) { r.Seats[1].ComputerKind = ai.ControllerClassic },
		"difficulty":  func(r *session.MatchConfigRequest) { r.Seats[1].Difficulty = 1 },
		"AI parameters": func(r *session.MatchConfigRequest) {
			r.Seats[1].AIParams = []session.AIParam{{Key: "jitter", Value: "0"}}
		},
		"side":       func(r *session.MatchConfigRequest) { r.Seats[0].Side++ },
		"resources":  func(r *session.MatchConfigRequest) { r.Seats[0].Metal++ },
		"unit limit": func(r *session.MatchConfigRequest) { r.UnitLimit++ },
		"commander":  func(r *session.MatchConfigRequest) { r.CommanderDeath++ },
		"visibility": func(r *session.MatchConfigRequest) { r.LineOfSight = 0 },
		"pace":       func(r *session.MatchConfigRequest) { r.SurvivalPace = survival.PaceRelaxed },
		"air":        func(r *session.MatchConfigRequest) { r.SurvivalNoAir = true },
		"naval":      func(r *session.MatchConfigRequest) { r.SurvivalNoNaval = true },
	} {
		r := setup()
		change(&r)
		if key(r, [32]byte{1}) == want {
			t.Errorf("%s did not separate records", name)
		}
	}
	if key(setup(), [32]byte{2}) == want {
		t.Fatal("changed content shares a best")
	}
}

func TestSurvivalBestCompletionAndExclusions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv(settings.EnvPath, path)
	view := frame.ResultView{Ended: true, Kind: "defeat", Survival: &frame.SurvivalResult{Score: 100, Waves: 2, TicksAlive: 1800}}
	b := &battleSession{survivalBest: &survivalBestRun{key: "setup"}}
	b.finishSurvivalBest(frame.ResultView{Survival: view.Survival})
	if b.survivalBest.finished {
		t.Fatal("unfinished battle recorded")
	}
	b.finishSurvivalBest(view)
	if !strings.HasPrefix(b.survivalBestResultLine(), "New personal best") {
		t.Fatal(b.survivalBestResultLine())
	}
	// The result lifecycle can revisit this edge; it must never write twice.
	view.Survival.Score = 200
	b.finishSurvivalBest(view)
	if got, _, _ := settings.LoadSurvivalBest("setup"); got.Score != 100 {
		t.Fatal("repeated completion changed record")
	}
	for name, exclude := range map[string]func(*battleSession){
		"cheat":         func(b *battleSession) { b.noteSurvivalCommand(session.HumanCommand{Kind: session.HumanATM}) },
		"changed rules": func(b *battleSession) { b.noteSurvivalCommand(session.HumanCommand{Kind: session.HumanGameplay}) },
		"replay":        func(b *battleSession) { b.playback = &replayPlayback{} },
		"online":        func(b *battleSession) { b.multiplayer = &battleMultiplayer{} },
		"preview":       func(b *battleSession) { b.preview = true },
	} {
		b := &battleSession{survivalBest: &survivalBestRun{key: name}}
		exclude(b)
		b.finishSurvivalBest(view)
		if _, exists, err := settings.LoadSurvivalBest(name); exists || err != nil {
			t.Fatalf("%s recorded: %v, %v", name, exists, err)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "survival-bests.json")); err != nil {
		t.Fatal(err)
	}
}

func TestSurvivalBestCheatDispatchIsStickyInEveryMode(t *testing.T) {
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Community39, gameplay.Modern} {
		for _, command := range []string{"+atm", "+radar", "+los", "+mapping", "+lostype", "+view 1", "+nowisee", "+nometal", "+selectable", "+doubleshot", "+halfshot", "+dev"} {
			b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
			b.sess.Gameplay = mode
			b.survivalBest = &survivalBestRun{key: "setup"}
			b.dispatchLocalCommand(command)
			b.dispatchLocalCommand("+fps")
			if b.survivalBest.excluded == "" {
				t.Errorf("%s %s remained eligible", mode, command)
			}
			if b.cl != nil {
				b.cl.Close()
			}
		}
	}
}
