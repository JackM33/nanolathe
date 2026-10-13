package combat

// Modern aircraft target choice — Nanolathe Modern policy
// (DESIGN_MOVEMENT_PATH "Modern aircraft tactics", DESIGN_WEAPONS_PROJECTILES
// "Aircraft anti-air preference"). Only ModernRules reads or writes the board
// below; Strict 3.1 and Community 3.9 never reach this file. Every number here
// is Modern tuning, not a retail constant.

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

const (
	// modernAirDefenceBonus is the visible anti-air preference an aircraft's
	// automatic target choice adds; it outranks every ordinary threat score
	// but not the infection preference.
	modernAirDefenceBonus = 8000
	// modernAirHitBonus is added for a candidate that has recently damaged
	// this side's aircraft, plus up to modernAirHitMore more by how much:
	// whatever is hurting aircraft now outranks anti-air that is not.
	modernAirHitBonus = 6000
	modernAirHitMore  = 4000
	// modernAirReach bounds how far beyond its own sight an aircraft looking
	// for work will go for anti-air that threatens it.
	modernAirReach = 1536
	// airHitHalfLife halves a unit's recent damage to aircraft; airHitMemory
	// forgets it. airClaimHold is how long an attack claim stays valid
	// without its pilot renewing it.
	airHitHalfLife = 150
	airHitMemory   = 900
	airClaimHold   = 60
	// airClaimHorizon is the time a gun or missile claim is expected to keep
	// firing before its target could be someone else's.
	airClaimHorizon = 150
)

// AirBoard is the Modern aircraft tactics' shared transient picture: which
// target each aircraft's attack is flying at, written by movement's pilots,
// and how much damage each unit has recently dealt to aircraft, written by
// the Modern damage observer. Modern target choice reads both. The session
// gives combat and movement the same board. It is never saved: a restored
// battle starts with an empty board that the pilots and hits refill.
type AirBoard struct {
	claims []airClaim // indexed by the shooter's handle index
	hits   []airHit   // indexed by the attacker's handle index
}

type airClaim struct {
	shooter, target *units.Unit
	tick            uint32
}

type airHit struct {
	unit   *units.Unit
	tick   uint32
	damage int32
	owners uint16 // players whose aircraft it hit
}

// Claim records that shooter's attack is flying at target this tick.
func (b *AirBoard) Claim(shooter, target *units.Unit, tick uint32) {
	if b == nil || shooter == nil || shooter.Handle == 0 {
		return
	}
	i := int(shooter.Handle)
	for len(b.claims) <= i {
		b.claims = append(b.claims, airClaim{})
	}
	b.claims[i] = airClaim{shooter: shooter, target: target, tick: tick}
}

func decayAirHit(damage int32, age uint32) int32 {
	if age >= airHitMemory {
		return 0
	}
	return damage >> (age / airHitHalfLife)
}

// noteHit adds damage attacker dealt to an aircraft of victimOwner.
func (b *AirBoard) noteHit(attacker *units.Unit, victimOwner uint8, amount int32, tick uint32) {
	if b == nil || attacker == nil || attacker.Handle == 0 || amount <= 0 {
		return
	}
	i := int(attacker.Handle)
	for len(b.hits) <= i {
		b.hits = append(b.hits, airHit{})
	}
	h := &b.hits[i]
	if h.unit != attacker {
		*h = airHit{unit: attacker}
	}
	h.damage = decayAirHit(h.damage, tick-h.tick)
	if h.damage == 0 {
		h.owners = 0
	}
	h.damage = min(h.damage+amount, 1<<24)
	h.tick = tick
	if victimOwner < 16 {
		h.owners |= 1 << victimOwner
	}
}

// RecentAirDamage is the decayed damage u has recently dealt to aircraft of
// the players in owners (a bit per player).
func (b *AirBoard) RecentAirDamage(u *units.Unit, owners uint16, tick uint32) int32 {
	if b == nil || u == nil || u.Handle == 0 {
		return 0
	}
	i := int(u.Handle)
	if i >= len(b.hits) {
		return 0
	}
	h := &b.hits[i]
	if h.unit != u || h.owners&owners == 0 {
		return 0
	}
	if int32(tick-h.tick) < 0 {
		return h.damage
	}
	return decayAirHit(h.damage, tick-h.tick)
}

