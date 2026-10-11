package orders

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

type firingFixture struct {
	q                              *Queue
	u, target                      *units.Unit
	n                              *Node
	installs, destroys, previews   int
	blocked, candidates, installOK bool
	point                          PointGoalRequest
	bound                          *Node
}

func newFiringFixture() *firingFixture {
	q, u, target := dangerFixture(true)
	u.Move.Mode = 1
	n := &Node{ID: Lookup("Attack_Chase"), Owner: u.Handle, Target: target.Handle, Phase: 1, DynamicGate: 0x13808, Deadline: -1, GoalX: u.X, GoalZ: u.Z, Param2: 6}
	q.primary = []*Node{n, {ID: Lookup("Move_Ground"), Owner: u.Handle, GoalX: numeric.FixedFromInt(123)}}
	f := &firingFixture{q: q, u: u, target: target, n: n, blocked: true, candidates: true, installOK: true}
	b := q.Binding()
	b.SetDangerRouteFeasible(func(*units.Unit, numeric.Fixed, numeric.Fixed) bool { return true })
	b.World.SetTerrainHeight(func(numeric.Fixed, numeric.Fixed) (numeric.Fixed, bool) { return 0, true })
	b.Weapons = NewWeaponAdapter(WeaponAdapterConfig{
		FiringPositionBlocked: func(shooter, target *units.Unit, tick uint32) bool {
			return f.blocked && shooter == f.u && target == f.target
		},
		FiringPositionClear: func(_, _ *units.Unit, _ uint32, x, y, z numeric.Fixed) bool {
			f.previews++
			return f.candidates
		},
	})
	b.Movement = NewMovementGoalAdapter(MovementGoalAdapterConfig{
		InstallPoint: func(p PointGoalRequest) bool {
			f.installs++
			f.point = p
			if f.installOK {
				f.bound = p.Node
			}
			return f.installOK
		},
		Destroy: func(n *Node) bool {
			f.destroys++
			if f.bound == n {
				f.bound = nil
			}
			return true
		},
		Release: func(*Node) bool { panic("overlay must use identity-safe destruction") },
	})
	return f
}

func (f *firingFixture) step(tick uint32) bool {
	return f.q.Binding().rules().StepFiringPosition(f.u, tick)
}

func TestFiringPositionKeepsAttackAndSecondaryPump(t *testing.T) {
	f := newFiringFixture()
	before := *f.n
	random := *f.q.Binding().SimRNG
	rearCalls := 0
	restore := setHandler(Lookup("BuildWeapon"), func(*units.Unit, *Node, uint32, uint32) Code { rearCalls++; return 5 })
	defer restore()
	f.q.secondary = []*Node{{ID: Lookup("BuildWeapon"), Owner: f.u.Handle, Deadline: -1}}
	f.q.Pump(f.u, 10)
	if !f.q.firingPosition.active || f.installs != 1 || rearCalls != 1 || len(f.q.secondary) != 0 {
		t.Fatal("overlay did not suspend only primary")
	}
	if f.n.Target != before.Target || f.n.GoalX != before.GoalX || f.n.GoalZ != before.GoalZ || f.n.Param2 != before.Param2 || f.n.Phase != before.Phase || len(f.q.primary) != 2 {
		t.Fatal("attack payload or queue changed")
	}
	if f.point.X != f.u.X+numeric.FixedFromInt(16) || f.point.Z != f.u.Z || f.point.Node != f.n || f.point.Radius != 0 {
		t.Fatal("point search order or ownership changed")
	}
	if !f.step(11) || f.installs != 1 {
		t.Fatal("active route was restarted")
	}
	if *f.q.Binding().SimRNG != random {
		t.Fatal("overlay drew RNG")
	}
}

