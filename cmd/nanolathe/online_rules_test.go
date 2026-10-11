package main

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// roomRules decodes the base configuration the host last sent and names its
// rule set.
func roomRules(t *testing.T, fake *fakeOnlineLobby) string {
	t.Helper()
	base, err := session.DecodeMatchConfig(fake.config)
	if err != nil {
		t.Fatal(err)
	}
	return base.Request().RuleName
}

// The host's Rules control steps Strict 3.1, Community 3.9 and Modern; each
// step replaces the base configuration and clears readiness, and a guest
// adopts the rules and sees them read-only (DESIGN_MULTIPLAYER §16.6).
func TestOnlineLobbyRulesHostCyclesGuestAdopts(t *testing.T) {
	useOnlineSessionSeams(t, 4)
	host := onlineTestShell(t)
	hostLobby := newFakeOnlineLobby(0, 0, 1)
	openTestLobby(t, host, hostLobby, onlineTestCatalog())
	hr := &host.online.room
	if hr.settings.rules != session.ModernRuleSetName || lobbyText(host, lobbyRules) != "Modern" || greyed(hr.panel, lobbyRules) {
		t.Fatalf("the host's new room: rules %q, caption %q", hr.settings.rules, lobbyText(host, lobbyRules))
	}
	guest := onlineTestShell(t)
	guestLobby := newFakeOnlineLobby(1, 0, 1)
	openTestLobby(t, guest, guestLobby, onlineTestCatalog())
	gr := &guest.online.room
	if !greyed(gr.panel, lobbyRules) || !strings.HasPrefix(gr.panel.HelpOf(lobbyRules), "The host chose these rules.") {
		t.Fatalf("a guest is offered the rules: %q", gr.panel.HelpOf(lobbyRules))
	}
	for _, want := range []struct{ rules, caption string }{
		{session.StrictRuleSetName, "Strict 3.1"},
		{session.CommunityRuleSetName, "Community 3.9"},
		{session.ModernRuleSetName, "Modern"},
	} {
		hostLobby.state.Seats[1].Ready = true
		version := hostLobby.state.ConfigVersion
		host.activateGadget(lobbyRules)
		host.pollOnline()
		if got := roomRules(t, hostLobby); got != want.rules || hr.settings.rules != want.rules || lobbyText(host, lobbyRules) != want.caption {
			t.Fatalf("host stepped to %q (settings %q, caption %q), want %q", got, hr.settings.rules, lobbyText(host, lobbyRules), want.rules)
		}
		if hostLobby.state.ConfigVersion == version || hostLobby.state.Seats[1].Ready {
			t.Fatal("a rules change did not replace the base or clear readiness")
		}
		// The guest was ready; the new base clears it and it adopts the rules.
		gr.prepared, gr.ready = &onlinePrepared{key: gr.key()}, true
		guestLobby.state.Seats[1].Ready = true
		guest.pollOnline()
		guestLobby.config = hostLobby.config
		guestLobby.state.ConfigVersion++
		guestLobby.clearReady()
		guest.pollOnline()
		if gr.settings.rules != want.rules || lobbyText(guest, lobbyRules) != want.caption || !greyed(gr.panel, lobbyRules) {
			t.Fatalf("guest adopted %q, caption %q", gr.settings.rules, lobbyText(guest, lobbyRules))
		}
		if gr.ready || !strings.Contains(lobbyText(guest, lobbyStatus), "changed the game settings") {
			t.Fatalf("the guest's readiness after a rules change: %v %q", gr.ready, lobbyText(guest, lobbyStatus))
		}
		version = guestLobby.state.ConfigVersion
		guest.activateGadget(lobbyRules)
		if gr.settings.rules != want.rules || guestLobby.state.ConfigVersion != version {
			t.Fatal("a guest changed the rules")
		}
	}
	// The hello names the room's rule set, not Modern always.
	if got := onlineHello(0, session.StrictRuleSetName).Identity.Rules.Name; got != session.StrictRuleSetName {
		t.Fatalf("hello rules %q", got)
	}
	if got := onlineHello(1, "").Identity.Rules.Name; got != session.ModernRuleSetName {
		t.Fatalf("hello rules for an unnamed set %q", got)
	}
}

