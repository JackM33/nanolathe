package main

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// A display name is the configuration's nickname rule; spaces at either end
// are trimmed and an empty name chooses none.
func TestOnlineNameRefusal(t *testing.T) {
	for typed, refused := range map[string]bool{
		"":                  false,
		"   ":               false,
		"Daniel":            false,
		"  Commander Keen ": false,
		"Commander Keen99":  false,
		"Commander Keen991": true,
		"Big<Bad>":          true,
		"tab\there":         true,
	} {
		if got := onlineNameRefusal(typed); (got != "") != refused {
			t.Fatalf("%q: refusal %q", typed, got)
		}
	}
	if !strings.Contains(onlineNameRefusal("Commander Keen991"), "at most 16") || !strings.Contains(onlineNameRefusal("Big<Bad>"), "not < or >") {
		t.Fatal("the refusals do not say why in plain words")
	}
}

// The chooser's Name control opens the name popup, which refuses a bad name in
// plain words and keeps a good one, trimmed, in the settings key `nickname`
// for the next visit; an emptied field chooses none.
func TestOnlineChooserNamePersists(t *testing.T) {
	g := onlineTestShell(t)
	if err := g.openOnlineScreen(); err != nil {
		t.Fatal(err)
	}
	p := g.online.panel
	if p.TextOf("NAMELABEL") != "Name: none chosen" || greyed(p, "NAME") {
		t.Fatalf("chooser name %q", p.TextOf("NAMELABEL"))
	}
	g.activateGadget("NAME")
	p = g.online.panel
	if g.online.view != onlineNameEntry || p.TextOf("TEXT") != onlineNamePrompt || p.TextOf("ADDRESS") != "" || !p.EditorCaptured() || p.TextOf("STATUS") != onlineNameHint {
		t.Fatalf("name popup: %q %q %q", p.TextOf("TEXT"), p.TextOf("ADDRESS"), p.TextOf("STATUS"))
	}
	p.SetText("ADDRESS", "Big<Bad>")
	g.activateGadget("OK")
	if g.online.view != onlineNameEntry || !strings.Contains(p.TextOf("STATUS"), "not < or >") || g.online.name != "" || !p.EditorCaptured() {
		t.Fatalf("a refused name: %q", p.TextOf("STATUS"))
	}
	p.SetText("ADDRESS", "  Daniel ")
	g.activateGadget("ADDRESS") // Enter in the field
	if g.online.view != onlineChooser || g.online.name != "Daniel" || g.online.panel.TextOf("NAMELABEL") != "Name: Daniel" {
		t.Fatalf("named: view %d name %q label %q", g.online.view, g.online.name, g.online.panel.TextOf("NAMELABEL"))
	}
	if s, err := settings.Load(); err != nil || s.Nickname != "Daniel" {
		t.Fatalf("stored nickname %q: %v", s.Nickname, err)
	}
	// The next visit reads it back, and the popup starts from it.
	g.closeOnlineScreen()
	if err := g.openOnlineScreen(); err != nil {
		t.Fatal(err)
	}
	g.activateGadget("NAME")
	if g.online.name != "Daniel" || g.online.panel.TextOf("ADDRESS") != "Daniel" {
		t.Fatalf("reopened with %q, field %q", g.online.name, g.online.panel.TextOf("ADDRESS"))
	}
	// Cancel keeps it; an emptied field and OK chooses none.
	g.activateGadget("PREV")
	g.activateGadget("NAME")
	g.online.panel.SetText("ADDRESS", "")
	g.activateGadget("OK")
	if g.online.name != "" || g.online.panel.TextOf("NAMELABEL") != "Name: none chosen" {
		t.Fatalf("cleared name %q", g.online.name)
	}
	if s, _ := settings.Load(); s.Nickname != "" {
		t.Fatalf("stored nickname %q after clearing", s.Nickname)
	}
}

// The name rides in the hello's dial options: a join describes the room with
// it.
func TestOnlineJoinCarriesTheName(t *testing.T) {
	fake := &fakeOnlineRelay{}
	useOnlineTestRelay(t, fake)
	g := onlineTestShell(t)
	if err := g.openOnlineScreen(); err != nil {
		t.Fatal(err)
	}
	g.online.name = "Daniel"
	g.activateGadget("JOIN")
	g.online.panel.SetText("ADDRESS", "CFH234")
	g.activateGadget("OK")
	awaitOnline(t, g)
	if len(fake.options) != 1 || fake.options[0].Name != "Daniel" {
		t.Fatalf("dial options %+v", fake.options)
	}
}

