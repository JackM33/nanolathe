package aikit

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// sideBrain records the side its kit names at Init.
type sideBrain struct {
	countBrain
	side string
}

func (b *sideBrain) Init(k *Kit) {
	b.countBrain.Init(k)
	b.side = k.Side
}

// A unit is a player's commander because its side record names it, not
// because its definition sets the commander flag [08 R-TRIG-01 §3]; converted
// Mynn's commander authors no flag (docs/DESIGN_MODS_MUTATORS.md §5.8). The
// table must root the build tree at it, and the host must take the player's
// side from it: otherwise the brain plays with no side and roots its tree at
// the first flagged commander, another side's, whose factories ours cannot
// build.
func TestSideRecordCommanderWithoutFlag(t *testing.T) {
	unit := func(key, side string) *content.UnitDef {
		return &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: key}, UnitName: key, Side: side, MaxDamage: 100, FootprintX: 2, FootprintZ: 2}
	}
	flagged, lead, plant, tank := unit("flagcom", "OTHER"), unit("lead", "AUTHORED"), unit("plant", "AUTHORED"), unit("tank", "AUTHORED")
	flagged.Commander, flagged.Builder, flagged.CanMove, flagged.BMCode = true, true, true, 1
	lead.Builder, lead.CanMove, lead.BMCode, lead.RadarDistance = true, true, 1, 400
	plant.Builder = true
	tank.CanMove, tank.BMCode = true, 1
	menu := func(builder string, products ...string) *content.BuildMenuPage {
		return &content.BuildMenuPage{DefinitionHeader: content.DefinitionHeader{CanonicalKey: builder}, Builder: builder, Buttons: products}
	}
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{"flagcom": flagged, "lead": lead, "plant": plant, "tank": tank},
		// The side record names its commander in other letter case, as
		// retail's name compare ignores case.
		Sides:      []*content.SideDef{{Name: "OTHER", Commander: "FLAGCOM"}, {Name: "Authored", Commander: "Lead"}},
		BuildMenus: map[string]*content.BuildMenuPage{"flagcom": menu("flagcom"), "lead": menu("lead", "plant"), "plant": menu("plant", "tank")},
	}

	table := BuildTable(cat, nil)
	l, p, k, f := table.Lookup("lead"), table.Lookup("plant"), table.Lookup("tank"), table.Lookup("flagcom")
	if !l.Role.Has(RoleCommander) || l.Role.Has(RoleRadar) || l.Depth != 0 {
		t.Fatalf("unflagged side commander: role %#x depth %d; want a commander (not a radar) at depth 0", uint32(l.Role), l.Depth)
	}
	if !p.Role.Has(RoleFactory) || p.Depth != 1 || k.Depth != 2 {
		t.Fatalf("its tree: plant role %#x depth %d, tank depth %d; want a factory at 1 and its product at 2", uint32(p.Role), p.Depth, k.Depth)
	}
	if !f.Role.Has(RoleCommander) || f.Depth != 0 {
		t.Fatalf("flagged commander: role %#x depth %d; want unchanged", uint32(f.Role), f.Depth)
	}

	w := fixtureWorld(cat)
	if _, err := w.Create(lead, 0, numeric.FixedFromInt(100), 0, numeric.FixedFromInt(100)); err != nil {
		t.Fatal(err)
	}
	m := ai.NewManager(ai.ManagerConfig{Player: 0, Catalog: cat})
	b := &sideBrain{}
	h := NewHost(m, b, PersonaMax)
	defer h.Close()
	econ := computerEconomy()
	for tick := uint32(1); tick < 60 && b.inits == 0; tick++ {
		h.Step(tick, w, econ)
		h.Join()
	}
	if b.inits != 1 || b.side != "AUTHORED" {
		t.Fatalf("host: %d inits, kit side %q; want one init on the side of the unflagged commander", b.inits, b.side)
	}
}
