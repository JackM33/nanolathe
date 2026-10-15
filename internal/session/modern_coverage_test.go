package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// Contracts for the Modern coverage saturation policy through the real
// phase-5 publisher (issue 120, DESIGN_WORLD_VISIBILITY "Modern coverage
// saturation"). Stock construction aircraft publish a seventeen-tile mask
// (sightdistance 280 selects shape index 3); the fixture's one solid 5x5
// shape stands in for it at the same quantization.

// coveragePileSession builds the issue's scene: count flying observers piled
// on a spread x spread tile lattice, bound to set and published through the
// ordinary bulk entry path. Publication itself must draw no RNG and move no
// resources.
func coveragePileSession(t *testing.T, set RuleSet, count, spread int32) (*Session, []pool.Handle) {
	t.Helper()
	s := visibilityFixture(t, true)
	s.BindRules(set)
	s.Units = newSessionFixtureWorld(int(count)+4, s.Catalog)
	def := &content.UnitDef{UnitName: "constructor", CanFly: true, MaxDamage: 100, SightDistance: 280}
	s.Catalog.Units["constructor"] = def
	start := -spread / 2
	handles := make([]pool.Handle, 0, count)
	for i := int32(0); i < count; i++ {
		// One coverage cell is 32 pixels of 1<<16 world units.
		gx := int32(16) + start + i%spread
		gz := int32(16) + start + (i/spread)%spread
		h, err := s.Units.Create(def, 1, numeric.Fixed(gx)<<21, 0, numeric.Fixed(gz)<<21)
		if err != nil {
			t.Fatalf("create observer %d: %v", i, err)
		}
		handles = append(handles, h)
	}
	sim, crt := *s.SimRNG(), *s.CrtRNG()
	stock := s.Econ.Players[1].Stock
	publishVisibilityForAll(s)
	if *s.SimRNG() != sim || *s.CrtRNG() != crt || s.Econ.Players[1].Stock != stock {
		t.Fatal("coverage publication spent RNG or resources")
	}
	return s, handles
}

// coverageHoles counts the cells some live observer of owner covers but whose
// byte refcount reads zero — the fog holes of issue 120. The walk mirrors
// publication for this fixture exactly: the bound shape is the single solid
// 5x5 frame anchored at (2,2), and the observer cell is the sprite-mask
// projection the session hands the service.
func coverageHoles(s *Session, owner visibility.PlayerID) int {
	grid := s.Vis.ByteGrid(owner)
	width, height := s.Vis.GridDimensions()
	covered := make([]int, int(width*height))
	for _, u := range s.Units.IterSliced() {
		if u == nil || !u.Alive || u.Owner != uint8(owner) {
			continue
		}
		cx, cz := spriteObserverTile(u, seaLevelFor(s))
		for dz := int32(-2); dz <= 2; dz++ {
			for dx := int32(-2); dx <= 2; dx++ {
				gx, gz := cx+dx, cz+dz
				if uint32(gx) >= uint32(width) || uint32(gz) >= uint32(height) {
					continue
				}
				covered[gz*width+gx]++
			}
		}
	}
	holes := 0
	for i, c := range covered {
		if c > 0 && grid[i] == 0 {
			holes++
		}
	}
	return holes
}

// TestModernCoveragePileThroughPhase5KeepsCoverage locks the Modern contract
// at the publisher: a pile deep enough to wrap Strict's refcount leaves no
// covered cell reading as fog, at entry and while the pile drifts across
// ticks.
func TestModernCoveragePileThroughPhase5KeepsCoverage(t *testing.T) {
	s, handles := coveragePileSession(t, ModernRuleSet(), 400, 5)
	if holes := coverageHoles(s, 1); holes != 0 {
		t.Fatalf("modern pile left %d covered cells reading as fog at entry", holes)
	}
	for tick := uint32(1); tick <= 6; tick++ {
		// A third of the pile drifts one tile per tick; phase 5 republishes.
		for i := 0; i < len(handles); i += 3 {
			u := s.Units.Unit(handles[i])
			if u == nil {
				continue
			}
			u.X += numeric.Fixed(1) << 21
			u.Z += numeric.Fixed(1) << 21
		}
		s.stepAuthoritativePhases(tick)
		if holes := coverageHoles(s, 1); holes != 0 {
			t.Fatalf("tick %d: modern pile left %d covered cells reading as fog", tick, holes)
		}
	}
}

// TestStrictCoveragePileThroughPhase5StillWraps is the Strict 3.1 bypass and
// the issue's regression: the same scene under retail's un-clamped refcount
// wraps to zero, and the pile's overlap contour reads as fog.
func TestStrictCoveragePileThroughPhase5StillWraps(t *testing.T) {
	s, _ := coveragePileSession(t, StrictRuleSet(), 400, 5)
	holes := coverageHoles(s, 1)
	t.Logf("strict pile holes at entry: %d", holes)
	if holes == 0 {
		t.Fatal("strict pile produced no wrap holes; the regression scene lost its shape")
	}
}

// TestCoveragePolicyFollowsTheBoundSet locks the selection path: the same
// scene is clean under Modern only, and Strict and Community keep retail's
// un-clamped refcount.
func TestCoveragePolicyFollowsTheBoundSet(t *testing.T) {
	for _, tc := range []struct {
		set      RuleSet
		wantHole bool
	}{
		{ModernRuleSet(), false},
		{StrictRuleSet(), true},
		{CommunityRuleSet(), true},
	} {
		t.Run(tc.set.Name, func(t *testing.T) {
			s, _ := coveragePileSession(t, tc.set, 400, 5)
			holes := coverageHoles(s, 1)
			if tc.wantHole && holes == 0 {
				t.Fatal("expected the retail wrap under this set")
			}
			if !tc.wantHole && holes != 0 {
				t.Fatalf("set left %d covered cells reading as fog", holes)
			}
		})
	}
}
