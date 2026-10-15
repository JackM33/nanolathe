//go:build retail

package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"testing"
)

func TestPublishedPatrolWorkBorrowedAndReturnRetail(t *testing.T) {
	for _, key := range []string{"armcv", "armca"} {
		t.Run(key, func(t *testing.T) {
			f := newConstructorPatrolRuntime(t, key, 1, nil)
			target := f.lateNanoframe(160)
			f.observeWork(target)
			seenWork, seenReturn, seenResume := false, false, false
			for i := 0; i < 1800; i++ {
				f.step()
				head := f.q.Head()
				if head == nil {
					t.Fatal("patrol disappeared")
				}
				ordinal := f.q.WorkAssignmentOrdinal(head)
				name := orders.DescriptorFor(head.ID).Name
				if ordinal > 0 {
					if name == "Move_Ground" || name == "VTOL_Move" {
						seenReturn = true
					} else {
						seenWork = true
						if allocations := testing.AllocsPerRun(100, func() { f.s.publishedPatrolWork(f.helper) }); allocations != 0 {
							t.Fatalf("borrowed patrol publication allocated %.1f times", allocations)
						}
						// Failure still executes the recovery return; successful work
						// now continues the pending patrol leg directly (issue #122).
						f.s.Units.Destroy(target.Handle, units.DeathUnknown)
					}
				} else if seenReturn && name == f.patrol {
					seenResume = true
				}
				if got := f.s.publishedPatrolWork(f.helper); got != 2 {
					t.Fatalf("%s label=%d want Both", name, got)
				}
				if seenWork && seenReturn && seenResume {
					break
				}
			}
			if !seenWork || !seenReturn || !seenResume {
				t.Fatalf("missing work/return/resume: %t/%t/%t", seenWork, seenReturn, seenResume)
			}
		})
	}
}
