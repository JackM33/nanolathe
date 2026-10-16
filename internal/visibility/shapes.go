package visibility

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// Authored raster inputs [03 §3.2].
//
// Neither raster synthesizes its shape. The sprite-mask path indexes the
// visibility-mask GAF compiled into content.SightShapes; the terrain-ray path
// walks the line lists of the LOS.TDF table its radius selects.

// step is one position along a spoke. dist counts from one [03 §3.2] C5.
type step struct {
	dx, dz int32
	dist   int32
}

// SetShapes binds the authored sight shapes [03 §3.2].
func (s *Service) SetShapes(sh *content.SightShapes) {
	if s != nil {
		s.shapes = sh
		s.dropRemovalRecords()
	}
}

// SetRayTables binds the parsed LOS.TDF tables and rebuilds the spoke cache
// [03 §3.2].
func (s *Service) SetRayTables(lt *content.LOSTables) {
	if s == nil {
		return
	}
	s.rayTables = lt
	s.spokeCache = nil
	s.dropRemovalRecords()
}

// rayTableCount is the clamp bound for the terrain-ray group index.
//
// It is the declared numtables narrowed to a signed word [03 R-VIS-01 §3].
// The reference install declares nine and ships twelve, and the declared value is the one the loader sizes its
// table list to, with an empty record wherever a declared slot has no section
// (docs/SPEC_CONFLICTS.md SC9, [03 R-COMP-02 §1]). Clamping by len(Tables) would
// reach three tables retail never loads, and would also shrink the bound for
// content that declares more tables than it ships, where retail keeps the
// declared bound and finds the missing slots empty.
func (s *Service) rayTableCount() int {
	if s == nil || s.rayTables == nil {
		return 0
	}
	n := int(int16(s.rayTables.NumTables))
	if n < 0 {
		n = 0
	}
	return n
}

// raySpokes returns the spoke list held in a zero-based table SLOT, building it
// once.
//
// The argument is a storage slot, not a group: the table-by-index accessor is
// one-based, so the caller passes g-1 for group g [03 R-COMP-02 §1]. Slot d was
// filled from the section named TABLE d+1 — the loader builds that name from
// the slot — so the two off-by-ones cancel and group g walks TABLE g, whose
// authored extent is exactly g cells. The surviving skew is at the top: the
// clamp stops at numtables-1, so the last loaded table, TABLE numtables, is
// never selected.
//
// The len(Tables) bound is a guard against a hand-built table list shorter than
// its declared count, not a second clamp: a compiled list always materializes
// every slot up to its highest authored section, and a slot past that end reads
// as the same empty line list.
func (s *Service) raySpokes(slot int) [][]step {
	if s == nil || s.rayTables == nil || slot < 0 || slot >= s.rayTableCount() {
		return nil
	}
	if slot >= len(s.rayTables.Tables) {
		return nil
	}
	if s.spokeCache == nil {
		s.spokeCache = make([][][]step, len(s.rayTables.Tables))
	}
	if s.spokeCache[slot] != nil {
		return s.spokeCache[slot]
	}
	built := buildSpokes(s.rayTables.Tables[slot])
	if built == nil {
		// Non-nil marks a line-less slot as built, so the empty result is
		// cached instead of re-derived for every observer.
		built = [][]step{}
	}
	s.spokeCache[slot] = built
	return built
}

// buildSpokes converts named declared LOS.TDF lines into four quadrant blocks
// [03 R-VIS-01 §3]. Coordinates and their negations wrap to signed words before
// becoming tile offsets. The ordinal, not geometric distance, is the horizon
// denominator; authored positions need not increase monotonically.
func buildSpokes(tb content.LOSTable) [][]step {
	var out [][]step
	lines := tb.Lines[:min(len(tb.Lines), max(0, int(int16(tb.NumLines))))]
	for quadrant := 0; quadrant < 4; quadrant++ {
		for _, line := range lines {
			if len(line) < 3 {
				continue
			}
			count := int(int16(line[0]))
			// TODO(question): retail safety for nonpositive counts or incomplete
			// coordinate lists is unknown. Keep the host's empty-spoke boundary;
			// allocation/failure-path evidence would settle it [03 R-VIS-01 §3].
			if count <= 0 || len(line) < 1+2*count {
				continue
			}
			spoke := make([]step, count)
			for i := range spoke {
				u, v := int16(line[1+2*i]), int16(line[2+2*i])
				var dx, dz int16
				switch quadrant {
				case 0:
					dx, dz = u, -v
				case 1:
					dx, dz = v, u
				case 2:
					dx, dz = -u, v
				case 3:
					dx, dz = -v, -u
				}
				spoke[i] = step{dx: int32(dx), dz: int32(dz), dist: int32(i + 1)}
			}
			out = append(out, spoke)
		}
	}
	return out
}
