package session

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func activityFixture(t *testing.T, online bool) *seatFixture {
	t.Helper()
	f := newSeatFixture(t, online, false)
	f.s.BindRules(ModernRuleSet())
	return f
}

func activityBuilding(t *testing.T, f *seatFixture, owner uint8) *units.Unit {
	t.Helper()
	def := *f.def
	def.BMCode, def.EnergyUse, def.OnOffable = 0, 10, true
	h, err := f.s.Units.Create(&def, owner, 200<<16, 0, 100<<16)
	if err != nil {
		t.Fatal(err)
	}
	u := f.s.Units.Unit(h)
	u.Remaining = 0
	u.SetActivationEdge(true)
	return u
}

func TestEconomyActivityPauseAndResumeAreIdempotent(t *testing.T) {
	f := activityFixture(t, false)
	for a := economy.ActivityGeneration; a < economy.ActivityCount; a++ {
		c := HumanEconomyActivityCommand{Activity: a, Pause: true}
		if got := f.s.applyEconomyActivity(0, c); got != CommandApplied || !f.s.Econ.ActivityHeld(0, a) {
			t.Fatalf("activity %d: %v", a, got)
		}
		if got := f.s.applyEconomyActivity(0, c); got != CommandNoOp {
			t.Fatal("repeated pause changed hold")
		}
		c.Pause = false
		if got := f.s.applyEconomyActivity(0, c); got != CommandApplied || f.s.Econ.ActivityHeld(0, a) {
			t.Fatalf("resume activity %d: %v", a, got)
		}
		if got := f.s.applyEconomyActivity(0, c); got != CommandNoOp {
			t.Fatal("repeated resume changed hold")
		}
	}
}

func TestEconomyUpkeepRestoresOnlyRetainedAllocationAndIntent(t *testing.T) {
	f := activityFixture(t, false)
	wantRestore := activityBuilding(t, f, 0)
	manualOff := activityBuilding(t, f, 0)
	manualOn := activityBuilding(t, f, 0)
	reused := activityBuilding(t, f, 0)
	captured := activityBuilding(t, f, 0)
	foreign := activityBuilding(t, f, 1)
	foreignDef := *foreign.Def
	mobile := f.s.Units.Unit(f.own0)
	mobile.SetActivationEdge(true)
	f.s.applyEconomyActivity(0, HumanEconomyActivityCommand{Activity: economy.ActivityUpkeep, Pause: true})
	if wantRestore.Activated || manualOff.Activated || !foreign.Activated || !mobile.Activated {
		t.Fatal("upkeep shutdown exceeded owned switchable buildings")
	}
	for _, tc := range []struct {
		u  *units.Unit
		on bool
	}{{manualOff, false}, {manualOn, true}} {
		f.s.applyHumanCommand(HumanCommand{Kind: HumanActivation, Activation: HumanActivationCommand{Unit: tc.u.Handle, Activate: tc.on}}, 11)
	}
	// Another owner and another allocation can never inherit this control.
	captured.Owner = 1
	old := reused.Handle
	f.s.Units.FreeImmediate(old)
	h, err := f.s.Units.Create(&foreignDef, 0, 0, 0, 0)
	if err != nil || h != old {
		t.Fatalf("reuse %d %v", h, err)
	}
	replacement := f.s.Units.Unit(h)
	replacement.Remaining = 0
	replacement.SetActivationEdge(false)
	newcomer := activityBuilding(t, f, 0)
	f.s.applyEconomyActivity(0, HumanEconomyActivityCommand{Activity: economy.ActivityUpkeep})
	if !wantRestore.Activated || manualOff.Activated || manualOn.Activated || replacement.Activated || captured.Activated || !newcomer.Activated || !foreign.Activated {
		t.Fatal("resume overwrote allocation, ownership, manual intent or new units")
	}
	if len(f.s.economyUpkeepRestore) != 0 {
		t.Fatal("resumed restoration retained")
	}
}

func TestEconomyActivitySeatOwnerStrictBypassAndRulesSwitch(t *testing.T) {
	f := activityFixture(t, true)
	streams, players := f.streams(), f.s.Econ.Players
	c := SeatCommand{Kind: SeatEconomyActivity, EconomyActivity: EconomyActivityPayload{Activity: economy.ActivityGeneration, Pause: true}}
	if r := f.issue(t, 1, c); r.Outcome != CommandApplied {
		t.Fatal(r)
	}
	if !f.s.Econ.ActivityHeld(1, economy.ActivityGeneration) || f.s.Econ.ActivityHeld(0, economy.ActivityGeneration) {
		t.Fatal("command did not use stamped issuer")
	}
	if r := f.issue(t, 3, c); r.Outcome != CommandRejected {
		t.Fatal("watcher hold admitted")
	}
	if f.streams() != streams || !reflect.DeepEqual(players, f.s.Econ.Players) {
		t.Fatal("hold command changed resources or RNG")
	}
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Community39} {
		f.s.SetGameplay(mode)
		if f.s.Econ.HoldActive[1][economy.ActivityGeneration] {
			t.Fatal("rule switch retained hold")
		}
		if r := f.issue(t, 1, c); r.Outcome != CommandRejected {
			t.Fatal("unsupported rules admitted hold", r)
		}
		if f.streams() != streams || !reflect.DeepEqual(players, f.s.Econ.Players) {
			t.Fatal("unsupported hold changed resources or RNG")
		}
	}
	local := activityFixture(t, false)
	u := activityBuilding(t, local, 0)
	local.s.applyEconomyActivity(0, HumanEconomyActivityCommand{Activity: economy.ActivityUpkeep, Pause: true})
	local.s.SetGameplay(gameplay.Strict31)
	if !u.Activated || len(local.s.economyUpkeepRestore) != 0 {
		t.Fatal("unsupported rule switch did not restore activation")
	}
}

