package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func obstructionFixture(t *testing.T) ShotQuery {
	t.Helper()
	w, terrain := newContactFixture(t)
	muzzle, aim := modernTerrainPoints()
	def := contactDef(contactModelTop)
	def.StandingFireOrder = 2
	// A mobile shooter without a queue may still step aside, so obstruction
	// holds it (§2.3.2 "No room").
	def.CanMove = true
	h, err := w.Create(def, 0, muzzle.X, muzzle.Y, muzzle.Z)
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{Rules: &ModernRules{}, Reaction: NewReactionSeams(ReactionSeamsConfig{Allied: func(a, b uint8) bool { return a == b || (a == 0 && b == 2) }})}
	return ShotQuery{Service: svc, World: w, Shooter: w.Unit(h), Terrain: terrain,
		Launch: Slot{Weapon: modernTerrainWeapon()}, Muzzle: muzzle, Aim: aim, Tick: 10, Wind: &world.Wind{}}
}

type groundAttackIntent bool

func (intent groundAttackIntent) ExplicitGroundAttack(*units.Unit) bool { return bool(intent) }

type automaticAttackIntent struct{ groundAttackIntent }

func (automaticAttackIntent) AutomaticAttack() bool { return true }

// Nanolathe Modern policy: DESIGN_WEAPONS_PROJECTILES §2.3.2. Explicit ground
// fire and the intended friendly target are allowed; intervening friends on
// a unit-target shot still block, even when an order owns that slot.
func TestModernOrderedShotObstruction(t *testing.T) {
	for _, mode := range []struct {
		name  string
		rules Rules
	}{
		{"modern", &ModernRules{}},
		{"community", CommunityRules{}},
		{"strict", StrictRules{}},
	} {
		for _, tc := range []struct {
			name                      string
			ordered, ground, command  bool
			derivedPoint, automatic   bool
			targetOwner               uint8
			blockerOwner              int
			feature, terrain, blocked bool
		}{
			{name: "ground through own building", ordered: true, ground: true, targetOwner: 1, blockerOwner: 0},
			{name: "D-gun through own building", ordered: true, ground: true, command: true, targetOwner: 1, blockerOwner: 0},
			{name: "ground through ally", ordered: true, ground: true, targetOwner: 1, blockerOwner: 2},
			{name: "own target", ordered: true, targetOwner: 0, blockerOwner: -1},
			{name: "allied target", ordered: true, command: true, targetOwner: 2, blockerOwner: -1},
			{name: "enemy behind own building", ordered: true, targetOwner: 1, blockerOwner: 0, blocked: true},
			{name: "enemy behind ally", ordered: true, command: true, targetOwner: 1, blockerOwner: 2, blocked: true},
			{name: "friendly target behind another friend", ordered: true, targetOwner: 2, blockerOwner: 0, blocked: true},
			{name: "autonomous ground", ground: true, targetOwner: 1, blockerOwner: 0, blocked: true},
			{name: "autonomous friendly target", targetOwner: 0, blockerOwner: -1, blocked: true},
			{name: "automatic order on now-friendly target", ordered: true, automatic: true, targetOwner: 0, blockerOwner: -1, blocked: true},
			{name: "automatic order using ground slot", ordered: true, automatic: true, ground: true, targetOwner: 1, blockerOwner: 0, blocked: true},
			{name: "ground at wreck", ordered: true, ground: true, targetOwner: 1, blockerOwner: -1, feature: true},
			{name: "enemy behind wreck", ordered: true, targetOwner: 1, blockerOwner: -1, feature: true, blocked: true},
			{name: "ground behind ridge", ordered: true, ground: true, targetOwner: 1, blockerOwner: -1, terrain: true, blocked: true},
			{name: "enemy attack using point slot", ordered: true, ground: true, derivedPoint: true, targetOwner: 1, blockerOwner: 0, blocked: true},
		} {
			t.Run(mode.name+"/"+tc.name, func(t *testing.T) {
				q := obstructionFixture(t)
				q.Service.Rules = mode.rules
				q.Shooter.Orders = groundAttackIntent(tc.ground && !tc.derivedPoint)
				if tc.automatic {
					q.Shooter.Orders = automaticAttackIntent{groundAttackIntent(tc.ground)}
				}
				weapon := q.Launch.Weapon
				weapon.Turret, weapon.Accuracy, weapon.ReloadTime = true, 128, 30
				weapon.EnergyPerShot, weapon.MetalPerShot = 100, 5
				weapon.CommandFire = tc.command
				idx := 0
				if tc.command {
					idx = 2
				}
				q.Shooter.InstallWeapon(idx, weapon)
				slot := q.Shooter.SlotAt(idx)
				if tc.ordered {
					slot.Flags &^= units.SlotFlagAutonomous
				}
				targetDef := contactDef(contactModelTop)
				targetDef.ModelTop = contactModelTop >> 16
				target, err := q.World.Create(targetDef, tc.targetOwner, q.Aim.X, 0, q.Aim.Z)
				if err != nil {
					t.Fatal(err)
				}
				stampGroundRect(q.Terrain, 12, 1, 1, 1, target)
				slot.Target = units.Target{Kind: units.TargetUnit, Unit: target}
				if tc.ground {
					slot.Target = units.Target{Kind: units.TargetGround, X: q.Aim.X, Z: q.Aim.Z}
				}
				if tc.blockerOwner >= 0 {
					h, err := q.World.Create(contactDef(contactModelTop), uint8(tc.blockerOwner), cellCentre(4), 0, cellCentre(1))
					if err != nil {
						t.Fatal(err)
					}
					stampGroundRect(q.Terrain, 4, 1, 1, 1, h)
				}
				if tc.feature {
					q.Terrain.FeatureDefs[0].Height = 64
					q.Terrain.PlotAt(4, 1).SetFeature(0)
				}
				if tc.terrain {
					q.Terrain.PlotAt(4, 1).SetMinHeight(64)
				}
				var econ economy.Service
				econ.Players[0].Stock = [2]float32{50, 500}
				random := rng.NewSimulation(7)
				before, pending := random, q.Shooter.Pending
				var summary UnitStepSummary
				q.Service.firePreparedSlot(q.Shooter, slot, idx, &slotPrep{weapon: weapon, tgtPos: q.Aim}, q.Tick, q.Terrain, &econ, &random, q.World, nil, &summary)
				if mode.name == "modern" && tc.blocked {
					if summary.Fired != 0 || q.Service.Count() != 0 || slot.Reload != 0 || random != before || q.Shooter.Pending != pending || econ.Players[0].Stock != [2]float32{50, 500} {
						t.Fatal("blocked shot spent resources, randomness or order state")
					}
				} else if summary.Fired != 1 || q.Service.Count() != 1 || slot.Reload != 30 || random.Draws()-before.Draws() != 2 || econ.Players[0].Stock != [2]float32{45, 400} {
					t.Fatalf("ordered/retail shot failed: fired=%d count=%d reload=%d draws=%d stock=%v", summary.Fired, q.Service.Count(), slot.Reload, random.Draws()-before.Draws(), econ.Players[0].Stock)
				}
			})
		}
	}
}

