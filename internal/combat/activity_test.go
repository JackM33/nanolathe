package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Activity suspension also covers explicitly ordered fire, unlike the
// standing Hold Fire policy. Existing targets and ammunition remain resumable.
func TestActivityHoldWeaponsStopsOrderedLaunchAndCost(t *testing.T) {
	for _, modern := range []bool{false, true} {
		w, terrain, u, slot, e := holdFireFixture(t, "ordinary", units.TargetGround, true)
		slot.Reload = 0
		e.Rules = economy.StrictRules{}
		if modern {
			e.Rules = economy.ModernRules{}
		}
		e.HoldActive[u.Owner][economy.ActivityWeapons] = true
		random := rng.NewSimulation(77)
		beforeRandom, beforeTarget := random, slot.Target
		s := &Service{Rules: StrictRules{}}
		sum := s.StepWeaponsForUnit(u, 1, w, nil, terrain, e, nil, &random, nil)
		if modern {
			if sum.Fired != 0 || sum.Dispatched || s.Count() != 0 || random != beforeRandom || e.Players[u.Owner].Stock[economy.Energy] != 100 || slot.Target != beforeTarget {
				t.Fatal("held ordered fire had launch, aim, random or resource effects")
			}
			e.HoldActive[u.Owner][economy.ActivityWeapons] = false
			sum = s.StepWeaponsForUnit(u, 2, w, nil, terrain, e, nil, &random, nil)
		}
		if sum.Fired != 1 || e.Players[u.Owner].Stock[economy.Energy] != 93 || e.UnitBuckets(u.Handle)[economy.Energy].Requested != 7 {
			t.Fatalf("modern=%v launch did not resume with ordinary cost", modern)
		}
	}
}

func TestActivityHoldWeaponsRetainsBurstAndMovesLaunchedShots(t *testing.T) {
	for _, modern := range []bool{false, true} {
		w, _, shooter, _ := newTestWorldAndUnits(t)
		e := &economy.Service{Rules: economy.StrictRules{}}
		if modern {
			e.Rules = economy.ModernRules{}
		}
		e.HoldActive[shooter.Owner][economy.ActivityWeapons] = true
		weapon := &content.WeaponDef{ID: 11, LineOfSight: true, WeaponVelocity: 65536, Range: 32767, BurstRate: 3, SprayAngle: 500, RandomDecay: 100}
		cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"burst": weapon}}
		cat.RebuildWeaponIndex()
		pos := Vec3{X: numeric.FixedFromInt(100), Y: numeric.FixedFromInt(50), Z: numeric.FixedFromInt(100)}
		s := &Service{Rules: StrictRules{}}
		s.Reserve()
		s.Records[0] = Projectile{Shooter: shooter.Handle, WeaponID: 11, BurstRemaining: 2, BurstDeadline: 4, ExpiryTick: 1000, Speed: 65536, Pos: pos, StartPos: pos, Velocity: Vec3{X: 65536}}
		s.Reserve()
		s.Records[1] = Projectile{Shooter: shooter.Handle, WeaponID: 11, ExpiryTick: 1000, Speed: 65536, Pos: pos, StartPos: pos, Velocity: Vec3{X: 65536}}
		random := rng.NewSimulation(77)
		before := random
		s.TickProjectiles(4, w, nil, nil, nil, nil, e, cat, &random, nil)
		if modern {
			if s.Count() != 2 || s.Records[0].BurstRemaining != 2 || s.Records[0].Pos != pos || s.Records[1].Pos.X != pos.X+65536 || random != before {
				t.Fatal("held burst progressed or stopped an already launched shot")
			}
			e.HoldActive[shooter.Owner][economy.ActivityWeapons] = false
			s.TickProjectiles(5, w, nil, nil, nil, nil, e, cat, &random, nil)
		}
		if s.Count() != 3 || s.Records[0].BurstRemaining != 1 || random == before {
			t.Fatalf("modern=%v burst did not launch normally after hold", modern)
		}
	}
}
