package vfs_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Root precedence is Nanolathe host policy, including archive-over-loose
// resolution across roots (docs/DESIGN_CONTENT_VFS.md §5).
func TestOrderedRootsOverrideWholeEarlierRoot(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	archive := func(root, name, data string) {
		t.Helper()
		var buf bytes.Buffer
		if err := vfs.WriteArchive(&buf, []vfs.ArchiveFile{{Path: "shared.txt", Data: []byte(data)}}, vfs.ArchiveWriteOptions{}); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), buf.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	archive(first, "totala1.hpi", "first base")
	archive(first, "patch.ccx", "first expansion")
	archive(first, "rev31.gp3", "first patch")
	archive(second, "totala1.hpi", "second base")
	if err := os.WriteFile(filepath.Join(first, "shared.txt"), []byte("first loose"), 0600); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	defer fs.Close()
	if err := fs.MountGameDirectories([]string{first, second}); err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile("shared.txt")
	if err != nil || string(data) != "second base" {
		t.Fatalf("winner = %q, %v", data, err)
	}
	sources := fs.Sources("shared.txt")
	want := []string{filepath.Join(second, "totala1.hpi"), filepath.Join(first, "shared.txt"), filepath.Join(first, "rev31.gp3"), filepath.Join(first, "patch.ccx"), filepath.Join(first, "totala1.hpi")}
	var got []string
	for _, source := range sources {
		got = append(got, source.Source.SourcePath)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sources = %v, want %v", got, want)
	}
	// The same ordering must reach enumeration and the manifest.
	entries, err := fs.ReadDir("")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name == "shared.txt" && entry.Source.SourcePath != want[0] {
			t.Fatal("enumeration lost root priority")
		}
	}
	records, err := fs.Manifest(vfs.ManifestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.LogicalPath == "shared.txt" && (record.ProviderID != "totala1.hpi" || len(record.Shadowed) != 4) {
			t.Fatalf("manifest = %+v", record)
		}
	}
	reversed := vfs.New()
	defer reversed.Close()
	if err := reversed.MountGameDirectories([]string{second, first}); err != nil {
		t.Fatal(err)
	}
	data, err = reversed.ReadFile("shared.txt")
	if err != nil || string(data) != "first loose" {
		t.Fatalf("reversed winner = %q, %v", data, err)
	}
}

func TestOneRootPreservesExistingManifest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	original, layered := vfs.New(), vfs.New()
	defer original.Close()
	defer layered.Close()
	if err := original.MountGameDirectory(root); err != nil {
		t.Fatal(err)
	}
	if err := layered.MountGameDirectories([]string{root, root}); err != nil {
		t.Fatal(err)
	}
	a, err := original.ManifestHash()
	if err != nil {
		t.Fatal(err)
	}
	b, err := layered.ManifestHash()
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("single/repeated root changed manifest identity")
	}
}

// An online battle mounts the base install with only the archives its room
// names and none of its loose files, and the roots keep the priority spans
// their positions give them (docs/DESIGN_MULTIPLAYER.md §16.8).
func TestGameRootsAdmitNamedArchivesWithoutLoose(t *testing.T) {
	base, mod := t.TempDir(), t.TempDir()
	write := func(root, name string, files ...vfs.ArchiveFile) {
		t.Helper()
		var buf bytes.Buffer
		if err := vfs.WriteArchive(&buf, files, vfs.ArchiveWriteOptions{}); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), buf.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(base, "totala1.hpi", vfs.ArchiveFile{Path: "units/base.fbi", Data: []byte("base")}, vfs.ArchiveFile{Path: "shared.txt", Data: []byte("base")})
	write(base, "Extra.ufo", vfs.ArchiveFile{Path: "units/extra.fbi", Data: []byte("extra")})
	if err := os.MkdirAll(filepath.Join(base, "units"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "units", "loose.fbi"), []byte("loose"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "shared.txt"), []byte("mod"), 0600); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	defer fs.Close()
	admit := func(name string) bool { return name == "totala1.hpi" }
	if err := fs.MountGameRoots([]vfs.GameRoot{{Dir: base, Archives: admit, NoLoose: true}, {Dir: mod}}); err != nil {
		t.Fatal(err)
	}
	if data, err := fs.ReadFile("units/base.fbi"); err != nil || string(data) != "base" {
		t.Fatalf("named archive = %q, %v", data, err)
	}
	for _, left := range []string{"units/extra.fbi", "units/loose.fbi"} {
		if _, err := fs.Stat(left); err == nil {
			t.Fatalf("%s is mounted", left)
		}
	}
	if data, err := fs.ReadFile("shared.txt"); err != nil || string(data) != "mod" {
		t.Fatalf("later root winner = %q, %v", data, err)
	}
	whole := vfs.New()
	defer whole.Close()
	if err := whole.MountGameDirectories([]string{base, mod}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"units/base.fbi", "shared.txt"} {
		a, b := fs.Sources(path), whole.Sources(path)
		if len(a) == 0 || len(b) == 0 || a[0].Source.Priority != b[0].Source.Priority {
			t.Fatalf("%s: filtered winner %+v, whole %+v", path, a, b)
		}
	}
}

