package addonlibrary

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func ufo(t *testing.T, files map[string]string) []byte {
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

func write(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func archiveOf(name string, data []byte) Archive {
	return Archive{Name: name, Size: int64(len(data)), SHA256: sha256.Sum256(data)}
}

// fixture writes a staged add-on package holding a FARK-like unit archive
// and returns its root, metadata and expected archives.
func fixture(t *testing.T) (string, modlibrary.Metadata, map[string][]byte) {
	t.Helper()
	root := t.TempDir()
	meta := modlibrary.Metadata{Schema: 1, ID: "bonus", Name: "Bonus units", Version: "original"}
	raw, _ := json.Marshal(meta)
	write(t, root, modlibrary.MetadataFile, raw)
	write(t, root, "readme.txt", []byte("notes"))
	archives := map[string][]byte{
		"AFark.ufo":   ufo(t, map[string]string{"units/armfark.fbi": "[UNITINFO]{UnitName=ARMFARK;}", "features/corpses/armfark_dead.tdf": "[ARMFARK_DEAD]{}"}),
		"corplas.ufo": ufo(t, map[string]string{"units/corplas.fbi": "[UNITINFO]{UnitName=CORPLAS;}"}),
	}
	for name, data := range archives {
		write(t, root, name, data)
	}
	return root, meta, archives
}

func expected(archives map[string][]byte) []Archive {
	var out []Archive
	for name, data := range archives {
		out = append(out, archiveOf(name, data))
	}
	return out
}

func TestValidatorAdmitsExactlyTheListedArchives(t *testing.T) {
	root, meta, archives := fixture(t)
	if err := Validator(expected(archives))(root, meta); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func([]Archive) []Archive{
		"missing archive": func(a []Archive) []Archive { return a[:1] },
		"other bytes":     func(a []Archive) []Archive { a[0].SHA256[0] ^= 1; return a },
		"extra archive":   func(a []Archive) []Archive { return append(a, Archive{Name: "x.ufo", Size: 1}) },
	} {
		if err := Validator(change(expected(archives)))(root, meta); err == nil {
			t.Errorf("%s: admitted", name)
		}
	}
	for name, add := range map[string]string{"nested file": "units/armfark.fbi", "other archive": "Extra.hpi", "loose content": "armfark.fbi"} {
		t.Run(name, func(t *testing.T) {
			root, meta, archives := fixture(t)
			write(t, root, add, []byte("x"))
			if err := Validator(expected(archives))(root, meta); err == nil {
				t.Fatal("admitted")
			}
		})
	}
	root, meta, archives = fixture(t)
	meta.Summary = "x"
	raw := []byte(`{"schema":1,"id":"bonus","name":"Bonus units","version":"original","config":{}}`)
	write(t, root, modlibrary.MetadataFile, raw)
	if err := Validator(expected(archives))(root, meta); err == nil {
		t.Fatal("a configured package was admitted")
	}
}

// The catalogue's contents, an installed copy and a copy in the base folder
// give one identity (§5.7).
func TestInstalledFolderAndCatalogueAgree(t *testing.T) {
	staged, _, archives := fixture(t)
	lib, err := Open(filepath.Join(t.TempDir(), "addons"))
	if err != nil {
		t.Fatal(err)
	}
	want := expected(archives)
	if _, err := lib.InstallDirectory(staged, modlibrary.InstallOptions{Validate: Validator(want)}); err != nil {
		t.Fatal(err)
	}
	addons, skipped, err := Installed(lib.Root)
	if err != nil || len(skipped) != 0 || len(addons) != 1 {
		t.Fatalf("installed = %+v, %+v, %v", addons, skipped, err)
	}
	got := addons[0]
	if got.ID != "bonus" || got.Version != "original" || len(got.Paths) != 2 || Digest(got.Archives) != Digest(want) || Size(got.Archives) != Size(want) {
		t.Fatalf("addon = %+v", got)
	}
	folder := t.TempDir()
	for name, data := range archives {
		write(t, folder, strings.ToUpper(name), data)
	}
	if paths, ok := InFolder(folder, got.Archives); !ok || len(paths) != 2 {
		t.Fatalf("folder copy not recognised: %v", paths)
	}
	write(t, folder, "CORPLAS.UFO", []byte("other"))
	if _, ok := InFolder(folder, got.Archives); ok {
		t.Fatal("a differing folder copy was recognised")
	}
}

// A same-path definition is an override; a same name at another path is a
// conflict that keeps the add-on off (§5.7).
func TestConflictNamesAnotherPathsDefinition(t *testing.T) {
	staged, _, archives := fixture(t)
	addon := Addon{ID: "bonus"}
	for name := range archives {
		addon.Paths = append(addon.Paths, filepath.Join(staged, name))
	}
	names, err := DefinedNames(addon)
	if err != nil || names.Units["armfark"] != "units/armfark.fbi" || names.Features["armfark_dead"] == "" {
		t.Fatalf("names = %+v, %v", names, err)
	}
	others := func(files map[string]string) vfs.FSOps {
		dir := t.TempDir()
		write(t, dir, "totala1.hpi", ufo(t, files))
		fs := vfs.New()
		t.Cleanup(func() { fs.Close() })
		if err := fs.MountGameDirectory(dir); err != nil {
			t.Fatal(err)
		}
		return fs
	}
	if found, err := Conflict(others(map[string]string{"units/armfark.fbi": "[UNITINFO]{UnitName=ARMFARK;}", "units/armpw.fbi": "[UNITINFO]{UnitName=ARMPW;}"}), names); err != nil || found != "" {
		t.Fatalf("same-path override = %q, %v", found, err)
	}
	if found, err := Conflict(others(map[string]string{"units/fark2.fbi": "[UNITINFO]{UnitName=armfark;}"}), names); err != nil || found != "unit armfark" {
		t.Fatalf("unit conflict = %q, %v", found, err)
	}
	if found, err := Conflict(others(map[string]string{"features/corpses/mod.tdf": "[armfark_dead]{}"}), names); err != nil || found != "feature armfark_dead" {
		t.Fatalf("feature conflict = %q, %v", found, err)
	}
}

// A local install has no catalogue contents list: the gate is identity-only
// metadata the package carries and the payload audit, and a second install
// of the same id and version is refused as a manual mod install is (§4.5,
// §5.7).
func TestLocalValidatorAuditsWithoutAContentsList(t *testing.T) {
	staged, meta, _ := fixture(t)
	if err := LocalValidator()(staged, meta); err != nil {
		t.Fatal(err)
	}
	lib, err := Open(filepath.Join(t.TempDir(), "addons"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.InstallDirectory(staged, modlibrary.InstallOptions{Validate: LocalValidator()}); err != nil {
		t.Fatal(err)
	}
	if addons, skipped, err := Installed(lib.Root); err != nil || len(skipped) != 0 || len(addons) != 1 || len(addons[0].Archives) != 2 {
		t.Fatalf("installed = %+v, %+v, %v", addons, skipped, err)
	}
	if _, err := lib.InstallDirectory(staged, modlibrary.InstallOptions{Validate: LocalValidator()}); !errors.Is(err, modlibrary.ErrAlreadyInstalled) {
		t.Fatalf("a second install = %v, want already installed", err)
	}
	for name, change := range map[string]func(t *testing.T, root string){
		"configured metadata": func(t *testing.T, root string) {
			write(t, root, modlibrary.MetadataFile, []byte(`{"schema":1,"id":"bonus","name":"Bonus units","version":"original","config":{}}`))
		},
		"loose unit file": func(t *testing.T, root string) { write(t, root, "armfark.fbi", []byte("x")) },
		"other archive": func(t *testing.T, root string) {
			write(t, root, "Extra.hpi", ufo(t, map[string]string{"units/x.fbi": "x"}))
		},
		"broken archive": func(t *testing.T, root string) { write(t, root, "broken.ufo", []byte("not an archive")) },
		"nested file":    func(t *testing.T, root string) { write(t, root, "units/armfark.fbi", []byte("x")) },
	} {
		t.Run(name, func(t *testing.T) {
			root, meta, _ := fixture(t)
			change(t, root)
			if raw, err := os.ReadFile(filepath.Join(root, modlibrary.MetadataFile)); err == nil {
				_ = json.Unmarshal(raw, &meta)
			}
			if err := LocalValidator()(root, meta); err == nil {
				t.Fatal("admitted")
			}
		})
	}
	// A package without metadata is described as local-<name>@local by the
	// library; an add-on needs its own identity.
	root, _, _ := fixture(t)
	generated := modlibrary.Metadata{Schema: 1, ID: "local-bonus", Name: "bonus", Version: "local"}
	raw, _ := json.Marshal(generated)
	write(t, root, modlibrary.MetadataFile, raw)
	if err := LocalValidator()(root, generated); err == nil {
		t.Fatal("a package without metadata was admitted")
	}
}

// Two add-ons that supply one logical path, or define one unit or feature
// name, share it; the comparison folds case and names the first in order.
func TestSharedPathAndName(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.ufo", ufo(t, map[string]string{"units/AAA.fbi": "[UNITINFO]{UnitName=aaa;}", "Gamedata/Shared.tdf": "x", "anims/zz.gaf": "x"}))
	write(t, dir, "b.ufo", ufo(t, map[string]string{"units/bbb.fbi": "[UNITINFO]{UnitName=AAA;}", "gamedata/shared.TDF": "y", "anims/ZZ.gaf": "y"}))
	write(t, dir, "c.ufo", ufo(t, map[string]string{"units/ccc.fbi": "[UNITINFO]{UnitName=ccc;}", "features/c.tdf": "[ccc_dead]{}"}))
	read := func(name string) (map[string]string, Names) {
		addon := Addon{ID: name, Paths: []string{filepath.Join(dir, name+".ufo")}}
		paths, err := Paths(addon)
		if err != nil {
			t.Fatal(err)
		}
		names, err := DefinedNames(addon)
		if err != nil {
			t.Fatal(err)
		}
		return paths, names
	}
	ap, an := read("a")
	bp, bn := read("b")
	cp, cn := read("c")
	if len(ap) != 3 {
		t.Fatalf("paths = %v", ap)
	}
	if got := SharedPath(ap, bp); got != "anims/zz.gaf" {
		t.Fatalf("shared path = %q", got)
	}
	if got := SharedName(an, bn); got != "unit aaa" {
		t.Fatalf("shared name = %q", got)
	}
	if SharedPath(ap, cp) != "" || SharedName(an, cn) != "" {
		t.Fatal("disjoint add-ons share something")
	}
}
