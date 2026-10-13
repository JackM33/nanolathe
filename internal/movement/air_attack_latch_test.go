package movement

import (
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"testing"
)

// Preparation must inhibit every slot without prematurely binding the target;
// each executor's firing leg takes only slot zero [04 R-AIR-01 §8].
func TestAirAttackPreparationUsesSlotVerbs(t *testing.T) {
	for _, name := range []string{"AirStrike", "AirToGround", "AirToGroundHover", "AirToAir"} {
		t.Run(name, func(t *testing.T) {
			sys, w, u := wideAirFixture(t)
			target := airTargetFor(t, sys, w, 30, 16)
			q := orders.QueueForUnit(u)
			q.Binding().Weapons = orders.NewWeaponAdapter(orders.WeaponAdapterConfig{InhibitSlot: combat.InhibitWeaponSlot, ReleaseSlot: combat.ReleaseWeaponSlot, SetManualTarget: combat.SetManualWeaponTarget, FireTarget: combat.FireWeaponTarget, FirePoint: combat.FireWeaponPoint, StopFiring: combat.StopWeaponFiring})
			for i := range u.Slots {
				u.InstallWeapon(i, &content.WeaponDef{Range: 1000})
				combat.ReleaseWeaponSlot(u, i)
				combat.SetManualWeaponTarget(u, i, target.Handle)
			}
			q.Push(orders.Lookup(name), orders.Node{Owner: u.Handle, Target: target.Handle, Phase: 1, GoalX: target.X, GoalY: target.Y, GoalZ: target.Z, GoalSupplied: true})
			n := q.Head()
			switch name {
			case "AirStrike":
				sys.legAirStrike(u, n, 0, 100)
			case "AirToGround":
				sys.legAirToGround(u, n, 100)
			case "AirToGroundHover":
				sys.legAirToGroundHover(u, n, 100)
			case "AirToAir":
				sys.legAirToAir(u, n, 0, 100)
			}
			for i := range u.Slots {
				slot := &u.Slots[i]
				wantTarget := units.TargetNone
				wantAutonomy := true
				if i == 0 && (name == "AirStrike" || name == "AirToAir") {
					wantAutonomy = false
				}
				if i == 0 && name == "AirToAir" {
					wantTarget = units.TargetUnit
				}
				if slot.Target.Kind != wantTarget || (slot.Flags&units.SlotFlagAutonomous != 0) != wantAutonomy {
					t.Fatalf("phase1 slot%d target=%v flags=%x", i, slot.Target, slot.Flags)
				}
			}
			if name == "AirToGround" || name == "AirToGroundHover" {
				n.Phase = 2
				if name == "AirToGround" {
					sys.legAirToGround(u, n, 101)
				} else {
					sys.legAirToGroundHover(u, n, 101)
				}
				if u.Slots[0].Target.Unit != target.Handle {
					t.Fatal("release did not bind primary target")
				}
				for i := 1; i < units.NumSlots; i++ {
					if u.Slots[i].Target.Kind != units.TargetNone {
						t.Fatalf("release bound nonprimary slot%d", i)
					}
				}
			}
		})
	}
}

// A bomber must not create a projectile during repositioning. Its release leg
// arms a point shot; the break clears it without restoring autonomy [04 R-AIR-01 §8].
func TestAirStrikeFiresOnlyAfterRelease(t *testing.T) {
	sys, w, u := wideAirFixture(t)
	target := airTargetFor(t, sys, w, 30, 16)
	u.Y = 100 << 16
	weapon := &content.WeaponDef{ID: 41, Range: 1000, Dropped: true, ReloadTime: 5}
	for i := range u.Slots {
		u.InstallWeapon(i, weapon)
		combat.ReleaseWeaponSlot(u, i)
	}
	q := orders.QueueForUnit(u)
	q.Binding().Weapons = orders.NewWeaponAdapter(orders.WeaponAdapterConfig{InhibitSlot: combat.InhibitWeaponSlot, ReleaseSlot: combat.ReleaseWeaponSlot, SetManualTarget: combat.SetManualWeaponTarget, FireTarget: combat.FireWeaponTarget, FirePoint: combat.FireWeaponPoint, StopFiring: combat.StopWeaponFiring})
	q.Push(orders.Lookup("AirStrike"), orders.Node{Owner: u.Handle, Target: target.Handle, Phase: 1, GoalX: target.X, GoalY: target.Y, GoalZ: target.Z, GoalSupplied: true})
	n := q.Head()
	sys.legAirStrike(u, n, 0, 100)
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"bomb": weapon}}
	cat.RebuildWeaponIndex()
	var svc combat.Service
	if got := svc.StepWeaponsForUnit(u, 101, w, nil, sys.Terrain, nil, cat, q.Binding().SimRNG, nil).Fired; got != 0 {
		t.Fatalf("bombs fired before release: %d", got)
	}
	n.Phase = 5
	sys.legAirStrike(u, n, 0, 102)
	if u.Slots[0].Target.Kind != units.TargetGround {
		t.Fatal("release failed to install primary point target")
	}
	if got := svc.StepWeaponsForUnit(u, 103, w, nil, sys.Terrain, nil, cat, q.Binding().SimRNG, nil).Fired; got != 1 {
		t.Fatalf("release fired %d slots, want primary only", got)
	}
	n.Phase = 6
	flags := u.Slots[0].Flags
	sys.legAirStrike(u, n, 0, 104)
	if u.Slots[0].Target.Kind != units.TargetNone || u.Slots[0].Flags != flags {
		t.Fatal("break must clear target without changing control")
	}
	u.Slots[0].Reload = 0
	if got := svc.StepWeaponsForUnit(u, 105, w, nil, sys.Terrain, nil, cat, q.Binding().SimRNG, nil).Fired; got != 0 {
		t.Fatalf("bombs fired after break: %d", got)
	}
}

