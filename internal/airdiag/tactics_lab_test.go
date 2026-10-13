package airdiag

// The aircraft tactics laboratory: authored, displayless scenarios that put
// stock aircraft against stock targets and anti-air with real flight, COB,
// weapons and damage, and report what happened. It runs only when
// NANOLATHE_AIRLAB_OUT names an output directory, so the ordinary test tiers
// never pay for it. The same file compiles on a baseline checkout through
// `go test -overlay`, which is how a before/after comparison is produced.
//
// Every number this file computes is an observation of committed session
// state. Floating point here is diagnostic arithmetic only and never feeds a
// simulation decision (INVARIANTS I2, diagnostic observers).

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// labHarness composes a two-player skirmish whose second seat is a passive
// human, so no planner sends units into a scenario.
func labHarness(t *testing.T, root, mapName string, seed uint32) *Harness {
	t.Helper()
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Skipf("mount: %v", err)
	}
	cat, err := content.Compile(fs)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	cfg := session.SkirmishConfig{MapName: mapName, NumPlayers: 2, RNGSimSeed: seed, RNGCrtSeed: seed*7 + 3}
	cfg.ApplyDefaults()
	cfg.Players[0].Side, cfg.Players[0].Controller = 0, session.SkirmishControllerHuman
	cfg.Players[1].Side, cfg.Players[1].Controller = 1, session.SkirmishControllerComputer
	cfg.Players[0].AllyGroup, cfg.Players[1].AllyGroup = 5, 6
	// Known-geometry by default: mapping and line of sight off, so every
	// anti-air unit is visible from the start. NANOLATHE_AIRLAB_LOS=1 keeps
	// ordinary line of sight, where anti-air is discovered in flight.
	if os.Getenv("NANOLATHE_AIRLAB_LOS") != "1" {
		cfg.Mapping, cfg.LineOfSight = 0, 0
	}
	sess, err := session.NewSkirmishWithProgress(fs, cat, cfg, nil)
	if err != nil {
		_ = fs.Close()
		t.Skipf("compose %q: %v", mapName, err)
	}
	// The computer seat exists because the constructor requires one; its
	// planner is passive so nothing but the scenario's own units acts.
	for _, m := range sess.AI {
		if m != nil {
			m.Passive = true
		}
	}
	h := &Harness{FS: fs, Catalog: cat, Session: sess}
	t.Cleanup(h.Close)
	h.Step(2)
	return h
}

type labVec struct{ X, Z float64 }

func labFixed(v float64) numeric.Fixed { return numeric.Fixed(int64(v * 65536)) }

func labPos(u *units.Unit) labVec { return labVec{fx(u.X), fx(u.Z)} }

func (a labVec) dist(b labVec) float64 { return math.Hypot(a.X-b.X, a.Z-b.Z) }

// labScene is one running scenario: who flies, who is shot at, who shoots
// back, and what the observer has recorded so far.
type labScene struct {
	trace    *os.File // optional per-run trace, shots included
	h        *Harness
	name     string
	air      []*units.Unit
	targets  []*units.Unit
	aa       []*units.Unit
	builders []*units.Unit
	immortal bool // targets are healed every tick so accuracy can be sampled for the whole run

	bombs     map[string]*labBomb
	bombOrder []string
	strings   []labString

	firstRelease int
	killTick     int
	tick         int

	airStartHP   []int32
	airMinHP     []int32
	airLostTick  []int
	healed       []int64 // health regained per aircraft
	prevHP       []int32
	targetDamage int64
	aaKillTicks  []int
	otherKills   []int
	aaLOSTicks   int // aircraft-ticks spent inside a live AA unit's weapon range
	grounded     []int
	landedNear   []int
	firstAAKill  int
	strCreated   []uint32

	// Every non-dropped shot an aircraft fires: how far off its nose the
	// target sat at launch, in degrees, and whether that was outside the
	// weapon's authored drift gate [06 R-WPN-03 §2].
	shots         map[string]bool
	launchErr     []float64
	launchOverTol int
	minBombGap    int
	lastTgt       []*units.Unit // each aircraft's latest attack target
	lastTgtPos    []labVec      // and where that target last stood

	// centre and turn rotate every authored position about the map centre,
	// so each seed also flies a different approach geometry.
	centre labVec
	turn   float64
}

func newLabScene(h *Harness, name string, centre labVec) *labScene {
	return &labScene{h: h, name: name, bombs: map[string]*labBomb{}, shots: map[string]bool{}, centre: centre}
}

// begin sizes the observer's per-unit records once the scenario has placed
// its aircraft, targets and anti-air.
func (s *labScene) begin() {
	for _, u := range s.air {
		s.airStartHP = append(s.airStartHP, u.Health)
		s.airMinHP = append(s.airMinHP, u.Health)
		s.prevHP = append(s.prevHP, u.Health)
	}
	s.airLostTick = make([]int, len(s.air))
	s.lastTgt = make([]*units.Unit, len(s.air))
	s.lastTgtPos = make([]labVec, len(s.air))
	s.healed = make([]int64, len(s.air))
	s.grounded = make([]int, len(s.air))
	s.landedNear = make([]int, len(s.air))
	s.aaKillTicks = make([]int, len(s.aa))
	s.otherKills = make([]int, len(s.targets))
}

func (s *labScene) place(at labVec) labVec {
	dx, dz := at.X-s.centre.X, at.Z-s.centre.Z
	c, sn := math.Cos(s.turn), math.Sin(s.turn)
	return labVec{s.centre.X + dx*c - dz*sn, s.centre.Z + dx*sn + dz*c}
}

type labBomb struct {
	release  labVec
	relY     float64
	dir      labVec
	along    float64
	cross    float64
	shooter  pool.Handle
	created  uint32
	last     labVec
	lastVel  labVec
	lastY    float64
	impact   labVec
	target   labVec
	done     bool
	miss     float64
	shooterI int
	// tgt is the unit the bomber's order was attacking at release; wasted
	// marks a bomb that landed after that unit was already destroyed.
	tgt    *units.Unit
	wasted bool
}

type labString struct {
	shooter    int
	wasted     bool      // its first bomb landed after its target was gone
	bombs      []float64 // per-bomb miss distance in release order
	middleMiss float64
	nearest    float64
}

func (s *labScene) slotWeapon(u *units.Unit, i int) *content.WeaponDef {
	if sl := u.SlotAt(i); sl != nil {
		return sl.Weapon
	}
	return nil
}

func (s *labScene) airIndex(h pool.Handle) int {
	for i, u := range s.air {
		if u.Handle == h {
			return i
		}
	}
	return -1
}