// These lock Nanolathe Modern policy, not a change to retail contact [06 §8.1].
func TestModernFriendlyShotObstruction(t *testing.T) {
	for _, tc := range []struct {
		name    string
		owner   uint8
		air     bool
		y, top  int64
		cell    int32
		blocked bool
	}{
		{"own unit", 0, false, 0, 40, 4, true},
		{"allied player", 2, false, 0, 40, 4, true},
		{"enemy remains shootable", 1, false, 0, 40, 4, false},
		{"ground upper equality clears", 0, false, 0, 16, 4, false},
		{"air lower equality blocks", 0, true, 16, 16, 4, true},
		{"air upper equality blocks", 2, true, 0, 16, 4, true},
		{"air above shot clears", 2, true, 17, 16, 4, false},
		{"air below shot clears", 2, true, 0, 15, 4, false},
		{"beyond target clears", 2, false, 0, 40, 14, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := obstructionFixture(t)
			h, err := q.World.Create(contactDef(int32(tc.top<<16)), tc.owner, cellCentre(tc.cell), numeric.FixedFromInt(tc.y), cellCentre(1))
			if err != nil {
				t.Fatal(err)
			}
			if tc.air {
				stampAirRect(q.Terrain, tc.cell, 1, 1, 1, h)
			} else {
				stampGroundRect(q.Terrain, tc.cell, 1, 1, 1, h)
			}
			// Three-cell movement used to skip this one-cell obstruction.
			q.Launch.Weapon.WeaponVelocity = 48 << 16
			if got := q.Service.rules().AdmitShot(&q); got == tc.blocked || q.Blocked != tc.blocked {
				t.Fatalf("admitted=%v blocked=%v want blocked=%v", got, q.Blocked, tc.blocked)
			}
			if !(StrictRules{}).AdmitShot(&q) {
				t.Fatal("Strict refused a shot")
			}
		})
	}
}

