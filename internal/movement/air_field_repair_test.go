package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func fieldRepairConstructor(t *testing.T, w *units.World, x, z int64) *units.Unit {
	t.Helper()
	d := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("fieldrepairer")},
		UnitName:         "fieldrepairer", Builder: true, CanMove: true, BMCode: 1,
		BuildDistance: 128, SightDistance: 256, MaxDamage: 100, FootprintX: 1, FootprintZ: 1,
	}
	h, err := w.Create(d, 0, numeric.FixedFromInt(x), 10<<16, numeric.FixedFromInt(z))
	if err != nil {
		t.Fatal(err)
	}
	c := w.Unit(h)
	orders.QueueForUnit(c).PushHead(orders.Lookup("RepairPatrol"), orders.Node{Owner: h})
	return c
}

// Constructor-side repair must have somewhere to land before suspending the
// current order. Strict and Community never offer this Modern fallback.
func TestAirFieldRepairNeedsLandingPoint(t *testing.T) {
	for _, mode := range []struct {
		name   string
		rules  Rules
		modern bool
	}{{"strict", StrictRules{}, false}, {"community", &CommunityRules{}, false}, {"modern", &ModernRules{}, true}} {
		for _, water := range []bool{false, true} {
			name := "land"
			if water {
				name = "water"
			}
			t.Run(mode.name+"/"+name, func(t *testing.T) {
				sys, w, u := wideAirFixture(t)
				sys.Rules = mode.rules
				sys.Terrain.SeaLevel = 10
				if water {
					sys.Terrain.SeaLevel = 20
				}
				u.Move.Mode, u.Health = 2, 50
				c := fieldRepairConstructor(t, w, 512, 512)
				n := pushAirOrder(t, u, "VTOL_Patrol", 900<<16, 512<<16)
				q := orders.QueueOfUnit(u)
				marker := sys.newPointMarker(u, Vec3{X: n.GoalX, Z: n.GoalZ})
				sys.installAirGoal(u, n, marker)
				n.DynamicGate = airLegGate
				econ := &economy.Service{}
				stock := [2]float32{100, 200}
				econ.Players[u.Owner].Stock = stock
				q.Binding().Economy = econ
				random := *q.Binding().SimRNG
				got := sys.airSeekFieldRepair(u, n, 100)
				want := mode.modern && !water
				if got != want {
					t.Fatalf("field repair admitted=%v, want %v", got, want)
				}
				if want {
					if q.LenPrimary() != 2 || q.Head().ID != orders.Lookup("VTOL_GetRepaired") || q.Head().Target != c.Handle {
						t.Fatal("landable constructor did not receive the repair order")
					}
					if code := sys.legFieldRepairModern(u, q.Head(), 0, 100); code != 2 || q.Head().DynamicGate == 0 {
						t.Fatal("admitted repair did not hold for its flight")
					}
				} else if q.LenPrimary() != 1 || q.Head() != n || n.DynamicGate != airLegGate || sys.FlightCommandFor(u.Handle, u).Payload != marker {
					t.Fatal("refused repair changed the suspended order")
				}
				if *q.Binding().SimRNG != random || u.Health != 50 {
					t.Fatal("field repair selection drew RNG or healed before landing")
				}
				if econ.Players[u.Owner].Stock != stock || *econ.UnitBuckets(u.Handle) != [2]economy.Bucket{} || *econ.UnitBuckets(c.Handle) != [2]economy.Bucket{} {
					t.Fatal("field repair selection charged resources before landing")
				}
			})
		}
	}
}

// Issue 131: a damaged aircraft cannot land beside a construction ship at sea.
// Completing that repair immediately used to restart the same patrol forever
// in one pump [04 R-ORD-01 §10]. Bound the test's dispatch count so a regression
// fails here instead of hanging the test process.
func TestAirPatrolUnlandableFieldRepairDoesNotRestart(t *testing.T) {
	sys, w, u := wideAirFixture(t)
	sys.Rules = &ModernRules{}
	sys.Terrain.SeaLevel = 20
	u.Move.Mode, u.Health = 2, 50
	fieldRepairConstructor(t, w, 512, 512)
	n := pushAirOrder(t, u, "VTOL_Patrol", 900<<16, 512<<16)
	n.Phase = 2
	sys.BindAirOrderLegs()
	q := orders.QueueOfUnit(u)
	handler := orders.DescriptorFor(n.ID).Handler
	visits := 0
	q.SetOwnedHandler(n.ID, func(u *units.Unit, n *orders.Node, satisfied uint32, tick uint32) (orders.Code, bool) {
		visits++
		if visits > 1 {
			t.Fatal("unlandable field repair restarted patrol in the same tick")
		}
		return handler(u, n, satisfied, tick), true
	})
	random := *q.Binding().SimRNG
	q.Pump(u, 100)
	if q.Head() != n || n.Phase != 2 || n.DynamicGate == 0 || n.Deadline != 130 {
		t.Fatal("patrol did not keep its ordinary flight and 30-tick wait")
	}
	if *q.Binding().SimRNG != random {
		t.Fatal("failed field repair drew RNG")
	}
}

// A nearer constructor over water must not hide another usable constructor.
func TestAirFieldRepairSkipsUnlandableConstructor(t *testing.T) {
	sys, w, u := wideAirFixture(t)
	sys.Rules = &ModernRules{}
	sys.Terrain.SeaLevel = 10
	u.Move.Mode, u.Health = 2, 50
	for z := int32(0); z < sys.Terrain.CellH; z++ {
		for x := int32(0); x < 64; x++ {
			cell := sys.Terrain.PlotAt(x, z)
			cell.SetHeight(0)
			cell.SetMinHeight(0)
			cell.SetMaxHeight(0)
		}
	}
	fieldRepairConstructor(t, w, 512, 512)
	land := fieldRepairConstructor(t, w, 1500, 512)
	n := pushAirOrder(t, u, "VTOL_Patrol", 900<<16, 512<<16)
	if !sys.airSeekFieldRepair(u, n, 100) || orders.QueueOfUnit(u).Head().Target != land.Handle {
		t.Fatal("unlandable constructor hid a usable field repair destination")
	}
}