// observeShot records a gun or missile shot by aircraft ai the first time it
// is seen: how far off the aircraft's nose its target sat. A heading h faces
// (−sin h, −cos h) in world X and Z, the dropped motion's own convention.
func (s *labScene) observeShot(ai int, shooter pool.Handle, created uint32, sx, sz numeric.Fixed, target pool.Handle, w *content.WeaponDef) {
	key := fmt.Sprintf("%d/%d/%d/%d", shooter, created, sx, sz)
	if s.shots[key] || target == 0 {
		return
	}
	s.shots[key] = true
	u, t := s.air[ai], s.h.Session.Units.Unit(target)
	if t == nil || !u.Alive {
		return
	}
	a := float64(u.Move.Heading) * 2 * math.Pi / 65536
	nx, nz := -math.Sin(a), -math.Cos(a)
	dx, dz := fx(t.X)-fx(u.X), fx(t.Z)-fx(u.Z)
	d := math.Hypot(dx, dz)
	if d < 1 {
		return
	}
	cos := (nx*dx + nz*dz) / d
	deg := math.Acos(math.Max(-1, math.Min(1, cos))) * 180 / math.Pi
	s.launchErr = append(s.launchErr, deg)
	if s.trace != nil {
		fmt.Fprintf(s.trace, "t=%d shot air%d %s tgt=%s d=%.0f off=%.1f\n", s.h.Session.Clock.GlobalTick, ai, w.Name, t.Def.Name, d, deg)
	}
	tol := float64(w.Tolerance)
	if tol == 0 {
		tol = 2000
	}
	if deg > tol*360/65536+0.5 {
		s.launchOverTol++
	}
}

// labPilotTrace, when set by a scratch overlay, adds a build's own pilot
// state to the trace line.
var labPilotTrace func(h *Harness, u *units.Unit) string

type labProj struct {
	x, z float64
	who  string
}

// labTracer writes a run's optional trace beyond the aircraft rows: the
// anti-air units' health and who hit them, and every anti-air missile's launch
// geometry and whether it hit the aircraft it chased.
type labTracer struct {
	f           *os.File
	s           *labScene
	aaHP, airHP []int32
	prev        []labProj
	launched    map[[2]uint32]bool
	live, last  map[[2]uint32]pool.Handle
}

func newLabTracer(f *os.File, s *labScene) *labTracer {
	tr := &labTracer{f: f, s: s, aaHP: make([]int32, len(s.aa)), airHP: make([]int32, len(s.air)),
		launched: map[[2]uint32]bool{}, live: map[[2]uint32]pool.Handle{}, last: map[[2]uint32]pool.Handle{}}
	for k, a := range s.aa {
		tr.aaHP[k] = a.Health
		fmt.Fprintf(f, "aa%d %s pos=(%.0f,%.0f) hp=%d\n", k, a.Def.Name, fx(a.X), fx(a.Z), a.Health)
	}
	for k, a := range s.targets {
		fmt.Fprintf(f, "tgt%d %s pos=(%.0f,%.0f) hp=%d\n", k, a.Def.Name, fx(a.X), fx(a.Z), a.Health)
	}
	return tr
}

func (tr *labTracer) step(i int) {
	s, h, f := tr.s, tr.s.h, tr.f
	tick := h.Session.Clock.GlobalTick
	if i%5 == 0 {
		fmt.Fprintf(f, "t=%d aa", tick)
		for _, a := range s.aa {
			fmt.Fprintf(f, " %d", a.Health)
		}
		fmt.Fprintln(f)
	}
	for k, a := range s.aa {
		if a.Health < tr.aaHP[k] {
			fmt.Fprintf(f, "t=%d aa%d hit %d by", tick, k, tr.aaHP[k]-a.Health)
			for _, pr := range tr.prev {
				if math.Hypot(pr.x-fx(a.X), pr.z-fx(a.Z)) < 80 {
					fmt.Fprintf(f, " %s", pr.who)
				}
			}
			fmt.Fprintln(f)
		}
		tr.aaHP[k] = a.Health
	}
	tr.prev = tr.prev[:0]
	if c := h.Session.Combat; c != nil {
		for idx := 0; idx < c.Count() && idx < len(c.Records); idx++ {
			if !c.Alive(pool.Handle(idx + 1)) {
				continue
			}
			p := c.Records[idx]
			who := "?"
			su := h.Session.Units.Unit(p.Shooter)
			if su != nil && su.Def != nil {
				who = su.Def.Name
			}
			tr.prev = append(tr.prev, labProj{fx(p.Pos.X), fx(p.Pos.Z), who})
			if s.airIndex(p.Shooter) >= 0 {
				continue
			}
			key := [2]uint32{uint32(p.Shooter), p.CreationTick}
			if !tr.launched[key] {
				tr.launched[key] = true
				desc := ""
				if ai := s.airIndex(p.TargetUnit); ai >= 0 && su != nil {
					a := s.air[ai]
					desc = fmt.Sprintf(" air%d from=(%.0f,%.0f) at=(%.0f,%.0f) hd=%.0f spd=%.2f", ai, fx(su.X), fx(su.Z), fx(a.X), fx(a.Z), float64(a.Move.Heading)*360/65536, fx(a.Move.Speed))
				}
				fmt.Fprintf(f, "t=%d aashot %s k=%d/%d%s\n", tick, who, key[0], key[1], desc)
			}
			tr.live[key] = p.TargetUnit
		}
	}
	// A missile gone since the last tick hit if the aircraft it chased lost
	// health on this one.
	for key, th := range tr.last {
		if _, ok := tr.live[key]; ok {
			continue
		}
		if ai := s.airIndex(th); ai >= 0 {
			fmt.Fprintf(f, "t=%d aaend k=%d/%d air%d hit=%v\n", tick, key[0], key[1], ai, s.air[ai].Health < tr.airHP[ai])
		}
	}
	for k, a := range s.air {
		tr.airHP[k] = a.Health
	}
	tr.last, tr.live = tr.live, tr.last
	clear(tr.live)
}

