package economy

import (
	"bytes"
	"math"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func activityUnit(t *testing.T, w *units.World, def *content.UnitDef) *units.Unit {
	t.Helper()
	def.MaxDamage, def.Limit = 100, -1
	h, err := w.Create(economyFixtureDef(def), 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	u := w.Unit(h)
	u.Remaining, u.Activated = 0, true
	return u
}

// The report observes stored ledger effects. In particular, denied demand
// remains requested and immediate payment never becomes deferred carry.
func TestActivityReportPreservesSettlementAndDemand(t *testing.T) {
	run := func(enabled bool) (*Service, TraceSnapshot, rng.Simulation) {
		s := &Service{}
		if enabled {
			s.EnableActivityReport()
		}
		p := settlingPlayer(s, 0)
		p.Stock = [2]float32{20, 20}
		w := units.NewSliced(2, nil)
		random := rng.NewSimulation(19)
		w.SetSimulationRNG(&random)
		u := activityUnit(t, w, &content.UnitDef{UnitName: "activity", EnergyMake: 4, MetalMake: 2, EnergyUse: 3, EnergyStorage: 100, MetalStorage: 100})
		b := s.UnitBuckets(u.Handle)
		before := s.ActivitySnapshot(u.Handle)
		if !AdmitTwoResource(b, 6, 2) {
			t.Fatal("initial work refused")
		}
		s.ObserveActivity(u, ActivityConstruction, before)
		b[Energy].Carry = 2
		before = s.ActivitySnapshot(u.Handle)
		if AdmitTwoResource(b, 9, 4) {
			t.Fatal("carry did not refuse later work")
		}
		s.ObserveActivity(u, ActivityConstruction, before)
		before = s.ActivitySnapshot(u.Handle)
		if !ImmediateDebit(p, b, 2, 1) {
			t.Fatal("shot refused")
		}
		s.ObserveActivity(u, ActivityWeapons, before)
		p.Mirror[Metal].Production, p.Mirror[Energy].Requested = 5, 4
		s.SetCloakDue(func(*units.Unit) bool { return true })
		s.SetCloakCost(func(*units.Unit) float32 { return 2 })
		s.Settle(0, 30, w)
		return s, s.ParitySnapshot(w), random
	}
	_, plain, plainRandom := run(false)
	s, observed, observedRandom := run(true)
	if !reflect.DeepEqual(plain, observed) || plainRandom != observedRandom {
		t.Fatal("enabling the report changed accounting or randomness")
	}
	rows := s.AppendActivityReport(nil, 0)
	var totals ActivityContribution
	var byActivity [ActivityCount]ActivityContribution
	for _, row := range rows {
		for r := Metal; r <= Energy; r++ {
			totals.Produced[r] += row.Produced[r]
			totals.Requested[r] += row.Requested[r]
			byActivity[row.Activity].Produced[r] += row.Produced[r]
			byActivity[row.Activity].Requested[r] += row.Requested[r]
		}
		if row.Activity == ActivityConstruction && row.Requested != [2]float32{6, 15} {
			t.Fatalf("construction demand=%v", row.Requested)
		}
		if row.Activity == ActivityWeapons && row.Requested != [2]float32{1, 2} {
			t.Fatalf("weapon demand=%v", row.Requested)
		}
	}
	if byActivity[ActivityConstruction].Requested != [2]float32{6, 15} || byActivity[ActivityWeapons].Requested != [2]float32{1, 2} || byActivity[ActivityGeneration].Produced != [2]float32{2, 4} || byActivity[ActivityUpkeep].Requested[Energy] != 3 || byActivity[ActivityCloaking].Requested[Energy] != 2 {
		t.Fatalf("activity attribution mismatch: %+v", byActivity)
	}
	if totals.Produced != s.Players[0].PassProduced || totals.Requested != s.Players[0].PassConsumed || s.ActivityReportTick(0) != 30 {
		t.Fatalf("report does not reconcile: %+v / %+v", totals, s.Players[0])
	}
	copyBefore := append([]ActivityContribution(nil), rows...)
	rows[0].Requested[0] = 123456
	if !reflect.DeepEqual(s.AppendActivityReport(nil, 0), copyBefore) {
		t.Fatal("caller changed retained report")
	}
}

func TestActivityReportUsesAllocationIdentityAndRestoredResidual(t *testing.T) {
	s := &Service{}
	s.EnableActivityReport()
	w := units.NewSliced(2, nil)
	u := activityUnit(t, w, &content.UnitDef{UnitName: "old", EnergyStorage: 100, MetalStorage: 100})
	oldRef := pool.UnitRef{Handle: u.Handle, Serial: u.AllocationSerial}
	b := s.UnitBuckets(u.Handle)
	before := s.ActivitySnapshot(u.Handle)
	AdmitOneResource(b, 9)
	s.ObserveActivity(u, ActivityHealing, before)
	w.FreeImmediate(u.Handle)
	u = activityUnit(t, w, &content.UnitDef{UnitName: "new", EnergyStorage: 100, MetalStorage: 100})
	if u.Handle != oldRef.Handle || u.AllocationSerial == oldRef.Serial {
		t.Fatal("fixture did not reuse allocation")
	}
	s.InitializeUnitEconomy(u.Handle)
	b = s.UnitBuckets(u.Handle)
	b[Energy].Requested = 7 // restored/unobserved work is not reclassified
	before = s.ActivitySnapshot(u.Handle)
	AdmitOneResource(b, 3)
	s.ObserveActivity(u, ActivityHealing, before)
	s.Settle(0, 30, w)
	var other, healing float32
	for _, row := range s.AppendActivityReport(nil, 0) {
		if row.Unit == oldRef {
			t.Fatal("old allocation survived reuse")
		}
		if row.Activity == ActivityOther {
			other += row.Requested[Energy]
		}
		if row.Activity == ActivityHealing {
			healing += row.Requested[Energy]
		}
	}
	if other != 7 || healing != 3 {
		t.Fatalf("other=%v healing=%v", other, healing)
	}
}

func TestActivityHoldsKeepUpkeepAndOldDebtAndBypassStrict(t *testing.T) {
	for _, modern := range []bool{false, true} {
		s := &Service{Rules: StrictRules{}}
		if modern {
			s.Rules = ModernRules{}
		}
		for _, a := range []Activity{ActivityGeneration, ActivityUpkeep, ActivityCloaking} {
			s.HoldActive[0][a] = true
		}
		w := units.NewSliced(2, nil)
		u := activityUnit(t, w, &content.UnitDef{UnitName: "held", EnergyMake: 10, MetalMake: 4, EnergyUse: 3, EnergyStorage: 100, MetalStorage: 100})
		u.IsCloaked, u.Hidden = true, true
		s.Players[0].Stock = [2]float32{20, 20}
		s.UnitBuckets(u.Handle)[Energy].Carry = 2
		s.SetCloakDue(func(*units.Unit) bool { return true })
		s.SetCloakCost(func(*units.Unit) float32 { return 5 })
		s.Settle(0, 30, w)
		want := [2]float32{4, 10}
		if modern {
			want = [2]float32{}
		}
		if s.Players[0].PassProduced != want {
			t.Fatalf("modern=%v production=%v", modern, s.Players[0].PassProduced)
		}
		wantRequested := float32(8)
		if modern {
			wantRequested = 3
		}
		if s.Players[0].PassConsumed[Energy] != wantRequested || s.UnitBuckets(u.Handle)[Energy].Carry != 0 || u.Hidden == modern || !u.IsCloaked {
			t.Fatalf("modern=%v upkeep/debt/cloak mismatch: %+v hidden=%v request=%v", modern, s.Players[0], u.Hidden, u.IsCloaked)
		}
	}
}

func TestActivityHoldsRequireExplicitResume(t *testing.T) {
	s := &Service{Rules: ModernRules{}}
	w := units.NewSliced(2, nil)
	activityUnit(t, w, &content.UnitDef{UnitName: "held", EnergyMake: 4, EnergyStorage: 100})
	s.HoldActive[0][ActivityGeneration] = true
	for _, tick := range []uint32{0, 30, 180, ^uint32(0), 0} {
		s.Settle(0, tick, w)
		if !s.ActivityHeld(0, ActivityGeneration) || s.Players[0].PassProduced[Energy] != 0 {
			t.Fatal("settlement ended a persistent generation hold")
		}
	}
	s.HoldActive[0][ActivityGeneration] = false
	s.Settle(0, 30, w)
	if s.ActivityHeld(0, ActivityGeneration) || s.Players[0].PassProduced[Energy] != 4 {
		t.Fatal("explicit resume retained a hold")
	}
}

func TestActivityReportsDoNotGrowAuthoritativeBuckets(t *testing.T) {
	s := &Service{}
	s.ActivitySnapshot(500)
	if len(s.unitBuckets) != 0 {
		t.Fatal("observation grew authoritative buckets")
	}
	s.EnableActivityReport()
	s.ActivitySnapshot(500)
	if len(s.unitBuckets) != 0 {
		t.Fatal("enabled observation grew authoritative buckets")
	}
}

func TestActivityReportIsExcludedFromCheckpoint(t *testing.T) {
	s := &Service{}
	u := &units.Unit{Handle: 1, AllocationSerial: 2}
	s.UnitBuckets(1)[Energy].Requested = 4
	before := economyCheckpointBytes(t, s)
	s.EnableActivityReport()
	s.ObserveActivity(u, ActivityHealing, [2]Bucket{})
	if !bytes.Equal(before, economyCheckpointBytes(t, s)) {
		t.Fatal("optional observations changed checkpoint bytes")
	}
}

func TestActivityHoldGenerationPreservesDrainsAndUnorderedValues(t *testing.T) {
	for _, value := range []float32{-4, float32(math.NaN())} {
		s := &Service{Rules: ModernRules{}}
		s.HoldActive[0][ActivityGeneration] = true
		w := units.NewSliced(2, nil)
		u := activityUnit(t, w, &content.UnitDef{UnitName: "drain", EnergyMake: float64(value), MetalMake: 5})
		s.PerUnitProductionFills(0, w)
		b := s.UnitBuckets(u.Handle)
		if b[Metal].Production != 0 {
			t.Fatal("positive generation was not held")
		}
		if value == value && b[Energy].Production != value {
			t.Fatal("generation hold waived authored drain")
		}
		if value != value && b[Energy].Production == b[Energy].Production {
			t.Fatal("generation hold sanitized unordered contribution")
		}
	}
}