func TestFiringPositionEligibility(t *testing.T) {
	cases := map[string]func(*firingFixture){
		"strict":                  func(f *firingFixture) { f.q.Binding().Rules = StrictRules{} },
		"community":               func(f *firingFixture) { f.q.Binding().Rules = CommunityRules{} },
		"dying shooter":           func(f *firingFixture) { f.u.Dying = true },
		"dying target":            func(f *firingFixture) { f.target.Dying = true },
		"non-ground mover":        func(f *firingFixture) { f.u.Move.Mode = 2 },
		"no refusal":              func(f *firingFixture) { f.blocked = false },
		"automatic hold position": func(f *firingFixture) { f.n.automaticAttack = true; f.u.Flags &^= 3 << units.StandingMoveShift },
		"automatic hold fire":     func(f *firingFixture) { f.n.automaticAttack = true; f.u.Flags &^= 3 << units.StandingFireShift },
		"air":                     func(f *firingFixture) { f.u.Def.CanFly = true },
		"immobile":                func(f *firingFixture) { f.u.Def.CanMove = false },
		"stunned":                 func(f *firingFixture) { f.u.Stunned = true },
		"incomplete":              func(f *firingFixture) { f.u.Remaining = 1 },
		"carried":                 func(f *firingFixture) { f.u.Attachment.Carrier = 5 },
		"stationary order":        func(f *firingFixture) { f.n.ID = Lookup("Attack_NoMove") },
		"hidden": func(f *firingFixture) {
			f.q.Binding().SetDangerVisible(func(*units.Unit, *units.Unit) bool { return false })
		},
		"friendly":                 func(f *firingFixture) { f.target.Owner = f.u.Owner },
		"target gone":              func(f *firingFixture) { f.n.Satisfied |= pendTargetGone },
		"unit pending disengage":   func(f *firingFixture) { f.u.Pending |= pendDisengage },
		"unit pending target loss": func(f *firingFixture) { f.u.Pending |= pendTargetGone },
		"disengage":                func(f *firingFixture) { f.n.Satisfied |= pendDisengage },
	}
	for name, modify := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFiringFixture()
			modify(f)
			before := *f.n
			random := *f.q.Binding().SimRNG
			if f.step(10) || f.installs != 0 || !reflect.DeepEqual(*f.n, before) || *f.q.Binding().SimRNG != random {
				t.Fatal("ineligible attack changed")
			}
		})
	}
	f := newFiringFixture()
	f.u.Flags &^= 3 << units.StandingFireShift
	if !f.step(10) {
		t.Fatal("explicit attack must keep its Hold Fire permission")
	}
	// An explicit chase already walks into range on Hold Position.
	f = newFiringFixture()
	f.u.Flags &^= 3 << units.StandingMoveShift
	if !f.step(10) {
		t.Fatal("explicit attack must reposition on Hold Position")
	}
}

func TestFiringPositionBoundedSearchAndRetry(t *testing.T) {
	f := newFiringFixture()
	f.candidates = false
	if f.step(10) || f.previews != 32 {
		t.Fatal("search was not bounded to 32 candidates")
	}
	f.candidates = true
	if f.step(39) || f.previews != 32 {
		t.Fatal("retry before cooldown")
	}
	if !f.step(40) {
		t.Fatal("retry did not run at cooldown boundary")
	}
	f.n.MoveState = MoveBlocked
	if f.step(41) || f.destroys != 1 || f.n.Phase != 1 || f.n.DynamicGate != 0 {
		t.Fatal("failure did not release/restart chase")
	}
	if f.step(70) || !f.step(71) {
		t.Fatal("failure cooldown changed")
	}
}

func TestFiringPositionLeashAndRefusedInstall(t *testing.T) {
	f := newFiringFixture()
	f.n.GuardX, f.n.GuardY = 512, 512
	f.n.Param3 = 16
	f.q.Binding().SetDangerRouteFeasible(func(_ *units.Unit, x, z numeric.Fixed) bool { return x == f.u.X || z == f.u.Z })
	if f.step(10) || f.installs != 0 {
		t.Fatal("candidate at leash equality was admitted")
	}
	f = newFiringFixture()
	f.installOK = false
	if f.step(10) || f.q.firingPosition.active || f.n.MoveState != 0 {
		t.Fatal("refused goal suspended primary")
	}
}

