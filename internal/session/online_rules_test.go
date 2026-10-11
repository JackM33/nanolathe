package session

import (
	"errors"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
)

// Online rooms under the three reserved rule sets (DESIGN_MULTIPLAYER §6.6,
// §16.6): a room names Strict 3.1, Community 3.9 or Modern, the empty name
// being Modern. These are Nanolathe lobby and protocol values, not retail
// data.

// onlineRuleCases are the names a room may give, with the set each selects
// and its wire base.
var onlineRuleCases = []struct {
	rules string
	mode  gameplay.Mode
	base  MatchRuleBase
}{
	{"", gameplay.Modern, MatchBaseModern},
	{ModernRuleSetName, gameplay.Modern, MatchBaseModern},
	{StrictRuleSetName, gameplay.Strict31, MatchBaseStrict31},
	{CommunityRuleSetName, gameplay.Community39, MatchBaseCommunity39},
}

// onlineRulesFixtureMatch resolves and freezes a lobby setup on the authored
// two-position install.
func onlineRulesFixtureMatch(t *testing.T, setup OnlineMatchSetup) (*content.SimulationInputs, EffectiveMatchConfig) {
	t.Helper()
	fs, cat := restrictionMatchInstall(t)
	setup.MapName = restrictionMatchMap
	setup.SideCount = len(OnlineSides(cat))
	room := matchTestRoom()
	schema, err := OnlineMapSchema(fs, cat, setup.MapName, setup.Rows())
	if err != nil {
		t.Fatal(err)
	}
	room.MapSchema = schema
	_, options := restrictionMatchSetup()
	r, err := NewOnlineMatchRequest(setup, options, room)
	if err != nil {
		t.Fatal(err)
	}
	config := resolveMatch(t, r)
	inputs, err := FreezeMatchInputs(fs, cat, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	return inputs, config
}

// A room's request names its set and that set's base, resolves the
// Community table through it — Strict 3.1's is the zero table whatever the
// content declares, Community 3.9 and Modern carry the mounted content's own
// table under their declarations — and gives every human row the set's
// default builder options. The empty name builds exactly Modern's request.
func TestOnlineMatchRequestCarriesTheRoomRules(t *testing.T) {
	if StrictRuleSet().Orders.DefaultBuilderOptions() == ModernRuleSet().Orders.DefaultBuilderOptions() {
		t.Fatal("Strict 3.1 and Modern share builder defaults, so this test cannot tell them apart")
	}
	snap := true
	sources := CommunitySources{Content: []community.Overrides{{MexSnap: &snap}}}
	digests := map[gameplay.Mode]EffectiveMatchConfig{}
	for _, tc := range onlineRuleCases {
		setup := onlineTestSetup(false, 1, 2)
		setup.Rules = tc.rules
		r, err := NewOnlineMatchRequest(setup, SkirmishEntryOptions{CommunitySources: sources}, matchTestRoom())
		if err != nil {
			t.Fatalf("%q: %v", tc.rules, err)
		}
		if r.RuleName != string(tc.mode) || r.RuleBase != tc.base {
			t.Fatalf("%q: rule %q base %d, want %q base %d", tc.rules, r.RuleName, r.RuleBase, tc.mode, tc.base)
		}
		want, err := ResolveCommunity(tc.mode, sources)
		if err != nil {
			t.Fatal(err)
		}
		if tc.mode == gameplay.Strict31 {
			if r.Community != (community.Features{}) {
				t.Fatalf("%q: Strict 3.1 carries the Community table %+v", tc.rules, r.Community)
			}
		} else if r.Community != want || !r.Community.MexSnap {
			t.Fatalf("%q: Community table %+v, want the content's own under the set, %+v", tc.rules, r.Community, want)
		}
		builder := RuleSetForMode(tc.mode).Orders.DefaultBuilderOptions()
		for i := range setup.Seats {
			if r.Seats[i].BuilderOptions != builder {
				t.Fatalf("%q: seat %d builder options %+v, want the set's defaults %+v", tc.rules, i, r.Seats[i].BuilderOptions, builder)
			}
		}
		c := resolveMatch(t, r)
		payload, err := EncodeMatchConfig(c)
		if err != nil {
			t.Fatal(err)
		}
		back, err := DecodeMatchConfig(payload)
		if err != nil || back.Digest() != c.Digest() || back.Request().RuleName != string(tc.mode) {
			t.Fatalf("%q: round trip %v", tc.rules, err)
		}
		if seen, ok := digests[tc.mode]; ok {
			if seen.Digest() != c.Digest() {
				t.Fatalf("%q: the empty name and Modern's own build different configurations", tc.rules)
			}
			continue
		}
		for mode, other := range digests {
			if other.Digest() == c.Digest() {
				t.Fatalf("%q builds %s's configuration", tc.rules, mode)
			}
		}
		digests[tc.mode] = c
	}
}

// A name other than the three reserved sets' is refused, a set a mod
// registered included: online rooms offer only Strict 3.1, Community 3.9 and
// Modern.
func TestOnlineRulesRefuseOtherSets(t *testing.T) {
	if _, ok := LookupRuleSet(testRuleSetName); !ok {
		t.Fatalf("the registered test set %q is missing", testRuleSetName)
	}
	for _, rules := range []string{testRuleSetName, "Modern", "strict", "classic", " modern"} {
		setup := onlineTestSetup(false, 0, 0)
		setup.Rules = rules
		if _, err := NewOnlineMatchRequest(setup, SkirmishEntryOptions{}, matchTestRoom()); err == nil || !strings.Contains(err.Error(), "logical path setup.rules,") {
			t.Fatalf("%q: %v", rules, err)
		}
		if _, err := OnlineComputersPerHost(rules); err == nil || !strings.Contains(err.Error(), "logical path rules,") {
			t.Fatalf("%q: computers per host %v", rules, err)
		}
	}
}

// Strict 3.1 keeps one computer per human and Community 3.9 and Modern
// allow the available seats (§6.6, §15 Q23). Only the room host adds
// computers, so a Strict room holds one: a second is refused when the lobby
// builds its request, and a request that holds two anyway is refused by
// validation.
func TestOnlineComputersPerHostFollowsTheRules(t *testing.T) {
	for _, tc := range onlineRuleCases {
		want := SkirmishMaxPlayers
		if tc.mode == gameplay.Strict31 {
			want = 1
		}
		if got, err := OnlineComputersPerHost(tc.rules); err != nil || got != want {
			t.Fatalf("%q: %d computers per host, %v; want %d", tc.rules, got, err, want)
		}
		setup := onlineTestSetup(false, 1, 2)
		setup.Rules = tc.rules
		setup.Computers = []OnlineComputer{{Team: 2, Side: 1, Color: 5, Kind: ai.ControllerClassic, Difficulty: 1}}
		resolveMatch(t, onlineTestRequest(t, setup))
		setup.Computers = append(setup.Computers, OnlineComputer{Side: 0, Color: 6, Kind: ai.ControllerModern, Difficulty: 2})
		r, err := NewOnlineMatchRequest(setup, SkirmishEntryOptions{}, matchTestRoom())
		if tc.mode == gameplay.Strict31 {
			if err == nil || !strings.Contains(err.Error(), "logical path setup.computers,") {
				t.Fatalf("Strict 3.1 admitted a second computer: %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q refused a second computer: %v", tc.rules, err)
		}
		resolveMatch(t, r)
	}
	setup := onlineTestSetup(false, 1, 2)
	setup.Computers = []OnlineComputer{{Color: 5}, {Color: 6}}
	r := onlineTestRequest(t, setup)
	r.RuleName, r.RuleBase, r.Community = StrictRuleSetName, MatchBaseStrict31, community.Features{}
	if _, err := ResolveMatchConfig(r); err == nil || !strings.Contains(err.Error(), "logical path seats[0],") {
		t.Fatalf("validation admitted two Strict computers on one host: %v", err)
	}
}

// refusedByPlaytestGate reports a refusal by the online composition's own
// gate rather than by admission's content checks.
func refusedByPlaytestGate(err error) bool {
	return errors.Is(err, ErrMatchConfigurationRejected) && strings.Contains(err.Error(), "logical path playtest,")
}

// The online composition admits the three reserved sets, binding the named
// one, and still refuses a set a mod registered, cheats, watching and
// Deathmatch under each.
func TestPlaytestAdmitsTheReservedRuleSets(t *testing.T) {
	for _, tc := range onlineRuleCases[1:] {
		setup := onlineTestSetup(false, 0, 0)
		setup.Rules = tc.rules
		inputs, config := onlineRulesFixtureMatch(t, setup)
		s, err := NewPlaytestSkirmish(inputs, config, 1, nil)
		if err != nil {
			t.Fatalf("%q: %v", tc.rules, err)
		}
		s.closeAIControllers()
		if s.Rules.Name != tc.rules || s.Gameplay != tc.mode {
			t.Fatalf("%q: composed under %q (%q)", tc.rules, s.Rules.Name, s.Gameplay)
		}
		for _, edit := range []struct {
			name string
			edit func(*MatchConfigRequest)
		}{
			{"cheats", func(r *MatchConfigRequest) { r.CheatsAllowed = true }},
			{"watching", func(r *MatchConfigRequest) { r.WatchingAllowed = true }},
			{"Deathmatch", func(r *MatchConfigRequest) { r.CommanderDeath = uint8(CommanderDeathDeathmatch) }},
		} {
			r := config.Request()
			edit.edit(&r)
			if _, err := NewPlaytestSkirmish(inputs, resolveMatch(t, r), 0, nil); !refusedByPlaytestGate(err) {
				t.Fatalf("%q with %s: %v", tc.rules, edit.name, err)
			}
		}
	}
	config := rehearsalFixtureConfig(t, func(c *SkirmishConfig, _ *SkirmishEntryOptions) { c.Gameplay = gameplay.Mode(testRuleSetName) })
	if got := config.Request().RuleName; got != testRuleSetName {
		t.Fatalf("the fixture names %q", got)
	}
	if _, err := NewPlaytestSkirmish(rehearsalFixtureInputs(t, config), config, 0, nil); !refusedByPlaytestGate(err) {
		t.Fatalf("a mod-registered set was composed online: %v", err)
	}
}

// Each set rehearses its own battle: within a set the digest is the same
// whichever seat a composition presents, and Modern's differs from the
// others'. The fixture's script only moves its weaponless commanders, which
// Strict 3.1 and Community 3.9 move alike; the retail test tells those two
// apart through construction and weapons.
func TestRehearsalDigestFollowsTheRuleSet(t *testing.T) {
	digests := map[gameplay.Mode][32]byte{}
	for _, tc := range onlineRuleCases[1:] {
		setup := onlineTestSetup(false, 0, 0)
		setup.Rules = tc.rules
		inputs, config := onlineRulesFixtureMatch(t, setup)
		digest := rehearsalDigestOf(t, inputs, config, 0)
		if got := rehearsalDigestOf(t, inputs, config, 1); got != digest {
			t.Fatalf("%q: seat 1's digest %x, want %x", tc.rules, got, digest)
		}
		digests[tc.mode] = digest
	}
	if modern := digests[gameplay.Modern]; modern == digests[gameplay.Strict31] || modern == digests[gameplay.Community39] {
		t.Fatal("Modern rehearses another set's battle")
	}
}
