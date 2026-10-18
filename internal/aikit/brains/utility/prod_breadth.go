package utility

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// Production breadth (the breadth parameter, README §13.16; Nanolathe
// Modern AI policy, docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI production
// variety"). Four parts, each switched by one bit:
//
//   - variety: a combat product's share is counted among its own factory's
//     combat products rather than the whole army (a second factory no longer
//     dilutes the first's mix), every combat product carries a per-game
//     preference drawn once from the player's private generator, and a
//     product whose efficiency with counters is far below the factory's best
//     is cut, so the preference cannot lift a poor unit into the mix;
//   - fast: an armed unit cheap and fast enough to take the scout role is a
//     combat candidate too, beyond the scout allowance, up to a share of its
//     factory's combat units (the army makes the first three of a game
//     scouts);
//   - support: mobile radar and radar jammers, a few once the army exists,
//     kept with the main squad by tactics;
//   - assist: mobile units with build power that cannot build the economy,
//     one per two live factories, at most four, while metal outruns what
//     build power spends; those without products are sent to help
//     factories and frames from here (prod_assist.go).
const (
	bVariety int32 = 1 << iota
	bFast
	bSupport
	bAssist
)

// bpart reports whether a breadth part is on.
func (s *shared) bpart(part int32) bool { return s.p.Breadth&part != 0 }

// Product classes, a bit set per definition (breadth.kind).
const (
	kCombat     uint8 = 1 << iota // an eligible combat product (combatScore's or combatScoreN's test)
	kFast                         // cheap fast combat: an armed unit with the scout role (aikit summarize)
	kRadar                        // support: a mobile radar whose radar reaches past its sight
	kJam                          // support: a mobile radar jammer
	kAssistFree                   // assist builder without products: production sends it to help
	kAssistBld                    // assist builder whose products the economy places: the economy commands it
)

const (
	// mixHalf is the share of its factory's combat units at which a
	// product's variety consideration halves (the whole army's share halved
	// it at 300‰). Units at 45% of the best's efficiency then take about a
	// tenth of a four-product factory's units rather than a twentieth.
	mixHalf = 200
	// prefSpread bounds each of the two uniform terms of a product's
	// per-game preference: 1000 ± up to 250 permille, triangular, so most
	// products sit near neutral and a pair can differ by up to 5:3.
	prefSpread = 125
	// effFloorLo and effFloorHi ramp a combat product in by its efficiency
	// with counters, permille of the factory's best: at or below a fifth it
	// is cut, from two fifths it is untouched. Below that the old mix too
	// built it only when the best unit's share was very large.
	effFloorLo, effFloorHi = 200, 400
	// fastLo and fastHi ramp cheap fast combat units out by their share of
	// the factory's combat units: untouched below 15%, none at 30%. They
	// raid well and fight poorly; the raid squad takes fast units up to a
	// fifth of the army's value.
	fastLo, fastHi = 150, 300
	// supportFrom is the army value from which the first mobile radar and
	// jammer are wanted (about ten first-tier units), supportPer the army
	// value per further one, supportMax the most of each kind.
	supportFrom, supportPer, supportMax = 1500, 3000, 2
	// assistBPMin is the least build power per metal-equivalent an assist
	// builder must give, permille of the best constructor's in the tree: a
	// unit bought for its build power that buys under a third of what a
	// constructor does is a poor way to buy it (Talon's repair drone).
	assistBPMin = 333
	// assistPer and assistMax size the assist builders: one per two live
	// factories, at most four. One costs about a heavy combat unit, so the
	// builders stay a small part of what the army is worth while each adds
	// about a constructor's build power or more to the factories.
	assistPer, assistMax = 2, 4
	// queuedTicks is how long a product queued at a factory counts as coming
	// (the constructor count's window, production.go).
	queuedTicks = 1800
)

// breadth is production breadth's static tables and per-think counts.
type breadth struct {
	init bool
	kind []uint8 // per definition: product classes
	pref []int32 // per definition: per-game preference, permille (nil: 1000 for all)
	// fac is, per factory definition, its distinct combat products, its
	// cheapest support product of each kind and its best assist builder.
	fac []facBreadth
	// Per think: own units of each class, built and framed.
	built, frames [6]int32
	// queuedPrev counts factories whose last product was queued before
	// this think, still in the window and with a queue; queuedNow those
	// queued this think.
	queuedPrev, queuedNow [6]int32
	factories             int32 // live factories (not written off)
	// Report counters (arena instrumentation): products queued by class,
	// cheap fast units queued as combat, assist orders sent, and the
	// per-game preference seed.
	made     [6]int32
	fastMade int32
	assists  int32
	seed     uint32
	seedSet  bool
	// mem is, per handle, the help last ordered for an assist builder
	// without products (prod_assist.go); anyFree is whether the table has
	// such a builder at all.
	mem     []assistMem
	anyFree bool
	guards  []int32 // per b.Factories entry: assist builders guarding it (this think)
}

