//go:build retail

package hud

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
)

// Against the compiled retail corpus the radial's partition must hold for
// every builder: each authored placement product (the bound rules'
// membership, here the catalog baseline) lands in exactly one bucket, and
// the opening chain classifies the way a player expects — the extractor
// is Metal, the solar Energy, the lab a Factory (DESIGN_INTERFACE_HUD_INPUT
// §3.18). Skips without the retail opt-in, like every corpus test.
func TestRadialRetailCorpusPartition(t *testing.T) {
	cat, _ := retailcat.Shared(t)
	for key, menu := range cat.BuildMenus {
		if menu == nil || len(menu.Buttons) == 0 {
			continue
		}
		groups := RadialGroups(cat, menu.Buttons)
		seen := map[string]int{}
		total := 0
		for _, g := range groups {
			for _, p := range g.Products {
				seen[p]++
				total++
			}
		}
		// Authored menus list some products twice; the radial offers the
		// first occurrence, so the expectation counts unique products.
		want := 0
		unique := map[string]bool{}
		for _, p := range menu.Buttons {
			if unique[p] {
				continue
			}
			unique[p] = true
			def, ok := cat.Unit(p)
			if !ok || def == nil || !ProductArmsPlacement(def) {
				continue
			}
			want++
			if seen[p] != 1 {
				t.Errorf("builder %s product %s grouped %d times, want 1 (groups %v)", key, p, seen[p], groups)
			}
		}
		if total != want {
			t.Errorf("builder %s grouped %d placement products, want %d (groups %v)", key, total, want, groups)
		}
	}
	for side := range cat.Sides {
		chain := retailcat.SelectOpeningChain(t, cat, side)
		for product, want := range map[string]RadialCategory{
			chain.Mex:     RadialMetal,
			chain.Solar:   RadialEnergy,
			chain.KbotLab: RadialFactory,
		} {
			def, _ := cat.Unit(product)
			if got := RadialCategoryOf(cat, def); got != want {
				t.Errorf("side %d product %s bucket = %v, want %v", side, product, got, want)
			}
		}
	}
}
