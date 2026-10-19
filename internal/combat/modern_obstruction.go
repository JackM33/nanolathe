package combat

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// modernObstructedShot holds fire against current friendly footprints and
// features. This is Nanolathe Modern policy (DESIGN_WEAPONS_PROJECTILES §2.3.2),
// not retail collision: [06 §8.1] samples points and exempts the shooter's side.
// Motion still comes from the real creators and advance kernels. No live
// projectile, feature cache, random stream or unit state is changed.
func modernObstructedShot(q *ShotQuery) bool {
	// Explicit point fire may intentionally cross a friend or hit a feature.
	// Enemy-target air attacks also store point slots [04 R-AIR-01 §8], so
	// require the order's intent, or the burst's snapshot of that intent.
	if q.manualAttack() && ((q.Burst == nil && q.Launch.Target.Kind == TargetPoint && explicitGroundAttack(q.Shooter)) ||
		(q.Burst != nil && q.Burst.GroundAttackBurst)) {
		return false
	}
	w := q.Launch.Weapon
	if w == nil || q.Terrain == nil || q.Shooter == nil || w.Interceptor || w.Cruise ||
		w.Range < 0 || w.Range >= 32768 || w.WeaponVelocity < 0 || w.StartVelocity < 0 || w.WeaponAcceleration < 0 ||
		!terrainPointValid(q.Muzzle) || (q.Burst == nil && !terrainPointValid(q.Aim)) {
		return false
	}
	dx, dy, dz := q.Aim.X.Raw()-q.Muzzle.X.Raw(), q.Aim.Y.Raw()-q.Muzzle.Y.Raw(), q.Aim.Z.Raw()-q.Muzzle.Z.Raw()
	const max = int64(1<<31 - 1)
	if q.Burst == nil && (numeric.Abs(dx) > max || numeric.Abs(dy) > max || numeric.Abs(dz) > max || dx*dx > max*max-dz*dz) {
		return false
	}
	var target pool.Handle
	if q.Target != nil {
		target = q.Target.Handle
	}
	q.obstructionPreview = Projectile{Shooter: q.Shooter.Handle, ShooterSide: q.Shooter.Owner}
	p := &q.obstructionPreview
	motion := MotionFamilyForWeapon(w)
	if (motion == MotionBallistic || motion == MotionDropped) && q.Wind == nil {
		return false
	}
	switch creation := liveCreationFamilyForWeapon(w); {
	case q.Burst != nil:
		*p = *q.Burst
		p.CreationTick, p.BurstRemaining = q.Tick, 0
		if w.WeaponTimer != 0 {
			p.ExpiryTick = q.Tick + uint32(w.WeaponTimer)
		} else if p.Speed != 0 {
			p.ExpiryTick = q.Tick + (uint32(p.StoredPlanarDistance)+uint32(numeric.FixedFromInt(16)))/uint32(p.Speed)
		} else {
			return false
		}
	case creation == CreationOrdinary && (motion == MotionDirect || motion == MotionSelfProp):
		InitOrdinary(p, w, q.Tick, q.Muzzle, q.Aim, target)
	case creation == CreationBallistic && motion == MotionBallistic:
		if w.WeaponVelocity == 0 || q.Wind == nil || q.Launch.DistanceWord < 0 || q.Terrain.Gravity < 0 ||
			(w.BurnBlow && numeric.MulRound(numeric.Cos(numeric.Angle(q.Launch.DesiredPitch)), w.WeaponVelocity) <= 0) {
			return false
		}
		InitBallistic(p, w, q.Tick, q.Muzzle, q.Aim, target, numeric.Angle(q.Launch.DesiredPitch),
			numeric.Angle(retailYawFromGo(q.Launch.DesiredYaw)), q.Launch.DistanceWord, q.Terrain.Gravity)
	case creation == CreationVertical && motion == MotionSelfProp:
		InitVertical(p, w, q.Tick, q.Muzzle, q.Aim, target)
	case creation == CreationDropped && motion == MotionDropped:
		if q.Wind == nil {
			return false
		}
		InitDropped(p, w, q.Tick, q.Muzzle, q.Aim, target, numeric.Angle(q.Shooter.Move.Heading), q.Shooter.Move.Speed)
	default:
		return false
	}
	budget := modernTerrainSampleBudget
	// Freeze only the guidance lookup's current point [06 §6.7], keeping the
	// creator's aim geometry. No per-shot callback or persistent forecast.
	if q.Target != nil && q.Target.Alive {
		p.TargetPos = Vec3{X: q.Target.X, Y: q.Target.Y, Z: q.Target.Z}
		p.TargetUnit = 0
	}
	env := GuidanceEnv{Service: q.Service, Terrain: q.Terrain}
	firstTick := q.Tick
	if q.Burst != nil {
		if firstTick == ^uint32(0) {
			return false
		}
		// Phase-3 appends lie beyond the captured span [06 §4.3][06 §5.1].
		firstTick++
	}
	for tick := firstTick; budget > 0; tick++ {
		budget--
		previous := p.Pos
		var result AdvanceResult
		switch motion {
		case MotionDirect:
			result = AdvanceDirect(p, w, tick)
		case MotionBallistic, MotionDropped:
			// Future phase-8 wind draws are unknowable [01 §7.3].
			zeroWind := q.Wind.Min == 0 && q.Wind.Max >= 0 && q.Wind.Max <= 1 && q.Wind.DirX == 0 && q.Wind.DirZ == 0
			if tick != q.Tick && tick-1 > q.Wind.NextChange && !zeroWind {
				return false
			}
			wind := Vec3{X: numeric.Fixed(q.Wind.DirX), Z: numeric.Fixed(q.Wind.DirZ)}
			if motion == MotionDropped {
				result = AdvanceDropped(p, w, tick, wind, q.Terrain.Gravity)
			} else {
				result = AdvanceBallistic(p, w, tick, wind, q.Terrain.Gravity)
			}
		case MotionSelfProp:
			if tick >= p.ExpiryTick {
				return false
			}
			result = AdvanceSelfProp(p, w, tick, q.Terrain.Gravity, numeric.FixedFromInt(int64(q.Terrain.SeaLevel)), env)
		}
		if result != AdvanceAlive || !terrainPointValid(p.Pos) || !terrainPointValid(p.Velocity) {
			return false
		}
		if modernObstructedSegment(q, previous, p.Pos, &budget) {
			return true
		}
		// Stop at an actual contact or the aim plane. This safety policy does
		// not predict what a target or blocker will do after launch.
		cx, cz := world.WorldToCell(p.Pos.X), world.WorldToCell(p.Pos.Z)
		cell := q.Terrain.PlotAt(cx, cz)
		if cell == nil || contactUnitInCell(p, q.World, q.Terrain, cx, cz) != 0 {
			return false
		}
		// Under "No room" the flight ends at the first feature the live
		// contact ladder hits, after units and before terrain [06 §8.1]. The
		// shot is worth firing only if that blast damages the feature.
		if q.passFeatures && !w.UnitsOnly {
			if hit, damages := modernFeatureImpact(q, w, p.Pos, cx, cz, cell); hit {
				return !damages
			}
		}
		if (!w.UnitsOnly && (int16(p.Pos.Y.Raw()>>16) < int16(cell.MinHeight()) || (!w.WaterWeapon && int16(p.Pos.Y.Raw()>>16) < int16(q.Terrain.SeaLevel)))) ||
			(q.Burst == nil && modernPastAim(q.Muzzle, q.Aim, p.Pos)) || tick == ^uint32(0) {
			return false
		}
	}
	return false
}

