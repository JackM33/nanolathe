// Faction declarations: the gamedata a faction add-on contributes beside the
// base game's own tables instead of replacing them
// (docs/DESIGN_MODS_MUTATORS.md §5.8).

package content

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// FactionDir is the logical directory a faction add-on declares itself in:
// factions/<id>/sidedata.tdf names its sides, and the optional
// moveinfo.tdf, sound.tdf and weapons.tdf beside it carry the movement
// classes, sound categories and weapons its units use (§5.8). The optional
// damage.tdf adds base weapons' damage against its own units, and
// download.tdf puts its own units on other builders' menus. A unit pack is a
// declaration with no SIDE records, adding units to the sides that exist.
// Retail reads no such directory, so content without it compiles exactly as
// before.
const FactionDir = "factions"

// Faction is one declaration found under FactionDir.
type Faction struct {
	// ID is the directory name as the archive spells it.
	ID string
	// Dir is the logical directory, factions/<id>.
	Dir string
}

// The fragment files a declaration may hold. sidedata.tdf is what makes a
// directory a declaration.
const (
	factionSides     = "sidedata.tdf"
	factionMovement  = "moveinfo.tdf"
	factionSounds    = "sound.tdf"
	factionWeapons   = "weapons.tdf"
	factionDamage    = "damage.tdf"
	factionDownloads = "download.tdf"
)

