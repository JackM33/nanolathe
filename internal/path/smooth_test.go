package path

import "testing"

func smooth(t *testing.T, blocked map[Cell]bool, route []Point) ([]Point, *smoothSearch) {
	t.Helper()
	s := &smoothSearch{Search: &cannedSearch{route: route}, cfg: SearchConfig{FootPrintX: 1, FootPrintZ: 1, PassableValue: func(c Cell) uint8 {
		if blocked[c] {
			return 0
		}
		return 3
	}}}
	if _, _, done := s.Resume(100); done {
		t.Fatal("smoothing published before the search finished")
	}
	out, _, done := s.Resume(100)
	if !done {
		t.Fatal("smoothing did not publish on the slice the search finished")
	}
	return out, s
}

// A dog-leg over open ground — the eighth-turn route a search makes to a goal
// off its eight directions — becomes one straight leg, and the anchors probed
// for it are charged as work.
func TestSmoothPullsADogLegTaut(t *testing.T) {
	route := routeCells(Cell{0, 0}, Cell{10, 10}, Cell{30, 10})
	out, s := smooth(t, nil, route)
	if len(out) != 2 || out[0] != route[0] || out[1] != route[2] {
		t.Fatalf("smoothed to %v, want %v", out, []Point{route[0], route[2]})
	}
	if s.Popped() <= 7 {
		t.Fatal("probed anchors were not charged as work")
	}
}

// Ground the footprint cannot stand on keeps the turn, whether it lies on the
// straight leg or within the margin beside it.
func TestSmoothKeepsATurnRoundAWall(t *testing.T) {
	route := routeCells(Cell{0, 0}, Cell{10, 10}, Cell{30, 10})
	for _, wall := range []Cell{{15, 5}, {17, 6}} {
		if out, _ := smooth(t, map[Cell]bool{wall: true}, route); len(out) != 3 {
			t.Fatalf("a leg past blocked cell %v was taken: %v", wall, out)
		}
	}
}

// A leg longer than the span is not examined.
func TestSmoothLeavesLongLegs(t *testing.T) {
	route := routeCells(Cell{0, 0}, Cell{40, 40}, Cell{100, 40})
	if out, _ := smooth(t, nil, route); len(out) != 3 {
		t.Fatalf("a leg longer than %d world units was taken: %v", smoothSpan, out)
	}
}

// A route the wrapped search could not make is published as it came.
func TestSmoothLeavesShortRoutes(t *testing.T) {
	route := routeCells(Cell{0, 0}, Cell{10, 10})
	out, s := smooth(t, nil, route)
	if len(out) != 2 || s.Popped() != 7 {
		t.Fatalf("a two-point route was examined: %v, %d work", out, s.Popped())
	}
}

// A leg is judged by the leg view when the request supplies one: ground the
// search may not cross, such as the footprint of a friend on the move, may
// still be crossed by a straight leg.
func TestSmoothReadsTheLegView(t *testing.T) {
	route := routeCells(Cell{0, 0}, Cell{10, 10}, Cell{30, 10})
	wall := Cell{15, 5}
	s := &smoothSearch{Search: &cannedSearch{route: route}, cfg: SearchConfig{FootPrintX: 1, FootPrintZ: 1,
		PassableValue: func(c Cell) uint8 {
			if c == wall {
				return 0
			}
			return 3
		},
		LegValue: func(Cell) uint8 { return 3 },
	}}
	s.Resume(100)
	if out, _, _ := s.Resume(100); len(out) != 2 {
		t.Fatalf("the leg view was not read: %v", out)
	}
}

// A leg is judged at the anchors a mover holds along it. A mover's commit
// adds half a cell before it divides [04 R-COLL-01 §1]: a footprint of one
// cell holds the cell its centre is in, and one of two cells the two cells
// nearest its centre.
func TestSmoothJudgesALegAtTheMoversAnchors(t *testing.T) {
	for _, tc := range []struct {
		x, z, foot int32
		want       Cell
	}{
		{8, 8, 1, Cell{0, 0}},
		{15, 0, 1, Cell{0, 0}},
		{16, 31, 1, Cell{1, 1}},
		{1216, 3296, 2, Cell{75, 205}}, // a route point: the centre of its anchor's footprint
		{1208, 3303, 2, Cell{75, 205}}, // half a cell west of it, the same anchor still
		{1207, 3304, 2, Cell{74, 206}},
		{-1, -1, 1, Cell{-1, -1}},
	} {
		if got := anchorAt(tc.x, tc.z, tc.foot, tc.foot); got != tc.want {
			t.Errorf("a footprint of %d centred on (%d,%d) is judged at %v, want %v", tc.foot, tc.x, tc.z, got, tc.want)
		}
	}
	// A leg along the middle of a row holds the cells of that row and of no
	// other, the margin to either side included: a wall in the row beside
	// it, which a leg judged half a cell off would have met, does not keep
	// the turn.
	route := routeCells(Cell{0, 2}, Cell{5, 7}, Cell{10, 2})
	for _, wall := range []Cell{{5, 1}, {5, 3}} {
		if out, _ := smooth(t, map[Cell]bool{wall: true}, route); len(out) != 2 {
			t.Fatalf("a wall at %v beside the row kept the turn: %v", wall, out)
		}
	}
	if out, _ := smooth(t, map[Cell]bool{{5, 2}: true}, route); len(out) != 3 {
		t.Fatalf("a leg across a blocked cell of its row was taken: %v", out)
	}
}

