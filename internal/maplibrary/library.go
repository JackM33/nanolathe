// Package maplibrary installs downloaded map content in a separate library.
// It reuses modlibrary's extraction, receipts and atomic publication, with a
// narrower content policy (docs/DESIGN_MODS_MUTATORS.md §5.6). It has no network.
package maplibrary

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// MaxTNTBytes is the hosted map terrain read budget, a host admission policy
// shared with the desktop mount; it does not change format or gameplay rules.
const MaxTNTBytes int64 = 64 << 20

// DefaultRoot is the maps sibling of the mod library in the XDG data directory.
func DefaultRoot() (string, error) {
	root, err := modlibrary.DefaultRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(root), "maps"), nil
}

// Open prepares the map library. Callers must supply Validator when installing.
func Open(root string) (*modlibrary.Library, error) { return modlibrary.Open(root) }

// Skipped is an installed package that a mount leaves out, and why. Dir is the
// package's <library>/<id>/<version> directory, so a notice can name what to
// delete (docs/DESIGN_CONTENT_VFS.md "Downloaded community maps").
type Skipped struct {
	Dir string
	Err error
}

// Roots returns installed dependency roots first, followed by map roots. Within
// each group the installed library's stable order applies. No network or writes
// are needed, and a missing library is empty. Commands prepend these roots to
// their existing content roots so base and selected-mod content retain precedence.
//
// A package that fails its mount-time audit is returned in skipped rather than
// failing the mount: one damaged package must not stop the game from starting.
// File-manager clutter inside a package is ignored here (modlibrary.OSClutter); install
// validation of a hash-pinned archive stays strict. The error reports only a
// library that cannot be read.
func Roots(root string) (roots []string, skipped []Skipped, err error) {
	dependencies, maps, skipped, err := scanRoots(root)
	return append(dependencies, maps...), skipped, err
}

// DependencyRoots are the installed packages Roots mounts that hold no map:
// the shared feature packages maps depend on, in mount order.
func DependencyRoots(root string) ([]string, error) {
	dependencies, _, _, err := scanRoots(root)
	return dependencies, err
}

func scanRoots(root string) (dependencies, maps []string, skipped []Skipped, err error) {
	if _, err := os.Stat(root); errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil, nil
	} else if err != nil {
		return nil, nil, nil, err
	}
	installed, err := (&modlibrary.Library{Root: root}).Installed()
	if err != nil {
		return nil, nil, nil, err
	}
	for _, item := range installed {
		if err := identityOnly(item.Dir, item.Metadata); err != nil {
			skipped = append(skipped, Skipped{Dir: item.Dir, Err: err})
			continue
		}
		mounted, paths, err := inspect(item.Dir, true)
		if err != nil {
			skipped = append(skipped, Skipped{Dir: item.Dir, Err: err})
			continue
		}
		mounted.Close()
		if len(paths) == 0 {
			dependencies = append(dependencies, item.Dir)
		} else {
			maps = append(maps, item.Dir)
		}
	}
	return dependencies, maps, skipped, nil
}

// SelectRoots keeps the map roots that validate against the current base and
// mod stack (ValidateRoots), in their given order, and reports the rest. The
// usual case costs one validation. When the whole set fails, each root is
// retried in order on top of the roots already kept, so a package that
// collides with a later mod or base feature is left out alone instead of
// unmounting every downloaded map or stopping the start.
func SelectRoots(mapRoots, baseAndModRoots []string) (kept []string, skipped []Skipped) {
	if len(mapRoots) == 0 {
		return nil, nil
	}
	if ValidateRoots(mapRoots, baseAndModRoots) == nil {
		return append([]string(nil), mapRoots...), nil
	}
	for _, root := range mapRoots {
		candidate := append(append([]string(nil), kept...), root)
		if err := ValidateRoots(candidate, baseAndModRoots); err != nil {
			skipped = append(skipped, Skipped{Dir: root, Err: err})
			continue
		}
		kept = candidate
	}
	return kept, skipped
}

