package movement

import (
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// ClaimsPilot (Nanolathe Modern policy, docs/DESIGN_MOVEMENT_PATH.md "Modern
// route claims") makes a route search aware of where friendly traffic is
// about to go.
//
// A search that knows only the ground sends every member of a group down the
// same shortest line and through the same gap, where they queue; the gap next
// to it stands empty. Here every ground mover claims the ground its route
// crosses over the next stretch, a search pays for each claim another unit of
// its owner holds on an anchor, and the pass over the finished route may not
// pull a leg across ground dearer than the search accepted (path.SmoothKernel).
//
// A claim has a direction, the way its unit will cross the ground. With
// Oncoming, a step against a claim costs more than a step along it: two
// streams that meet plan lanes beside each other instead of one through the
// other.
//
// With Rank, a unit that turns slowly pays only for the claims of other such
// units, and a nimble one pays for every claim: vehicles keep the direct way
// and the flat ground, and the units that lose least by going round are the
// ones that go round.
//
// Claims are counted afresh every few ticks from the routes units hold, so
// nothing is remembered that a saved game would have to carry. Nothing here
// steers, slows or moves a unit. Integer arithmetic, pool order, no map
// ranged, neither random stream drawn from.
type ClaimsPilot struct {
	NoPilot
	// Per is what each claim adds to a step that goes its way, in the
	// search's cost units (a cardinal step is sixteen): eight when zero.
	Per int32
	// Oncoming, when positive, is what a claim adds to a step that goes
	// against it, three sectors or more off the claim's own way.
	Oncoming int32
	// Rank exempts slow-turning units from the claims of nimble ones.
	Rank bool
}

const (
	// claimCap is the most claims a step pays for, along and against
	// counted apart; claimReach how far along its route a unit claims, in
	// world units; and claimEvery how many ticks pass between counts.
	claimCap   = 6
	claimReach = 768
	claimEvery = 4
	// claimShift is the claim grid's block size as a shift of world units:
	// blocks of thirty-two, one two-cell footprint wide.
	claimShift = 5
	// claimSample is the spacing of the samples taken along a route.
	claimSample = 16
	// claimNimble is the least authored turn rate of a nimble unit. The
	// stock walkers turn at 900 and more, the stock vehicles at 512 and less.
	claimNimble = 700
)

// claimGrid is one owner's claims: per block, the claims that cross it in
// each of the search's eight sectors, of every unit and of the slow-turning
// ones.
type claimGrid struct {
	all, slow                     [][8]uint8
	checkpointAll, checkpointSlow *checkpointClaimRow
	// written lists the blocks that hold a claim, so that a count clears
	// those and not the whole map.
	written []int32
}

type claimState struct {
	w, h  int32
	grids []*claimGrid // by owner
	// own marks the requester's own claims: the search's serial and the
	// claim's sector.
	own           []uint32
	checkpointOwn *checkpointClaimRow
	// slots are the own marks of the searches in Modern path batch places
	// one and up, place zero's being own: searches of one batch are open at
	// once, and each must recognise its own claims however the others'
	// overlap them (docs/DESIGN_MOVEMENT_PATH.md "Modern parallel path
	// batches"). Each row is made when its place is first used.
	slots  []*checkpointClaimRow
	serial uint32
	have   bool
	trail  []int32 // scratch: one route's claims, block and sector
}

func claimStateOf(s *System) *claimState {
	st, _ := s.PilotState.(*claimState)
	if st == nil {
		st = &claimState{}
		s.PilotState = st
	}
	return st
}

// per is what a claim along the way adds.
func (p ClaimsPilot) per() int32 {
	if p.Per == 0 {
		return 8
	}
	return p.Per
}

// claimSlow reports a unit that turns slowly.
func claimSlow(u *units.Unit) bool {
	return u.Def != nil && u.Def.TurnRate < claimNimble
}

func (st *claimState) grid(owner uint8) *claimGrid {
	for int(owner) >= len(st.grids) {
		st.grids = append(st.grids, nil)
	}
	g := st.grids[owner]
	if g == nil {
		n := int(st.w) * int(st.h)
		g = &claimGrid{all: make([][8]uint8, n), slow: make([][8]uint8, n)}
		g.checkpointAll = &checkpointClaimRow{kind: 2, counts: g.all}
		g.checkpointSlow = &checkpointClaimRow{kind: 2, counts: g.slow}
		st.grids[owner] = g
	}
	return g
}

// trace fills the scratch list with the claims of a unit standing at (x, z)
// along the first reach world units of its route: the block, shifted up by
// three, and the sector it is crossed in; each once in a row.
func (st *claimState) trace(x, z int32, r *Route, reach int32) []int32 {
	st.trail = st.trail[:0]
	last := int32(-1)
	left := int64(reach)
	px, pz := int64(x), int64(z)
	for i := 1; i < int(r.Count) && left > 0; i++ {
		qx, qz := int64(r.Points[i].X), int64(r.Points[i].Z)
		dx, dz := qx-px, qz-pz
		length := int64(isqrt(uint64(dx*dx + dz*dz)))
		if length == 0 {
			continue
		}
		sector := int32(path.Octant(dx, dz))
		span := min(length, left)
		steps := span/claimSample + 1
		for k := int64(0); k <= steps; k++ {
			sx, sz := px+dx*span*k/(steps*length), pz+dz*span*k/(steps*length)
			bx, bz := int32(sx>>claimShift), int32(sz>>claimShift)
			if bx < 0 || bz < 0 || bx >= st.w || bz >= st.h {
				continue
			}
			if c := (bz*st.w+bx)<<3 | sector; c != last {
				st.trail = append(st.trail, c)
				last = c
			}
		}
		left -= span
		px, pz = qx, qz
	}
	return st.trail
}

// claimSearched reports a route a search published, as against the goal
// installer's straight line from where the unit stood to its goal
// [04 R-PATH-01 §8]: a searched route's points are anchor centres.
func claimSearched(r *Route, fx, fz int32) bool {
	if r == nil || !r.Active || r.Count < 2 {
		return false
	}
	if r.Count > 2 {
		return true
	}
	for _, p := range r.Points[:2] {
		if (p.X-8*fx)&15 != 0 || (p.Z-8*fz)&15 != 0 {
			return false
		}
	}
	return true
}

// BeginTick counts the claims afresh when the count is due.
func (p ClaimsPilot) BeginTick(s *System, tick uint32) {
	if s.Terrain == nil || s.world == nil || s.Terrain.CellW <= 0 || s.Terrain.CellH <= 0 {
		return
	}
	st := claimStateOf(s)
	// The count is due on every tick the period divides, so that a restored
	// game counts on the ticks the saved one would have, and at once when
	// there is none to read.
	if st.have && tick%claimEvery != 0 {
		return
	}
	w, h := (s.Terrain.CellW*16)>>claimShift+1, (s.Terrain.CellH*16)>>claimShift+1
	if w != st.w || h != st.h {
		st.w, st.h = w, h
		st.grids = st.grids[:0]
		st.own = make([]uint32, int(w)*int(h))
		st.checkpointOwn = &checkpointClaimRow{kind: 1, own: st.own}
		st.slots = nil
	}
	for _, g := range st.grids {
		if g == nil {
			continue
		}
		for _, b := range g.written {
			g.all[b], g.slow[b] = [8]uint8{}, [8]uint8{}
		}
		g.written = g.written[:0]
	}
	st.have = true
	for hd := 1; hd < len(s.Collisions); hd++ {
		c := s.Collisions[hd]
		if c == nil || c.Building || c.Mode != 1 || c.CachedMode != 1 {
			continue
		}
		u := s.world.Unit(pool.Handle(hd))
		if u == nil || !u.Alive || u.Def == nil || u.Attachment.Carrier != 0 {
			continue
		}
		r := handleRow(s.Routes, pool.Handle(hd))
		if !claimSearched(r, int32(max(c.FootPrintX, 1)), int32(max(c.FootPrintZ, 1))) {
			continue
		}
		g := st.grid(u.Owner)
		slow := claimSlow(u)
		for _, claim := range st.trace(int32(u.X>>16), int32(u.Z>>16), r, claimReach) {
			b, sector := claim>>3, claim&7
			if g.all[b] == ([8]uint8{}) {
				g.written = append(g.written, b)
			}
			if g.all[b][sector] < 255 {
				g.all[b][sector]++
			}
			if slow && g.slow[b][sector] < 255 {
				g.slow[b][sector]++
			}
		}
	}
}

// ownRow is the own-mark row of the search in batch place slot.
func (st *claimState) ownRow(slot int) ([]uint32, *checkpointClaimRow) {
	if slot <= 0 {
		return st.own, st.checkpointOwn
	}
	for len(st.slots) < slot {
		st.slots = append(st.slots, nil)
	}
	h := st.slots[slot-1]
	if h == nil {
		h = &checkpointClaimRow{kind: 1, own: make([]uint32, len(st.own))}
		st.slots[slot-1] = h
	}
	return h.own, h
}

// Search charges the request for the claims of the requester's owner, its own
// left out.
func (p ClaimsPilot) Search(s *System, r path.Request, cfg *path.SearchConfig) {
	if s.PilotState == nil || s.world == nil {
		return
	}
	st := claimStateOf(s)
	if !st.have {
		return
	}
	u := s.world.Unit(r.Unit)
	if u == nil || u.Def == nil || int(u.Owner) >= len(st.grids) || st.grids[u.Owner] == nil {
		return
	}
	g := st.grids[u.Owner]
	counts := g.all
	countsHolder, row := g.checkpointAll, uint8(2)
	if p.Rank && claimSlow(u) {
		counts = g.slow
		countsHolder, row = g.checkpointSlow, 3
	}
	fx, fz := max(cfg.FootPrintX, 1), max(cfg.FootPrintZ, 1)
	// The serial keeps twenty-nine bits beside the sector.
	st.serial++
	if st.serial >= 1<<29 {
		clear(st.own)
		for _, h := range st.slots {
			if h != nil {
				clear(h.own)
			}
		}
		st.serial = 1
	}
	serial := st.serial
	// A search of a Modern path batch marks its own claims in its place's
	// row, which no other open search writes.
	own, ownHolder := st.ownRow(s.searchSlot)
	if route := handleRow(s.Routes, r.Unit); claimSearched(route, fx, fz) {
		for _, claim := range st.trace(int32(u.X>>16), int32(u.Z>>16), route, claimReach) {
			own[claim>>3] = serial<<3 | uint32(claim&7)
		}
	}
	w, h := st.w, st.h
	per, against := p.per(), p.Oncoming
	if roots := s.checkpointSearch; roots != nil && cfg == &s.searchCfg {
		roots.cost = &checkpointAccessorCapture{
			value: path.CheckpointAccessor{Kind: 7, Owner: u.Owner, Serial: serial,
				Width: w, Height: h, FootprintX: fx, FootprintZ: fz, Per: per, Against: against, Row: row},
			counts: countsHolder, own: ownHolder,
		}
		if countsHolder == nil || ownHolder == nil {
			// Authored rows without construction metadata cannot prove aliases.
			roots.unsupported = "TODO(M3-U6): claim rows lack checkpoint holders"
		}
	}
	cfg.CostDir = func(c path.Cell, dir uint8) int32 {
		// The footprint's centre, in world units.
		x, z := c.X*16+8*fx, c.Z*16+8*fz
		bx, bz := x>>claimShift, z>>claimShift
		if bx < 0 || bz < 0 || bx >= w || bz >= h {
			return 0
		}
		b := bz*w + bx
		row := &counts[b]
		if *row == ([8]uint8{}) {
			return 0
		}
		mine := int32(-1)
		if own[b]>>3 == serial {
			mine = int32(own[b] & 7)
		}
		var along, facing int32
		for sector := int32(0); sector < 8; sector++ {
			n := int32(row[sector])
			if sector == mine && n > 0 {
				n--
			}
			if n == 0 {
				continue
			}
			if against > 0 && dir < 8 {
				if off := (sector - int32(dir)) & 7; off >= 3 && off <= 5 {
					facing += n
					continue
				}
			}
			along += n
		}
		return min(along, claimCap)*per + min(facing, claimCap)*against
	}
}
