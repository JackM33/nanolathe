package tactics

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// The standing guard must activate below Attention 3: Easy is 1 and Medium
// is 2, and those are the personas the long-game correction targets.
func TestStandingGuardAtLowAttention(t *testing.T) {
	a, _ := testArmy(10, 10)
	a.P.Defend = true
	if !a.defendSquadAllowed(1, 0, true) || !a.defendSquadAllowed(2, 0, true) {
		t.Error("standing guard did not activate at Attention 1/2")
	}
	if a.defendSquadAllowed(1, 1000, false) || a.defendSquadAllowed(2, 1000, false) {
		t.Error("an incident activated the guard below Attention 3")
	}
	if !a.defendSquadAllowed(3, 1000, false) {
		t.Error("an incident did not activate the guard at Attention 3")
	}
	a.P.Defend = false
	if a.defendSquadAllowed(3, 1000, true) {
		t.Error("defend=0 still activated the guard")
	}
}

// An active, under-strength guard recruits existing main-squad members;
// otherwise production can stop and the guard stays empty while the main
// squad keeps attacking.
func TestStandingGuardRecruitsMainSquad(t *testing.T) {
	a, _ := testArmy(10, 10)
	info := &aikit.UnitInfo{Value: 100, HP: 1000}
	own := make([]aikit.OwnUnit, 10)
	for i := range own {
		own[i] = aikit.OwnUnit{H: pool.Handle(i + 1), Info: info, Built: true}
	}
	b := &core.Board{K: &aikit.Kit{}, O: &aikit.Obs{Own: own}}
	a.sq[sqDefend].active = true
	a.sq[sqMain].members = []int32{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	a.recruitHomeGuard(b, 350)
	if len(a.sq[sqDefend].members) != 4 || len(a.sq[sqMain].members) != 6 {
		t.Fatalf("guard %d main %d, want 4 and 6", len(a.sq[sqDefend].members), len(a.sq[sqMain].members))
	}
	for _, i := range a.sq[sqDefend].members {
		if b.O.Own[i].Tag != sqDefend {
			t.Errorf("unit %d tag = %d, want defend", i, b.O.Own[i].Tag)
		}
	}
}