// An extra archive mounts as if it sat in the root: in its tier's lexical
// order by file name, replacing a same-named archive of the folder
// (docs/DESIGN_MODS_MUTATORS.md §5.7).
func TestGameRootExtraArchivesJoinTheFoldersTier(t *testing.T) {
	base, library := t.TempDir(), t.TempDir()
	write := func(root, name, data string) {
		t.Helper()
		var buf bytes.Buffer
		if err := vfs.WriteArchive(&buf, []vfs.ArchiveFile{{Path: "shared.txt", Data: []byte(data)}, {Path: name + ".txt", Data: []byte(data)}}, vfs.ArchiveWriteOptions{}); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), buf.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(base, "totala1.hpi", "base")
	write(base, "Alpha.ufo", "alpha")
	write(base, "Zulu.ufo", "zulu")
	write(base, "Mike.ufo", "folder mike")
	write(library, "MIKE.ufo", "library mike")
	write(library, "Bravo.ufo", "bravo")
	fs := vfs.New()
	defer fs.Close()
	extra := []string{filepath.Join(library, "MIKE.ufo"), filepath.Join(library, "Bravo.ufo")}
	if err := fs.MountGameRoots([]vfs.GameRoot{{Dir: base, Archives: func(name string) bool { return name != "Bravo.ufo" }, Extra: extra}}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, source := range fs.Sources("shared.txt") {
		got = append(got, source.Source.SourcePath)
	}
	want := []string{filepath.Join(base, "Zulu.ufo"), filepath.Join(library, "MIKE.ufo"), filepath.Join(library, "Bravo.ufo"), filepath.Join(base, "Alpha.ufo"), filepath.Join(base, "totala1.hpi")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sources = %v, want %v", got, want)
	}
	if _, err := fs.Stat("Bravo.ufo.txt"); err != nil {
		t.Fatal("the root's archive filter left out an extra archive")
	}
}

// An extra archive with a layout answers under the layout's directories: a
// content set that reads units from unitsE finds the archive's units/ files
// beside its own, through the layout's view under the retail name, while a
// root's units/ stay hidden and every other directory keeps its path
// (docs/DESIGN_MODS_MUTATORS.md §5.8 "With a mod").
func TestGameRootExtraLayoutRehomesAnArchive(t *testing.T) {
	base, mod, library := t.TempDir(), t.TempDir(), t.TempDir()
	write := func(path string, files []vfs.ArchiveFile) {
		t.Helper()
		var buf bytes.Buffer
		if err := vfs.WriteArchive(&buf, files, vfs.ArchiveWriteOptions{}); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, buf.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(base, "totala1.hpi"), []vfs.ArchiveFile{{Path: "units/armcom.fbi", Data: []byte("base")}})
	write(filepath.Join(mod, "mod.gp3"), []vfs.ArchiveFile{{Path: "unitsE/armcom.fbi", Data: []byte("mod")}})
	faction := filepath.Join(library, "faction.ufo")
	write(faction, []vfs.ArchiveFile{{Path: "Units/XenoCom.fbi", Data: []byte("xeno")}, {Path: "objects3d/xenocom.3do", Data: []byte("model")}})
	layout := vfs.NewLayout(map[string]string{"units": "unitsE"})
	fs := vfs.New()
	defer fs.Close()
	if err := fs.MountGameRoots([]vfs.GameRoot{
		{Dir: base, Extra: []string{faction}, ExtraLayout: func(full string) vfs.Layout {
			if full == faction {
				return layout
			}
			return vfs.Layout{}
		}},
		{Dir: mod},
	}); err != nil {
		t.Fatal(err)
	}
	view := layout.Apply(fs)
	entries, err := view.ReadDir("units")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Path)
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"units/armcom.fbi", "units/xenocom.fbi"}) {
		t.Fatalf("units = %v, want the mod's and the archive's", names)
	}
	if data, err := view.ReadFileLimit("units/xenocom.fbi", 64); err != nil || string(data) != "xeno" {
		t.Fatalf("rehomed file = %q, %v", data, err)
	}
	if data, err := view.ReadFileLimit("units/armcom.fbi", 64); err != nil || string(data) != "mod" {
		t.Fatalf("the mod's file = %q, %v", data, err)
	}
	if _, err := fs.Stat("units/xenocom.fbi"); err == nil {
		t.Fatal("the archive still answers under the renamed retail directory")
	}
	if _, err := view.Stat("objects3d/xenocom.3do"); err != nil {
		t.Fatalf("a directory the layout keeps moved: %v", err)
	}
	file, err := fs.Open("unitse/xenocom.fbi")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if info := file.Info(); info.Path != "unitse/xenocom.fbi" || info.Source.LogicalPath != "unitse/xenocom.fbi" || info.OriginalPath != "Units/XenoCom.fbi" {
		t.Fatalf("opened file info %+v", info)
	}
}

