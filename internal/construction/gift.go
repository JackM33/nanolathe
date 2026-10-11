// Unit gifts: the share screen's unit sharing [05 R-SHARE-01 §5]
// (docs/DESIGN_MULTIPLAYER.md §6.3 "Unit transfer").

package construction

import (
	"slices"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// giftExcludedCategory is the category whose members are never shared
// [05 R-SHARE-01 §5]. A catalog with no such category excludes nothing: an
// unknown category name is an empty bitset [02 R-P0-03 §4].
const giftExcludedCategory = "Commander"

// transferKillNominal is the fixed damage the remote branch applies to the
// giver's departing copy [05 R-WORK-01 §15]. At 30,000 it is not below the
// armor gate's threshold, so armor never scales it, while defender veterancy
// still does [06 §9.2].
const transferKillNominal int32 = 30000

// GiftCopyRule selects which branch of the ownership transfer gives a unit
// away [05 R-WORK-01 §15]. The session chooses it by whether the old and the
// new owner share a machine (DESIGN_MULTIPLAYER §6.3): a human's own hosted
// computer is local, another human or another human's computer is remote.
type GiftCopyRule uint8

const (
	// GiftCopyLocal is the branch for a new owner simulated on the giver's
	// machine: the replacement is created at once with the local copy list
	// (TransferOwnership).
	GiftCopyLocal GiftCopyRule = iota
	// GiftCopyRemote is the branch for a new owner on another machine: the
	// giving machine's remote branch, then the receiving machine's transfer
	// arm, with the remote copy list (GiftUnits).
	GiftCopyRemote
)

// GiftOutcome is what GiftUnits did with one unit.
type GiftOutcome uint8

const (
	// GiftTransferred: the unit was replaced by a record of the new owner.
	GiftTransferred GiftOutcome = iota + 1
	// GiftExcluded: the share screen leaves the unit out (GiftEligible).
	GiftExcluded
	// GiftRefused: the transfer refused silently and the unit stays with its
	// owner. Either rule refuses at the entry gate (same owner, not alive, or
	// death-latched); the local rule also at the allocator gate (the new
	// owner's slice is full or the definition's limit is reached), and the
	// remote rule when the new owner's record is not live
	// [05 R-SHARE-01 §5] [05 R-SHARE-01 §8] [05 R-WORK-01 §15].
	GiftRefused
	// GiftNotOwned: the handle names no unit of the giver. Retail gathers
	// the giver's own selection, so such a unit is never handed over.
	GiftNotOwned
	// GiftLost: the remote rule's giving side ran — the departing unit took
	// the transfer's kill — but the receiving arm's allocator refused, so no
	// replacement exists [05 R-WORK-01 §15] [05 R-SHARE-01 §5].
	GiftLost
)

// GiftResult reports one unit of a gift, in the order GiftUnits visited it.
// Under the remote rule a transferred unit's departing record is still the
// giver's, latched for death or — when defender veterancy leaves the kill
// short of its health — alive with that much less health.
type GiftResult struct {
	Unit        pool.Handle
	Replacement pool.Handle // the new owner's record when Outcome is GiftTransferred
	Outcome     GiftOutcome
}

// giftPacket is what the remote branch's transfer packet carries, captured
// on the giving side before the kill [05 R-WORK-01 §15] [08 "Declared packet
// types"]. Definition, position, movement mode and the operational byte are
// not in it: the receiving arm reads them from its mirror of the unit.
type giftPacket struct {
	departing *units.Unit
	// fraction is the remaining-build fraction truncated to a whole number:
	// 0 for a finished or partly built unit, 1 for an untouched nanoframe.
	fraction int32
	// health is the 16-bit health word; the receiver keeps its low 16 bits.
	health             int16
	bank, heading, pit uint16
	// stockpile holds the three slots' stockpiled-round bytes, written only
	// when the departing unit's slot 0 is enabled.
	stockpile [units.NumSlots]uint8
}

// GiftEligible reports whether the share screen hands u to the ownership
// transfer [05 R-SHARE-01 §5]. It excludes a unit whose movement-mode mirror
// — the low two bits of its status word [04 §2] — is 2 (airborne), a unit
// that is carried or carrying — a carrier back-pointer or a cargo-list head
// [04 R-UNIT-06 §3] — and a unit whose definition is in the Commander
// category. It reads nothing else: liveness and the death latch are the
// transfer's own entry gate, and a nanoframe is given like any other unit.
func (s *Service) GiftEligible(u *units.Unit) bool {
	if s == nil {
		return false
	}
	return giftEligible(u, s.giftExcludedMask())
}

// giftExcludedMask resolves the excluded category once per gift.
func (s *Service) giftExcludedMask() content.CategoryMask {
	mask, _ := s.Catalog.Category(giftExcludedCategory)
	return mask
}

func giftEligible(u *units.Unit, commanders content.CategoryMask) bool {
	if u == nil || u.Def == nil {
		return false
	}
	if u.Move.ModeMirror&3 == 2 {
		return false
	}
	if u.Attachment.Carrier != 0 || len(u.Attachment.Cargo) != 0 {
		return false
	}
	return !commanders.Contains(u.Def.UnitDefID)
}

// GiftUnits is the share screen's unit sharing [05 R-SHARE-01 §5]: each of
// giver's units in handles, in ascending handle order, is checked against
// GiftEligible and handed to the ownership transfer with newOwner. Retail
// gathers the giver's selection by walking the giver's own slice from its
// first record upward, so ascending unit-identifier order is its order
// [05 R-SHARE-01 §5]; a duplicate handle is visited once. An earlier
// replacement counts against the new owner's slice and definition limit when
// a later unit is tried.
//
// rule picks the branch [05 R-WORK-01 §15]. The local rule is
// TransferOwnership, unchanged. The remote rule is the lockstep form of the
// cross-machine transfer (DESIGN_MULTIPLAYER §6.3, §6.7 Q25): every eligible
// unit takes the giving machine's remote branch, in order, and then every
// unit that branch sent takes the receiving machine's transfer arm, in the
// same order — retail's giver finishes the whole confirm before the
// receiver's drain consumes a packet, and nothing models the transport's
// delay. tick is the battle tick the gift applies on; the departing unit's
// kill goes through the ordinary damage receiver with it.
//
// Validating the gift itself — that newOwner may receive one, and that the
// giver may give — is the session's: the screen requires a living,
// undefeated target and has no alliance test [05 R-SHARE-01 §5]. The result
// lists every visited handle with its outcome; an unknown rule visits
// nothing.
func (s *Service) GiftUnits(giver, newOwner uint8, handles []pool.Handle, rule GiftCopyRule, tick uint32) []GiftResult {
	if s == nil || s.World == nil || len(handles) == 0 || rule > GiftCopyRemote {
		return nil
	}
	order := slices.Clone(handles)
	slices.Sort(order)
	order = slices.Compact(order)
	commanders := s.giftExcludedMask()
	results := make([]GiftResult, 0, len(order))
	var sent []giftPacket
	var sentAt []int
	for _, h := range order {
		r := GiftResult{Unit: h}
		u := s.World.Unit(h)
		switch {
		case u == nil || u.Owner != giver:
			r.Outcome = GiftNotOwned
		case !giftEligible(u, commanders):
			r.Outcome = GiftExcluded
		case rule == GiftCopyRemote:
			if packet, ok := s.giftDepart(u, newOwner, tick); ok {
				sent = append(sent, packet)
				sentAt = append(sentAt, len(results))
				r.Outcome = GiftLost // until the receiving arm creates the replacement
			} else {
				r.Outcome = GiftRefused
			}
		default:
			if repl, ok := s.TransferOwnership(u, newOwner); ok {
				r.Outcome, r.Replacement = GiftTransferred, repl.Handle
			} else {
				r.Outcome = GiftRefused
			}
		}
		results = append(results, r)
	}
	for i, packet := range sent {
		if repl, ok := s.giftArrive(packet, newOwner); ok {
			results[sentAt[i]].Outcome, results[sentAt[i]].Replacement = GiftTransferred, repl
		}
	}
	return results
}

// giftDepart is the remote branch on the giving machine
// [05 R-WORK-01 §15]: the entry gate, then — when the new owner's record is
// live — the countdown and selected-bit writes, the packet's fields, and the
// fixed kill. It reports whether the packet was sent.
func (s *Service) giftDepart(u *units.Unit, newOwner uint8, tick uint32) (giftPacket, bool) {
	// The entry gate, shared with the local branch: owner differs, alive,
	// death latch clear [05 R-WORK-01 §15].
	if u.Owner == newOwner || !u.Alive || u.Dying {
		return giftPacket{}, false
	}
	// TODO(question): after the gate and before either branch, retail tells
	// the session's registered observers — by inference the per-player
	// managers — that the unit is leaving, on the giving and the receiving
	// machine alike; what they do with it is Unknown. A read of those
	// objects' notification methods, as [05 R-WORK-01 §15] names, would
	// settle it. Nothing is notified here, as TransferOwnership notifies
	// nothing.
	if !s.giftRecipientLive(newOwner) {
		return giftPacket{}, false // returns after the notification, nothing sent or killed
	}
	// Step 1: the departing copy's post-capture countdown becomes 150 and
	// its selected bit clears; neither is written anywhere else by the
	// transfer [05 R-WORK-01 §15] [04 R-MOV-03 §1] step 6.
	// TODO(question): internal/units has no post-capture countdown field
	// (the sweep's step 6 and the selection and own-unit predicates read it
	// as constant zero), so the 150 has nowhere to go. Which owner adds the
	// field — and its sweep decrement, readiness and own-unit reject terms
	// and checkpoint entry — settles it; until then a departing copy that
	// survives its kill stays selectable and rejects its owner's clicks.
	u.Flags &^= units.SelectedStatus
	// Step 2: the packet's fields, read before the kill. The fraction goes
	// through the runtime's integer conversion, truncating toward zero
	// [01 R-DET-01 §1].
	p := giftPacket{
		departing: u,
		fraction:  numeric.TruncateFloat32ToLow32(u.Remaining),
		health:    int16(u.Health),
		bank:      u.Move.Bank, heading: u.Move.Heading, pit: u.Move.Pitch,
	}
	if u.Slots[0].IsEnabled() {
		for i := range p.stockpile {
			p.stockpile[i] = uint8(u.Slots[i].Ammo)
		}
	}
	// Step 3, the broadcast, has no counterpart: one world applies the
	// receiving arm itself (GiftUnits).
	// Step 4: 30,000 damage of kind 4 with a null attacker through the
	// ordinary damage receiver. Defender veterancy scales it, so a copy whose
	// health exceeds the scaled amount survives, still the giver's; the
	// receiver's replacement is made regardless [05 R-WORK-01 §15]
	// [06 §9.2]. Lethal damage latches the death; the next unit phase runs
	// the cause-4 death [06 §12.1].
	s.Combat.AcceptDamage(s.World, tick, combat.DamageInput{
		Victim: u.Handle, Nominal: transferKillNominal, Kind: uint8(combat.CauseCapture),
	})
	return p, true
}

// giftRecipientLive is the remote branch's test that the new owner's record
// is live [05 R-WORK-01 §15]: an existing player record. A service with no
// ledger (a fixture) has no record to test and admits any player slot.
func (s *Service) giftRecipientLive(newOwner uint8) bool {
	if int(newOwner) >= 10 {
		return false
	}
	if s.Economy == nil {
		return true
	}
	return int(newOwner) < len(s.Economy.Players) && s.Economy.Players[newOwner].Exists
}

// giftArrive is the receiving machine's transfer arm [05 R-WORK-01 §15]. Its
// mirror of the departing unit is the departing record itself: the mirror
// never carries the giver's death latch, so the arm's entry gate holds while
// the record is alive, and the arm reads the definition, position, movement
// mode and operational byte from it — the packet carries none of them. It
// returns the replacement, or false when the allocator refused and the unit
// is lost.
func (s *Service) giftArrive(p giftPacket, newOwner uint8) (pool.Handle, bool) {
	mirror := p.departing
	if mirror == nil || !mirror.Alive || mirror.Def == nil {
		return 0, false
	}
	// The ordinary allocator, no preferred record, in the new owner's slice:
	// a finished unit of the mirror's definition at its position with its
	// movement mode. The constructor runs whole, its random draws included;
	// the creatable bit, the definition limit and a free record gate it
	// [05 R-SHARE-01 §8]. The structure facing is resolved as the local
	// branch resolves it.
	facing := s.ResolveStructureFacing(mirror.Def, units.FacingFromHeading(mirror.Move.Heading))
	h, err := s.World.CreateWithMoverModeFacing(mirror.Def, newOwner, mirror.X, mirror.Y, mirror.Z, mirror.Move.Mode, facing)
	if err != nil {
		return 0, false
	}
	repl := s.World.Unit(h)
	if repl == nil {
		return 0, false
	}
	// Standing-order bits 18–21 clear, exactly as in the local branch
	// [05 R-WORK-01 §15] [04 R-STANCE-01 §6].
	repl.Flags &^= (units.StandingFieldMask << units.StandingMoveShift) |
		(units.StandingFieldMask << units.StandingFireShift)
	// From the packet, in order: health (its low 16 bits), the fraction
	// rebuilt from the whole number — so a partly built unit arrives
	// finished and an untouched nanoframe stays unbuilt — the orientation
	// triple, and each slot's stockpiled rounds where the REPLACEMENT's slot
	// is enabled. Everything else keeps the constructor's value.
	repl.Health = int32(p.health)
	repl.Remaining = float32(p.fraction)
	repl.Move.Bank, repl.Move.Heading, repl.Move.Pitch = p.bank, p.heading, p.pit
	for i := range repl.Slots {
		if repl.Slots[i].IsEnabled() {
			repl.Slots[i].Ammo = int32(p.stockpile[i])
		}
	}
	// The operational byte is the mirror's, replayed through the same two
	// state-edge calls as the local branch [05 R-WORK-01 §15]. The mirror is
	// not removed here; it falls to the giver's kill.
	repl.ReplayOperationalState(mirror.OperationalState())
	return h, true
}