// observe records one committed tick.
func (s *labScene) observe() {
	s.tick++
	c := s.h.Session.Combat
	seen := map[string]bool{}
	if c != nil {
		for idx := 0; idx < c.Count() && idx < len(c.Records); idx++ {
			p := c.Records[idx]
			if !c.Alive(pool.Handle(idx + 1)) {
				continue
			}
			ai := s.airIndex(p.Shooter)
			if ai < 0 {
				continue
			}
			w, _ := s.h.Session.Catalog.WeaponByID(p.WeaponID)
			if w == nil {
				continue
			}
			if !w.Dropped {
				s.observeShot(ai, p.Shooter, p.CreationTick, p.StartPos.X, p.StartPos.Z, p.TargetUnit, w)
				continue
			}
			key := fmt.Sprintf("%d/%d/%d/%d", p.Shooter, p.CreationTick, p.StartPos.X, p.StartPos.Z)
			seen[key] = true
			b := s.bombs[key]
			if b == nil {
				b = &labBomb{shooter: p.Shooter, created: p.CreationTick, shooterI: ai, release: labVec{fx(p.Pos.X), fx(p.Pos.Z)}, relY: fx(p.Pos.Y)}
				if v := math.Hypot(fx(p.Velocity.X), fx(p.Velocity.Z)); v > 0 {
					b.dir = labVec{fx(p.Velocity.X) / v, fx(p.Velocity.Z) / v}
				}
				// The attack the string was released on; a string still falling
				// after its attack ended belongs to that attack's target.
				b.tgt, b.target = s.lastTgt[ai], s.lastTgtPos[ai]
				b.wasted = b.tgt != nil && (!b.tgt.Alive || b.tgt.Dying)
				s.bombs[key] = b
				s.bombOrder = append(s.bombOrder, key)
				if s.firstRelease == 0 {
					s.firstRelease = s.tick
				}
			}
			b.last = labVec{fx(p.Pos.X), fx(p.Pos.Z)}
			b.lastY = fx(p.Pos.Y)
			b.lastVel = labVec{fx(p.Velocity.X), fx(p.Velocity.Z)}
			switch {
			case b.tgt != nil:
				if b.tgt.Alive && !b.tgt.Dying {
					b.target = labPos(b.tgt)
				}
			case len(s.targets) > 0:
				b.target = labPos(s.targets[0])
			}
		}
	}
	for _, key := range s.bombOrder {
		b := s.bombs[key]
		if b.done || seen[key] {
			continue
		}
		// One more step of the bomb's own horizontal motion is the impact
		// tick's position to within the tick's wind term.
		b.impact = labVec{b.last.X + b.lastVel.X, b.last.Z + b.lastVel.Z}
		b.miss = b.impact.dist(b.target)
		b.wasted = b.wasted || b.tgt != nil && (!b.tgt.Alive || b.tgt.Dying)
		dx, dz := b.impact.X-b.target.X, b.impact.Z-b.target.Z
		b.along = dx*b.dir.X + dz*b.dir.Z
		b.cross = dx*b.dir.Z - dz*b.dir.X
		b.done = true
	}
	for i, u := range s.air {
		if q := orders.QueueForUnit(u); u.Alive && q != nil && q.Head() != nil && q.Head().Target != 0 {
			if name := orders.DescriptorFor(q.Head().ID).Name; name == "AirStrike" || name == "AirToGround" || name == "AirToGroundHover" {
				s.lastTgt[i] = s.h.Session.Units.Unit(q.Head().Target)
			}
		}
		if t := s.lastTgt[i]; t != nil && t.Alive && !t.Dying {
			s.lastTgtPos[i] = labPos(t)
		}
		if s.airLostTick[i] == 0 && (!u.Alive || u.Dying) {
			s.airLostTick[i] = s.tick
		}
		if !u.Alive {
			continue
		}
		if u.Health < s.airMinHP[i] {
			s.airMinHP[i] = u.Health
		}
		if u.Health > s.prevHP[i] {
			s.healed[i] += int64(u.Health - s.prevHP[i])
		}
		s.prevHP[i] = u.Health
		if u.Move.Mode&3 != 2 {
			s.grounded[i]++
			for _, b := range s.builders {
				if b.Alive && labPos(b).dist(labPos(u)) < 300 {
					s.landedNear[i]++
					break
				}
			}
		}
		for _, a := range s.aa {
			if !a.Alive || a.Dying {
				continue
			}
			r := 0.0
			for k := 0; k < 3; k++ {
				if w := s.slotWeapon(a, k); w != nil && w.Range > 16 && float64(w.Range) > r {
					r = float64(w.Range)
				}
			}
			if labPos(a).dist(labPos(u)) <= r {
				s.aaLOSTicks++
				break
			}
		}
	}
	for i, a := range s.aa {
		if s.aaKillTicks[i] == 0 && (!a.Alive || a.Dying) {
			s.aaKillTicks[i] = s.tick
			if s.firstAAKill == 0 {
				s.firstAAKill = s.tick
			}
		}
	}
	for i, t := range s.targets {
		if s.immortal {
			if t.Alive && t.Health < t.Def.MaxDamage {
				s.targetDamage += int64(t.Def.MaxDamage - t.Health)
				t.Health = t.Def.MaxDamage
			}
			continue
		}
		if s.otherKills[i] == 0 && (!t.Alive || t.Dying) {
			s.otherKills[i] = s.tick
		}
	}
	if !s.immortal && s.killTick == 0 && len(s.targets) > 0 {
		all := true
		for _, t := range s.targets {
			if t.Alive && !t.Dying {
				all = false
			}
		}
		if all {
			s.killTick = s.tick
		}
	}
}

func (s *labScene) finish() {
	// A string is one bomber's bombs released no more than 20 ticks apart;
	// strings of different bombers interleave in release order.
	open := map[int]int{}
	for _, key := range s.bombOrder {
		b := s.bombs[key]
		if !b.done {
			continue
		}
		n, ok := open[b.shooterI]
		if !ok || b.created-s.lastCreated(n) > 20 {
			s.strings = append(s.strings, labString{shooter: b.shooterI, wasted: b.wasted})
			s.strCreated = append(s.strCreated, b.created)
			n = len(s.strings) - 1
			open[b.shooterI] = n
		} else if gap := int(b.created - s.lastCreated(n)); s.minBombGap == 0 || gap < s.minBombGap {
			s.minBombGap = gap
		}
		s.strings[n].bombs = append(s.strings[n].bombs, b.miss)
		s.strCreated[n] = b.created
	}
	for i := range s.strings {
		st := &s.strings[i]
		st.middleMiss = st.bombs[len(st.bombs)/2]
		st.nearest = st.bombs[0]
		for _, m := range st.bombs {
			st.nearest = math.Min(st.nearest, m)
		}
	}
}

func (s *labScene) lastCreated(i int) uint32 { return s.strCreated[i] }

