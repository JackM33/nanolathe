package utility

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// gateFixture is a constructor that makes gates, a ground tank, and a board
// with home at (1000, 1000), the enemy base at (9000, 1000) and the side
// field to +Z. Travelling tanks walk to (4500, 1000), the army's goal, so
// the base gate's site is (1250, 550) and the front gate's (4200, 1000)
// (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI gates").
type gateFixture struct {
	e               *Economy
	b               *core.Board
	con, gate, tank *aikit.UnitInfo
	units           []aikit.OwnUnit
}

func newGateFixture() *gateFixture {
	f := &gateFixture{}
	f.gate = &aikit.UnitInfo{Key: "gate", Role: aikit.RoleGate, Metal: 624, Energy: 3120, Value: 676, HP: 5000, FootX: 9, FootZ: 9, Def: &content.UnitDef{}}
	f.tank = &aikit.UnitInfo{Key: "tank", Role: aikit.RoleCombat | aikit.RoleMobile, Metal: 150, Value: 160, HP: 800, DPS: 40, Speed: 50, Def: &content.UnitDef{}}
	f.con = &aikit.UnitInfo{Key: "con", Role: aikit.RoleBuilder | aikit.RoleMobile, Speed: 40, BuildPower: 200, Builds: []*aikit.UnitInfo{f.gate}, Def: &content.UnitDef{}}
	tab := &aikit.Table{Units: []*aikit.UnitInfo{f.con, f.gate, f.tank}}
	for i, u := range tab.Units {
		u.Index = int32(i)
	}
	k := &aikit.Kit{Table: tab, Map: &aikit.MapInfo{WorldW: 10000, WorldH: 10000}, Persona: aikit.PersonaHard}
	s := &shared{p: DefaultParams()}
	s.setup(k)
	s.tick, s.minutes = 18000, 10
	s.safeX, s.safeZ, s.sideX, s.sideZ = 800, 1000, 1000, 1600
	s.supplyM, s.supplyE = 30000, 500000
	s.mInc, s.eInc, s.eExp, s.eStock, s.eCap = 30000, 300000, 200000, 5000, 10000
	f.e = &Economy{s: s}
	f.b = &core.Board{K: k, Tick: s.tick, HomeX: 1000, HomeZ: 1000, EnemyX: 9000, EnemyZ: 1000,
		Threat: aikit.NewGrid(&aikit.MapInfo{SectorW: 80, SectorH: 80})}
	return f
}

// observe sets the board's units after the constructor, numbering their
// handles from 2, and lists the combat units, as the board's update does.
func (f *gateFixture) observe(units ...aikit.OwnUnit) {
	s := f.e.s
	s.tick += 15
	f.b.Tick = s.tick
	own := append([]aikit.OwnUnit{{H: 1, Gen: 1, Info: f.con, X: 1000, Z: 1000, Built: true}}, units...)
	for i := range own {
		if own[i].H == 0 {
			own[i].H = pool.Handle(1 + i)
		}
		if own[i].Gen == 0 {
			own[i].Gen = 1
		}
	}
	f.units = own
	f.b.O = &aikit.Obs{Tick: s.tick, Own: own}
	f.b.Builders, f.b.Combat = []int32{0}, nil
	for i := range own {
		if own[i].Built && own[i].Info.Role.Has(aikit.RoleCombat) {
			f.b.Combat = append(f.b.Combat, int32(i))
		}
	}
}

// travellers are n tanks at home walking to the army's goal.
func (f *gateFixture) travellers(n int) []aikit.OwnUnit {
	var out []aikit.OwnUnit
	for i := 0; i < n; i++ {
		out = append(out, aikit.OwnUnit{H: pool.Handle(10 + i), Info: f.tank, X: 1100, Z: 1000 + int32(20*i), Built: true, Order: aikit.OrderMove, GoalX: 4500, GoalZ: 1000})
	}
	return out
}

func (f *gateFixture) eval() cand { return f.e.evalGate(f.b, &f.b.O.Own[0], f.gate) }

