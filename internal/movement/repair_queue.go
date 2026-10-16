package movement

import (
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Nanolathe Modern policy, DESIGN_MOVEMENT_PATH "Modern repair-pad queue".
// These are queue tuning, not retail constants: the claim retry, the smallest
// loiter radius a waiter flies, and the departure ring's radius and spacing.
// The loiter leg itself, the normal approach, descent, attachment and
// resource-admitted SelfRepair remain retail's.
const (
	repairQueueRetry = 30
	repairHoldRadius = 192
	repairHoldSpace  = 64
)

type repairLanding struct {
	unit     *units.Unit
	node     *orders.Node
	pad      *units.Unit
	piece    uint16
	reserved bool
	holding  bool // flying the loiter leg about anchor
	anchor   Vec3
}

func (s *System) discardRepairLanding(n *orders.Node) {
	for i, entry := range s.repairLandings {
		if entry.node == n {
			copy(s.repairLandings[i:], s.repairLandings[i+1:])
			s.repairLandings[len(s.repairLandings)-1] = nil
			s.repairLandings = s.repairLandings[:len(s.repairLandings)-1]
			return
		}
	}
}

func (s *System) pruneRepairLandings() {
	if len(s.repairLandings) == 0 {
		return
	}
	if !s.rules().RepairPadQueue(s) {
		clear(s.repairLandings)
		s.repairLandings = s.repairLandings[:0]
		return
	}
	for i := len(s.repairLandings) - 1; i >= 0; i-- {
		e := s.repairLandings[i]
		retained := false
		if q := orders.QueueOfUnit(e.unit); q != nil {
			for i, n := 0, q.PrimaryLen(); i < n; i++ {
				if q.PrimaryAt(i) == e.node {
					retained = true
					break
				}
			}
		}
		if s.unitFor(e.unit.Handle) != e.unit || !e.unit.Alive || e.unit.Dying ||
			!retained || len(e.unit.Attachment.Cargo) != 0 {
			s.discardRepairLanding(e.node)
		}
	}
}

func (s *System) usableRepairPad(u, pad *units.Unit) bool {
	if pad == nil || !pad.Alive || pad.Dying || pad.Def == nil ||
		!pad.Def.IsAirBase || !pad.Def.Builder || pad.Remaining != 0 ||
		!pad.Activated || pad.Attachment.Carrier != 0 {
		return false
	}
	if u.Owner == pad.Owner {
		return true
	}
	b := airBinding(u)
	return b != nil && b.World != nil && b.World.DeclaresAllianceHook() != nil &&
		b.World.DeclaresAllianceHook()(u.Owner, pad.Owner) && b.World.DeclaresAllianceHook()(pad.Owner, u.Owner)
}

// repairPlanarDistance is the raw 16.16 planar distance the queue ranks
// replacement bases and threat clearance by. It is Nanolathe Modern policy
// arithmetic (DESIGN_MOVEMENT_PATH "Modern repair-pad queue"), not a retail
// distance: the exact integer floor of the Euclidean distance over 64-bit
// differences, which never wraps. The retail attack-leg distance,
// airPlanarDistance, wraps its raw differences and reads a signed low word
// [04 R-AIR-01 §8], so from 32,768 world units it reads negative — nearer than
// anything — and would let a base or threat across a large map outrank a close
// one. This form is exact for any separation below 65,536 world units on each
// axis (raw differences below 2^32, so the sum of squares fits the unsigned
// 64-bit radicand), which covers every retail-sized map; the map format's own
// ceiling is larger, and beyond that bound this would wrap as well.
func repairPlanarDistance(ax, az, bx, bz numeric.Fixed) int64 {
	dx := int64(ax) - int64(bx)
	dz := int64(az) - int64(bz)
	return int64(isqrt(uint64(dx*dx + dz*dz)))
}

// Only a lost/unavailable base triggers a new selection. A full base retains
// its queue, so patients do not chase the same newly freed piece elsewhere.
func (s *System) replacementRepairPad(u *units.Unit) *units.Unit {
	var best *units.Unit
	var distance int64
	for _, pad := range s.world.IterSliced() {
		if pad == u || !s.usableRepairPad(u, pad) {
			continue
		}
		d := repairPlanarDistance(u.X, u.Z, pad.X, pad.Z)
		if best == nil || d < distance {
			best, distance = pad, d
		}
	}
	return best
}

// grantRepairPieces preserves existing reservations, then fills free authored
// pieces in FIFO order. Duplicate script outputs cannot grant a piece twice.
func (s *System) grantRepairPieces(pad *units.Unit) {
	if pad == nil || pad.ScriptBridge() == nil {
		return
	}
	result := pad.ScriptBridge().QueryLandingPad()
	var pieces [airPadCandidates]uint16
	count := 0
	for _, value := range result.Values {
		if value < 0 {
			continue
		}
		piece := uint16(value)
		duplicate := false
		for _, prior := range pieces[:count] {
			duplicate = duplicate || prior == piece
		}
		if !duplicate && s.padPieceFree(pad, piece) {
			pieces[count] = piece
			count++
		}
	}
	var taken [airPadCandidates]bool
	for _, e := range s.repairLandings {
		if e.pad != pad || !e.reserved {
			continue
		}
		e.reserved = false
		if !s.usableRepairPad(e.unit, pad) || e.node.Target != pad.Handle {
			continue
		}
		for i, piece := range pieces[:count] {
			if e.piece == piece && !taken[i] {
				taken[i], e.reserved = true, true
				break
			}
		}
		if !e.reserved {
			e.node.Phase = 1
		}
	}
	for _, e := range s.repairLandings {
		if e.pad != pad || e.reserved || e.node.Target != pad.Handle || !s.usableRepairPad(e.unit, pad) {
			continue
		}
		for i, piece := range pieces[:count] {
			if !taken[i] {
				e.piece, e.reserved, taken[i] = piece, true, true
				break
			}
		}
	}
}

func (s *System) landingPiece(pad *units.Unit, e *repairLanding) (uint16, bool) {
	if e == nil {
		return s.queryLandingPad(pad)
	}
	return e.piece, e.reserved && e.pad == pad && s.padPieceFree(pad, e.piece)
}

func (s *System) legQueuedRepairLanding(u *units.Unit, n *orders.Node, satisfied uint32, tick uint32) orders.Code {
	if !s.airMoverReady(u) {
		return 7
	}
	pad := s.unitFor(n.Target)
	s.pruneRepairLandings()
	var entry *repairLanding
	for _, e := range s.repairLandings {
		if e.node == n {
			entry = e
			break
		}
	}
	// Non-repair commands retain the ordinary landing behavior. An existing
	// patient whose target was captured/replaced instead seeks another base.
	if entry == nil && pad != nil && pad.Def != nil && (!pad.Def.IsAirBase || !pad.Def.Builder) {
		return s.legPadLanding(u, n, satisfied, tick, nil)
	}
	if entry == nil {
		entry = &repairLanding{unit: u, node: n, pad: pad, anchor: Vec3{X: u.X, Y: u.Y, Z: u.Z}}
		// A waiter restored from a save, or re-admitted after a switch from
		// Strict, keeps flying the leg its record still owns until it arrives:
		// the marker, bearing and gate persist with the record.
		entry.holding = n.Phase == 1 && s.airPayloadOwner(u.Handle) == n
		s.repairLandings = append(s.repairLandings, entry)
		// Restored and interrupted approaches acquire a fresh reservation
		// before descending. COB is queried here, never during save restore.
		if n.Phase > 1 {
			n.Phase = 1
		}
	}
	if entry.pad != pad || !s.usableRepairPad(u, pad) {
		pad = s.replacementRepairPad(u)
		if pad != entry.pad {
			entry.holding = false // a new base is a new loiter centre
		}
		entry.pad, entry.reserved = pad, false
		n.Target = 0
		if pad != nil {
			n.Target = pad.Handle
		}
		if n.Phase != 0 {
			n.Phase = 1
		}
	}
	if pad != nil {
		entry.anchor = Vec3{X: pad.X, Y: pad.Y, Z: pad.Z}
	}
	if n.Phase == 0 && pad != nil {
		return s.legPadLanding(u, n, satisfied, tick, entry)
	}
	if n.Phase == 0 {
		s.takeoffPreamble(u, n)
		n.Phase = 1
	}
	s.grantRepairPieces(pad)
	if !entry.reserved {
		// Wait on retail's phase-1 loiter leg about the base, from the bearing
		// phase 0 drew [04 R-AIR-01 §6]. A leg starts when waiting begins or
		// the base changes, and on retail's own wake set: the marker's movement
		// outcomes or target removal. The claim retry's deadline only asks for
		// a piece again and leaves the leg alone, so only arrival turns the
		// circuit. The radius has a Modern floor: an unarmed aircraft's range
		// would put its circuit on the pad itself. The landing remains the
		// head, keeping suspended battle orders inert.
		if !entry.holding || satisfied&0xE8 != 0 {
			s.padLoiterLeg(u, n, entry.anchor, max(firstWeaponRange(u), repairHoldRadius))
			entry.holding = true
		}
		n.Phase, n.DynamicGate = 1, 0xE8
		airDeadline(n, tick, repairQueueRetry)
		return 2
	}
	if entry.holding || n.Phase == 1 || satisfied&0x40 != 0 {
		entry.holding = false
		n.Phase = 2
	}
	if n.Phase >= 3 {
		n.Param1 = uint32(entry.piece)
	}
	return s.legPadLanding(u, n, satisfied, tick, entry)
}

// repairDeparturePoint chooses where a repaired aircraft leaves its piece for.
// It searches stations around the base and ranks visible armed contacts
// conservatively by distance beyond their longest weapon range. Hidden
// positions never participate. No claim of guaranteed safety is possible when
// the base itself is under attack.
func (s *System) repairDeparturePoint(e *repairLanding) Vec3 {
	centre := e.anchor
	if e.pad != nil {
		centre = Vec3{X: e.pad.X, Y: e.pad.Y, Z: e.pad.Z}
	}
	ordinal := 0
	for _, other := range s.repairLandings {
		if other == e {
			break
		}
		if other.pad == e.pad {
			ordinal++
		}
	}
	directions := [...]struct{ x, z int64 }{{-1, 0}, {0, -1}, {1, 0}, {0, 1}, {-1, -1}, {1, -1}, {1, 1}, {-1, 1}}
	var points [16]Vec3
	var clearance [16]int64
	var feasible [16]bool
	for i := range points {
		radius := int64(repairHoldRadius + repairHoldSpace*(ordinal/8+i/8))
		d := directions[(i+ordinal)%len(directions)]
		p := Vec3{X: centre.X + numeric.Fixed(d.x*radius<<16), Y: centre.Y, Z: centre.Z + numeric.Fixed(d.z*radius<<16)}
		if s.Terrain != nil {
			margin := numeric.Fixed(repairHoldSpace << 16)
			p.X = max(margin, min(p.X, numeric.Fixed(int64(s.Terrain.CellW*16)<<16)-margin))
			p.Z = max(margin, min(p.Z, numeric.Fixed(int64(s.Terrain.CellH*16)<<16)-margin))
		}
		points[i], clearance[i] = p, 1<<60
		feasible[i] = s.repairStationFree(e.unit, p)
	}
	if b := airBinding(e.unit); b != nil && b.DangerVisibleHook() != nil {
		for _, threat := range s.world.IterSliced() {
			if !b.DangerVisibleHook()(e.unit, threat) {
				continue
			}
			var weaponRange int32
			for slot := 0; slot < units.NumSlots; slot++ {
				if w := threat.SlotAt(slot); w != nil && w.Weapon != nil {
					weaponRange = max(weaponRange, w.Weapon.Range)
				}
			}
			if weaponRange == 0 {
				continue
			}
			for i, p := range points {
				clearance[i] = min(clearance[i], (repairPlanarDistance(p.X, p.Z, threat.X, threat.Z)>>16)-int64(weaponRange))
			}
		}
	}
	best := -1
	for i := range points {
		if !feasible[i] {
			continue
		}
		if best == -1 || clearance[i] > 0 && clearance[best] <= 0 ||
			(clearance[i] > 0) == (clearance[best] > 0) && clearance[i] > clearance[best] {
			best = i
		}
	}
	if best == -1 {
		return Vec3{X: e.unit.X, Y: e.unit.Y, Z: e.unit.Z}
	}
	return points[best]
}

// Check the destination footprint, not a ground path: aircraft use their
// ordinary flight integrator to reach it, potentially from across the map.
func (s *System) repairStationFree(u *units.Unit, p Vec3) bool {
	if s.Terrain == nil || s.Grid == nil {
		return true
	}
	c := handleRow(s.Collisions, u.Handle)
	if c == nil {
		return false
	}
	bx, bz := c.HalfBias()
	at := QuantizedAnchor(int32(p.X), int32(p.Z), bx, bz)
	if !commitRectInBounds(s.Terrain, at, c.FootPrintX, c.FootPrintZ) {
		return false
	}
	for z := int32(0); z < int32(c.FootPrintZ); z++ {
		for x := int32(0); x < int32(c.FootPrintX); x++ {
			if id, occupied := s.Grid.OccupantAtPlane(PlaneAir, Cell{X: at.X + x, Z: at.Z + z}); occupied && id != int(u.Handle) {
				return false
			}
		}
	}
	return true
}