func TestModernFeatureShotObstruction(t *testing.T) {
	for _, tc := range []struct {
		name              string
		fringe, unitsOnly bool
		height            int32
		blocked           bool
	}{
		{"wreck", false, false, 32, true},
		{"wreck fringe", true, false, 32, true},
		{"feature upper equality clears", false, false, 16, false},
		{"units only passes features", false, true, 32, false},
		{"authored height narrows to byte", false, false, 272, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := obstructionFixture(t)
			q.Terrain.FeatureDefs[0].Height = tc.height
			q.Terrain.PlotAt(3, 1).SetFeature(0)
			if tc.fringe {
				q.Terrain.PlotAt(3, 1).SetFeature(world.PlotFeatureNone)
				q.Terrain.PlotAt(3, 2).SetFeature(0)
				q.Terrain.PlotAt(3, 1).SetFeature(world.PlotFeatureFringe)
				q.Terrain.PlotAt(3, 1).SetAnchorSigned(0, 1)
			}
			q.Launch.Weapon.WeaponVelocity = 48 << 16
			q.Launch.Weapon.UnitsOnly = tc.unitsOnly
			if got := modernObstructedShot(&q); got != tc.blocked {
				t.Fatalf("blocked=%v want %v", got, tc.blocked)
			}
			q.Terrain.PlotAt(3, 1).SetFeature(world.PlotFeatureNone)
			if modernObstructedShot(&q) {
				t.Fatal("removed wreck still blocks")
			}
		})
	}
}

func TestModernOrderedBurstObstruction(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		ordered, unit, blocker bool
		automatic              bool
		targetOwner            uint8
		blocked                bool
	}{
		{name: "ordered ground", ordered: true, blocker: true},
		{name: "autonomous ground", blocker: true, blocked: true},
		{name: "ordered own target", ordered: true, unit: true},
		{name: "ordered allied target", ordered: true, unit: true, targetOwner: 2},
		{name: "autonomous own target", unit: true, blocked: true},
		{name: "automatic order on now-friendly target", ordered: true, automatic: true, unit: true, blocked: true},
		{name: "ordered enemy behind friend", ordered: true, unit: true, blocker: true, targetOwner: 1, blocked: true},
		{name: "ordered friend behind another friend", ordered: true, unit: true, blocker: true, targetOwner: 2, blocked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := obstructionFixture(t)
			weapon := q.Launch.Weapon
			weapon.Burst, weapon.BurstRate, weapon.SprayAngle, weapon.RandomDecay = 2, 3, 128, 4
			var target pool.Handle
			if tc.unit {
				var err error
				target, err = q.World.Create(contactDef(contactModelTop), tc.targetOwner, q.Aim.X, 0, q.Aim.Z)
				if err != nil {
					t.Fatal(err)
				}
				stampGroundRect(q.Terrain, 12, 1, 1, 1, target)
			}
			if tc.blocker {
				ally, err := q.World.Create(contactDef(contactModelTop), 2, cellCentre(4), 0, cellCentre(1))
				if err != nil {
					t.Fatal(err)
				}
				stampGroundRect(q.Terrain, 4, 1, 1, 1, ally)
			}
			h, ok := q.Service.Reserve()
			if !ok {
				t.Fatal("reserve burst template")
			}
			p := &q.Service.Records[int(h)-1]
			p.Shooter, p.ShooterSide = q.Shooter.Handle, q.Shooter.Owner
			InitOrdinary(p, weapon, 1, q.Muzzle, q.Aim, target)
			p.BurstRemaining, p.BurstDeadline, p.OrderedBurst = 2, q.Tick, tc.ordered
			p.GroundAttackBurst = tc.ordered && !tc.unit
			p.AutomaticAttackBurst = tc.automatic
			// The live slot no longer represents the burst's original order.
			q.Shooter.InstallWeapon(0, weapon)
			q.Shooter.SlotAt(0).Flags &^= units.SlotFlagAutonomous
			q.Shooter.SlotAt(0).Target = units.Target{Kind: units.TargetGround, X: q.Aim.X, Z: q.Aim.Z}
			random := rng.NewSimulation(7)
			lookup := func(id int32) (*content.WeaponDef, bool) { return weapon, id == weapon.ID }
			clones := q.Service.advanceBurstAt(0, q.Tick, &random, lookup, nil, q.World, q.Terrain)
			if tc.blocked {
				if clones != 0 || !p.Dead || p.BurstRemaining != 0 || q.Service.Count() != 1 || random.Draws() != 0 || p.BurstDeadline != q.Tick {
					t.Fatal("blocked burst emitted or spent RNG")
				}
			} else if clones != 1 || p.Dead || p.BurstRemaining != 1 || q.Service.Count() != 2 || random.Draws() != 2 || p.BurstDeadline != q.Tick+3 {
				t.Fatal("ordered burst failed to preserve emission and RNG")
			}
		})
	}
}

