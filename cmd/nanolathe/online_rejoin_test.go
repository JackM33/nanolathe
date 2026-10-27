package main

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/relay"
)

// The seat's rejoin ticket is kept beside the settings only while a battle
// runs: it is written owner-readable, offered while recent, and forgotten
// when the battle ends or the relay no longer holds the seat
// (DESIGN_MULTIPLAYER §16.9).
func TestOnlineRejoinTicketIsKeptWhileTheBattleRuns(t *testing.T) {
	g := onlineTestShell(t)
	if _, ok := keptOnlineRejoin(); ok {
		t.Fatal("a fresh install offered a rejoin")
	}
	ticket := relay.HostedRejoinTicket{Code: "K7M2PX", Seat: 1, Credential: [32]byte{1, 2, 3}}
	record := onlineRejoinRecord{Server: "wss://relay.example.test/relay", Code: ticket.Code, Seat: ticket.Seat, Credential: "010203" + strings.Repeat("00", 29), Started: time.Now()}
	writeRejoinRecord(t, record)
	if info, err := os.Stat(onlineRejoinPath()); err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("the ticket file is readable by others: %v %v", info, err)
	}
	got, ok := keptOnlineRejoin()
	if back, valid := got.ticket(); !ok || !valid || back != ticket || got.Server != record.Server {
		t.Fatalf("kept %+v %v", got, ok)
	}
	// The chooser offers Rejoin and says which game.
	if err := g.openOnlineScreen(); err != nil {
		t.Fatal(err)
	}
	p := g.online.panel
	if p.Index("REJOIN") < 0 || greyed(p, "REJOIN") || !strings.Contains(g.online.status, "K7M2PX") {
		t.Fatalf("the chooser did not offer the kept game: %q", g.online.status)
	}
	// An old ticket is not offered, and an ended battle forgets the ticket.
	record.Started = time.Now().Add(-onlineRejoinAge - time.Minute)
	writeRejoinRecord(t, record)
	if _, ok := keptOnlineRejoin(); ok {
		t.Fatal("a stale ticket was offered")
	}
	record.Started = time.Now()
	writeRejoinRecord(t, record)
	forgetOnlineRejoin()
	if _, ok := keptOnlineRejoin(); ok {
		t.Fatal("a forgotten ticket was offered")
	}
	g.closeOnlineScreen()
	if err := g.openOnlineScreen(); err != nil {
		t.Fatal(err)
	}
	if g.online.panel.Index("REJOIN") >= 0 {
		t.Fatal("the chooser offered Rejoin with nothing kept")
	}
	// A battle on an older relay, which gives no ticket, keeps none.
	writeRejoinRecord(t, record)
	keepOnlineRejoin("wss://relay.example.test/relay", (*relay.LocalClient)(nil))
	if _, ok := keptOnlineRejoin(); ok {
		t.Fatal("a battle without a ticket left an earlier one")
	}
}

func writeRejoinRecord(t *testing.T, r onlineRejoinRecord) {
	t.Helper()
	data := []byte(`{"server":"` + r.Server + `","code":"` + r.Code + `","seat":1,"credential":"` + r.Credential + `","started":"` + r.Started.UTC().Format(time.RFC3339Nano) + `"}`)
	if err := os.MkdirAll(strings.TrimSuffix(onlineRejoinPath(), onlineRejoinFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(onlineRejoinPath(), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// The relay's answers to a kept ticket, and to a lost match, read as plain
// words, and an answer that the match is gone forgets the ticket.
func TestOnlineReconnectWordsArePlain(t *testing.T) {
	onlineTestShell(t)
	relayRefusal := func(path, expected string) error {
		return errors.New("nanolathe: hosted relay rejected: logical path " + path + ", providers searched [hosted transport], expected " + expected)
	}
	for _, c := range []struct {
		err    error
		want   string
		forget bool
	}{
		{relayRefusal("room", "a game that is still running"), "That game has ended", true},
		{relayRefusal("rejoin ticket", "the credential this seat received when its game started"), "no longer has your seat", true},
		{relayRefusal("rejoin history", "a game short enough to replay from the resumed tick"), "too long to replay", true},
		{relayRefusal("handshake", "hosted protocol version 7 or 8; this client sent version 9"), "does not support rejoining", false},
	} {
		writeRejoinRecord(t, onlineRejoinRecord{Server: "wss://relay.example.test/relay", Code: "K7M2PX", Credential: strings.Repeat("ab", 32), Started: time.Now()})
		got := onlineRejoinRefusal(c.err)
		if _, kept := keptOnlineRejoin(); !strings.Contains(got, c.want) || kept == c.forget {
			t.Fatalf("%v: %q (kept %v)", c.err, got, kept)
		}
	}
	for _, c := range []struct {
		err  error
		want string
	}{
		{relayRefusal("player", "every player to stay until the game ends; a player left"), "A player left the game"},
		{relayRefusal("room", "a game that is still running"), "ended while you were away"},
		{relayRefusal("reconnection", "every dropped player back within three minutes"), "did not come back within three minutes"},
		{relayRefusal("reconnection", "the relay back within the rejoin window"), "Could not reconnect"},
	} {
		if got := onlineRefusalText(c.err); !strings.Contains(got, c.want) || strings.Contains(got, "logical path") {
			t.Fatalf("%v: %q", c.err, got)
		}
	}
	if onlineClock(3*time.Minute+7*time.Second) != "3:07" || onlineClock(-time.Second) != "0:00" {
		t.Fatal("wait clock")
	}
}
