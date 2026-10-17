// Package addonlibrary installs Nanolathe-hosted official add-ons: original
// Cavedog archives that mount with the base game at its *.UFO tier, apart
// from the mod slot (docs/DESIGN_MODS_MUTATORS.md §5.7). It reuses
// modlibrary's extraction, receipts and atomic publication, with a payload of
// archives only. It has no network.
package addonlibrary

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// MaxArchives bounds the archives one add-on may hold.
const MaxArchives = 32

// DefaultRoot is the addons sibling of the mod library in the XDG data
// directory.
func DefaultRoot() (string, error) {
	root, err := modlibrary.DefaultRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(root), "addons"), nil
}

// Open prepares the add-on library. Callers must supply Validator when
// installing.
func Open(root string) (*modlibrary.Library, error) { return modlibrary.Open(root) }

// Archive is one archive an add-on holds: its file name, byte size and
// SHA-256.
type Archive struct {
	Name   string
	Size   int64
	SHA256 [32]byte
}

// Addon is one add-on version and the files that supply its archives, in the
// order of Archives: the library's copies, or byte-identical copies already
// in the base install's folder.
type Addon struct {
	ID, Name, Version string
	Archives          []Archive
	Paths             []string
	// Dir is the library directory, "" for copies in the base folder.
	Dir string
}

// SortArchives orders archives by file name without regard to case, the
// order an add-on's identity is computed in.
func SortArchives(archives []Archive) {
	sort.Slice(archives, func(i, j int) bool {
		return strings.ToLower(archives[i].Name) < strings.ToLower(archives[j].Name)
	})
}

// Digest is an add-on's content identity: the SHA-256 of its archives' folded
// names, sizes and digests in SortArchives order. The catalogue's contents
// list gives it before a download, and an install or a folder copy gives it
// from the files, so all three agree (§5.7).
func Digest(archives []Archive) [32]byte {
	sorted := append([]Archive(nil), archives...)
	SortArchives(sorted)
	h := sha256.New()
	io.WriteString(h, "nanolathe/addon-contents/1\x00")
	var word [8]byte
	for _, a := range sorted {
		binary.LittleEndian.PutUint64(word[:], uint64(len(a.Name)))
		h.Write(word[:])
		io.WriteString(h, strings.ToLower(a.Name))
		binary.LittleEndian.PutUint64(word[:], uint64(a.Size))
		h.Write(word[:])
		h.Write(a.SHA256[:])
	}
	var out [32]byte
	h.Sum(out[:0])
	return out
}

// Size is the total byte size of an add-on's archives.
func Size(archives []Archive) uint64 {
	var total uint64
	for _, a := range archives {
		total += uint64(a.Size)
	}
	return total
}

// Skipped is an installed add-on that a listing leaves out, and why.
type Skipped struct {
	Dir string
	Err error
}

// Installed lists the library's add-ons with their archives hashed, in the
// library's stable order. A package that fails its audit is returned in
// skipped rather than failing the listing; a missing library is empty.
func Installed(root string) (addons []Addon, skipped []Skipped, err error) {
	if _, err := os.Stat(root); errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	} else if err != nil {
		return nil, nil, err
	}
	installed, err := (&modlibrary.Library{Root: root}).Installed()
	if err != nil {
		return nil, nil, err
	}
	for _, item := range installed {
		addon, err := read(item)
		if err != nil {
			skipped = append(skipped, Skipped{Dir: item.Dir, Err: err})
			continue
		}
		addons = append(addons, addon)
	}
	return addons, skipped, nil
}

func read(item modlibrary.Mod) (Addon, error) {
	if err := identityOnly(item.Dir, item.Metadata); err != nil {
		return Addon{}, err
	}
	names, err := payload(item.Dir, true)
	if err != nil {
		return Addon{}, err
	}
	addon := Addon{ID: item.ID, Name: item.Name, Version: item.Version, Dir: item.Dir}
	for _, name := range names {
		full := filepath.Join(item.Dir, name)
		archive, err := hashArchive(full, name)
		if err != nil {
			return Addon{}, err
		}
		addon.Archives = append(addon.Archives, archive)
		addon.Paths = append(addon.Paths, full)
	}
	return addon, nil
}

