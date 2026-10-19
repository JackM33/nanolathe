package main

// verify mounts the base install with faction add-ons the way the game
// mounts add-ons — their archives at the base folder's *.UFO tier — compiles
// the catalog, and checks the isolation contract of §5.8 against the base
// compiled alone: every base side, weapon, feature, movement class and sound
// category is unchanged, and no two add-ons supply one path. The two
// composed exceptions are checked to name only add-on units: damage entries
// a declaration adds to a base weapon's table, and a unit pack's products
// after a base builder's own.

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func runVerify(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: factionpack verify <base> [add-on dir ...]")
	}
	baseDir, addons := args[0], args[1:]
	var archives []string
	providers := map[string]bool{} // folded archive names
	owner := map[string]string{}   // folded path -> add-on
	for _, dir := range addons {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.ufo"))
		for _, ufo := range matches {
			archives = append(archives, ufo)
			providers[lower(filepath.Base(ufo))] = true
			single := vfs.New()
			if _, err := single.MountArchive(ufo, 0); err != nil {
				single.Close()
				return err
			}
			for _, e := range single.Entries() {
				if e.IsDir {
					continue
				}
				key := lower(e.Path)
				if prior, ok := owner[key]; ok {
					single.Close()
					return fmt.Errorf("%s and %s both supply %s", prior, dir, e.Path)
				}
				owner[key] = dir
			}
			single.Close()
		}
	}
	baseCat, err := compileWith(baseDir, nil)
	if err != nil {
		return fmt.Errorf("base: %w", err)
	}
	// No add-on path may be a base path.
	baseFS := vfs.New()
	if err := baseFS.MountGameDirectories([]string{baseDir}); err != nil {
		return err
	}
	// The path check is against the whole install, add-on archives included:
	// an add-on may not replace a bonus file either.
	for key, dir := range owner {
		if _, err := baseFS.Stat(key); err == nil {
			baseFS.Close()
			return fmt.Errorf("%s supplies %s, which the base install supplies", dir, key)
		}
	}
	baseFS.Close()
	cat, err := compileWith(baseDir, archives)
	if err != nil {
		return fmt.Errorf("composed: %w", err)
	}
	var problems []string
	for i, s := range baseCat.Sides {
		if i >= len(cat.Sides) || cat.Sides[i].Hash != s.Hash {
			problems = append(problems, "base side "+s.Name+" changed")
		}
	}
	addonUnit := func(name string) bool {
		u := cat.Units[content.CanonicalKey(name)]
		return u != nil && baseCat.Units[content.CanonicalKey(name)] == nil && providers[lower(u.Provenance.ProviderID)]
	}
	for k, w := range baseCat.Weapons {
		got := cat.Weapons[k]
		if got == nil || got.Hash != w.Hash || got.ID != w.ID {
			problems = append(problems, "base weapon "+k+" changed")
			continue
		}
		for _, key := range got.DamageKeysSorted() {
			if v, ok := w.Damage[key]; ok && got.Damage[key] == v {
				continue
			}
			if _, base := w.Damage[key]; base || !addonUnit(key) {
				problems = append(problems, "base weapon "+k+" damage against "+key+" changed")
			}
		}
	}
	for _, e := range cat.FactionDamage {
		if !addonUnit(e.Unit) {
			problems = append(problems, "composed damage of "+e.Weapon+" names "+e.Unit+", which no add-on defines")
		}
	}
	for k, f := range baseCat.Features {
		if got := cat.Features[k]; got == nil || got.Hash != f.Hash {
			problems = append(problems, "base feature "+k+" changed")
		}
	}
	for k, m := range baseCat.Movement {
		if got := cat.Movement[k]; got == nil || got.Hash != m.Hash {
			problems = append(problems, "base movement class "+k+" changed")
		}
	}
	for i, s := range baseCat.SoundCategoryOrder {
		if i >= len(cat.SoundCategoryOrder) || cat.SoundCategoryOrder[i].Hash != s.Hash {
			problems = append(problems, "base sound category "+s.Name+" changed")
		}
	}
	capped := 0
	for k, b := range baseCat.BuildMenus {
		got := cat.BuildMenus[k]
		if got == nil || len(got.AuthoredButtons) < len(b.AuthoredButtons) || strings.Join(got.AuthoredButtons[:len(b.AuthoredButtons)], ",") != strings.Join(b.AuthoredButtons, ",") {
			problems = append(problems, "base build menu "+k+" changed")
			continue
		}
		for _, extra := range got.AuthoredButtons[len(b.AuthoredButtons):] {
			if !addonUnit(extra) {
				problems = append(problems, "base build menu "+k+" gained "+extra+", which no add-on defines")
			}
		}
		if len(got.AuthoredButtons) > len(got.Buttons) && len(got.AuthoredButtons) > len(b.AuthoredButtons) {
			capped++
			fmt.Printf("  %s offers %d products; Strict 3.1's download cutoff keeps %d [02 R-CAT-01 §8]\n", got.Builder, len(got.AuthoredButtons), len(got.Buttons))
		}
	}
	for k, u := range baseCat.Units {
		got := cat.Units[k]
		if got == nil || got.Provenance.LogicalPath != u.Provenance.LogicalPath || got.Provenance.ProviderID != u.Provenance.ProviderID || got.Side != u.Side || got.MaxDamage != u.MaxDamage ||
			weaponName(got.Weapon1Def) != weaponName(u.Weapon1Def) || weaponName(got.Weapon2Def) != weaponName(u.Weapon2Def) ||
			weaponName(got.Weapon3Def) != weaponName(u.Weapon3Def) {
			problems = append(problems, "base unit "+k+" changed")
		}
	}
	sort.Strings(problems)
	fmt.Printf("base: %d sides, %d units, %d weapons; composed: %d sides, %d units, %d weapons, limits units %d weapons %d\n",
		len(baseCat.Sides), len(baseCat.Units), len(baseCat.Weapons), len(cat.Sides), len(cat.Units), len(cat.Weapons), cat.CompiledLimits().Units, cat.CompiledLimits().Weapons)
	for i, s := range cat.Sides {
		units := 0
		for _, u := range cat.UnitRecords() {
			if strings.EqualFold(u.Side, s.Name) {
				units++
			}
		}
		fmt.Printf("  side %d %s commander %s prefix %s: %d units\n", i, s.Name, s.Commander, s.NamePrefix, units)
	}
	inactive := 0
	for _, u := range cat.UnitRecords() {
		for _, w := range []string{u.Weapon1, u.Weapon2, u.Weapon3} {
			if strings.TrimSpace(w) != "" && cat.Weapons[content.CanonicalKey(w)] == nil {
				inactive++
			}
		}
	}
	fmt.Printf("  %d unit weapon links resolve to no weapon; %d warnings\n", inactive, len(cat.Warnings))
	placed := 0
	for _, p := range cat.DownloadPlacements {
		if strings.HasPrefix(lower(p.Provenance.LogicalPath), content.FactionDir+"/") {
			placed++
		}
	}
	fmt.Printf("  %d composed damage entries; %d composed menu entries; %d base builders past the Strict cutoff\n", len(cat.FactionDamage), placed, capped)
	if printWarnings {
		base := map[string]bool{}
		for _, w := range baseCat.Warnings {
			base[w] = true
		}
		for _, w := range cat.Warnings {
			if !base[w] {
				fmt.Println("   warning:", w)
			}
		}
	}
	if len(addons) > 1 {
		problems = append(problems, independence(baseDir, archives, cat)...)
	}
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "  ", p)
		}
		return fmt.Errorf("%d base definitions changed", len(problems))
	}
	fmt.Println("isolation holds: every base definition is unchanged")
	if len(addons) > 1 {
		fmt.Println("independence holds: each add-on's definitions are the same alone as together, but for weapon slots and menu pages")
	}
	return nil
}

