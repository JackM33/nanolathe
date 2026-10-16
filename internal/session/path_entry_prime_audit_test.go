package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// The production composition must bind the physical pool before entry's idle
// scheduler pass. Its visits survive the prime and ordinary same-world binds;
// they determine which subsequently staged request is admitted first
// [04 R-PATH-01 §6][08 R-ENTRY-01 §8].
func TestEntryPrimeRetainsPhysicalPathVisits(t *testing.T) {
	s := assistSeamSession(t)
	start, end, ok := s.Units.SliceForPlayer(0)
	if !ok || end-start < 2 {
		t.Fatal("fixture needs at least three physical slots")
	}
	s.Movement.ConfigurePath(1, int32(end-start+1), func(p int) bool { return p == 0 })
	s.Movement.Scheduler.SetStepAllowance(1)
	var admitted []pool.Handle
	s.Movement.Scheduler.SetSearch(func(r path.Request, _ int32, _ int) path.WorkResult {
		admitted = append(admitted, r.Unit)
		return path.WorkResult{Done: true}
	})
	if err := finishBattleEntry(s, nil); err != nil {
		t.Fatal(err)
	}
	if len(admitted) != 0 || s.Movement.Scheduler.TraceState().CallCount != 1 {
		t.Fatal("empty entry prime did not perform exactly one idle scheduler call")
	}
	// With the cursor seeded on the first slot, one idle visit reaches the
	// second. A later first admission must select the third, ahead of second.
	for _, slot := range []int{start + 1, start + 2} {
		h, err := s.Units.CreateWithForcedSlot(s.Catalog.Units["assistseamprod"], 0, 0, 0, 0, pool.Handle(slot))
		if err != nil {
			t.Fatal(err)
		}
		s.Movement.EnsureUnit(s.Units.Unit(h))
		s.Movement.SubmitMove(h, 0, path.Cell{}, path.Cell{X: 1})
	}
	s.Movement.BindWorld(s.Units)
	s.Movement.Scheduler.SetStepAllowance(101)
	s.Movement.Scheduler.EnableTrace()
	s.Movement.Scheduler.Tick(60)
	// The allowance buys one admission. A Modern path batch opens its
	// searches through the provider rather than the search hook
	// (docs/DESIGN_MOVEMENT_PATH.md "Modern parallel path batches"); the
	// scheduler's trace records an admission under either scheduler.
	_, third := s.Movement.Scheduler.TraceFor(pool.Handle(start + 2))
	_, second := s.Movement.Scheduler.TraceFor(pool.Handle(start + 1))
	if third == nil || second != nil || len(admitted) > 1 || len(admitted) == 1 && admitted[0] != pool.Handle(start+2) {
		t.Fatalf("first post-prime admission=%v (third traced %v, second traced %v), want third physical slot %d", admitted, third != nil, second != nil, start+2)
	}
}
