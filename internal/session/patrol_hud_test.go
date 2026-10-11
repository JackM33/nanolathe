package session

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func TestPublishedPatrolWorkPreferencesAndIsolation(t *testing.T) {
	for _, air := range []bool{false, true} {
		for _, mode := range []gameplay.Mode{gameplay.Modern, gameplay.Strict31, gameplay.Community39} {
			t.Run(fmt.Sprintf("air=%t/%s", air, mode), func(t *testing.T) {
				def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "constructor"}, MaxDamage: 100, Builder: true, BMCode: 1, CanMove: true, CanFly: air, MobileStandOrders: true}
				w := newSessionFixtureWorld(2, nil)
				handle, err := w.Create(def, 0, 0, 0, 0)
				if err != nil {
					t.Fatal(err)
				}
				u := w.Unit(handle)
				s := &Session{Rules: RuleSetForMode(mode), Units: w, Snapshot: frame.NewBuffer()}
				preferences := orders.BuilderOptions{Patrol: [3]orders.PatrolWorkOption{orders.PatrolReclaimOnly, orders.PatrolBoth, orders.PatrolAssistOnly}}
				binding := &orders.QueueBinding{Rules: s.Rules.Orders}
				binding.SetBuilderOptions(func(owner uint8) orders.BuilderOptions {
					if owner == 0 {
						return preferences
					}
					return s.Rules.Orders.DefaultBuilderOptions()
				})
				q := orders.BindQueueBinding(u, binding)
				name := "RepairPatrol"
				if air {
					name = "VTOL_RepairPatrol"
				}
				id := orders.Lookup(name)
				q.PushHead(id, orders.NewNodeForOrder(id, 0, 0, 0, 0, 0, u.Handle, false))
				for stance := uint32(0); stance < 3; stance++ {
					u.Flags = u.Flags & ^(units.StandingFieldMask<<units.StandingMoveShift) | stance<<units.StandingMoveShift
					s.publishSnapshot(stance*2 + 1)
					cur := s.Snapshot.Current()
					want := uint8(0)
					if mode == gameplay.Modern {
						want = uint8(preferences.Patrol[stance]) + 1
					}
					if got := cur.Units[0].PatrolWork; got != want {
						t.Fatalf("stance%d work=%d want%d", stance, got, want)
					}
					preferences.Patrol[stance] = orders.PatrolBoth
					if cur.Units[0].PatrolWork != want {
						t.Fatal("live preference mutation changed committed frame")
					}
					s.publishSnapshot(stance*2 + 2)
					want = 0
					if mode == gameplay.Modern {
						want = 2
					}
					if s.Snapshot.Current().Units[0].PatrolWork != want {
						t.Fatal("next publication missed preference change")
					}
				}
				preferences.Patrol[2] = orders.PatrolAssistOnly
				u.Owner = 1
				if got := s.publishedPatrolWork(u); mode == gameplay.Modern && got != 2 {
					t.Fatalf("foreign owner preference=%d want default Both", got)
				}
				def.BMCode = 2
				if got := s.publishedPatrolWork(u); got != 0 {
					t.Fatalf("non-mobile builder label=%d", got)
				}
				def.BMCode = 1
				// Explicit work must not inherit a future queued patrol's label.
				id = orders.Lookup("RepairUnit")
				q.PushHead(id, orders.NewNodeForOrder(id, 0, 0, 0, 0, 0, u.Handle, false))
				if got := s.publishedPatrolWork(u); got != 0 {
					t.Fatalf("explicit work labelled as patrol: %d", got)
				}
				q.CancelAll()
				if got := s.publishedPatrolWork(u); got != 0 {
					t.Fatalf("idle unit labelled as patrol: %d", got)
				}
			})
		}
	}
}