// noteModernAirHit is the damage observer's half of the board.
func (s *Service) noteModernAirHit(victim, attacker *units.Unit, amount int32, tick uint32) {
	if s == nil || s.AirBoard == nil || victim == nil || attacker == nil || victim.Def == nil || !victim.Def.CanFly ||
		victim.Owner == attacker.Owner || s.Reaction == nil || s.Reaction.AlliedHook() == nil ||
		s.Reaction.AlliedHook()(victim.Owner, attacker.Owner) {
		return
	}
	s.AirBoard.noteHit(attacker, victim.Owner, min(amount, max(victim.Health, 0)), tick)
}

// airFriends is the bit set of players whose aircraft count as this
// shooter's: its own and those it is allied with.
func (s *Service) airFriends(q *TargetQuery) uint16 {
	mask := uint16(1) << (q.Shooter.Owner & 15)
	for p := uint8(0); p < combatPlayerSlots && p < 16; p++ {
		if p != q.Shooter.Owner && s.allied(q.Shooter.Owner, p, q.Economy) {
			mask |= 1 << p
		}
	}
	return mask
}

// airClaimDamage is what one claiming aircraft is expected to do to target
// before it could turn to something else: one string's middle half for a
// bomber, airClaimHorizon ticks of fire otherwise.
func (s *Service) airClaimDamage(shooter, target *units.Unit) int64 {
	slot := orderSlot(shooter, 0)
	if slot == nil || slot.Weapon == nil {
		return 0
	}
	w := slot.Weapon
	per := s.effectiveDamage(w, target, shooter)
	if per <= 0 {
		return 0
	}
	reload := max(int64(w.ReloadTime), 1)
	shots := int64(1)
	if w.Dropped {
		// One retail string: the authored run length at full speed, then
		// about half its bombs within the blast of the target.
		if shooter.Def != nil && shooter.Def.MaxVelocity > 0 {
			ticks := int64(shooter.Def.AttackRunLength)<<16/int64(shooter.Def.MaxVelocity) + 2
			shots = (ticks/reload + 2) / 2
		}
	} else {
		shots = max(airClaimHorizon/reload, 1) * max(int64(w.Burst), 1)
	}
	return per * shots
}

// airClaimView is one other aircraft's claim on a candidate, gathered once
// per target choice.
type airClaimView struct {
	target *units.Unit
	damage int64
}

// gatherAirClaims collects the live claims of the shooter's friends other
// than the shooter itself, with the damage each is expected to deal.
func (s *Service) gatherAirClaims(q *TargetQuery, friends uint16, out []airClaimView) []airClaimView {
	out = out[:0]
	if s.AirBoard == nil {
		return out
	}
	tick := s.modernTick
	for i := range s.AirBoard.claims {
		c := &s.AirBoard.claims[i]
		// Signed age: pilots post during the order and movement phases, a tick
		// ahead of the combat tick this scan reads.
		if c.shooter == nil || c.shooter == q.Shooter || c.target == nil || int32(tick-c.tick) > airClaimHold ||
			!c.shooter.Alive || c.shooter.Dying || !c.target.Alive || c.target.Dying ||
			c.shooter.Owner >= 16 || friends&(1<<c.shooter.Owner) == 0 || q.World.Unit(c.shooter.Handle) != c.shooter {
			continue
		}
		out = append(out, airClaimView{target: c.target, damage: s.airClaimDamage(c.shooter, c.target)})
	}
	return out
}

// airCovered reports whether the claims already expected on target would
// destroy it.
func airCovered(claims []airClaimView, target *units.Unit) bool {
	var sum int64
	for i := range claims {
		if claims[i].target == target {
			sum += claims[i].damage
		}
	}
	return sum > 0 && sum >= int64(max(target.Health, 1))
}

// airDefence grades what candidate can do against an airborne aircraft.
type airDefence uint8

const (
	airDefenceNone airDefence = iota
	// airDefenceIncidental: a weapon that can engage the aircraft but whose
	// authored bad-target category disfavours aircraft, so it shoots them
	// only when it has no ground target (a Storm's rockets, a laser tower).
	airDefenceIncidental
	// airDefenceDedicated: a weapon that prefers aircraft (a Pulverizer).
	airDefenceDedicated
)