// Gates are wanted only once three ground units walk to one goal well out
// from home whose walk the pair would shorten, past minute five, while the
// income pays for both, energy is not stalling and a builder other than the
// commander can make one; the base gate first, beside home toward the goal
// and away from the side field, then the front gate short of the goal once
// the base gate stands or is framed; a builder already sent for one keeps
// another from being sent; no front gate in the enemy's reach, nor past
// halfway to the enemy base.
func TestGatesWantedOnceTheArmyTravels(t *testing.T) {
	f := newGateFixture()
	f.observe(f.travellers(2)...)
	f.e.serviceGates(f.b, 4)
	if c := f.eval(); c.score != 0 {
		t.Fatalf("a gate with two units travelling: %+v", c)
	}
	f.observe(f.travellers(3)...)
	f.e.serviceGates(f.b, 4)
	c := f.eval()
	if c.score < minScore || c.kind != cGate || c.x != 1250 || c.z != 550 {
		t.Fatalf("base gate %+v, want one at (1250, 550)", c)
	}
	s := f.e.s
	s.mInc = 2*624*1000/gateIncomeSecs - 1
	if c := f.eval(); c.score != 0 {
		t.Fatalf("a gate the metal income cannot pay for in %d s: %+v", gateIncomeSecs, c)
	}
	s.mInc = 30000
	s.eStock, s.eExp = 400, 400000 // nearly empty and falling
	if c := f.eval(); c.score != 0 {
		t.Fatalf("a gate in an energy stall: %+v", c)
	}
	s.eStock, s.eExp = 5000, 200000
	s.minutes = gateFrom - 1
	if c := f.eval(); c.score != 0 {
		t.Fatalf("a gate before minute %d: %+v", gateFrom, c)
	}
	s.minutes = 10
	f.con.Role |= aikit.RoleCommander
	if c := f.eval(); c.score != 0 {
		t.Fatalf("a gate when only the commander, leashed to home, can build one: %+v", c)
	}
	f.con.Role &^= aikit.RoleCommander

	// A builder sent for the base gate this think: no second one.
	cm := s.commitOf(&f.b.O.Own[0])
	*cm = commitment{def: f.con, gen: 1, kind: cGate, prod: f.gate, x: 1250, z: 550, tick: s.tick}
	if c := f.eval(); c.score != 0 {
		t.Fatalf("a second base gate while one is ordered: %+v", c)
	}
	*cm = commitment{}

	// The base gate framed: the front gate.
	base := aikit.OwnUnit{H: 5, Info: f.gate, X: 1250, Z: 550, Progress: 30}
	f.observe(append([]aikit.OwnUnit{base}, f.travellers(3)...)...)
	f.e.serviceGates(f.b, 4)
	if c := f.eval(); c.score < minScore || c.x != 4200 || c.z != 1000 {
		t.Fatalf("front gate %+v, want one at (4200, 1000)", c)
	}
	// The front site in the enemy's reach, or the goal past halfway: none
	// once the last sites planned have aged out.
	far := f.travellers(3)
	for i := range far {
		far[i].GoalX = 6000
	}
	for _, c := range []struct {
		name   string
		units  []aikit.OwnUnit
		threat bool
	}{{"in the enemy's reach", f.travellers(3), true}, {"past halfway", far, false}} {
		f2 := newGateFixture()
		if c.threat {
			f2.b.Threat.AddDisc(4200, 1000, 300, 50)
		}
		f2.observe(append([]aikit.OwnUnit{base}, c.units...)...)
		f2.e.serviceGates(f2.b, 4)
		if got := f2.eval(); got.score != 0 {
			t.Fatalf("a front gate %s: %+v", c.name, got)
		}
	}
	// Both stand: none.
	front := aikit.OwnUnit{H: 6, Info: f.gate, X: 4200, Z: 1000, Built: true}
	f.observe(append([]aikit.OwnUnit{base, front}, f.travellers(3)...)...)
	f.e.serviceGates(f.b, 4)
	if c := f.eval(); c.score != 0 {
		t.Fatalf("a third gate: %+v", c)
	}
}