// Field 10 and the unit limit follow the rule set from the content's own
// Community table, not from the table the room was created with: Strict 3.1
// composes the zero table and the retail default limit, and a switch back
// finds the content's table again, on the host and on a seat that reads the
// base (onlineFrozenOf).
func TestOnlineRulesResolveTheCommunityTablePerMode(t *testing.T) {
	useOnlineSessionSeams(t, 0)
	limit := 400
	cs := &contentSet{profile: "retail", limits: content.RetailLimits(), gameplayFeatures: []community.Overrides{{UnitLimit: &limit}}}
	settings := onlineSettings{rules: session.ModernRuleSetName, mapName: "Test Map", location: 1, commanderDeath: 1}
	compose := func(rules string, frozen onlineFrozen) session.MatchConfigRequest {
		t.Helper()
		settings.rules = rules
		base, err := onlineBaseConfig(cs, onlineTestCatalog(), settings, frozen)
		if err != nil {
			t.Fatalf("%s: %v", rules, err)
		}
		if r := base.Request(); r.RuleName != rules {
			t.Fatalf("composed %q, want %q", r.RuleName, rules)
		}
		return base.Request()
	}
	modern := compose(session.ModernRuleSetName, onlineCreationFrozen(cs, [2]uint32{1, 2}, content.Mutators{}, content.Restrictions{}, nil))
	if modern.Community.UnitLimit != limit || modern.UnitLimit != uint16(limit) {
		t.Fatalf("Modern table limit %d, configuration limit %d, want the content's %d", modern.Community.UnitLimit, modern.UnitLimit, limit)
	}
	strict := compose(session.StrictRuleSetName, onlineFrozenOf(cs, modern))
	if strict.Community != (community.Features{}) || int(strict.UnitLimit) != session.DirectSkirmishConfig("Test Map").UnitLimit {
		t.Fatalf("Strict table %+v, limit %d", strict.Community, strict.UnitLimit)
	}
	communityReq := compose(session.CommunityRuleSetName, onlineFrozenOf(cs, strict))
	want, err := session.ResolveCommunity(gameplay.Community39, session.CommunitySources{Content: cs.gameplayFeatures})
	if err != nil {
		t.Fatal(err)
	}
	if communityReq.Community != want || communityReq.UnitLimit != uint16(limit) {
		t.Fatalf("Community 3.9 after Strict: limit %d, table matches the content's: %v", communityReq.UnitLimit, communityReq.Community == want)
	}
	if again := compose(session.ModernRuleSetName, onlineFrozenOf(cs, communityReq)); again.Community != modern.Community || again.UnitLimit != modern.UnitLimit {
		t.Fatal("switching back to Modern lost the content's table")
	}
}