func TestGroundBurstSnapshotsAttackIntent(t *testing.T) {
	for _, ground := range []bool{false, true} {
		for _, modern := range []bool{false, true} {
			q := obstructionFixture(t)
			q.Service.Rules = rulesForModern(modern)
			q.Shooter.Orders = groundAttackIntent(ground)
			q.Launch.Weapon.Burst, q.Launch.Weapon.BurstRate = 2, 3
			q.Launch.Target = Target{Kind: TargetPoint, X: q.Aim.X, Y: q.Aim.Y, Z: q.Aim.Z}
			h, fired := TryFire(q.Service, &q.Launch, 0, q.Launch.Target, q.Tick, FirePorts{
				Shooter: q.Shooter, Origin: q.Muzzle, Shot: &q,
				ShooterHealth: 100, ShooterMaxHealth: 100,
			})
			if !fired {
				t.Fatal("initial clear shot failed")
			}
			p := &q.Service.Records[int(h)-1]
			if !p.OrderedBurst || p.GroundAttackBurst != ground {
				t.Fatal("burst did not snapshot the original point-attack intent")
			}
			// A different order and rule set after launch cannot change intent.
			q.Shooter.Orders = groundAttackIntent(!ground)
			q.Service.Rules = &ModernRules{}
			q.Terrain.FeatureDefs[0].Height = 64
			q.Terrain.PlotAt(4, 1).SetFeature(0)
			weapon := q.Launch.Weapon
			lookup := func(id int32) (*content.WeaponDef, bool) { return weapon, id == weapon.ID }
			clones := q.Service.advanceBurstAt(0, p.BurstDeadline, nil, lookup, nil, q.World, q.Terrain)
			if (clones == 1) != ground {
				t.Fatalf("ground=%v started modern=%v: emitted %d pellets after order replacement", ground, modern, clones)
			}
		}
	}
}

func TestAutomaticBurstKeepsSafetyAfterOrderReplacement(t *testing.T) {
	q := obstructionFixture(t)
	q.Shooter.Orders = automaticAttackIntent{}
	q.Launch.Weapon.Burst, q.Launch.Weapon.BurstRate = 2, 3
	def := contactDef(contactModelTop)
	def.ModelTop = contactModelTop >> 16
	target, err := q.World.Create(def, 1, q.Aim.X, 0, q.Aim.Z)
	if err != nil {
		t.Fatal(err)
	}
	q.Target = q.World.Unit(target)
	stampGroundRect(q.Terrain, 12, 1, 1, 1, target)
	q.Launch.Target = Target{Kind: TargetUnit, Unit: target}
	h, fired := TryFire(q.Service, &q.Launch, 0, q.Launch.Target, q.Tick, FirePorts{
		Shooter: q.Shooter, Origin: q.Muzzle, Shot: &q,
		TargetWorld:   func(pool.Handle) (Vec3, bool) { return q.Aim, true },
		ShooterHealth: 100, ShooterMaxHealth: 100,
	})
	if !fired {
		t.Fatal("initial clear enemy shot failed")
	}
	p := &q.Service.Records[int(h)-1]
	if !p.OrderedBurst || !p.AutomaticAttackBurst || p.GroundAttackBurst {
		t.Fatal("burst lost automatic order provenance")
	}
	q.Target.Owner = q.Shooter.Owner
	q.Shooter.Orders = groundAttackIntent(true)
	weapon := q.Launch.Weapon
	lookup := func(id int32) (*content.WeaponDef, bool) { return weapon, id == weapon.ID }
	random := rng.NewSimulation(7)
	if clones := q.Service.advanceBurstAt(0, p.BurstDeadline, &random, lookup, nil, q.World, q.Terrain); clones != 0 || !p.Dead || random.Draws() != 0 {
		t.Fatal("automatic burst inherited manual permission after target became friendly")
	}
}