// A leg's length is the least integer whose square reaches the squared
// distance, over every distance a leg may span.
func TestSmoothLegLengthRoundsUp(t *testing.T) {
	for d2 := int64(0); d2 <= smoothSpan*smoothSpan; d2++ {
		l := ceilSqrt(d2)
		if l*l < d2 || l > 0 && (l-1)*(l-1) >= d2 {
			t.Fatalf("length of squared distance %d is %d", d2, l)
		}
	}
}

// Remembering a leg's recent anchors changes neither the route nor the work
// charged: every probe is counted, and each anchor's answer is the one a
// fresh judgement gives on that leg.
func TestSmoothLegMemoKeepsRouteAndWork(t *testing.T) {
	route := routeCells(Cell{0, 0}, Cell{6, 9}, Cell{14, 11}, Cell{22, 20}, Cell{30, 21}, Cell{37, 30})
	blocked := func(c Cell) bool { return (c.X*7+c.Z*13)%61 == 0 }
	dear := func(c Cell, dir uint8) int32 { return (c.X*3 + c.Z + int32(dir)) % 5 * 4 }
	cfg := SearchConfig{FootPrintX: 2, FootPrintZ: 2,
		PassableValue: func(c Cell) uint8 {
			if blocked(c) {
				return 0
			}
			return 3
		},
		CostDir: dear,
	}
	s := &smoothSearch{Search: &cannedSearch{route: route}, cfg: cfg}
	s.Resume(100)
	got, _, _ := s.Resume(100)

	// The reference judges every probe afresh.
	probes := 0
	standable := func(x, z int32, dir uint8, limit int32) bool {
		c := anchorAt(x, z, 2, 2)
		probes++
		return dear(c, dir) <= limit && !blocked(c)
	}
	clearLeg := func(a, b Point, limit int32) bool {
		dx, dz := int64(b.X-a.X), int64(b.Z-a.Z)
		d2 := dx*dx + dz*dz
		if d2 > smoothSpan*smoothSpan || d2 == 0 {
			return false
		}
		dir := Octant(dx, dz)
		var length int64
		for length*length < d2 {
			length++
		}
		steps := length/smoothStep + 1
		px, pz := -dz*smoothSide/length, dx*smoothSide/length
		for k := int64(0); k <= steps; k++ {
			x, z := int64(a.X)+dx*k/steps, int64(a.Z)+dz*k/steps
			if !standable(int32(x), int32(z), dir, limit) || !standable(int32(x+px), int32(z+pz), dir, limit) || !standable(int32(x-px), int32(z-pz), dir, limit) {
				return false
			}
		}
		return true
	}
	ref := &smoothSearch{cfg: cfg}
	points := append([]Point(nil), route...)
	var worst [straightenCap]int32
	for i := 0; i+1 < len(points); i++ {
		worst[i] = ref.dearest(points[i], points[i+1])
	}
	probes += ref.probes
	want := []Point{points[0]}
	for i := 0; i < len(points)-1; {
		j := i + 1
		for k := len(points) - 1; k > i+1 && probes < smoothBudget; k-- {
			limit := int32(0)
			for m := i; m < k; m++ {
				limit = max(limit, worst[m])
			}
			if clearLeg(points[i], points[k], limit) {
				j = k
				break
			}
		}
		want = append(want, points[j])
		i = j
	}
	if len(got) != len(want) {
		t.Fatalf("smoothed to %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("smoothed to %v, want %v", got, want)
		}
	}
	if s.probes != probes {
		t.Fatalf("charged %d probes, want %d", s.probes, probes)
	}
	if len(want) == len(route) || len(want) == 2 {
		t.Fatalf("fixture exercises no partial smoothing: %v", want)
	}
}
