package movement

import "github.com/nanolathe-gg/nanolathe/internal/pool"

// Nanolathe Modern policy: a held follower waits for its way to change
// (docs/DESIGN_MOVEMENT_PATH.md "Modern prompt re-routing", "Held followers").
// Strict 3.1 and Community 3.9 answer RepathDelay with retail's sixty
// [04 R-MOV-01 §7] and never reach any of this.
//
// Modern lets a follower ask for a route again fifteen ticks after its last
// admission. A follower that is still standing where that admission found it,
// at the start of the route the search gave it, and whose last step a mobile
// unit refused, would search again from the same cell to the same goal over
// the same ground: the request revision pass walls a mobile occupant only once
// its commit tick is more than thirty ticks old [04 R-PATH-01 §14], so until
// then the unit in its way reads as passable to the new search as it did to
// the old one. Such a follower is admitted again when that unit would be
// walled, if it has not moved meanwhile — no earlier than fifteen ticks and no
// later than sixty after its last admission. Everything it reads is committed
// state the movement system already keeps; it writes nothing.

// modernHeldRepathCap is the longest a held follower waits after its last
// admission: retail's own throttle period [04 R-MOV-01 §7]. It is Nanolathe
// Modern policy tuning (docs/DESIGN_MOVEMENT_PATH.md "Modern prompt
// re-routing").
const modernHeldRepathCap = retailRepathDelay

// occupantWallAge is how many ticks after its last commit a mobile occupant is
// first walled by a search's revision pass: the watermark is the tick less
// thirty, and the gate walls a commit tick below it [04 R-PATH-01 §14].
const occupantWallAge = 31

// modernRepathDelayFor is ModernRules.RepathDelay for the follower at slot,
// last admitted at tick last.
func (s *System) modernRepathDelayFor(slot int, last uint32) uint32 {
	if s == nil {
		return modernRepathDelay
	}
	h := pool.Handle(slot)
	coll := handleRow(s.Collisions, h)
	// Refused by a unit, and no cell changed since the last admission: a
	// search now starts from the cell that one started from. The commit tick
	// is written only when the footprint changes cells.
	if coll == nil || !coll.Blocked || coll.BlockerID <= 0 || coll.LastStampTick > last {
		return modernRepathDelay
	}
	blocker := handleRow(s.Collisions, pool.Handle(coll.BlockerID))
	if blocker == nil || blocker.Building || !s.standsAtRouteStart(h, coll) {
		// A building is walled from its first classification
		// [04 R-PATH-01 §14]; a follower whose route was not searched from
		// where it stands — a new goal's straight line, or a route it has
		// walked along — has something new to ask.
		return modernRepathDelay
	}
	walled := blocker.LastStampTick + occupantWallAge
	switch {
	case walled <= last+modernRepathDelay:
		return modernRepathDelay
	case walled >= last+modernHeldRepathCap:
		return modernHeldRepathCap
	}
	return walled - last
}

// standsAtRouteStart reports whether the route h holds begins at the cell its
// committed footprint stands on, in the published form of a search's first
// point: the start cell doubled and narrowed to sixteen bits, plus the
// requester's footprint, times eight [04 R-PATH-01 §7]. A route the search
// gave the follower begins there until the follower passes its first
// waypoint; the goal installer's straight line begins at the unit's own
// position [04 R-PATH-01 §8].
func (s *System) standsAtRouteStart(h pool.Handle, coll *CollisionState) bool {
	route := handleRow(s.Routes, h)
	if route == nil || route.Count == 0 {
		return false
	}
	p := s.ProfileFor(h)
	a := coll.CachedAnchor
	here := Point{X: (int32(int16(a.X*2)) + int32(p.FootPrintX)) * 8, Z: (int32(int16(a.Z*2)) + int32(p.FootPrintZ)) * 8}
	return route.Points[0] == here
}
