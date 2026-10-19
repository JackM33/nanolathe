package aikit

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// An upgrade module is found from authored keys alone: an immobile builder
// with build power whose every product has an empty footprint
// [04 R-P0-08-C] and which has no purpose of its own. Its products are
// upgrades it keeps, not units to send anywhere, even one authored able to
// move (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI upgrade modules").
// A factory that also makes ordinary units, a structure with its own
// weapon, and a builder with no build power keep their roles.
func TestUpgradeModulesFromAuthoredKeys(t *testing.T) {
	defs := map[string]*content.UnitDef{}
	unit := func(key string, foot int32, f func(*content.UnitDef)) {
		d := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: key}, UnitName: key, MaxDamage: 100, FootprintX: foot, FootprintZ: foot}
		if f != nil {
			f(d)
		}
		defs[key] = d
	}
	builder := func(wt int32) func(*content.UnitDef) {
		return func(d *content.UnitDef) { d.Builder, d.WorkerTime = true, wt }
	}
	held := func(move bool) func(*content.UnitDef) {
		return func(d *content.UnitDef) { d.BMCode, d.CanMove, d.CantBeTransported = 1, move, true }
	}
	unit("com", 2, func(d *content.UnitDef) {
		d.Commander, d.Builder, d.CanMove, d.BMCode, d.WorkerTime = true, true, true, 1, 100
	})
	unit("module", 3, builder(30))
	unit("shield", 0, held(false))
	unit("drone", 0, held(true))
	unit("plant", 4, builder(240))
	unit("tank", 2, held(true))
	unit("marker", 0, held(false))
	unit("cannon", 6, func(d *content.UnitDef) {
		d.Builder, d.WorkerTime = true, 60
		d.Weapon1Def = &content.WeaponDef{ID: 7, DamageDefault: 3600, ReloadTime: 180, Range: 30000}
	})
	unit("shells", 0, held(true))
	unit("sensor", 5, builder(0))
	unit("helper", 0, held(true))
	menu := func(products ...string) *content.BuildMenuPage { return &content.BuildMenuPage{Buttons: products} }
	cat := &content.Catalog{Units: defs, BuildMenus: map[string]*content.BuildMenuPage{
		"com":    menu("module", "plant", "cannon", "sensor"),
		"module": menu("shield", "drone", "shield"),
		"plant":  menu("tank", "marker"),
		"cannon": menu("shells"),
		"sensor": menu("helper"),
	}}
	tab := BuildTable(cat, nil)
	role := func(key string) Role { return tab.Lookup(key).Role }
	if role("module") != RoleModule {
		t.Fatalf("module roles %b, want the module role alone (not a factory)", role("module"))
	}
	for _, key := range []string{"shield", "drone"} {
		if r := role(key); !r.Has(RoleUpgrade) || r.Any(RoleMobile|RoleScout|RoleCombat) {
			t.Errorf("%s roles %b, want an upgrade, not a mobile unit or scout", key, r)
		}
	}
	if r := role("plant"); r != RoleFactory || role("marker").Has(RoleUpgrade) {
		t.Errorf("a plant making ordinary units: %b, its fixture %b", r, role("marker"))
	}
	for _, key := range []string{"cannon", "sensor"} {
		if role(key).Has(RoleModule) {
			t.Errorf("%s roles %b: an armed structure or a builder without build power is not a module", key, role(key))
		}
	}
	for _, key := range []string{"shells", "helper"} {
		if role(key).Has(RoleUpgrade) {
			t.Errorf("%s roles %b: not a module's upgrade", key, role(key))
		}
	}
}
