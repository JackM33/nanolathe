package session

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// Online sharing and alliances (DESIGN_MULTIPLAYER §6.3, §6.7 Q22/Q25, §7.1).
// Admission is Nanolathe protocol; the gates and the effects are retail's
// [05 R-SHARE-01 §1]–[05 R-SHARE-01 §6] [07 "Tab options menu and manual
// exit"]. Every multi-seat test has at least three human seats (Q22).

// shareRoom resolves and freezes the authored four-position room: three
// humans with the given teams (0 none), then a Classic computer on no team
// hosted by seat 0.
func shareRoom(t *testing.T, teams ...uint8) (*content.SimulationInputs, EffectiveMatchConfig) {
	t.Helper()
	setup := onlineTestSetup(false, teams...)
	setup.Computers = []OnlineComputer{{Side: 1, Color: 5, Kind: ai.ControllerClassic, Difficulty: 1}}
	return onlineComputerFixtureMatch(t, setup)
}

// shareReplica composes the room presenting seat and readies it.
func shareReplica(t *testing.T, inputs *content.SimulationInputs, config EffectiveMatchConfig, seat uint8) *Session {
	t.Helper()
	s, err := NewPlaytestSkirmish(inputs, config, seat, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeAIControllers)
	if err := s.PrepareGrantedBattle(); err != nil {
		t.Fatal(err)
	}
	return s
}

// shareEntry is one stamped command of a test stream.
type shareEntry struct {
	seat uint8
	c    SeatCommand
}

// shareStream feeds every replica the same stream entries.
type shareStream struct {
	replicas []*Session
	pos      uint64
}