// The base gate is dialed to the front gate once both stand, and again,
// aiming at the next offset, when the front gate is replaced; after the
// dialing animation travellers whose trip the gates shorten are sent to the
// base pad in one command, at most three at once, a unit going nowhere far is not, and a
// unit idle on the front pad is moved on to the goal it had. A unit seen at
// the front gate far from where it stood is counted as carried.
func TestGatesDialSendAndMoveOn(t *testing.T) {
	f := newGateFixture()
	base := aikit.OwnUnit{H: 5, Info: f.gate, X: 1250, Z: 550, Built: true}
	front := aikit.OwnUnit{H: 6, Info: f.gate, X: 4200, Z: 1000, Built: true}
	near := aikit.OwnUnit{H: 20, Info: f.tank, X: 1100, Z: 1200, Built: true, Order: aikit.OrderMove, GoalX: 1500, GoalZ: 1300}
	f.observe(append([]aikit.OwnUnit{base, front, near}, f.travellers(3)...)...)
	st := &f.e.ab.gates
	if n := f.e.serviceGates(f.b, 4); n != 1 || st.dials != 1 || !st.dialed() || st.sent != 0 {
		t.Fatalf("dial: %d actions, %d dials, dialed %v, sent %d; want the one dial and nothing sent yet", n, st.dials, st.dialed(), st.sent)
	}
	if n := f.e.serviceGates(f.b, 4); n != 0 {
		t.Fatalf("%d actions while the link is being made, want none", n)
	}
	f.e.s.tick += gateLinkTicks
	f.observe(append([]aikit.OwnUnit{base, front, near}, f.travellers(3)...)...)
	if n := f.e.serviceGates(f.b, 4); n != 1 || st.sent != 3 {
		t.Fatalf("send: %d actions, %d sent; want one command for the three travellers", n, st.sent)
	}
	// A fourth traveller waits while three are on their way.
	more := append([]aikit.OwnUnit{base, front, near}, f.travellers(4)...)
	f.observe(more...)
	if f.e.serviceGates(f.b, 4); st.sent != 3 {
		t.Fatalf("%d sent with three on their way, want no more than %d at once", st.sent, gateInFlight)
	}
	if r := &st.routes[10]; r.tick == 0 || r.gx != 4500 || r.gz != 1000 {
		t.Fatalf("route %+v: want the traveller's goal remembered", *r)
	}
	if int(near.H) < len(st.routes) && st.routes[near.H].tick != 0 {
		t.Fatal("a unit whose walk the gates do not shorten was sent")
	}
	// Sent ones are not sent again; one arrives at the front pad, idle.
	ts := f.travellers(3)
	ts[0].X, ts[0].Z, ts[0].Order, ts[0].GoalX, ts[0].GoalZ = 4210, 1010, aikit.OrderIdle, 0, 0
	f.observe(append([]aikit.OwnUnit{base, front, near}, ts...)...)
	if n := f.e.serviceGates(f.b, 4); n != 1 || st.sent != 3 || st.delivered != 1 || len(st.send) != 0 {
		t.Fatalf("move on: %d actions, %d sent, %d carried, %v resent; want one move-on command, nothing resent, one carried", n, st.sent, st.delivered, st.send)
	}
	// Another stands idle beside the base pad, which was taken: sent onto it
	// again once gateRetryTicks have passed since it was sent.
	ts[1].X, ts[1].Z, ts[1].Order, ts[1].GoalX, ts[1].GoalZ = 1300, 650, aikit.OrderIdle, 0, 0
	ts[0].X, ts[0].Z = 4400, 1000 // moved on
	f.e.s.tick += gateRetryTicks
	f.observe(append([]aikit.OwnUnit{base, front, near}, ts...)...)
	if f.e.serviceGates(f.b, 4); len(st.send) != 1 || st.send[0] != 11 || st.sent != 3 {
		t.Fatalf("retry %v (%d sent): want the unit beside the pad sent onto it again", st.send, st.sent)
	}
	if g := st.groups[0]; g.gx != 4500 || g.gz != 1000 || len(g.units) != 1 || g.units[0] != 10 {
		t.Fatalf("move-on group %+v: want the carried unit sent to its goal", g)
	}
	// The front gate replaced: dialed again at the next aim point.
	front2 := front
	front2.H = 7
	f.observe(append([]aikit.OwnUnit{base, front2}, ts...)...)
	if f.e.serviceGates(f.b, 4); st.dials != 2 || st.dial.to != 7 || st.dial.n != 2 {
		t.Fatalf("redial %+v (%d dials): want the new front gate dialed", st.dial, st.dials)
	}
}

// A unit sent through that stands free at the base pad for fifteen seconds
// before anything was ever carried marks the gates failed (they do not
// carry in this battle, as under a rule set whose gate script never links):
// they are then neither used nor built again.
func TestGatesFailWhenNothingIsCarried(t *testing.T) {
	f := newGateFixture()
	base := aikit.OwnUnit{H: 5, Info: f.gate, X: 1250, Z: 550, Built: true}
	front := aikit.OwnUnit{H: 6, Info: f.gate, X: 4200, Z: 1000, Built: true}
	f.observe(append([]aikit.OwnUnit{base, front}, f.travellers(3)...)...)
	st := &f.e.ab.gates
	f.e.serviceGates(f.b, 4)
	f.e.s.tick += gateLinkTicks
	f.observe(append([]aikit.OwnUnit{base, front}, f.travellers(3)...)...)
	f.e.serviceGates(f.b, 4)
	at := f.travellers(3)
	at[0].X, at[0].Z, at[0].Order = 1260, 560, aikit.OrderIdle
	for i := 0; i <= gateCaptureWait/15+1; i++ {
		f.observe(append([]aikit.OwnUnit{base, front}, at...)...)
		f.e.serviceGates(f.b, 4)
	}
	if !st.failed {
		t.Fatal("a unit stood at the pad uncarried and the gates were not marked failed")
	}
	f.observe(f.travellers(3)...) // the gates are gone
	f.e.serviceGates(f.b, 4)
	if c := f.eval(); c.score != 0 {
		t.Fatalf("a gate after the gates failed: %+v", c)
	}
}