func TestFiringPositionRetiresOnCompletionAndIdentityChanges(t *testing.T) {
	cases := map[string]func(*firingFixture){
		"arrival":                  func(f *firingFixture) { f.n.MoveState = MoveArrived },
		"arrival pending":          func(f *firingFixture) { f.n.Satisfied |= 0x20 },
		"unit pending disengage":   func(f *firingFixture) { f.u.Pending |= pendDisengage },
		"unit pending target loss": func(f *firingFixture) { f.u.Pending |= pendTargetGone },
		"strict switch":            func(f *firingFixture) { f.q.Binding().Rules = StrictRules{} },
		"automatic hold position": func(f *firingFixture) {
			f.n.automaticAttack = true
			f.u.Flags &^= 3 << units.StandingMoveShift
		},
		"hidden": func(f *firingFixture) {
			f.q.Binding().SetDangerVisible(func(*units.Unit, *units.Unit) bool { return false })
		},
		"reused target": func(f *firingFixture) {
			replacement := *f.target
			old := f.q.Binding().LookupHook()
			f.q.Binding().SetLookup(func(h pool.Handle) *units.Unit {
				if h == replacement.Handle {
					return &replacement
				}
				return old(h)
			})
		},
		"displaced": func(f *firingFixture) {
			successor := &Node{ID: Lookup("Paralyze"), Owner: f.u.Handle}
			f.q.primary = append([]*Node{successor}, f.q.primary...)
			f.bound = successor
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFiringFixture()
			if !f.step(10) {
				t.Fatal("start failed")
			}
			change(f)
			bound := f.bound
			control := (f.n.Satisfied | f.u.Pending) & (pendTargetGone | pendDisengage)
			if f.step(11) || f.q.firingPosition.active || f.destroys != 1 || f.n.Phase != 1 || f.n.DynamicGate != control || f.n.Target != f.target.Handle {
				t.Fatal("cleanup lost attack or left maneuver active")
			}
			if name == "displaced" && f.bound != bound {
				t.Fatal("displaced cleanup unbound successor")
			}
		})
	}
	f := newFiringFixture()
	f.step(10)
	if !f.step(189) || f.step(190) {
		t.Fatal("travel timeout boundary changed")
	}
	f = newFiringFixture()
	f.step(10)
	f.u.Flags &^= 3 << units.StandingMoveShift
	if !f.step(11) || f.destroys != 0 {
		t.Fatal("Hold Position retired an explicit attack's maneuver")
	}
}

func TestFiringPositionRemovalAndNewCommand(t *testing.T) {
	f := newFiringFixture()
	f.step(10)
	f.q.RemovePrimaryNode(f.n, false)
	if f.q.firingPosition.node != nil || f.bound != nil {
		t.Fatal("removal retained overlay")
	}
	f = newFiringFixture()
	f.step(10)
	f.q.lastPumpTick = 11
	f.q.Push(Lookup("Move_Ground"), NewMoveNode(Lookup("Move_Ground"), 0, 0, 11, f.u.Handle, true))
	if f.q.firingPosition.active || f.bound != nil || f.q.indexOfPrimary(f.n) < 0 || f.n.Target != f.target.Handle {
		t.Fatal("queued command failed to retire travel or lost attack")
	}
}

