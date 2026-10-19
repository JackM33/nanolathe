package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/addonlibrary"
	contentprofiles "github.com/nanolathe-gg/nanolathe/internal/content/profiles"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// addonArchive is one authored *.UFO holding files.
func addonArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var list []vfs.ArchiveFile
	for name, data := range files {
		list = append(list, vfs.ArchiveFile{Path: name, Data: []byte(data)})
	}
	var buf bytes.Buffer
	if err := vfs.WriteArchive(&buf, list, vfs.ArchiveWriteOptions{}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// addonPackage writes an add-on package folder: identity-only metadata, one
// archive holding files and a readme.
func addonPackage(t *testing.T, id, name string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), id)
	meta, _ := json.Marshal(modlibrary.Metadata{Schema: 1, ID: id, Name: name, Version: "1"})
	writeModFixtureFile(t, dir, modlibrary.MetadataFile, string(meta))
	writeModFixtureFile(t, dir, "README.txt", "authored fixture\n")
	writeModFixtureFile(t, dir, id+".ufo", string(addonArchive(t, files)))
	return dir
}

// xenoFiles are a faction add-on's authored files: a declaration with one
// side and two units of it.
func xenoFiles() map[string]string {
	return map[string]string{
		"factions/xeno/sidedata.tdf": "[SIDE0]{name=Xeno;font=fixture;" + authoredSideAnchors() + "}",
		"units/xenocom.fbi":          "[UNITINFO]{UnitName=xenocom;Name=Xeno Commander;side=XENO;}",
		"units/xenotank.fbi":         "[UNITINFO]{UnitName=xenotank;Name=Xeno Tank;side=xeno;}",
	}
}

