package hud

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// The optional build radial's category buckets (DESIGN_INTERFACE_HUD_INPUT
// §3.18). The radial is a Nanolathe host presentation policy, not retail
// behaviour: retail has no such grouping, so the buckets are derived here
// from authored definition fields — never unit names, so mods classify by
// what their structures do — and they decide presentation grouping only.
// Nothing here reaches the simulation [I6].

// RadialCategory is one first-level bucket. The declaration order is the
// ring order: each category owns a fixed 60-degree slot starting at the top
// and running clockwise, so flick directions stay learnable even when a
// builder has no product in a bucket.
type RadialCategory int

const (
	RadialMetal RadialCategory = iota
	RadialEnergy
	RadialFactory
	RadialDefence
	RadialUtility
	RadialMisc

	// RadialCategoryCount is the fixed number of ring slots.
	RadialCategoryCount = int(RadialMisc) + 1
)

// Label is the bucket's hub and ring caption.
func (c RadialCategory) Label() string {
	switch c {
	case RadialMetal:
		return "Metal"
	case RadialEnergy:
		return "Energy"
	case RadialFactory:
		return "Factories"
	case RadialDefence:
		return "Defence"
	case RadialUtility:
		return "Radar & Utility"
	default:
		return "Misc"
	}
}

// RadialCategoryOf buckets one definition by its authored fields, using
// the economy's own terms for production [02 "Unit record"]. A structure
// with the builder flag is a factory when its authored menu names a mobile
// product, and otherwise a utility: an air repair pad authors the flag
// and a small energy make, and belongs with the support structures, not
// the generators. Metal comes from extraction, metal making and the
// passive metal make; energy from the passive energy make net of upkeep
// (a solar authors its output as a negative energy use, and a radar's
// equal make and use net to nothing), or the wind and tidal generator
// branches. A defence authors at least one weapon name — the resolved
// weapon pointers are populated for every definition, armed or not, so
// they cannot carry this test. The tests run in this order because a
// structure can satisfy several: a metal producer that also stores energy
// is metal first, and an armed radar is a defence first. Anything no test
// recognises is Misc — a bucket, never a drop.
func RadialCategoryOf(cat *content.Catalog, def *content.UnitDef) RadialCategory {
	if def == nil {
		return RadialMisc
	}
	switch {
	case def.Builder && radialBuildsMobile(cat, def):
		return RadialFactory
	case def.Builder:
		return RadialUtility
	case def.ExtractsMetal > 0 || def.MetalMake > 0 || def.MakesMetal > 0:
		return RadialMetal
	case def.EnergyMake-def.EnergyUse > 0 || def.WindGenerator > 0 || def.TidalGenerator > 0:
		return RadialEnergy
	case def.Weapon1 != "" || def.Weapon2 != "" || def.Weapon3 != "":
		return RadialDefence
	case def.RadarDistance > 0 || def.SonarDistance > 0 ||
		def.RadarDistanceJam > 0 || def.SonarDistanceJam > 0 ||
		def.EnergyStorage > 0 || def.MetalStorage > 0:
		return RadialUtility
	default:
		return RadialMisc
	}
}

// radialBuildsMobile reports whether a definition's authored build menu
// names at least one mobile (non-building) product.
func radialBuildsMobile(cat *content.Catalog, def *content.UnitDef) bool {
	if cat == nil {
		return false
	}
	if menu := cat.BuildMenus[content.CanonicalKey(def.UnitName)]; menu != nil {
		for _, button := range menu.Buttons {
			if product, ok := cat.Unit(button); ok && product != nil && product.BMCode != 0 {
				return true
			}
		}
	}
	return false
}

// RadialGroup is one non-empty bucket of a builder's placement products,
// in authored order.
type RadialGroup struct {
	Category RadialCategory
	Products []string
}

// RadialGroups partitions a builder's allowed products for the radial.
// Only placement products (building class, ProductArmsPlacement) whose
// definitions resolve are offered — factory queueing keeps the sidebar's
// counted interaction (§3.18) — and each offered product lands in exactly
// one group, preserving the authored order inside it. Authored menus can
// list one product twice; the sidebar keeps both records, but two ring
// slots for one structure would only split its flick direction, so the
// radial offers the first occurrence. Groups are returned in ring order
// with empty buckets omitted. The products slice is the committed frame's
// published membership and is only read [I6].
func RadialGroups(cat *content.Catalog, products []string) []RadialGroup {
	if cat == nil || len(products) == 0 {
		return nil
	}
	var buckets [RadialCategoryCount][]string
	seen := make(map[string]bool, len(products))
	placed := 0
	for _, product := range products {
		key := content.CanonicalKey(product)
		if seen[key] {
			continue
		}
		seen[key] = true
		def, ok := cat.Unit(product)
		if !ok || def == nil || !ProductArmsPlacement(def) {
			continue
		}
		c := RadialCategoryOf(cat, def)
		buckets[c] = append(buckets[c], product)
		placed++
	}
	if placed == 0 {
		return nil
	}
	var groups []RadialGroup
	for c := RadialMetal; c <= RadialMisc; c++ {
		if len(buckets[c]) != 0 {
			groups = append(groups, RadialGroup{Category: c, Products: buckets[c]})
		}
	}
	return groups
}
