package utility

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// Upgrade modules (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI upgrade
// modules"). An upgrade module (aikit RoleModule) makes fixtures and keeps
// them (RoleUpgrade). Talon's commander upgrade module is the one the
// faction inventory holds: it keeps each upgrade it completes and then
// makes no other, and the commander's script reads every completed upgrade
// of its owner, at any distance, as its shield, drone, missile pack or
// uplink (research/extensions/talon-engine.md "Commander upgrades").
//
// The brain keeps one of each upgrade its build tree reaches. A missing
// upgrade is queued at an idle module, cheapest first; when no module is
// idle or coming for it, an idle builder that can make a module scores one
// among its other buildings. No definition authors what these upgrades
// do — their effects are all in scripts — so the order is a fixed planning
// choice, not a judgement of their worth.

// moduleWeight is an upgrade module's base score: a nominal weight of 100,
// scaled as the economy's weights are (w_factory and the rest, ×10).
const moduleWeight = 1000

// A built module of ours stands, this think, as one of these.
const (
	modFree  uint8 = iota // idle and holding nothing: may make one upgrade
	modBusy               // making an upgrade
	modSpent              // holding a completed upgrade
)

// modView is one built module and how it stands.
type modView struct {
	own   int32 // index in O.Own
	fix   int32 // index in O.Own of the upgrade on its pad, -1 for none
	state uint8
}

// upgradeState is the per-think picture of our upgrade modules. It is
// rebuilt from the observation each think, so a load rebuilds it.
type upgradeState struct {
	init    bool
	modules int32 // module definitions our build tree reaches; 0 skips everything
	tick    uint32
	fresh   bool    // the fields below describe the think at tick
	queued  []bool  // per definition: queued at a module that is not spent
	notFree []int32 // per module definition: built modules busy or spent
	free    []int32 // O.Own indices of free modules, observation order
	mods    []modView
	fix     []int32 // scratch: O.Own indices of our upgrades
}

// upgrades returns this think's picture, building it on first use. A side
// with no module in its tree (every stock side) costs one test.
func (s *shared) upgrades(b *core.Board) *upgradeState {
	u := &s.upg
	if !u.init {
		u.init = true
		t := s.k.Table
		for _, m := range t.Units {
			if m.Role.Has(aikit.RoleModule) && s.k.Owns(m) {
				u.modules++
			}
		}
		if u.modules > 0 {
			u.queued = make([]bool, len(t.Units))
			u.notFree = make([]int32, len(t.Units))
		}
	}
	if u.modules == 0 || (u.fresh && u.tick == s.tick) {
		return u
	}
	u.fresh, u.tick = true, s.tick
	clear(u.queued)
	clear(u.notFree)
	u.free, u.mods, u.fix = u.free[:0], u.mods[:0], u.fix[:0]
	o := b.O
	for i := range o.Own {
		if o.Own[i].Info.Role.Has(aikit.RoleUpgrade) {
			u.fix = append(u.fix, int32(i))
		}
	}
	for i := range o.Own {
		m := &o.Own[i]
		if !m.Built || !m.Info.Role.Has(aikit.RoleModule) {
			continue
		}
		// What stands on its pad is what it makes and keeps: the factory
		// starts its product at its build piece.
		st, on := modFree, int32(-1)
		for _, fi := range u.fix {
			f := &o.Own[fi]
			if !onPad(m, f.X, f.Z) {
				continue
			}
			on = fi
			if f.Built {
				st = modSpent
				break
			}
			st = modBusy
		}
		if st != modSpent && m.QueueLen > 0 {
			st = modBusy
			if m.Target != nil {
				u.queued[m.Target.Index] = true
			}
		}
		if st == modFree {
			u.free = append(u.free, int32(i))
		} else {
			u.notFree[m.Info.Index]++
		}
		u.mods = append(u.mods, modView{own: int32(i), fix: on, state: st})
	}
	return u
}

