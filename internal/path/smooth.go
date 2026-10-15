package path

import "slices"

// SmoothKernel is Modern's search kernel (docs/DESIGN_MOVEMENT_PATH.md
// "Modern route smoothing"). It runs the straightening kernel and, on the
// slice that search finishes, pulls the route taut: a run of turns is
// replaced by one straight leg when the mover's footprint can stand all the
// way along that leg. The retail search turns only by eighths of a circle and
// weighs its heuristic, so its routes run up to a tenth longer than the
// ground requires even with nothing in the way
// [04 R-PATH-01 §3][04 R-PATH-01 §7]; the mover itself steers at any heading
// [04 R-MOV-01 §2].
//
// It changes which points are published and never when: publication happens
// on the slice the wrapped search finishes on, and every anchor probed is
// charged to the player's work like a heap pop [04 §7.3].
//
// It is zero size, as every implementation a reserved rule set binds is
// (docs/DESIGN_GAMEPLAY_RULES.md §3).
type SmoothKernel struct{}

// smoothSpan bounds one leg, in world units, and smoothBudget the anchors
// one route's smoothing may probe.
const (
	smoothSpan   = 1024
	smoothBudget = 4096
	// smoothStep is the distance between samples along a leg and
	// smoothSide how far to either side of it the footprint is also
	// required to stand, both in world units: a mover does not hold a
	// line exactly.
	smoothStep = 8
	smoothSide = 6
)

// NewSession opens a straightening search for cfg behind the smoothing pass.
func (SmoothKernel) NewSession(cfg SearchConfig) Search {
	return &smoothSearch{Search: StraightenKernel{}.NewSession(cfg), cfg: cfg}
}

type smoothSearch struct {
	Search
	cfg    SearchConfig
	probes int
	// limit is the dearest extra cost (SearchConfig.CostDir) along the stretch
	// a straight leg would replace: the leg may not cross dearer ground than
	// the search accepted there, or pulling the route taut would undo the way
	// round the search paid for.
	limit   int32
	limited bool
	// legDir is the sector of the leg being judged, for a cost that depends
	// on the way a step goes.
	legDir uint8
	out    []Point
	done   bool
	status Status
}

func (s *smoothSearch) Popped() int { return s.Search.Popped() + s.probes }

func (s *smoothSearch) Resume(budget int) ([]Point, Status, bool) {
	if s.done {
		return s.out, s.status, true
	}
	points, status, done := s.Search.Resume(budget)
	if !done {
		return points, status, done
	}
	s.done, s.status, s.out = true, status, points
	if len(points) < 3 || len(points) > straightenCap || s.cfg.PassableValue == nil {
		return points, status, true
	}
	// The finishing pass is synchronous: only its output must outlive this
	// call. Short and unfinished routes need no extra point storage, and
	// the per-leg costs stay on the stack rather than in every search.
	points = slices.Clone(points)
	n := len(points)
	var dear [straightenCap]int32
	if s.cfg.CostDir != nil {
		s.limited = true
		for i := 0; i+1 < n; i++ {
			dear[i] = s.dearest(points[i], points[i+1])
		}
	}
	out := 1 // points[0] stays
	for i := 0; i < n-1; {
		// The farthest later point the footprint can reach in a straight
		// line from point i.
		j := i + 1
		for k := n - 1; k > i+1; k-- {
			if s.probes >= smoothBudget {
				break
			}
			if s.limited {
				s.limit = 0
				for m := i; m < k; m++ {
					s.limit = max(s.limit, dear[m])
				}
			}
			if s.clear(points[i], points[k]) {
				j = k
				break
			}
		}
		points[out] = points[j]
		out++
		i = j
	}
	s.out = points[:out]
	return s.out, status, true
}

// standable reports whether the footprint can stand with its centre at the
// world point.
func (s *smoothSearch) standable(x, z int32) bool {
	c := anchorAt(x, z, s.cfg.FootPrintX, s.cfg.FootPrintZ)
	s.probes++
	if s.cfg.HasBounds && !InBounds(c, s.cfg.Bounds) {
		return false
	}
	if s.limited && s.cost(c, s.legDir) > s.limit {
		return false
	}
	if s.cfg.LegValue != nil {
		return s.cfg.LegValue(c) != 0
	}
	return s.cfg.PassableValue(c) != 0
}

// dearest is the greatest extra cost of the anchors along the leg from a to
// b, sampled a cell apart.
func (s *smoothSearch) dearest(a, b Point) int32 {
	worst := int32(0)
	dx, dz := int64(b.X-a.X), int64(b.Z-a.Z)
	dir := Octant(dx, dz)
	steps := max(max(dx, -dx), max(dz, -dz))/16 + 1
	for k := int64(0); k <= steps; k++ {
		x, z := int32(int64(a.X)+dx*k/steps), int32(int64(a.Z)+dz*k/steps)
		c := anchorAt(x, z, s.cfg.FootPrintX, s.cfg.FootPrintZ)
		s.probes++
		worst = max(worst, s.cost(c, dir))
	}
	return worst
}

// cost is the request's extra cost of a step onto c in sector dir.
func (s *smoothSearch) cost(c Cell, dir uint8) int32 { return s.cfg.CostDir(c, dir) }

// anchorAt is the anchor of a footprint whose centre stands at the world
// point: the cell a mover's commit quantizes that position to, which adds
// half a cell before it divides [04 R-COLL-01 §1]. A route point is the
// centre of its anchor's footprint [04 R-PATH-01 §7], so the two agree
// there; between route points the half cell decides which anchor a mover
// holds, and a leg judged without it is judged half a cell off.
func anchorAt(x, z, footX, footZ int32) Cell {
	return Cell{X: floorDiv16(x + 8 - 8*footX), Z: floorDiv16(z + 8 - 8*footZ)}
}

func floorDiv16(v int32) int32 {
	if v >= 0 {
		return v / 16
	}
	return -((-v + 15) / 16)
}

// clear reports whether the footprint can stand all the way along the leg
// from a to b and a little to either side of it.
func (s *smoothSearch) clear(a, b Point) bool {
	dx, dz := int64(b.X-a.X), int64(b.Z-a.Z)
	d2 := dx*dx + dz*dz
	if d2 > smoothSpan*smoothSpan || d2 == 0 {
		return false
	}
	s.legDir = Octant(dx, dz)
	// Integer length, rounded up.
	var length int64
	for length*length < d2 {
		length++
	}
	steps := length/smoothStep + 1
	// The perpendicular of length smoothSide.
	px, pz := -dz*smoothSide/length, dx*smoothSide/length
	for k := int64(0); k <= steps; k++ {
		x, z := int64(a.X)+dx*k/steps, int64(a.Z)+dz*k/steps
		if !s.standable(int32(x), int32(z)) || !s.standable(int32(x+px), int32(z+pz)) || !s.standable(int32(x-px), int32(z-pz)) {
			return false
		}
	}
	return true
}