// modernFireIntoFeatures is the "No room" clause of §2.3.2: when the shooter
// has no clear place to step to, a held shot whose only obstructions are
// features that can be destroyed fires if it would reach its target between
// the features or damage the first one it hits. Friends, features that cannot
// be destroyed, and a blast that would not reach the feature still hold it.
// The second preview runs only for a held shot.
func modernFireIntoFeatures(q *ShotQuery) bool {
	if q.Target == nil || q.Shooter == nil || q.Shooter.Def == nil || !modernNoFiringPosition(q.Shooter, q.Target, q.Tick) {
		return false
	}
	q.passFeatures = true
	blocked := modernObstructedShot(q)
	q.passFeatures = false
	return !blocked
}

// modernFeatureImpact reports whether a projectile at pos contacts a feature
// as the live ladder's feature step does, and whether the blast there reaches
// that feature's area candidate, which is what damages or ignites it
// [06 §8.1][06 §9.3][06 §13.1]. The blast centre is the contact point. The
// preview ignores the record's last-feature-cell cache, which a stale pool
// record can carry [R-DMG-01 §13].
func modernFeatureImpact(q *ShotQuery, w *content.WeaponDef, pos Vec3, cx, cz int32, cell *world.PlotCell) (hit, damages bool) {
	if cell.IsEmpty() {
		return false, false
	}
	ax, az := int(cx), int(cz)
	if cell.IsFringe() {
		ax += int(cell.AnchorDXSigned())
		az += int(cell.AnchorDZSigned())
	}
	index, ok := world.ResolveFeature(q.Terrain.Plot, int(q.Terrain.CellW), int(q.Terrain.CellH), ax, az)
	if !ok {
		return false, false
	}
	def, ok := q.Terrain.FeatureDefAt(index)
	if !ok || def == nil || int16(pos.Y.Raw()>>16) >= int16(int32(cell.MinHeight())+int32(uint8(def.Height))) {
		return false, false
	}
	if def.Indestructible || q.Service == nil || q.Service.Features == nil {
		return true, false
	}
	cand, ok := q.Service.Features.AreaCandidateAt(int(cx), int(cz))
	if !ok {
		return true, false
	}
	point := Vec3{X: cand.X, Y: cand.Y, Z: cand.Z}
	return true, DistanceToBox(pos, UnitForArea{Pos: point, Min: point, Max: point}) < BlastRadius(w.AreaOfEffect)
}