// facBreadth is one factory definition's breadth table.
type facBreadth struct {
	combat []int32         // distinct combat products (table indices)
	radar  *aikit.UnitInfo // cheapest radar support product
	jam    *aikit.UnitInfo // cheapest jammer support product
	assist *aikit.UnitInfo // assist builder with the most build power per cost
}

// classIndex maps a single class bit to its slot in the per-class counts.
func classIndex(k uint8) int {
	for i := 0; i < 6; i++ {
		if k == 1<<i {
			return i
		}
	}
	return 0
}

// setupBreadth derives the tables once, after the shared model; with
// jitter on it draws the per-game preference seed, one draw, after every
// draw of the strategy's Init (core runs production's Init last).
func (pr *Production) setupBreadth(k *aikit.Kit, draw bool) {
	br := &pr.br
	if br.init {
		return
	}
	br.init = true
	s := pr.s
	t := k.Table
	n := len(t.Units)
	br.kind = make([]uint8, n)
	br.fac = make([]facBreadth, n)
	ext := s.p.Naval != 0 || s.p.Air != 0 || s.p.Tech != 0
	// The best constructor's build power per metal-equivalent in our tree
	// (×10⁶), the yardstick for assist builders.
	var bestCon int64
	for _, u := range t.Units {
		if s.info[u.Index].canEco && u.Role.Has(aikit.RoleMobile) && !u.Role.Has(aikit.RoleCommander) && k.Owns(u) {
			if r := bpPerCost(s, u); r > bestCon {
				bestCon = r
			}
		}
	}
	for i, u := range t.Units {
		si := &s.info[i]
		var c uint8
		if (ext && si.combatN && (s.p.Naval != 0 || si.combat)) || (!ext && si.combat) {
			c |= kCombat
			if u.Role.Has(aikit.RoleScout) {
				c |= kFast
			}
		}
		c |= supportKind(u)
		if a := assistKind(s, u); a != 0 && bestCon > 0 && bpPerCost(s, u)*1000 >= bestCon*assistBPMin {
			c |= a
		}
		br.kind[i] = c
		if c&kAssistFree != 0 {
			br.anyFree = true
		}
	}
	for i, f := range t.Units {
		if !f.Role.Has(aikit.RoleFactory) {
			continue
		}
		fb := &br.fac[i]
		for _, q := range f.Builds {
			qk := br.kind[q.Index]
			if qk&kCombat != 0 && !containsIdx(fb.combat, q.Index) {
				fb.combat = append(fb.combat, q.Index)
			}
			if qk&kRadar != 0 && (fb.radar == nil || s.info[q.Index].costMeq < s.info[fb.radar.Index].costMeq) {
				fb.radar = q
			}
			if qk&kJam != 0 && (fb.jam == nil || s.info[q.Index].costMeq < s.info[fb.jam.Index].costMeq) {
				fb.jam = q
			}
			if qk&(kAssistFree|kAssistBld) != 0 && (fb.assist == nil || bpPerCost(s, q) > bpPerCost(s, fb.assist)) {
				fb.assist = q
			}
		}
	}
	if draw && s.bpart(bVariety) && k.Rand != nil {
		br.seed, br.seedSet = k.Rand.Uint32(), true
		br.pref = make([]int32, n)
		for i := range br.pref {
			br.pref[i] = prefFrom(br.seed, int32(i))
		}
	}
}

func containsIdx(v []int32, x int32) bool {
	for _, y := range v {
		if y == x {
			return true
		}
	}
	return false
}

// bpPerCost is build power per metal-equivalent, ×10⁶.
func bpPerCost(s *shared, u *aikit.UnitInfo) int64 {
	return int64(u.BuildPower) * 1000000 / max64(s.info[u.Index].costMeq, 1)
}

// supportKind classifies a mobile radar or radar jammer the army can take
// along: a ground, hover or amphibious unit, unarmed, without build power,
// carrying nothing else a layer uses it for. Its radar must reach past its
// own sight (a token radar is not a radar unit); a sonar alone or a sonar
// jammer serves at sea, which this policy leaves out. A definition that
// costs no metal or has a single hit point is a scripted helper, not a unit
// to field. The tactics army keeps these with the main squad
// (tactics.SupportUnit, which TestSupportKindsAgree holds to the same
// answer).
func supportKind(u *aikit.UnitInfo) uint8 {
	r := u.Role
	if u.Def == nil || !r.Has(aikit.RoleMobile) || r.Any(aikit.RoleAir|aikit.RoleNaval|aikit.RoleCommander|aikit.RoleBuilder|aikit.RoleCombat|
		aikit.RoleTransport|aikit.RoleKamikaze|aikit.RoleStockpile|aikit.RoleInterceptor|aikit.RoleUpgrade) {
		return 0
	}
	if u.DPS+u.AirDPS > 0 || u.BuildPower > 0 || u.Metal <= 0 || u.HP <= 1 {
		return 0
	}
	var k uint8
	if u.Def.RadarDistance > u.Def.SightDistance {
		k |= kRadar
	}
	if u.Def.RadarDistanceJam > 0 {
		k |= kJam
	}
	return k
}

