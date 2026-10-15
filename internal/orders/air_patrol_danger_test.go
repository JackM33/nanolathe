package orders

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Modern patrol keeps flying when an impact supplies no usable counterattack.
// A short withdrawal move would brake and clear the patrol's weapon slots.
func TestAirPatrolKeepsRouteWithoutCounterattack(t *testing.T) {
	for _, modern := range []bool{false, true} {
		for _, visible := range []bool{false, true} {
			q, u, enemy := dangerFixture(modern)
			u.Def.CanFly, u.Def.CanPatrol = true, true
			u.Move.Mode = 2
			q.Binding().SetDangerVisible(func(*units.Unit, *units.Unit) bool { return visible })
			q.Binding().SetDangerCanRespond(func(*units.Unit, *units.Unit, int) bool { return false })
			head := &Node{ID: Lookup("VTOL_Patrol"), Owner: u.Handle, Phase: 2, Deadline: 100, DynamicGate: gateMoveOutcomes}
			next := &Node{ID: head.ID, Owner: u.Handle}
			q.primary = []*Node{head, next}
			before, random := *head, *q.Binding().SimRNG
			q.Binding().Movement = NewMovementGoalAdapter(MovementGoalAdapterConfig{Release: func(*Node) bool {
				t.Fatal("unanswerable fire released the patrol marker")
				return true
			}})
			for tick := uint32(10); tick < 100; tick += 30 {
				ObserveImpact(u, 16384, tick)
				ObserveDanger(u, enemy, tick)
				StepDangerResponse(u, tick)
			}
			if q.danger.response != nil || len(q.primary) != 2 || q.primary[0] != head || q.primary[1] != next || !reflect.DeepEqual(*head, before) || *q.Binding().SimRNG != random {
				t.Fatalf("modern=%v visible=%v: unanswerable fire interrupted patrol or drew RNG", modern, visible)
			}
		}
	}
}

func TestAirPatrolStillRespondsToVisibleAttacker(t *testing.T) {
	q, u, enemy := dangerFixture(true)
	u.Def.CanFly, u.Def.CanPatrol = true, true
	u.Move.Mode = 2
	head := &Node{ID: Lookup("VTOL_Patrol"), Owner: u.Handle, Phase: 2}
	q.primary = []*Node{head}
	ObserveDanger(u, enemy, 10)
	StepDangerResponse(u, 10)
	if q.danger.response == nil || q.danger.response.Target != enemy.Handle || len(q.primary) != 2 || q.primary[1] != head {
		t.Fatal("visible counterattack lost its target or retained patrol")
	}
}
