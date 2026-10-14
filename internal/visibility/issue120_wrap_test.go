package visibility

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Issue 120 investigation: many clumped aircraft turn single cells into fog.
//
// The per-player byte grid is a uint8 refcount (C1/C4). With 256 overlapping
// observers on one cell the plain increment wraps to zero, and the fog cache
// reads that cell as unseen [03 §3.1]. These tests are diagnostics for the
// mechanism; they are not landed contracts.

// unionCoverage counts, per cell, how many of the supplied observer positions
// cover it, using the same sprite-mask walk publication uses.
func unionCoverage(s *Service, cx, cz, radius int32, positions [][2]int32) []int {
	counts := make([]int, int(s.W*s.H))
	for _, p := range positions {
		s.walkSpriteMask(p[0], p[1], radius, func(idx int) {
			counts[idx]++
		})
	}
	return counts
}

// holesButCovered reports the cells that are covered by at least one observer
// but whose local byte refcount reads zero.
func holesButCovered(s *Service, owner PlayerID, coverage []int) []int {
	var holes []int
	for i, c := range coverage {
		if c > 0 && s.byteGrids[owner][i] == 0 {
			holes = append(holes, i)
		}
	}
	return holes
}

// TestIssue120ModernSaturationPrototype sketch (issue 120 investigation, not
// a landed contract): with a saturating rule bound, the true overlap count
// lives in the wide grid, the byte grid stores min(count, 255), and removals
// clear exactly at zero — no wrap hole and no leak. Strict keeps the wrap.
func TestIssue120ModernSaturationPrototype(t *testing.T) {
	const cx, cz = 64, 64
	newModern := func() *Service {
		s := newTestService(&world.Terrain{CellW: 256, CellH: 256}, ModeHistoryEnabled|ModeCurrentEnabled)
		s.Rules = ModernRules{}
		return s
	}
	center := func(s *Service) uint8 { return s.byteGrids[0][int(cz*s.W+cx)] }

	t.Run("256 stack saturates", func(t *testing.T) {
		s := newModern()
		for i := 0; i < 256; i++ {
			s.Publish(0, cx, cz, 0, 128)
		}
		if got := center(s); got != 255 {
			t.Fatalf("byte = %d, want 255 (min(count,255))", got)
		}
		coverage := unionCoverage(s, cx, cz, 128, cloneN([2]int32{cx, cz}, 256))
		if holes := holesButCovered(s, 0, coverage); len(holes) != 0 {
			t.Fatalf("saturating count still produced %d fog holes", len(holes))
		}
	})
	t.Run("removal clears exactly at zero", func(t *testing.T) {
		s := newModern()
		const n = 300
		for i := 0; i < n; i++ {
			s.Publish(0, cx, cz, 0, 128)
		}
		for i := 0; i < n-1; i++ {
			s.Unpublish(0, cx, cz, 0, 128)
		}
		if got := center(s); got != 1 {
			t.Fatalf("byte after %d removals = %d, want 1", n-1, got)
		}
		s.Unpublish(0, cx, cz, 0, 128)
		if got := center(s); got != 0 {
			t.Fatalf("byte after all removals = %d, want 0", got)
		}
	})
	t.Run("strict still wraps", func(t *testing.T) {
		s := newTestService(&world.Terrain{CellW: 256, CellH: 256}, ModeHistoryEnabled|ModeCurrentEnabled)
		for i := 0; i < 256; i++ {
			s.Publish(0, cx, cz, 0, 128)
		}
		if got := center(s); got != 0 {
			t.Fatalf("strict byte = %d, want the retail wrap at 0", got)
		}
	})
}

func cloneN(p [2]int32, n int) [][2]int32 {
	out := make([][2]int32, n)
	for i := range out {
		out[i] = p
	}
	return out
}

func TestIssue120ExactStackWrap(t *testing.T) {
	for _, count := range []int{255, 256, 257, 300, 512} {
		t.Run(fmt.Sprintf("count=%d", count), func(t *testing.T) {
			terrain := &world.Terrain{CellW: 256, CellH: 256}
			s := newTestService(terrain, ModeHistoryEnabled|ModeCurrentEnabled)
			const cx, cz = 64, 64
			positions := make([][2]int32, count)
			for i := range positions {
				positions[i] = [2]int32{cx, cz} // +spawn at one pointer: identical stacks
				s.Publish(0, cx, cz, 0, 128)
			}
			center := int(cz*s.W + cx)
			got := s.byteGrids[0][center]
			want := uint8(count % 256)
			if got != want {
				t.Fatalf("center refcount = %d, want %d (u8 wrap)", got, want)
			}
			coverage := unionCoverage(s, cx, cz, 128, positions)
			holes := holesButCovered(s, 0, coverage)
			t.Logf("count=%d center=%d covered-cells-with-zero-refcount=%d", count, got, len(holes))
			if count%256 == 0 && len(holes) == 0 {
				t.Fatalf("count=%d: no holes; the wrap was not reproduced", count)
			}
			if count%256 != 0 && len(holes) != 0 {
				t.Fatalf("count=%d: holes without a wrap", count)
			}
		})
	}
}

