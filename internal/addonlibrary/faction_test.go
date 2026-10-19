package addonlibrary

import (
	"path/filepath"
	"testing"
)

// A faction declaration's sides are SIDE0..N to the first gap, by name, and
// its units are the unit files whose side names one of them; an add-on
// without a declaration has none (§5.8).
func TestFactionsReadsTheDeclaration(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "faction-x.ufo", ufo(t, map[string]string{
		"factions/X/sidedata.tdf": "[SIDE0]{name=Xeno;}[SIDE1]{name=Yeti;}[SIDE3]{name=Gap;}",
		"units/xone.fbi":          "[UNITINFO]{UnitName=xone;side=XENO;}",
		"units/xtwo.fbi":          "[UNITINFO]{UnitName=xtwo;side=yeti ;}",
		"units/arm.fbi":           "[UNITINFO]{UnitName=armx;side=ARM;}",
		"units/gap.fbi":           "[UNITINFO]{UnitName=gapx;side=Gap;}",
	}))
	factions, err := Factions(Addon{ID: "x", Paths: []string{filepath.Join(dir, "faction-x.ufo")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(factions) != 1 || factions[0].ID != "X" || len(factions[0].Sides) != 2 || factions[0].Sides[0] != "Xeno" || factions[0].Sides[1] != "Yeti" || factions[0].Units != 2 {
		t.Fatalf("factions = %+v", factions)
	}
	// A declaration with no SIDE records is a unit pack: its units are the
	// ones naming none of the add-on's sides.
	write(t, dir, "units-y.ufo", ufo(t, map[string]string{
		"factions/Y/sidedata.tdf": "[CANBUILD]{}",
		"units/armx.fbi":          "[UNITINFO]{UnitName=armx;side=ARM;}",
		"units/corx.fbi":          "[UNITINFO]{UnitName=corx;side=CORE;}",
	}))
	packs, err := Factions(Addon{ID: "y", Paths: []string{filepath.Join(dir, "units-y.ufo")}})
	if err != nil || len(packs) != 1 || !packs[0].UnitPack() || packs[0].Units != 2 {
		t.Fatalf("unit pack = %+v, %v", packs, err)
	}
	staged, _, archives := fixture(t)
	addon := Addon{ID: "bonus"}
	for name := range archives {
		addon.Paths = append(addon.Paths, filepath.Join(staged, name))
	}
	if factions, err := Factions(addon); err != nil || factions != nil {
		t.Fatalf("an add-on without a declaration = %+v, %v", factions, err)
	}
}