// A unit that cannot move has nowhere to step to. Any other unit answers
// through its existing opaque queue link: the queue owns the firing-position
// search and its result (DESIGN_UNITS_ORDERS_COB "Modern firing positions").
func modernNoFiringPosition(u, target *units.Unit, tick uint32) bool {
	if !u.Def.CanMove {
		return true
	}
	q, ok := u.Orders.(interface {
		NoFiringPosition(u, target *units.Unit, tick uint32) bool
	})
	return ok && q.NoFiringPosition(u, target, tick)
}

func (q *ShotQuery) manualAttack() bool {
	if q.Burst != nil {
		return q.Burst.OrderedBurst && !q.Burst.AutomaticAttackBurst
	}
	return slotOrdered(q.Launch.Flags) && !automaticAttack(q.Shooter)
}

// Read order intent through the unit's existing opaque queue link. The queue
// owns descriptor/target history; combat.Rules.AdmitShot owns the policy.
func automaticAttack(u *units.Unit) bool {
	if u == nil {
		return false
	}
	q, ok := u.Orders.(interface{ AutomaticAttack() bool })
	return ok && q.AutomaticAttack()
}

func explicitGroundAttack(u *units.Unit) bool {
	if u == nil {
		return false
	}
	q, ok := u.Orders.(interface{ ExplicitGroundAttack(*units.Unit) bool })
	return ok && q.ExplicitGroundAttack(u)
}

func modernPastAim(muzzle, aim, point Vec3) bool {
	dx, dz := aim.X.Raw()-muzzle.X.Raw(), aim.Z.Raw()-muzzle.Z.Raw()
	if numeric.Abs(dx) >= numeric.Abs(dz) {
		return (dx > 0 && point.X >= aim.X) || (dx < 0 && point.X <= aim.X)
	}
	return (dz > 0 && point.Z >= aim.Z) || (dz < 0 && point.Z <= aim.Z)
}

