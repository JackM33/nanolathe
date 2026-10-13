package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Only Modern flies the aircraft pilots; Strict 3.1 and Community 3.9 keep the
// retail legs [04 R-AIR-01 §8] (DESIGN_MOVEMENT_PATH "Modern aircraft tactics").
func TestAirTacticsRulePolarity(t *testing.T) {
	if (StrictRules{}).AirTactics(nil) || (&CommunityRules{}).AirTactics(nil) || !(&ModernRules{}).AirTactics(nil) {
		t.Fatal("air tactics must be Modern only")
	}
}

// A bomber times its release on the dropped projectile's own motion and
// contact [06 §6.4][06 §8.1]: launch velocity from heading and scalar speed,
// wind, gravity, and the terrain test. The prediction must land where combat's
// kernel lands, on the same tick, or the middle bomb drifts off the target.
func TestAirDropImpactMatchesDroppedMotion(t *testing.T) {
	sys, _, _ := wideAirFixture(t)
	weapon := &content.WeaponDef{Dropped: true}
	for _, wind := range [][2]int32{{0, 0}, {9000, -4000}, {-12000, 15000}} {
		sys.AirWind = &world.Wind{DirX: wind[0], DirZ: wind[1]}
		for _, h := range []uint16{0, 0x2345, 0x9000, 0xC100} {
			sx, sy, sz := numeric.FixedFromInt(2000), numeric.FixedFromInt(220), numeric.FixedFromInt(512)
			speed := numeric.Fixed(7<<16 + 12345)
			ix, iz, k, ok := sys.airDropImpact(sx, sy, sz, h, speed, 0)
			if !ok {
				t.Fatalf("wind=%v heading=%#x: no prediction", wind, h)
			}
			var p combat.Projectile
			combat.InitDropped(&p, weapon, 0, combat.Vec3{X: sx, Y: sy, Z: sz}, combat.Vec3{}, 0, numeric.Angle(h), speed)
			ticks := int64(0)
			for {
				ticks++
				combat.AdvanceDropped(&p, weapon, uint32(ticks), combat.Vec3{X: numeric.Fixed(wind[0]), Z: numeric.Fixed(wind[1])}, sys.Terrain.Gravity)
				cell := sys.Terrain.PlotAt(world.WorldToCell(p.Pos.X), world.WorldToCell(p.Pos.Z))
				if int16(p.Pos.Y.Raw()>>16) < int16(cell.MinHeight()) || int16(p.Pos.Y.Raw()>>16) < int16(sys.Terrain.SeaLevel) {
					break
				}
				if ticks > 300 {
					t.Fatal("reference bomb never landed")
				}
			}
			if ticks != k || p.Pos.X != ix || p.Pos.Z != iz {
				t.Fatalf("wind=%v heading=%#x: predicted (%d,%d) at %d, dropped motion (%d,%d) at %d", wind, h, ix, iz, k, p.Pos.X, p.Pos.Z, ticks)
			}
		}
	}
}

// Going round anti-air: a circle dead ahead bends the heading onto a tangent
// that clears it, the chosen side is kept, and a circle round the goal itself
// is entered rather than avoided.
func TestAirAvoidClearsCircleAndKeepsSide(t *testing.T) {
	threats := []airThreat{{x: 1000, z: 500, r: 200, w: 30}}
	p := &airPilot{}
	// Heading 0xC000 travels toward +X [04 R-MOV-01 §4].
	h := airAvoid(p, 0, 500, 0xC000, 2000, 2000, 500, threats)
	if h == 0xC000 || p.avoidSide == 0 {
		t.Fatal("a circle dead ahead was not avoided")
	}
	ex, ez := airAlong(0, 500, h, 2000)
	if airSegmentExposure(0, 500, ex, ez, 0, threats) != 0 {
		t.Fatal("the avoiding heading still crosses the circle")
	}
	side := p.avoidSide
	airAvoid(p, 0, 520, 0xC100, 2000, 2000, 500, threats)
	if p.avoidSide != side {
		t.Fatal("the avoidance side flipped between ticks")
	}
	if got := airAvoid(&airPilot{}, 0, 500, 0xC000, 1000, 1000, 500, threats); got != 0xC000 {
		t.Fatal("a circle holding the goal was avoided")
	}
}

// Exposure is inside length times damage rate; a leg outside every circle
// costs nothing.
func TestAirSegmentExposure(t *testing.T) {
	threats := []airThreat{{x: 500, z: 0, r: 100, w: 10}}
	// A central crossing is 200 world units at rate 10, doubled for passing
	// over the turret.
	if got := airSegmentExposure(0, 0, 1000, 0, 0, threats); got < 3980 || got > 4020 {
		t.Fatalf("central crossing exposure=%d, want about 4000", got)
	}
	if got := airSegmentExposure(0, 200, 1000, 200, 0, threats); got != 0 {
		t.Fatalf("clear leg exposure=%d", got)
	}
}

// A strafing run breaks early only when its turn is then clear of every
// circle over the target: a target that one circle covers beyond the
// weapon's reach is overflown at speed, not broken off short inside it.
func TestAirStrafeBreakNeedsEveryCircle(t *testing.T) {
	const rng, turn = 659, 100
	near := airThreat{x: 100, z: 0, r: 200, w: 10}
	if got := airStrafeBreak(0, 0, -1000, 0, rng, turn, []airThreat{near}); got != 200 {
		t.Fatalf("break=%d, want 200: a turn radius beyond the reachable circle", got)
	}
	wide := airThreat{x: 0, z: 300, r: 800, w: 10}
	if got := airStrafeBreak(0, 0, -1000, 0, rng, turn, []airThreat{near, wide}); got != rng/5 {
		t.Fatalf("break=%d, want %d: overfly a target no break can clear", got, rng/5)
	}
}

// After a pass under anti-air a pilot flies straight out before it turns,
// when its heading leaves the cover soon enough.
func TestAirFlyOut(t *testing.T) {
	sys := &System{}
	threats := []airThreat{{x: 0, z: 0, r: 300, w: 10}}
	u := &units.Unit{}
	u.Move.Heading = 0xC000 // travelling toward +X [04 R-MOV-01 §4]
	if !sys.airFlyOut(u, 100, 0, 500, threats) {
		t.Fatal("a pilot inside cover with a short way out did not fly on")
	}
	if sys.airFlyOut(u, 100, 0, 100, threats) {
		t.Fatal("a way out beyond the limit was flown")
	}
	if sys.airFlyOut(u, 400, 0, 500, threats) {
		t.Fatal("a pilot already clear of cover flew on")
	}
}

// Against a tracking missile slower than twice its own speed, a fighter's leg
// straight over the launcher is priced below one passing it a few hundred
// units off; a slower aircraft keeps the plain rule, where the leg over the
// launcher is the dearer.
func TestAirSegmentExposureByAspect(t *testing.T) {
	threats := []airThreat{{x: 500, z: 0, r: 400, w: 10, missile: 1419946}}
	over := func(v int64) int64 { return airSegmentExposure(0, 0, 1000, 0, v, threats) }
	beside := func(v int64) int64 { return airSegmentExposure(0, 250, 1000, 250, v, threats) }
	if fast := int64(12 << 16); over(fast) >= beside(fast) {
		t.Fatalf("fighter: over=%d beside=%d, want the leg over the launcher cheaper", over(fast), beside(fast))
	}
	if slow := int64(6 << 16); over(slow) <= beside(slow) {
		t.Fatalf("slow aircraft: over=%d beside=%d, want the leg over the launcher dearer", over(slow), beside(slow))
	}
}
