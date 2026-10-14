package session

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// Issue 120 investigation: a dense pile of aircraft drives the per-player
// byte refcount past 255, and the wrap reads as fog over cells the pile
// covers. These diagnostics drive the real phase-5 publisher; they are not
// landed contracts.

// issue120Pile builds a session with count flying observers piled on a
// spread x spread tile lattice around grid cell (16,16), published through the
// ordinary bulk entry path. SightDistance 280 is the stock ARMCA/CORCA radius
// (shape index 3, a seventeen-tile mask on authored content).
func issue120Pile(t *testing.T, count, spread int32) (*Session, []pool.Handle) {
	t.Helper()
	s := visibilityFixture(t, true)
	s.Units = newSessionFixtureWorld(int(count)+4, s.Catalog)
	def := &content.UnitDef{UnitName: "constructor", CanFly: true, MaxDamage: 100, SightDistance: 280}
	s.Catalog.Units["constructor"] = def
	handles := make([]pool.Handle, 0, count)
	start := -spread / 2
	for i := int32(0); i < count; i++ {
		// One grid cell is 32 pixels, one pixel is 1<<16 world units.
		gx := int32(16) + start + i%spread
		gz := int32(16) + start + (i/spread)%spread
		h, err := s.Units.Create(def, 1, numeric.Fixed(gx)<<21, 0, numeric.Fixed(gz)<<21)
		if err != nil {
			t.Fatalf("create observer %d: %v", i, err)
		}
		handles = append(handles, h)
	}
	publishVisibilityForAll(s)
	return s, handles
}

// issue120Holes reports the cells whose owner's history bit is set but whose
// byte refcount reads zero. With no retirement in play, history-bit-set means
// the cell was stamped, so a zero byte is the wrap hole.
func issue120Holes(s *Session, owner visibility.PlayerID) []int {
	words := s.Vis.WordMask()
	grid := s.Vis.ByteGrid(owner)
	var holes []int
	for i, w := range words {
		if w&(1<<owner) != 0 && grid[i] == 0 {
			holes = append(holes, i)
		}
	}
	return holes
}

func TestIssue120DensePileHolesThroughPhase5(t *testing.T) {
	anyHoles := false
	for _, count := range []int32{200, 255, 256, 300, 400, 512} {
		t.Run(fmt.Sprintf("count=%d", count), func(t *testing.T) {
			s, _ := issue120Pile(t, count, 5)
			holes := issue120Holes(s, 1)
			t.Logf("pile of %d: holes=%d", count, len(holes))
			if count < 256 && len(holes) != 0 {
				t.Fatalf("pile of %d produced holes below the wrap threshold", count)
			}
			if len(holes) != 0 {
				anyHoles = true
			}
		})
	}
	t.Run("exact-stack-256", func(t *testing.T) {
		// What a cheat stack at one pointer produces: identical footprints,
		// so every covered cell lands on the same count.
		s, _ := issue120Pile(t, 256, 1)
		holes := issue120Holes(s, 1)
		t.Logf("exact stack of 256: holes=%d", len(holes))
		if len(holes) == 0 {
			t.Fatal("exact stack of 256 produced no holes")
		}
	})
	if !anyHoles {
		t.Fatal("no pile at or above the wrap threshold produced a hole")
	}
}

func TestIssue120DensePileBlink(t *testing.T) {
	s, handles := issue120Pile(t, 400, 5)
	before := len(issue120Holes(s, 1))
	if before == 0 {
		t.Fatal("precondition: no holes in the static pile")
	}
	// Drift a third of the pile by one tile and step the authoritative phases,
	// which republishes coverage through the phase-5 sweep.
	changed := 0
	prev := map[int]bool{}
	for _, h := range issue120Holes(s, 1) {
		prev[h] = true
	}
	for tick := uint32(1); tick <= 6; tick++ {
		for i := 0; i < len(handles); i += 3 {
			u := s.Units.Unit(handles[i])
			if u == nil {
				continue
			}
			u.X += numeric.Fixed(1) << 21
			u.Z += numeric.Fixed(1) << 21
		}
		s.stepAuthoritativePhases(tick)
		cur := map[int]bool{}
		for _, h := range issue120Holes(s, 1) {
			cur[h] = true
		}
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
		prev = cur
		t.Logf("tick %d: holes=%d", tick, len(cur))
	}
	if changed == 0 {
		t.Fatal("hole set never changed while the pile drifted; no blink")
	}
	t.Logf("cells that appeared or disappeared across the drift: %d", changed)
}
