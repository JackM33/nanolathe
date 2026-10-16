package movement

import (
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// What the traffic policy reads of a mover's friends
// (docs/DESIGN_MOVEMENT_PATH.md "Modern traffic"): which of them are on
// their way somewhere, and which stay walls to a search or to a straight leg
// that reads friendly units as absent. Everything here reads committed state
// with integer arithmetic and draws from neither random stream.

// noteGoal records a mover's goal when it is installed. A goal installed
// again — the retry of [04 R-ORD-01 §4] — changes nothing; a new one ends
// the count of a unit whose searches have found nothing. It writes nothing
// under the zero traffic policy.
func (s *System) noteGoal(h pool.Handle, x, z numeric.Fixed) {
	if s.trafficNow.Pilot == nil && s.trafficNow.Through == 0 {
		return
	}
	st := handleRow(s.traffic, h)
	if st.hasGoal && st.goalX == x && st.goalZ == z {
		return
	}
	st.hasGoal, st.goalX, st.goalZ = true, x, z
	st.routeless = false
	setHandleRow(&s.traffic, h, st)
}

// onItsWay reports whether unit o is a mover with somewhere to go: a live,
// finished, grounded mobile unit with a goal installed or a route.
func (s *System) onItsWay(o *units.Unit) bool {
	if o == nil || !o.Alive || o.Dying || o.Def == nil || o.Remaining != 0 || o.Attachment.Carrier != 0 ||
		o.Def.BMCode != 1 || !o.Def.CanMove || o.Def.CanFly || o.Def.MaxVelocity == 0 || o.Move.Mode&3 != 1 {
		return false
	}
	if s.hasControllerGoal(o.Handle) {
		return true
	}
	r := handleRow(s.Routes, o.Handle)
	return r != nil && r.Active && r.Count >= 2
}

// keepsItsGround is the wall test of a reading that takes friendly movers
// for absent: unit id is another player's, or a friendly unit with nowhere
// to go — a parked one, or one at work. A friendly mover is on its way
// somewhere and will not be standing there when the route reaches it. Like
// hostileMover's, the test reads live state when it is called and is built
// once per owner (System.groundKeepers).
func (s *System) keepsItsGround(owner uint8) func(id int) bool {
	if owner < 10 {
		if fn := s.groundKeepers[owner]; fn != nil {
			return fn
		}
	}
	hostile := s.hostileMover(owner)
	fn := func(id int) bool {
		if hostile(id) {
			return true
		}
		o := s.world.Unit(pool.Handle(id))
		return o != nil && !s.onItsWay(o)
	}
	if owner < 10 {
		s.groundKeepers[owner] = fn
	}
	return fn
}

// passableThrough is the passability read that takes friendly units for
// absent: the static view with the units keep names still walls, and an
// anchor only friendly movers hold read at its ground's own tier, so that a
// route through a column of friends costs what the ground costs. own is the
// search's own reading of its requester's footprint, nil for the shared
// layer's (own_footprint.go).
func (l *ClassLayer) passableThrough(x, z int32, footX, footZ int16, player uint8, learned *LearnedTerrain, keep func(id int) bool, own *ownFootprint) uint8 {
	v := l.passableOwn(x, z, footX, footZ, player, own)
	if v != LayerBlocked {
		if v == LayerUnmapped && learned != nil {
			return l.staticPassableKeeping(x, z, footX, footZ, player, learned, keep, own)
		}
		return v
	}
	if l.staticPassableKeeping(x, z, footX, footZ, player, learned, keep, own) == LayerBlocked {
		return LayerBlocked
	}
	fx, fz := l.footprintSize()
	for cz := z; cz < z+fz; cz++ {
		for cx := x; cx < x+fx; cx++ {
			if l.Profile.classifyCell(l.Terrain, cx, cz) != ClassClear {
				return LayerSteep
			}
		}
	}
	return LayerClear
}
