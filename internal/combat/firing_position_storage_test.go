package combat

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func firingPositionStorageFixture(t testing.TB, weapon *content.WeaponDef) ShotQuery {
	t.Helper()
	w := newCombatFixtureWorld(10, nil)
	terrain := &world.Terrain{CellW: contactCellsPerSide, CellH: contactCellsPerSide}
	terrain.Plot = make([]world.PlotCell, terrain.CellW*terrain.CellH)
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	muzzle, aim := modernTerrainPoints()
	makeUnit := func(owner uint8, point Vec3) *units.Unit {
		def := contactDef(contactModelTop)
		def.StandingFireOrder = 2
		h, err := w.Create(def, owner, point.X, point.Y, point.Z)
		if err != nil {
			t.Fatal(err)
		}
		return w.Unit(h)
	}
	shooter, target := makeUnit(0, muzzle), makeUnit(1, aim)
	shooter.InstallWeapon(0, weapon)
	shooter.SlotAt(0).Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	shooter.SlotAt(0).Flags &^= units.SlotFlagAutonomous
	stampGroundRect(terrain, 12, 1, 1, 1, target.Handle)
	svc := NewServiceWithProjectileCapacity(1)
	svc.Rules = &ModernRules{}
	svc.ProjectileWind = &world.Wind{}
	q := ShotQuery{Service: svc, World: w, Shooter: shooter, Target: target, Terrain: terrain,
		Launch: Slot{Weapon: weapon, Target: Target{Kind: TargetUnit, Unit: target.Handle}},
		Muzzle: muzzle, Aim: aim, Tick: 10, Blocked: true}
	// The fixture starts at the existing physical-refusal observation boundary;
	// candidate prediction itself neither fires nor queries a script.
	svc.firingPosition = firingPositionObservation{shooter: shooter, tick: q.Tick}
	svc.observeFiringPosition(&q, &q.Launch, 0, false)
	return q
}

func firingPositionStorageCandidate(q ShotQuery, x, y, z numeric.Fixed) bool {
	return q.Service.FiringPositionClear(q.Shooter, q.Target, q.Tick, x, y, z, q.World, q.Terrain)
}

type firingPositionStorageRules struct {
	ModernRules
	admit    func(ShotTimeAdmission) bool
	guidance func(*Service, *Projectile, *content.WeaponDef, int16, uint8) bool
}

func (r *firingPositionStorageRules) ShotTimeAdmitted(q ShotTimeAdmission) bool {
	if r.admit != nil {
		return r.admit(q)
	}
	return r.ModernRules.ShotTimeAdmitted(q)
}

func (r *firingPositionStorageRules) GuidanceAdmitted(s *Service, p *Projectile, w *content.WeaponDef, y int16, sea uint8) bool {
	if r.guidance != nil {
		return r.guidance(s, p, w, y, sea)
	}
	return r.ModernRules.GuidanceAdmitted(s, p, w, y, sea)
}

// A rule may make another synchronous request, but cannot retain its request
// arguments (DESIGN_GAMEPLAY_RULES §3). Both admission and guidance receive
// pointers that must still name the outer candidate after a nested return.
func TestFiringPositionStorageNestedCallbacks(t *testing.T) {
	for _, boundary := range []string{"admission", "guidance"} {
		t.Run(boundary, func(t *testing.T) {
			weapon := guidedTerrainWeapon()
			weapon.Burst = 3 // exercise the initial-shot weapon copy as well
			q := firingPositionStorageFixture(t, weapon)
			q.Service.shotQuery = ShotQuery{Shooter: q.Target, Tick: 99, Covered: true}
			fireQuery := q.Service.shotQuery
			inside, nested := false, false
			visit := func() {
				t.Helper()
				inside, nested = true, true
				if !firingPositionStorageCandidate(q, q.Shooter.X, q.Shooter.Y, cellCentre(4)) {
					t.Fatal("nested clear candidate refused")
				}
				if firingPositionStorageCandidate(q, numeric.FixedFromInt(2000), q.Shooter.Y, q.Shooter.Z) {
					t.Fatal("nested out-of-range candidate admitted")
				}
				inside = false
			}
			rules := &firingPositionStorageRules{}
			if boundary == "admission" {
				rules.admit = func(a ShotTimeAdmission) bool {
					if !inside && !nested {
						if a.Shooter == q.Shooter || a.Weapon == weapon || a.Weapon.Burst != 0 {
							t.Fatal("admission did not receive independent candidate values")
						}
						unitBefore, weaponBefore := *a.Shooter, *a.Weapon
						visit()
						if !reflect.DeepEqual(*a.Shooter, unitBefore) || !reflect.DeepEqual(*a.Weapon, weaponBefore) {
							t.Fatal("nested request changed outer admission arguments")
						}
					}
					return strictShotTimeAdmits(a)
				}
			} else {
				rules.guidance = func(s *Service, p *Projectile, w *content.WeaponDef, y int16, sea uint8) bool {
					if !inside && !nested {
						projectileBefore, weaponBefore := *p, *w
						visit()
						if !reflect.DeepEqual(*p, projectileBefore) || !reflect.DeepEqual(*w, weaponBefore) {
							t.Fatal("nested request changed outer guidance arguments")
						}
					}
					return (StrictRules{}).GuidanceAdmitted(s, p, w, y, sea)
				}
			}
			q.Service.Rules = rules
			shooterBefore, targetBefore, weaponBefore := *q.Shooter, *q.Target, *weapon
			simBefore, crtBefore := rng.Global.Sim, rng.Global.Crt
			if !firingPositionStorageCandidate(q, q.Shooter.X, q.Shooter.Y, q.Shooter.Z) || !nested {
				t.Fatal("clear outer candidate did not reach nested callback")
			}
			if !reflect.DeepEqual(*q.Shooter, shooterBefore) || !reflect.DeepEqual(*q.Target, targetBefore) ||
				!reflect.DeepEqual(*weapon, weaponBefore) || !reflect.DeepEqual(q.Service.shotQuery, fireQuery) ||
				rng.Global.Sim != simBefore || rng.Global.Crt != crtBefore {
				t.Fatal("nested candidate changed source state, live fire query or RNG")
			}
		})
	}
}