// DiscoverFactions lists the faction declarations in fs: every factions/<id>
// directory holding a sidedata.tdf, ordered by folded id so the composition
// order never depends on mount order (I1). Content with no factions
// directory has none.
func DiscoverFactions(fs vfs.FSOps) ([]Faction, error) {
	if fs == nil {
		return nil, nil
	}
	if info, err := fs.Stat(FactionDir); err != nil || !info.IsDir {
		return nil, nil
	}
	entries, err := fs.ReadDir(FactionDir)
	if err != nil {
		return nil, fmt.Errorf("content: %s: %w", FactionDir, err)
	}
	var out []Faction
	for _, e := range entries {
		if !e.IsDir {
			continue
		}
		name := e.OriginalPath
		if name == "" {
			name = e.Path
		}
		if i := strings.LastIndexAny(name, `/\`); i >= 0 {
			name = name[i+1:]
		}
		dir := FactionDir + "/" + strings.ToLower(name)
		if info, err := fs.Stat(dir + "/" + factionSides); err != nil || info.IsDir {
			continue
		}
		out = append(out, Faction{ID: name, Dir: dir})
	}
	// ReadDir sorts by folded logical path already; keep that order.
	return out, nil
}

// factionFragment parses one fragment of a declaration. ok is false when the
// declaration does not hold that file.
func factionFragment(fs vfs.FSOps, f Faction, file string, limit int64) (doc *formats.Document, prov Provenance, ok bool, err error) {
	logical := f.Dir + "/" + file
	info, statErr := fs.Stat(logical)
	if statErr != nil || info.IsDir {
		return nil, Provenance{}, false, nil
	}
	data, err := fs.ReadFileLimit(logical, limit)
	if err != nil {
		return nil, Provenance{}, false, fmt.Errorf("content: %s: %w", logical, err)
	}
	doc, err = formats.ParseTDF(data)
	if err != nil {
		return nil, Provenance{}, false, formats.WithTDFContext(fs, err, logical)
	}
	return doc, ProvenanceFrom(info), true, nil
}

// factionDuplicate is the diagnostic when a declaration defines a name the
// base tables or an earlier declaration already define. The mount refuses
// such an add-on first (§5.8); this is the compile's own guarantee that a
// faction never replaces a definition.
func factionDuplicate(kind, name, logical string, prior Provenance) error {
	return fmt.Errorf("nanolathe: faction defines a %s that is already defined: logical path %s, providers searched [%s], expected a %s name unique to the faction: %s", kind, logical, prior.LogicalPath, kind, name)
}

// factionWeaponBase is the first record slot faction weapons are given: past
// the retail table and past every slot the base content authored, so a
// faction weapon never shares a slot with one, and the result depends only on
// the content, never on the limits a caller passes (§5.8).
func factionWeaponBase(slots map[int32]*WeaponDef) int32 {
	next := int32(RetailWeaponSlots)
	for id := range slots {
		if id >= next {
			next = id + 1
		}
	}
	return next
}

// nextFactionWeaponSlot returns the first slot at or after id whose low byte
// is not zero: the low byte is the definition's active gate and the byte
// saves copy (WeaponDef.ActiveByte), so a zero there would leave the weapon
// inactive.
func nextFactionWeaponSlot(id int32) int32 {
	for uint8(id) == 0 {
		id++
	}
	return id
}

// factionUnitDomain raises a unit-definition domain to fit count records
// when factions are present, rounded up to whole 32-bit category words, as
// the domain is a word count of membership bits [R-P0-03].
func factionUnitDomain(domain, count int) int {
	need := count + 1
	if need <= domain {
		return domain
	}
	return (need + 31) / 32 * 32
}

// factionLimits widens the compile's tables to hold what faction declarations
// add: the weapon table to its highest assigned slot and the unit domain to
// the composed record count. Content with no declarations keeps limits
// exactly, so the retail baseline and every mod compile as before. The result
// depends only on limits and the content, and widening is idempotent, so a
// recompile under the widened limits produces the same catalog (§5.8).
func factionLimits(fs vfs.FSOps, limits Limits, weapons map[string]*WeaponDef, units int) (Limits, error) {
	factions, err := DiscoverFactions(fs)
	if err != nil || len(factions) == 0 {
		return limits, err
	}
	for _, wd := range weapons {
		if wd != nil && int64(wd.ID) >= int64(limits.Weapons) {
			limits.Weapons = int(wd.ID) + 1
		}
	}
	limits.Units = factionUnitDomain(limits.Units, units)
	return limits.normalize()
}

// inFactionDeclaration reports a definition read from a declaration's own
// fragment files rather than from the families retail reads.
func inFactionDeclaration(prov Provenance) bool {
	return strings.HasPrefix(CanonicalKey(prov.LogicalPath), FactionDir+"/")
}

// ownUnit resolves name to a unit the declaration's own archive defines: the
// only units a declaration's damage entries and menu entries may name, so
// that what one add-on composes never touches the base game's units or
// another add-on's.
func ownUnit(units map[string]*UnitDef, name string, decl Provenance) (*UnitDef, error) {
	u := units[CanonicalKey(trimTDFSemantic(name))]
	if u == nil || u.Provenance.ProviderID != decl.ProviderID {
		return nil, fmt.Errorf("nanolathe: faction names a unit it does not define: logical path %s, providers searched [%s], expected a unit of the same archive: %s", decl.LogicalPath, decl.ProviderID, name)
	}
	return u, nil
}

// FactionDamage is one entry a declaration's damage.tdf added to a base
// weapon's damage table (§5.8).
type FactionDamage struct {
	// Weapon is the base weapon's catalog name; Unit the unit name as the
	// declaration authors it, which is the key the table holds.
	Weapon, Unit string
	Damage       int32
	Provenance   Provenance
}

// composeFactionDamage adds each declaration's damage.tdf to the base
// weapons' damage tables. A section names a base weapon as the weapon
// catalog does, and each of its assignments is one unit=damage entry, read
// with the integer accessor as a DAMAGE entry is [02 "Weapon record"]. Every
// unit must be one the declaration's archive defines, the weapon must be the
// base's, and the table must not already name the unit: an entry changes
// only how a base weapon hits that add-on's units, so entries from different
// add-ons never meet. The weapon's Hash stays its authored record's, as a
// mutator's damage rewrite leaves it; the entries are returned for the
// catalog hash, and the semantic digest reads the table itself.
func composeFactionDamage(fs vfs.FSOps, weapons map[string]*WeaponDef, units map[string]*UnitDef) ([]FactionDamage, error) {
	factions, err := DiscoverFactions(fs)
	if err != nil || len(factions) == 0 {
		return nil, err
	}
	var out []FactionDamage
	for _, f := range factions {
		doc, prov, ok, err := factionFragment(fs, f, factionDamage, 1<<20)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		for _, section := range doc.Root.Sections() {
			name := trimTDFSemantic(section.OriginalName)
			if name == "" {
				continue
			}
			wd := weapons[CanonicalKey(name)]
			if wd == nil || inFactionDeclaration(wd.Provenance) {
				return nil, fmt.Errorf("nanolathe: faction damage names no base weapon: logical path %s, providers searched [%s], expected a base weapon name: %s", prov.LogicalPath, prov.ProviderID, name)
			}
			for _, item := range section.ResolvedAssignments() {
				if _, err := ownUnit(units, item.Key, prov); err != nil {
					return nil, err
				}
				if _, exists := wd.DamageOverride(item.Key); exists {
					return nil, fmt.Errorf("nanolathe: faction damage replaces an entry: logical path %s, providers searched [%s, %s], expected a unit %s's table does not name: %s", prov.LogicalPath, prov.ProviderID, wd.Provenance.LogicalPath, wd.Name, item.Key)
				}
				damage := section.IntValue(item.Key, 0)
				wd.addDamage(item.OriginalKey, damage)
				out = append(out, FactionDamage{Weapon: wd.CanonicalKey, Unit: item.OriginalKey, Damage: damage, Provenance: prov})
			}
		}
	}
	return out, nil
}

// addDamage inserts one entry at its case-insensitive lower bound in the
// retained lookup order, as the record compiler inserts an authored DAMAGE
// key [06 R-DMG-01 §1], and refreshes the folded projection.
func (w *WeaponDef) addDamage(key string, damage int32) {
	order := w.DamageKeysSorted()
	lo := sort.Search(len(order), func(i int) bool { return CanonicalKey(order[i]) >= CanonicalKey(key) })
	order = append(order, "")
	copy(order[lo+1:], order[lo:])
	order[lo] = key
	if w.Damage == nil {
		w.Damage = make(map[string]int32)
	}
	w.Damage[key] = damage
	w.damageOrder = order
	w.damageFold = make([]string, len(order))
	for i, k := range order {
		w.damageFold[i] = strings.ToLower(k)
	}
}

// factionPageButtons is the stock builder page's product count, the page a
// declaration's menu entries fill [07 R-HUD-03 §6].
const factionPageButtons = 6

// composeFactionPlacements places each declaration's download.tdf entries
// on builders' menus and returns placements with them appended. An entry is
// a section holding UNITMENU, the builder, and UNITNAME, a unit the
// declaration's archive defines; it authors no MENU or BUTTON. Each
// declaration's products for one builder fill new pages after every page
// that builder already has, its authored page windows and every placement
// before them, six buttons to a page in entry order, and the
// next declaration starts on the page after. Add-ons therefore never claim
// one button, whichever are enabled. An entry whose builder this content
// does not define, or that is no builder, places nothing.
func composeFactionPlacements(fs vfs.FSOps, units map[string]*UnitDef, placements []DownloadMenuPlacement) ([]DownloadMenuPlacement, error) {
	factions, err := DiscoverFactions(fs)
	if err != nil || len(factions) == 0 {
		return placements, err
	}
	used := map[string]int{} // builder key -> highest MENU byte in use
	for _, u := range units {
		if u != nil && int(u.BuildPageCount) > used[u.CanonicalKey] {
			used[u.CanonicalKey] = int(u.BuildPageCount)
		}
	}
	files := 0
	for _, p := range placements {
		if key := CanonicalKey(p.Builder); p.BuilderResolved && int(p.Menu) > used[key] {
			used[key] = int(p.Menu)
		}
		if p.FileOrder >= files {
			files = p.FileOrder + 1
		}
	}
	for _, f := range factions {
		doc, prov, ok, err := factionFragment(fs, f, factionDownloads, 1<<20)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		first := map[string]int{} // builder key -> first MENU byte of this declaration
		count := map[string]int{} // builder key -> products placed
		seen := map[string]bool{}
		for item, section := range doc.Root.Sections() {
			builderName, _ := section.StringValue("UNITMENU", "")
			productName, _ := section.StringValue("UNITNAME", "")
			product, err := ownUnit(units, productName, prov)
			if err != nil {
				return nil, err
			}
			builder := units[CanonicalKey(trimTDFSemantic(builderName))]
			if builder == nil || !builder.Builder || seen[builder.CanonicalKey+"|"+product.CanonicalKey] {
				continue
			}
			seen[builder.CanonicalKey+"|"+product.CanonicalKey] = true
			key := builder.CanonicalKey
			if _, started := first[key]; !started {
				// MENU=2 is the first numbered page [fmt tdf].
				first[key] = max(used[key], 1) + 1
			}
			menu := first[key] + count[key]/factionPageButtons
			if menu > 255 {
				return nil, fmt.Errorf("nanolathe: faction menu entries exceed the page byte: logical path %s, providers searched [%s], expected at most page 254 of %s", prov.LogicalPath, prov.ProviderID, builder.UnitName)
			}
			placements = append(placements, DownloadMenuPlacement{
				Builder:         builder.UnitName,
				Menu:            uint8(menu),
				Button:          uint8(count[key] % factionPageButtons),
				Product:         product.UnitName,
				BuilderResolved: true,
				ProductResolved: true,
				Provenance:      prov,
				FileOrder:       files,
				ItemOrder:       item,
			})
			count[key]++
			used[key] = menu
		}
		files++
	}
	return placements, nil
}
