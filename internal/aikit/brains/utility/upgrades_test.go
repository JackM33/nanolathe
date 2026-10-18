package utility

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// upgradeFixture is a constructor that makes an upgrade module, whose menu
// lists three upgrades (one twice, as a menu may), and a board of our units
// built from the given modules and upgrades.
type upgradeFixture struct {
	e                *Economy
	pr               *Production
	b                *core.Board
	con, mod         *aikit.UnitInfo
	cheap, mid, dear *aikit.UnitInfo
}

func newUpgradeFixture() *upgradeFixture {
	f := &upgradeFixture{}
	up := func(key string, value int32) *aikit.UnitInfo {
		return &aikit.UnitInfo{Key: key, Role: aikit.RoleUpgrade, Metal: value, Value: value, HP: 30}
	}
	f.cheap, f.mid, f.dear = up("drone", 184), up("shield", 301), up("gate", 651)
	f.mod = &aikit.UnitInfo{Key: "module", Role: aikit.RoleModule, Metal: 222, Energy: 4755, Value: 301, HP: 250, FootX: 3, FootZ: 3,
		BuildPower: 30, Builds: []*aikit.UnitInfo{f.dear, f.mid, f.cheap, f.mid}}
	f.con = &aikit.UnitInfo{Key: "con", Role: aikit.RoleBuilder | aikit.RoleMobile, Speed: 40, BuildPower: 200, Builds: []*aikit.UnitInfo{f.mod}}
	tab := &aikit.Table{Units: []*aikit.UnitInfo{f.con, f.mod, f.cheap, f.mid, f.dear}}
	for i, u := range tab.Units {
		u.Index = int32(i)
	}
	k := &aikit.Kit{Table: tab, Map: &aikit.MapInfo{WorldW: 10000, WorldH: 10000}, Persona: aikit.PersonaHard}
	s := &shared{p: DefaultParams()}
	s.setup(k)
	s.tick, s.safeX, s.safeZ = 30000, 500, 500
	s.supplyM, s.supplyE, s.covM, s.covE = 30000, 500000, 1500, 1500
	f.e, f.pr = &Economy{s: s}, &Production{s: s}
	f.b = &core.Board{K: k, Tick: s.tick, HomeX: 500, HomeZ: 500,
		Threat: aikit.NewGrid(&aikit.MapInfo{SectorW: 20, SectorH: 20}),
		Metal:  aikit.Res{Stock: 2000, Cap: 2000, Income: 30},
		Energy: aikit.Res{Stock: 20000, Cap: 20000, Income: 500}}
	return f
}

// observe sets the board's units, the constructor first, and counts them
// per definition as the shared model's observe does.
func (f *upgradeFixture) observe(units ...aikit.OwnUnit) {
	s := f.e.s
	s.tick++
	f.b.Tick = s.tick
	own := append([]aikit.OwnUnit{{H: 1, Gen: 1, Info: f.con, X: 500, Z: 500, Built: true}}, units...)
	for i := range own {
		own[i].H, own[i].Gen = pool.Handle(1+i), 1
	}
	f.b.O = &aikit.Obs{Tick: s.tick, Metal: f.b.Metal, Energy: f.b.Energy, Own: own}
	f.b.Builders = []int32{0}
	clear(s.count)
	for i := range own {
		s.count[own[i].Info.Index]++
	}
}

