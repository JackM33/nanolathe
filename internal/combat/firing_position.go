package combat

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// One visit's physical firing evidence, consumed by the subsequent order
// visit. This is Nanolathe Modern policy, not retail behavior:
// DESIGN_UNITS_ORDERS_COB "Modern firing positions".
type firingPositionObservation struct {
	shooter *units.Unit
	tick    uint32
	slots   [NumSlots]firingPositionShot
}

type firingPositionShot struct {
	target                 *units.Unit
	fired, blocked         bool
	launch                 Slot
	origin, muzzle, aim    Vec3
	yawSpread, pitchSpread uint16
}

// firingPositionPreview is one candidate's scratch. The service owns it so
// passing the translated unit, adjusted weapon and query through rule callbacks
// does not allocate. Each call restores the whole frame before returning, so a
// nested preview preserves the pointers an outer callback is still using.
// Rules cannot retain request arguments (DESIGN_GAMEPLAY_RULES §3).
type firingPositionPreview struct {
	shooter units.Unit
	query   ShotQuery
	weapon  content.WeaponDef
}

func (s *Service) observeFiringPosition(q *ShotQuery, before *Slot, idx int, fired bool) {
	o := &s.firingPosition
	if q == nil || q.Shooter != o.shooter || q.Tick != o.tick || q.Target == nil || idx < 0 || idx >= len(o.slots) {
		return
	}
	if !fired && (!q.Blocked || q.Covered) {
		return
	}
	// On refusal TryFire has not committed its trial slot: before still has
	// the unspread pair, including the muzzle query's absolute yaw conversion.
	// Success commits that pair, but successful shots never supply candidates.
	o.slots[idx] = firingPositionShot{
		target: q.Target, fired: fired, blocked: !fired && q.Blocked,
		launch: q.Launch, origin: Vec3{X: q.Shooter.X, Y: q.Shooter.Y, Z: q.Shooter.Z},
		muzzle: q.Muzzle, aim: q.Aim,
		yawSpread:   q.Launch.DesiredYaw - before.DesiredYaw,
		pitchSpread: q.Launch.DesiredPitch - before.DesiredPitch,
	}
}

func (s *Service) blockedFiringPosition(shooter, target *units.Unit, tick uint32) *firingPositionShot {
	if s == nil || !previewsShot(s.rules()) || shooter == nil || !shooter.Alive || shooter.Dying ||
		target == nil || !target.Alive || target.Dying || s.firingPosition.shooter != shooter || s.firingPosition.tick != tick {
		return nil
	}
	var blocked *firingPositionShot
	for idx := range s.firingPosition.slots {
		shot := &s.firingPosition.slots[idx]
		if shot.target != target {
			continue
		}
		// Any successful slot makes movement unnecessary for this target,
		// including a success before the physically refused slot.
		if shot.fired {
			return nil
		}
		slot := shooter.SlotAt(idx)
		if blocked == nil && shot.blocked && slot != nil && slot.Weapon == shot.launch.Weapon &&
			slot.Target.Kind == units.TargetUnit && slot.Target.Unit == target.Handle {
			blocked = shot
		}
	}
	return blocked
}

// FiringPositionBlocked reports a physical refusal against this exact live
// target in the current weapon visit. Tactical coverage holds are not blocks.
func (s *Service) FiringPositionBlocked(shooter, target *units.Unit, tick uint32) bool {
	return s.blockedFiringPosition(shooter, target, tick) != nil
}

// FiringPositionClear predicts an observed shot from a translated shooter.
// It queries no script and changes no unit, stream, resource or projectile.
// Unknown flight geometry rejects a candidate; actual firing remains authority.
func (s *Service) FiringPositionClear(shooter, target *units.Unit, tick uint32, x, y, z numeric.Fixed, w *units.World, terrain *world.Terrain) bool {
	shot := s.blockedFiringPosition(shooter, target, tick)
	if shot == nil || w == nil || terrain == nil || w.Unit(shooter.Handle) != shooter || w.Unit(target.Handle) != target {
		return false
	}
	saved := s.firingPreview
	defer func() { s.firingPreview = saved }()
	preview := &s.firingPreview
	preview.shooter = *shooter
	preview.shooter.X, preview.shooter.Y, preview.shooter.Z = x, y, z
	launch := shot.launch
	weapon := launch.Weapon
	// Predict only the initial projectile. Later burst pellets retain their
	// own real admission, including inherited spray and refreshed muzzle.
	if weapon.Burst != 0 && (liveCreationFamilyForWeapon(weapon) != CreationOrdinary || MotionFamilyForWeapon(weapon) != MotionDirect) {
		preview.weapon = *weapon
		preview.weapon.Burst = 0
		weapon = &preview.weapon
		launch.Weapon = weapon
	}
	if !checkAdmission(s, &preview.shooter, weapon, shot.aim, terrain) {
		return false
	}
	muzzle := Vec3{X: shot.muzzle.X.Add(x.Sub(shot.origin.X)), Y: shot.muzzle.Y.Add(y.Sub(shot.origin.Y)), Z: shot.muzzle.Z.Add(z.Sub(shot.origin.Z))}
	if liveCreationFamilyForWeapon(weapon) == CreationBallistic {
		dx, dy, dz := shot.aim.X.Sub(muzzle.X), shot.aim.Y.Sub(muzzle.Y), shot.aim.Z.Sub(muzzle.Z)
		pitch, ok := BallisticSolve(dx, dy, dz, numeric.Fixed(weapon.WeaponVelocity), terrain.Gravity, weapon.MinBarrelAngle)
		if !ok {
			return false
		}
		// Re-solve the candidate's arc, retaining the already-observed spread
		// without drawing again [06 §3.3][06 §4.4].
		launch.DesiredYaw = retailYawFromGo(uint16(YawFromDelta(dx, dz))) + shot.yawSpread
		launch.DesiredPitch = pitch + shot.pitchSpread
	}
	// The moving-target guidance fallback proves only the first step; it
	// cannot establish a useful firing position for the remainder of flight.
	if MotionFamilyForWeapon(weapon) == MotionSelfProp && weapon.Guidance && !weapon.TwoPhase &&
		(weapon.BurnBlow || target.Def == nil || target.Def.MaxVelocity != 0) {
		return false
	}
	if modernTerrainAdmission(launch, muzzle, shot.aim, tick, terrain, target, s.ProjectileWind) != terrainShotClear {
		return false
	}
	preview.query = ShotQuery{Service: s, World: w, Shooter: &preview.shooter, Target: target, Tick: tick, Terrain: terrain,
		Launch: launch, Muzzle: muzzle, Aim: shot.aim, Wind: s.ProjectileWind}
	// Do not call AdmitShot: incoming-damage coordination must never choose
	// a place to move. Only the physical obstruction kernels apply here.
	return !modernObstructedShot(&preview.query)
}