func TestEconomyUpkeepKeepsPreviouslyQueuedActivationIntent(t *testing.T) {
	for _, on := range []bool{false, true} {
		f := activityFixture(t, false)
		u := activityBuilding(t, f, 0)
		f.s.applyHumanCommand(HumanCommand{Kind: HumanActivation, Activation: HumanActivationCommand{Unit: u.Handle, Activate: on, Queued: true}}, 10)
		f.s.applyEconomyActivity(0, HumanEconomyActivityCommand{Activity: economy.ActivityUpkeep, Pause: true})
		if u.Activated || len(f.s.economyUpkeepRestore) != 0 {
			t.Fatal("hold claimed already queued activation intent")
		}
		q := orders.QueueOfUnit(u)
		q.Pump(u, 11)
		if u.Activated != on {
			t.Fatal("hold changed queued intent")
		}
		f.s.applyEconomyActivity(0, HumanEconomyActivityCommand{Activity: economy.ActivityUpkeep})
		if u.Activated != on {
			t.Fatal("resume overrode queued intent")
		}
	}
}

func TestEconomyActivityWireVersionsAndMalformedInputs(t *testing.T) {
	for _, context := range []CommandContext{OnlineCommand, SinglePlayerReplay} {
		for a := economy.ActivityGeneration; a < economy.ActivityCount; a++ {
			for _, pause := range []bool{false, true} {
				c := SeatCommand{Kind: SeatEconomyActivity, EconomyActivity: EconomyActivityPayload{Activity: a, Pause: pause}}
				wire, err := EncodeSeatCommand(context, c)
				last := byte(0)
				if pause {
					last = 1
				}
				if err != nil || !bytes.Equal(wire, []byte{47, byte(a), last}) {
					t.Fatalf("wire %v %v", wire, err)
				}
				back, err := DecodeSeatCommand(context, wire)
				if err != nil || !reflect.DeepEqual(back, c) {
					t.Fatalf("roundtrip %+v %v", back, err)
				}
				for _, version := range []uint16{1, 2} {
					if _, err := EncodeSeatCommandVersion(version, context, c); err == nil {
						t.Fatal("old schema encoded new kind")
					}
					if _, err := DecodeSeatCommandVersion(version, context, wire); err == nil {
						t.Fatal("old schema decoded new kind")
					}
				}
			}
		}
		for _, bad := range [][]byte{{47}, {47, 1}, {47, 0, 1}, {47, 8, 1}, {47, 255, 1}, {47, 1, 2}, {47, 1, 1, 0}} {
			if _, err := DecodeSeatCommand(context, bad); err == nil {
				t.Fatalf("accepted %v", bad)
			}
		}
	}
}

func TestEconomyActivityCaptureAndReplay(t *testing.T) {
	f := activityFixture(t, false)
	activityBuilding(t, f, 0)
	h := HumanCommand{Kind: HumanEconomyActivity, EconomyActivity: HumanEconomyActivityCommand{Activity: economy.ActivityUpkeep, Pause: true}}
	c, err := f.s.replayCommand(h)
	if err != nil || c.Kind != SeatEconomyActivity {
		t.Fatalf("replay capture %+v %v", c, err)
	}
	f.s.applyHumanCommand(h, 11)
	// The same typed replay command uses the same application as local input.
	playback := activityFixture(t, false)
	p := activityBuilding(t, playback, 0)
	if r := playback.issue(t, 0, c); r.Outcome != CommandApplied || p.Activated {
		t.Fatal("replay hold differs", r)
	}
	online := activityFixture(t, true)
	if out, err := online.s.CaptureOnlineCommand(h); err != nil || out.Kind != SeatEconomyActivity {
		t.Fatal(out, err)
	}
}

func TestEconomyActivityCheckpointOptionalState(t *testing.T) {
	f := activityFixture(t, false)
	baseline := runtimeCheckpointBytes(t, f.s)
	u := activityBuilding(t, f, 0)
	f.s.applyEconomyActivity(0, HumanEconomyActivityCommand{Activity: economy.ActivityUpkeep, Pause: true})
	active := runtimeCheckpointBytes(t, f.s)
	f.s.economyUpkeepRestore[0].Unit.Serial++
	if bytes.Equal(active, runtimeCheckpointBytes(t, f.s)) {
		t.Fatal("checkpoint omitted restoration identity")
	}
	f.s.economyUpkeepRestore[0].Unit = f.s.Units.Reference(u.Handle)
	f.s.applyEconomyActivity(0, HumanEconomyActivityCommand{Activity: economy.ActivityUpkeep})
	if !bytes.Equal(baseline, runtimeCheckpointBytes(t, f.s)) {
		t.Fatal("completed hold changed runtime fingerprint")
	}
}