func hashArchive(full, name string) (Archive, error) {
	f, err := os.Open(full)
	if err != nil {
		return Archive{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return Archive{}, err
	}
	a := Archive{Name: name, Size: n}
	h.Sum(a.SHA256[:0])
	return a, nil
}

// InFolder returns the files in dir that hold byte-identical copies of every
// archive, in the order of archives, matching file names without regard to
// case. An add-on whose archives all sit in the base install's folder is
// already in the install (§5.7).
func InFolder(dir string, archives []Archive) ([]string, bool) {
	if len(archives) == 0 {
		return nil, false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, false
	}
	byName := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.Type().IsRegular() {
			byName[strings.ToLower(entry.Name())] = entry.Name()
		}
	}
	paths := make([]string, len(archives))
	for i, want := range archives {
		name, ok := byName[strings.ToLower(want.Name)]
		if !ok {
			return nil, false
		}
		full := filepath.Join(dir, name)
		if info, err := os.Stat(full); err != nil || info.Size() != want.Size {
			return nil, false
		}
		got, err := hashArchive(full, name)
		if err != nil || got.SHA256 != want.SHA256 {
			return nil, false
		}
		paths[i] = full
	}
	return paths, true
}

// Validator is the catalogue install gate: identity-only metadata, and a
// payload of exactly the expected archives at the package root, each
// passing the container gate, beside root documentation (§5.7).
func Validator(expect []Archive) func(string, modlibrary.Metadata) error {
	want := append([]Archive(nil), expect...)
	SortArchives(want)
	return func(root string, meta modlibrary.Metadata) error {
		if err := identityOnly(root, meta); err != nil {
			return err
		}
		names, err := payload(root, false)
		if err != nil {
			return err
		}
		got := make([]Archive, 0, len(names))
		for _, name := range names {
			archive, err := hashArchive(filepath.Join(root, name), name)
			if err != nil {
				return err
			}
			got = append(got, archive)
		}
		SortArchives(got)
		if len(got) != len(want) {
			return diagnostic("add-on holds other archives", root, names, fmt.Sprintf("the %d archives the catalogue lists", len(want)))
		}
		for i := range got {
			if !strings.EqualFold(got[i].Name, want[i].Name) || got[i].Size != want[i].Size || got[i].SHA256 != want[i].SHA256 {
				return diagnostic("add-on archive differs from the catalogue", got[i].Name, []string{root}, "the size and SHA-256 the catalogue lists")
			}
		}
		return nil
	}
}

func identityOnly(root string, meta modlibrary.Metadata) error {
	ok, err := modlibrary.IdentityOnly(root, meta)
	if err != nil {
		return err
	}
	if !ok {
		return diagnostic("add-on package contains configuration", modlibrary.MetadataFile, []string{root}, "identity-only schema 1 metadata")
	}
	return nil
}