// placeable are the roles of buildings the economy places.
const placeable = aikit.RoleExtractor | aikit.RoleEnergy | aikit.RoleMetalMaker | aikit.RoleStorage | aikit.RoleDefense |
	aikit.RoleRadar | aikit.RoleStockpile | aikit.RoleInterceptor | aikit.RoleModule

// assistKind classifies a mobile unit with build power that cannot build an
// extractor or energy producer: kAssistFree when it has no products (an
// assist-and-repair unit), kAssistBld for a builder with a product the
// economy places and no factory among them. A unit whose purpose is another
// mechanic is left out: one that authors resurrection or capture, a builder
// whose products are all buildings the economy never places (a minelayer's
// mines), and one that makes a factory, which it deploys into (stream A's
// A6); so are armed units, ships, transports and scripted helpers.
func assistKind(s *shared, u *aikit.UnitInfo) uint8 {
	r := u.Role
	if u.Def == nil || !r.Has(aikit.RoleMobile) || u.BuildPower <= 0 || s.info[u.Index].canEco ||
		r.Any(aikit.RoleCommander|aikit.RoleNaval|aikit.RoleCombat|aikit.RoleTransport|aikit.RoleKamikaze|aikit.RoleStockpile|aikit.RoleInterceptor|aikit.RoleUpgrade) {
		return 0
	}
	if u.DPS+u.AirDPS > 0 || u.Metal <= 0 || u.HP <= 1 || u.Def.CanResurrect || u.Def.CanCapture {
		return 0
	}
	if len(u.Builds) == 0 {
		return kAssistFree
	}
	if !r.Has(aikit.RoleBuilder) {
		return 0
	}
	useful := false
	for _, p := range u.Builds {
		if p.Role.Has(aikit.RoleFactory) {
			return 0
		}
		if p.Role.Any(placeable) {
			useful = true
		}
	}
	if useful {
		return kAssistBld
	}
	return 0
}

// prefFrom is definition i's per-game preference: 1000 plus two uniform
// terms in ±prefSpread taken from the SplitMix64 output function of the
// game's seed and the index, so one draw from the player's generator fixes
// every definition's preference.
func prefFrom(seed uint32, i int32) int32 {
	z := uint64(seed)<<32 | uint64(uint32(i))
	z += 0x9e3779b97f4a7c15
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	z ^= z >> 31
	const w = 2*prefSpread + 1
	a := int32((z&0xffff)*w>>16) - prefSpread
	b := int32((z>>16&0xffff)*w>>16) - prefSpread
	return one + a + b
}

// prefOf is a product's preference this game (1000 without one).
func (br *breadth) prefOf(q *aikit.UnitInfo) int64 {
	if br.pref == nil {
		return one
	}
	return int64(br.pref[q.Index])
}

// observeBreadth counts our units of each class, the live factories, and
// the products queued before this think.
func (pr *Production) observeBreadth(b *core.Board) {
	br := &pr.br
	s := pr.s
	br.built, br.frames, br.queuedPrev, br.queuedNow = [6]int32{}, [6]int32{}, [6]int32{}, [6]int32{}
	o := b.O
	for i := range o.Own {
		u := &o.Own[i]
		k := br.kind[u.Info.Index] &^ (kCombat | kFast)
		for j := 2; j < 6; j++ {
			if k&(1<<j) == 0 {
				continue
			}
			if u.Built {
				br.built[j]++
			} else {
				br.frames[j]++
			}
		}
	}
	br.factories = 0
	for _, fi := range b.Factories {
		f := &o.Own[fi]
		if !s.fstateOf(f).dead {
			br.factories++
		}
		if int(f.H) >= len(pr.reg) || f.QueueLen < 1 {
			continue
		}
		r := &pr.reg[f.H]
		if r.def != f.Info || r.gen != f.Gen || r.prod == nil || s.tick-r.tick >= queuedTicks || r.tick == s.tick {
			continue
		}
		k := br.kind[r.prod.Index] &^ (kCombat | kFast)
		for j := 2; j < 6; j++ {
			if k&(1<<j) != 0 {
				br.queuedPrev[j]++
			}
		}
	}
}