func TestModernObstructionUsesFlightGeometry(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pitch   uint16
		height  int32
		blocked bool
	}{
		{"high arc clears low wreck", 8192, 32, false},
		{"arc intersects tall wreck", 8192, 96, true},
		{"flat shot hits wreck", 0, 32, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := obstructionFixture(t)
			q.Launch = modernBallisticSlot()
			q.Launch.DesiredPitch = tc.pitch
			q.Terrain.Gravity = numeric.FixedFromInt(1)
			q.Terrain.FeatureDefs[0].Height = tc.height
			q.Terrain.PlotAt(4, 1).SetFeature(0)
			if got := modernObstructedShot(&q); got != tc.blocked {
				t.Fatalf("blocked=%v want %v", got, tc.blocked)
			}
		})
	}
	q := obstructionFixture(t)
	q.Launch.Weapon = guidedTerrainWeapon()
	h, err := q.World.Create(contactDef(contactModelTop), 1, q.Aim.X, q.Aim.Y, q.Aim.Z)
	if err != nil {
		t.Fatal(err)
	}
	q.Target = q.World.Unit(h)
	q.Target.Def.MaxVelocity = 2
	q.Terrain.FeatureDefs[0].Height = 64
	q.Terrain.PlotAt(6, 1).SetFeature(0)
	if !modernObstructedShot(&q) {
		t.Fatal("guided mobile-target shot ignored a distant wreck")
	}
}

func TestModernGroundTargetContactPrecedesAlliedAirWord(t *testing.T) {
	q := obstructionFixture(t)
	target, err := q.World.Create(contactDef(contactModelTop), 1, q.Aim.X, 0, q.Aim.Z)
	if err != nil {
		t.Fatal(err)
	}
	q.Target = q.World.Unit(target)
	ally, err := q.World.Create(contactDef(contactModelTop), 2, q.Aim.X, 0, q.Aim.Z)
	if err != nil {
		t.Fatal(err)
	}
	stampGroundRect(q.Terrain, 12, 1, 1, 1, target)
	stampAirRect(q.Terrain, 12, 1, 1, 1, ally)
	if modernObstructedShot(&q) {
		t.Fatal("terminal ground target failed to precede allied air word")
	}
	// At this speed the endpoint skips the target cell, so the friend must
	// still block: a hypothetical swept target is not an actual interception.
	q.Launch.Weapon.WeaponVelocity = 96 << 16
	if !modernObstructedShot(&q) {
		t.Fatal("skipped target incorrectly shielded allied air word")
	}
	q.Muzzle = Vec3{X: cellCentre(3), Y: numeric.FixedFromInt(100), Z: cellCentre(1)}
	q.Aim = Vec3{X: cellCentre(4), Y: numeric.FixedFromInt(16), Z: cellCentre(1)}
	q.World.Unit(ally).Y = numeric.FixedFromInt(50)
	stampGroundRect(q.Terrain, 4, 1, 1, 1, target)
	stampAirRect(q.Terrain, 4, 1, 1, 1, ally)
	budget := modernTerrainSampleBudget
	if !modernObstructedSegment(&q, q.Muzzle, q.Aim, &budget) {
		t.Fatal("descending shot crossed an ally before terminal ground contact")
	}
}

func TestModernObstructionUsesBoundSurfaceFireGuidance(t *testing.T) {
	q := obstructionFixture(t)
	q.Launch.Weapon.SelfProp, q.Launch.Weapon.WaterWeapon, q.Launch.Weapon.SurfaceFire = true, true, true
	q.Service.Community.WeaponTargetKeys = true
	q.Terrain.Gravity = numeric.FixedFromInt(1)
	ally, err := q.World.Create(contactDef(contactModelTop), 2, cellCentre(4), numeric.FixedFromInt(16), cellCentre(1))
	if err != nil {
		t.Fatal(err)
	}
	stampAirRect(q.Terrain, 4, 1, 1, 1, ally)
	if !modernObstructedShot(&q) {
		t.Fatal("preview bypassed bound surfacefire policy and dropped below the ally")
	}
}

func TestModernObstructionSweepDirections(t *testing.T) {
	for _, delta := range [...][2]int32{{11, 0}, {-11, 0}, {0, 11}, {0, -11}, {11, 11}, {-11, -11}, {11, -11}, {-11, 11}} {
		q := obstructionFixture(t)
		q.Muzzle = Vec3{X: cellCentre(15), Y: numeric.FixedFromInt(16), Z: cellCentre(15)}
		q.Aim = Vec3{X: cellCentre(15 + delta[0]), Y: q.Muzzle.Y, Z: cellCentre(15 + delta[1])}
		q.Launch.Weapon.WeaponVelocity = 96 << 16
		cx, cz := int32(15)+delta[0]/3, int32(15)+delta[1]/3
		q.Terrain.FeatureDefs[0].Height = 32
		q.Terrain.PlotAt(cx, cz).SetFeature(0)
		if !modernObstructedShot(&q) {
			t.Fatalf("direction %v skipped wreck", delta)
		}
	}
}

