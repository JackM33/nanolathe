package addonlibrary

// Faction add-ons (docs/DESIGN_MODS_MUTATORS.md §5.8): an ordinary add-on
// whose archives hold a declaration directory, factions/<id>/, naming the
// sides it adds beside ARM and CORE. This reads what a listing shows of one;
// the catalog compiler composes the declaration itself.

import (
	"fmt"
	"path"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Faction is one faction declaration an add-on's archives hold: its
// directory name as the archive spells it, its sides' names in SIDE order,
// and how many of the archives' units name one of those sides as their side.
// A unit pack declares no side; its Units are the archives' units that name
// none of the add-on's sides, which it adds to the sides that exist.
type Faction struct {
	ID    string
	Sides []string
	Units int
}

// UnitPack reports a declaration that adds units to the existing sides
// rather than sides of its own.
func (f Faction) UnitPack() bool { return len(f.Sides) == 0 }

// Factions reads an add-on's faction declarations, in the compiler's
// declaration order (content.DiscoverFactions): each factions/<id>/
// sidedata.tdf's SIDE0..N to the first gap, by its name key, and the count
// of units/*.fbi whose UNITINFO side names one of them. An add-on without a
// declaration has none.
func Factions(addon Addon) ([]Faction, error) {
	fs := vfs.New()
	defer fs.Close()
	for _, full := range addon.Paths {
		if _, err := fs.MountArchive(full, 0); err != nil {
			return nil, err
		}
	}
	declared, err := content.DiscoverFactions(fs)
	if err != nil || len(declared) == 0 {
		return nil, err
	}
	unitSides, err := unitSideKeys(fs)
	if err != nil {
		return nil, err
	}
	out := make([]Faction, 0, len(declared))
	declaredSides := map[string]bool{}
	for _, d := range declared {
		doc, err := readTDF(fs, d.Dir+"/sidedata.tdf")
		if err != nil {
			return nil, err
		}
		f := Faction{ID: d.ID}
		names := map[string]bool{}
		for i := 0; ; i++ {
			section := doc.Root.Section(fmt.Sprintf("SIDE%d", i))
			if section == nil {
				break
			}
			name, _ := section.StringValue("name", "")
			name = strings.TrimSpace(name)
			f.Sides = append(f.Sides, name)
			if key := content.CanonicalKey(name); key != "" {
				names[key], declaredSides[key] = true, true
			}
		}
		for _, side := range unitSides {
			if names[side] {
				f.Units++
			}
		}
		out = append(out, f)
	}
	for i := range out {
		if !out[i].UnitPack() {
			continue
		}
		for _, side := range unitSides {
			if !declaredSides[side] {
				out[i].Units++
			}
		}
	}
	return out, nil
}

// unitSideKeys reads each unit file's UNITINFO side, folded as the compiler
// folds side names.
func unitSideKeys(from vfs.FSOps) ([]string, error) {
	entries, err := from.ReadDir("units")
	if err != nil {
		return nil, nil
	}
	var sides []string
	for _, entry := range entries {
		if entry.IsDir || !strings.EqualFold(path.Ext(entry.Path), ".fbi") {
			continue
		}
		doc, err := readTDF(from, entry.Path)
		if err != nil {
			return nil, err
		}
		if unit := doc.Root.Section("UNITINFO"); unit != nil {
			side, _ := unit.StringValue("side", "")
			sides = append(sides, content.CanonicalKey(side))
		}
	}
	return sides, nil
}
