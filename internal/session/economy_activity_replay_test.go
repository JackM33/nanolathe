package session

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// The authored commander is a switchable building whose two callbacks count
// their executions. This observes the edge's script work, not just its final
// activation bit [04 R-UNIT-06 §2].
func activityReplayFS(t *testing.T) vfs.FSOps {
	t.Helper()
	fs := replayFixtureFS(t).(*vfs.FS)
	fbi, err := fs.ReadFileLimit("units/portarm.fbi", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	fbi = bytes.Replace(fbi, []byte("BMcode=1;"), []byte("BMcode=0;EnergyUse=10;ActivateWhenBuilt=1;"), 1)
	code := []uint32{0x10021001, 0, 0x10023004, 0, 0x10021001, 0, 0x10023004, 1, 0x10065000}
	for _, counter := range []uint32{0, 1} {
		code = append(code, 0x10021004, counter, 0x10021001, 1, 0x10031000, 0x10023004, counter, 0x10021001, 0, 0x10065000)
	}
	script := fixtureCOBBytes(code, []string{"Create", "Activate", "Deactivate"}, []uint32{0, 9, 19}, []string{"base"})
	binary.LittleEndian.PutUint32(script[16:], 2) // two authored statics [fmt cob]
	var archive bytes.Buffer
	if err := vfs.WriteArchive(&archive, []vfs.ArchiveFile{{Path: "units/portarm.fbi", Data: fbi}, {Path: "scripts/portarm.cob", Data: script}}, vfs.ArchiveWriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.MountArchiveReader("activity.hpi", bytes.NewReader(archive.Bytes()), int64(archive.Len()), 20, vfs.ArchiveOptions{}); err != nil {
		t.Fatal(err)
	}
	return fs
}

func TestEconomyUpkeepPausedResumeMatchesLiveAndReplay(t *testing.T) {
	fs := activityReplayFS(t)
	cfg := replayFixtureConfig(t, gameplay.Modern)
	recorded := replayFixtureBattle(t, fs, cfg, false)
	live := replayFixtureBattle(t, fs, cfg, false)
	played := replayFixtureBattle(t, fs, cfg, true)
	tape := &replayTape{t: t}
	if err := recorded.SetReplayRecorder(tape); err != nil {
		t.Fatal(err)
	}
	h := replayUnits(recorded, recorded.LocalOwner)[0]
	if u := recorded.Units.Unit(h); !economyUpkeepCandidate(u) || !u.Activated {
		t.Fatal("fixture is not an active upkeep building")
	}
	hold := HumanCommand{Kind: HumanEconomyActivity, EconomyActivity: HumanEconomyActivityCommand{Activity: economy.ActivityUpkeep, Pause: true}}
	for _, s := range []*Session{recorded, live} {
		if err := s.EnqueueHumanCommand(hold); err != nil {
			t.Fatal(err)
		}
	}
	pump := func() {
		t.Helper()
		from := len(tape.pumps)
		recorded.ExecuteStep(StepPlan{run: true, ticks: 1})
		live.ExecuteStep(StepPlan{run: true, ticks: 1})
		replayPlayPartial(t, played, tape, from)
	}
	pump()
	for range 200 {
		pump()
	}
	if !recorded.Econ.ActivityHeld(recorded.LocalOwner, economy.ActivityUpkeep) || recorded.Units.Unit(h).Activated {
		t.Fatal("hold resumed without a Play command")
	}
	before := recorded.Units.Unit(h).GetScript().DebugSnapshot().Statics
	stock, sim, crt := recorded.Econ.Players, *recorded.SimRNG(), *recorded.CrtRNG()
	recorded.SetPaused(true)
	recorded.ExecuteStep(StepPlan{run: true})
	if !recorded.Econ.ActivityHeld(recorded.LocalOwner, economy.ActivityUpkeep) {
		t.Fatal("idle paused pump advanced the hold")
	}
	resume := HumanCommand{Kind: HumanEconomyActivity, EconomyActivity: HumanEconomyActivityCommand{Activity: economy.ActivityUpkeep}}
	off := HumanCommand{Kind: HumanActivation, Activation: HumanActivationCommand{Unit: h}}
	for _, s := range []*Session{recorded, live} {
		if err := s.EnqueueHumanCommand(resume); err != nil {
			t.Fatal(err)
		}
		if err := s.EnqueueHumanCommand(off); err != nil {
			t.Fatal(err)
		}
	}
	recorded.ExecuteStep(StepPlan{run: true})
	if !recorded.Units.Unit(h).Activated || recorded.Econ.HoldActive[recorded.LocalOwner][economy.ActivityUpkeep] {
		t.Fatal("paused phase 1 did not resume upkeep before the queued off intent")
	}
	if !reflect.DeepEqual(stock, recorded.Econ.Players) || sim != *recorded.SimRNG() || crt != *recorded.CrtRNG() {
		t.Fatal("paused resume spent resources or RNG")
	}
	recorded.SetPaused(false)
	for range 2 { // phase 2 drains each callback before phase 5 pumps its next edge
		for _, s := range []*Session{recorded, live, played} {
			replayRequestCheckpoint(t, s)
		}
		pump()
		replayCompare(t, "paused resume/live", recorded, live)
		replayCompare(t, "paused resume/replay", recorded, played)
		for _, s := range []*Session{live, played} {
			if !reflect.DeepEqual(recorded.Econ.Players, s.Econ.Players) || !reflect.DeepEqual(recorded.Units.Unit(h).GetScript().DebugSnapshot(), s.Units.Unit(h).GetScript().DebugSnapshot()) {
				t.Fatal("paused resume changed resources or activation callbacks")
			}
		}
	}
	after := recorded.Units.Unit(h).GetScript().DebugSnapshot().Statics
	if recorded.Units.Unit(h).Activated || len(after) != 2 || after[0] != before[0]+1 || after[1] != before[1]+1 {
		t.Fatalf("resume and manual off did not each run one callback: before %v, after %v", before, after)
	}
}

func TestEconomyActivitySaveLoadsFreshWithoutChangingLiveState(t *testing.T) {
	fs := activityReplayFS(t)
	s := replayFixtureBattle(t, fs, replayFixtureConfig(t, gameplay.Modern), false)
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanDeveloperSpawn, DeveloperSpawn: HumanDeveloperSpawnCommand{Pattern: "portarm", X: 200 << 16, Z: 160 << 16}}); err != nil {
		t.Fatal(err)
	}
	s.ExecuteStep(StepPlan{run: true, ticks: 3})
	own := replayUnits(s, s.LocalOwner)
	if len(own) != 2 {
		t.Fatalf("local units %v", own)
	}
	for _, h := range own {
		if u := s.Units.Unit(h); !economyUpkeepCandidate(u) || !u.Activated {
			t.Fatal("fixture is not an active upkeep building")
		}
	}
	// Save with Deactivate still pending, proving the projection preserves it
	// and queues Activate after it without executing either callback live.
	s.SetPaused(true)
	for a := economy.ActivityGeneration; a < economy.ActivityCount; a++ {
		if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanEconomyActivity, EconomyActivity: HumanEconomyActivityCommand{Activity: a, Pause: true}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanActivation, Activation: HumanActivationCommand{Unit: own[1]}}); err != nil {
		t.Fatal(err)
	}
	s.ExecuteStep(StepPlan{run: true})
	position := CheckpointPosition{Tick: s.Clock.GlobalTick, Boundary: CheckpointFinalPumpTick}
	before, err := s.captureCheckpoint(s.checkpoints.keys, position, nil)
	if err != nil {
		t.Fatal(err)
	}
	sim, crt := *s.SimRNG(), *s.CrtRNG()
	first := s.Units.Unit(own[0]).GetScript().DebugSnapshot()
	second := s.Units.Unit(own[1]).GetScript().DebugSnapshot()
	in, err := s.RetailBattleSaveInputs(RetailBattleSummary(s, "activity", "0", s.Skirmish.UnitLimit), save.Camera{})
	if err != nil {
		t.Fatal(err)
	}
	if len(in.ResumeEconomyUpkeep) != 1 || in.ResumeEconomyUpkeep[0] != s.Units.Reference(own[0]) {
		t.Fatalf("save must resume only retained intent: %v", in.ResumeEconomyUpkeep)
	}
	projection, err := s.RetailProjection(in)
	if err != nil {
		t.Fatal(err)
	}
	data, err := projection.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.captureCheckpoint(s.checkpoints.keys, position, nil)
	if err != nil || before.Digests != after.Digests || sim != *s.SimRNG() || crt != *s.CrtRNG() ||
		!reflect.DeepEqual(first, s.Units.Unit(own[0]).GetScript().DebugSnapshot()) || !reflect.DeepEqual(second, s.Units.Unit(own[1]).GetScript().DebugSnapshot()) {
		t.Fatalf("save changed live checkpoint, callbacks or RNG: %v", err)
	}
	bank, err := save.OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadRetailSaveWithDeps(bank, RetailLoadDeps{FS: fs, Catalog: s.Catalog, Gameplay: gameplay.Modern, UnitLimit: s.Skirmish.UnitLimit, SimSeed: 7, CRTSeed: 11})
	if err != nil {
		t.Fatal(err)
	}
	dst := loaded.Battle.Session
	if dst.Econ.HoldActive != ([10][economy.ActivityCount]bool{}) || len(dst.economyUpkeepRestore) != 0 {
		t.Fatal("load retained an activity hold")
	}
	for i, h := range own {
		u := dst.Units.Unit(loaded.Battle.StableUnit[uint16(h)])
		if u == nil || u.Activated != (i == 0) {
			t.Fatalf("loaded unit %d activation does not preserve manual intent", i)
		}
		// A load's normal entry prime may have already drained the deferred
		// scripts. One more normal bridge drain also covers an unticked load.
		u.COBBinding().Callbacks.Drain(1)
		got := u.GetScript().DebugSnapshot().Statics
		want := append([]int32(nil), first.Statics...)
		if i == 1 {
			want = append([]int32(nil), second.Statics...)
		} else {
			want[0]++
		}
		want[1]++
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("loaded unit %d callbacks %v, want %v", i, got, want)
		}
	}
}
