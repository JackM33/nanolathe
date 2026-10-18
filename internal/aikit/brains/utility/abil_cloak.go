package utility

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// Cloak (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI commands"). A unit whose
// definition can cloak — a cloak cost above zero gives it the capability the
// Cloak_On handler tests [04 R-ORD-01 §2] — has its cloak requested while it
// is threatened, and kept for a while after, or always when its definition is
// authored to start cloaked; the request is cleared once neither holds or
// the energy store can no longer pay. A request costs its unit's authored
// cloak cost, the moving one while it moves, from the energy store at every
// settlement pass, once a second, and hides the unit only on a pass that
// paid [05 "Cloak debit"]. Kamikazes (mines) keep the state they are given:
// placing and firing them is not this policy's.
//
// What may be spent is the net income the cloaks paid last pass did not
// already take, plus a thirtieth of the store above a fifth of its capacity
// (thirty seconds of runway): the store's reserve stays with the economy. The
// commander is served first, then the other units in observation order; a
// unit not yet cloaked needs half again its cost free, so a request does not
// come and go with every swing of income. These are planning choices, not
// game rules.
const (
	cloakRunway     = 30  // seconds of store above the reserve that may pay beyond income
	cloakReserveDiv = 5   // the store keeps 1/cloakReserveDiv of its capacity
	cloakHold       = 450 // ticks a unit stays cloaked after it was last threatened
	cloakDwell      = 90  // ticks between two switches of one unit
)

// cloakMemo is when one unit was last threatened and last switched; def
// and gen say which unit it belongs to.
type cloakMemo struct {
	def             *aikit.UnitInfo
	gen             uint32
	threat, toggled uint32
}

type cloakState struct {
	memo    []cloakMemo
	order   []int32 // O.Own indices of the cloak-capable units, the commander first
	on, off []pool.Handle
	// Counters for the arena (Economy.Report): units sent Cloak_On and
	// Cloak_Off, and the most units wanted cloaked at once.
	sentOn, sentOff, peak int32
}

// canCloak reports a definition this policy cloaks: one with the can-cloak
// capability that is not a kamikaze.
func canCloak(info *aikit.UnitInfo) bool {
	return info != nil && info.Def != nil && info.Def.CloakCost > 0 && !info.Role.Has(aikit.RoleKamikaze)
}

// cloakCost is what one settlement pass would debit for u: the moving cost
// for a mobile unit with an order that moves it, otherwise the stationary
// one [05 "Cloak debit"].
func cloakCost(u *aikit.OwnUnit) int64 {
	d := u.Info.Def
	if u.Info.Role.Has(aikit.RoleMobile) {
		switch u.Order {
		case aikit.OrderMove, aikit.OrderPatrol, aikit.OrderAttack, aikit.OrderGuard, aikit.OrderTransport:
			return int64(d.CloakCostMoving)
		}
	}
	return int64(d.CloakCost)
}

// threatened reports whether u stands in the enemy's reach, or is moving
// toward a point in it.
func threatened(b *core.Board, u *aikit.OwnUnit) bool {
	if b.Threat == nil {
		return false
	}
	if b.Threat.At(u.X, u.Z) > 0 {
		return true
	}
	if !u.Info.Role.Has(aikit.RoleMobile) || (u.GoalX == 0 && u.GoalZ == 0) {
		return false
	}
	switch u.Order {
	case aikit.OrderMove, aikit.OrderPatrol, aikit.OrderAttack:
		return b.Threat.At(u.GoalX, u.GoalZ) > 0
	}
	return false
}

func (st *cloakState) memoOf(s *shared, u *aikit.OwnUnit) *cloakMemo {
	st.memo = handleSlots(st.memo, u.H, s.hcap)
	m := &st.memo[u.H]
	if m.def != u.Info || m.gen != u.Gen {
		*m = cloakMemo{def: u.Info, gen: u.Gen}
	}
	return m
}

// serviceCloak requests and clears cloak, at most limit actions (one group
// command each way), and returns the actions used.
func (e *Economy) serviceCloak(b *core.Board, limit int) int {
	s := e.s
	st := &e.ab.cloak
	o := b.O
	st.order = st.order[:0]
	var paid int64
	for i := range o.Own {
		u := &o.Own[i]
		if !u.Built || u.Carrier != 0 || !canCloak(u.Info) {
			continue
		}
		if u.Hidden {
			paid += cloakCost(u)
		}
		if u.Info.Role.Has(aikit.RoleCommander) {
			st.order = append(st.order, 0)
			copy(st.order[1:], st.order[:len(st.order)-1])
			st.order[0] = int32(i)
			continue
		}
		st.order = append(st.order, int32(i))
	}
	if len(st.order) == 0 {
		return 0
	}
	reserve := int64(o.Energy.Cap) / cloakReserveDiv
	budget := max64(0, int64(o.Energy.Income)-int64(o.Energy.Expense)+paid) + max64(0, int64(o.Energy.Stock)-reserve)/cloakRunway
	var spent int64
	tick := b.Tick
	st.on, st.off = st.on[:0], st.off[:0]
	var wanted int32
	for _, i := range st.order {
		u := &o.Own[i]
		m := st.memoOf(s, u)
		if threatened(b, u) {
			m.threat = tick
		}
		cost := cloakCost(u)
		need := cost
		if !u.Cloaked {
			need = cost * 3 / 2
		}
		reason := u.Info.Def.InitCloaked || (m.threat != 0 && tick-m.threat <= cloakHold)
		want := reason && spent+need <= budget
		if want != u.Cloaked && m.toggled != 0 && tick-m.toggled < cloakDwell {
			want = u.Cloaked // switched lately: it stays as it is a little longer
		}
		if want {
			spent += cost
			wanted++
		}
		switch {
		case want && !u.Cloaked:
			st.on = append(st.on, u.H)
		case !want && u.Cloaked:
			st.off = append(st.off, u.H)
		}
	}
	st.peak = max(st.peak, wanted)
	used := 0
	for _, side := range [2]struct {
		units []pool.Handle
		on    bool
	}{{st.on, true}, {st.off, false}} {
		if len(side.units) == 0 || used >= limit {
			continue
		}
		b.K.Cloak(side.units, side.on)
		used++
		if side.on {
			st.sentOn += int32(len(side.units))
		} else {
			st.sentOff += int32(len(side.units))
		}
		for _, h := range side.units {
			st.memo[h].toggled = tick
		}
	}
	return used
}
