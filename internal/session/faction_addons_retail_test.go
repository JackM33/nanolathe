//go:build retail

package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Faction add-ons compose beside ARM and CORE in one battle, every computer
// player builds its own side's units, and a save restores the composed
// battle (docs/DESIGN_MODS_MUTATORS.md §5.8). NANOLATHE_FACTION_ADDONS lists
// converted add-on directories (cmd/factionpack); the packages are local and
// never part of the reference install.
func TestFactionAddonsSkirmishAndSave(t *testing.T) {
	value := strings.TrimSpace(os.Getenv("NANOLATHE_FACTION_ADDONS"))
	if value == "" {
		t.Skip("NANOLATHE_FACTION_ADDONS is unset: faction add-on acceptance needs converted packages")
	}
	var archives []string
	for _, dir := range filepath.SplitList(value) {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.ufo"))
		archives = append(archives, matches...)
	}
	fs := vfs.New()
	t.Cleanup(func() { fs.Close() })
	if err := fs.MountGameRoots([]vfs.GameRoot{{Dir: testsupport.RetailRoot(t), Extra: archives}}); err != nil {
		t.Fatal(err)
	}
	cat, err := content.CompileWithOptions(fs, content.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Sides) < 3 || cat.Sides[0].Name != "ARM" || cat.Sides[1].Name != "CORE" {
		t.Fatalf("composed sides %d; ARM and CORE must keep ordinals 0 and 1", len(cat.Sides))
	}
	players := len(cat.Sides)
	if players > 4 {
		players = 4
	}
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Modern} {
		t.Run(string(mode), func(t *testing.T) {
			cfg := SkirmishConfig{MapName: "ashap plateau", NumPlayers: players}
			for i := 0; i < players; i++ {
				cfg.Players[i] = SkirmishPlayer{Controller: SkirmishControllerComputer, AllyGroup: i, Color: i,
					Metal: SkirmishDefaultMetal, Energy: SkirmishDefaultEnergy}
			}
			cfg.Location = 1
			cfg.ApplyDefaults()
			// ARM, then the faction sides from the last one composed.
			sides := []int{0}
			for side := len(cat.Sides) - 1; len(sides) < players; side-- {
				sides = append(sides, side)
			}
			for i, side := range sides {
				cfg.Players[i].Side = side
			}
			cfg.Gameplay = mode
			cfg.RNGSimSeed, cfg.RNGCrtSeed = 11, 11
			s, err := NewSkirmishWithEntryOptions(fs, cat, cfg, SkirmishEntryOptions{AutomatedPlayers: true})
			if err != nil {
				t.Fatal(err)
			}
			advanceFactionTicks(t, s, 1800)
			for i, side := range sides {
				name := cat.Sides[side].Name
				own, foreign := 0, 0
				for _, u := range s.Units.IterSliced() {
					if int(u.Owner) != i || u.Def == nil {
						continue
					}
					if strings.EqualFold(u.Def.Side, name) || strings.HasPrefix(strings.ToUpper(u.Def.Side), strings.ToUpper(name)) {
						own++
					} else {
						foreign++
					}
				}
				if own < 2 || foreign != 0 {
					t.Fatalf("player %d (%s) has %d own units and %d of another side after a minute", i, name, own, foreign)
				}
			}
			inputs, err := s.RetailBattleSaveInputs(RetailBattleSummary(s, "Faction add-ons", "0", s.Skirmish.UnitLimit), save.Camera{})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "FACTIONS.SAV")
			if err := s.WriteRetailSave(path, inputs); err != nil {
				t.Fatal(err)
			}
			loaded, err := LoadRetailSavePath(path, RetailLoadDeps{
				FS: fs, Catalog: cat, Gameplay: mode, SimSeed: 11, CRTSeed: 11, UnitLimit: s.Skirmish.UnitLimit,
			})
			if err != nil {
				t.Fatal(err)
			}
			if loaded.Battle == nil || loaded.Battle.Session == nil {
				t.Fatal("the composed battle did not restore")
			}
			dst := loaded.Battle.Session
			if dst.Clock.GlobalTick != s.Clock.GlobalTick || len(dst.Units.IterSliced()) != len(s.Units.IterSliced()) {
				t.Fatalf("restore: tick %d units %d, saved tick %d units %d", dst.Clock.GlobalTick, len(dst.Units.IterSliced()), s.Clock.GlobalTick, len(s.Units.IterSliced()))
			}
			advanceFactionTicks(t, dst, 300)
		})
	}
}

func advanceFactionTicks(t *testing.T, s *Session, ticks int) {
	t.Helper()
	advanced := 0
	for dispatch := 0; advanced < ticks && dispatch < ticks+8; dispatch++ {
		before := s.Clock.GlobalTick
		s.Step(s.Clock.ScaledAnchor + 1)
		if delta := int(s.Clock.GlobalTick - before); delta > 0 {
			advanced += delta
		}
	}
	if advanced != ticks {
		t.Fatalf("advanced %d ticks, want %d (state %v)", advanced, ticks, s.State)
	}
}