// LabResult is one scenario run's summary.
type LabResult struct {
	Scenario, Rules  string
	Seed             uint32
	Ticks            int
	KillTick         int   `json:",omitempty"`
	FirstRelease     int   `json:",omitempty"`
	TargetDamage     int64 `json:",omitempty"`
	TargetKillTicks  []int `json:",omitempty"`
	AircraftLost     int
	AircraftLostAt   []int `json:",omitempty"`
	AircraftDamage   int64
	AircraftHealed   int64 `json:",omitempty"`
	AAKilled         int   `json:",omitempty"`
	FirstAAKill      int   `json:",omitempty"`
	AAExposureTicks  int
	Bombs            int     `json:",omitempty"`
	Strings          int     `json:",omitempty"`
	BombMissMean     float64 `json:",omitempty"`
	MiddleMissMean   float64 `json:",omitempty"`
	MiddleMissMedian float64 `json:",omitempty"`
	MiddleWithin48   float64 `json:",omitempty"`
	MiddleOver150    float64 `json:",omitempty"`
	StringHitRate    float64 `json:",omitempty"` // strings with any bomb within 48 world units
	GroundedTicks    int     `json:",omitempty"`
	LandedNearTicks  int     `json:",omitempty"`
	FinalAirHealth   []int32 `json:",omitempty"`
	MiddleMisses     []float64
	// BombsPerString, MinBombGap (ticks between a bomber's consecutive bombs
	// within a string) and WastedBombs (landed after their target was gone).
	BombsPerString float64 `json:",omitempty"`
	MinBombGap     int     `json:",omitempty"`
	WastedBombs    int     `json:",omitempty"`
	WastedStrings  int     `json:",omitempty"`
	// AAKilledFirst counts anti-air destroyed before the first other target;
	// AllAAKill is the tick the last anti-air unit died.
	AAKilledFirst int `json:",omitempty"`
	AllAAKill     int `json:",omitempty"`
	// Shots, LaunchErrMean/Max (degrees off the nose at launch) and
	// LaunchOverTol (shots outside the weapon's drift gate).
	Shots         int     `json:",omitempty"`
	LaunchErrMean float64 `json:",omitempty"`
	LaunchErrP90  float64 `json:",omitempty"`
	LaunchErrMax  float64 `json:",omitempty"`
	LaunchOverTol int     `json:",omitempty"`
}

func (s *labScene) result(rules string, seed uint32) LabResult {
	s.finish()
	r := LabResult{Scenario: s.name, Rules: rules, Seed: seed, Ticks: s.tick, KillTick: s.killTick, FirstRelease: s.firstRelease, TargetDamage: s.targetDamage, AAExposureTicks: s.aaLOSTicks, FirstAAKill: s.firstAAKill}
	if !s.immortal {
		for i, t := range s.targets {
			lost := t.Def.MaxDamage - t.Health
			if !t.Alive || t.Dying {
				lost = t.Def.MaxDamage
			}
			if lost > 0 {
				r.TargetDamage += int64(lost)
			}
			r.TargetKillTicks = append(r.TargetKillTicks, s.otherKills[i])
		}
	}
	for i, u := range s.air {
		if s.airLostTick[i] != 0 {
			r.AircraftLost++
			r.AircraftLostAt = append(r.AircraftLostAt, s.airLostTick[i])
			r.AircraftDamage += int64(s.airStartHP[i])
		} else {
			r.AircraftDamage += int64(s.airStartHP[i] - s.airMinHP[i])
		}
		r.AircraftHealed += s.healed[i]
		r.GroundedTicks += s.grounded[i]
		r.LandedNearTicks += s.landedNear[i]
		h := u.Health
		if !u.Alive {
			h = 0
		}
		r.FinalAirHealth = append(r.FinalAirHealth, h)
	}
	for i := range s.aa {
		if s.aaKillTicks[i] != 0 {
			r.AAKilled++
		}
	}
	var sum float64
	for _, key := range s.bombOrder {
		if b := s.bombs[key]; b.done {
			r.Bombs++
			sum += b.miss
			if b.wasted {
				r.WastedBombs++
			}
		}
	}
	r.MinBombGap = s.minBombGap
	firstOther := 0
	for _, k := range s.otherKills {
		if k != 0 && (firstOther == 0 || k < firstOther) {
			firstOther = k
		}
	}
	all := len(s.aa) > 0
	for _, k := range s.aaKillTicks {
		if k != 0 && (firstOther == 0 || k < firstOther) {
			r.AAKilledFirst++
		}
		if k == 0 {
			all = false
		} else if k > r.AllAAKill {
			r.AllAAKill = k
		}
	}
	if !all {
		r.AllAAKill = 0
	}
	r.Shots = len(s.launchErr)
	r.LaunchOverTol = s.launchOverTol
	if r.Shots > 0 {
		errs := append([]float64(nil), s.launchErr...)
		sortFloats(errs)
		var t float64
		for _, e := range errs {
			t += e
		}
		r.LaunchErrMean, r.LaunchErrP90, r.LaunchErrMax = t/float64(r.Shots), errs[len(errs)*9/10], errs[len(errs)-1]
	}
	if r.Bombs > 0 {
		r.BombMissMean = sum / float64(r.Bombs)
	}
	r.Strings = len(s.strings)
	if r.Strings > 0 {
		r.BombsPerString = float64(r.Bombs) / float64(r.Strings)
		for _, st := range s.strings {
			if st.wasted {
				r.WastedStrings++
			}
		}
		var mid, hit, within, over float64
		ms := make([]float64, 0, r.Strings)
		for _, st := range s.strings {
			mid += st.middleMiss
			ms = append(ms, st.middleMiss)
			if st.nearest <= 48 {
				hit++
			}
			if st.middleMiss <= 48 {
				within++
			}
			if st.middleMiss > 150 {
				over++
			}
		}
		n := float64(r.Strings)
		r.MiddleMissMean, r.StringHitRate, r.MiddleWithin48, r.MiddleOver150 = mid/n, hit/n, within/n, over/n
		sortFloats(ms)
		r.MiddleMissMedian = ms[len(ms)/2]
		r.MiddleMisses = ms
	}
	return r
}

func sortFloats(a []float64) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

// --- scenario authoring ---

type labScenarioDef struct {
	name  string
	ticks int
	build func(t *testing.T, s *labScene, c labVec)
}

func (s *labScene) spawn(t *testing.T, owner uint8, key string, at labVec) *units.Unit {
	t.Helper()
	at = s.place(at)
	u, err := s.h.Spawn(owner, key, labFixed(at.X), labFixed(at.Z))
	if err != nil {
		t.Skipf("spawn %s: %v", key, err)
	}
	return u
}

