package visibility

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Contracts for the Modern coverage saturation policy (issue 120):
// DESIGN_WORLD_VISIBILITY "Modern coverage saturation". Strict keeps retail's
// un-clamped uint8 refcount, whose 256-observer wrap reads as fog [03 §3.1];
// Modern carries the true overlap count in the service's wide row and stores
// min(count, 255) in the byte grid.

// unionCoverage counts, per cell, how many of the supplied observer positions
// cover it, using the same sprite-mask walk publication uses.
func unionCoverage(s *Service, positions [][2]int32, radius int32) []int {
	counts := make([]int, int(s.W*s.H))
	for _, p := range positions {
		s.walkSpriteMask(p[0], p[1], radius, func(idx int) {
			counts[idx]++
		})
	}
	return counts
}

// fogHoles reports the cells that at least one observer covers but whose byte
// refcount reads zero.
func fogHoles(s *Service, owner PlayerID, coverage []int) []int {
	var holes []int
	for i, c := range coverage {
		if c > 0 && s.byteGrids[owner][i] == 0 {
			holes = append(holes, i)
		}
	}
	return holes
}

// clumpPositions piles count observer positions onto a spread x spread tile
// lattice centred on (cx, cz); count above spread*spread stacks observers on
// shared cells, which is how clumped aircraft sit.
func clumpPositions(cx, cz, spread, count int32) [][2]int32 {
	positions := make([][2]int32, 0, count)
	start := -spread / 2
	for i := int32(0); i < count; i++ {
		positions = append(positions, [2]int32{
			cx + start + i%spread,
			cz + start + (i/spread)%spread,
		})
	}
	return positions
}

func modernCoverageService() *Service {
	s := newTestService(&world.Terrain{CellW: 256, CellH: 256}, ModeHistoryEnabled|ModeCurrentEnabled)
	s.Rules = ModernRules{}
	return s
}

// TestModernCoverageSaturatesInsteadOfWrapping locks the Modern answer: a
// stack deep enough to wrap the byte grid leaves every covered cell visible,
// and removals clear the cell exactly when the last observer leaves.
func TestModernCoverageSaturatesInsteadOfWrapping(t *testing.T) {
	const cx, cz = 64, 64
	for _, count := range []int{256, 512} {
		t.Run(fmt.Sprintf("stack=%d", count), func(t *testing.T) {
			s := modernCoverageService()
			positions := clumpPositions(cx, cz, 1, int32(count))
			for range positions {
				s.Publish(0, cx, cz, 0, 128)
			}
			if got := s.byteGrids[0][int(cz*s.W+cx)]; got != 255 {
				t.Fatalf("byte refcount = %d, want the 255 saturation", got)
			}
			if holes := fogHoles(s, 0, unionCoverage(s, positions, 128)); len(holes) != 0 {
				t.Fatalf("%d observers left %d covered cells reading as fog", count, len(holes))
			}
		})
	}
}

// TestModernCoverageRemovalClearsExactlyAtZero locks that the saturated byte
// cannot leak coverage: the byte counts down through the wide row and reaches
// zero only with the last observer, then reads zero.
func TestModernCoverageRemovalClearsExactlyAtZero(t *testing.T) {
	const cx, cz = 64, 64
	s := modernCoverageService()
	const count = 300
	for i := 0; i < count; i++ {
		s.Publish(0, cx, cz, 0, 128)
	}
	center := func() uint8 { return s.byteGrids[0][int(cz*s.W+cx)] }
	if got := center(); got != 255 {
		t.Fatalf("byte after %d observers = %d, want 255", count, got)
	}
	// Down to 255 observers the byte still saturates; one more removal drops
	// it to the true count again.
	for i := 0; i < 45; i++ {
		s.Unpublish(0, cx, cz, 0, 128)
	}
	if got := center(); got != 255 {
		t.Fatalf("byte at 255 observers = %d, want 255", got)
	}
	s.Unpublish(0, cx, cz, 0, 128)
	if got := center(); got != 254 {
		t.Fatalf("byte at 254 observers = %d, want 254", got)
	}
	for i := 0; i < 254; i++ {
		s.Unpublish(0, cx, cz, 0, 128)
	}
	if got := center(); got != 0 {
		t.Fatalf("byte with no observers = %d, want 0", got)
	}
}

