package movement

import (
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Laboratory diagnostic (docs/PATHFINDING_LAB.md): what a route search for a
// unit would answer from where it stands, under three readings of the ground.
// It is called on a match that has ended, to look into a jam; nothing in a
// tick calls it. It opens searches of its own and publishes nothing, but it
// does run the request revision a search runs, so a session is not to be
// stepped after it when its fingerprint matters.

// The three readings of LabRouteViews.
const (
	// LabViewSearch is the reading the unit's own search has.
	LabViewSearch = iota
	// LabViewFriends reads friendly mobile units as absent.
	LabViewFriends
	// LabViewStatic reads every mobile unit as absent.
	LabViewStatic
	LabViews
)

// labDiagnoseWork bounds one diagnostic search, in nodes taken off the heap.
const labDiagnoseWork = 400000

// LabRouteView is one search's answer.
type LabRouteView struct {
	// Found reports a published route; Setup a request rejected before its
	// first expansion; neither, a search that ran out of ground.
	Found bool `json:"found"`
	Setup bool `json:"setup,omitempty"`
	// Pops is the search's work, and EndX, EndZ where its route ends, in
	// world units.
	Pops int   `json:"pops"`
	EndX int32 `json:"end_x,omitempty"`
	EndZ int32 `json:"end_z,omitempty"`
	// Points is how many points the route has, and Reaches whether it ends
	// where the goal is satisfied rather than at the nearest ground the
	// request's setup found [04 R-PATH-01 §5].
	Points  int  `json:"points,omitempty"`
	Reaches bool `json:"reaches,omitempty"`
	// Sealed is the verdict of the sealed-goal probe under this reading
	// (path.ProbeGoalSealed), and Frontier how far from the unit, in cells
	// along the longer axis, the nearest ground it found lies.
	Sealed   bool  `json:"sealed,omitempty"`
	Frontier int32 `json:"frontier,omitempty"`
}

// LabRouteViews runs u's route request under the three readings. It reports
// false for a unit with no order that moves it over the ground.
func (s *System) LabRouteViews(u *units.Unit) (out [LabViews]LabRouteView, ok bool) {
	if s == nil || u == nil || u.Def == nil || u.Def.CanFly || s.Terrain == nil {
		return out, false
	}
	binding := handleRow(s.activeOrders, u.Handle)
	var head *orders.Node
	if binding != nil {
		head = binding.order
	}
	if head == nil {
		if q := orders.QueueOfUnit(u); q != nil {
			head = q.Head()
		}
	}
	if head == nil {
		return out, false
	}
	start, goalCell, have := s.pathCellsForOrder(u, head)
	if !have {
		return out, false
	}
	fx, fz := s.pathFootprint(u)
	goal := s.goalForOrderWithFootprint(u, goalCell, head, fx, fz)
	if goal == nil {
		return out, false
	}
	profile := s.ProfileFor(u.Handle)
	reg := s.ensureLayerRegistry()
	if reg == nil {
		return out, false
	}
	reg.BindMappingWordWithCheckpointBinding(s.checkpointMappingWordSource(u.Handle))
	cls := s.classKeyFor(u.Handle)
	layer := reg.For(cls, profile)
	if layer == nil {
		return out, false
	}
	reg.ReviseFor(cls, profile, u.Handle, s.tick)
	footX, footZ := profile.FootPrintX, profile.FootPrintZ
	owner := u.Owner
	learned := s.rules().LearnedTerrain(s)
	hostile := s.hostileMover(owner)
	scale := s.trafficNow.HeuristicScale
	if scale <= 0 {
		scale = 0x18000
	}
	views := [LabViews]func(c path.Cell) uint8{
		LabViewSearch: func(c path.Cell) uint8 {
			if learned != nil {
				return layer.passableLearned(c.X, c.Z, footX, footZ, owner, learned, nil)
			}
			return layer.Passable(c.X, c.Z, footX, footZ, owner)
		},
		LabViewFriends: func(c path.Cell) uint8 {
			return layer.staticPassableKeeping(c.X, c.Z, footX, footZ, owner, learned, hostile, nil)
		},
		LabViewStatic: func(c path.Cell) uint8 {
			return layer.staticPassable(c.X, c.Z, footX, footZ, owner, learned)
		},
	}
	for i, read := range views {
		cfg := path.SearchConfig{
			Start:         start,
			Goal:          goal,
			Scale:         scale,
			FootPrintX:    int32(profile.FootPrintX),
			FootPrintZ:    int32(profile.FootPrintZ),
			StartDir:      uint8((s.headingFor(u.Handle) + 0x1000) >> 13 & 7),
			HasBounds:     true,
			Bounds:        path.Rect{Max: path.Cell{X: s.Terrain.CellW - 1, Z: s.Terrain.CellH - 1}},
			PassableValue: read,
		}
		sess := path.NewSession(cfg)
		var points []path.Point
		var status path.Status
		done := false
		for !done && sess.Popped() < labDiagnoseWork {
			points, status, done = sess.Resume(4096)
		}
		v := LabRouteView{Pops: sess.Popped(), Points: len(points)}
		switch {
		case done && status != path.StatusRejected && len(points) > 0:
			v.Found = true
			end := points[len(points)-1]
			v.EndX, v.EndZ = end.X, end.Z
			if c, ok := path.RouteEndCell(points, cfg.FootPrintX, cfg.FootPrintZ); ok {
				v.Reaches = goal.H(c) == 0
			}
		case done && !sess.Seeded():
			v.Setup = true
		}
		sess.Release()
		var probe path.GoalSealedProbe
		probe, s.unreachableGoals = path.ProbeGoalSealed(start, goal, true, cfg.Bounds, read, s.unreachableGoals)
		v.Sealed = probe.Sealed
		dx, dz := probe.Frontier.X-start.X, probe.Frontier.Z-start.Z
		v.Frontier = max(dx, -dx, dz, -dz)
		out[i] = v
	}
	return out, true
}

// LabGround reads the ground of the cell rectangle from (x0, z0) to
// (x1, z1), both inclusive, as u's search reads it and as it reads with every
// mobile unit absent, one byte an anchor, row by row. It is LabRouteViews'
// picture.
func (s *System) LabGround(u *units.Unit, x0, z0, x1, z1 int32) (search, static []uint8, ok bool) {
	if s == nil || u == nil || u.Def == nil || s.Terrain == nil || x1 < x0 || z1 < z0 {
		return nil, nil, false
	}
	profile := s.ProfileFor(u.Handle)
	reg := s.ensureLayerRegistry()
	if reg == nil {
		return nil, nil, false
	}
	reg.BindMappingWordWithCheckpointBinding(s.checkpointMappingWordSource(u.Handle))
	layer := reg.For(s.classKeyFor(u.Handle), profile)
	if layer == nil {
		return nil, nil, false
	}
	footX, footZ := profile.FootPrintX, profile.FootPrintZ
	learned := s.rules().LearnedTerrain(s)
	n := int(x1-x0+1) * int(z1-z0+1)
	search, static = make([]uint8, 0, n), make([]uint8, 0, n)
	for z := z0; z <= z1; z++ {
		for x := x0; x <= x1; x++ {
			v := layer.Passable(x, z, footX, footZ, u.Owner)
			if learned != nil {
				v = layer.passableLearned(x, z, footX, footZ, u.Owner, learned, nil)
			}
			search = append(search, v)
			static = append(static, layer.staticPassable(x, z, footX, footZ, u.Owner, learned))
		}
	}
	return search, static, true
}

// labRing is the eight sectors' steps, from the north one turning by the
// west [04 R-PATH-01 §3].
var labRing = [8]Cell{{0, -1}, {-1, -1}, {-1, 0}, {-1, 1}, {0, 1}, {1, 1}, {1, 0}, {1, -1}}

// LabRing reads the eight anchors round u's own, from the north one turning
// by the west as the sectors do, as the commit would: '.' for one the
// footprint may step to, 'g' for one the ground refuses, 'f' a feature, 's'
// a structure, 'u' a mobile unit of u's owner and 'e' one of another's.
func (s *System) LabRing(u *units.Unit) string {
	if s == nil || u == nil || s.Terrain == nil {
		return ""
	}
	start, fx16, fz16, ok := s.CommittedFootprint(u.Handle)
	if !ok {
		return ""
	}
	profile := s.ProfileFor(u.Handle)
	fx, fz := int32(fx16), int32(fz16)
	out := make([]byte, 0, 8)
	for d := 0; d < 8; d++ {
		delta := labRing[d]
		a := Cell{X: start.X + delta.X, Z: start.Z + delta.Z}
		code := byte('.')
		if !commitRectInBounds(s.Terrain, a, int16(fx), int16(fz)) {
			code = 'g'
		}
		for dz := int32(0); dz < fz && code == '.'; dz++ {
			for dx := int32(0); dx < fx && code == '.'; dx++ {
				c := Cell{X: a.X + dx, Z: a.Z + dz}
				switch {
				case isFeatureBlocked(s.Terrain, c.X, c.Z):
					code = 'f'
				case !profile.IsPassableCommitCell(s.Terrain, c.X, c.Z):
					code = 'g'
				case s.Grid != nil:
					id, held := s.Grid.OccupantAt(c)
					if !held || id == int(u.Handle) {
						continue
					}
					o := handleRow(s.Collisions, pool.Handle(id))
					ou := s.world.Unit(pool.Handle(id))
					switch {
					case o == nil || o.Building:
						code = 's'
					case ou != nil && ou.Owner != u.Owner:
						code = 'e'
					default:
						code = 'u'
					}
				}
			}
		}
		out = append(out, code)
	}
	return string(out)
}

// LabReach is the ground of one kind of unit and one owner, as it reads with
// every mobile unit absent, labelled by what is connected with what: two
// anchors carry one label when a footprint could be walked from one to the
// other over anchors it may stand on, by the eight steps a search takes.
type LabReach struct {
	w, h  int32
	label []int32
	queue []int32
}

// LabReachKey names the reading LabReachOf makes for u: units of one key
// share it.
func (s *System) LabReachKey(u *units.Unit) string {
	if s == nil || u == nil {
		return ""
	}
	return string(rune('0'+u.Owner)) + ":" + s.classKeyFor(u.Handle)
}

// LabReachOf labels the ground for u's kind and owner into r. It reads the
// class layer as it stands and changes nothing but r.
func (s *System) LabReachOf(u *units.Unit, r *LabReach) bool {
	if s == nil || u == nil || u.Def == nil || s.Terrain == nil || r == nil {
		return false
	}
	profile := s.ProfileFor(u.Handle)
	reg := s.ensureLayerRegistry()
	if reg == nil {
		return false
	}
	reg.BindMappingWordWithCheckpointBinding(s.checkpointMappingWordSource(u.Handle))
	layer := reg.For(s.classKeyFor(u.Handle), profile)
	if layer == nil {
		return false
	}
	footX, footZ := profile.FootPrintX, profile.FootPrintZ
	learned := s.rules().LearnedTerrain(s)
	w, h := s.Terrain.CellW, s.Terrain.CellH
	r.w, r.h = w, h
	n := int(w) * int(h)
	if cap(r.label) < n {
		r.label = make([]int32, n)
		r.queue = make([]int32, 0, n)
	}
	r.label = r.label[:n]
	for i := range r.label {
		r.label[i] = -1 // not read yet
	}
	read := func(i int32) bool {
		if r.label[i] == -1 {
			r.label[i] = 0
			if layer.staticPassable(i%w, i/w, footX, footZ, u.Owner, learned) == LayerBlocked {
				r.label[i] = -2
			}
		}
		return r.label[i] == 0
	}
	next := int32(0)
	for i := int32(0); i < int32(n); i++ {
		if !read(i) {
			continue
		}
		next++
		r.label[i] = next
		r.queue = append(r.queue[:0], i)
		for len(r.queue) > 0 {
			c := r.queue[len(r.queue)-1]
			r.queue = r.queue[:len(r.queue)-1]
			cx, cz := c%w, c/w
			for _, d := range labRing {
				x, z := cx+d.X, cz+d.Z
				if x < 0 || z < 0 || x >= w || z >= h {
					continue
				}
				if j := z*w + x; read(j) {
					r.label[j] = next
					r.queue = append(r.queue, j)
				}
			}
		}
	}
	return true
}

// LabGoalOpen reports whether the ground r labels connects where u stands
// with a cell of the goal of its head order; ok is false for a unit with no
// order that moves it over the ground. It changes nothing.
func (s *System) LabGoalOpen(u *units.Unit, r *LabReach) (open, ok bool) {
	if s == nil || u == nil || r == nil || len(r.label) == 0 {
		return false, false
	}
	q := orders.QueueOfUnit(u)
	if q == nil || q.Head() == nil {
		return false, false
	}
	head := q.Head()
	start, goalCell, have := s.pathCellsForOrder(u, head)
	if !have {
		return false, false
	}
	fx, fz := s.pathFootprint(u)
	goal := s.goalForOrderWithFootprint(u, goalCell, head, fx, fz)
	if goal == nil {
		return false, false
	}
	at := func(c path.Cell) int32 {
		if c.X < 0 || c.Z < 0 || c.X >= r.w || c.Z >= r.h {
			return -2
		}
		return r.label[c.Z*r.w+c.X]
	}
	mine := at(start)
	if mine <= 0 {
		// It stands where its kind may not: on a wreck, or on ground the
		// class layer's reading keeps it from. Any neighbour's ground is its
		// own then.
		for _, d := range labRing {
			if l := at(path.Cell{X: start.X + d.X, Z: start.Z + d.Z}); l > 0 {
				mine = l
				break
			}
		}
	}
	if mine <= 0 {
		return false, true
	}
	if goal.StartSatisfied(start) {
		return true, true
	}
	s.unreachableGoals = goal.Enumerate(s.unreachableGoals[:0])
	for _, c := range s.unreachableGoals {
		if at(c) == mine {
			return true, true
		}
	}
	// A goal with a radius is satisfied on cells it does not list: look
	// round the listed ones.
	// The first is enough: a goal's cells lie together.
	const round = 12
	if len(s.unreachableGoals) == 0 {
		return false, true
	}
	g := s.unreachableGoals[0]
	for z := g.Z - round; z <= g.Z+round; z++ {
		for x := g.X - round; x <= g.X+round; x++ {
			if c := (path.Cell{X: x, Z: z}); at(c) == mine && goal.H(c) == 0 {
				return true, true
			}
		}
	}
	return false, true
}

// LabSteer reports how h's last visit steered: the side its sidestep keeps
// to, +1 left or -1 right, whether the visit steered round something, and
// what (SteerStatic and the rest).
func (s *System) LabSteer(h pool.Handle) (side int8, steering bool, ahead uint8) {
	if s == nil {
		return 0, false, 0
	}
	st := handleRow(s.traffic, h)
	if !st.steering {
		return st.side, false, SteerNone
	}
	return st.side, true, st.ahead
}

// LabCells reads what stands on each cell of the rectangle from (x0, z0) to
// (x1, z1), both inclusive, row by row: 'f' for a feature that blocks, 's'
// for a structure, 'u' for a mobile unit, 'g' for ground u's kind may not
// stand on, and '.' for none of these. It is LabGround's companion: an
// anchor is a footprint of cells.
func (s *System) LabCells(u *units.Unit, x0, z0, x1, z1 int32) []byte {
	if s == nil || u == nil || s.Terrain == nil || x1 < x0 || z1 < z0 {
		return nil
	}
	profile := s.ProfileFor(u.Handle)
	out := make([]byte, 0, int(x1-x0+1)*int(z1-z0+1))
	for z := z0; z <= z1; z++ {
		for x := x0; x <= x1; x++ {
			code := byte('.')
			switch {
			case isFeatureBlocked(s.Terrain, x, z):
				code = 'f'
			case !profile.IsPassableCommitCell(s.Terrain, x, z):
				code = 'g'
			case s.Grid != nil:
				if id, held := s.Grid.OccupantAt(Cell{X: x, Z: z}); held {
					code = 'u'
					if c := handleRow(s.Collisions, pool.Handle(id)); c == nil || c.Building {
						code = 's'
					}
				}
			}
			out = append(out, code)
		}
	}
	return out
}
