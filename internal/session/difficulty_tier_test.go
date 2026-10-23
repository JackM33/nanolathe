package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
)

// A stored difficulty index plays its own tier unless the table reverses it,
// as TA: Escalation's patched executable does
// (research/extensions/taesc-engine.md "AI"); the reading is its own inverse,
// and an index outside the vocabulary is left for its reader to refuse.
func TestDifficultyTierReversesOnlyUnderTheSwitch(t *testing.T) {
	reversed := community.Features{AIDifficultyReversed: true}
	for index, want := range []int{2, 1, 0} {
		if got := DifficultyTier(community.Features{}, index); got != index {
			t.Fatalf("index %d plays %d without the switch", index, got)
		}
		if got := DifficultyTier(reversed, index); got != want {
			t.Fatalf("index %d plays %d reversed, want %d", index, got, want)
		}
		if back := DifficultyTier(reversed, DifficultyTier(reversed, index)); back != index {
			t.Fatalf("index %d does not read back: %d", index, back)
		}
	}
	for _, index := range []int{-1, 3, 5} {
		if got := DifficultyTier(reversed, index); got != index {
			t.Fatalf("out-of-vocabulary index %d became %d", index, got)
		}
	}
}

// Under a reversing table every seat reader takes the tier its stored index
// plays: the plan gate and persona (sessionAIDifficultyFor), the ledger's
// per-player word and the construction refunds' word; the human row reads
// the battle's index the same way. Strict ignores the table, so the same
// setup plays in retail's order there.
func TestReversedSeatWordsReachEveryReader(t *testing.T) {
	s := perSeatSession(t) // stored indices: seat 1 at 0, seat 2 at 2, seat 3 at 1; the battle at 0
	on := true
	s.CommunitySources = CommunitySources{Content: []community.Overrides{{AIDifficultyReversed: &on}}}
	for _, c := range []struct {
		set     RuleSet
		reverse bool
	}{
		{CommunityRuleSet(), true},
		{StrictRuleSet(), false},
	} {
		s.BindRules(c.set)
		names := [3]ai.Difficulty{ai.DifficultyEasy, ai.DifficultyMedium, ai.DifficultyHard}
		for p, index := range map[uint8]int{0: 0, 1: 0, 2: 2, 3: 1} {
			tier := index
			if c.reverse {
				tier = 2 - index
			}
			if got, ok := sessionAIDifficultyFor(s, p); !ok || got != names[tier] {
				t.Fatalf("%s: player %d at index %d plan gate %q, want %q", c.set.Name, p, index, got, names[tier])
			}
			if p == 0 {
				continue
			}
			if got, ok := s.Econ.PlayerSelector(p); !ok || got != tier {
				t.Fatalf("%s: player %d ledger word %d (%t), want %d", c.set.Name, p, got, ok, tier)
			}
			if got := s.Build.RefundSelector(p); got != tier {
				t.Fatalf("%s: player %d refund word %d, want %d", c.set.Name, p, got, tier)
			}
		}
	}
}

// TA: Escalation's own config reverses the index: a skirmish composed at
// index 0, which that content's menus spell Hard, gives its computer the Hard
// plan and persona and the ledger the hard word; under Strict, which ignores
// every table, the same index plays easy as in retail.
func TestEscalationConfigPlaysIndexZeroHard(t *testing.T) {
	f := loadRetailFixture(t)
	sources := CommunitySources{Content: loadModContent(t, nil, "escalation-10.2.0").GameplaySources()}
	for _, c := range []struct {
		mode gameplay.Mode
		want ai.Difficulty
		word int
	}{
		{gameplay.Community39, ai.DifficultyHard, 2},
		{gameplay.Strict31, ai.DifficultyEasy, 0},
	} {
		cfg := f.cfg
		cfg.Gameplay, cfg.Difficulty = c.mode, 0
		s, err := NewSkirmishWithEntryOptions(f.fs, f.cat, cfg, SkirmishEntryOptions{CommunitySources: sources})
		if err != nil {
			t.Fatal(err)
		}
		if m := s.AI[1]; m == nil || m.Profile == nil || m.Profile.Plan != c.want || ControllerDifficulty(m.Profile) != c.want {
			t.Fatalf("%s: the computer's plan at index 0 is not %q", c.mode, c.want)
		}
		if s.Econ.EconomySelector == nil || *s.Econ.EconomySelector != c.word {
			t.Fatalf("%s: ledger word %v at index 0, want %d", c.mode, s.Econ.EconomySelector, c.word)
		}
	}
}