// clumpPositions places count observers on a dense square lattice centred on
// (cx, cz). spread is the lattice side in tiles: observers stack on shared
// cells (count may exceed spread*spread), which is how a pile of constructors
// or a cheat-spawned stack sits.
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

// TestIssue120ClumpContourHoles piles a stack of observers onto a few tiles,
// the way a real pile of hovering constructors (or a cheat-spawned stack)
// sits, and looks for the isolated cells whose local sum lands exactly on a
// wrap value. Not every pile depth lands on the contour; the cases are logged
// and at least one must reproduce.
func TestIssue120ClumpContourHoles(t *testing.T) {
	total := 0
	for _, tc := range []struct {
		count, spread int32
	}{{260, 6}, {300, 6}, {400, 6}, {400, 8}, {600, 8}} {
		t.Run(fmt.Sprintf("count=%d-spread=%d", tc.count, tc.spread), func(t *testing.T) {
			terrain := &world.Terrain{CellW: 256, CellH: 256}
			s := newTestService(terrain, ModeHistoryEnabled|ModeCurrentEnabled)
			positions := clumpPositions(64, 64, tc.spread, tc.count)
			for _, p := range positions {
				s.Publish(0, p[0], p[1], 0, 128)
			}
			coverage := unionCoverage(s, 64, 64, 128, positions)
			max := 0
			for _, c := range coverage {
				if c > max {
					max = c
				}
			}
			holes := holesButCovered(s, 0, coverage)
			t.Logf("observers=%d max overlap=%d holes=%d", tc.count, max, len(holes))
			// The holes sit on the count ≡ 0 (mod 256) contour.
			for _, h := range holes {
				if coverage[h]%256 != 0 {
					t.Fatalf("hole at %d has coverage %d, not a multiple of 256", h, coverage[h])
				}
			}
			for _, h := range holes {
				gz, gx := h/int(s.W), h%int(s.W)
				t.Logf("hole cell (%d,%d) coverage=%d", gx, gz, coverage[h])
			}
			total += len(holes)
		})
	}
	if total == 0 {
		t.Fatal("no pile produced a wrap hole at all")
	}
}

// TestIssue120HolesBlink moves a third of the pile by one cell, frame after
// frame, and shows the hole set changing: the reported blink.
func TestIssue120HolesBlink(t *testing.T) {
	terrain := &world.Terrain{CellW: 256, CellH: 256}
	const count = 400
	base := clumpPositions(64, 64, 6, count)
	snapshot := func(drift int32) map[int]bool {
		s := newTestService(terrain, ModeHistoryEnabled|ModeCurrentEnabled)
		positions := make([][2]int32, len(base))
		copy(positions, base)
		// The hovering pile breathes: a third of it is one tile off its
		// neighbours' phase, and the phase walks over time.
		for i := 0; i < len(positions); i += 3 {
			positions[i][1] += drift
			positions[i][0] += drift
		}
		for _, p := range positions {
			s.Publish(0, p[0], p[1], 0, 128)
		}
		coverage := unionCoverage(s, 64, 64, 128, positions)
		holes := holesButCovered(s, 0, coverage)
		set := map[int]bool{}
		for _, h := range holes {
			set[h] = true
		}
		t.Logf("drift=%d holes=%d", drift, len(holes))
		return set
	}
	total := 0
	prev := snapshot(0)
	for drift := int32(1); drift <= 4; drift++ {
		cur := snapshot(drift)
		changed := 0
		for h := range prev {
			if !cur[h] {
				changed++
			}
		}
		for h := range cur {
			if !prev[h] {
				changed++
			}
		}
		t.Logf("drift %d -> %d: %d cells appeared or disappeared", drift-1, drift, changed)
		total += changed
		prev = cur
	}
	if total == 0 {
		t.Fatal("hole set never changed while the pile drifted; no blink")
	}
}