func weaponName(w *content.WeaponDef) string {
	if w == nil {
		return ""
	}
	return w.CanonicalKey
}

// compileWith compiles the base install without its own add-on archives —
// an add-on must not depend on another, such as the Cavedog bonus units a
// player's install may lack — and with extra mounted as enabled add-ons are.
func compileWith(baseDir string, extra []string) (*content.Catalog, error) {
	fs := vfs.New()
	defer fs.Close()
	if err := fs.MountGameRoots([]vfs.GameRoot{{Dir: baseDir, Archives: func(name string) bool { return !addonArchive(name) }, Extra: extra}}); err != nil {
		return nil, err
	}
	return content.CompileWithOptions(fs, content.Options{})
}

func init() {
	if os.Getenv("FACTIONPACK_WARNINGS") != "" {
		printWarnings = true
	}
}

var printWarnings bool

// independence compiles the base with each add-on alone and checks that the
// add-on's own definitions are the same in the composition of them all: the
// weapon record slot, which allocation gives by composition order, aside.
func independence(baseDir string, archives []string, all *content.Catalog) []string {
	var problems []string
	for _, ufo := range archives {
		alone, err := compileWith(baseDir, []string{ufo})
		if err != nil {
			problems = append(problems, filepath.Base(ufo)+" alone: "+err.Error())
			continue
		}
		provider := filepath.Base(ufo)
		own := func(prov content.Provenance) bool { return strings.EqualFold(prov.ProviderID, provider) }
		for k, w := range alone.Weapons {
			if !own(w.Provenance) {
				continue
			}
			got := all.Weapons[k]
			if got == nil || !sameWeapon(got, w) {
				problems = append(problems, provider+" weapon "+k+" differs when composed with the others")
			}
		}
		for k, f := range alone.Features {
			if own(f.Provenance) && (all.Features[k] == nil || all.Features[k].Hash != f.Hash) {
				problems = append(problems, provider+" feature "+k+" differs when composed with the others")
			}
		}
		for k, m := range alone.Movement {
			if own(m.Provenance) && (all.Movement[k] == nil || all.Movement[k].Hash != m.Hash) {
				problems = append(problems, provider+" movement class "+k+" differs when composed with the others")
			}
		}
		if got, want := composedOf(all, own), composedOf(alone, own); got != want {
			problems = append(problems, provider+" composes different damage or menu entries alongside the others:\n"+want+"\nversus\n"+got)
		}
		for k, u := range alone.Units {
			if !own(u.Provenance) {
				continue
			}
			got := all.Units[k]
			if got == nil || got.Side != u.Side || got.MaxDamage != u.MaxDamage || got.BuildCostMetal != u.BuildCostMetal ||
				weaponName(got.Weapon1Def) != weaponName(u.Weapon1Def) || weaponName(got.Weapon2Def) != weaponName(u.Weapon2Def) ||
				weaponName(got.Weapon3Def) != weaponName(u.Weapon3Def) || got.MovementClass != u.MovementClass {
				problems = append(problems, provider+" unit "+k+" differs when composed with the others")
			}
		}
	}
	return problems
}

// composedOf lists one add-on's composed damage entries and, per builder,
// its menu products, which do not depend on composition; the pages they land
// on do, as the weapon slots do.
func composedOf(cat *content.Catalog, own func(content.Provenance) bool) string {
	var lines []string
	for _, e := range cat.FactionDamage {
		if own(e.Provenance) {
			lines = append(lines, fmt.Sprintf("damage %s %s=%d", e.Weapon, content.CanonicalKey(e.Unit), e.Damage))
		}
	}
	for _, p := range cat.DownloadPlacements {
		if own(p.Provenance) && strings.HasPrefix(lower(p.Provenance.LogicalPath), content.FactionDir+"/") {
			lines = append(lines, fmt.Sprintf("menu %s %s", content.CanonicalKey(p.Builder), content.CanonicalKey(p.Product)))
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// sameWeapon compares two weapon definitions but for their record slot and
// what derives from it, and their mount position.
func sameWeapon(a, b *content.WeaponDef) bool {
	x, y := *a, *b
	x.ID, y.ID = 0, 0
	x.Hash, y.Hash = "", ""
	x.Provenance.MountOrder, y.Provenance.MountOrder = 0, 0
	return reflect.DeepEqual(x, y)
}