// wanted reports an upgrade our tree reaches, the restriction caps allow,
// and that we neither have (built or being made) nor have queued.
func (u *upgradeState) wanted(s *shared, q *aikit.UnitInfo) bool {
	return q.Role.Has(aikit.RoleUpgrade) && s.k.Owns(q) && s.count[q.Index] == 0 && !u.queued[q.Index] && s.k.Allowance(q) > 0
}

// missing counts the distinct upgrades module definition mod could make
// that are wanted, and returns the first of them in the fixed order:
// least metal-equivalent value, then table order.
func (u *upgradeState) missing(s *shared, mod *aikit.UnitInfo) (int32, *aikit.UnitInfo) {
	var n int32
	var first *aikit.UnitInfo
	for i, q := range mod.Builds {
		if !u.wanted(s, q) || listedBefore(mod.Builds, i) {
			continue
		}
		n++
		if first == nil || q.Value < first.Value || (q.Value == first.Value && q.Index < first.Index) {
			first = q
		}
	}
	return n, first
}

// listedBefore reports whether list[i] appears earlier in list (a menu may
// author a product twice).
func listedBefore(list []*aikit.UnitInfo, i int) bool {
	for _, q := range list[:i] {
		if q == list[i] {
			return true
		}
	}
	return false
}

// evalModule scores building module p while one of its upgrades is wanted
// beyond the modules that are free, framed or ordered: the nominal weight
// times the affordability, travel and site-threat considerations every
// building takes, and nothing during an energy stall (the store nearly
// empty and falling, which also pauses combat production).
func (e *Economy) evalModule(b *core.Board, u *aikit.OwnUnit, p *aikit.UnitInfo) cand {
	s := e.s
	c := cand{kind: cModule, prod: p, spot: -1, spacing: 2}
	st := s.upgrades(b)
	if st.modules == 0 || !s.k.Owns(p) {
		return c
	}
	need, _ := st.missing(s, p)
	avail := s.count[p.Index] - st.notFree[p.Index]
	if need <= avail || (s.eStock*20 < s.eCap && s.eInc < s.eExp) {
		return c
	}
	travel, thr, ok := e.place(b, u, &c)
	if !ok {
		return c
	}
	aff := e.afford(p)
	c.score = mul(mul(mul(moduleWeight, aff), travel), thr)
	c.f = [4]int64{int64(need - avail), aff, travel, thr}
	return c
}

// produceUpgrades queues the first missing upgrade at each free module,
// one action each, within budget.
func (pr *Production) produceUpgrades(b *core.Board, budget int) {
	s := pr.s
	st := s.upgrades(b)
	for _, mi := range st.free {
		if budget <= 0 {
			return
		}
		m := &b.O.Own[mi]
		n, q := st.missing(s, m.Info)
		if q == nil {
			continue
		}
		b.K.Produce(m.H, q, 1)
		budget--
		st.queued[q.Index] = true
		d := &pr.ring[pr.next]
		pr.next = (pr.next + 1) % len(pr.ring)
		d.reset(s.tick, m.Info, m.X, m.Z)
		d.top[0] = cand{kind: pUpgrade, prod: q, spot: -1, x: m.X, z: m.Z, score: one, f: [4]int64{int64(n), int64(q.Value), 0, 0}}
		d.n, d.chosen, d.note = 1, 0, noteAssign
	}
}

// explainUpgrades lists our modules and how each stands.
func (pr *Production) explainUpgrades(b *core.Board, x *aikit.Explain) {
	st := &pr.s.upg
	if st.modules == 0 || !st.fresh || st.tick != pr.s.tick {
		return
	}
	names := [...]string{"free", "making", "spent"}
	for _, v := range st.mods {
		m := &b.O.Own[v.own]
		what := names[v.state]
		switch {
		case v.fix >= 0:
			f := &b.O.Own[v.fix]
			what += fmt.Sprintf(" %s %d%%", f.Info.Key, f.Progress)
		case m.Target != nil:
			what += " " + m.Target.Key
		}
		x.Notes = append(x.Notes, fmt.Sprintf("upgrade module %s at (%d,%d): %s, queue %d", m.Info.Key, m.X, m.Z, what, m.QueueLen))
	}
}