// holdPosition sets the Hold Position movement stance, so an accuracy target
// stays where it was placed instead of withdrawing from the bombs under the
// Modern danger response. It is the ordinary stance field the HUD writes.
func (s *labScene) holdPosition(u *units.Unit) *units.Unit {
	u.Flags &^= 3 << 18
	return u
}

// fireAtWill sets the Fire at Will stance. Stock bombers are authored to
// hold fire, so on patrol they attack only when a player sets this.
func (s *labScene) fireAtWill(u *units.Unit) *units.Unit {
	u.Flags = u.Flags&^(3<<20) | 2<<20
	return u
}

func (s *labScene) order(t *testing.T, u *units.Unit, code input.Latch, target *units.Unit, at labVec) {
	t.Helper()
	at = s.place(at)
	var h pool.Handle
	x, z := labFixed(at.X), labFixed(at.Z)
	y := numeric.Fixed(0)
	if target != nil {
		h, x, y, z = target.Handle, target.X, target.Y, target.Z
	}
	if err := s.h.Order(u, int(code), h, x, y, z); err != nil {
		t.Fatalf("order: %v", err)
	}
}

// labScenarios are authored probe choices, not gameplay constants. c is an
// open point near the map's centre; everything is placed relative to it.
var labScenarios = []labScenarioDef{
	{
		// Accuracy: one bomber makes pass after pass on a small stationary
		// target that is healed every tick, so every string is sampled.
		name: "bomb-accuracy-thunder", ticks: 3600,
		build: func(t *testing.T, s *labScene, c labVec) {
			s.immortal = true
			tgt := s.holdPosition(s.spawn(t, 1, "CORCK", c))
			s.targets = append(s.targets, tgt)
			b := s.spawn(t, 0, "ARMTHUND", labVec{c.X - 1400, c.Z + 300})
			s.air = append(s.air, b)
			s.h.Step(30)
			s.order(t, b, input.LatchAttack, tgt, labVec{})
		},
	},
	{
		name: "bomb-accuracy-shadow", ticks: 3600,
		build: func(t *testing.T, s *labScene, c labVec) {
			s.immortal = true
			tgt := s.holdPosition(s.spawn(t, 1, "ARMCK", c))
			s.targets = append(s.targets, tgt)
			b := s.spawn(t, 0, "CORSHAD", labVec{c.X + 1300, c.Z - 600})
			s.air = append(s.air, b)
			s.h.Step(30)
			s.order(t, b, input.LatchAttack, tgt, labVec{})
		},
	},
	{
		// Accuracy against a target driving back and forth.
		name: "bomb-moving", ticks: 3600,
		build: func(t *testing.T, s *labScene, c labVec) {
			s.immortal = true
			tgt := s.spawn(t, 1, "CORCV", labVec{c.X - 250, c.Z})
			s.targets = append(s.targets, tgt)
			b := s.spawn(t, 0, "ARMTHUND", labVec{c.X - 300, c.Z + 1500})
			s.air = append(s.air, b)
			s.h.Step(30)
			s.order(t, tgt, input.LatchPatrol, nil, labVec{c.X + 250, c.Z})
			s.order(t, b, input.LatchAttack, tgt, labVec{})
		},
	},
	{
		// Nimbleness: the bomber sits 320 world units from its target.
		name: "bomb-close", ticks: 1800,
		build: func(t *testing.T, s *labScene, c labVec) {
			tgt := s.spawn(t, 1, "CORSOLAR", c)
			s.targets = append(s.targets, tgt)
			for i := 0; i < 2; i++ {
				b := s.spawn(t, 0, "ARMTHUND", labVec{c.X - 320, c.Z + float64(i)*64 - 32})
				s.air = append(s.air, b)
			}
			s.h.Step(30)
			for _, b := range s.air {
				s.order(t, b, input.LatchAttack, tgt, labVec{})
			}
		},
	},
	{
		// A defended target: two Pulverizers stand between the bombers'
		// start and the target, a third flank is open.
		name: "bomb-defended", ticks: 2700,
		build: func(t *testing.T, s *labScene, c labVec) {
			tgt := s.spawn(t, 1, "CORVP", c)
			s.targets = append(s.targets, tgt)
			s.aa = append(s.aa, s.spawn(t, 1, "CORRL", labVec{c.X - 420, c.Z - 120}))
			s.aa = append(s.aa, s.spawn(t, 1, "CORRL", labVec{c.X - 420, c.Z + 160}))
			for i := 0; i < 4; i++ {
				b := s.spawn(t, 0, "ARMTHUND", labVec{c.X - 1800, c.Z + float64(i)*80 - 120})
				s.air = append(s.air, b)
			}
			s.h.Step(30)
			for _, b := range s.air {
				s.order(t, b, input.LatchAttack, tgt, labVec{})
			}
		},
	},
	{
		// Gunships on a target with one anti-air kbot covering one side.
		name: "gunship-defended", ticks: 2400,
		build: func(t *testing.T, s *labScene, c labVec) {
			tgt := s.spawn(t, 1, "CORVP", c)
			s.targets = append(s.targets, tgt)
			s.aa = append(s.aa, s.spawn(t, 1, "CORCRASH", labVec{c.X - 380, c.Z}))
			for i := 0; i < 2; i++ {
				g := s.spawn(t, 0, "ARMBRAWL", labVec{c.X - 1600, c.Z + float64(i)*96})
				s.air = append(s.air, g)
			}
			s.h.Step(30)
			for _, g := range s.air {
				s.order(t, g, input.LatchAttack, tgt, labVec{})
			}
		},
	},
	{
		// Fighters strafing a target with a Pulverizer beside it.
		name: "strafe-defended", ticks: 2400,
		build: func(t *testing.T, s *labScene, c labVec) {
			tgt := s.spawn(t, 1, "CORCV", c)
			s.targets = append(s.targets, tgt)
			s.aa = append(s.aa, s.spawn(t, 1, "CORRL", labVec{c.X - 400, c.Z + 120}))
			for i := 0; i < 3; i++ {
				f := s.spawn(t, 0, "ARMHAWK", labVec{c.X - 1800, c.Z + float64(i)*96})
				s.air = append(s.air, f)
			}
			s.h.Step(30)
			for _, f := range s.air {
				s.order(t, f, input.LatchAttack, tgt, labVec{})
			}
		},
	},
	{
		// Gunships on a target with an anti-air kbot right beside it: no
		// standoff point is out of its reach, so only movement helps.
		name: "gunship-pinned", ticks: 2400,
		build: func(t *testing.T, s *labScene, c labVec) {
			tgt := s.spawn(t, 1, "CORVP", c)
			s.targets = append(s.targets, tgt)
			s.aa = append(s.aa, s.holdPosition(s.spawn(t, 1, "CORCRASH", labVec{c.X + 90, c.Z + 90})))
			for i := 0; i < 2; i++ {
				g := s.spawn(t, 0, "ARMBRAWL", labVec{c.X - 1600, c.Z + float64(i)*96})
				s.air = append(s.air, g)
			}
			s.h.Step(30)
			for _, g := range s.air {
				s.order(t, g, input.LatchAttack, tgt, labVec{})
			}
		},
	},
	{
		// Fighters strafing a factory ringed by four anti-air units: every
		// approach is covered, so only the run shape limits the damage.
		name: "strafe-dense", ticks: 2400,
		build: func(t *testing.T, s *labScene, c labVec) {
			tgt := s.spawn(t, 1, "CORVP", c)
			s.targets = append(s.targets, tgt)
			s.aa = append(s.aa, s.spawn(t, 1, "CORRL", labVec{c.X - 300, c.Z - 260}))
			s.aa = append(s.aa, s.spawn(t, 1, "CORRL", labVec{c.X + 300, c.Z + 260}))
			s.aa = append(s.aa, s.holdPosition(s.spawn(t, 1, "CORCRASH", labVec{c.X + 280, c.Z - 300})))
			s.aa = append(s.aa, s.holdPosition(s.spawn(t, 1, "CORCRASH", labVec{c.X - 280, c.Z + 300})))
			fighter := "ARMHAWK"
			if v := os.Getenv("NANOLATHE_AIRLAB_FIGHTER"); v != "" {
				fighter = v
			}
			for i := 0; i < 4; i++ {
				f := s.spawn(t, 0, fighter, labVec{c.X - 1700, c.Z + float64(i)*80 - 120})
				s.air = append(s.air, f)
			}
			s.h.Step(30)
			for _, f := range s.air {
				s.order(t, f, input.LatchAttack, tgt, labVec{})
			}
		},
	},
	{
		// Attack tempo with nothing shooting back: fighters strafing.
		name: "strafe-open", ticks: 2400,
		build: func(t *testing.T, s *labScene, c labVec) {
			tgt := s.holdPosition(s.spawn(t, 1, "CORCV", c))
			s.targets = append(s.targets, tgt)
			for i := 0; i < 2; i++ {
				f := s.spawn(t, 0, "ARMHAWK", labVec{c.X - 1500, c.Z + float64(i)*96})
				s.air = append(s.air, f)
			}
			s.h.Step(30)
			for _, f := range s.air {
				s.order(t, f, input.LatchAttack, tgt, labVec{})
			}
		},
	},
	{
		// Attack tempo with nothing shooting back: gunships.
		name: "gunship-open", ticks: 2400,
		build: func(t *testing.T, s *labScene, c labVec) {
			tgt := s.spawn(t, 1, "CORVP", c)
			s.targets = append(s.targets, tgt)
			for i := 0; i < 2; i++ {
				g := s.spawn(t, 0, "ARMBRAWL", labVec{c.X - 1500, c.Z + float64(i)*96})
				s.air = append(s.air, g)
			}
			s.h.Step(30)
			for _, g := range s.air {
				s.order(t, g, input.LatchAttack, tgt, labVec{})
			}
		},
	},
	{
		// Fighters patrolling over enemy ground: harmless units and anti-air.
		name: "patrol-fighters", ticks: 3600,
		build: func(t *testing.T, s *labScene, c labVec) {
			s.targets = append(s.targets, s.spawn(t, 1, "CORCV", labVec{c.X - 150, c.Z + 200}))
			s.targets = append(s.targets, s.spawn(t, 1, "CORCV", labVec{c.X + 150, c.Z - 150}))
			s.targets = append(s.targets, s.spawn(t, 1, "CORSOLAR", labVec{c.X + 50, c.Z + 30}))
			s.aa = append(s.aa, s.spawn(t, 1, "CORCRASH", labVec{c.X + 260, c.Z + 220}))
			s.aa = append(s.aa, s.spawn(t, 1, "CORRL", labVec{c.X - 280, c.Z - 240}))
			for i := 0; i < 4; i++ {
				g := s.spawn(t, 0, "ARMHAWK", labVec{c.X - 1400, c.Z + float64(i)*90})
				s.air = append(s.air, g)
			}
			s.h.Step(30)
			for _, g := range s.air {
				s.order(t, g, input.LatchPatrol, nil, labVec{c.X + 600, c.Z})
			}
		},
	},
	{
		// Gunships patrolling over enemy ground: harmless units and anti-air.
		name: "patrol-mixed", ticks: 3600,
		build: func(t *testing.T, s *labScene, c labVec) {
			s.targets = append(s.targets, s.spawn(t, 1, "CORCV", labVec{c.X - 150, c.Z + 200}))
			s.targets = append(s.targets, s.spawn(t, 1, "CORCV", labVec{c.X + 150, c.Z - 150}))
			s.targets = append(s.targets, s.spawn(t, 1, "CORSOLAR", labVec{c.X + 50, c.Z + 30}))
			s.aa = append(s.aa, s.spawn(t, 1, "CORCRASH", labVec{c.X + 260, c.Z + 220}))
			s.aa = append(s.aa, s.spawn(t, 1, "CORCRASH", labVec{c.X - 280, c.Z - 240}))
			for i := 0; i < 4; i++ {
				g := s.spawn(t, 0, "ARMBRAWL", labVec{c.X - 1400, c.Z + float64(i)*90})
				s.air = append(s.air, g)
			}
			s.h.Step(30)
			for _, g := range s.air {
				s.order(t, g, input.LatchPatrol, nil, labVec{c.X + 600, c.Z})
			}
		},
	},
	{
		// Bombers patrolling over an outpost: economy under a Pulverizer and
		// two Storms, the anti-air standing off the bombers' flight line.
		name: "patrol-bombers", ticks: 3600,
		build: func(t *testing.T, s *labScene, c labVec) {
			for i, at := range []labVec{{c.X - 120, c.Z - 60}, {c.X + 60, c.Z + 90}, {c.X + 200, c.Z - 120}, {c.X - 40, c.Z + 220}} {
				key := "CORMEX"
				if i%2 == 1 {
					key = "CORSOLAR"
				}
				s.targets = append(s.targets, s.spawn(t, 1, key, at))
			}
			s.aa = append(s.aa, s.spawn(t, 1, "CORRL", labVec{c.X + 120, c.Z + 420}))
			s.aa = append(s.aa, s.holdPosition(s.spawn(t, 1, "CORSTORM", labVec{c.X - 260, c.Z + 330})))
			s.aa = append(s.aa, s.holdPosition(s.spawn(t, 1, "CORSTORM", labVec{c.X + 340, c.Z + 300})))
			for i := 0; i < 4; i++ {
				b := s.fireAtWill(s.spawn(t, 0, "ARMTHUND", labVec{c.X - 1400, c.Z + float64(i)*80 - 120}))
				s.air = append(s.air, b)
			}
			s.h.Step(30)
			for _, b := range s.air {
				s.order(t, b, input.LatchPatrol, nil, labVec{c.X + 900, c.Z})
			}
		},
	},
	{
		// Six bombers patrolling over six small undefended targets: how many
		// bombs land on targets that are already gone.
		name: "patrol-bombers-spread", ticks: 3600,
		build: func(t *testing.T, s *labScene, c labVec) {
			for i := 0; i < 6; i++ {
				key := "CORMEX"
				if i%3 == 2 {
					key = "CORSOLAR"
				}
				s.targets = append(s.targets, s.spawn(t, 1, key, labVec{c.X + float64(i%3)*170 - 170, c.Z + float64(i/3)*200 - 100}))
			}
			for i := 0; i < 6; i++ {
				b := s.fireAtWill(s.spawn(t, 0, "ARMTHUND", labVec{c.X - 1400, c.Z + float64(i)*70 - 180}))
				s.air = append(s.air, b)
			}
			s.h.Step(30)
			for _, b := range s.air {
				s.order(t, b, input.LatchPatrol, nil, labVec{c.X + 900, c.Z})
			}
		},
	},
	{
		// Gunships patrolling onto a Punisher with Storms and a Pulverizer
		// standing off beside it.
		name: "gunship-punisher", ticks: 3600,
		build: func(t *testing.T, s *labScene, c labVec) {
			s.targets = append(s.targets, s.spawn(t, 1, "CORPUN", c))
			s.aa = append(s.aa, s.holdPosition(s.spawn(t, 1, "CORSTORM", labVec{c.X + 120, c.Z + 330})))
			s.aa = append(s.aa, s.holdPosition(s.spawn(t, 1, "CORSTORM", labVec{c.X + 330, c.Z - 140})))
			s.aa = append(s.aa, s.spawn(t, 1, "CORRL", labVec{c.X + 420, c.Z + 260}))
			for i := 0; i < 4; i++ {
				g := s.spawn(t, 0, "ARMBRAWL", labVec{c.X - 1400, c.Z + float64(i)*90 - 135})
				s.air = append(s.air, g)
			}
			s.h.Step(30)
			for _, g := range s.air {
				s.order(t, g, input.LatchPatrol, nil, labVec{c.X + 300, c.Z})
			}
		},
	},
	{
		// Damaged gunships on patrol at home, a constructor patrolling the
		// same ground and no repair pad.
		name: "repair-constructor", ticks: 2700,
		build: func(t *testing.T, s *labScene, c labVec) {
			k := s.spawn(t, 0, "ARMCK", labVec{c.X, c.Z + 200})
			s.builders = append(s.builders, k)
			for i := 0; i < 3; i++ {
				g := s.spawn(t, 0, "ARMBRAWL", labVec{c.X - 700, c.Z + float64(i)*90})
				g.Health = g.Def.MaxDamage * 2 / 5
				s.air = append(s.air, g)
			}
			s.h.Step(30)
			s.order(t, k, input.LatchPatrol, nil, labVec{c.X + 300, c.Z + 200})
			for _, g := range s.air {
				s.order(t, g, input.LatchPatrol, nil, labVec{c.X + 900, c.Z})
			}
		},
	},
}

