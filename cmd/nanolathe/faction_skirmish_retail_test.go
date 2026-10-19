//go:build retail

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	contentprofiles "github.com/nanolathe-gg/nanolathe/internal/content/profiles"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
)

// The skirmish screen with faction add-ons enabled: its side button cycles
// through ARM, CORE and every enabled faction's sides, each with its own
// button frame, and the battle it starts gives every row its side's
// commander (docs/DESIGN_MODS_MUTATORS.md §5.8). NANOLATHE_FACTION_ADDONS
// lists converted add-on directories (cmd/factionpack), installed here into
// a scratch library; with NANOLATHE_SHOT_DIR set the screen is written there.
func TestSkirmishScreenOffersFactionSidesRetail(t *testing.T) {
	value := strings.TrimSpace(os.Getenv("NANOLATHE_FACTION_ADDONS"))
	if value == "" {
		t.Skip("NANOLATHE_FACTION_ADDONS is unset: the faction skirmish screen needs converted packages")
	}
	resetSaveLoadScreenState(t)
	root := testsupport.RetailRoot(t)
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	var selection []settings.AddonSelection
	for _, dir := range filepath.SplitList(value) {
		if err := runInstallAddon(Options{InstallAddon: dir}, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(dir, modlibrary.MetadataFile))
		if err != nil {
			t.Fatal(err)
		}
		meta, err := modlibrary.ParseMetadata(data)
		if err != nil {
			t.Fatal(err)
		}
		selection = append(selection, settings.AddonSelection{ID: meta.ID, Version: meta.Version})
	}
	opts := Options{Root: root, Addons: selection, AddonsSet: true}
	cs, err := openContent(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	if len(cs.addons) != len(selection) {
		t.Fatalf("mounted %d of %d faction add-ons: %v", len(cs.addons), len(selection), cs.notes)
	}
	shell, err := newGameShell(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	shell.opts.Root = t.TempDir()
	shell.settingsWritable = false
	cat, err := cs.compileCatalog(nil)
	if err != nil {
		t.Fatal(err)
	}
	if shell.skirmishSideCount() != len(cat.Sides) || len(cat.Sides) <= 2 {
		t.Fatalf("the side button cycles %d sides; the composed game has %d", shell.skirmishSideCount(), len(cat.Sides))
	}
	art := shell.assets.panel[modeMenuSkirmish].art
	entry, ok := art.Find("SIDEx")
	if !ok || len(entry.Frames) < len(cat.Sides)+1 {
		t.Fatalf("SIDEx holds %d frames for %d sides", len(entry.Frames), len(cat.Sides))
	}

	shot := survivalMenuShooter(t, shell)
	shell.activateGadget("SINGLE")
	shell.activateGadget("Skirmish")
	players := min(len(cat.Sides), 10)
	shell.setup.NumPlayers = players
	for i := 0; i < players; i++ {
		shell.setup.Players[i].Side = i
	}
	shell.openMenu(modeMenuSkirmish)
	for i := 1; i < players; i++ {
		shell.activateGadget("Player" + string(rune('0'+i))) // Open -> computer
	}
	shot("skirmish-factions")
	cfg := shell.skirmishConfigForStart(shell.setup.MapName)
	request, err := skirmishBattleRequest(shell.opts, shell.cs, cfg, headlessScenarioSkirmish, nil, newBattleSeedSource(Options{Seed: 5}))
	if err != nil {
		t.Fatal(err)
	}
	request.value.Gameplay = gameplay.Modern
	battle, err := headless.ComposeFreshBattle(request.value)
	if err != nil {
		t.Fatalf("the faction skirmish does not compose: %v", err)
	}
	s := battle.Session
	for owner := 0; owner < players; owner++ {
		side, ok := s.SideForOwner(owner)
		if !ok || side != owner {
			t.Fatalf("row %d plays side %d", owner, side)
		}
		want := strings.ToLower(cat.Sides[side].Commander)
		found := false
		for _, u := range s.Units.IterSliced() {
			if int(u.Owner) == owner && u.Def != nil && strings.ToLower(u.Def.UnitName) == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("row %d (%s) has no %s", owner, cat.Sides[side].Name, cat.Sides[side].Commander)
		}
	}
}

// A faction add-on the catalog compiler refuses — here a side with no font,
// which no mount check reads — is left off with a notice, and the add-ons
// that compile stay, so a broken package can never stop the start
// (docs/DESIGN_MODS_MUTATORS.md §5.8).
func TestResolveAddonsLeavesOffAFactionThatDoesNotCompileRetail(t *testing.T) {
	root := testsupport.RetailRoot(t)
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	side := func(name, extra string) string {
		return "[SIDE0]{name=" + name + ";commander=" + name + "COM;" + extra + authoredSideAnchors() + "}"
	}
	good := addonPackage(t, "good", "Good", map[string]string{"factions/good/sidedata.tdf": side("GOOD", "font=console;fontgui=armbutt;")})
	bad := addonPackage(t, "bad", "Bad", map[string]string{"factions/bad/sidedata.tdf": side("BAD", "")})
	for _, dir := range []string{bad, good} {
		if err := runInstallAddon(Options{InstallAddon: dir}, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
	}
	opts := Options{AddonsSet: true, Addons: []settings.AddonSelection{{ID: "bad", Version: "1"}, {ID: "good", Version: "1"}}}
	out, err := resolveAddons(opts, contentprofiles.Retail(), []string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.mounted) != 1 || out.mounted[0].ID != "good" || len(out.notes) != 1 ||
		!strings.HasPrefix(out.notes[0], "nanolathe: official add-on Bad not mounted: the game data does not compile with it: ") {
		t.Fatalf("mounted %+v, notes %q", out.mounted, out.notes)
	}
}
