package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func activityHoldForTest(e *economy.Service, owner uint8, a economy.Activity, modern bool) {
	e.Rules = economy.StrictRules{}
	if modern {
		e.Rules = economy.ModernRules{}
	}
	e.HoldActive[owner][a] = true
}

func TestActivityHoldStockpileKeepsQueuedRound(t *testing.T) {
	for _, modern := range []bool{false, true} {
		u, q, e := armsFixture(t)
		weapon := weaponDefForBuild(true, 12)
		weapon.EnergyPerShot, weapon.MetalPerShot = 12, 12
		u.Slots[0].Weapon = weapon
		q.CoalesceTail(Lookup("BuildWeapon"), Node{Param2: 2})
		n := q.Secondary()[0]
		activityHoldForTest(e, u.Owner, economy.ActivityWeapons, modern)
		q.Pump(u, 1)
		if modern {
			if n.Param3 != 0 || n.Param2 != 2 || u.Slots[0].Ammo != 0 || e.UnitBuckets(u.Handle)[economy.Energy].Requested != 0 {
				t.Fatal("held stockpile changed work or demand")
			}
			e.HoldActive[u.Owner][economy.ActivityWeapons] = false
			q.Pump(u, 2)
		}
		if n.Param3 != 5 || n.Param2 != 2 || e.UnitBuckets(u.Handle)[economy.Energy].Requested != 5 {
			t.Fatalf("modern=%v queued work failed to resume normally", modern)
		}
	}
}

func TestActivityHoldFeatureReclaimRetainsWorkAndPayout(t *testing.T) {
	for _, modern := range []bool{false, true} {
		for _, air := range []bool{false, true} {
			tree, _ := retailShapedTree()
			f := newFeatureWorkFixture(t, []*content.FeatureDef{tree}, 9, 11)
			activityHoldForTest(f.econ, f.builder.Owner, economy.ActivityReclaim, modern)
			n := &Node{Owner: f.builder.Handle, Phase: 4, Param1: 40, GoalX: world.CellToWorld(9), GoalZ: world.CellToWorld(11)}
			handler := reclaimHandler
			if air {
				n.Phase = 3
				handler = vtolReclaimHandler
			}
			handler(f.builder, n, 0, 1)
			if modern {
				if n.Param1 != 40 || f.segments != 0 {
					t.Fatal("held feature reclaim consumed work or emitted spray")
				}
				n.Phase++
				handler(f.builder, n, 0, 2)
				if _, _, _, ok := featureAtGoal(f.builder, n); !ok {
					t.Fatal("held payout removed feature")
				}
				if f.econ.UnitBuckets(f.builder.Handle)[economy.Metal].Production != 0 {
					t.Fatal("held payout credited metal")
				}
				f.econ.HoldActive[f.builder.Owner][economy.ActivityReclaim] = false
				handler(f.builder, n, 0, 3)
			} else {
				if n.Param1 != 38 {
					t.Fatal("Strict reclaim countdown did not advance")
				}
				n.Phase++
				handler(f.builder, n, 0, 2)
			}
			if got := f.econ.UnitBuckets(f.builder.Handle)[economy.Energy].Production; got != float32(tree.Energy) {
				t.Fatalf("modern=%v air=%v refund=%v", modern, air, got)
			}
		}
	}
}

// The air executor otherwise sprays even after failed energy admission. A
// temporary hold must stop that attempted work before the nano query as well.
func TestActivityHoldAirRepairSkipsCallbackAndSpray(t *testing.T) {
	for _, modern := range []bool{false, true} {
		tree, _ := retailShapedTree()
		f := newFeatureWorkFixture(t, []*content.FeatureDef{tree}, 9, 11)
		u := f.builder
		u.Def.CanFly = true
		u.Move.Mode, u.Move.ModeMirror = 1, 1
		u.Health = 10
		calls, segments := 0, 0
		f.q.Binding().Work = NewWorkAdapter(WorkAdapterConfig{Repair: func(*units.Unit, *units.Unit, *Node, uint32) bool { calls++; return true }})
		f.q.Binding().Presentation = NewPresentationAdapter(PresentationAdapterConfig{Nanolathe: func(*units.Unit, *Node, uint32) bool { segments++; return true }})
		activityHoldForTest(f.econ, u.Owner, economy.ActivityHealing, modern)
		n := &Node{Owner: u.Handle, Target: u.Handle, Phase: 2}
		vtolRepairUnitHandler(u, n, 0, 1)
		want := 1
		if modern {
			want = 0
		}
		if calls != want || segments != want {
			t.Fatalf("modern=%v callbacks=%d spray=%d", modern, calls, segments)
		}
	}
}