// One of each upgrade: an idle module is given the cheapest upgrade we
// neither have, nor are making, nor have queued at a module that can still
// make one; a module holding a completed upgrade is spent, and what its
// queue names counts for nothing. Two free modules in one think are given
// different upgrades, and none once every upgrade is had
// (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI upgrade modules").
func TestOneOfEachUpgrade(t *testing.T) {
	// free, busy (making the gate), spent (holding the drone, its queue
	// naming the shield) and the drone it holds.
	units := func(f *upgradeFixture) []aikit.OwnUnit {
		return []aikit.OwnUnit{
			{Info: f.mod, X: 1000, Z: 1000, Built: true},
			{Info: f.mod, X: 1200, Z: 1000, Built: true, QueueLen: 1, Target: f.dear},
			{Info: f.mod, X: 1400, Z: 1000, Built: true, QueueLen: 1, Target: f.mid},
			{Info: f.cheap, X: 1400, Z: 1008, Built: true},
		}
	}
	f := newUpgradeFixture()
	f.observe(units(f)...)
	st := f.e.s.upgrades(f.b)
	if len(st.free) != 1 || st.notFree[f.mod.Index] != 2 || !st.queued[f.dear.Index] || st.queued[f.mid.Index] {
		t.Fatalf("free %v not free %d queued %v: want one free module, the gate queued, the spent module's queue ignored",
			st.free, st.notFree[f.mod.Index], st.queued)
	}
	if n, q := st.missing(f.e.s, f.mod); n != 1 || q != f.mid {
		t.Fatalf("missing %d first %v, want only the shield", n, q)
	}
	f.pr.produceUpgrades(f.b, 4)
	if f.pr.next != 1 || f.pr.ring[0].top[0].prod != f.mid || f.pr.ring[0].top[0].kind != pUpgrade || !st.queued[f.mid.Index] {
		t.Fatalf("the free module was not given the shield: %+v", f.pr.ring[0])
	}

	// Two free modules, nothing had: the two cheapest, one each.
	f = newUpgradeFixture()
	f.observe(units(f)[0], aikit.OwnUnit{Info: f.mod, X: 1600, Z: 1000, Built: true})
	f.pr.produceUpgrades(f.b, 4)
	if f.pr.next != 2 || f.pr.ring[0].top[0].prod != f.cheap || f.pr.ring[1].top[0].prod != f.mid {
		t.Fatalf("two free modules: %s then %s, want the drone then the shield", f.pr.ring[0].top[0].prod.Key, f.pr.ring[1].top[0].prod.Key)
	}

	// Every upgrade had or being made: nothing to queue, no module wanted.
	f = newUpgradeFixture()
	making := aikit.OwnUnit{Info: f.dear, X: 1000, Z: 1004, Progress: 40} // a frame on the free module's pad
	f.observe(append(units(f), making, aikit.OwnUnit{Info: f.mid, X: 3000, Z: 3000, Built: true})...)
	st = f.e.s.upgrades(f.b)
	if len(st.free) != 0 {
		t.Fatalf("a module making an upgrade is not free: %v", st.free)
	}
	f.pr.produceUpgrades(f.b, 4)
	if f.pr.next != 0 {
		t.Fatal("an upgrade was queued although every one is had")
	}
	if c := f.e.evalModule(f.b, &f.b.O.Own[0], f.mod); c.score != 0 {
		t.Fatalf("a module was wanted with every upgrade had: %+v", c)
	}
}

// A module is wanted only for an upgrade no module is free, framed or
// ordered for, and not during an energy stall.
func TestModuleWantedForAMissingUpgrade(t *testing.T) {
	f := newUpgradeFixture()
	f.observe()
	con := &f.b.O.Own[0]
	c := f.e.evalModule(f.b, con, f.mod)
	if c.score < minScore || c.kind != cModule || c.f[0] != 3 {
		t.Fatalf("no module with three upgrades missing: %+v", c)
	}
	f.e.s.count[f.mod.Index] += 3 // three modules ordered this think
	if c := f.e.evalModule(f.b, con, f.mod); c.score != 0 {
		t.Fatalf("a fourth module for three upgrades: %+v", c)
	}
	f.e.s.count[f.mod.Index] -= 3
	f.observe(aikit.OwnUnit{Info: f.mod, X: 1000, Z: 1000, Progress: 50}) // a module frame
	if c := f.e.evalModule(f.b, &f.b.O.Own[0], f.mod); c.f[0] != 2 {
		t.Fatalf("a framed module did not count: %+v", c)
	}
	s := f.e.s
	s.eStock, s.eCap, s.eInc, s.eExp = 900, 20000, 300000, 400000 // nearly empty and falling
	if c := f.e.evalModule(f.b, &f.b.O.Own[0], f.mod); c.score != 0 {
		t.Fatalf("a module in an energy stall: %+v", c)
	}
	s.eInc = 500000 // refilling
	if c := f.e.evalModule(f.b, &f.b.O.Own[0], f.mod); c.score < minScore {
		t.Fatalf("no module once energy refills: %+v", c)
	}
}