// --install-addon installs a local folder or zip into the add-on library,
// says what it holds and refuses the same version again (§5.7, §5.8).
func TestInstallAddonInstallsALocalPackage(t *testing.T) {
	modFixture(t)
	dir := addonPackage(t, "xeno", "Xeno", xenoFiles())
	var out bytes.Buffer
	if err := runInstallAddon(Options{InstallAddon: dir}, &out); err != nil {
		t.Fatal(err)
	}
	if text := out.String(); !strings.Contains(text, "installed add-on xeno@1 (Xeno)") || !strings.Contains(text, "faction xeno: sides Xeno; 2 units") || !strings.Contains(text, "--addon xeno@1") {
		t.Fatalf("output %q", text)
	}
	if err := runInstallAddon(Options{InstallAddon: dir}, &out); !errors.Is(err, modlibrary.ErrAlreadyInstalled) {
		t.Fatalf("a second install = %v, want already installed", err)
	}
	// A zip installs the same way; one holding loose content is refused.
	zipped := func(name string, files map[string][]byte) string {
		var buf bytes.Buffer
		z := zip.NewWriter(&buf)
		for file, data := range files {
			f, err := z.Create(file)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = f.Write(data)
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	meta, _ := json.Marshal(modlibrary.Metadata{Schema: 1, ID: "plain", Name: "Plain", Version: "2"})
	archive := addonArchive(t, map[string]string{"units/plainx.fbi": "[UNITINFO]{UnitName=plainx;side=ARM;}"})
	out.Reset()
	if err := runInstallAddon(Options{InstallAddon: zipped("plain-2.zip", map[string][]byte{modlibrary.MetadataFile: meta, "plain.ufo": archive})}, &out); err != nil {
		t.Fatal(err)
	}
	if text := out.String(); !strings.Contains(text, "installed add-on plain@2 (Plain)") || strings.Contains(text, "faction") {
		t.Fatalf("output %q", text)
	}
	meta, _ = json.Marshal(modlibrary.Metadata{Schema: 1, ID: "loose", Name: "Loose", Version: "1"})
	if err := runInstallAddon(Options{InstallAddon: zipped("loose-1.zip", map[string][]byte{modlibrary.MetadataFile: meta, "loose.ufo": archive, "units/loose.fbi": []byte("x")})}, &out); err == nil {
		t.Fatal("a package with loose content was installed")
	}
	root, _ := addonlibrary.DefaultRoot()
	if addons, _, err := addonlibrary.Installed(root); err != nil || len(addons) != 2 {
		t.Fatalf("library = %+v, %v", addons, err)
	}
}

// Add-ons are kept apart from each other as from the base: one that supplies
// a logical path, or defines a unit or feature name, an add-on earlier in the
// selection also does is left off with a notice naming both. A faction
// add-on mounts under a mod, its files under the mod's layout, unless it
// defines a name the mod also defines (§5.8 "With a mod").
func TestResolveAddonsKeepsAddonsApart(t *testing.T) {
	base, mods := modFixture(t)
	packages := map[string]string{
		"alpha": addonPackage(t, "alpha", "Alpha", map[string]string{"gamedata/shared.tdf": "a", "units/aaa.fbi": "[UNITINFO]{UnitName=aaa;}"}),
		"beta":  addonPackage(t, "beta", "Beta", map[string]string{"Gamedata/SHARED.tdf": "b", "units/bbb.fbi": "[UNITINFO]{UnitName=bbb;}"}),
		"gamma": addonPackage(t, "gamma", "Gamma", map[string]string{"units/other.fbi": "[UNITINFO]{UnitName=AAA;}"}),
		"xeno":  addonPackage(t, "xeno", "Xeno", xenoFiles()),
	}
	for _, id := range []string{"alpha", "beta", "gamma", "xeno"} {
		if err := runInstallAddon(Options{InstallAddon: packages[id]}, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
	}
	resolveWith := func(profile contentprofiles.Profile, mod *modlibrary.Mod, ids ...string) ([]string, []string) {
		t.Helper()
		opts := Options{AddonsSet: true, Addons: []settings.AddonSelection{}}
		for _, id := range ids {
			opts.Addons = append(opts.Addons, settings.AddonSelection{ID: id, Version: "1"})
		}
		out, err := resolveAddons(opts, profile, []string{base}, mod)
		if err != nil {
			t.Fatal(err)
		}
		var mounted []string
		for _, a := range out.mounted {
			mounted = append(mounted, a.ID)
		}
		if len(out.selection) != len(ids) {
			t.Fatalf("selection %+v, want every installed add-on asked for", out.selection)
		}
		return mounted, out.notes
	}
	resolve := func(mod *modlibrary.Mod, ids ...string) ([]string, []string) {
		t.Helper()
		return resolveWith(contentprofiles.Retail(), mod, ids...)
	}
	mounted, notes := resolve(nil, "alpha", "beta", "gamma", "xeno")
	if strings.Join(mounted, ",") != "alpha,xeno" || len(notes) != 2 ||
		notes[0] != "nanolathe: official add-on Beta not mounted: the enabled add-on Alpha also supplies Gamedata/SHARED.tdf" ||
		notes[1] != "nanolathe: official add-on Gamma not mounted: the enabled add-on Alpha already defines unit aaa" {
		t.Fatalf("mounted %v, notes %q", mounted, notes)
	}
	// The selection's order decides which of two keeps its own; the notice
	// spells the path as the add-on left off holds it.
	if mounted, notes := resolve(nil, "beta", "alpha"); strings.Join(mounted, ",") != "beta" || len(notes) != 1 || !strings.Contains(notes[0], "Alpha not mounted: the enabled add-on Beta also supplies gamedata/shared.tdf") {
		t.Fatalf("mounted %v, notes %q", mounted, notes)
	}
	// One add-on alone mounts as before.
	if mounted, notes := resolve(nil, "beta"); strings.Join(mounted, ",") != "beta" || len(notes) != 0 {
		t.Fatalf("mounted %v, notes %q", mounted, notes)
	}
	// A faction add-on never replaces a base file, even one it is alone in
	// supplying among the add-ons (§5.8 isolation rule 1).
	clobber := xenoFiles()
	clobber["GameData/SideData.tdf"] = "[SIDE0]{name=Hijack;}"
	delete(clobber, "factions/xeno/sidedata.tdf")
	clobber["factions/zeta/sidedata.tdf"] = xenoFiles()["factions/xeno/sidedata.tdf"]
	if err := runInstallAddon(Options{InstallAddon: addonPackage(t, "zeta", "Zeta", clobber)}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if mounted, notes := resolve(nil, "zeta"); len(mounted) != 0 || len(notes) != 1 || notes[0] != "nanolathe: official add-on Zeta not mounted: the installed content already supplies GameData/SideData.tdf" {
		t.Fatalf("a faction replacing a base file: mounted %v, notes %q", mounted, notes)
	}
	// Skirmish keeps ordinal 10 for no side, so a mount never composes more
	// than 10 sides; a faction that would pass that stays off (§5.8).
	var many strings.Builder
	for i := 0; i <= 10; i++ {
		many.WriteString("[SIDE" + strconv.Itoa(i) + "]{name=Omega" + strconv.Itoa(i) + ";font=fixture;" + authoredSideAnchors() + "}")
	}
	if err := runInstallAddon(Options{InstallAddon: addonPackage(t, "omega", "Omega", map[string]string{"factions/omega/sidedata.tdf": many.String()})}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if mounted, notes := resolve(nil, "xeno", "omega"); strings.Join(mounted, ",") != "xeno" || len(notes) != 1 || notes[0] != "nanolathe: official add-on Omega not mounted: its 11 sides would take the battle past 10 sides" {
		t.Fatalf("too many sides: mounted %v, notes %q", mounted, notes)
	}
	mod := installFixtureMod(t, mods, "plain", &modlibrary.Metadata{Schema: 1, ID: "plain", Name: "Plain", Version: "1"})
	if mounted, notes := resolve(&mod, "alpha", "xeno"); strings.Join(mounted, ",") != "alpha,xeno" || len(notes) != 0 {
		t.Fatalf("under a mod: mounted %v, notes %q", mounted, notes)
	}
	renamed := contentprofiles.Retail()
	renamed.Directories = map[string]string{"units": "unitsM"}
	if mounted, notes := resolveWith(renamed, &mod, "alpha", "xeno"); strings.Join(mounted, ",") != "alpha,xeno" || len(notes) != 0 {
		t.Fatalf("under a renaming layout: mounted %v, notes %q", mounted, notes)
	}
	rival := filepath.Join(t.TempDir(), "rival")
	writeModFixtureFile(t, rival, "units/rivaltank.fbi", "[UNITINFO]{UnitName=XENOTANK;Name=Rival Tank;side=ARM;}")
	meta, _ := json.Marshal(modlibrary.Metadata{Schema: 1, ID: "rival", Name: "Rival", Version: "1"})
	writeModFixtureFile(t, rival, modlibrary.MetadataFile, string(meta))
	rivalMod, err := mods.InstallDirectory(rival, modlibrary.InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if mounted, notes := resolve(&rivalMod, "xeno"); len(mounted) != 0 || len(notes) != 1 || !strings.Contains(notes[0], "Xeno not mounted: the installed content already defines unit xenotank at another path") {
		t.Fatalf("a faction sharing a mod's unit name: mounted %v, notes %q", mounted, notes)
	}
}

// A faction add-on's row reads its sides and unit count in place of the
// unit names; other add-ons keep their unit names (§5.8).
func TestNLScreenAddonRowNamesTheFaction(t *testing.T) {
	dir := t.TempDir()
	xeno := filepath.Join(dir, "xeno.ufo")
	plain := filepath.Join(dir, "plain.ufo")
	if err := os.WriteFile(xeno, addonArchive(t, xenoFiles()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plain, addonArchive(t, map[string]string{"units/armfark.fbi": "[UNITINFO]{UnitName=ARMFARK;Name=FARK;side=ARM;}"}), 0o644); err != nil {
		t.Fatal(err)
	}
	rows := nlAddonRowsOf([]knownAddon{
		{id: "xeno", version: "1", name: "Xeno", installed: &addonlibrary.Addon{ID: "xeno", Paths: []string{xeno}}},
		{id: "plain", version: "1", name: "Plain", installed: &addonlibrary.Addon{ID: "plain", Paths: []string{plain}}},
	}, "")
	s := &nlScreen{}
	if len(rows) != 2 || len(rows[0].units) != 2 || s.addonSubText(rows[0]) != "Faction: Xeno · 2 units" || s.addonSubText(rows[1]) != "FARK" {
		t.Fatalf("rows %+v", rows)
	}
}

// --sides sets the --map skirmish's two rows by side name, without regard to
// case, or by ordinal, a faction's sides included, and an explicit 0
// survives battle composition's defaults; a side the content lacks fails the
// start naming the side tables searched (§5.8).
func TestSidesChooseTheMapSkirmishSides(t *testing.T) {
	base := t.TempDir()
	writeModFixtureFile(t, base, "gamedata/sidedata.tdf", "[SIDE0]{name=ARM;font=fixture;"+authoredSideAnchors()+"}[SIDE1]{name=CORE;font=fixture;"+authoredSideAnchors()+"}")
	faction := filepath.Join(t.TempDir(), "xeno.ufo")
	if err := os.WriteFile(faction, addonArchive(t, xenoFiles()), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	t.Cleanup(func() { fs.Close() })
	if err := fs.MountDirectory(base, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.MountArchive(faction, 20); err != nil {
		t.Fatal(err)
	}
	cs := testContentSet(fs)
	sides := func(choice ...string) ([2]int, error) {
		request, err := directMapBattleRequest(Options{Map: "fixture", Seed: 1, Sides: choice}, cs, nil)
		if err != nil {
			return [2]int{}, err
		}
		players := request.value.Skirmish.Players
		return [2]int{players[0].Side, players[1].Side}, nil
	}
	for _, tc := range []struct {
		choice []string
		want   [2]int
	}{{nil, [2]int{0, 1}}, {[]string{"xeno", "arm"}, [2]int{2, 0}}, {[]string{"Core", "2"}, [2]int{1, 2}}, {[]string{"1", "0"}, [2]int{1, 0}}} {
		if got, err := sides(tc.choice...); err != nil || got != tc.want {
			t.Errorf("--sides %v = %v, %v; want %v", tc.choice, got, err, tc.want)
		}
	}
	for _, bad := range [][]string{{"arm", "zork"}, {"3", "arm"}, {"-1", "arm"}} {
		_, err := sides(bad...)
		if err == nil || !strings.Contains(err.Error(), "nanolathe: skirmish side is not defined: logical path --sides ") || !strings.Contains(err.Error(), "providers searched [gamedata/sidedata.tdf, factions/xeno/sidedata.tdf]") || !strings.Contains(err.Error(), "2 Xeno") {
			t.Errorf("--sides %v: %v", bad, err)
		}
	}
	for _, args := range [][]string{
		{"--map", "x", "--sides", "arm"},
		{"--map", "x", "--sides", "arm,core,xeno"},
		{"--map", "x", "--survival", "--sides", "arm,core"},
		{"--sides", "arm,core"},
	} {
		if _, err := parseFlags(args, &bytes.Buffer{}); err == nil {
			t.Errorf("parseFlags %v admitted", args)
		}
	}
	if opts, err := parseFlags([]string{"--map", "x", "--sides", " xeno , 0 "}, &bytes.Buffer{}); err != nil || strings.Join(opts.Sides, ",") != "xeno,0" {
		t.Fatalf("parseFlags = %v, %v", opts.Sides, err)
	}
}
