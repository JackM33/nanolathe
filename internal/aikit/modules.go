package aikit

import "github.com/nanolathe-gg/nanolathe/internal/content"

// Upgrade modules (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI upgrade
// modules").
//
// A fixture is a definition whose selected footprint is empty, either
// extent zero: it occupies no cell, and the factory caller places it
// without inspecting the ground [04 R-P0-08-C]. Content uses such products
// as upgrades their producer keeps. Talon's commander upgrade module
// retains each product it completes, and the commander's script reads the
// completed products as its shield, drone, missile pack and uplink
// (research/extensions/talon-engine.md "Commander upgrades").
//
// An upgrade module is an immobile builder with build power whose every
// product is a fixture and which has no purpose of its own: no role from
// its definition (weapon, economy, storage, sensor, transport, commander),
// no active weapon (a superweapon's is left out of DPS), and no air base
// or teleporter. It makes no unit that leaves it, so it is not a factory.
// A structure that does have a purpose and makes fixtures (a reactor's
// shield, an artillery piece's shells, the zero-worker sensor plants)
// upgrades itself; this rule leaves it as it was. The rule reads authored
// keys only, never a unit name.

// emptyFootprint reports a definition whose selected footprint is empty
// [04 R-P0-08-C].
func emptyFootprint(def *content.UnitDef) bool {
	return def != nil && def.FootprintX >= 0 && def.FootprintZ >= 0 && (def.FootprintX == 0 || def.FootprintZ == 0)
}

// ownPurpose reports whether an immobile builder does something besides
// making its products.
func ownPurpose(u *UnitInfo) bool {
	def := u.Def
	if u.Role&^RoleFactory != 0 || u.DPS != 0 || def.IsAirBase || def.Teleporter {
		return true
	}
	for _, w := range [...]*content.WeaponDef{def.Weapon1Def, def.Weapon2Def, def.Weapon3Def} {
		if weaponActive(w) {
			return true
		}
	}
	return false
}

// fixtureRoles are the mobile unit's roles summarize may have given a
// fixture: a module keeps what it makes, so the fixture is not a unit to
// send anywhere.
const fixtureRoles = RoleMobile | RoleScout | RoleCombat | RoleFighter | RoleBomber

// classifyModules runs after products are linked and builders classified:
// a module's role is RoleModule alone, and each of its products takes
// RoleUpgrade in place of the mobile unit's roles.
func (t *Table) classifyModules() {
	for _, u := range t.Units {
		def := u.Def
		if def == nil || !def.Builder || def.BMCode != 0 || u.Role.Has(RoleMobile) || u.BuildPower <= 0 || len(u.Builds) == 0 || ownPurpose(u) {
			continue
		}
		fixtures := true
		for _, p := range u.Builds {
			if !emptyFootprint(p.Def) {
				fixtures = false
				break
			}
		}
		if !fixtures {
			continue
		}
		u.Role = RoleModule
		for _, p := range u.Builds {
			p.Role = p.Role&^fixtureRoles | RoleUpgrade
		}
	}
}