// AirStrike's point slot can still belong to a unit-target order [04 R-AIR-01
// §8]. Only an issued ground attack receives Modern's obstruction exception
// (DESIGN_WEAPONS_PROJECTILES §2.3.2), including on burst-capable mixed flags.
func TestAirStrikeRetainsAttackIntent(t *testing.T) {
	for _, ground := range []bool{false, true} {
		sys, w, u := wideAirFixture(t)
		target := airTargetFor(t, sys, w, 30, 16)
		blocker := airTargetFor(t, sys, w, 23, 16)
		blocker.Owner = u.Owner
		blocker.Def.ModelTop, blocker.Def.ModelTopFixed = 128, 128<<16
		sys.Terrain.PlotAt(23, 16).SetOccupantA(int16(blocker.Handle))
		u.Y = 100 << 16
		weapon := &content.WeaponDef{ID: 41, Range: 1000, Dropped: true, LineOfSight: true,
			WeaponVelocity: 32 << 16, Tolerance: 32767, AreaOfEffect: 32, ReloadTime: 5, Burst: 2, BurstRate: 3}
		u.InstallWeapon(0, weapon)
		q := orders.QueueForUnit(u)
		q.Binding().Weapons = orders.NewWeaponAdapter(orders.WeaponAdapterConfig{ReleaseSlot: combat.ReleaseWeaponSlot, FirePoint: combat.FireWeaponPoint})
		n := orders.Node{Owner: u.Handle, Target: target.Handle, Phase: 5,
			GoalX: target.X, GoalY: target.Y, GoalZ: target.Z, GoalSupplied: true}
		if ground {
			n.Target = 0
		}
		q.Push(orders.Lookup("AirStrike"), n)
		sys.legAirStrike(u, q.Head(), 0, 100)
		if u.Slots[0].Target.Kind != units.TargetGround {
			t.Fatal("release failed to install primary point target")
		}
		cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"bomb": weapon}}
		cat.RebuildWeaponIndex()
		svc := combat.Service{Rules: &combat.ModernRules{}}
		want := 0
		if ground {
			want = 1
		}
		if got := svc.StepWeaponsForUnit(u, 101, w, nil, sys.Terrain, nil, cat, q.Binding().SimRNG, nil).Fired; got != want {
			t.Fatalf("ground=%v: fired %d through friendly blocker, want %d", ground, got, want)
		}
		if !ground {
			// Once the path clears, the enemy order may fire but its burst
			// must not acquire ground permission from the derived point slot.
			sys.Terrain.PlotAt(23, 16).SetOccupantA(0)
			if got := svc.StepWeaponsForUnit(u, 102, w, nil, sys.Terrain, nil, cat, q.Binding().SimRNG, nil).Fired; got != 1 {
				t.Fatalf("clear enemy attack fired %d slots", got)
			}
		}
		anchor := &svc.Records[0]
		if anchor.BurstRemaining != 2 || !anchor.OrderedBurst || anchor.GroundAttackBurst != ground {
			t.Fatalf("ground=%v: burst lost original attack intent", ground)
		}
	}
}

// Nanolathe Modern policy (DESIGN_MOVEMENT_PATH "Modern aircraft tactics"):
// the Modern strafe and hover pilots start an attack as retail's phases 1 and
// 2 do [04 R-AIR-01 §8]. Every slot is handed to its own scan and only slot
// zero is bound, so a fighter's second launcher keeps choosing its own
// targets, such as anti-air passed on the run.
func TestModernAirAttackKeepsSecondaryScan(t *testing.T) {
	for _, name := range []string{"AirToGround", "AirToGroundHover"} {
		t.Run(name, func(t *testing.T) {
			sys, w, u := wideAirFixture(t)
			target := airTargetFor(t, sys, w, 30, 16)
			q := orders.QueueForUnit(u)
			q.Binding().Weapons = orders.NewWeaponAdapter(orders.WeaponAdapterConfig{InhibitSlot: combat.InhibitWeaponSlot, ReleaseSlot: combat.ReleaseWeaponSlot, SetManualTarget: combat.SetManualWeaponTarget, FireTarget: combat.FireWeaponTarget, FirePoint: combat.FireWeaponPoint, StopFiring: combat.StopWeaponFiring})
			for i := range u.Slots {
				u.InstallWeapon(i, &content.WeaponDef{Range: 1000})
				combat.ReleaseWeaponSlot(u, i)
			}
			q.Push(orders.Lookup(name), orders.Node{Owner: u.Handle, Target: target.Handle, Phase: 1, GoalX: target.X, GoalY: target.Y, GoalZ: target.Z, GoalSupplied: true})
			n := q.Head()
			if name == "AirToGround" {
				sys.legAirToGroundModern(u, n, 100)
			} else {
				sys.legAirToGroundHoverModern(u, n, 100)
			}
			if u.Slots[0].Target.Unit != target.Handle || u.Slots[0].Flags&units.SlotFlagAutonomous != 0 {
				t.Fatalf("slot0 target=%v flags=%x, want bound to the attack", u.Slots[0].Target, u.Slots[0].Flags)
			}
			for i := 1; i < units.NumSlots; i++ {
				if s := &u.Slots[i]; s.Target.Kind != units.TargetNone || s.Flags&units.SlotFlagAutonomous == 0 {
					t.Fatalf("slot%d target=%v flags=%x, want free to scan", i, s.Target, s.Flags)
				}
			}
		})
	}
}
