package session

import (
	"bytes"
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

// Chosen start positions (docs/DESIGN_SESSIONS_AI_SAVE.md "Chosen start
// positions"): a lobby choice per player, Nanolathe policy in every mode. A
// setup with no choice must place exactly as retail does; the others lock the
// policy's ordering rules.

// chosenStarts are stored numbers 0..5, authored out of order so the
// assignment's own ordering is what puts them in ascending order.
func chosenStarts() []mission.Special {
	var starts []mission.Special
	for _, id := range []int{3, 0, 5, 1, 4, 2} {
		starts = append(starts, mission.Special{Kind: 1, ID: int32(id), X: int16(10 + 10*id), Z: int16(20 + 10*id)})
	}
	return starts
}

func chosenStartConfig(location int, starts ...int) SkirmishConfig {
	cfg := SkirmishConfig{NumPlayers: len(starts), Location: location, RNGCrtSeed: 67890, MapName: "fixture"}
	for i, start := range starts {
		cfg.Players[i].Start = start
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
		cfg := chosenStartConfig(location, 0, 0, 0, 0)
		crt := rng.NewCRT(cfg.RNGCrtSeed)
		got, chosen, err := skirmishStartAssignment(cfg, eligibleSlots(4), chosenStarts(), &crt)
		if err != nil || chosen != 0 {
			t.Fatalf("location %d: %v, %d chosen", location, err, chosen)
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

// Under fixed starts a chosen start is taken first, every other player keeps
// its own number when that start is free, and the rest take the lowest free
// starts in slot order. Here slot 2 chooses StartPos1 (stored 0), so slots 1
// and 3 keep their own, and slot 0, whose own was taken, gets the lowest
// free start — stored 2, slot 2's own. No CRT draw is taken.
func TestChosenStartsUnderFixedLocation(t *testing.T) {
	cfg := chosenStartConfig(1, 0, 0, 1, 0)
	crt := rng.NewCRT(cfg.RNGCrtSeed)
	got, chosen, err := skirmishStartAssignment(cfg, eligibleSlots(4), chosenStarts(), &crt)
	if err != nil || chosen != 1 {
		t.Fatalf("assignment: %v, %d chosen", err, chosen)
	}
	for slot, want := range []int{2, 1, 0, 3} {
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
func TestChosenStartsUnderRandomLocation(t *testing.T) {
	cfg := chosenStartConfig(0, 5, 0, 0, 0)
	crt := rng.NewCRT(cfg.RNGCrtSeed)
	got, _, err := skirmishStartAssignment(cfg, eligibleSlots(4), chosenStarts(), &crt)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != 4 {
		t.Fatalf("slot 0 took %d, want its chosen StartPos5 (stored 4)", got[0])
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

// A chosen start the map lacks is retail's missing-start fatal, a duplicate
// is refused, and too few free starts is this build's error.
func TestChosenStartsRefuseWhatTheMapCannotSeat(t *testing.T) {
	starts := chosenStarts()[:4] // stored 3, 0, 5, 1
	crt := rng.NewCRT(1)
	if _, _, err := skirmishStartAssignment(chosenStartConfig(1, 3, 0), eligibleSlots(2), starts, &crt); err == nil || err.Error() != errStartPositionMissing(2).Error() {
		t.Fatalf("missing StartPos3: %v, want %q", err, errStartPositionMissing(2))
	}
	if _, _, err := skirmishStartAssignment(chosenStartConfig(1, 2, 2), eligibleSlots(2), starts, &crt); err == nil || !strings.Contains(err.Error(), "chosen twice") {
		t.Fatalf("duplicate choice: %v", err)
	}
	if _, _, err := skirmishStartAssignment(chosenStartConfig(0, 1, 0, 0, 0, 0), eligibleSlots(5), starts, &crt); err == nil || !strings.Contains(err.Error(), "free after the chosen ones") {
		t.Fatalf("five players on four starts: %v", err)
	}
	if err := validateChosenStarts(chosenStartConfig(1, 4, 4)); err == nil {
		t.Fatal("Validate accepted two players on one start")
	}
	if err := validateChosenStarts(chosenStartConfig(1, SkirmishMaxStart+1, 0)); err == nil {
		t.Fatal("Validate accepted a start beyond the last")
	}
}

// Battle entry places each commander on its assignment's start: here slot 0
// chooses StartPos2, so slot 1, whose own start that is, takes StartPos1.
func TestChosenStartPlacesTheCommander(t *testing.T) {
	s := strictNewSessionWithUnits(t, 0, 7, 11)
	m := wu19178Mission(
		mission.Special{Kind: 1, ID: 0, X: 12, Z: 20, Name: "StartPos1"},
		mission.Special{Kind: 1, ID: 1, X: 24, Z: 28, Name: "StartPos2"},
	)
	cfg := wu19178Config()
	cfg.Players[0].Start = 2
	if err := skirmishReconstructUnits(s, cfg, m); err != nil {
		t.Fatalf("skirmishReconstructUnits: %v", err)
	}
	want := map[uint8][2]int32{0: {24, 28}, 1: {12, 20}}
	for _, u := range s.Units.Iter() {
		w := want[u.Owner]
		if u.X != numeric.Fixed(w[0])<<16 || u.Z != numeric.Fixed(w[1])<<16 {
			t.Fatalf("player %d commander at (%d,%d), want (%d,%d)", u.Owner, u.X>>16, u.Z>>16, w[0], w[1])
		}
	}
}

// Version 2 of the configuration: a request with no chosen start keeps its
// version-1 bytes and identity (TestMatchConfigEncodingIsUnchangedBySharedPrimitives
// holds them), one with a chosen start appends one byte per seat and field
// 17 and hashes under the version-2 domain, and a version-2 tail of defaults
// is refused.
func TestChosenStartsEncodeAsConfigurationVersion2(t *testing.T) {
	r := validMatchRequest(t)
	v1 := encodeMatchForTest(t, resolveMatch(t, r))
	r.Seats[0].Start = 3
	r.Seats[2].Start = 1
	c := resolveMatch(t, r)
	v2 := encodeMatchForTest(t, c)
	if !bytes.Equal(v2[:len(v1)], v1) || !bytes.Equal(v2[len(v1):], []byte{3, 0, 1, 0}) {
		t.Fatalf("version 2 is not version 1, one start byte per seat and field 17:\nv1 %x\nv2 %x", v1, v2)
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
	if got := decoded.Request().Seats; got[0].Start != 3 || got[1].Start != 0 || got[2].Start != 1 {
		t.Fatalf("decoded starts %d %d %d, want 3 0 1", got[0].Start, got[1].Start, got[2].Start)
	}
	if checkMatchDecode(t, append(append([]byte(nil), v1...), 0, 0, 0, 0)) {
		t.Fatal("a version-2 tail of defaults decoded")
	}
	r.Seats[1].Start = 3
	if _, err := ResolveMatchConfig(r); err == nil {
		t.Fatal("two seats on one start resolved")
	}
	s := validSurvivalRequest(t)
	s.Seats[0].Start = 1
	if _, err := ResolveMatchConfig(s); err == nil {
		t.Fatal("a Survival seat with a chosen start resolved")
	}
}

// The local adapter and the admitted setup carry each row's choice, so a
// replay composes the battle its recording played.
func TestChosenStartsRoundTripTheLocalAdapter(t *testing.T) {
	cfg := DirectSkirmishConfig("fixture")
	cfg.Players[1].Start = 2
	r, err := NewMatchConfigRequest(cfg, SkirmishEntryOptions{}, MatchRoomInputs{Policies: MatchPolicies{Revision: 1, Scheduling: 1, Pacing: 1, Drop: 1, Audience: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Seats[0].Start != 0 || r.Seats[1].Start != 2 {
		t.Fatalf("request starts %d %d, want 0 2", r.Seats[0].Start, r.Seats[1].Start)
	}
	back, _ := matchSkirmishSetup(&r)
	if back.Players[0].Start != 0 || back.Players[1].Start != 2 {
		t.Fatalf("admitted setup starts %d %d, want 0 2", back.Players[0].Start, back.Players[1].Start)
	}
}
