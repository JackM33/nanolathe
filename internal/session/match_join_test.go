package session

import (
	"crypto/sha256"
	"errors"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/netproto"
	"github.com/nanolathe-gg/nanolathe/internal/version"
)

// Identity comparison tests of DESIGN_MULTIPLAYER §8.2 and §8.8 and contract
// M2-C10. The identities are Nanolathe protocol values, not retail data.

// joinRelease is a stamped common release manifest admitting two platform
// variants.
func joinRelease() version.BuildManifest {
	return version.BuildManifest{
		SourceTree: sha256.Sum256([]byte("release source")),
		GoVersion:  "go1.27.1",
		GoMod:      sha256.Sum256([]byte("go.mod")),
		GoSum:      sha256.Sum256([]byte("go.sum")),
		BuildArgs:  []string{"-mod=readonly", "-trimpath", "./cmd/nanolathe"},
		Variants: []version.BuildVariant{
			{GOOS: "darwin", GOARCH: "arm64", ArchitectureLevel: "v8.0", ToolchainArchive: sha256.Sum256([]byte("darwin"))},
			{GOOS: "linux", GOARCH: "amd64", ArchitectureLevel: "v1", ToolchainArchive: sha256.Sum256([]byte("linux"))},
		},
	}
}

func joinDigest(t *testing.T, m version.BuildManifest) [32]byte {
	t.Helper()
	d, err := m.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// joinPair is two seats of one match on two installs: the configuration is
// the host's, and the other seat decodes it from its bytes and freezes its
// own content from it.
func joinPair(t *testing.T) (local MatchJoin, remote MatchJoin) {
	t.Helper()
	config := admitConfig(t, DirectSkirmishConfig(admitFixtureMap), SkirmishEntryOptions{}, 0)
	local = MatchJoin{Build: MatchBuild{Running: joinRelease()}, Inputs: newAdmitFixture(t).freeze(t, config), Config: config}
	payload, err := EncodeMatchConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeMatchConfig(payload)
	if err != nil {
		t.Fatal(err)
	}
	other := newAdmitFixture(t)
	inputs, err := FreezeMatchInputs(other.fs, other.cat, decoded, nil)
	if err != nil {
		t.Fatalf("freeze the decoded configuration's content: %v", err)
	}
	if err := ValidateMatchInputs(decoded, inputs); err != nil {
		t.Fatalf("the decoded configuration does not admit its own content: %v", err)
	}
	remote = MatchJoin{Build: MatchBuild{Running: joinRelease()}, Inputs: inputs, Config: decoded}
	return local, remote
}

func joinIdentity(t *testing.T, j MatchJoin) netproto.Identity {
	t.Helper()
	id, err := j.Identity()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

var joinKinds = []error{ErrMatchProtocolMismatch, ErrMatchBuildMismatch, ErrMatchContentMismatch, ErrMatchMapMismatch,
	ErrMatchRulesMismatch, ErrMatchModMismatch, ErrMatchConfigurationRejected}

// requireJoinKinds checks that err wraps exactly the wanted categories, each
// refusal in the join's diagnostic shape.
func requireJoinKinds(t *testing.T, name string, err error, want ...error) {
	t.Helper()
	if len(want) == 0 {
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return
	}
	if err == nil {
		t.Fatalf("%s: admitted", name)
	}
	for _, kind := range joinKinds {
		wanted := false
		for _, w := range want {
			wanted = wanted || w == kind
		}
		if errors.Is(err, kind) != wanted {
			t.Fatalf("%s: %v\nwraps %q is %v, want %v", name, err, kind, !wanted, wanted)
		}
	}
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok || len(joined.Unwrap()) != len(want) {
		t.Fatalf("%s: %v is not one refusal per category", name, err)
	}
	for _, e := range joined.Unwrap() {
		if !strings.Contains(e.Error(), "logical path ") || !strings.Contains(e.Error(), "providers searched [match identity v1, this seat, other seat], expected ") {
			t.Fatalf("%s: diagnostic shape %q", name, e)
		}
	}
}

// Two seats of one match on two installs, the second decoding the
// configuration from its bytes and freezing its own content from it, report
// one identity and admit each other (M2-C10).
func TestMatchJoinAdmitsOneMatch(t *testing.T) {
	local, remote := joinPair(t)
	id := joinIdentity(t, local)
	if id != joinIdentity(t, remote) {
		t.Fatalf("two installs of one match report different identities:\n%+v\n%+v", id, joinIdentity(t, remote))
	}
	r := local.Config.Request()
	if id.Protocol != netproto.CommandSchemaVersion || id.Build != joinDigest(t, joinRelease()) || id.Content != local.Inputs.Digest() ||
		id.Map != MatchMapIdentity(local.Inputs) || id.Configuration != local.Config.Digest() ||
		id.Rules != (netproto.RuleIdentity{Name: r.RuleName, Base: uint8(r.RuleBase), Community: communityDigest(r.Community)}) || id.Mod != (netproto.ModIdentity{}) {
		t.Fatalf("identity %+v does not name its sources", id)
	}
	if id.Map == ([32]byte{}) || id.Map == id.Content {
		t.Fatal("the map identity is not its own value")
	}
	requireJoinKinds(t, "one match", CompareMatchIdentity(local, joinIdentity(t, remote)))
	requireJoinKinds(t, "one match, the other way", CompareMatchIdentity(remote, id))
}

// Every category is compared and reported on its own: a remote differing in
// everything is refused in all seven categories at once, each with its own
// diagnostic, and a remote differing in one field is refused in that
// category alone.
func TestMatchJoinReportsEveryCategorySeparately(t *testing.T) {
	local, remote := joinPair(t)
	good := joinIdentity(t, remote)
	other := [32]byte{0xee}
	edits := []struct {
		kind error
		edit func(*netproto.Identity)
	}{
		{ErrMatchProtocolMismatch, func(id *netproto.Identity) { id.Protocol = netproto.CommandSchemaVersion + 1 }},
		{ErrMatchBuildMismatch, func(id *netproto.Identity) { id.Build = other }},
		{ErrMatchContentMismatch, func(id *netproto.Identity) { id.Content = other }},
		{ErrMatchMapMismatch, func(id *netproto.Identity) { id.Map = other }},
		{ErrMatchRulesMismatch, func(id *netproto.Identity) { id.Rules.Name = StrictRuleSetName }},
		{ErrMatchModMismatch, func(id *netproto.Identity) {
			id.Mod = netproto.ModIdentity{ID: "prota", Version: "4.8", Archive: other}
		}},
		{ErrMatchConfigurationRejected, func(id *netproto.Identity) { id.Configuration = other }},
	}
	all := good
	for _, e := range edits {
		one := good
		e.edit(&one)
		e.edit(&all)
		requireJoinKinds(t, "one field", CompareMatchIdentity(local, one), e.kind)
	}
	err := CompareMatchIdentity(local, all)
	requireJoinKinds(t, "every field", err, joinKinds...)
	seen := map[string]bool{}
	for _, e := range err.(interface{ Unwrap() []error }).Unwrap() {
		if seen[e.Error()] {
			t.Fatalf("two categories share the diagnostic %q", e)
		}
		seen[e.Error()] = true
	}
	// The rules identity's three parts each count.
	for _, edit := range []func(*netproto.Identity){
		func(id *netproto.Identity) { id.Rules.Base = uint8(MatchBaseStrict31) },
		func(id *netproto.Identity) { id.Rules.Community = other },
	} {
		one := good
		edit(&one)
		requireJoinKinds(t, "rules part", CompareMatchIdentity(local, one), ErrMatchRulesMismatch)
	}
}

// Matching digests excuse nothing: a remote whose configuration and content
// digests match is still refused for its map, its rules or its mod, and a
// matching configuration does not excuse different content (M2-C10).
func TestMatchJoinDoesNotShortCircuitOnMatchingDigests(t *testing.T) {
	local, remote := joinPair(t)
	good := joinIdentity(t, remote)
	for _, tc := range []struct {
		name string
		kind error
		edit func(*netproto.Identity)
	}{
		{"map beside matching content and configuration", ErrMatchMapMismatch, func(id *netproto.Identity) { id.Map[0] ^= 1 }},
		{"rules beside a matching configuration", ErrMatchRulesMismatch, func(id *netproto.Identity) { id.Rules.Community[0] ^= 1 }},
		{"mod beside a matching configuration", ErrMatchModMismatch, func(id *netproto.Identity) { id.Mod.Version = "4.9" }},
		{"content beside a matching configuration and map", ErrMatchContentMismatch, func(id *netproto.Identity) { id.Content[31] ^= 1 }},
	} {
		id := good
		tc.edit(&id)
		if id.Configuration != good.Configuration {
			t.Fatal("the edit moved the configuration digest")
		}
		requireJoinKinds(t, tc.name, CompareMatchIdentity(local, id), tc.kind)
	}
}

// In a normal room an unstamped or dirty build is not refused and reports a
// zero build identity, because the rehearsal rather than a stamp shows that
// two builds agree (DESIGN_MULTIPLAYER §16.7). A development room's explicit
// common manifest is a parameter of the comparison, never an environment
// flag. Platform variants live inside one common manifest, so another
// admitted variant of the same release reports the same identity, while a
// manifest admitting other variants is another release (M2-C10).
func TestMatchJoinBuildAdmission(t *testing.T) {
	local, remote := joinPair(t)
	release := joinRelease()
	unstamped := version.BuildManifest{GoVersion: "go1.27.1", Variants: []version.BuildVariant{{GOOS: "darwin", GOARCH: "arm64", ArchitectureLevel: "v8.0"}}}

	dirty, dirtyRemote := local, remote
	dirty.Build, dirtyRemote.Build = MatchBuild{Running: unstamped}, MatchBuild{Running: unstamped}
	if id := joinIdentity(t, dirty); id.Build != ([32]byte{}) || id.Content != local.Inputs.Digest() || id.Configuration != local.Config.Digest() {
		t.Fatalf("an unstamped build in a normal room reported %+v, want a zero build beside its other identities", id)
	}
	requireJoinKinds(t, "two unstamped seats", CompareMatchIdentity(dirty, joinIdentity(t, dirtyRemote)))
	// The other categories are still compared beside an advisory build.
	other := joinIdentity(t, dirtyRemote)
	other.Content[0] ^= 1
	requireJoinKinds(t, "unstamped seats with different content", CompareMatchIdentity(dirty, other), ErrMatchContentMismatch)

	dev := release
	dev.SourceTree = sha256.Sum256([]byte("development source inventory"))
	room := MatchBuild{Running: unstamped, Development: &dev}
	devLocal, devRemote := local, remote
	devLocal.Build, devRemote.Build = room, room
	if id := joinIdentity(t, devLocal); id.Build != joinDigest(t, dev) {
		t.Fatal("a development room's seat does not report the room's common manifest")
	}
	requireJoinKinds(t, "development room", CompareMatchIdentity(devLocal, joinIdentity(t, devRemote)))
	requireJoinKinds(t, "a release seat in a development room", CompareMatchIdentity(devLocal, joinIdentity(t, remote)), ErrMatchBuildMismatch)
	matching := devLocal
	matching.Build = MatchBuild{Running: dev, Development: &dev}
	requireJoinKinds(t, "the development build stamped", CompareMatchIdentity(matching, joinIdentity(t, devRemote)))
	for _, tc := range []struct {
		name  string
		build MatchBuild
	}{
		{"a stamped release posing as the development build", MatchBuild{Running: release, Development: &dev}},
		{"a development room without a source inventory", MatchBuild{Running: unstamped, Development: &unstamped}},
	} {
		j := devLocal
		j.Build = tc.build
		requireJoinKinds(t, tc.name, CompareMatchIdentity(j, joinIdentity(t, devRemote)), ErrMatchBuildMismatch)
	}

	// The other seat runs the linux/amd64 binary of the same release: the
	// identity is the common manifest's, so it is admitted.
	stamp, err := version.EncodeBuildManifest(release)
	if err != nil {
		t.Fatal(err)
	}
	onLinux, err := version.DecodeBuildManifest(stamp)
	if err != nil {
		t.Fatal(err)
	}
	linuxSeat := remote
	linuxSeat.Build = MatchBuild{Running: onLinux}
	requireJoinKinds(t, "another admitted variant", CompareMatchIdentity(local, joinIdentity(t, linuxSeat)))
	widened := joinRelease()
	widened.Variants = append(widened.Variants, version.BuildVariant{GOOS: "windows", GOARCH: "amd64", ArchitectureLevel: "v1", ToolchainArchive: sha256.Sum256([]byte("windows"))})
	widenedSeat := remote
	widenedSeat.Build = MatchBuild{Running: widened}
	requireJoinKinds(t, "another release's variant list", CompareMatchIdentity(local, joinIdentity(t, widenedSeat)), ErrMatchBuildMismatch)
}

// The mod is the one this seat's mount applied, as its mod library names it:
// it must be the configuration's and the other seat's.
func TestMatchJoinComparesTheMountedMod(t *testing.T) {
	local, remote := joinPair(t)
	prota := MatchMod{ID: "prota", Version: "4.8", Archive: sha256.Sum256([]byte("prota archive"))}
	mounted := local
	mounted.Mod = prota
	if _, err := mounted.Identity(); !errors.Is(err, ErrMatchModMismatch) {
		t.Fatalf("a mount of another mod than the configuration's reported an identity: %v", err)
	}
	requireJoinKinds(t, "mounted mod not the configuration's", CompareMatchIdentity(mounted, joinIdentity(t, remote)), ErrMatchModMismatch)
	other := joinIdentity(t, remote)
	other.Mod = netproto.ModIdentity{ID: prota.ID, Version: prota.Version, Archive: prota.Archive}
	requireJoinKinds(t, "the other seat mounted a mod", CompareMatchIdentity(local, other), ErrMatchModMismatch)
}

// A value this seat lacks is refused in its own categories instead of being
// compared, and the other categories are still compared.
func TestMatchJoinRefusesMissingLocalValues(t *testing.T) {
	local, remote := joinPair(t)
	id := joinIdentity(t, remote)
	noInputs := local
	noInputs.Inputs = nil
	requireJoinKinds(t, "no frozen inputs", CompareMatchIdentity(noInputs, id), ErrMatchContentMismatch, ErrMatchMapMismatch)
	noConfig := local
	noConfig.Config = EffectiveMatchConfig{}
	id.Protocol = 7
	requireJoinKinds(t, "no configuration", CompareMatchIdentity(noConfig, id), ErrMatchProtocolMismatch, ErrMatchRulesMismatch, ErrMatchConfigurationRejected)
	if _, err := (MatchJoin{}).Identity(); err == nil {
		t.Fatal("an empty seat reported an identity")
	}
}

// The public front half refuses what it cannot freeze in the project's
// diagnostic shape: no configuration, no mounted content, or a configuration
// naming a map this install lacks.
func TestFreezeMatchInputsRefusals(t *testing.T) {
	f := newAdmitFixture(t)
	config := admitConfig(t, DirectSkirmishConfig(admitFixtureMap), SkirmishEntryOptions{}, 0)
	if _, err := FreezeMatchInputs(f.fs, f.cat, EffectiveMatchConfig{}, nil); !errors.Is(err, ErrMatchConfigurationRejected) {
		t.Fatalf("no configuration: %v", err)
	}
	if _, err := FreezeMatchInputs(nil, f.cat, config, nil); !errors.Is(err, ErrMatchContentMismatch) {
		t.Fatalf("no filesystem: %v", err)
	}
	missing := admitConfig(t, DirectSkirmishConfig("elsewhere"), SkirmishEntryOptions{}, 0)
	_, err := FreezeMatchInputs(f.fs, f.cat, missing, nil)
	if !errors.Is(err, ErrMatchContentMismatch) || !strings.Contains(err.Error(), "logical path inputs, providers searched [match configuration v1, simulation content none], expected ") ||
		!strings.Contains(err.Error(), `"elsewhere"`) {
		t.Fatalf("a map this install lacks: %v", err)
	}
}