func TestModernBlockedShotWaitsWithoutSpending(t *testing.T) {
	for _, modern := range []bool{false, true} {
		t.Run(map[bool]string{false: "strict", true: "modern"}[modern], func(t *testing.T) {
			q := obstructionFixture(t)
			q.Service.Rules = rulesForModern(modern)
			weapon := q.Launch.Weapon
			weapon.Turret, weapon.Accuracy, weapon.ReloadTime = true, 128, 30
			weapon.EnergyPerShot, weapon.MetalPerShot = 100, 5
			q.Shooter.InstallWeapon(0, weapon)
			slot := q.Shooter.SlotAt(0)
			target, err := q.World.Create(contactDef(contactModelTop), 1, q.Aim.X, q.Aim.Y, q.Aim.Z)
			if err != nil {
				t.Fatal(err)
			}
			slot.Target = units.Target{Kind: units.TargetUnit, Unit: target}
			slot.Ammo = 3
			q.Terrain.FeatureDefs[0].Height = 64
			q.Terrain.PlotAt(4, 1).SetFeature(0)
			var econ economy.Service
			econ.Players[0].Stock = [2]float32{50, 500}
			random := rng.NewSimulation(7)
			before := random
			pending := q.Shooter.Pending
			var summary UnitStepSummary
			q.Service.firePreparedSlot(q.Shooter, slot, 0, &slotPrep{weapon: weapon, tgtPos: q.Aim}, 10, q.Terrain, &econ, &random, q.World, nil, &summary)
			if modern {
				if summary.Fired != 0 || slot.Reload != 0 || slot.Ammo != 3 || random != before || q.Shooter.Pending != pending || econ.Players[0].Stock != [2]float32{50, 500} {
					t.Fatal("blocked shot spent resources, random draws or order state")
				}
				weapon.Stockpile = true
				q.Service.firePreparedSlot(q.Shooter, slot, 0, &slotPrep{weapon: weapon, tgtPos: q.Aim}, 11, q.Terrain, &econ, &random, q.World, nil, &summary)
				if slot.Ammo != 3 || summary.Fired != 0 || random != before {
					t.Fatal("blocked stockpile launch spent ammunition or RNG")
				}
				weapon.Stockpile = false
				q.Terrain.PlotAt(4, 1).SetFeature(world.PlotFeatureNone)
				q.Service.firePreparedSlot(q.Shooter, slot, 0, &slotPrep{weapon: weapon, tgtPos: q.Aim}, 12, q.Terrain, &econ, &random, q.World, nil, &summary)
			}
			if summary.Fired != 1 || slot.Reload == 0 || econ.Players[0].Stock != [2]float32{45, 400} || random == before {
				t.Fatalf("clear/Strict shot failed to fire normally: fired=%d stock=%v slot=%+v pending=%v RNG=%v", summary.Fired, econ.Players[0].Stock, slot, q.Shooter.Pending, random)
			}
		})
	}
}

func TestModernObstructionAdmissionDoesNotAllocate(t *testing.T) {
	q := obstructionFixture(t)
	stampGroundRect(q.Terrain, 1, 1, 1, 1, q.Shooter.Handle)
	if modernObstructedShot(&q) {
		t.Fatal("shooter blocked its own muzzle")
	}
	if got := testing.AllocsPerRun(100, func() { rulesDispatchSink = modernObstructedShot(&q) }); got != 0 {
		t.Fatalf("admission allocated %v per shot", got)
	}
}

func TestModernBlockedShotMakesNoLaunchCallbacks(t *testing.T) {
	q := obstructionFixture(t)
	w := q.Launch.Weapon
	w.Turret, w.Accuracy, w.SoundStart, w.StartSmoke = true, 128, "laser", true
	q.Terrain.FeatureDefs[0].Height = 64
	q.Terrain.PlotAt(4, 1).SetFeature(0)
	queries := 0
	random := rng.NewSimulation(7)
	script, events := &scriptRecorder{}, &eventRecorder{}
	ports := FirePorts{Shooter: q.Shooter, Origin: q.Muzzle,
		MuzzlePiece: func(int) int32 { queries++; return -1 }, RNG: &random,
		Script: script, Events: events, ShooterHealth: 100, ShooterMaxHealth: 100, Shot: &q}
	if _, ok := TryFire(q.Service, &q.Launch, 0, Target{Kind: TargetPoint, X: q.Aim.X, Y: q.Aim.Y, Z: q.Aim.Z}, q.Tick, ports); ok {
		t.Fatal("wreck-obstructed shot fired")
	}
	if queries != 1 || random.Draws() != 0 || q.Service.Count() != 0 || len(script.calls) != 0 || len(events.sounds) != 0 || len(events.smoke) != 0 {
		t.Fatal("refusal repeated the muzzle query or committed launch side effects")
	}
}