func TestFiringPositionAutomaticStationaryResponseKeepsManeuverPost(t *testing.T) {
	for _, danger := range []bool{false, true} {
		f := newFiringFixture()
		f.n.ID = Lookup("Attack_NoMove")
		f.n.Phase = 2
		if danger {
			f.q.danger.response = f.n
		} else {
			f.n.automaticAttack = true
		}
		if !f.step(10) {
			t.Fatal("roaming automatic stationary attack did not reposition")
		}
		f.n.MoveState = MoveArrived
		f.step(11)
		if f.n.Phase != 1 || f.n.Target != f.target.Handle {
			t.Fatal("stationary attack did not restart binding")
		}
	}
	f := newFiringFixture()
	f.n.ID = Lookup("Attack_NoMove")
	f.q.danger.response = f.n
	f.u.Flags = f.u.Flags&^(uint32(3)<<units.StandingMoveShift) | 1<<units.StandingMoveShift
	if f.step(10) {
		t.Fatal("maneuver without anchor moved")
	}
	f.q.danger.anchored = true
	f.q.danger.anchorX, f.q.danger.anchorZ = f.u.X, f.u.Z
	f.u.Def.ManeuverLeashLength = 16
	f.q.Binding().SetDangerRouteFeasible(func(_ *units.Unit, x, z numeric.Fixed) bool { return x == f.u.X || z == f.u.Z })
	if f.step(11) {
		t.Fatal("stationary maneuver reached leash equality")
	}
	f.u.Def.ManeuverLeashLength = 32
	if !f.step(41) || !f.q.danger.withdrew {
		t.Fatal("stationary maneuver lost its existing return-to-post policy")
	}
}

func TestFiringPositionPumpDeliversCancellationBeforeRestart(t *testing.T) {
	for _, name := range []string{"Attack_Chase", "Attack_NoMove"} {
		for _, event := range []struct {
			name string
			bit  uint32
		}{{"disengage", pendDisengage}, {"target removed", pendTargetRemoved}, {"target cloaked", pendTargetCloaked}} {
			for _, fromUnit := range []bool{false, true} {
				origin := "record"
				if fromUnit {
					origin = "unit"
				}
				t.Run(name+"/"+event.name+"/"+origin, func(t *testing.T) {
					f := newFiringFixture()
					f.n.ID = Lookup(name)
					f.n.automaticAttack = true
					if !f.step(10) {
						t.Fatal("start failed")
					}
					successor := f.q.primary[1]
					successor.DynamicGate, successor.Deadline = 0x400, -1
					// Movement outcomes belong to the finished temporary goal, while the
					// control event must still reach the actual attack's pre-check.
					f.n.Satisfied |= 0x3E0
					if fromUnit {
						f.u.Pending |= event.bit | 0x3E0
					} else {
						f.n.Satisfied |= event.bit
					}
					f.q.Binding().Movement.SetRelease(func(*Node) bool { return true })
					f.q.Binding().Weapons.SetCanEngage(func(*units.Unit, pool.Handle, int) bool { return true })
					handler := DescriptorFor(f.n.ID).Handler
					calls := 0
					restore := setHandler(f.n.ID, func(u *units.Unit, n *Node, satisfied, tick uint32) Code {
						calls++
						if satisfied != event.bit {
							t.Errorf("handler satisfied=%#x, want only control event %#x", satisfied, event.bit)
						}
						return handler(u, n, satisfied, tick)
					})
					defer restore()
					random := *f.q.Binding().SimRNG
					f.q.Pump(f.u, 11)
					if calls != 1 || f.q.indexOfPrimary(f.n) >= 0 || f.q.Head() != successor || f.q.firingPosition.active {
						t.Fatal("cancelled attack restarted or disturbed its successor")
					}
					if f.u.Pending&event.bit != 0 || f.n.Satisfied&event.bit != 0 {
						t.Fatal("control event was not consumed by ordinary pump")
					}
					if *f.q.Binding().SimRNG != random {
						t.Fatal("cancellation drew RNG")
					}
				})
			}
		}
	}
}

func TestFiringPositionDisplacedCleanupLeavesSuccessorControlEvents(t *testing.T) {
	f := newFiringFixture()
	if !f.step(10) {
		t.Fatal("start failed")
	}
	successor := &Node{ID: Lookup("Paralyze"), Owner: f.u.Handle}
	f.q.primary = append([]*Node{successor}, f.q.primary...)
	f.bound = successor
	f.u.Pending = pendDisengage | pendTargetGone
	if f.step(11) || f.n.DynamicGate != 0 || f.n.Satisfied&(pendDisengage|pendTargetGone) != 0 || f.u.Pending != pendDisengage|pendTargetGone || f.bound != successor {
		t.Fatal("displaced attack took its successor's control events or goal")
	}
}