// Strict 3.1 allows the host one computer: the Add computer row goes once it
// has one, and a switch to Strict 3.1 with two seated is refused in plain
// words until the host removes one (DESIGN_MULTIPLAYER §6.6).
func TestOnlineLobbyStrictComputerCap(t *testing.T) {
	useOnlineSessionSeams(t, 6)
	g := onlineTestShell(t)
	fake := newFakeOnlineLobby(0, 0, 1)
	openTestLobby(t, g, fake, onlineTestCatalog())
	r := &g.online.room
	p := r.panel
	g.activateGadget("Player2")
	g.activateGadget("Player3")
	if len(r.settings.computers) != 2 {
		t.Fatalf("computers %+v", r.settings.computers)
	}
	version := fake.state.ConfigVersion
	g.activateGadget(lobbyRules)
	if r.settings.rules != session.ModernRuleSetName || fake.state.ConfigVersion != version || r.notice != onlineStrictComputersText || !strings.HasPrefix(lobbyText(g, lobbyStatus), "Strict 3.1 allows one computer per human") {
		t.Fatalf("Strict 3.1 with two computers: rules %q, notice %q", r.settings.rules, r.notice)
	}
	// Modern AI 2 to Classic, then out of the room.
	g.activateGadget("Player3")
	g.activateGadget("Player3")
	g.activateGadget(lobbyRules)
	if r.settings.rules != session.StrictRuleSetName || len(r.settings.computers) != 1 || roomRules(t, fake) != session.StrictRuleSetName {
		t.Fatalf("Strict 3.1 with one computer: rules %q, notice %q", r.settings.rules, r.notice)
	}
	if r.canAddComputer() || p.ActiveOf("Player3") {
		t.Fatal("Strict 3.1 offers a second computer")
	}
	if !strings.Contains(p.HelpOf(lobbyRules), "One computer per human") {
		t.Fatalf("the Strict 3.1 help does not explain its cap: %q", p.HelpOf(lobbyRules))
	}
	// Community 3.9 offers it again.
	g.activateGadget(lobbyRules)
	if r.settings.rules != session.CommunityRuleSetName || !p.ActiveOf("Player3") || lobbyText(g, "Player3") != onlineAddComputerCaption {
		t.Fatalf("Community 3.9 does not offer another computer: %q", lobbyText(g, "Player3"))
	}
}

// A mod's minimumGameplay leaves the lower stages out of the room, even for a
// host who overrode the mod's lock locally; a new room starts at the minimum.
func TestOnlineRulesFollowTheModMinimum(t *testing.T) {
	useOnlineSessionSeams(t, 4)
	g := onlineTestShell(t)
	g.cs.mod = &modlibrary.Mod{Metadata: modlibrary.Metadata{ID: "minmod", Version: "1", MinimumGameplay: string(gameplay.Community39)}}
	g.lockOverrides = []string{"minmod"}
	if got := onlineRoomRules(gameplay.Strict31, g.cs); got != session.CommunityRuleSetName {
		t.Fatalf("a Strict 3.1 host's room under a Community 3.9 mod starts on %q", got)
	}
	if got := onlineRoomRules(gameplay.Modern, g.cs); got != session.ModernRuleSetName {
		t.Fatalf("a Modern host's room starts on %q", got)
	}
	if got := onlineRoomRules(gameplay.Strict31, &contentSet{}); got != session.StrictRuleSetName {
		t.Fatalf("a Strict 3.1 host without a mod starts on %q", got)
	}
	fake := newFakeOnlineLobby(0, 0, 1)
	openTestLobby(t, g, fake, onlineTestCatalog())
	r := &g.online.room
	for _, want := range []string{session.CommunityRuleSetName, session.ModernRuleSetName, session.CommunityRuleSetName} {
		g.activateGadget(lobbyRules)
		if r.settings.rules != want {
			t.Fatalf("stepped to %q, want %q", r.settings.rules, want)
		}
	}
	if !strings.HasPrefix(r.panel.HelpOf(lobbyRules), "Click for this mod's other rules.") {
		t.Fatalf("help %q", r.panel.HelpOf(lobbyRules))
	}
	// A Modern-only mod leaves nothing to choose.
	g.cs.mod.MinimumGameplay = string(gameplay.Modern)
	g.refreshOnlineLobby()
	if !greyed(r.panel, lobbyRules) || !strings.HasPrefix(r.panel.HelpOf(lobbyRules), "This mod plays only Modern rules.") {
		t.Fatalf("a Modern-only mod: help %q", r.panel.HelpOf(lobbyRules))
	}
	if got := onlineNextRules(session.StrictRuleSetName, 1, 2); got != session.ModernRuleSetName {
		t.Fatalf("next above Modern's minimum %q", got)
	}
}