// ValidateRoots checks installed support against the current base/mod stack at
// mount time. A later mod switch must not let a differently named downloaded
// feature file take ownership of an existing feature. It does not decode TNTs.
func ValidateRoots(mapRoots, baseAndModRoots []string) error {
	if len(mapRoots) == 0 {
		return nil
	}
	combined := vfs.New()
	defer combined.Close()
	roots := append(append([]string(nil), mapRoots...), baseAndModRoots...)
	if err := combined.MountGameDirectories(roots); err != nil {
		return diagnostic("mounting installed map support failed", "<map roots>", roots, err.Error())
	}
	var features map[string]*content.FeatureDef
	if _, err := combined.Stat("features"); err == nil {
		var err error
		features, err = content.CompileFeatures(combined)
		if err != nil {
			return err
		}
	}
	return preserveBaseFeatures(baseAndModRoots, features)
}

func diagnostic(what, logical string, providers []string, expected string) error {
	return fmt.Errorf("nanolathe: %s: logical path %s, providers searched [%s], expected %s", what, logical, strings.Join(providers, ", "), expected)
}

// ErrMapInInstall refuses a package whose map the base install or selected mod
// already supplies (docs/DESIGN_MODS_MUTATORS.md §5.6).
var ErrMapInInstall = errors.New("map is already in your install")

// within reports whether name lies inside root, following symbolic links.
func within(root, name string) bool {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	name, err = filepath.EvalSymlinks(name)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, name)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// InstallValidator is the catalogue install gate. Features resolve only from
// requiredRoots: the base install, the selected mod and the package's own
// declared dependencies. Other installed map packages never complete a
// package, which would lose those features when that package is removed; they
// are installedRoots, whose existing feature definitions the package must not
// change (docs/DESIGN_MODS_MUTATORS.md §5.6).
func InstallValidator(requiredRoots, installedRoots []string, mapPath string) func(string, modlibrary.Metadata) error {
	closure := Validator(requiredRoots, mapPath)
	others := append(append([]string(nil), installedRoots...), requiredRoots...)
	return func(root string, meta modlibrary.Metadata) error {
		if err := closure(root, meta); err != nil {
			return err
		}
		if len(installedRoots) == 0 {
			return nil
		}
		return ValidateRoots([]string{root}, others)
	}
}