func TestFiringPositionStorageRefusalsPreserveSources(t *testing.T) {
	for _, name := range []string{"clear", "range", "medium", "unknown", "moving guidance", "terrain"} {
		t.Run(name, func(t *testing.T) {
			weapon := modernTerrainWeapon()
			if name == "moving guidance" {
				weapon = guidedTerrainWeapon()
				weapon.Burst = 3
			}
			q := firingPositionStorageFixture(t, weapon)
			x, y, z := q.Shooter.X, q.Shooter.Y, q.Shooter.Z
			switch name {
			case "range":
				x = numeric.FixedFromInt(2000)
			case "medium":
				y = numeric.FixedFromInt(-100)
			case "unknown":
				weapon.Cruise = true
			case "moving guidance":
				q.Target.Def.MaxVelocity = 1
			case "terrain":
				q.Terrain.PlotAt(6, 1).SetMinHeight(64)
			}
			shooterBefore, targetBefore, weaponBefore := *q.Shooter, *q.Target, *weapon
			plotBefore := append([]world.PlotCell(nil), q.Terrain.Plot...)
			observationBefore := q.Service.firingPosition
			simBefore, crtBefore := rng.Global.Sim, rng.Global.Crt
			q.Service.SetEvents(func(Event) { t.Fatal("candidate emitted event") })
			if got, want := firingPositionStorageCandidate(q, x, y, z), name == "clear"; got != want {
				t.Fatalf("candidate clear = %v, want %v", got, want)
			}
			if !reflect.DeepEqual(*q.Shooter, shooterBefore) || !reflect.DeepEqual(*q.Target, targetBefore) ||
				!reflect.DeepEqual(*weapon, weaponBefore) || !reflect.DeepEqual(q.Terrain.Plot, plotBefore) ||
				q.Service.firingPosition != observationBefore || q.Service.Count() != 0 ||
				rng.Global.Sim != simBefore || rng.Global.Crt != crtBefore {
				t.Fatal("candidate changed its sources, observation, projectile pool or RNG")
			}
		})
	}
}

func TestFiringPositionStorageLeavesCheckpointUnchanged(t *testing.T) {
	q := firingPositionStorageFixture(t, modernTerrainWeapon())
	q.Service.ProjectileWind = nil // direct motion needs no wind binding
	c := combatCheckpointContext(t, q.Launch.Weapon)
	before := combatCheckpointBytes(t, q.Service, c)
	if !firingPositionStorageCandidate(q, q.Shooter.X, q.Shooter.Y, q.Shooter.Z) {
		t.Fatal("clear candidate refused")
	}
	if !bytes.Equal(combatCheckpointBytes(t, q.Service, c), before) {
		t.Fatal("candidate preview changed canonical state")
	}
}

var firingPositionStorageSink bool

// Opt-in request benchmark: fixture creation is outside the measured window.
// The flight kernel's own preview allocation is separate from the translated
// unit, shot query and initial-shot weapon storage under measurement here.
func BenchmarkFiringPositionStorage(b *testing.B) {
	for _, name := range []string{"direct", "guided burst", "range refusal"} {
		b.Run(name, func(b *testing.B) {
			weapon := modernTerrainWeapon()
			if name == "guided burst" {
				weapon = guidedTerrainWeapon()
				weapon.Burst = 3
			}
			q := firingPositionStorageFixture(b, weapon)
			x := q.Shooter.X
			if name == "range refusal" {
				x = numeric.FixedFromInt(2000)
			}
			want := name != "range refusal"
			if firingPositionStorageCandidate(q, x, q.Shooter.Y, q.Shooter.Z) != want {
				b.Fatal("fixture candidate did not reach expected verdict")
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				firingPositionStorageSink = firingPositionStorageCandidate(q, x, q.Shooter.Y, q.Shooter.Z)
			}
			if firingPositionStorageSink != want {
				b.Fatal("candidate verdict changed")
			}
		})
	}
}