func TestModernObstructionCancelsPendingBurst(t *testing.T) {
	for _, modern := range []bool{false, true} {
		for _, feature := range []bool{false, true} {
			q := obstructionFixture(t)
			q.Service.Rules = rulesForModern(modern)
			weapon := q.Launch.Weapon
			weapon.Burst, weapon.BurstRate, weapon.SprayAngle, weapon.RandomDecay = 2, 3, 128, 4
			lookup := func(id int32) (*content.WeaponDef, bool) { return weapon, id == weapon.ID }
			h, ok := q.Service.Reserve()
			if !ok {
				t.Fatal("reserve burst template")
			}
			p := &q.Service.Records[int(h)-1]
			p.Shooter, p.ShooterSide = q.Shooter.Handle, q.Shooter.Owner
			InitOrdinary(p, weapon, 1, q.Muzzle, q.Aim, 0)
			p.BurstRemaining, p.BurstDeadline = 2, q.Tick
			if feature {
				q.Terrain.FeatureDefs[0].Height = 64
				q.Terrain.PlotAt(4, 1).SetFeature(0)
			} else {
				ally, err := q.World.Create(contactDef(contactModelTop), 2, cellCentre(4), 0, cellCentre(1))
				if err != nil {
					t.Fatal(err)
				}
				stampGroundRect(q.Terrain, 4, 1, 1, 1, ally)
			}
			random := rng.NewSimulation(7)
			clones := q.Service.advanceBurstAt(0, q.Tick, &random, lookup, nil, q.World, q.Terrain)
			if modern {
				if clones != 0 || !p.Dead || p.BurstRemaining != 0 || q.Service.Count() != 1 || random.Draws() != 0 || p.BurstDeadline != q.Tick {
					t.Fatal("blocked burst emitted/spent RNG or failed to cancel")
				}
			} else if clones != 1 || p.Dead || p.BurstRemaining != 1 || q.Service.Count() != 2 || random.Draws() != 2 {
				t.Fatal("Strict burst schedule or RNG changed")
			}
		}
	}
	q := obstructionFixture(t)
	q.Terrain.FeatureDefs[0].Height = 64
	q.Terrain.PlotAt(4, 1).SetFeature(0)
	p := Projectile{Shooter: q.Shooter.Handle, ShooterSide: q.Shooter.Owner}
	InitOrdinary(&p, q.Launch.Weapon, 1, q.Muzzle, q.Aim, 0)
	p.Velocity = Vec3{Z: numeric.FixedFromInt(16)}
	q.Burst = &p
	if modernObstructedShot(&q) {
		t.Fatal("burst preview re-aimed instead of using inherited spray velocity")
	}
	q.Launch = modernBallisticSlot()
	q.Wind = nil
	if modernObstructedShot(&q) {
		t.Fatal("burst with unknown wind must admit without prediction")
	}
	q.Wind = &world.Wind{}
	InitBallistic(&p, q.Launch.Weapon, 1, q.Muzzle, q.Aim, 0, 0, numeric.Angle(16384), 0, 0)
	// A ballistic creator retains the previous record's stored point. A
	// current wreck beyond that stale point still obstructs the copied flight.
	p.TargetPos = Vec3{X: cellCentre(2), Y: q.Aim.Y, Z: q.Aim.Z}
	q.Aim = p.TargetPos
	if !modernObstructedShot(&q) {
		t.Fatal("ballistic burst clipped safety at an unrelated retained aim")
	}
	q = obstructionFixture(t)
	q.Terrain.FeatureDefs[0].Height = 64
	q.Terrain.PlotAt(4, 1).SetFeature(0)
	q.Launch.Weapon.WeaponTimer = 3
	p = Projectile{Shooter: q.Shooter.Handle, ShooterSide: q.Shooter.Owner}
	InitOrdinary(&p, q.Launch.Weapon, 1, q.Muzzle, q.Aim, 0)
	q.Burst = &p
	if modernObstructedShot(&q) {
		t.Fatal("pellet preview moved on the emission tick and reached a wreck beyond expiry")
	}
}

