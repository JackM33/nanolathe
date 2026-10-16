package client

import (
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/poseblend"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// walkState belongs to the recorder, including its speculative pass (§13.10).
// Preparation runs before the unit pool wakes; each worker then reads only its
// unit's history. No history points into a reusable publication buffer [I6].
type walkState struct {
	walkBuffer  *frame.Buffer
	walk        map[pool.Handle]*walkHistory
	walkForUnit []*walkHistory
	walkTick    uint32
	walkValid   bool
	// walkFree holds the histories of units that stopped walking, their pose
	// storage kept, for the next unit that starts: units start and stop all
	// battle long, and a fresh history allocated its per-piece endpoint
	// storage each time. It never holds more than the most histories ever
	// live at once.
	walkFree []*walkHistory
}

type walkHistory struct {
	pose poseblend.History
	last walkSample
	seen bool
}

// A shared committed tick must name the same subject and root position in
// both pairs. In addition to previousUnit's adjacent-pair checks, this rejects
// stale history after a load or replacement of a publication at the same tick.
type walkSample struct {
	instance   uint64
	slot       pool.Handle
	definition uint16
	owner      uint8
	x, y, z    numeric.Fixed
	pieces     int
}

func walkSampleOf(u *frame.UnitView) walkSample {
	return walkSample{u.InstanceID, u.Slot, u.DefID, u.Owner, u.X, u.Y, u.Z, len(u.Pieces)}
}

func walkEligible(u *frame.UnitView) bool {
	return !u.IsBuilding && u.MoverMode == 1 && u.Carrier == 0 && u.BuildRemaining == 0
}

func (in *interpolator) resetWalk() {
	for _, h := range in.walk {
		in.releaseWalk(h)
	}
	clear(in.walk)
	clear(in.walkForUnit)
	in.walkValid = false
	in.blendValid = false
	in.pairValid = false
}

// prepareWalk consumes only the available adjacent publications. An older
// member of a repeated pair is never re-recorded, and a missing tick resets
// the filter rather than inventing a pose (DESIGN_GPU_RENDERER §13.5).
func (in *interpolator) prepareWalk(prev, cur *frame.Frame) {
	clear(in.walkForUnit)
	in.walkForUnit = growSlice(in.walkForUnit, len(cur.Units))
	if cur.Tick == 0 || prev.Tick+1 != cur.Tick || (in.walkValid && cur.Tick < in.walkTick) {
		in.resetWalk()
		if cur.Tick == 0 || prev.Tick+1 != cur.Tick {
			return
		}
	}
	in.walkTick, in.walkValid = cur.Tick, true
	for _, h := range in.walk {
		h.seen = false
	}
	for i := range cur.Units {
		u := &cur.Units[i]
		p := in.previousUnit(prev, u)
		if p == nil || !walkEligible(u) || !walkEligible(p) || (p.X == u.X && p.Y == u.Y && p.Z == u.Z) {
			continue
		}
		if in.walk == nil {
			in.walk = make(map[pool.Handle]*walkHistory)
		}
		h := in.walk[u.Slot]
		if h == nil {
			if n := len(in.walkFree); n > 0 {
				h = in.walkFree[n-1]
				in.walkFree[n-1] = nil
				in.walkFree = in.walkFree[:n-1]
			} else {
				h = &walkHistory{}
			}
			in.walk[u.Slot] = h
		}
		last, valid := h.pose.LastTick()
		if valid {
			switch {
			case last == cur.Tick:
				if h.last != walkSampleOf(u) {
					h.pose.Reset()
				}
			case last == prev.Tick:
				if h.last != walkSampleOf(p) {
					h.pose.Reset()
				}
			case last+1 != prev.Tick:
				h.pose.Reset()
			default:
				// Both available samples are new. Identity must still match
				// the retained subject; the shared position is unavailable.
				s := walkSampleOf(p)
				s.x, s.y, s.z = h.last.x, h.last.y, h.last.z
				if h.last != s || !continuousUnitPosition(h.last.x, h.last.z, p.X, p.Z) ||
					(h.last.x == p.X && h.last.y == p.Y && h.last.z == p.Z) {
					h.pose.Reset()
				}
			}
		}
		for _, sample := range []struct {
			tick uint32
			unit *frame.UnitView
		}{{prev.Tick, p}, {cur.Tick, u}} {
			last, valid := h.pose.LastTick()
			if !valid || sample.tick > last {
				h.pose.Record(sample.tick, sample.unit.Pieces)
				h.last = walkSampleOf(sample.unit)
			}
		}
		h.seen = true
		in.walkForUnit[i] = h
	}
	// Removal, stop, capture, transport and construction release retained pose
	// storage immediately. Iteration order affects no result: this is solely
	// renderer-owned garbage collection, never a simulation path [I1][I6].
	for slot, h := range in.walk {
		if !h.seen {
			delete(in.walk, slot)
			in.releaseWalk(h)
		}
	}
}

// releaseWalk resets a history to the zero history's state, keeping its pose
// storage, and puts it on the free list.
func (in *interpolator) releaseWalk(h *walkHistory) {
	h.pose.Reset()
	h.last, h.seen = walkSample{}, false
	in.walkFree = append(in.walkFree, h)
}
