package aikit

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// Two authored shapes the Modern AI read as unarmed: a flak weapon flagged
// toairweapon with no default damage, whose damage is all in entries naming
// aircraft, and an armed mobile unit authoring builder=1 that builds
// nothing (Talon's Vanguard flak tank has both). Each is armed, at its
// anti-air rate, and a combat unit or a defense; a real constructor stays a
// builder.
func TestDamageTableAntiAirIsArmed(t *testing.T) {
	unit := func(key string) *content.UnitDef {
		return &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: key}, UnitName: key, Side: "TAL", MaxDamage: 100, FootprintX: 2, FootprintZ: 2}
	}
	flak := &content.WeaponDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "flaklaser"}, Name: "flak laser", ID: 7, ToAirWeapon: true, ReloadTime: 3, Range: 800,
		Damage: map[string]int32{"jet": 200}}
	jet, tank, tower, con := unit("jet"), unit("tank"), unit("tower"), unit("con")
	jet.CanFly, jet.CanMove, jet.BMCode = true, true, 1
	tank.CanMove, tank.BMCode, tank.Builder, tank.Weapon1Def = true, 1, true, flak
	tower.Weapon1Def = flak
	con.CanMove, con.BMCode, con.Builder, con.WorkerTime, con.Weapon1Def = true, 1, true, 80, flak
	menu := func(builder string, products ...string) *content.BuildMenuPage {
		return &content.BuildMenuPage{DefinitionHeader: content.DefinitionHeader{CanonicalKey: builder}, Builder: builder, Buttons: products}
	}
	cat := &content.Catalog{
		Units:      map[string]*content.UnitDef{"jet": jet, "tank": tank, "tower": tower, "con": con},
		BuildMenus: map[string]*content.BuildMenuPage{"con": menu("con", "tower"), "tank": menu("tank")},
	}
	table := BuildTable(cat, nil)
	tk, tw, c := table.Lookup("tank"), table.Lookup("tower"), table.Lookup("con")
	if !tk.Role.Has(RoleCombat|RoleAntiAir) || tk.Role.Has(RoleScout) || tk.DPS != 0 || tk.AirDPS <= 0 {
		t.Fatalf("flak tank: role %#x DPS %d air %d; want an anti-air combat unit with anti-air fire and no ground fire", uint32(tk.Role), tk.DPS, tk.AirDPS)
	}
	if !tw.Role.Has(RoleDefense|RoleAntiAir) || tw.AirDPS <= 0 {
		t.Fatalf("flak tower: role %#x air %d; want an anti-air defense", uint32(tw.Role), tw.AirDPS)
	}
	if !c.Role.Has(RoleBuilder) || c.Role.Has(RoleCombat) {
		t.Fatalf("armed constructor: role %#x; want a builder, not combat", uint32(c.Role))
	}
}

// A unit's nl_ai_dpspermille key scales the damage the table credits it, so
// a unit whose script holds its fire is rated at what it delivers; a value
// outside 0..1000 is ignored (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI
// damage hints").
func TestDamageHintScalesTheTable(t *testing.T) {
	gun := &content.WeaponDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "gun"}, Name: "gun", ID: 9, DamageDefault: 100, ReloadTime: 6, Range: 300}
	tank := func(key, hint string) *content.UnitDef {
		u := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: key}, UnitName: key, Side: "TAL", MaxDamage: 100, FootprintX: 2, FootprintZ: 2, CanMove: true, BMCode: 1, Weapon1Def: gun}
		if hint != "" {
			u.Unknown = map[string]string{"NL_AI_DPSPERMILLE": hint}
		}
		return u
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"plain": tank("plain", ""), "hinted": tank("hinted", "150"), "bad": tank("bad", "2000")}}
	table := BuildTable(cat, nil)
	plain, hinted, bad := table.Lookup("plain"), table.Lookup("hinted"), table.Lookup("bad")
	if plain.DPS != 500 || hinted.DPS != 75 || bad.DPS != 500 {
		t.Fatalf("DPS plain %d hinted %d out-of-range %d; want 500, 75, 500", plain.DPS, hinted.DPS, bad.DPS)
	}
}