// modernAntiAirFor grades candidate's enabled weapons against the airborne
// aircraft: direct or authored anti-air, not dropped, command-fired or an
// interceptor, with damage against it and passing the medium gate
// [06 §3.1] without its range clause. Range and readiness do not matter: the
// capability does. It also reports the longest such weapon's range.
func modernAntiAirFor(s *Service, candidate, aircraft *units.Unit, terrain *world.Terrain, catalog *content.Catalog) (airDefence, int32) {
	grade, reach := airDefenceNone, int32(0)
	sea := int32(0)
	if terrain != nil {
		sea = int32(terrain.SeaLevel)
	}
	for idx := 0; idx < units.NumSlots; idx++ {
		slot := orderSlot(candidate, idx)
		if slot == nil || !slot.IsEnabled() || slot.Weapon == nil {
			continue
		}
		w := slot.Weapon
		if w.Dropped || w.CommandFire || w.Interceptor || (w.Ballistic && !w.ToAirWeapon) ||
			modernEffectiveDamage(s, w, aircraft, candidate, false, false) <= 0 || (w.Paralyzer && aircraft.Def.ImmuneToParalyzer) ||
			!unitToUnitAdmitsBeforeRange(gateEndForUnit(candidate), gateEndForUnit(aircraft), sea, w.WaterWeapon, w.ToAirWeapon) {
			continue
		}
		g := airDefenceDedicated
		if catalog != nil && !IsPreferredCategoryMask(aircraft.Def.DefinitionMask(), badMaskForSlot(candidate.Def, idx)) {
			g = airDefenceIncidental
		}
		grade, reach = max(grade, g), max(reach, w.Range)
	}
	return grade, reach
}

// airThreatReach lists, for an aircraft's sight-distance scan, the visible
// hostile anti-air outside the scan's own radius that threatens the aircraft
// now — it is within its weapon's range of the aircraft, or it has recently
// damaged this player's aircraft — up to modernAirReach away. An aircraft
// looking for work otherwise sees only what is under it, while the anti-air
// shooting at it stands off at a longer range than its sight.
func (s *Service) airThreatReach(q *TargetQuery, friends uint16, out []Candidate) []Candidate {
	out = out[:0]
	u := q.Shooter
	var sea = q.Acquisition.SeaLevel
	for _, h := range s.targets.primaryList(u.Owner) {
		t := q.World.Unit(h)
		if t == nil || t == u || !t.Alive || t.Dying || t.Def == nil || t.Remaining != 0 ||
			WithinRange(u.X, u.Z, t.X, t.Z, q.Acquisition.FilterRange) || !WithinRange(u.X, u.Z, t.X, t.Z, modernAirReach) ||
			s.allied(u.Owner, t.Owner, q.Economy) {
			continue
		}
		status := perspectiveStatus(q.Visibility, u.Owner, t)
		if q.Visibility == nil || !q.Visibility.IsVisible(visibility.PlayerID(u.Owner), visibilityTarget(t, status)) {
			continue
		}
		aa, reach := modernAntiAirFor(s, t, u, q.Terrain, q.Catalog)
		if aa == airDefenceNone || (!WithinRange(u.X, u.Z, t.X, t.Z, reach+48) && s.AirBoard.RecentAirDamage(t, friends, s.modernTick) == 0) {
			continue
		}
		c := acquisitionCandidate(u, t, sea, status, q.Catalog)
		// The physical gate without its range clause: the aircraft flies to it.
		acq := q.Acquisition
		acq.Range = modernAirReach
		if !acq.admits(c) || acq.rejectsNoChase(c) || acq.rejectsStunned(c) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// airTarget is SelectTarget's aircraft context for one query.
type airTarget struct {
	active  bool // a flying shooter under Modern
	sight   bool // its sight-distance (order-work) scan
	friends uint16
	claims  []airClaimView
}

func (s *Service) airTargetContext(q *TargetQuery) airTarget {
	var a airTarget
	if q.Shooter.Def == nil || !q.Shooter.Def.CanFly {
		return a
	}
	a.active = true
	a.friends = s.airFriends(q)
	a.sight = q.Acquisition.SightScan
	if a.sight {
		s.airClaimScratch = s.gatherAirClaims(q, a.friends, s.airClaimScratch)
		a.claims = s.airClaimScratch
	}
	return a
}

// airScore adds an aircraft shooter's preferences to a candidate's score:
// whatever is hurting this side's aircraft first, by how much; then
// anti-air that prefers aircraft; then ground weapons that can still shoot
// at aircraft.
func (s *Service) airScore(a airTarget, q *TargetQuery, target *units.Unit, visible bool) int64 {
	var score int64
	if visible {
		switch aa, _ := modernAntiAirFor(s, target, q.Shooter, q.Terrain, q.Catalog); aa {
		case airDefenceDedicated:
			score += modernAirDefenceBonus
		case airDefenceIncidental:
			score += modernAirDefenceBonus / 4
		}
	}
	if hit := s.AirBoard.RecentAirDamage(target, a.friends, s.modernTick); hit > 0 {
		score += modernAirHitBonus + min(int64(hit)*4, modernAirHitMore)
	}
	return score
}
