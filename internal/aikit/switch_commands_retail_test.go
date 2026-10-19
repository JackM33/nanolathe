//go:build retail

package aikit_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// factionContent mounts the retail install with the converted faction
// packages NANOLATHE_FACTION_ADDONS lists (cmd/factionpack directories, as
// the session's faction add-on acceptance reads them); the packages are local
// and never part of the reference install.
func factionContent(t *testing.T) (*content.Catalog, vfs.FSOps) {
	t.Helper()
	value := strings.TrimSpace(os.Getenv("NANOLATHE_FACTION_ADDONS"))
	if value == "" {
		t.Skip("NANOLATHE_FACTION_ADDONS is unset: the Talon scenario needs converted faction packages")
	}
	var archives []string
	for _, dir := range filepath.SplitList(value) {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.ufo"))
		archives = append(archives, matches...)
	}
	fs := vfs.New()
	t.Cleanup(func() { fs.Close() })
	if err := fs.MountGameRoots([]vfs.GameRoot{{Dir: testsupport.RetailRoot(t), Extra: archives}}); err != nil {
		t.Fatal(err)
	}
	cat, err := content.CompileWithOptions(fs, content.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return cat, fs
}

// sideOf is the ordinal of the side whose commander is key, or skips.
func sideOf(t *testing.T, cat *content.Catalog, key string) int {
	t.Helper()
	for i, s := range cat.Sides {
		if s != nil && strings.EqualFold(s.Commander, key) {
			return i
		}
	}
	t.Skipf("no side's commander is %s", key)
	return -1
}

// scriptBrain runs the step the test hands it on the next think.
type scriptBrain struct {
	next func(k *aikit.Kit, o *aikit.Obs)
	saw  func(o *aikit.Obs)
}

func (*scriptBrain) Name() string    { return "script" }
func (*scriptBrain) Init(*aikit.Kit) {}
func (b *scriptBrain) Think(k *aikit.Kit, o *aikit.Obs) {
	if b.saw != nil {
		b.saw(o)
	}
	if f := b.next; f != nil {
		b.next = nil
		f(k, o)
	}
}

// scriptPersona looks every tick and acts on the next, without an action
// limit.
var scriptPersona = aikit.Persona{Name: "script", ThinkEvery: 1, Reaction: 1, Attention: 1, Skill: 100}

// scriptBattle composes a two-player skirmish whose players are Modern AI
// players run by script brains, player 0 on side.
func scriptBattle(t *testing.T, cat *content.Catalog, fs vfs.FSOps, mode gameplay.Mode, mapName string, side int) (*session.Session, [2]*scriptBrain) {
	t.Helper()
	if _, ok := cat.Maps[content.CanonicalKey(mapName)]; !ok {
		t.Skipf("map %q is absent", mapName)
	}
	var brains [2]*scriptBrain
	capTestHost = func(m *ai.Manager) *aikit.Host {
		b := &scriptBrain{}
		brains[m.Player] = b
		return aikit.NewHost(m, b, scriptPersona)
	}
	t.Cleanup(func() { capTestHost = nil })
	cfg := session.SkirmishConfig{MapName: mapName, NumPlayers: 2, Location: 1}
	for i := 0; i < 2; i++ {
		cfg.Players[i] = session.SkirmishPlayer{Controller: session.SkirmishControllerComputer, AllyGroup: i, Color: i, Side: i, Metal: session.SkirmishDefaultMetal, Energy: session.SkirmishDefaultEnergy}
	}
	cfg.ApplyDefaults()
	cfg.Players[0].Side, cfg.Players[1].Side = side, 0
	if err := cfg.ApplyComputerAI([]session.ComputerAI{{Row: session.ComputerAIEveryRow, Controller: ai.ControllerModern}}); err != nil {
		t.Fatal(err)
	}
	battle, err := headless.ComposeFreshBattle(headless.FreshBattleRequest{
		Gameplay: mode, Kind: headless.ScenarioDirectOTA, Map: mapName, LocalOwner: -1,
		Difficulty: 2, Skirmish: cfg, SimulationSeed: 7, CRTSeed: 7,
		FS: fs, Catalog: cat, AutomatedPlayers: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := battle.Session
	t.Cleanup(func() { closeHosts(s) })
	for range 2 {
		s.Step(s.Clock.ScaledAnchor + 1)
	}
	if brains[0] == nil {
		t.Fatal("player 0 has no script brain")
	}
	return s, brains
}

// placeDone creates a finished unit for owner at (x, z).
func placeDone(t *testing.T, s *session.Session, key string, owner uint8, x, z int32) *units.Unit {
	t.Helper()
	def, ok := s.Catalog.Unit(key)
	if !ok || def == nil {
		t.Skipf("unit %q is absent", key)
	}
	fx, fz := numeric.Fixed(int64(x)<<16), numeric.Fixed(int64(z)<<16)
	h, err := s.Units.Create(def, owner, fx, s.World.HeightAt(fx, fz), fz)
	if err != nil {
		t.Fatalf("place %s: %v", key, err)
	}
	s.CompleteUnit(h)
	return s.Units.Unit(h)
}

// stepUntil steps until ready holds, at most limit ticks.
func stepUntil(t *testing.T, s *session.Session, limit int, what string, ready func() bool) {
	t.Helper()
	for range limit {
		if ready() {
			return
		}
		if s.State == session.StatePostBattle {
			t.Fatalf("the battle ended before %s", what)
		}
		s.Step(s.Clock.ScaledAnchor + 1)
	}
	t.Fatalf("%s did not happen within %d ticks", what, limit)
}

// advance steps n ticks, or until the battle ends.
func advance(s *session.Session, n int) {
	for i := 0; i < n && s.State != session.StatePostBattle; i++ {
		s.Step(s.Clock.ScaledAnchor + 1)
	}
}

// pieceShown reports whether a unit's script shows the named piece.
func pieceShown(t *testing.T, u *units.Unit, name string) bool {
	t.Helper()
	for i, n := range u.Script.Program().Pieces {
		if strings.EqualFold(n, name) {
			return u.Script.RenderPieceFlags()[i]&1 != 0
		}
	}
	t.Fatalf("%s has no piece %s", u.Def.UnitName, name)
	return false
}

func ux(u *units.Unit) int32 { return int32(u.X.Int()) }
func uz(u *units.Unit) int32 { return int32(u.Z.Int()) }

func dist2(u *units.Unit, x, z int32) int64 {
	dx, dz := int64(ux(u))-int64(x), int64(uz(u))-int64(z)
	return dx*dx + dz*dz
}

// The Kit's switch and transport commands reach a unit through the state
// and order codes the side panel and a human's clicks use: Cloak sets and
// clears the cloak request and a paid pass hides the unit, Activate switches
// an on/off unit, Load and Unload carry a unit with an ordinary transport,
// and the observation reports each state (docs/DESIGN_SESSIONS_AI_SAVE.md
// "Modern AI commands").
func TestKitSwitchAndTransportCommandsRetail(t *testing.T) {
	cat, fs := factionContent(t)
	talon := sideOf(t, cat, "TALCOM")
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Modern} {
		t.Run(string(mode), func(t *testing.T) {
			s, brains := scriptBattle(t, cat, fs, mode, "Comet Catcher", talon)
			br := brains[0]
			var com *units.Unit
			for _, u := range s.Units.IterSliced() {
				if u.Owner == 0 && u.Def != nil && strings.EqualFold(u.Def.UnitName, "TALCOM") {
					com = u
				}
			}
			if com == nil {
				t.Fatal("player 0 has no Talon commander")
			}
			radar := placeDone(t, s, "TALRAD", 0, ux(com)+200, uz(com))
			var obs struct {
				cloaked, hidden, active bool
				carrier                 pool.Handle
			}
			watch := func(h pool.Handle, f func(u *aikit.OwnUnit)) func(o *aikit.Obs) {
				return func(o *aikit.Obs) {
					if u := o.OwnByHandle(h); u != nil {
						f(u)
					}
				}
			}

			// Cloak: requested, then paid and hidden; then cleared.
			br.saw = watch(com.Handle, func(u *aikit.OwnUnit) { obs.cloaked, obs.hidden = u.Cloaked, u.Hidden })
			br.next = func(k *aikit.Kit, o *aikit.Obs) { k.Cloak([]pool.Handle{com.Handle, radar.Handle}, true) }
			stepUntil(t, s, 120, "a paid cloak", func() bool { return com.IsCloaked && com.Hidden && obs.cloaked && obs.hidden })
			if radar.IsCloaked {
				t.Fatal("a unit that cannot cloak was given the request")
			}
			br.next = func(k *aikit.Kit, o *aikit.Obs) { k.Cloak([]pool.Handle{com.Handle}, false) }
			stepUntil(t, s, 120, "the cloak cleared", func() bool { return !com.IsCloaked && !com.Hidden && !obs.cloaked && !obs.hidden })

			// Activate: off, then on again.
			br.saw = watch(radar.Handle, func(u *aikit.OwnUnit) { obs.active = u.Active })
			stepUntil(t, s, 10, "the radar seen on", func() bool { return radar.Activated && obs.active })
			br.next = func(k *aikit.Kit, o *aikit.Obs) { k.Activate([]pool.Handle{radar.Handle}, false) }
			stepUntil(t, s, 60, "the radar off", func() bool { return !radar.Activated && !obs.active })
			br.next = func(k *aikit.Kit, o *aikit.Obs) { k.Activate([]pool.Handle{radar.Handle}, true) }
			stepUntil(t, s, 60, "the radar on", func() bool { return radar.Activated && obs.active })

			// Load and unload with an ordinary air transport.
			atlas := placeDone(t, s, "ARMATLAS", 0, ux(com)-300, uz(com))
			cargo := placeDone(t, s, "ARMPW", 0, ux(com)-300, uz(com)+200)
			br.saw = watch(cargo.Handle, func(u *aikit.OwnUnit) { obs.carrier = u.Carrier })
			br.next = func(k *aikit.Kit, o *aikit.Obs) { k.Load(atlas.Handle, cargo.Handle, false) }
			stepUntil(t, s, 900, "the pickup", func() bool { return cargo.Attachment.Carrier == atlas.Handle && obs.carrier == atlas.Handle })
			dropX, dropZ := ux(com)-300, uz(com)-400
			br.next = func(k *aikit.Kit, o *aikit.Obs) { k.Unload(atlas.Handle, dropX, dropZ, false) }
			stepUntil(t, s, 1500, "the unload", func() bool { return cargo.Attachment.Carrier == 0 && obs.carrier == 0 })
			if d := dist2(cargo, dropX, dropZ); d > 200*200 {
				t.Fatalf("unloaded %d world units from the point asked", isqrt(d))
			}
		})
	}
}

func isqrt(v int64) int64 {
	r := int64(0)
	for (r+1)*(r+1) <= v {
		r++
	}
	return r
}

// A gate dialed and used through the Kit alone: an ordinary ground attack at
// the peer gate links the sending gate, a unit moved onto its pad is carried
// to the receiving gate and released there idle — the move queued behind the
// pad does not survive the hand-off — and a move given after the release
// carries it on (research/extensions/talon-engine.md "Gate network";
// docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI gates"). The observation shows
// the passenger attached to the gate while it is held. Under Strict 3.1 the
// gate's script never links, which the brain's failure rule meets.
func TestKitDialsAGateAndSendsAUnitRetail(t *testing.T) {
	cat, fs := factionContent(t)
	talon := sideOf(t, cat, "TALCOM")
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Community39, gameplay.Modern} {
		t.Run(string(mode), func(t *testing.T) {
			s, brains := scriptBattle(t, cat, fs, mode, "Comet Catcher", talon)
			br := brains[0]
			a := placeDone(t, s, "TALGATE", 0, 1000, 1000)
			b := placeDone(t, s, "TALGATE", 0, 2000, 1000)
			advance(s, 240)
			br.next = func(k *aikit.Kit, o *aikit.Obs) { k.AttackPos([]pool.Handle{a.Handle}, ux(b), uz(b), false) }
			if mode == gameplay.Strict31 {
				advance(s, 900)
				if pieceShown(t, a, "linked_surface") {
					t.Fatal("the gate linked under Strict 3.1; the brain's failure rule and its design note assume it does not")
				}
				return
			}
			stepUntil(t, s, 900, "the link", func() bool { return pieceShown(t, a, "linked_surface") })
			advance(s, 3)
			tank := placeDone(t, s, "TALLTANK", 0, 1000, 1120)
			const goalX, goalZ = 2300, 1300
			held := false
			var order aikit.OrderClass
			br.saw = func(o *aikit.Obs) {
				if u := o.OwnByHandle(tank.Handle); u != nil {
					held = held || u.Carrier == a.Handle
					order = u.Order
				}
			}
			br.next = func(k *aikit.Kit, o *aikit.Obs) {
				one := []pool.Handle{tank.Handle}
				k.Move(one, ux(a), uz(a), false)
				k.Move(one, goalX, goalZ, true)
			}
			jumped := false
			stepUntil(t, s, 1500, "the delivery", func() bool {
				jumped = jumped || ux(tank) > 1500
				return jumped && tank.Attachment.Carrier == 0
			})
			if !held {
				t.Fatal("the observation never showed the passenger held by the gate")
			}
			stepUntil(t, s, 300, "the released unit idle", func() bool { return order == aikit.OrderIdle })
			if d := dist2(tank, ux(b), uz(b)); d > 64*64 {
				t.Fatalf("released %d world units from the receiving pad: the queued move survived", isqrt(d))
			}
			br.next = func(k *aikit.Kit, o *aikit.Obs) { k.Move([]pool.Handle{tank.Handle}, goalX, goalZ, false) }
			stepUntil(t, s, 900, "a move after the release", func() bool { return dist2(tank, goalX, goalZ) < 120*120 })
		})
	}
}

// The util+tac brain, given a Talon gate pair standing beside its base and
// toward the enemy, dials the base gate to the front one and sends the
// ground units it produces through the pair; the hand-off carries them
// (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI gates").
func TestModernAIUsesATalonGatePairRetail(t *testing.T) {
	cat, fs := factionContent(t)
	talon := sideOf(t, cat, "TALCOM")
	const mapName = "Painted Desert"
	if _, ok := cat.Maps[content.CanonicalKey(mapName)]; !ok {
		t.Skipf("map %q is absent", mapName)
	}
	var brain *core.Brain
	capTestHost = func(m *ai.Manager) *aikit.Host {
		if m.Player == 0 {
			brain = utilTac()
			return aikit.NewHost(m, brain, aikit.PersonaHard)
		}
		return aikit.NewHost(m, &scriptBrain{}, scriptPersona)
	}
	t.Cleanup(func() { capTestHost = nil })
	cfg := session.SkirmishConfig{MapName: mapName, NumPlayers: 2, Location: 1}
	for i := 0; i < 2; i++ {
		cfg.Players[i] = session.SkirmishPlayer{Controller: session.SkirmishControllerComputer, AllyGroup: i, Color: i, Side: i, Metal: 10000, Energy: 50000}
	}
	cfg.ApplyDefaults()
	cfg.Players[0].Side, cfg.Players[1].Side = talon, 0
	if err := cfg.ApplyComputerAI([]session.ComputerAI{{Row: session.ComputerAIEveryRow, Controller: ai.ControllerModern}}); err != nil {
		t.Fatal(err)
	}
	battle, err := headless.ComposeFreshBattle(headless.FreshBattleRequest{
		Gameplay: gameplay.Modern, Kind: headless.ScenarioDirectOTA, Map: mapName, LocalOwner: -1,
		Difficulty: 2, Skirmish: cfg, SimulationSeed: 7, CRTSeed: 7,
		FS: fs, Catalog: cat, AutomatedPlayers: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := battle.Session
	t.Cleanup(func() { closeHosts(s) })
	var home, enemy *units.Unit
	for _, u := range s.Units.IterSliced() {
		if u.Def != nil && u.Def.Commander || u.Def != nil && strings.EqualFold(u.Def.UnitName, "TALCOM") {
			if u.Owner == 0 {
				home = u
			} else {
				enemy = u
			}
		}
	}
	if home == nil || enemy == nil {
		t.Fatal("a commander is missing")
	}
	hx, hz, ex, ez := ux(home), uz(home), ux(enemy), uz(enemy)
	base := placeDone(t, s, "TALGATE", 0, hx+(ex-hx)/16, hz+(ez-hz)/16+300)
	front := placeDone(t, s, "TALGATE", 0, hx+(ex-hx)*2/5, hz+(ez-hz)*2/5)
	report := func() map[string]int64 {
		h := s.AI[0].Ext.(*aikit.Host)
		h.Join()
		out := map[string]int64{}
		brain.Economy.(aikit.Reporter).Report(func(name string, v int64) { out[name] = v })
		return out
	}
	carried := false
	for tick := 0; tick < 12000 && !carried; tick += 300 {
		advance(s, 300)
		carried = report()["gate_carried"] > 0
	}
	got := report()
	if !pieceShown(t, base, "linked_surface") || got["gate_dials"] != 1 || got["gate_sent"] == 0 || !carried {
		t.Fatalf("gate pair at (%d,%d) and (%d,%d): linked %v, report %v; want it dialed once, units sent and carried",
			ux(base), uz(base), ux(front), uz(front), pieceShown(t, base, "linked_surface"), got)
	}
	t.Logf("by tick %d: %v", s.Clock.GlobalTick, got)
}
