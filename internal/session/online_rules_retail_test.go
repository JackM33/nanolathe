//go:build retail

package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
)

// On the reference install the rehearsal's script builds and fires, so each
// reserved set rehearses its own battle: the three digests differ, and
// within a set the digest is the same whichever seat a composition presents
// (DESIGN_MULTIPLAYER §16.6, §16.7).
func TestOnlineRehearsalFollowsTheRuleSetRetail(t *testing.T) {
	digests := map[[32]byte]gameplay.Mode{}
	for _, tc := range onlineRuleCases[1:] {
		setup := onlineRetailSetup(false, 0, 0, 0)
		setup.Rules = tc.rules
		inputs, config := onlineRetailMatch(t, setup)
		digest := rehearsalDigestOf(t, inputs, config, 0)
		if got := rehearsalDigestOf(t, inputs, config, 1); got != digest {
			t.Fatalf("%q: seat 1's digest %x, want %x", tc.rules, got, digest)
		}
		if other, ok := digests[digest]; ok {
			t.Fatalf("%q rehearses %q's battle", tc.rules, other)
		}
		digests[digest] = tc.mode
	}
}

// A Strict 3.1 room holds its one computer, and a Community 3.9 room the
// same Classic computer: two seats, each freezing its own inputs, play alike
// for thirty seconds, the computer hosted by seat 0 and borrowing its
// perspective (DESIGN_MULTIPLAYER §6.6).
func TestOnlineRuleSetRoomsWithAComputerPlayAlikeRetail(t *testing.T) {
	for _, rules := range []string{StrictRuleSetName, CommunityRuleSetName} {
		setup := onlineRetailSetup(false, 0, 1, 2)
		setup.Rules = rules
		setup.Computers = []OnlineComputer{{Team: 3, Side: 0, Color: 2, Kind: ai.ControllerClassic, Difficulty: 1}}
		a, b, config, _ := onlineComputerRetailPair(t, setup)
		for _, s := range []*Session{a, b} {
			if s.Rules.Name != rules || s.AI[2] == nil || s.AI[2].Controller != ai.ControllerClassic || s.Vis.PerspectiveOf(2) != 0 {
				t.Fatalf("%q: composed under %q with computer %+v", rules, s.Rules.Name, s.AI[2])
			}
		}
		onlineLockstep(t, a, b, config, 900, []uint8{2})
	}
}