func TestAircraftTacticsLab(t *testing.T) {
	out := os.Getenv("NANOLATHE_AIRLAB_OUT")
	if out == "" {
		t.Skip("NANOLATHE_AIRLAB_OUT unset")
	}
	root := testsupport.RetailRoot(t)
	rules := os.Getenv("NANOLATHE_AIRLAB_RULES")
	if rules == "" {
		rules = "modern"
	}
	mapName := os.Getenv("NANOLATHE_AIRLAB_MAP")
	if mapName == "" {
		mapName = diagMap
	}
	seeds := []uint32{11, 23, 37, 41, 53, 67, 79, 97}
	if v := os.Getenv("NANOLATHE_AIRLAB_SEEDS"); v != "" {
		seeds = seeds[:0]
		for _, f := range strings.Split(v, ",") {
			n, err := strconv.Atoi(f)
			if err != nil {
				t.Fatal(err)
			}
			seeds = append(seeds, uint32(n))
		}
	}
	only := os.Getenv("NANOLATHE_AIRLAB_SCENARIO")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(out, "results-"+rules+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, sc := range labScenarios {
		if only != "" && !strings.Contains(","+only+",", ","+sc.name+",") {
			continue
		}
		for _, seed := range seeds {
			sc, seed := sc, seed
			t.Run(fmt.Sprintf("%s/%d", sc.name, seed), func(t *testing.T) {
				h := labHarness(t, root, mapName, seed)
				if err := h.Session.SetRules(rules); err != nil {
					t.Fatal(err)
				}
				w := h.Session.World
				c := labVec{float64(w.CellW) * 8, float64(w.CellH) * 8}
				s := newLabScene(h, sc.name, c)
				if os.Getenv("NANOLATHE_AIRLAB_ROTATE") != "0" {
					s.turn = float64(seed*47%360) * math.Pi / 180
				}
				sc.build(t, s, c)
				s.begin()
				var trace *os.File
				if os.Getenv("NANOLATHE_AIRLAB_TRACE") == "1" {
					trace, _ = os.Create(filepath.Join(out, fmt.Sprintf("trace-%s-%s-%d.txt", rules, sc.name, seed)))
					defer trace.Close()
					s.trace = trace
				}
				var tr *labTracer
				if trace != nil {
					tr = newLabTracer(trace, s)
				}
				for i := 0; i < sc.ticks; i++ {
					h.Step(1)
					s.observe()
					if tr != nil {
						tr.step(i)
					}
					if trace != nil && i%5 == 0 {
						for k, u := range s.air {
							r := h.Observe(u)
							sl := u.SlotAt(0)
							var rl int32
							var tk uint8
							var ready, pend bool
							if sl != nil {
								rl, tk, ready, pend = sl.Reload, uint8(sl.Target.Kind), sl.Aim.Ready, sl.Aim.IssueBit
							}
							var thp int32
							if len(s.targets) > 0 {
								thp = s.targets[0].Health
							}
							extra := ""
							if s1 := u.SlotAt(1); s1 != nil && s1.Weapon != nil {
								extra = fmt.Sprintf(" s1=tk%d/%d rl%d en%v", uint8(s1.Target.Kind), s1.Target.Unit, s1.Reload, s1.IsEnabled())
							}
							if labPilotTrace != nil {
								extra += labPilotTrace(h, u)
							}
							fmt.Fprintf(trace, "t=%d air%d pos=(%.0f,%.0f,%.0f) hd=%.0f spd=%.2f hp=%d ord=%s ph=%d slot=tk%d rl%d rdy%v pend%v thp=%d%s\n", r.Tick, k, fx(u.X), fx(u.Y), fx(u.Z), float64(u.Move.Heading)*360/65536, fx(u.Move.Speed), u.Health, r.HeadName, r.HeadPhase, tk, rl, ready, pend, thp, extra)
						}
					}
					if s.killTick != 0 && len(s.aa) == 0 && !s.immortal {
						break
					}
				}
				r := s.result(rules, seed)
				if os.Getenv("NANOLATHE_AIRLAB_BOMBS") == "1" {
					bf, err := os.Create(filepath.Join(out, fmt.Sprintf("bombs-%s-%s-%d.txt", rules, sc.name, seed)))
					if err == nil {
						for _, key := range s.bombOrder {
							b := s.bombs[key]
							var th pool.Handle
							if b.tgt != nil {
								th = b.tgt.Handle
							}
							fmt.Fprintf(bf, "shooter=%d created=%d rel=(%.0f,%.0f,%.0f) tgt=(%.0f,%.0f) imp=(%.0f,%.0f) miss=%.1f along=%.1f cross=%.1f unit=%d wasted=%v\n",
								b.shooterI, b.created, b.release.X, b.relY, b.release.Z, b.target.X, b.target.Z, b.impact.X, b.impact.Z, b.miss, b.along, b.cross, th, b.wasted)
						}
						bf.Close()
					}
				}
				if err := enc.Encode(r); err != nil {
					t.Fatal(err)
				}
				t.Logf("%s seed=%d: %+v", sc.name, seed, r)
			})
		}
	}
}

// TestAircraftTacticsCost times the authoritative tick with a large air
// battle: three wings of 24 bombers, gunships and fighters attacking a
// defended base. It runs only when NANOLATHE_AIRLAB_PERF names an output
// file, and appends one line per run: rules, measured ticks, ns per tick.
// Wall time here is a diagnostic measurement, never a simulation input.
func TestAircraftTacticsCost(t *testing.T) {
	out := os.Getenv("NANOLATHE_AIRLAB_PERF")
	if out == "" {
		t.Skip("NANOLATHE_AIRLAB_PERF unset")
	}
	root := testsupport.RetailRoot(t)
	rules := os.Getenv("NANOLATHE_AIRLAB_RULES")
	if rules == "" {
		rules = "modern"
	}
	h := labHarness(t, root, diagMap, 11)
	if err := h.Session.SetRules(rules); err != nil {
		t.Fatal(err)
	}
	w := h.Session.World
	c := labVec{float64(w.CellW) * 8, float64(w.CellH) * 8}
	s := newLabScene(h, "cost", c)
	var targets []*units.Unit
	for i := 0; i < 6; i++ {
		targets = append(targets, s.spawn(t, 1, "CORVP", labVec{c.X + float64(i%3)*200 - 200, c.Z + float64(i/3)*220 - 110}))
	}
	var defenders []*units.Unit
	for i := 0; i < 8; i++ {
		key := "CORRL"
		if i%2 == 1 {
			key = "CORCRASH"
		}
		defenders = append(defenders, s.spawn(t, 1, key, labVec{c.X + float64(i%4)*240 - 360, c.Z + float64(i/4)*560 - 280}))
	}
	var air []*units.Unit
	for i, key := range []string{"ARMTHUND", "ARMBRAWL", "ARMHAWK"} {
		for k := 0; k < 24; k++ {
			air = append(air, s.spawn(t, 0, key, labVec{c.X - 1700 + float64(k%6)*60, c.Z - 900 + float64(i)*600 + float64(k/6)*60}))
		}
	}
	h.Step(30)
	for k, u := range air {
		s.order(t, u, input.LatchAttack, targets[k%len(targets)], labVec{})
	}
	h.Step(150)
	// Targets and defenders are healed between ticks so the whole window is a
	// sustained battle in both builds; the heal is outside the timed step.
	const measured = 900
	var elapsed time.Duration
	var dealt, aaDealt int64
	for i := 0; i < measured; i++ {
		for _, g := range targets {
			if g.Alive && !g.Dying {
				dealt += int64(g.Def.MaxDamage - g.Health)
				g.Health = g.Def.MaxDamage
			}
		}
		for _, g := range defenders {
			if g.Alive && !g.Dying {
				aaDealt += int64(g.Def.MaxDamage - g.Health)
				g.Health = g.Def.MaxDamage
			}
		}
		start := time.Now()
		h.Step(1)
		elapsed += time.Since(start)
	}
	ns := elapsed.Nanoseconds() / measured
	alive, flying, hurt := 0, 0, 0
	for _, u := range air {
		if u.Alive {
			alive++
			if u.Move.Mode&3 == 2 {
				flying++
			}
			if u.Health < u.Def.MaxDamage {
				hurt++
			}
		}
	}
	dead := 0
	for _, tg := range targets {
		if !tg.Alive || tg.Dying {
			dead++
		}
	}
	if q := orders.QueueForUnit(air[0]); q != nil && q.Head() != nil {
		t.Logf("air0 head=%s flying=%d hurt=%d targets dead=%d pos=(%.0f,%.0f)", orders.DescriptorFor(q.Head().ID).Name, flying, hurt, dead, fx(air[0].X), fx(air[0].Z))
	}
	f, err := os.OpenFile(out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	byKind := map[string]int{}
	for _, u := range air {
		if u.Alive {
			byKind[u.Def.UnitName]++
		}
	}
	fmt.Fprintf(f, "%s ticks=%d ns_per_tick=%d aircraft_alive=%d (thund %d brawl %d hawk %d) flying=%d hurt=%d targets_dead=%d dealt_targets=%d dealt_aa=%d\n", rules, measured, ns, alive, byKind["ARMTHUND"], byKind["ARMBRAWL"], byKind["ARMHAWK"], flying, hurt, dead, dealt, aaDealt)
}
