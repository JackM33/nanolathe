package orders

import (
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Nanolathe Modern policy: DESIGN_UNITS_ORDERS_COB "Modern firing positions".
// These bounded search and retry values are policy, not retail constants.
const firingPositionRetryTicks uint32 = 30
const firingPositionTravelTicks uint32 = 180

type firingPositionState struct {
	node          *Node
	owner, target *units.Unit
	active        bool
	// exhausted records that the search for this node and target found no
	// clear candidate. Until nextAttempt Modern combat may fire into a
	// destructible feature instead (DESIGN_WEAPONS_PROJECTILES §2.3.2
	// "No room").
	exhausted            bool
	nextAttempt, started uint32
}

// Strict never starts a maneuver. A switch back from Modern retires only the
// temporary goal that the previous policy owned; ordinary queues are untouched.
func (StrictRules) StepFiringPosition(u *units.Unit, tick uint32) bool {
	if q := QueueOfUnit(u); q != nil && q.firingPosition.node != nil {
		q.stopFiringPosition(tick)
		q.firingPosition = firingPositionState{}
	}
	return false
}

func normalizeFiringPositionOrder(n *Node) {
	n.Phase, n.Deadline = 1, -1
	n.Satisfied &^= 0x3E0 // movement-only outcomes [04 R-ORD-01 §0]
	// A detached save copy must deliver retained control events on its first
	// restored dispatch too, before the slot setter [04 R-ORD-01 §3][04 R-ORD-01 §7].
	n.DynamicGate = n.Satisfied & (pendTargetGone | pendDisengage)
	n.MoveState, n.PathStatus = 0, 0
}

// Explicit destruction is identity-safe even after a control head has displaced
// this attack. Release is not: it can unbind the successor's goal [04 R-ORD-01 §9].
func (q *Queue) stopFiringPosition(tick uint32) {
	f := &q.firingPosition
	if !f.active {
		return
	}
	if b := q.Binding(); b != nil && b.Movement != nil && b.Movement.DestroyHook() != nil {
		b.Movement.DestroyHook()(f.node)
	}
	if q.indexOfPrimary(f.node) >= 0 {
		// The pump delivers only the intersection with DynamicGate. Retain
		// attack-ending events before restarting, or phase 1 can bind a slot
		// and erase a pending disengage [04 R-ORD-01 §3][04 R-ORD-01 §7]. A displaced
		// record must not take its successor's unit-level control events.
		pending := f.node.Satisfied & (pendTargetGone | pendDisengage)
		if q.Head() == f.node && f.owner != nil {
			pending |= f.owner.Pending & (pendTargetGone | pendDisengage)
		}
		normalizeFiringPositionOrder(f.node)
		f.node.Satisfied |= pending
		f.node.DynamicGate = pending
	}
	f.active = false
	f.nextAttempt = tick + firingPositionRetryTicks
}

func firingPositionEligible(q *Queue, u *units.Unit, n *Node, b *QueueBinding) *units.Unit {
	if u == nil || n == nil || b == nil || u.Def == nil || !u.Alive || u.Dying || u.Move.Mode != 1 || u.Remaining != 0 || u.Stunned || u.Attachment.Carrier != 0 || u.Def.CanFly || !u.Def.CanMove || !hasLiveMover(u) || n.Phase == 0 || n.Owner != u.Handle || n.Target == 0 || (n.Satisfied|u.Pending)&(pendTargetGone|pendDisengage) != 0 || leashBroken(u, n) {
		return nil
	}
	automatic := n.automaticAttack || q.danger.response == n
	stationary := n.ID == Lookup("Attack_NoMove") && automatic
	if n.ID != Lookup("Attack_Chase") && !stationary {
		return nil
	}
	// Hold Position and Hold Fire restrain only automatic attacks. An explicit
	// attack already chases into range on Hold Position, so it may also step
	// aside for a clear shot.
	if automatic && (u.Flags>>stanceMoveShift&stanceFieldMask == 0 || u.Flags>>stanceFireShift&stanceFieldMask == 0) {
		return nil
	}
	if b.LookupHook() == nil || b.DangerVisibleHook() == nil || b.Weapons == nil || b.Weapons.FiringPositionBlockedHook() == nil || b.Weapons.FiringPositionClearHook() == nil || b.Movement == nil || b.Movement.InstallPointHook() == nil || b.Movement.DestroyHook() == nil || b.DangerRouteFeasibleHook() == nil || b.World == nil || b.World.TerrainHeightHook() == nil {
		return nil
	}
	if stationary && !firingPositionWithinLeash(q, u, n, u.X, u.Z) {
		return nil
	}
	target := b.LookupHook()(n.Target)
	if target == nil || !target.Alive || target.Dying || !scanHostile(b, u, target) || !b.DangerVisibleHook()(u, target) {
		return nil
	}
	return target
}

func (*ModernRules) StepFiringPosition(u *units.Unit, tick uint32) bool {
	q := QueueOfUnit(u)
	if q == nil {
		return false
	}
	n, b := q.Head(), q.Binding()
	target := firingPositionEligible(q, u, n, b)
	f := &q.firingPosition
	if f.node != nil && (f.node != n || f.owner != u || f.target != target) {
		q.stopFiringPosition(tick)
		q.firingPosition = firingPositionState{}
		// A changed identity is a cancellation, never a fresh observation.
		return false
	}
	if target == nil {
		return false
	}
	if f.active {
		// Reload/aim may prevent another launch observation while moving. The
		// installed leg finishes on movement outcome or its bounded deadline.
		if n.MoveState == MoveArrived || dangerMovementFailed(n) || n.Satisfied&0xA0 != 0 || tick-f.started >= firingPositionTravelTicks {
			q.stopFiringPosition(tick)
			return false
		}
		return true
	}
	if f.node != nil && int32(tick-f.nextAttempt) < 0 {
		return false
	}
	if !b.Weapons.FiringPositionBlockedHook()(u, target, tick) {
		return false
	}
	*f = firingPositionState{node: n, owner: u, target: target, nextAttempt: tick + firingPositionRetryTicks}
	// Stable expanding radial rings, cardinals before diagonals. Each candidate
	// uses the normal movement feasibility check and the combat owner's preview.
	directions := [8][2]int64{{1, 0}, {0, 1}, {-1, 0}, {0, -1}, {1, 1}, {-1, 1}, {-1, -1}, {1, -1}}
	for radius := int64(16); radius <= 64; radius += 16 {
		for _, direction := range directions {
			step := radius
			if direction[0] != 0 && direction[1] != 0 {
				step = radius * 181 / 256 // inward-rounded diagonal, at most radius away
			}
			x, z := u.X+numeric.FixedFromInt(step*direction[0]), u.Z+numeric.FixedFromInt(step*direction[1])
			if !firingPositionWithinLeash(q, u, n, x, z) {
				continue
			}
			y, ok := b.World.TerrainHeightHook()(x, z)
			if !ok || !b.DangerRouteFeasibleHook()(u, x, z) || !b.Weapons.FiringPositionClearHook()(u, target, tick, x, y, z) {
				continue
			}
			// Keep the record's original goal/target and parameters. The payload is
			// the movement owner's authoritative destination [04 R-ORD-01 §9].
			if !b.Movement.InstallPointHook()(PointGoalRequest{Owner: u.Handle, Node: n, X: x, Y: y, Z: z, Radius: 0}) {
				return false
			}
			n.Satisfied &^= 0x3E0
			n.MoveState, n.PathStatus = MoveEnRoute, 0
			if q.danger.response == n && u.Flags>>stanceMoveShift&stanceFieldMask == 1 {
				q.danger.withdrew = true // the existing danger return retains its post
			}
			f.active, f.started = true, tick
			return true
		}
	}
	f.exhausted = true
	return false
}

// NoFiringPosition projects the search's answer through the unit's opaque
// queue link: whether u has no clear place to step to for its shot at target.
// Modern combat then fires into a destructible feature instead of waiting
// (DESIGN_WEAPONS_PROJECTILES §2.3.2 "No room"). A unit that may not move for
// the shot has none: an explicit stationary attack, or its own fire under
// Hold Position. Otherwise only a search that found no candidate answers yes,
// until its retry is due, so the next search still looks for room. It is
// read-only.
func (q *Queue) NoFiringPosition(u, target *units.Unit, tick uint32) bool {
	if q == nil || u == nil || target == nil {
		return false
	}
	n := q.Head()
	automatic := n != nil && (n.automaticAttack || q.danger.response == n)
	if n != nil && !automatic && n.ID == Lookup("Attack_NoMove") && n.Owner == u.Handle && n.Target == target.Handle {
		return true
	}
	if u.Flags>>stanceMoveShift&stanceFieldMask == 0 && (n == nil || automatic) {
		return true
	}
	f := &q.firingPosition
	return f.exhausted && !f.active && f.node != nil && f.node == n && f.owner == u && f.target == target && int32(tick-f.nextAttempt) < 0
}

// A stationary danger response has no chase parameter leash. Its existing
// maneuver anchor owns the boundary and its ordinary return-to-post lifetime.
func firingPositionWithinLeash(q *Queue, u *units.Unit, n *Node, x, z numeric.Fixed) bool {
	if n.Param3 != 0 {
		leash := int64(n.Param3)
		return dangerDistance(x, z, numeric.FixedFromInt(int64(n.GuardX)), numeric.FixedFromInt(int64(n.GuardY))) < leash*leash
	}
	if n.ID != Lookup("Attack_NoMove") || u.Flags>>stanceMoveShift&stanceFieldMask != 1 {
		return true
	}
	if q.danger.response != n || !q.danger.anchored {
		return false
	}
	leash := int64(uint16(u.Def.ManeuverLeashLength))
	return leash > 0 && dangerDistance(x, z, q.danger.anchorX, q.danger.anchorZ) < leash*leash
}
