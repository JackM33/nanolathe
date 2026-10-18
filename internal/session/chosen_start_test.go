package session

import (
	"bytes"
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

// Chosen start points (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern chosen
// start positions"): a lobby choice per player, a Nanolathe Modern policy
// that Strict 3.1 ignores. A setup with no choice must place exactly as
// retail does; the others lock the policy's claiming and ordering rules.

// chosenStarts are stored numbers 0..5 a thousand pixels apart on one line,
// authored out of order so the assignment's own ordering is what puts them
// in ascending order.
func chosenStarts() []mission.Special {
	var starts []mission.Special
	for _, id := range []int{3, 0, 5, 1, 4, 2} {
		starts = append(starts, mission.Special{Kind: 1, ID: int32(id), X: int16(500 + 1000*id), Z: 700})
	}
	return starts
}

// chosenStartConfig is a setup of len(points) players; a nil point leaves
// the player to Location.
func chosenStartConfig(location int, points ...*[2]int32) SkirmishConfig {
	cfg := SkirmishConfig{NumPlayers: len(points), Location: location, RNGCrtSeed: 67890, MapName: "fixture"}
	for i, p := range points {
		if p != nil {
			cfg.Players[i].Start = StartPoint{Set: true, X: p[0], Z: p[1]}
		}
	}
	return cfg
}

func eligibleSlots(n int) []int {
	slots := make([]int, n)
	for i := range slots {
		slots[i] = i
	}
	return slots
}

// With no choice the assignment is retail's: the identity under fixed starts
// with no CRT draw, and the loading walk's cyclic permutation under random
// starts with exactly its draws.
func TestUnchosenStartsKeepRetailPlacement(t *testing.T) {
	for _, location := range []int{1, 0} {
		cfg := chosenStartConfig(location, nil, nil, nil, nil)
		crt := rng.NewCRT(cfg.RNGCrtSeed)
		got, points, _, err := skirmishStartAssignment(cfg, eligibleSlots(4), chosenStarts(), &crt, true, false)
		if err != nil || len(points) != 0 {
			t.Fatalf("location %d: %v, points %v", location, err, points)
		}
		want := eligibleSlots(4)
		control := rng.NewCRT(cfg.RNGCrtSeed)
		if location == 0 {
			shuffleEligibleStarts(&control, want)
		}
		for slot, start := range want {
			if got[slot] != start {
				t.Fatalf("location %d: slot %d took %d, want retail's %d [P0-04]", location, slot, got[slot], start)
			}
		}
		if crt.Draws() != control.Draws() {
			t.Fatalf("location %d: %d CRT draws, want retail's %d", location, crt.Draws(), control.Draws())
		}
	}
}

// Under fixed starts a chosen point claims every authored start within
// chosenStartClaim of it; every other player keeps its own number when that
// start is free, and the rest take the lowest free starts in slot order.
// Here slot 2 chooses a point beside stored start 0, so slots 1 and 3 keep
// their own, and slot 0, whose own was claimed, takes the lowest free start,
// stored 2. No CRT draw is taken.
func TestChosenPointsUnderFixedLocation(t *testing.T) {
	cfg := chosenStartConfig(1, nil, nil, &[2]int32{520, 760}, nil)
	crt := rng.NewCRT(cfg.RNGCrtSeed)
	got, points, _, err := skirmishStartAssignment(cfg, eligibleSlots(4), chosenStarts(), &crt, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := points[2]; !ok || p != [2]int32{520, 760} || len(points) != 1 {
		t.Fatalf("points %v, want slot 2's own", points)
	}
	if _, ok := got[2]; ok {
		t.Fatalf("slot 2 also took an authored start: %v", got)
	}
	for slot, want := range map[int]int{0: 2, 1: 1, 3: 3} {
		if got[slot] != want {
			t.Fatalf("slot %d took %d, want %d (assignment %v)", slot, got[slot], want, got)
		}
	}
	if crt.Draws() != 0 {
		t.Fatalf("fixed starts took %d CRT draws, want none", crt.Draws())
	}
}

// Under random starts the players who chose nothing are dealt the lowest
// free starts, one each, by a Fisher–Yates shuffle on the loading CRT. The
// expectation replays the shuffle from the stream definition [01 §7.2].
// Slot 0's point is far from every start, so it claims none.
func TestChosenPointsUnderRandomLocation(t *testing.T) {
	cfg := chosenStartConfig(0, &[2]int32{9000, 9000}, nil, nil, nil)
	crt := rng.NewCRT(cfg.RNGCrtSeed)
	got, _, _, err := skirmishStartAssignment(cfg, eligibleSlots(4), chosenStarts(), &crt, true, false)
	if err != nil {
		t.Fatal(err)
	}
	pool := []int{0, 1, 2}
	state := cfg.RNGCrtSeed
	for k := len(pool) - 1; k > 0; k-- {
		state = state*214013 + 2531011
		r := int((state>>16)&0x7FFF) % (k + 1)
		pool[k], pool[r] = pool[r], pool[k]
	}
	for i, slot := range []int{1, 2, 3} {
		if got[slot] != pool[i] {
			t.Fatalf("slot %d took %d, want %d (assignment %v, dealt %v)", slot, got[slot], pool[i], got, pool)
		}
	}
	if crt.Draws() != 2 {
		t.Fatalf("dealing three starts took %d CRT draws, want 2", crt.Draws())
	}
}

// Too few starts left after the chosen points is this build's error, and a
// point outside the coordinate bound is refused by Validate.
func TestChosenPointsRefuseWhatTheMapCannotSeat(t *testing.T) {
	starts := chosenStarts()[:4] // stored 3, 0, 5, 1
	crt := rng.NewCRT(1)
	cfg := chosenStartConfig(1, &[2]int32{500, 700}, nil, nil, nil, nil)
	if _, _, _, err := skirmishStartAssignment(cfg, eligibleSlots(5), starts, &crt, true, false); err == nil || !strings.Contains(err.Error(), "starts left after the chosen points") {
		t.Fatalf("four players on three free starts: %v", err)
	}
	if err := validateChosenStarts(chosenStartConfig(1, &[2]int32{StartPointLimit, 0}, nil)); err == nil {
		t.Fatal("Validate accepted a point beyond the coordinate bound")
	}
}

// Battle entry places a chosen commander on the nearest cell it can stand
// on, here the chosen cell itself on flat open ground, and gives the other
// player its own authored start, which the far point does not claim.
func TestChosenPointPlacesTheCommander(t *testing.T) {
	s := strictNewSessionWithUnits(t, 0, 7, 11)
	m := wu19178Mission(
		mission.Special{Kind: 1, ID: 0, X: 12, Z: 20, Name: "StartPos1"},
		mission.Special{Kind: 1, ID: 1, X: 24, Z: 28, Name: "StartPos2"},
	)
	cfg := wu19178Config()
	cfg.Players[0].Start = StartPoint{Set: true, X: 400, Z: 410}
	s.Rules.Seats = ModernSeats{}
	if err := skirmishReconstructUnits(s, cfg, m); err != nil {
		t.Fatalf("skirmishReconstructUnits: %v", err)
	}
	want := map[uint8][2]int32{0: {25*16 + 8, 25*16 + 8}, 1: {24, 28}}
	for _, u := range s.Units.Iter() {
		w := want[u.Owner]
		if u.X != numeric.Fixed(w[0])<<16 || u.Z != numeric.Fixed(w[1])<<16 {
			t.Fatalf("player %d commander at (%d,%d), want (%d,%d)", u.Owner, u.X>>16, u.Z>>16, w[0], w[1])
		}
	}
}

// Strict 3.1 keeps retail's placement: a chosen point is ignored, with no
// CRT draw beyond retail's, and the configuration refuses one
// (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern chosen start positions").
func TestStrictIgnoresChosenStarts(t *testing.T) {
	if (StrictSeats{}).ChosenStarts() || !(ModernSeats{}).ChosenStarts() || !CommunityRuleSet().Seats.ChosenStarts() {
		t.Fatal("the reserved sets answer the chosen-start question wrongly")
	}
	for _, location := range []int{1, 0} {
		cfg := chosenStartConfig(location, &[2]int32{520, 700}, nil, nil, nil)
		crt := rng.NewCRT(cfg.RNGCrtSeed)
		got, points, _, err := skirmishStartAssignment(cfg, eligibleSlots(4), chosenStarts(), &crt, false, false)
		if err != nil || len(points) != 0 {
			t.Fatalf("location %d: %v, points %v", location, err, points)
		}
		control := rng.NewCRT(cfg.RNGCrtSeed)
		want := eligibleSlots(4)
		if location == 0 {
			shuffleEligibleStarts(&control, want)
		}
		for slot, start := range want {
			if got[slot] != start || crt.Draws() != control.Draws() {
				t.Fatalf("location %d: slot %d took %d after %d draws, want retail's %d after %d", location, slot, got[slot], crt.Draws(), start, control.Draws())
			}
		}
	}
	r := validMatchRequest(t)
	r.RuleName, r.RuleBase = StrictRuleSetName, MatchBaseStrict31
	r.Community = community.Features{}
	r.Mod, r.ContentProfile = MatchMod{}, MatchContentProfile{Name: "retail", Units: 512, Weapons: 256, TNTBytes: 16 << 20, LOSBytes: 1 << 20}
	r.Seats[2].ComputerKind, r.Seats[2].AIParams = 0, nil
	if _, err := ResolveMatchConfig(r); err != nil {
		t.Fatalf("the fixture is not a Strict configuration: %v", err)
	}
	r.Seats[0].Start = StartPoint{Set: true, X: 100, Z: 100}
	if _, err := ResolveMatchConfig(r); err == nil {
		t.Fatal("a Strict 3.1 configuration carried a chosen start")
	}
}

// Version 2 of the configuration: a request with no chosen point keeps its
// version-1 bytes and identity (TestMatchConfigEncodingIsUnchangedBySharedPrimitives
// holds them); one with a chosen point appends each seat's flag, the set
// seats' coordinates and field 17, and hashes under the version-2 domain;
// and a version-2 tail of defaults is refused.
func TestChosenStartsEncodeAsConfigurationVersion2(t *testing.T) {
	r := validMatchRequest(t)
	v1 := encodeMatchForTest(t, resolveMatch(t, r))
	r.Seats[0].Start = StartPoint{Set: true, X: 3, Z: 4}
	r.Seats[2].Start = StartPoint{Set: true, X: 1, Z: 2}
	c := resolveMatch(t, r)
	v2 := encodeMatchForTest(t, c)
	if tail := []byte{1, 3, 4, 0, 1, 1, 2, 0}; !bytes.Equal(v2[:len(v1)], v1) || !bytes.Equal(v2[len(v1):], tail) {
		t.Fatalf("version 2 is not version 1, each seat's start and field 17:\nv1 %x\nv2 %x", v1, v2)
	}
	h := sha256.New()
	h.Write([]byte(matchConfigDomainV2))
	h.Write(v2)
	if digest := c.Digest(); !bytes.Equal(digest[:], h.Sum(nil)) {
		t.Fatalf("version 2 identity %x does not hash the version-2 domain", digest)
	}
	if !checkMatchDecode(t, v2) {
		t.Fatal("a version-2 payload did not decode")
	}
	decoded, _ := DecodeMatchConfig(v2)
	if got := decoded.Request().Seats; got[0].Start != r.Seats[0].Start || got[1].Start.Set || got[2].Start != r.Seats[2].Start {
		t.Fatalf("decoded starts %+v %+v %+v", got[0].Start, got[1].Start, got[2].Start)
	}
	if checkMatchDecode(t, append(append([]byte(nil), v1...), 0, 0, 0, 0)) {
		t.Fatal("a version-2 tail of defaults decoded")
	}
	s := validSurvivalRequest(t)
	s.Seats[0].Start = StartPoint{Set: true, X: 1, Z: 1}
	if _, err := ResolveMatchConfig(s); err == nil {
		t.Fatal("a Survival seat with a chosen start resolved")
	}
}

// The local adapter and the admitted setup carry each row's choice, so a
// replay composes the battle its recording played.
func TestChosenStartsRoundTripTheLocalAdapter(t *testing.T) {
	cfg := DirectSkirmishConfig("fixture")
	cfg.Players[1].Start = StartPoint{Set: true, X: 640, Z: 320}
	r, err := NewMatchConfigRequest(cfg, SkirmishEntryOptions{}, MatchRoomInputs{Policies: MatchPolicies{Revision: 1, Scheduling: 1, Pacing: 1, Drop: 1, Audience: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Seats[0].Start.Set || r.Seats[1].Start != cfg.Players[1].Start {
		t.Fatalf("request starts %+v %+v", r.Seats[0].Start, r.Seats[1].Start)
	}
	back, _ := matchSkirmishSetup(&r)
	if back.Players[0].Start.Set || back.Players[1].Start != cfg.Players[1].Start {
		t.Fatalf("admitted setup starts %+v %+v", back.Players[0].Start, back.Players[1].Start)
	}
}