// TestStrictCoverageWrapsAt256 is the Strict 3.1 bypass: the retail increment
// has no clamp [03 §3.1], so the same stack wraps to zero and a covered cell
// reads as fog again.
func TestStrictCoverageWrapsAt256(t *testing.T) {
	const cx, cz = 64, 64
	for _, tc := range []struct {
		count int
		want  uint8
	}{{255, 255}, {256, 0}, {257, 1}} {
		t.Run(fmt.Sprintf("stack=%d", tc.count), func(t *testing.T) {
			s := newTestService(&world.Terrain{CellW: 256, CellH: 256}, ModeHistoryEnabled|ModeCurrentEnabled)
			for i := 0; i < tc.count; i++ {
				s.Publish(0, cx, cz, 0, 128)
			}
			if got := s.byteGrids[0][int(cz*s.W+cx)]; got != tc.want {
				t.Fatalf("strict byte = %d, want %d (retail wrap)", got, tc.want)
			}
		})
	}
}

// TestModernCoveragePileHasNoContourHoles is the issue-120 regression at grid
// level: a pile whose overlap sum crosses 256 puts isolated holes on the
// contour under Strict, and none under Modern.
func TestModernCoveragePileHasNoContourHoles(t *testing.T) {
	const cx, cz = 64, 64
	positions := clumpPositions(cx, cz, 8, 400)
	coverage := func(s *Service) []int { return unionCoverage(s, positions, 128) }

	strict := newTestService(&world.Terrain{CellW: 256, CellH: 256}, ModeHistoryEnabled|ModeCurrentEnabled)
	for _, p := range positions {
		strict.Publish(0, p[0], p[1], 0, 128)
	}
	strictHoles := fogHoles(strict, 0, coverage(strict))
	if len(strictHoles) == 0 {
		t.Fatal("strict pile produced no contour holes; the regression scene lost its shape")
	}
	modern := modernCoverageService()
	for _, p := range positions {
		modern.Publish(0, p[0], p[1], 0, 128)
	}
	if modernHoles := fogHoles(modern, 0, coverage(modern)); len(modernHoles) != 0 {
		t.Fatalf("modern pile left %d contour holes", len(modernHoles))
	}
}

// TestModernCoverageSetSwitchSeedsFromTheByte locks the documented switch
// behavior: a set selected mid-battle continues the byte grid it inherits.
// The wide row reseeds from the byte on the next saturating increment and is
// retired by any retail-arithmetic increment, so switching away and back
// cannot resurrect a stale count.
func TestModernCoverageSetSwitchSeedsFromTheByte(t *testing.T) {
	const cx, cz = 64, 64
	s := modernCoverageService()
	for i := 0; i < 300; i++ {
		s.Publish(0, cx, cz, 0, 128)
	}
	center := func() uint8 { return s.byteGrids[0][int(cz*s.W+cx)] }
	if got := center(); got != 255 {
		t.Fatalf("modern byte = %d, want 255", got)
	}
	// Switch to Strict: the next increment is retail's plain wrap, and the
	// wide row is retired with it.
	s.Rules = StrictRules{}
	s.Publish(0, cx, cz, 0, 128)
	if got := center(); got != 0 {
		t.Fatalf("strict byte after the wrap = %d, want 0", got)
	}
	if s.wide[0] != nil {
		t.Fatal("retail arithmetic left a wide row behind")
	}
	// Switch back: the wide count seeds from the byte the switch inherited.
	s.Rules = ModernRules{}
	s.Publish(0, cx, cz, 0, 128)
	if got := center(); got != 1 {
		t.Fatalf("modern byte after re-seeding = %d, want 1", got)
	}
}
