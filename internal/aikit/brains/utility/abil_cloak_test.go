package utility

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// cloakFixture is a commander that can cloak (200 standing, 1000 moving),
// a sniper authored to start cloaked, a mine (a kamikaze) and a tank that
// cannot cloak, on a board whose energy pays 400 a second of net income
// beyond what cloaks take, with 5000 in a 10000 store.
type cloakFixture struct {
	e                       *Economy
	b                       *core.Board
	com, sniper, mine, tank *aikit.UnitInfo
}

func newCloakFixture() *cloakFixture {
	f := &cloakFixture{}
	f.com = &aikit.UnitInfo{Key: "com", Role: aikit.RoleCommander | aikit.RoleBuilder | aikit.RoleMobile, Def: &content.UnitDef{CloakCost: 200, CloakCostMoving: 1000}}
	f.sniper = &aikit.UnitInfo{Key: "sniper", Role: aikit.RoleCombat | aikit.RoleMobile, Def: &content.UnitDef{CloakCost: 25, CloakCostMoving: 750, InitCloaked: true}}
	f.mine = &aikit.UnitInfo{Key: "mine", Role: aikit.RoleKamikaze, Def: &content.UnitDef{CloakCost: 5, CloakCostMoving: 5, InitCloaked: true}}
	f.tank = &aikit.UnitInfo{Key: "tank", Role: aikit.RoleCombat | aikit.RoleMobile, Def: &content.UnitDef{}}
	tab := &aikit.Table{Units: []*aikit.UnitInfo{f.com, f.sniper, f.mine, f.tank}}
	for i, u := range tab.Units {
		u.Index = int32(i)
	}
	k := &aikit.Kit{Table: tab, Map: &aikit.MapInfo{WorldW: 10000, WorldH: 10000}, Persona: aikit.PersonaHard}
	s := &shared{p: DefaultParams()}
	s.setup(k)
	s.tick = 9000
	f.e = &Economy{s: s}
	f.b = &core.Board{K: k, Tick: s.tick, HomeX: 1000, HomeZ: 1000, Threat: aikit.NewGrid(&aikit.MapInfo{SectorW: 80, SectorH: 80})}
	return f
}

// think observes the units with the given energy and runs the policy.
func (f *cloakFixture) think(energy aikit.Res, units ...aikit.OwnUnit) *cloakState {
	f.e.s.tick += 15
	f.b.Tick = f.e.s.tick
	for i := range units {
		units[i].H, units[i].Gen, units[i].Built = pool.Handle(1+i), 1, true
	}
	f.b.O = &aikit.Obs{Tick: f.b.Tick, Energy: energy, Own: units}
	f.e.serviceCloak(f.b, 4)
	return &f.e.ab.cloak
}

func has(list []pool.Handle, h pool.Handle) bool {
	for _, x := range list {
		if x == h {
			return true
		}
	}
	return false
}

var paying = aikit.Res{Stock: 5000, Cap: 10000, Income: 500, Expense: 100}

// A threatened unit that can cloak is cloaked while energy can pay for it,
// at its moving cost when it moves; it keeps cloak for a while after the
// threat and drops it once the hold is over or energy cannot pay; a unit
// authored to start cloaked keeps its cloak while energy pays; a kamikaze
// and a unit that cannot cloak are never touched (docs/DESIGN_SESSIONS_AI_SAVE.md
// "Modern AI commands").
func TestCloakWhileThreatenedAndPaid(t *testing.T) {
	f := newCloakFixture()
	units := func(comCloaked bool, comOrder aikit.OrderClass) []aikit.OwnUnit {
		return []aikit.OwnUnit{
			{Info: f.com, X: 1000, Z: 1000, Cloaked: comCloaked, Order: comOrder},
			{Info: f.sniper, X: 3000, Z: 3000, Cloaked: true},
			{Info: f.mine, X: 4000, Z: 4000, Cloaked: false},
			{Info: f.tank, X: 1000, Z: 1100},
		}
	}
	// Unthreatened: nothing asked; the sniper keeps its authored cloak.
	if st := f.think(paying, units(false, aikit.OrderIdle)...); len(st.on) != 0 || len(st.off) != 0 {
		t.Fatalf("unthreatened: on %v off %v, want no switch", st.on, st.off)
	}
	// Threatened at home: the commander cloaks (budget 400 + 3000/30 = 500
	// against 1.5 × 200).
	f.b.Threat.AddDisc(1000, 1000, 200, 30)
	if st := f.think(paying, units(false, aikit.OrderIdle)...); !has(st.on, 1) || len(st.on) != 1 || len(st.off) != 0 {
		t.Fatalf("threatened commander: on %v off %v, want it alone cloaked", st.on, st.off)
	}
	// Moving, at 1000 a second, it cannot be paid for: cleared (after the
	// switch's dwell).
	f.e.s.tick += cloakDwell
	if st := f.think(paying, units(true, aikit.OrderMove)...); !has(st.off, 1) {
		t.Fatalf("moving commander at 1000/s on a 500/s budget: off %v, want it cleared", st.off)
	}
	// Standing, threat gone: kept through the hold, cleared after it.
	f.b.Threat.Clear()
	f.e.s.tick += cloakDwell
	if st := f.think(paying, units(true, aikit.OrderIdle)...); len(st.off) != 0 {
		t.Fatalf("within the hold: off %v, want the cloak kept", st.off)
	}
	f.e.s.tick += cloakHold
	if st := f.think(paying, units(true, aikit.OrderIdle)...); !has(st.off, 1) || has(st.off, 2) {
		t.Fatalf("after the hold: off %v, want the commander cleared and the sniper kept", st.off)
	}
	// Energy that cannot pay clears even the authored cloak.
	broke := aikit.Res{Stock: 1000, Cap: 10000, Income: 50, Expense: 60}
	if st := f.think(broke, units(false, aikit.OrderIdle)...); !has(st.off, 2) || has(st.off, 3) || has(st.on, 3) {
		t.Fatalf("no energy: off %v on %v, want the sniper cleared and the mine untouched", st.off, st.on)
	}
}

// A unit switched lately keeps its state for cloakDwell ticks, so a request
// does not flicker with every swing of the budget.
func TestCloakSwitchDwell(t *testing.T) {
	f := newCloakFixture()
	f.b.Threat.AddDisc(1000, 1000, 200, 30)
	com := func(cloaked bool) aikit.OwnUnit {
		return aikit.OwnUnit{Info: f.com, X: 1000, Z: 1000, Cloaked: cloaked}
	}
	if st := f.think(paying, com(false)); !has(st.on, 1) {
		t.Fatal("not cloaked when threatened and paid")
	}
	broke := aikit.Res{Stock: 1000, Cap: 10000, Income: 50, Expense: 60}
	if st := f.think(broke, com(true)); len(st.off) != 0 {
		t.Fatalf("switched back %d ticks after a switch: off %v", 15, st.off)
	}
	f.e.s.tick += cloakDwell
	if st := f.think(broke, com(true)); !has(st.off, 1) {
		t.Fatal("not cleared once the dwell passed and energy cannot pay")
	}
}