// A GameRoot ExtraTier override mounts an add-on layer above the retail
// tiers: a faction archive sits over a fix archive that replaced the patch,
// while the loose host files stay on top (docs/DESIGN_MODS_MUTATORS.md §5.7
// "Mounting", §5.8).
func TestGameRootExtraTierOrdersAnAddonLayer(t *testing.T) {
	base, library := t.TempDir(), t.TempDir()
	write := func(path string, files ...vfs.ArchiveFile) {
		t.Helper()
		var buf bytes.Buffer
		if err := vfs.WriteArchive(&buf, files, vfs.ArchiveWriteOptions{}); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, buf.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(base, "Alpha.ufo"), vfs.ArchiveFile{Path: "units/shared.fbi", Data: []byte("content")})
	write(filepath.Join(base, "rev31.gp3"), vfs.ArchiveFile{Path: "units/shared.fbi", Data: []byte("base patch")})
	fix := filepath.Join(library, "rev31.gp3")
	write(fix, vfs.ArchiveFile{Path: "units/shared.fbi", Data: []byte("fix")})
	faction := filepath.Join(library, "Faction.ufo")
	write(faction, vfs.ArchiveFile{Path: "units/shared.fbi", Data: []byte("faction")})
	overlay := vfs.DefaultRetailMountPlan().GP3 + 1

	fixOnly := vfs.New()
	defer fixOnly.Close()
	if err := fixOnly.MountGameRoots([]vfs.GameRoot{{Dir: base, Extra: []string{fix}}}); err != nil {
		t.Fatal(err)
	}
	if data, err := fixOnly.ReadFile("units/shared.fbi"); err != nil || string(data) != "fix" {
		t.Fatalf("the fix archive = %q, %v, want it over the base patch and the content archive", data, err)
	}

	layered := vfs.New()
	defer layered.Close()
	if err := layered.MountGameRoots([]vfs.GameRoot{{Dir: base, Extra: []string{fix, faction}, ExtraTier: func(full string) (vfs.MountTier, bool) {
		return overlay, full == faction
	}}}); err != nil {
		t.Fatal(err)
	}
	if data, err := layered.ReadFile("units/shared.fbi"); err != nil || string(data) != "faction" {
		t.Fatalf("the faction archive = %q, %v, want it over the fix archive", data, err)
	}

	bad := vfs.New()
	defer bad.Close()
	if err := bad.MountGameRoots([]vfs.GameRoot{{Dir: base, Extra: []string{faction}, ExtraTier: func(string) (vfs.MountTier, bool) {
		return vfs.DefaultRetailMountPlan().Loose, true
	}}}); err == nil {
		t.Fatal("an override at the loose tier was admitted")
	}
}

// LooseSkip leaves out the loose files under the top-level entries it names,
// and only those: a nested folder of the same name still mounts.
func TestGameRootLooseSkipLeavesOutTopLevelEntries(t *testing.T) {
	base := t.TempDir()
	for _, f := range []string{"unitsE/armcom.fbi", "units/armcom.fbi", "gamedata/unitsE/x.tdf", "readme.txt"} {
		path := filepath.Join(base, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(f), 0600); err != nil {
			t.Fatal(err)
		}
	}
	fs := vfs.New()
	defer fs.Close()
	skip := func(top string) bool { return top == "unitsE" }
	if err := fs.MountGameRoots([]vfs.GameRoot{{Dir: base, LooseSkip: skip}}); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Stat("unitse/armcom.fbi"); err == nil {
		t.Fatal("a skipped top-level folder mounted")
	}
	for _, kept := range []string{"units/armcom.fbi", "gamedata/unitse/x.tdf", "readme.txt"} {
		if _, err := fs.Stat(kept); err != nil {
			t.Fatalf("%s: %v", kept, err)
		}
	}
}