// Walk crossed cells, including cells a fast projectile skips. Fractions are
// transient integers; the height interval covers each cell's part of the
// actual per-tick trajectory, so a high arc clears a low wreck. This sweep is
// only Modern launch safety; retail projectile contact remains point sampled.
func modernObstructedSegment(q *ShotQuery, from, to Vec3, budget *int) bool {
	const scale int64 = 1 << 30
	dx, dy, dz := to.X.Raw()-from.X.Raw(), to.Y.Raw()-from.Y.Raw(), to.Z.Raw()-from.Z.Raw()
	const max = int64(1<<31 - 1)
	if numeric.Abs(dx) > max || numeric.Abs(dy) > max || numeric.Abs(dz) > max {
		return false
	}
	end := scale
	if q.Burst == nil && modernPastAim(q.Muzzle, q.Aim, to) {
		axis, delta, goal := from.Z.Raw(), dz, q.Aim.Z.Raw()
		if numeric.Abs(q.Aim.X.Raw()-q.Muzzle.X.Raw()) >= numeric.Abs(q.Aim.Z.Raw()-q.Muzzle.Z.Raw()) {
			axis, delta, goal = from.X.Raw(), dx, q.Aim.X.Raw()
		}
		if delta != 0 {
			end = (goal - axis) * scale / delta
			if end < 0 || end > scale {
				return false
			}
		}
	}
	// Only an actual endpoint contact may shield the later air word. A target
	// merely crossed by the safety sweep may be skipped by retail collision.
	endX, endZ := world.WorldToCell(to.X), world.WorldToCell(to.Z)
	endpoint := Projectile{Pos: to, ShooterSide: q.Shooter.Owner}
	terminalTarget := q.Target != nil && contactUnitInCell(&endpoint, q.World, q.Terrain, endX, endZ) == q.Target.Handle
	cx, cz := world.WorldToCell(from.X), world.WorldToCell(from.Z)
	for enter := int64(0); enter <= end && *budget > 0; {
		*budget -= 1
		nextX, nextZ := scale+1, scale+1
		stepX, stepZ := int32(1), int32(1)
		if dx != 0 {
			boundary := world.CellToWorld(cx + 1).Raw()
			if dx < 0 {
				boundary, stepX = world.CellToWorld(cx).Raw(), -1
			}
			nextX = (boundary - from.X.Raw()) * scale / dx
		}
		if dz != 0 {
			boundary := world.CellToWorld(cz + 1).Raw()
			if dz < 0 {
				boundary, stepZ = world.CellToWorld(cz).Raw(), -1
			}
			nextZ = (boundary - from.Z.Raw()) * scale / dz
		}
		leave := min(end, nextX, nextZ)
		low, high := from.Y.Raw()+dy*enter/scale, from.Y.Raw()+dy*leave/scale
		if low > high {
			low, high = high, low
		}
		if modernObstructedCell(q, cx, cz, int32(low), int32(high), terminalTarget && cx == endX && cz == endZ) {
			return true
		}
		if leave >= end {
			return false
		}
		if nextX == leave {
			cx += stepX
		}
		if nextZ == leave {
			cz += stepZ
		}
		enter = leave
	}
	return false
}

func modernObstructedCell(q *ShotQuery, cx, cz, low, high int32, terminalTarget bool) bool {
	cell := q.Terrain.PlotAt(cx, cz)
	if cell == nil {
		return false
	}
	targetContact := false
	if q.World != nil {
		for slot, word := range [...]int16{cell.OccupantA(), cell.OccupantB()} {
			if word <= 0 || pool.Handle(word) == q.Shooter.Handle {
				continue
			}
			u := q.World.Unit(pool.Handle(word))
			if u == nil || u.Def == nil {
				continue
			}
			lower, upper := contactBand(u)
			contact := low < upper
			if slot == 1 {
				contact = low <= upper && high >= lower
			}
			if !contact {
				continue
			}
			// A manual unit attack exempts only the intended victim. Friends
			// in front of any other target still require a clear firing place.
			if (u != q.Target || !q.manualAttack()) && (u.Owner == q.Shooter.Owner ||
				(q.Service != nil && q.Service.Reaction != nil && q.Service.Reaction.AlliedHook() != nil && q.Service.Reaction.AlliedHook()(q.Shooter.Owner, u.Owner))) {
				return true
			}
			if u == q.Target {
				if terminalTarget && high < upper {
					return false
				}
				targetContact = true
			}
		}
	}
	// A target occupying the cell is contacted before its underlying feature
	// [06 §8.1]. Units-only projectiles do not contact features either.
	if targetContact || q.Launch.Weapon.UnitsOnly || cell.IsEmpty() {
		return false
	}
	ax, az := int(cx), int(cz)
	if cell.IsFringe() {
		ax += int(cell.AnchorDXSigned())
		az += int(cell.AnchorDZSigned())
	}
	if index, ok := world.ResolveFeature(q.Terrain.Plot, int(q.Terrain.CellW), int(q.Terrain.CellH), ax, az); ok {
		if def, ok := q.Terrain.FeatureDefAt(index); ok && def != nil {
			// Only features the impact can damage are crossed [06 §13.1].
			if q.passFeatures && !def.Indestructible {
				return false
			}
			// Feature height is an authored byte and equality clears [06 §8.1].
			return int16(low>>16) < int16(int32(cell.MinHeight())+int32(uint8(def.Height)))
		}
	}
	return false
}