// Validator admits identity-only packages containing maps and their support
// assets. mapPath names the catalogue's OTA, or is empty for a dependency.
// Every terrain pair is parsed from staging, before base roots are mounted, so
// a good existing map cannot conceal a broken downloaded replacement. A map
// path that baseRoots already supply is refused with ErrMapInInstall.
func Validator(baseRoots []string, mapPath string) func(string, modlibrary.Metadata) error {
	base := append([]string(nil), baseRoots...)
	return func(root string, meta modlibrary.Metadata) error {
		if err := identityOnly(root, meta); err != nil {
			return err
		}
		staged, paths, err := inspect(root, false)
		if err != nil {
			return err
		}
		defer staged.Close()
		if mapPath == "" {
			if len(paths) != 0 {
				return diagnostic("map dependency contains maps", paths[0], staged.ProviderIDs(), "support assets only")
			}
		} else {
			found := false
			for _, name := range paths {
				if strings.EqualFold(name, mapPath) {
					found = true
				}
			}
			if !found {
				return diagnostic("catalogue map is absent", mapPath, staged.ProviderIDs(), "the named OTA/TNT pair in this package")
			}
		}
		// These are ordinary roots, with the existing installation winning any
		// same-path support resource. The compiled feature loader owns successor
		// linking and feature-name interpretation; no map-specific rules are added.
		combined := vfs.New()
		defer combined.Close()
		if err := combined.MountGameDirectories(append([]string{root}, base...)); err != nil {
			return diagnostic("mounting map support failed", root, base, err.Error())
		}
		var features map[string]*content.FeatureDef
		if _, err := combined.Stat("features"); err == nil {
			features, err = content.CompileFeatures(combined)
			if err != nil {
				return err
			}
		}
		if err := preserveBaseFeatures(base, features); err != nil {
			return err
		}
		// The base stack wins a shared logical path, so a map it already
		// supplies would be listed and played as the base edition while the
		// catalogue called the download installed.
		for _, name := range paths {
			if info, err := combined.Stat(name); err == nil && !within(root, info.Source.SourcePath) {
				return fmt.Errorf("nanolathe: %w: logical path %s, providers searched [%s], expected a map path the installed game and selected mod do not supply",
					ErrMapInInstall, name, strings.Join(combined.ProviderIDs(), ", "))
			}
		}

		for _, name := range paths {
			ota, err := formats.LoadOTAFile(staged, name)
			if err != nil {
				return diagnostic("map metadata is unreadable", name, staged.ProviderIDs(), err.Error())
			}
			if !ota.HasNetworkSchema() {
				return diagnostic("map has no skirmish schema", name, staged.ProviderIDs(), "an OTA network schema")
			}
			terrainName := strings.TrimSuffix(name, path.Ext(name)) + ".tnt"
			raw, err := staged.ReadFileLimit(terrainName, MaxTNTBytes)
			if err != nil {
				return diagnostic("map terrain is unreadable", terrainName, staged.ProviderIDs(), err.Error())
			}
			terrain, err := formats.LoadTNT(raw)
			if err != nil {
				return diagnostic("map terrain is invalid", terrainName, staged.ProviderIDs(), err.Error())
			}
			// Completeness is a hosted-package admission policy (§5.6), not a
			// change to the engine's missing-name skip behavior. Unreferenced
			// terrain records and unplaced OTA entries need no definition.
			requireFeature := func(name string) error {
				if name != "" && features[content.CanonicalKey(name)] == nil {
					return diagnostic("map feature is missing", name, combined.ProviderIDs(), "a feature definition supplied by the map, its dependencies or the base installation")
				}
				return nil
			}
			used := make([]bool, len(terrain.FeatureTable))
			for _, cell := range terrain.Attributes {
				// LoadTNT validates indices; the sentinel band is never a
				// feature index even in a large feature table [fmt tnt].
				if cell.Feature < 0xfffb && int(cell.Feature) < len(used) {
					used[cell.Feature] = true
				}
			}
			for i, feature := range terrain.FeatureTable {
				if used[i] {
					if err := requireFeature(feature.Name); err != nil {
						return err
					}
				}
			}
			for _, schema := range ota.Schemas {
				if formats.NetworkSchemaRank(schema.Type) == 0 {
					continue
				}
				for _, placement := range mission.DecodeFeaturePlacements(schema.Section) {
					if placement.IsPlaced() {
						if err := requireFeature(placement.Name); err != nil {
							return err
						}
					}
				}
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
		return diagnostic("map package contains configuration", modlibrary.MetadataFile, []string{root}, "identity-only schema 1 metadata")
	}
	return nil
}

func archive(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".hpi", ".ufo", ".ccx", ".gp3", ".gp4", ".gpf", ".swx":
		return true
	}
	return false
}

// allowedContent is a host installation policy, not a retail file-discovery
// rule. A filename's directory and extension must both fit map support.
func allowedContent(name string) bool {
	name = strings.ToLower(name)
	if strings.ContainsAny(name, "\\:\x00\r\n") || path.Clean(name) != name || strings.HasPrefix(name, "/") {
		return false
	}
	dir, _, nested := strings.Cut(name, "/")
	if !nested {
		return false
	}
	ext := path.Ext(name)
	switch dir {
	case "maps":
		return path.Dir(name) == "maps" && (ext == ".ota" || ext == ".tnt")
	case "features":
		return ext == ".tdf"
	case "anims":
		return ext == ".gaf" && name != "anims/vismasks.gaf"
	case "objects3d":
		return ext == ".3do"
	case "textures":
		return ext == ".gaf" || ext == ".bmp" || ext == ".pcx"
	}
	return false
}

// inspect audits every archive entry, including shadowed entries, as well as
// loose files. Each container is opened explicitly: ordinary VFS discovery may
// skip a rejected archive, which is unsuitable for accepting a hosted package.
// tolerateClutter is the mount-time audit of an installed package: modlibrary.OSClutter
// files are neither refused nor treated as maps. Installation passes false.
func inspect(root string, tolerateClutter bool) (*vfs.FS, []string, error) {
	mounted := vfs.New()
	fail := func(err error) (*vfs.FS, []string, error) { mounted.Close(); return nil, nil, err }
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
		if tolerateClutter && entry.IsDir() && strings.EqualFold(entry.Name(), "__MACOSX") {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		if tolerateClutter && modlibrary.OSClutter(rel) {
			return nil
		}
		if !entry.Type().IsRegular() {
			return diagnostic("map package has a non-regular file", rel, []string{root}, "regular content files")
		}
		if !strings.Contains(rel, "/") && archive(rel) {
			single := vfs.New()
			defer single.Close()
			if _, err := single.MountArchive(name, 0); err != nil {
				return diagnostic("map archive is invalid", rel, []string{root}, err.Error())
			}
			for _, asset := range single.Entries() {
				if !asset.IsDir && !allowedContent(asset.Path) {
					return diagnostic("map archive contains non-map content", asset.Path, []string{rel}, "only map and support assets")
				}
			}
			return nil
		}
		if rel == modlibrary.MetadataFile || rel == modlibrary.ReceiptFile || modlibrary.RootDocument(rel) || allowedContent(rel) {
			return nil
		}
		return diagnostic("map package contains non-map content", rel, []string{root}, "only map assets, support assets and root documentation")
	})
	if err != nil {
		return fail(err)
	}
	if err := mounted.MountGameDirectory(root); err != nil {
		return fail(err)
	}
	var paths []string
	seen := make(map[string]bool)
	for _, entry := range mounted.Entries() {
		if entry.IsDir || !strings.HasPrefix(entry.Path, "maps/") || seen[entry.Path] || (tolerateClutter && modlibrary.OSClutter(entry.Path)) {
			continue
		}
		seen[entry.Path] = true
		other := strings.TrimSuffix(entry.Path, path.Ext(entry.Path)) + ".ota"
		if path.Ext(entry.Path) == ".ota" {
			paths = append(paths, entry.Path)
			other = strings.TrimSuffix(entry.Path, ".ota") + ".tnt"
		}
		if _, err := mounted.Stat(other); err != nil {
			return fail(diagnostic("map is missing its paired file", other, mounted.ProviderIDs(), "an OTA and TNT with the same basename"))
		}
	}
	return mounted, paths, nil
}

// Feature identity is independent of its source filename. Compare the compiled
// base records too: precedence of two files cannot protect a feature redefined
// in a differently named, earlier-sorting file (§5.6).
func preserveBaseFeatures(roots []string, combined map[string]*content.FeatureDef) error {
	base := vfs.New()
	defer base.Close()
	if err := base.MountGameDirectories(roots); err != nil {
		return err
	}
	if _, err := base.Stat("features"); err != nil {
		return nil
	}
	features, err := content.CompileFeatures(base)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(features))
	for name := range features {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		existing, got := features[name], combined[name]
		if got == nil || !reflect.DeepEqual(featureValue(existing), featureValue(got)) {
			return diagnostic("map support changes an existing feature", name, base.ProviderIDs(), "base feature definitions unchanged")
		}
	}
	return nil
}

func featureValue(def *content.FeatureDef) content.FeatureDef {
	value := *def
	value.DefinitionHeader = content.DefinitionHeader{}
	value.FeatureDeadDef, value.FeatureReclamateDef, value.FeatureBurntDef = nil, nil, nil
	return value
}