type noRoomIntent bool

func (room noRoomIntent) NoFiringPosition(*units.Unit, *units.Unit, uint32) bool { return bool(room) }

// Nanolathe Modern policy: DESIGN_WEAPONS_PROJECTILES §2.3.2 "No room". A shot
// held only by features that can be destroyed fires when the shooter has
// nowhere to step to, if it would reach its target between samples or its
// blast would damage the feature it hits. Friends, indestructible features and
// a blast that falls short of the feature still hold it.
func TestModernNoRoomFiresIntoFeatures(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		noRoom, immobile      bool
		indestructible        bool
		aoe, velocity         int32
		friendCell            int32
		wreck, strict, admits bool
	}{
		{name: "wreck with room", wreck: true, aoe: 64},
		{name: "wreck without room", noRoom: true, wreck: true, aoe: 64, admits: true},
		{name: "blast short of wreck", noRoom: true, wreck: true, aoe: 8},
		{name: "samples pass wreck", noRoom: true, wreck: true, aoe: 8, velocity: 36, admits: true},
		{name: "immobile behind wreck", immobile: true, wreck: true, aoe: 64, admits: true},
		{name: "indestructible without room", noRoom: true, wreck: true, indestructible: true, aoe: 64},
		{name: "friend without room", noRoom: true, friendCell: 4, aoe: 64},
		{name: "wreck stops shot before friend", noRoom: true, wreck: true, aoe: 64, friendCell: 8, admits: true},
		{name: "friend beyond passed wreck", noRoom: true, wreck: true, aoe: 8, velocity: 36, friendCell: 8},
		{name: "strict", wreck: true, aoe: 8, strict: true, admits: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := obstructionFixture(t)
			q.Service.Features = &features.Service{Terrain: q.Terrain}
			if tc.strict {
				q.Service.Rules = StrictRules{}
			}
			q.Shooter.Def.CanMove = !tc.immobile
			q.Shooter.Orders = noRoomIntent(tc.noRoom)
			weapon := q.Launch.Weapon
			weapon.Turret, weapon.Accuracy, weapon.ReloadTime = true, 128, 30
			weapon.EnergyPerShot, weapon.MetalPerShot = 100, 5
			weapon.AreaOfEffect = tc.aoe
			if tc.velocity != 0 {
				weapon.WeaponVelocity = tc.velocity << 16
			}
			q.Shooter.InstallWeapon(0, weapon)
			slot := q.Shooter.SlotAt(0)
			target, err := q.World.Create(contactDef(contactModelTop), 1, q.Aim.X, q.Aim.Y, q.Aim.Z)
			if err != nil {
				t.Fatal(err)
			}
			stampGroundRect(q.Terrain, 12, 1, 1, 1, target)
			slot.Target = units.Target{Kind: units.TargetUnit, Unit: target}
			if tc.wreck {
				q.Terrain.FeatureDefs[0].Height = 64
				q.Terrain.FeatureDefs[0].Indestructible = tc.indestructible
				q.Terrain.PlotAt(4, 1).SetFeature(0)
			}
			if tc.friendCell != 0 {
				h, err := q.World.Create(contactDef(contactModelTop), 0, cellCentre(tc.friendCell), 0, cellCentre(1))
				if err != nil {
					t.Fatal(err)
				}
				stampGroundRect(q.Terrain, tc.friendCell, 1, 1, 1, h)
			}
			var econ economy.Service
			econ.Players[0].Stock = [2]float32{50, 500}
			random := rng.NewSimulation(7)
			before := random
			var summary UnitStepSummary
			q.Service.firePreparedSlot(q.Shooter, slot, 0, &slotPrep{weapon: weapon, tgtPos: q.Aim}, q.Tick, q.Terrain, &econ, &random, q.World, nil, &summary)
			if !tc.admits {
				if summary.Fired != 0 || slot.Reload != 0 || random != before || econ.Players[0].Stock != [2]float32{50, 500} {
					t.Fatal("held shot fired or spent")
				}
				return
			}
			if summary.Fired != 1 || slot.Reload != 30 || random.Draws()-before.Draws() != 2 || econ.Players[0].Stock != [2]float32{45, 400} {
				t.Fatalf("shot did not fire normally: fired=%d reload=%d draws=%d stock=%v", summary.Fired, slot.Reload, random.Draws()-before.Draws(), econ.Players[0].Stock)
			}
		})
	}
}