// payload audits a package and returns its archives' file names, sorted:
// *.UFO archives at the root that pass the container gate, beside metadata,
// receipt and root documentation; anything else refuses. tolerateClutter is
// the audit of an installed package, which ignores file-manager litter.
func payload(root string, tolerateClutter bool) ([]string, error) {
	var names []string
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == root {
			return nil
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if tolerateClutter && strings.EqualFold(entry.Name(), "__MACOSX") {
				return filepath.SkipDir
			}
			return diagnostic("add-on package has a directory", rel, []string{root}, "archives and documentation at the package root")
		}
		if tolerateClutter && modlibrary.OSClutter(rel) {
			return nil
		}
		if !entry.Type().IsRegular() {
			return diagnostic("add-on package has a non-regular file", rel, []string{root}, "regular files")
		}
		if rel == modlibrary.MetadataFile || rel == modlibrary.ReceiptFile || modlibrary.RootDocument(rel) {
			return nil
		}
		if !ArchiveName(rel) {
			return diagnostic("add-on package contains other content", rel, []string{root}, "*.UFO archives and root documentation")
		}
		single := vfs.New()
		defer single.Close()
		if _, err := single.MountArchive(name, 0); err != nil {
			return diagnostic("add-on archive is invalid", rel, []string{root}, err.Error())
		}
		names = append(names, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(names) == 0 || len(names) > MaxArchives {
		return nil, diagnostic("add-on package has no archives or too many", root, names, fmt.Sprintf("1 to %d *.UFO archives", MaxArchives))
	}
	folded := make(map[string]bool, len(names))
	for _, name := range names {
		if folded[strings.ToLower(name)] {
			return nil, diagnostic("add-on package repeats an archive name", name, []string{root}, "archive names unique without regard to case")
		}
		folded[strings.ToLower(name)] = true
	}
	sort.Strings(names)
	return names, nil
}

// ArchiveName reports whether name is a file name an add-on's archive may
// have: flat, at most 255 bytes, and a *.UFO, so that it mounts at that tier
// (§5.7).
func ArchiveName(name string) bool {
	return name != "" && len(name) <= 255 && path.Base(name) == name && !strings.ContainsAny(name, "/\\:\x00\r\n") &&
		strings.EqualFold(path.Ext(name), ".ufo") && len(name) > len(".ufo")
}

func diagnostic(what, logical string, providers []string, expected string) error {
	return fmt.Errorf("nanolathe: %s: logical path %s, providers searched [%s], expected %s", what, logical, strings.Join(providers, ", "), expected)
}

// Names are the unit and feature names an add-on's archives define, folded,
// each with the logical path that defines it.
type Names struct {
	Units, Features map[string]string
}

// DefinedNames reads the unit and feature names an add-on's archives define:
// each unit file's UNITINFO name and each feature file's sections.
func DefinedNames(addon Addon) (Names, error) {
	names := Names{Units: map[string]string{}, Features: map[string]string{}}
	for _, full := range addon.Paths {
		single := vfs.New()
		if _, err := single.MountArchive(full, 0); err != nil {
			single.Close()
			return Names{}, err
		}
		err := collectNames(single, names, nil)
		single.Close()
		if err != nil {
			return Names{}, err
		}
	}
	return names, nil
}

// Conflict reports the first unit or feature name the add-on defines that
// others defines at another logical path. others is the mounted content
// without the add-on: the base install and the selected mod. A same-path
// definition is an ordinary override and no conflict; a same name at another
// path would leave two definitions, so the add-on stays unmounted (§5.7).
func Conflict(others vfs.FSOps, names Names) (string, error) {
	var found string
	stop := errors.New("conflict")
	err := collectNames(others, Names{Units: map[string]string{}, Features: map[string]string{}}, func(kind, name, logical string) error {
		own := names.Units
		if kind == "feature" {
			own = names.Features
		}
		if at, ok := own[name]; ok && !strings.EqualFold(at, logical) {
			found = kind + " " + name
			return stop
		}
		return nil
	})
	if errors.Is(err, stop) {
		return found, nil
	}
	return "", err
}

// collectNames reads unit names from units/*.fbi and feature names from
// features/**/*.tdf, recording the first path that defines each in into and
// calling visit for every definition when visit is set.
func collectNames(from vfs.FSOps, into Names, visit func(kind, name, logical string) error) error {
	record := func(kind string, table map[string]string, name, logical string) error {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			return nil
		}
		if _, ok := table[name]; !ok {
			table[name] = logical
		}
		if visit != nil {
			return visit(kind, name, logical)
		}
		return nil
	}
	if entries, err := from.ReadDir("units"); err == nil {
		for _, entry := range entries {
			if entry.IsDir || !strings.EqualFold(path.Ext(entry.Path), ".fbi") {
				continue
			}
			doc, err := readTDF(from, entry.Path)
			if err != nil {
				return err
			}
			if unit := doc.Root.Section("UNITINFO"); unit != nil {
				name, _ := unit.StringValue("UnitName", "")
				if err := record("unit", into.Units, name, entry.Path); err != nil {
					return err
				}
			}
		}
	}
	var walk func(dir string) error
	walk = func(dir string) error {
		entries, err := from.ReadDir(dir)
		if err != nil {
			return nil
		}
		for _, entry := range entries {
			if entry.IsDir {
				if err := walk(entry.Path); err != nil {
					return err
				}
				continue
			}
			if !strings.EqualFold(path.Ext(entry.Path), ".tdf") {
				continue
			}
			doc, err := readTDF(from, entry.Path)
			if err != nil {
				return err
			}
			for _, section := range doc.Root.Sections() {
				if err := record("feature", into.Features, section.Name, entry.Path); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk("features")
}

func readTDF(from vfs.FSOps, name string) (*formats.Document, error) {
	data, err := from.ReadFileLimit(name, 1<<20)
	if err != nil {
		return nil, err
	}
	return formats.ParseTDF(data)
}