// Every seat composes the same final configuration from the same lobby state,
// names included: a named seat's row carries its name, an unnamed one "Player
// n", and a seat whose client cannot carry a name (no chat, an older protocol)
// its default whatever the state says. A rename is a new room key.
func TestOnlineSeatsComposeTheSameNames(t *testing.T) {
	useOnlineSessionSeams(t, 4)
	named := func(fake *fakeOnlineLobby) {
		fake.state.Seats[0].Name, fake.state.Seats[0].Chat = "Daniel", true
		fake.state.Seats[1].Chat = true
		fake.state.Seats[2].Name = "Ghost"
	}
	host, guest := onlineTestShell(t), onlineTestShell(t)
	hostLobby, guestLobby := newFakeOnlineLobby(0, 0, 1, 2), newFakeOnlineLobby(1, 0, 1, 2)
	named(hostLobby)
	named(guestLobby)
	openTestLobby(t, host, hostLobby, onlineTestCatalog())
	openTestLobby(t, guest, guestLobby, onlineTestCatalog())
	host.activateGadget(lobbyRules) // Strict 3.1, which the guest adopts
	guestLobby.config = hostLobby.config
	guestLobby.state.ConfigVersion++
	guest.pollOnline()
	compose := func(g *gameShell) session.EffectiveMatchConfig {
		t.Helper()
		r := &g.online.room
		seats, _, ok := onlineSeatsOf(r.state, r.lobby.Seat(), r.settings.survival)
		if !ok {
			t.Fatal("no local seat")
		}
		config, err := onlineConfig(g.cs, r.cat, r.settings, seats, onlineFrozenOf(g.cs, r.base.Request()))
		if err != nil {
			t.Fatal(err)
		}
		return config
	}
	hostConfig, guestConfig := compose(host), compose(guest)
	if hostConfig.Digest() != guestConfig.Digest() {
		t.Fatal("the host and the guest composed different configurations")
	}
	r := hostConfig.Request()
	if r.RuleName != session.StrictRuleSetName {
		t.Fatalf("composed rules %q", r.RuleName)
	}
	for slot, want := range []string{"Daniel", "Player 2", "Player 3"} {
		if got := r.Seats[slot].Nickname; got != want {
			t.Fatalf("slot %d nickname %q, want %q", slot, got, want)
		}
	}
	// The rows show the same names, the local mark kept.
	if lobbyText(host, "Player0") != "Daniel (You)" || lobbyText(guest, "Player0") != "Daniel (Host)" || lobbyText(guest, "Player1") != "Player 2 (You)" || lobbyText(host, "Player2") != "Player 3" {
		t.Fatalf("rows %q %q %q %q", lobbyText(host, "Player0"), lobbyText(guest, "Player0"), lobbyText(guest, "Player1"), lobbyText(host, "Player2"))
	}
	if help := guest.online.room.panel.HelpOf("Player0"); help != "Daniel is the host, who chooses the settings and starts the game." {
		t.Fatalf("host row help %q", help)
	}
	key := host.online.room.key()
	hostLobby.state.Seats[1].Name = "Ann"
	host.pollOnline()
	if host.online.room.key() == key || lobbyText(host, "Player1") != "Ann" {
		t.Fatal("a renamed seat kept the room key")
	}
}

// A long name gives way to its mark.
func TestOnlineFitCaptionKeepsTheMark(t *testing.T) {
	measure := func(s string) int { return 6 * len(s) }
	if got := onlineFitCaption("Daniel", " (You)", measure, 102); got != "Daniel (You)" {
		t.Fatalf("short name %q", got)
	}
	got := onlineFitCaption("Commander Keen99", " (You)", measure, 102)
	if !strings.HasSuffix(got, "... (You)") || measure(got) > 102 || !strings.HasPrefix(got, "Comm") {
		t.Fatalf("long name %q", got)
	}
}

// The battle's first message names the player when it has a name.
func TestOnlineEntryNoticeNamesThePlayer(t *testing.T) {
	useOnlineSessionSeams(t, 0)
	frozen := onlineCreationFrozen(&contentSet{profile: "retail", limits: content.RetailLimits()}, [2]uint32{1, 2}, content.Mutators{}, content.Restrictions{}, nil, nil)
	config, err := onlineConfig(nil, onlineTestCatalog(), onlineSettings{mapName: "Test Map"}, []session.OnlineSeat{{Name: "Daniel"}, {Color: 1}}, frozen)
	if err != nil {
		t.Fatal(err)
	}
	r := config.Request()
	if got := onlineEntryNotice("K7M2QX", r, 0, 2); got != "Online game K7M2QX: you are Daniel, player 1 of 2" {
		t.Fatalf("named %q", got)
	}
	if got := onlineEntryNotice("K7M2QX", r, 1, 2); got != "Online game K7M2QX: you are player 2 of 2" {
		t.Fatalf("unnamed %q", got)
	}
}