// step enqueues entries for the next tick on every replica, runs it, and
// returns the first replica's receipts, failing when the replicas' receipts,
// unit checksums, random streams, player records, shared-victory bits or
// explored histories part.
func (st *shareStream) step(t *testing.T, entries ...shareEntry) []CommandReceipt {
	t.Helper()
	a := st.replicas[0]
	tick := a.Clock.GlobalTick + 1
	base := st.pos
	var first []CommandReceipt
	for i, s := range st.replicas {
		st.pos = base
		for _, e := range entries {
			st.pos++
			if err := s.EnqueueSeatCommand(CommandStamp{Seat: e.seat, Tick: tick, Position: st.pos}, e.c); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.StepGranted(tick); err != nil {
			t.Fatal(err)
		}
		rs := s.DrainCommandReceipts()
		if i == 0 {
			first = rs
			continue
		}
		if len(rs) != len(first) {
			t.Fatalf("tick %d: replica %d has %d receipts, want %d", tick, i, len(rs), len(first))
		}
		for k := range rs {
			if rs[k].Stamp != first[k].Stamp || rs[k].Outcome != first[k].Outcome {
				t.Fatalf("tick %d: replica %d's receipt %+v, replica 0's %+v", tick, i, rs[k], first[k])
			}
		}
		if s.UnitStateChecksum() != a.UnitStateChecksum() || *s.SimRNG() != *a.SimRNG() || *s.CrtRNG() != *a.CrtRNG() ||
			s.Econ.Players != a.Econ.Players || s.onlineResults.sharedVictory != a.onlineResults.sharedVictory ||
			!slicesEqual16(s.Vis.WordMask(), a.Vis.WordMask()) {
			t.Fatalf("tick %d: replica %d diverged from replica 0", tick, i)
		}
	}
	return first
}

func slicesEqual16(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// phaseOne applies one stamped entry by running phase 1 of the next tick
// alone, as the seat fixture does, and returns its receipt.
func phaseOne(t *testing.T, s *Session, pos *uint64, e shareEntry) CommandReceipt {
	t.Helper()
	next := s.Clock.GlobalTick + 1
	*pos++
	if err := s.EnqueueSeatCommand(CommandStamp{Seat: e.seat, Tick: next, Position: *pos}, e.c); err != nil {
		t.Fatal(err)
	}
	s.applyHumanCommands(next)
	s.Clock.GlobalTick = next
	rs := s.DrainCommandReceipts()
	if len(rs) != 1 {
		t.Fatalf("receipts %+v, want one", rs)
	}
	return rs[0]
}

func declare(target uint8, value bool) SeatCommand {
	return SeatCommand{Kind: SeatDeclareAlliance, DeclareAlliance: DeclareAlliancePayload{Target: target, Value: value}}
}

func sharedVictory(on bool) SeatCommand {
	return SeatCommand{Kind: SeatSharedVictory, SharedVictory: SharedVictoryPayload{On: on}}
}

// allianceFixture is onlineTeamFixture with the online command context for
// its human rows and their teams, at tick 10.
func allianceFixture(t *testing.T, groups ...int) (*Session, []pool.Handle) {
	t.Helper()
	s, _, handles := onlineTeamFixture(t, groups...)
	roles := make([]MatchRole, len(groups))
	seats := make([]MatchSeat, len(groups))
	for i, g := range groups {
		roles[i] = MatchRoleHuman
		seats[i].AllyGroup = uint8(g)
	}
	s.Clock = &clock.State{GlobalTick: 10}
	s.seatCommands.online = &onlineCommandConfig{roles: roles, unitLimit: 20}
	s.onlineResults.setTeams(seats)
	return s, handles
}

// Q22: a declaration is one directed entry of the one matrix, its row B the
// transpose; a one-way declaration and shared victory win nothing, and a
// mutual alliance with both shared-victory bits wins only once the third
// seat is eliminated [08 R-SKIR-01 §3] "Victory detection".
func TestOnlineDeclarationsAreOneWayAndVictoryNeedsTheThirdSeat(t *testing.T) {
	s, handles := allianceFixture(t, 5, 5, 5)
	var pos uint64
	expectOutcome(t, phaseOne(t, s, &pos, shareEntry{0, declare(1, true)}), CommandApplied)
	if !s.Econ.Players[0].Allies[1] || s.Econ.Players[1].Allies[0] {
		t.Fatal("a declaration was not one-way")
	}
	if b := allianceRowB(s.Econ, 1); !b[0] || b[2] {
		t.Fatalf("row B of seat 1 %v is not the transpose", b)
	}
	for seat := uint8(0); seat < 2; seat++ {
		expectOutcome(t, phaseOne(t, s, &pos, shareEntry{seat, sharedVictory(true)}), CommandApplied)
	}
	onlineResultKill(t, s, handles[2])
	onlineEvaluateAll(s, 3, 30)
	if got := onlineArmed(s, 3); !sameKinds(got, "", "", "defeat") {
		t.Fatalf("a one-way declaration armed %v", got)
	}
	expectOutcome(t, phaseOne(t, s, &pos, shareEntry{1, declare(0, true)}), CommandApplied)
	for tick := uint32(60); tick <= 210; tick += 30 {
		onlineEvaluateAll(s, 3, tick)
	}
	if got := onlineArmed(s, 3); !sameKinds(got, "victory", "victory", "defeat") || !s.OnlineBattleEnded() {
		t.Fatalf("the mutual allies' results %v, battle ended %v", got, s.OnlineBattleEnded())
	}

	// With the third seat alive the same alliance wins nothing.
	s, _ = allianceFixture(t, 5, 5, 5)
	pos = 0
	for _, e := range []shareEntry{{0, declare(1, true)}, {1, declare(0, true)}, {0, sharedVictory(true)}, {1, sharedVictory(true)}} {
		expectOutcome(t, phaseOne(t, s, &pos, e), CommandApplied)
	}
	onlineEvaluateAll(s, 3, 30)
	if got := onlineArmed(s, 3); !sameKinds(got, "", "", "") {
		t.Fatalf("victory armed beside a live third seat: %v", got)
	}
}

// The Allies window's gates, checked by every receiver: a teammate's row
// is greyed, a computer row and the issuer's own are inactive, an
// eliminated or watching seat cannot declare, and a seat on a team of two
// or more cannot change its shared-victory bit [07 "Tab options menu and
// manual exit"]. A refusal changes nothing.
func TestOnlineAllianceAdmission(t *testing.T) {
	s, handles := allianceFixture(t, 1, 1, 5, 5)
	s.seatCommands.online.roles[3] = MatchRoleComputer
	s.Econ.Players[3].ControllerState = 2
	var pos uint64
	before := s.Econ.Players
	for _, e := range []shareEntry{
		{0, declare(1, true)},     // teammate
		{0, declare(0, true)},     // self
		{0, declare(3, true)},     // computer
		{0, declare(9, true)},     // no row
		{0, sharedVictory(false)}, // team of two
	} {
		expectOutcome(t, phaseOne(t, s, &pos, e), CommandRejected)
	}
	if s.Econ.Players != before || !s.onlineResults.sharedVictory[0] {
		t.Fatal("a refused alliance command changed the battle")
	}
	s.Econ.Players[1].Watcher = true
	expectOutcome(t, phaseOne(t, s, &pos, shareEntry{1, declare(2, true)}), CommandRejected)
	s.Econ.Players[1].Watcher = false
	expectOutcome(t, phaseOne(t, s, &pos, shareEntry{0, declare(2, true)}), CommandApplied)
	expectOutcome(t, phaseOne(t, s, &pos, shareEntry{2, sharedVictory(true)}), CommandApplied)
	onlineResultKill(t, s, handles[2])
	expectOutcome(t, phaseOne(t, s, &pos, shareEntry{2, declare(0, true)}), CommandRejected)
	expectOutcome(t, phaseOne(t, s, &pos, shareEntry{0, declare(2, false)}), CommandRejected)
	if !s.Econ.Players[0].Allies[2] || !s.onlineResults.sharedVictory[2] || s.Econ.Players[1].Allies[2] {
		t.Fatal("the admitted writes did not stand")
	}
}

// Three replicas composed for seats 0, 1 and 2 agree tick for tick through
// toggles, thresholds, declarations, gifts and the automatic shares; the
// committed frame publishes what the screens read.
func TestOnlineSharingReplicasAgree(t *testing.T) {
	inputs, config := shareRoom(t, 0, 0, 0)
	st := &shareStream{replicas: []*Session{shareReplica(t, inputs, config, 0), shareReplica(t, inputs, config, 1), shareReplica(t, inputs, config, 2)}}
	a := st.replicas[0]
	cmdr := func(seat int) pool.UnitRef {
		var ref pool.UnitRef
		a.Units.ForEachPlayerSliceLive(seat, func(u *units.Unit) {
			if ref.Handle == 0 {
				ref = pool.UnitRef{Handle: u.Handle, Serial: u.AllocationSerial}
			}
		})
		return ref
	}
	rs := st.step(t,
		shareEntry{0, SeatCommand{Kind: SeatShareAll, ShareAll: ShareAllPayload{Metal: true, Energy: true, Mapping: true, Radar: true}}},
		shareEntry{0, declare(1, true)},
		shareEntry{1, SeatCommand{Kind: SeatSetShareMetal, SetShareMetal: SetShareMetalPayload{Amount: -5}}},
		shareEntry{2, SeatCommand{Kind: SeatShareGift, ShareGift: ShareGiftPayload{Target: 1, Metal: 40, Energy: 60, Actors: []pool.UnitRef{cmdr(2)}, MapInfo: true}}},
		shareEntry{1, declare(3, true)}, // a computer row
	)
	for i, want := range []CommandOutcome{CommandApplied, CommandApplied, CommandApplied, CommandApplied, CommandRejected} {
		expectOutcome(t, rs[i], want)
	}
	if p := &a.Econ.Players[0]; !p.AutoShareMetal || !p.AutoShareEnergy || !p.AutoShareSensor || !p.ShareRadar {
		t.Fatal("ShareAll did not turn every option on")
	}
	if a.Econ.Players[1].MetalShareThreshold != -5 {
		t.Fatalf("a negative threshold stored as %v", a.Econ.Players[1].MetalShareThreshold)
	}
	if u := a.Units.LookupReference(cmdr(2)); u == nil || u.Owner != 2 {
		t.Fatal("a commander was given away")
	}
	for a.Clock.GlobalTick < 120 {
		st.step(t)
	}
	if got := a.Econ.RadarShareOwners(1); got != 1 {
		t.Fatalf("seat 1 sees sharers %b, want seat 0 alone", got)
	}
	if a.Econ.RadarShareOwners(2) != 0 {
		t.Fatal("seat 0 shares radar toward a seat it did not declare toward")
	}
	h := cmdr(0)
	if a.Vis.StatusForPerspective(1, uint16(h.Handle), h.Serial, 0, 0)&visibility.FriendlyMask == 0 {
		t.Fatal("the sharer's commander is not friendly in its ally's perspective")
	}
	if a.Vis.StatusForPerspective(2, uint16(h.Handle), h.Serial, 0, 0)&visibility.FriendlyMask != 0 {
		t.Fatal("the sharer's commander is friendly to a seat it did not declare toward")
	}
	f := st.replicas[2].Snapshot.Current()
	if f == nil || !f.Sharing.Online || f.Sharing.Seat != 2 || !f.Sharing.MayShare || !f.Sharing.SharedVictoryChangeable {
		t.Fatalf("seat 2's sharing view %+v", f.Sharing)
	}
	if !f.Players[0].Allies[1] || !f.Players[1].AlliedBy[0] || f.Players[0].AlliedBy[1] || !f.Players[0].ShareRadar {
		t.Fatal("the frame's alliance rows or share options are wrong")
	}
	if !f.Players[0].GiftTarget || !f.Players[3].GiftTarget || f.Players[2].GiftTarget ||
		!f.Players[0].AllianceTarget || f.Players[3].AllianceTarget || f.Players[2].AllianceTarget {
		t.Fatal("the frame's targets are not the receiver's admission")
	}
}

// One SHARE confirmation runs metal, energy, units and map information in
// turn, none gating the next: a step that does nothing — metal from an empty
// stock, a commander the screen leaves out — leaves the later ones to run.
// There is no alliance test: an enemy receives [05 R-SHARE-01 §5].
func TestOnlineShareGiftRunsEveryStep(t *testing.T) {
	inputs, config := shareRoom(t, 0, 0, 0)
	s := shareReplica(t, inputs, config, 0)
	tank, _ := s.Catalog.Unit("porttank")
	h, err := s.Units.Create(tank, 1, 200<<16, 0, 200<<16)
	if err != nil {
		t.Fatal(err)
	}
	var cmdr pool.UnitRef
	s.Units.ForEachPlayerSliceLive(1, func(u *units.Unit) {
		if u.Handle != h && cmdr.Handle == 0 {
			cmdr = pool.UnitRef{Handle: u.Handle, Serial: u.AllocationSerial}
		}
	})
	giver, target := &s.Econ.Players[1], &s.Econ.Players[2]
	giver.Stock[economy.Metal] = 0
	giver.Stock[economy.Energy] = 500
	credit := target.Mirror
	only := 0
	for i, w := range s.Vis.WordMask() {
		if w&2 != 0 && w&4 == 0 {
			only = i + 1
			break
		}
	}
	if only == 0 {
		t.Fatal("the fixture gives seat 1 no explored tile of its own")
	}
	var pos uint64
	r := phaseOne(t, s, &pos, shareEntry{1, SeatCommand{Kind: SeatShareGift, ShareGift: ShareGiftPayload{Target: 2, Metal: 100, Energy: 900,
		Actors: []pool.UnitRef{s.Units.Reference(h), cmdr}, MapInfo: true}}})
	expectOutcome(t, r, CommandApplied)
	if giver.Stock[economy.Metal] != 0 || target.Mirror[economy.Metal] != credit[economy.Metal] {
		t.Fatal("metal moved from an empty stock")
	}
	if giver.Stock[economy.Energy] != 0 || target.Mirror[economy.Energy].Production != credit[economy.Energy].Production+500 {
		t.Fatalf("energy: giver %v, target credit %v", giver.Stock[economy.Energy], target.Mirror[economy.Energy].Production)
	}
	if u := s.Units.Unit(h); u == nil || !u.Dying {
		t.Fatal("the given tank's departing copy was not killed")
	}
	found := false
	s.Units.ForEachPlayerSliceLive(2, func(u *units.Unit) { found = found || u.Def == tank })
	if !found {
		t.Fatal("the target received no tank")
	}
	if s.Units.LookupReference(cmdr).Owner != 1 {
		t.Fatal("the commander was given")
	}
	if s.Vis.WordMask()[only-1]&4 == 0 {
		t.Fatal("the map information did not reach the target at once")
	}
}

// The transfer's branch is the target's machine's: a computer the giver
// hosts takes the local branch, which copies the remaining fraction exactly;
// any other target the remote one, which carries it as a whole number
// [05 R-WORK-01 §15] (DESIGN_MULTIPLAYER §6.3).
func TestOnlineShareGiftBranchFollowsTheHost(t *testing.T) {
	inputs, config := shareRoom(t, 0, 0, 0)
	if s := shareReplica(t, inputs, config, 0); s.giftCopyRule(0, 3) != construction.GiftCopyLocal ||
		s.giftCopyRule(1, 3) != construction.GiftCopyRemote || s.giftCopyRule(0, 1) != construction.GiftCopyRemote {
		t.Fatal("the copy rule does not follow the computer's host")
	}
	for _, tc := range []struct {
		giver, target uint8
		remaining     float32
	}{{0, 3, 0.5}, {1, 3, 0}, {0, 2, 0}} {
		s := shareReplica(t, inputs, config, 0)
		tank, _ := s.Catalog.Unit("porttank")
		h, err := s.Units.Create(tank, tc.giver, 200<<16, 0, 200<<16)
		if err != nil {
			t.Fatal(err)
		}
		s.Units.Unit(h).Remaining = 0.5
		var pos uint64
		expectOutcome(t, phaseOne(t, s, &pos, shareEntry{tc.giver, SeatCommand{Kind: SeatShareGift,
			ShareGift: ShareGiftPayload{Target: tc.target, Actors: []pool.UnitRef{s.Units.Reference(h)}}}}), CommandApplied)
		var got *units.Unit
		s.Units.ForEachPlayerSliceLive(int(tc.target), func(u *units.Unit) {
			if u.Def == tank {
				got = u
			}
		})
		if got == nil || got.Remaining != tc.remaining {
			t.Fatalf("gift %d→%d: replacement %+v, want remaining %v", tc.giver, tc.target, got, tc.remaining)
		}
	}
}

// The automatic dispatcher runs for every human seat at the end of its due
// tick: with share metal on, the giver's surplus goes to the last declared
// ally poorer than it, and to no one before the due [05 R-SHARE-01 §3].
func TestOnlineAutomaticShareOnItsDue(t *testing.T) {
	inputs, config := shareRoom(t, 0, 0, 0)
	on, off := shareReplica(t, inputs, config, 0), shareReplica(t, inputs, config, 0)
	for _, s := range []*Session{on, off} {
		var pos uint64
		for _, e := range []shareEntry{{0, declare(1, true)}, {0, declare(2, true)}} {
			expectOutcome(t, phaseOne(t, s, &pos, e), CommandApplied)
		}
	}
	on.Econ.SetShareOption(0, economy.ShareOptionMetal, true)
	for _, s := range []*Session{on, off} {
		for s.Clock.GlobalTick < 59 {
			if err := s.StepGranted(s.Clock.GlobalTick + 1); err != nil {
				t.Fatal(err)
			}
		}
		for i, stock := range []float32{900, 300, 400} {
			s.Econ.Players[i].Stock[economy.Metal] = stock
		}
	}
	mirror := func(s *Session, i int) float32 { return s.Econ.Players[i].Mirror[economy.Metal].Production }
	if mirror(on, 2) != mirror(off, 2) {
		t.Fatal("a share before its due")
	}
	for _, s := range []*Session{on, off} {
		if err := s.StepGranted(60); err != nil {
			t.Fatal(err)
		}
	}
	if !(mirror(on, 2) > mirror(off, 2)) || mirror(on, 1) != mirror(off, 1) || !(on.Econ.Players[0].Stock[economy.Metal] < off.Econ.Players[0].Stock[economy.Metal]) {
		t.Fatalf("tick 60: credits %v/%v to seat 2 and %v/%v to seat 1", mirror(on, 2), mirror(off, 2), mirror(on, 1), mirror(off, 1))
	}
}

// Q25: a periodic map share applies on its request tick, the 450th, and not
// before; the recipient then holds every tile the source had explored.
func TestOnlineMapShareOnItsRequestTick(t *testing.T) {
	inputs, config := shareRoom(t, 0, 0, 0)
	s := shareReplica(t, inputs, config, 0)
	st := &shareStream{replicas: []*Session{s}}
	st.step(t, shareEntry{0, SeatCommand{Kind: SeatShareMapping, ShareMapping: ShareMappingPayload{On: true}}}, shareEntry{0, declare(1, true)})
	unshared := func() int {
		n := 0
		for _, w := range s.Vis.WordMask() {
			if w&1 != 0 && w&2 == 0 {
				n++
			}
		}
		return n
	}
	for s.Clock.GlobalTick < 449 {
		st.step(t)
	}
	if unshared() == 0 {
		t.Fatal("seat 1 already holds every tile seat 0 explored before the share")
	}
	st.step(t)
	if n := unshared(); n != 0 {
		t.Fatalf("tick 450: %d of seat 0's tiles not copied to seat 1", n)
	}
}

// Hostile sharing commands are refused whole, before any resource, unit,
// random draw or later command moves: a foreign actor, a watcher issuer,
// the Survival attacker or the giver as target, a negative amount without
// the cheat permission, and a malformed record.
func TestOnlineHostileSharingCommandsChangeNothing(t *testing.T) {
	inputs, config := shareRoom(t, 0, 0, 0)
	s := shareReplica(t, inputs, config, 0)
	var foreign pool.UnitRef
	s.Units.ForEachPlayerSliceLive(0, func(u *units.Unit) { foreign = pool.UnitRef{Handle: u.Handle, Serial: u.AllocationSerial} })
	gift := func(target uint8, metal int32, actors ...pool.UnitRef) SeatCommand {
		return SeatCommand{Kind: SeatShareGift, ShareGift: ShareGiftPayload{Target: target, Metal: metal, Actors: actors}}
	}
	type state struct {
		players [10]economy.Player
		sum     [32]byte
		sim     any
		crt     any
	}
	// The unit checksum without the tick, which each phaseOne advances.
	snap := func() state {
		tick := s.Clock.GlobalTick
		s.Clock.GlobalTick = 0
		defer func() { s.Clock.GlobalTick = tick }()
		return state{s.Econ.Players, s.UnitStateChecksum(), *s.SimRNG(), *s.CrtRNG()}
	}
	before := snap()
	var pos uint64
	for _, e := range []shareEntry{
		{1, gift(2, 10, foreign)},
		{1, gift(1, 10)},
		{1, gift(2, -10)},
		{1, SeatCommand{Kind: SeatShareGift, ShareGift: ShareGiftPayload{Target: 2}, Give: GivePayload{Player: 1}}},
	} {
		expectOutcome(t, phaseOne(t, s, &pos, e), CommandRejected)
	}
	s.seatCommands.online.roles[3] = MatchRoleSurvivalAttacker
	r := phaseOne(t, s, &pos, shareEntry{1, gift(3, 10)})
	expectOutcome(t, r, CommandRejected)
	if !strings.Contains(r.Diagnostic, "Survival attacker") {
		t.Fatalf("attacker refusal %q", r.Diagnostic)
	}
	s.seatCommands.online.roles[3] = MatchRoleComputer
	s.Econ.Players[2].Watcher = true
	expectOutcome(t, phaseOne(t, s, &pos, shareEntry{2, SeatCommand{Kind: SeatShareMetal, ShareMetal: ShareMetalPayload{On: true}}}), CommandRejected)
	expectOutcome(t, phaseOne(t, s, &pos, shareEntry{1, gift(2, 10)}), CommandRejected) // a watching target
	s.Econ.Players[2].Watcher = false
	if snap() != before {
		t.Fatal("a refused sharing command changed the battle")
	}
	expectOutcome(t, phaseOne(t, s, &pos, shareEntry{1, gift(2, 10)}), CommandApplied)
	if s.Econ.Players[1].Stock[economy.Metal] != before.players[1].Stock[economy.Metal]-10 {
		t.Fatal("the next admitted gift did not apply")
	}
}

// A single-player session refuses the sharing and alliance kinds at its
// input boundary, as retail's handlers return outside a networked session
// [07 R-CAM-01 §6].
func TestSinglePlayerRefusesSharingCommands(t *testing.T) {
	f := newSeatFixture(t, false, false)
	for k := HumanShareMetal; k <= HumanSharedVictory; k++ {
		if err := f.s.EnqueueHumanCommand(HumanCommand{Kind: k}); err == nil {
			t.Fatalf("kind %d queued in a single-player session", k)
		}
	}
	if len(f.s.pendingHuman) != 0 {
		t.Fatal("a refused kind was queued")
	}
}

// The client captures every sharing and alliance press, `+Give` and the
// Community kickout into seat commands that survive the online codec.
func TestCaptureOnlineSharingCommands(t *testing.T) {
	f := newSeatFixture(t, true, false)
	f.s.LocalOwner = 1
	a, b := f.ref(f.own1a), f.ref(f.own1b)
	for _, tc := range []struct {
		in   HumanCommand
		want SeatCommand
	}{
		{HumanCommand{Kind: HumanShareMetal, ShareOption: HumanShareOptionCommand{On: true}}, SeatCommand{Kind: SeatShareMetal, ShareMetal: ShareMetalPayload{On: true}}},
		{HumanCommand{Kind: HumanShareEnergy, ShareOption: HumanShareOptionCommand{On: true}}, SeatCommand{Kind: SeatShareEnergy, ShareEnergy: ShareEnergyPayload{On: true}}},
		{HumanCommand{Kind: HumanShareMapping, ShareOption: HumanShareOptionCommand{On: true}}, SeatCommand{Kind: SeatShareMapping, ShareMapping: ShareMappingPayload{On: true}}},
		{HumanCommand{Kind: HumanShareRadar}, SeatCommand{Kind: SeatShareRadar}},
		{HumanCommand{Kind: HumanShareAll, ShareAll: HumanShareAllCommand{Metal: true, Radar: true}}, SeatCommand{Kind: SeatShareAll, ShareAll: ShareAllPayload{Metal: true, Radar: true}}},
		{HumanCommand{Kind: HumanSetShareMetal, ShareThreshold: HumanShareThresholdCommand{Amount: -7}}, SeatCommand{Kind: SeatSetShareMetal, SetShareMetal: SetShareMetalPayload{Amount: -7}}},
		{HumanCommand{Kind: HumanSetShareEnergy, ShareThreshold: HumanShareThresholdCommand{Amount: 300}}, SeatCommand{Kind: SeatSetShareEnergy, SetShareEnergy: SetShareEnergyPayload{Amount: 300}}},
		{HumanCommand{Kind: HumanShareGift, ShareGift: HumanShareGiftCommand{Target: 0, Metal: 5, Energy: 6, Handles: []pool.Handle{b.Handle, a.Handle, b.Handle}, MapInfo: true}},
			SeatCommand{Kind: SeatShareGift, ShareGift: ShareGiftPayload{Target: 0, Metal: 5, Energy: 6, Actors: []pool.UnitRef{a, b}, MapInfo: true}}},
		{HumanCommand{Kind: HumanShareGift, ShareGift: HumanShareGiftCommand{Target: 2}}, SeatCommand{Kind: SeatShareGift, ShareGift: ShareGiftPayload{Target: 2}}},
		{HumanCommand{Kind: HumanDeclareAlliance, DeclareAlliance: HumanDeclareAllianceCommand{Target: 0, Value: true}}, SeatCommand{Kind: SeatDeclareAlliance, DeclareAlliance: DeclareAlliancePayload{Target: 0, Value: true}}},
		{HumanCommand{Kind: HumanSharedVictory, SharedVictory: HumanSharedVictoryCommand{On: true}}, SeatCommand{Kind: SeatSharedVictory, SharedVictory: SharedVictoryPayload{On: true}}},
		{HumanCommand{Kind: HumanGive, Give: HumanGiveCommand{Player: 0, Resource: economy.Energy, Amount: 250}}, SeatCommand{Kind: SeatGive, Give: GivePayload{Player: 0, Resource: economy.Energy, Amount: 250}}},
		{HumanCommand{Kind: HumanCommunityKickout, CommunityKickout: HumanCommunityKickoutCommand{Unit: a.Handle, X: 1 << 16, Y: 2 << 16, Z: 3 << 16}},
			SeatCommand{Kind: SeatCommunityKickout, CommunityKickout: CommunityKickoutPayload{Unit: a, Destination: CommandPoint{X: 1 << 16, Y: 2 << 16, Z: 3 << 16}}}},
	} {
		got, err := f.s.CaptureOnlineCommand(tc.in)
		if err != nil || !commandsEqual(got, tc.want) {
			t.Fatalf("kind %d: capture = %+v, %v; want %+v", tc.in.Kind, got, err, tc.want)
		}
		wire, err := EncodeSeatCommand(OnlineCommand, got)
		if err != nil {
			t.Fatal(err)
		}
		if back, err := DecodeSeatCommand(OnlineCommand, wire); err != nil || !commandsEqual(back, got) {
			t.Fatalf("kind %d: round trip %+v, %v", tc.in.Kind, back, err)
		}
	}
	for _, in := range []HumanCommand{
		{Kind: HumanGive, Give: HumanGiveCommand{Player: 10, Amount: 5}},
		{Kind: HumanGive, Give: HumanGiveCommand{Player: 0, Amount: -5}}, // needs the room's cheat permission
		{Kind: HumanGive, Give: HumanGiveCommand{Player: 0, Amount: 0.5}},
		{Kind: HumanShareGift, ShareGift: HumanShareGiftCommand{Target: 0, Metal: -1}},
		{Kind: HumanShareGift, ShareGift: HumanShareGiftCommand{Target: 10}},
		{Kind: HumanDeclareAlliance, DeclareAlliance: HumanDeclareAllianceCommand{Target: 10}},
		{Kind: HumanCommunityKickout},
	} {
		if got, err := f.s.CaptureOnlineCommand(in); err == nil {
			t.Fatalf("kind %d captured as %+v", in.Kind, got)
		}
	}
}

func commandsEqual(a, b SeatCommand) bool {
	x, _ := EncodeSeatCommand(OnlineCommand, a)
	y, _ := EncodeSeatCommand(OnlineCommand, b)
	return x != nil && bytes.Equal(x, y)
}

// The runtime checkpoint carries an online battle's shared-victory bits
// behind bit 1 of the wind tag; a single-player session writes the bytes it
// always has (TestCheckpointRuntimeVector).
func TestCheckpointRuntimeOnlineSharedVictory(t *testing.T) {
	encode := func(s *Session) []byte {
		var out bytes.Buffer
		if err := s.writeCheckpointRuntime(checkpoint.NewEncoder(&out), &content.CheckpointKeys{}, CheckpointFinalPumpTick); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}
	single := &Session{Clock: &clock.State{GlobalTick: 3}}
	online := &Session{Clock: &clock.State{GlobalTick: 3}, onlineResults: newOnlineResultState([10]bool{true, true})}
	plain, tagged := encode(single), encode(online)
	if len(tagged) != len(plain)+10 {
		t.Fatalf("online section %d bytes longer, want 10", len(tagged)-len(plain))
	}
	online.onlineResults.sharedVictory[1] = true
	if bytes.Equal(encode(online), tagged) {
		t.Fatal("a shared-victory change left the checkpoint unchanged")
	}
}

// The rehearsal's sharing script reaches every new path on every replica
// alike: the toggles and thresholds stand, the declarations break and mend
// around the shared-victory change without arming a result, and the digest
// does not depend on the seat a composition presents (DESIGN_MULTIPLAYER
// §16.7).
func TestRehearsalExercisesSharing(t *testing.T) {
	inputs, config := shareRoom(t, 0, 0, 0)
	var at330, at300 bool
	digest, err := rehearse(inputs, config, 0, func(s *Session) {
		tick := s.Clock.GlobalTick
		p := &s.Econ.Players
		switch tick {
		case rehearsalBreakTick:
			at300 = !p[0].Allies[1] && !p[1].Allies[2] && !p[2].Allies[0] &&
				s.onlineResults.sharedVictory[0] && s.onlineResults.sharedVictory[1] && s.onlineResults.sharedVictory[2]
		case rehearsalMendTick:
			at330 = p[0].Allies[1] && p[1].Allies[2] && p[2].Allies[0] && !p[0].Allies[2] &&
				!s.onlineResults.sharedVictory[0] && p[1].ShareRadar && p[2].AutoShareSensor &&
				p[0].MetalShareThreshold == rehearsalMetalShare && p[0].EnergyShareThreshold == rehearsalEnergyShare
		}
		for i := range s.onlineResults.seats {
			if s.onlineResults.seats[i].armed {
				t.Fatalf("tick %d: seat %d's result armed", tick, i)
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !at300 || !at330 {
		t.Fatalf("the sharing script did not apply: break %v, mend %v", at300, at330)
	}
	for seat := uint8(1); seat < 3; seat++ {
		if got := rehearsalDigestOf(t, inputs, config, seat); got != digest {
			t.Fatalf("seat %d's rehearsal digest %x, want %x", seat, got, digest)
		}
	}
}
