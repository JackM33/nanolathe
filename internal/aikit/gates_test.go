package aikit

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// A gate is read from authored keys alone: an immobile structure that is no
// builder, authors a transport capacity, carries an active weapon that fires
// at units and can be switched on and off — the three keys the gate's
// script uses to dial, carry and unlink (research/extensions/talon-engine.md
// "Gate network"). Its weapon is the dialing device: the gate keeps no
// firepower, reach or other role. A structure missing any of the three, and
// a mobile transport, keep the roles they had
// (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI gates").
func TestGateRoleFromAuthoredKeys(t *testing.T) {
	dial := &content.WeaponDef{ID: 1, DamageDefault: 1337, ReloadTime: 270, Range: 20000}
	defs := map[string]*content.UnitDef{}
	unit := func(key string, f func(*content.UnitDef)) {
		d := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: key}, UnitName: key, MaxDamage: 5000, FootprintX: 9, FootprintZ: 9}
		f(d)
		defs[key] = d
	}
	unit("gate", func(d *content.UnitDef) { d.TransportCapacity, d.OnOffable, d.Weapon1Def = 255, true, dial })
	unit("tower", func(d *content.UnitDef) { d.OnOffable, d.Weapon1Def = true, dial })
	unit("dock", func(d *content.UnitDef) { d.TransportCapacity, d.OnOffable = 255, true })
	unit("fixed", func(d *content.UnitDef) { d.TransportCapacity, d.Weapon1Def = 255, dial })
	unit("lifter", func(d *content.UnitDef) {
		d.BMCode, d.CanMove, d.CanFly, d.TransportCapacity, d.OnOffable, d.Weapon1Def = 1, true, true, 5, true, dial
	})
	tab := BuildTable(&content.Catalog{Units: defs}, nil)
	g := tab.Lookup("gate")
	if g.Role != RoleGate || g.DPS != 0 || g.AirDPS != 0 || g.Range != 0 {
		t.Fatalf("gate roles %b DPS %d air %d range %d, want the gate role alone and no firepower", g.Role, g.DPS, g.AirDPS, g.Range)
	}
	for _, key := range []string{"tower", "dock", "fixed", "lifter"} {
		if u := tab.Lookup(key); u.Role.Has(RoleGate) {
			t.Errorf("%s roles %b: not a gate", key, u.Role)
		}
	}
	if u := tab.Lookup("tower"); !u.Role.Has(RoleDefense) || u.DPS == 0 {
		t.Errorf("an armed structure without a transport capacity lost its defense role: %b", u.Role)
	}
	if u := tab.Lookup("lifter"); !u.Role.Has(RoleTransport) {
		t.Errorf("a mobile transport lost its transport role: %b", u.Role)
	}
}
