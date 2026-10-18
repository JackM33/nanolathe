package hud

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// radialFixture is a hand-built catalog in the shape of a commander's menu:
// one product per expected bucket, a mobile product the radial must not
// offer (placement only), and an unrecognised structure that must fall to
// Misc rather than disappear (DESIGN_INTERFACE_HUD_INPUT §3.18).
func radialFixture() *content.Catalog {
	def := func(name string, mut func(*content.UnitDef)) *content.UnitDef {
		d := &content.UnitDef{UnitName: name, CanonicalKey: content.CanonicalKey(name)}
		if mut != nil {
			mut(d)
		}
		return d
	}
	units := map[string]*content.UnitDef{}
	for _, d := range []*content.UnitDef{
		def("ARMMEX", func(d *content.UnitDef) { d.ExtractsMetal = 0.001 }),
		// Retail's solar authors production as a negative energy use, and
		// its radar an equal make and use; both shapes are locked here.
		def("ARMSOLAR", func(d *content.UnitDef) { d.EnergyUse = -20 }),
		def("ARMWIN", func(d *content.UnitDef) { d.WindGenerator = 1 }),
		def("ARMLAB", func(d *content.UnitDef) { d.Builder = true }),
		// A builder structure with no mobile product, like the air repair
		// pad, which also authors a small energy make.
		def("ARMASP", func(d *content.UnitDef) { d.Builder = true; d.EnergyMake = 10 }),
		def("ARMLLT", func(d *content.UnitDef) { d.Weapon1 = "laser" }),
		def("ARMRAD", func(d *content.UnitDef) { d.RadarDistance = 900; d.EnergyMake = 5; d.EnergyUse = 5 }),
		def("ARMESTOR", func(d *content.UnitDef) { d.EnergyStorage = 1000 }),
		def("ARMDRAG", nil),
		def("ARMPW", func(d *content.UnitDef) { d.BMCode = 1; d.Weapon1 = "emg" }),
	} {
		units[d.CanonicalKey] = d
	}
	return &content.Catalog{
		Units: units,
		BuildMenus: map[string]*content.BuildMenuPage{
			"armlab": {Buttons: []string{"ARMPW"}},
		},
	}
}

func TestRadialCategoryBucketsAuthoredFields(t *testing.T) {
	cat := radialFixture()
	for product, want := range map[string]RadialCategory{
		"ARMMEX":   RadialMetal,
		"ARMSOLAR": RadialEnergy,
		"ARMWIN":   RadialEnergy,
		"ARMLAB":   RadialFactory,
		"ARMLLT":   RadialDefence,
		"ARMRAD":   RadialUtility,
		"ARMESTOR": RadialUtility,
		"ARMASP":   RadialUtility,
		"ARMDRAG":  RadialMisc,
	} {
		d, _ := cat.Unit(product)
		if got := RadialCategoryOf(cat, d); got != want {
			t.Errorf("%s bucket = %v, want %v", product, got, want)
		}
	}
}

func TestRadialGroupsPartitionPlacementProductsInAuthoredOrder(t *testing.T) {
	cat := radialFixture()
	products := []string{"ARMSOLAR", "ARMMEX", "ARMPW", "ARMWIN", "ARMLLT", "ARMDRAG", "ARMLAB", "ARMRAD", "armsolar", "MISSING"}
	groups := RadialGroups(cat, products)
	var seen []string
	byCategory := map[RadialCategory][]string{}
	for _, g := range groups {
		byCategory[g.Category] = g.Products
		seen = append(seen, g.Products...)
	}
	// The mobile ARMPW and the missing name are not placement products;
	// every other product appears exactly once.
	wantSeen := map[string]int{"ARMSOLAR": 1, "ARMMEX": 1, "ARMWIN": 1, "ARMLLT": 1, "ARMDRAG": 1, "ARMLAB": 1, "ARMRAD": 1}
	if len(seen) != len(wantSeen) {
		t.Fatalf("grouped products = %v, want %d entries", seen, len(wantSeen))
	}
	for _, p := range seen {
		if wantSeen[p] != 1 {
			t.Fatalf("product %q grouped unexpectedly in %v", p, seen)
		}
		wantSeen[p] = 0
	}
	// Authored order is kept inside a bucket: SOLAR precedes WIN as authored.
	energy := byCategory[RadialEnergy]
	if len(energy) != 2 || energy[0] != "ARMSOLAR" || energy[1] != "ARMWIN" {
		t.Fatalf("energy bucket = %v, want [ARMSOLAR ARMWIN]", energy)
	}
	// Groups come out in ring order, Metal first.
	if len(groups) == 0 || groups[0].Category != RadialMetal {
		t.Fatalf("first group = %+v, want Metal", groups)
	}
	for i := 1; i < len(groups); i++ {
		if groups[i].Category <= groups[i-1].Category {
			t.Fatalf("groups out of ring order: %v", groups)
		}
	}
}

func TestRadialGroupsEmptyAndNilSafe(t *testing.T) {
	if got := RadialGroups(nil, []string{"ARMMEX"}); got != nil {
		t.Fatalf("nil catalog groups = %v, want nil", got)
	}
	if got := RadialGroups(radialFixture(), nil); got != nil {
		t.Fatalf("no products groups = %v, want nil", got)
	}
	if got := RadialCategoryOf(nil, nil); got != RadialMisc {
		t.Fatalf("nil def bucket = %v, want Misc", got)
	}
}

// Factory classification uses the complete authored membership, including
// mobile products beyond the retail download cutoff (§3.18). The trimmed
// list still admits no mobile product in this fixture.
func TestRadialFactoryUsesCompleteAuthoredMenu(t *testing.T) {
	cat := radialFixture()
	menu := cat.BuildMenus["armlab"]
	menu.Buttons = []string{"ARMASP"}
	menu.AuthoredButtons = []string{"ARMASP", "ARMPW"}
	def, _ := cat.Unit("ARMLAB")
	if got := RadialCategoryOf(cat, def); got != RadialFactory {
		t.Fatalf("complete authored menu: category = %v, want Factories", got)
	}
}
