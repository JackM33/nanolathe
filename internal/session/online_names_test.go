package session

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
)

// Online display names (DESIGN_MULTIPLAYER §8.6 player row 1, §15 Q11): a
// seat's name is its configuration nickname, "Player n" when it has none,
// and names the human's player record. These are Nanolathe lobby and
// protocol values, not retail data.

// Unnamed rooms build the configurations released clients build, so the two
// agree at Ready: the digests are configuration version 2's (§8.6), whose
// rooms of no named content encode as the version-1 rooms recorded before
// seats had names or rooms had rule sets, plus field 16's empty count.
func TestOnlineUnnamedRoomsAreLocked(t *testing.T) {
	survivalRoom := onlineTestSetup(true, 0, 0)
	survivalRoom.Computers = []OnlineComputer{{Side: 1, Color: 4, Kind: ai.ControllerModern, Difficulty: 1}}
	for _, tc := range []struct {
		name  string
		setup OnlineMatchSetup
		want  string
	}{
		{"two teams", onlineTestSetup(false, 1, 2), "1a6072ee81ae7c6a76afe73eda1242c704aeee4ff9678e2fd118e77d75ebba40"},
		{"two humans and two computers", onlineComputerTestSetup(false), "13585ad18d5009766d87c29c6ded12bfef45fb8243763b62d8718abbd30200ff"},
		{"Survival with a computer survivor", survivalRoom, "61a1b2dcb9ddeb09121f891fb1a6fd867daca80e8455c76693e2d8710417e6a8"},
	} {
		digest := resolveMatch(t, onlineTestRequest(t, tc.setup)).Digest()
		if got := hex.EncodeToString(digest[:]); got != tc.want {
			t.Errorf("%s: configuration digest %s, want %s", tc.name, got, tc.want)
		}
	}
}

// A seat's name is its row's nickname and an unnamed seat keeps "Player n";
// a name changes the configuration every seat agrees, and naming a seat its
// own default changes nothing.
func TestOnlineNamesBecomeNicknames(t *testing.T) {
	plain := onlineComputerTestSetup(false)
	named := onlineComputerTestSetup(false)
	named.Seats[1].Name = "Ada ~ #2"
	r := onlineTestRequest(t, named)
	for i, want := range []string{"Player 1", "Ada ~ #2", "", ""} {
		if r.Seats[i].Nickname != want {
			t.Fatalf("row %d nickname %q, want %q", i, r.Seats[i].Nickname, want)
		}
	}
	c := resolveMatch(t, r)
	payload, err := EncodeMatchConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecodeMatchConfig(payload)
	if err != nil || back.Digest() != c.Digest() || back.Request().Seats[1].Nickname != "Ada ~ #2" {
		t.Fatalf("round trip: %v", err)
	}
	base := resolveMatch(t, onlineTestRequest(t, plain)).Digest()
	if c.Digest() == base {
		t.Fatal("a name left the configuration digest unchanged")
	}
	plain.Seats[0].Name = "Player 1"
	if resolveMatch(t, onlineTestRequest(t, plain)).Digest() != base {
		t.Fatal("naming a seat its default changed the configuration")
	}
}

// A name is 1..16 printable ASCII bytes without '<', '>' or a space at
// either end; a seat whose name is refused is refused at its own name.
func TestOnlineNameValidation(t *testing.T) {
	for _, name := range []string{"A", "sixteen bytes ok", "a b", "~!@#$%^&*()_+{}|", "Player 9"} {
		if err := ValidateOnlineName(name); err != nil {
			t.Fatalf("%q: %v", name, err)
		}
	}
	for _, name := range []string{"", " Ada", "Ada ", " ", "<Ada>", "A>b", "a<", "seventeen bytes!!", "tab\there", "café", "nul\x00", "del\x7f"} {
		if err := ValidateOnlineName(name); err == nil || !strings.Contains(err.Error(), "logical path name,") {
			t.Fatalf("%q: %v", name, err)
		}
		if name == "" {
			continue
		}
		setup := onlineTestSetup(false, 0, 0)
		setup.Seats[1].Name = name
		if _, err := NewOnlineMatchRequest(setup, SkirmishEntryOptions{}, matchTestRoom()); err == nil || !strings.Contains(err.Error(), "logical path setup.seats[1].name,") {
			t.Fatalf("%q in a seat: %v", name, err)
		}
	}
}

// Battle entry names each online human's player record with its row's
// nickname, which the result board, the elimination line and chat read; a
// computer keeps the conversion's side name [08 R-SKIR-01 §2].
func TestOnlineNamesNameTheHumanRecords(t *testing.T) {
	setup := onlineComputerTestSetup(false)
	setup.Seats[0].Name = "Ada"
	inputs, config := onlineComputerFixtureMatch(t, setup)
	s, err := NewPlaytestSkirmish(inputs, config, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeAIControllers)
	seats := config.Request().Seats
	for i, want := range []string{"Ada", "Player 2", skirmishSlotName(2, int(seats[2].Side)), skirmishSlotName(2, int(seats[3].Side))} {
		if got := s.Econ.Players[i].Name; got != want {
			t.Fatalf("player %d record name %q, want %q", i, got, want)
		}
	}
}

// Names are presentation: a named room composes, rehearses and plays
// exactly as the unnamed one does, so no unit checksum, fingerprint or
// rehearsal digest reads them.
func TestOnlineNamesLeaveTheSimulationAlone(t *testing.T) {
	plain := onlineTestSetup(false, 0, 0)
	named := onlineTestSetup(false, 0, 0)
	named.Seats[0].Name, named.Seats[1].Name = "Ada", "Grace"
	plainInputs, plainConfig := onlineRulesFixtureMatch(t, plain)
	namedInputs, namedConfig := onlineRulesFixtureMatch(t, named)
	if plainConfig.Digest() == namedConfig.Digest() {
		t.Fatal("the names did not reach the configuration")
	}
	a, err := NewPlaytestSkirmish(plainInputs, plainConfig, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewPlaytestSkirmish(namedInputs, namedConfig, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b.Econ.Players[1].Name != "Grace" || a.Econ.Players[1].Name != "Player 2" {
		t.Fatalf("record names %q and %q", a.Econ.Players[1].Name, b.Econ.Players[1].Name)
	}
	fa, err := a.PartialStateFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	fb, err := b.PartialStateFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if fa != fb || a.UnitStateChecksum() != b.UnitStateChecksum() {
		t.Fatal("the names reached the fingerprint or the unit checksum")
	}
	if rehearsalDigestOf(t, plainInputs, plainConfig, 0) != rehearsalDigestOf(t, namedInputs, namedConfig, 1) {
		t.Fatal("the names changed the rehearsal")
	}
}
