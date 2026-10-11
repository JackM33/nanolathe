package economy

import (
	"bytes"
	"math"
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The four named toggles flip one bit each; ShareAll is the four toggles in
// sequence, so it flips rather than sets [07 R-CAM-01 §6].
func TestShareOptionsToggleAndShareAll(t *testing.T) {
	var s Service
	if !s.SetShareOption(3, ShareOptionMetal, true) || !s.Players[3].AutoShareMetal {
		t.Fatal("SetShareOption did not set the metal bit")
	}
	if on, ok := s.ToggleShareOption(3, ShareOptionRadar); !ok || !on || !s.Players[3].ShareRadar {
		t.Fatal("ToggleShareOption did not raise the radar bit")
	}
	if !s.ToggleShareAll(3) {
		t.Fatal("ToggleShareAll refused a valid slot")
	}
	p := &s.Players[3]
	if p.AutoShareMetal || !p.AutoShareEnergy || !p.AutoShareSensor || p.ShareRadar {
		t.Fatalf("ShareAll from metal+radar = metal %t energy %t mapping %t radar %t, want each flipped",
			p.AutoShareMetal, p.AutoShareEnergy, p.AutoShareSensor, p.ShareRadar)
	}
	if s.SetShareOption(10, ShareOptionMetal, true) || s.SetShareOption(0, 3, true) || s.ToggleShareAll(-1) {
		t.Fatal("an invalid slot or the dead ShareLOS bit was accepted")
	}
	if s.ShareOptionOn(0, ShareOptionMetal) || !s.ShareOptionOn(3, ShareOptionMapping) {
		t.Fatal("ShareOptionOn disagrees with the record")
	}
}

// The threshold is the argument when it is not above capacity, else the
// capacity; a negative argument is stored as it is [05 R-SHARE-01 §3]
// [07 R-CAM-01 §6].
func TestSetShareThreshold(t *testing.T) {
	var s Service
	s.Players[1].Capacity = [2]float32{500, 750}
	for _, tc := range []struct {
		res  Res
		n    int32
		want float32
	}{
		{Metal, 200, 200}, {Metal, 500, 500}, {Metal, 501, 500}, {Metal, -40, -40},
		{Energy, 750, 750}, {Energy, 1 << 30, 750},
	} {
		got, ok := s.SetShareThreshold(1, tc.res, tc.n)
		stored := s.Players[1].MetalShareThreshold
		if tc.res == Energy {
			stored = s.Players[1].EnergyShareThreshold
		}
		if !ok || got != tc.want || stored != tc.want {
			t.Fatalf("res %d n %d: got %v stored %v, want %v", tc.res, tc.n, got, stored, tc.want)
		}
	}
	if _, ok := s.SetShareThreshold(10, Metal, 1); ok {
		t.Fatal("an invalid slot was accepted")
	}
	// An unordered compare keeps the argument: a NaN capacity stores n
	// [05 R-SHARE-01 §3].
	s.Players[2].Capacity[Metal] = float32(math.NaN())
	if got, ok := s.SetShareThreshold(2, Metal, 300); !ok || got != 300 || s.Players[2].MetalShareThreshold != 300 {
		t.Fatalf("NaN capacity stored %v, want the argument 300", got)
	}
}

// The radar mask reads the owner's declaration toward the viewer and the
// owner's radar bit, never the viewer's declaration [03 R-VIS-01 §7].
func TestRadarShareOwners(t *testing.T) {
	var s Service
	s.Players[1].ShareRadar, s.Players[1].Allies[0] = true, true // shares, declared toward 0
	s.Players[2].ShareRadar = true                               // shares, undeclared
	s.Players[3].Allies[0] = true                                // declared, not sharing
	s.Players[4].ShareRadar, s.Players[0].Allies[4] = true, true // only the viewer declared
	s.Players[0].ShareRadar, s.Players[0].Allies[0] = true, true // the viewer itself
	if got := s.RadarShareOwners(0); got != 1<<1 {
		t.Fatalf("mask = %#b, want only owner 1", got)
	}
	if s.RadarShareOwners(10) != 0 {
		t.Fatal("an invalid viewer read a mask")
	}
}

// shareSeatFixture makes the given seats human records with room to receive.
func shareSeatFixture(seats ...int) *Service {
	s := &Service{}
	for _, seat := range seats {
		activePlayer(&s.Players[seat])
	}
	return s
}

func seatMask(seats ...int) uint16 {
	var m uint16
	for _, seat := range seats {
		m |= 1 << uint(seat)
	}
	return m
}

// The resource pass: metal at one third, energy at one half of the stock
// above the threshold, limited by the recipient's capacity gap; the source
// gate and the candidate's stock test are both strict [05 R-SHARE-01 §3].
func TestShareTickSeatsRatiosAndStrictness(t *testing.T) {
	s := shareSeatFixture(0, 1)
	src, dst := &s.Players[0], &s.Players[1]
	src.AutoShareMetal, src.AutoShareEnergy, src.Allies[1] = true, true, true
	src.Stock = [2]float32{300, 300}
	dst.Capacity = [2]float32{1000, 120}
	s.ShareTickSeats(60, nil, seatMask(0, 1), nil)
	// Metal: 300 × 0.33333334 rounds to 100. Energy: 300 × 0.5 = 150, held
	// to the recipient's gap of 120.
	if src.Stock != [2]float32{200, 180} || dst.Mirror[Metal].Production != 100 || dst.Mirror[Energy].Production != 120 {
		t.Fatalf("source %v, recipient production %v/%v; want 200/180 and 100/120",
			src.Stock, dst.Mirror[Metal].Production, dst.Mirror[Energy].Production)
	}

	// Stock equal to the threshold does not pass the source gate.
	s = shareSeatFixture(0, 1)
	s.Players[0].AutoShareMetal, s.Players[0].Allies[1] = true, true
	s.Players[0].Stock[Metal], s.Players[0].MetalShareThreshold = 300, 300
	s.ShareTickSeats(60, nil, seatMask(0, 1), nil)
	if s.Players[0].Stock[Metal] != 300 {
		t.Fatal("a stock equal to the threshold shared")
	}
	// A candidate whose stock equals the giver's is not lower.
	s.Players[0].MetalShareThreshold = 0
	s.Players[1].Stock[Metal] = 300
	s.ShareTickSeats(60, nil, seatMask(0, 1), nil)
	if s.Players[0].Stock[Metal] != 300 {
		t.Fatal("a candidate with equal stock received")
	}
	// Off the 60-tick cadence nothing moves.
	s.Players[1].Stock[Metal] = 0
	s.ShareTickSeats(59, nil, seatMask(0, 1), nil)
	if s.Players[0].Stock[Metal] != 300 {
		t.Fatal("a non-multiple of 60 shared")
	}
}

// Several recipients: the last qualifying slot wins, and only humans the
// giver declared toward, that are not eliminated, qualify [05 R-SHARE-01 §3].
func TestShareTickSeatsLastCandidateWins(t *testing.T) {
	s := shareSeatFixture(0, 2, 4, 6, 8)
	src := &s.Players[0]
	src.AutoShareMetal = true
	src.Stock[Metal] = 300
	src.Allies[2], src.Allies[4], src.Allies[8] = true, true, true
	// Seat 6 is a human the giver never declared toward; seat 8 is an allied
	// computer, which is no seat in humans; seat 4 is eliminated.
	s.Players[8].ControllerState = 2
	w := units.NewSliced(4, &content.Catalog{})
	def := economyFixtureDef(&content.UnitDef{UnitName: "armcom", BuildTime: 100, MaxDamage: 100})
	def.CanonicalKey = content.CanonicalKey("armcom")
	h, err := w.Create(def, 4, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	if err != nil {
		t.Fatal(err)
	}
	w.Unit(h).Dying = true
	w.FinalizeDeath(h, 30)
	if !PlayerEliminated(w, 4) {
		t.Fatal("fixture seat 4 is not eliminated")
	}
	s.ShareTickSeats(60, w, seatMask(0, 2, 4, 6), nil)
	if s.Players[2].Mirror[Metal].Production != 100 {
		t.Fatalf("seat 2 production = %v, want the whole share", s.Players[2].Mirror[Metal].Production)
	}
	for _, seat := range []int{4, 6, 8} {
		if s.Players[seat].Mirror[Metal].Production != 0 {
			t.Fatalf("seat %d received", seat)
		}
	}
	// With seat 4 alive again (a fresh world), the later slot wins.
	s.Players[0].Stock[Metal] = 300
	s.Players[2].Mirror[Metal].Production = 0
	s.ShareTickSeats(120, nil, seatMask(0, 2, 4, 6), nil)
	if s.Players[4].Mirror[Metal].Production != 100 || s.Players[2].Mirror[Metal].Production != 0 {
		t.Fatalf("seat 2/4 production = %v/%v, want the last candidate, seat 4",
			s.Players[2].Mirror[Metal].Production, s.Players[4].Mirror[Metal].Production)
	}
}

// Seats run in ascending order, each seeing the stock an earlier seat moved:
// seat 0's gift lowers its stock below seat 1's, so seat 1 then gives back.
// Run the other way round, seat 1 would find no lower ally first
// (DESIGN_MULTIPLAYER §6.3 "Every human seat, ascending").
func TestShareTickSeatsAscendingSeats(t *testing.T) {
	s := shareSeatFixture(0, 1)
	a, b := &s.Players[0], &s.Players[1]
	a.AutoShareMetal, b.AutoShareMetal = true, true
	a.Allies[1], b.Allies[0] = true, true
	a.Stock[Metal], b.Stock[Metal] = 600, 500
	s.ShareTickSeats(60, nil, seatMask(0, 1), nil)
	// Seat 0 gives 600 × 0.33333334 = 200 and keeps 400; seat 1 then gives
	// 500 × 0.33333334 to seat 0.
	wantB := float32(float64(500) * float64(float32(0.33333334)))
	if a.Stock[Metal] != 400 || b.Mirror[Metal].Production != 200 || a.Mirror[Metal].Production != wantB || b.Stock[Metal] != 500-wantB {
		t.Fatalf("seat 0 stock %v production %v; seat 1 stock %v production %v",
			a.Stock[Metal], a.Mirror[Metal].Production, b.Stock[Metal], b.Mirror[Metal].Production)
	}
}

// No gate stands between the amount and the signed transfer helper: a
// recipient holding more than its capacity makes a negative gap, and the
// giver's stock rises [05 R-SHARE-01 §2] [05 R-SHARE-01 §3].
func TestShareTickSeatsNegativeGapReachesTheHelper(t *testing.T) {
	s := shareSeatFixture(0, 1)
	src, dst := &s.Players[0], &s.Players[1]
	src.AutoShareMetal, src.Allies[1] = true, true
	src.Stock[Metal] = 900
	dst.Stock[Metal], dst.Capacity[Metal] = 500, 400
	s.ShareTickSeats(60, nil, seatMask(0, 1), nil)
	if src.Stock[Metal] != 1000 || dst.Mirror[Metal].Production != -100 {
		t.Fatalf("giver stock %v, recipient production %v; want 1000 and -100", src.Stock[Metal], dst.Mirror[Metal].Production)
	}
}

// The mapping pass asks every qualifying human, ascending, on multiples of
// 450 only; the first is tick 450. Seats run in order, so seat 0's requests
// precede seat 2's [05 R-SHARE-01 §3] (Q25, DESIGN_MULTIPLAYER §6.7).
func TestShareTickSeatsMappingRequests(t *testing.T) {
	s := shareSeatFixture(0, 1, 2, 3, 5)
	s.Players[0].AutoShareSensor = true
	s.Players[0].Allies[1], s.Players[0].Allies[2], s.Players[0].Allies[5] = true, true, true
	s.Players[2].AutoShareSensor = true
	s.Players[2].Allies[0] = true
	s.Players[5].ControllerState = 2 // an allied computer is never asked
	humans := seatMask(0, 1, 2, 3)
	var got [][2]int
	record := func(source, recipient int) { got = append(got, [2]int{source, recipient}) }
	for _, tick := range []uint32{60, 449, 451} {
		s.ShareTickSeats(tick, nil, humans, record)
	}
	if len(got) != 0 {
		t.Fatalf("requests off the 450-tick cadence: %v", got)
	}
	s.ShareTickSeats(450, nil, humans, record)
	want := [][2]int{{0, 1}, {0, 2}, {2, 0}}
	if !slices.Equal(got, want) {
		t.Fatalf("requests = %v, want %v", got, want)
	}
	// No stock moved: the pass carries no resources.
	if s.Players[0].Stock != s.Players[1].Stock {
		t.Fatal("the mapping pass moved stock")
	}
}

// Single-player checkpoint bytes do not move: a ledger in which no player
// shares radar writes what it always has, and the bit is announced by the
// tag only when some player sets it.
func TestCheckpointShareRadarIsTagged(t *testing.T) {
	s := &Service{}
	s.Players[2].AutoShareMetal = true
	baseline := economyCheckpointBytes(t, s)
	s.Players[7].ShareRadar = true
	tagged := economyCheckpointBytes(t, s)
	if bytes.Equal(baseline, tagged) {
		t.Fatal("the share-radar bit vanished from the checkpoint")
	}
	// Ten rows each gain one Boolean.
	if len(tagged) != len(baseline)+10 {
		t.Fatalf("tagged payload grew by %d bytes, want 10", len(tagged)-len(baseline))
	}
	s.Players[7].ShareRadar = false
	if !bytes.Equal(baseline, economyCheckpointBytes(t, s)) {
		t.Fatal("clearing the bit did not restore the untagged bytes")
	}
}

// The excess and its product stay at working precision until the chosen
// amount is stored [05 R-SHARE-01 §3] step 3 [01 §8]. A negative threshold
// makes an excess single precision cannot hold, which separates this from an
// all-single evaluation.
func TestShareTickSeatsWorkingPrecision(t *testing.T) {
	s := shareSeatFixture(0, 1)
	src := &s.Players[0]
	src.AutoShareMetal, src.Allies[1] = true, true
	src.Stock[Metal] = float32(601) * 0.37
	src.MetalShareThreshold = -100
	stock := src.Stock[Metal]
	k := float32(0.33333334)
	working := float32((float64(stock) + 100) * float64(k))
	single := float32(float32(stock+100) * k)
	if working == single || working > stock {
		t.Fatal("fixture does not separate the two precisions below the stock cap")
	}
	s.ShareTickSeats(60, nil, seatMask(0, 1), nil)
	if got := s.Players[1].Mirror[Metal].Production; got != working {
		t.Fatalf("credit = %v, want the working-precision amount %v (all-single gives %v)", got, working, single)
	}
}