// have is the number of a class's units we own or have coming: built, and
// the more of framed and queued earlier (a queued product becomes the
// frame), plus those queued this think.
func (br *breadth) have(k uint8) int64 {
	j := classIndex(k)
	return int64(br.built[j] + max(br.frames[j], br.queuedPrev[j]) + br.queuedNow[j])
}

// facCount sums our units (built, framed, queued this think) of a
// factory's combat products, and of its cheap fast ones.
func (pr *Production) facCount(f *aikit.UnitInfo) (all, fast int64) {
	br := &pr.br
	for _, i := range br.fac[f.Index].combat {
		n := int64(pr.s.count[i])
		all += n
		if br.kind[i]&kFast != 0 {
			fast += n
		}
	}
	return all, fast
}

// supportWant is how many support units of each kind the army wants.
func supportWant(army int64) int64 {
	if army < supportFrom {
		return 0
	}
	return min64(1+(army-supportFrom)/supportPer, supportMax)
}

// supportCand scores the factory's cheapest support product of kind k while
// the army wants more of that kind: the army share's weight, the energy
// gate and how far the factory's army reaches the enemy (an army that
// cannot get there has no use for it). The terrain model rates the reach of
// armed products only, so the factory's stands for its support unit's.
func (pr *Production) supportCand(b *core.Board, q *aikit.UnitInfo, k uint8, armyF, gate, freach int64) cand {
	s := pr.s
	c := cand{prod: q, spot: -1, kind: pSupport}
	want := supportWant(int64(b.ArmyValue))
	if q == nil || want == 0 || pr.br.have(k) >= want || b.K.Allowance(q) <= 0 {
		return c
	}
	c.score = mul(mul(mul(int64(s.p.WArmy)*10, armyF), gate), freach)
	c.f = [4]int64{armyF, want, pr.br.have(k), freach}
	return c
}

// assistWant is how many assist builders the live factories want.
func assistWant(factories int32) int64 {
	return min64((int64(factories)+assistPer-1)/assistPer, assistMax)
}

// assistCand scores the factory's best assist builder while we have fewer
// than assistWant: the weight of guarding a factory and the
// economy's need for it — metal outrunning what build power spends, and
// the army share — the constructors' energy gate, and nothing while the
// ambition cap holds constructors (w_cons zeroed for the think).
func (pr *Production) assistCand(b *core.Board, q *aikit.UnitInfo, consGate int64) cand {
	s := pr.s
	sp := &s.p
	c := cand{prod: q, spot: -1, kind: pAssist}
	if q == nil || sp.WCons == 0 || b.K.Allowance(q) <= 0 {
		return c
	}
	have := pr.br.have(kAssistFree) + pr.br.have(kAssistBld)
	if have >= assistWant(pr.br.factories) {
		return c
	}
	cov := lin(s.covM, 900, 2500) // the economy's guard gate (economy.go evalAssist)
	armyF := clamp(int64(100-b.Posture.EcoShare)*20, 200, 1500)
	need := mul(cov, armyF)
	// Twice the guard weight: an assist builder adds its build power to the
	// factories for its whole life, a guard assignment until it is
	// reassessed.
	c.score = mul(mul(int64(sp.WAssist)*20, need), consGate)
	c.f = [4]int64{cov, armyF, have, int64(pr.br.factories)}
	return c
}

// noteQueued records a product queued this think, as kind, in the class
// counts.
func (br *breadth) noteQueued(q *aikit.UnitInfo, kind commitKind) {
	k := br.kind[q.Index] &^ (kCombat | kFast)
	for j := 2; j < 6; j++ {
		if k&(1<<j) != 0 {
			br.queuedNow[j]++
			br.made[j]++
		}
	}
	if kind == pCombat && br.kind[q.Index]&kFast != 0 {
		br.fastMade++
	}
}

// hasOther reports whether a factory makes a combat product that is not a
// cheap fast unit.
func (br *breadth) hasOther(f *aikit.UnitInfo) bool {
	if !br.init {
		return false
	}
	for _, i := range br.fac[f.Index].combat {
		if br.kind[i]&kFast == 0 {
			return true
		}
	}
	return false
}

// Report publishes production breadth's counters (arena instrumentation;
// never read by play): products queued as cheap fast combat units, radar,
// jammers and assist builders, assist orders sent, and whether a per-game
// preference was drawn.
func (pr *Production) Report(add func(name string, value int64)) {
	br := &pr.br
	add("prod_fast", int64(br.fastMade))
	add("prod_radar", int64(br.made[classIndex(kRadar)]))
	add("prod_jam", int64(br.made[classIndex(kJam)]))
	add("prod_assist", int64(br.made[classIndex(kAssistFree)]+br.made[classIndex(kAssistBld)]))
	add("prod_assist_orders", int64(br.assists))
	if br.seedSet {
		add("prod_pref", 1)
	}
}
