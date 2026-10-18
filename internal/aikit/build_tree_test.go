package aikit

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// A player owns what its commander's build tree reaches, whatever side its
// definitions author: Talon authors some of its units TALON, TAL1 or
// AIDONTBUILD rather than its side record's TAL (docs/DESIGN_MODS_MUTATORS.md
// §5.8). A unit spelt with the player's side that no builder offers is not
// the player's.
func TestKitOwnsItsBuildTree(t *testing.T) {
	unit := func(key, side string) *content.UnitDef {
		return &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: key}, UnitName: key, Side: side, MaxDamage: 100, FootprintX: 2, FootprintZ: 2}
	}
	com, plant, shell, orphan, other := unit("talcom", "TAL"), unit("talplant", "TAL"), unit("talshell", "AIDONTBUILD"), unit("talorphan", "TAL"), unit("armcom", "ARM")
	com.Builder, com.CanMove, com.BMCode = true, true, 1
	plant.Builder, shell.Builder = true, true
	other.Builder, other.CanMove, other.BMCode = true, true, 1
	menu := func(builder string, products ...string) *content.BuildMenuPage {
		return &content.BuildMenuPage{DefinitionHeader: content.DefinitionHeader{CanonicalKey: builder}, Builder: builder, Buttons: products}
	}
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{"talcom": com, "talplant": plant, "talshell": shell, "talorphan": orphan, "armcom": other},
		Sides: []*content.SideDef{{Name: "ARM", Commander: "ARMCOM"}, {Name: "TAL", Commander: "TALCOM"}},
		BuildMenus: map[string]*content.BuildMenuPage{
			"talcom": menu("talcom", "talplant"), "talplant": menu("talplant", "talshell"),
			"talshell": menu("talshell"), "armcom": menu("armcom"),
		},
	}
	table := BuildTable(cat, nil)
	k := Kit{Side: "TAL", Table: table, Com: table.Lookup("talcom"), own: buildTree(table, table.Lookup("talcom"))}
	for key, want := range map[string]bool{"talcom": true, "talplant": true, "talshell": true, "talorphan": false, "armcom": false} {
		if got := k.Owns(table.Lookup(key)); got != want {
			t.Fatalf("Owns(%s) = %v, want %v", key, got, want)
		}
	}
	// Without a commander the kit falls back to the side spelling.
	bare := Kit{Side: "TAL", Table: table}
	if !bare.Owns(table.Lookup("talorphan")) || bare.Owns(table.Lookup("talshell")) {
		t.Fatal("a kit without a commander must compare side spellings")
	}
}
