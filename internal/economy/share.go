// Online sharing state and the per-seat automatic dispatcher
// [05 R-SHARE-01 §3] (docs/DESIGN_MULTIPLAYER.md §6.3, §6.7, §7.1).
//
// Retail runs the dispatcher on each machine for that machine's own human
// only, and keeps each player's share options and thresholds in its player
// record [05 R-SHARE-01 §3] [07 R-CAM-01 §6]. A lockstep battle runs every
// human seat's dispatch in one common world, so the options become per-seat
// battle state and the dispatcher runs once per human seat. ShareTick in
// tick.go stays the single-player retail path, which is inert without a
// networked session.

package economy

import (
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// ShareOption names one sharing bit of a player's option word by its retail
// bit number [05 R-SHARE-01 §3]. Bit 3, ShareLOS, is dead in retail — no
// command reaches its handler and nothing reads it [05 "Sensor sharing"] — so
// it has no name here.
type ShareOption uint8

const (
	// ShareOptionMetal is bit 1, read by the 60-tick metal pass.
	ShareOptionMetal ShareOption = 1
	// ShareOptionEnergy is bit 2, read by the 60-tick energy pass.
	ShareOptionEnergy ShareOption = 2
	// ShareOptionMapping is bit 5, read by the 450-tick explored-map pass.
	ShareOptionMapping ShareOption = 5
	// ShareOptionRadar is bit 6, read by the sensor pass rather than the
	// dispatcher [03 R-VIS-01 §7].
	ShareOptionRadar ShareOption = 6
)

// shareAllOrder is ShareAll's sequence: the four toggles in turn
// [07 R-CAM-01 §6].
var shareAllOrder = [4]ShareOption{ShareOptionMetal, ShareOptionEnergy, ShareOptionMapping, ShareOptionRadar}

// shareOption returns the field that holds option for player, or nil for an
// invalid slot or an unnamed bit.
func (s *Service) shareOption(player int, option ShareOption) *bool {
	if s == nil || player < 0 || player >= len(s.Players) {
		return nil
	}
	p := &s.Players[player]
	switch option {
	case ShareOptionMetal:
		return &p.AutoShareMetal
	case ShareOptionEnergy:
		return &p.AutoShareEnergy
	case ShareOptionMapping:
		return &p.AutoShareSensor
	case ShareOptionRadar:
		return &p.ShareRadar
	}
	return nil
}

// ShareOptionOn reports one of player's share options.
func (s *Service) ShareOptionOn(player int, option ShareOption) bool {
	field := s.shareOption(player, option)
	return field != nil && *field
}

// SetShareOption writes one of player's share options. It reports false, and
// writes nothing, for an invalid slot or option. Retail's commands flip a bit
// rather than set it (ToggleShareOption); an explicit setter writes the same
// field [07 R-CAM-01 §6] [05 R-SHARE-01 §3].
func (s *Service) SetShareOption(player int, option ShareOption, on bool) bool {
	field := s.shareOption(player, option)
	if field == nil {
		return false
	}
	*field = on
	return true
}

// ToggleShareOption is one of the `ShareMetal`, `ShareEnergy`,
// `ShareMapping` and `ShareRadar` commands: it flips — it does not set — the
// bit in the issuer's own record and reports the new value
// [07 R-CAM-01 §6] [05 R-SHARE-01 §3]. Its network-mode gate, status line and
// player-record resend belong to the caller.
func (s *Service) ToggleShareOption(player int, option ShareOption) (on, ok bool) {
	field := s.shareOption(player, option)
	if field == nil {
		return false, false
	}
	*field = !*field
	return *field, true
}

// ToggleShareAll is the `ShareAll` command: the four toggles in sequence
// [07 R-CAM-01 §6]. Each bit flips independently, so a player sharing metal
// alone stops sharing metal and starts sharing energy, mapping and radar.
func (s *Service) ToggleShareAll(player int) bool {
	if s == nil || player < 0 || player >= len(s.Players) {
		return false
	}
	for _, option := range shareAllOrder {
		s.ToggleShareOption(player, option)
	}
	return true
}

// SetShareThreshold is the `SetShareMetal n` / `SetShareEnergy n` command: the
// threshold becomes n when n is not above the player's storage capacity for
// that resource, and the capacity otherwise [07 R-CAM-01 §6]
// [05 R-SHARE-01 §3]. It returns the stored value. Its network-mode gate and
// status line belong to the caller.
//
// The argument is the command word's signed 32-bit integer value, converted
// to single precision — rounded to nearest, ties to even, the default control
// word [01 R-DET-01 §2] — and compared with the single-precision capacity
// [05 R-SHARE-01 §3] [07 R-CAM-01 §6]. The compare keeps the argument for
// below, equal and unordered: only an ordered "above" stores the capacity,
// so a negative n is stored negative and a NaN capacity stores n.
func (s *Service) SetShareThreshold(player int, res Res, n int32) (float32, bool) {
	if s == nil || player < 0 || player >= len(s.Players) || res < Metal || res > Energy {
		return 0, false
	}
	p := &s.Players[player]
	capacity := p.Capacity[res]
	v := float32(n)
	if v > capacity {
		v = capacity
	}
	if res == Metal {
		p.MetalShareThreshold = v
	} else {
		p.EnergyShareThreshold = v
	}
	return v, true
}

// RadarShareOwners is the owner mask the sensor pass for viewer reads
// (visibility SensorTickForPerspectiveSharing): bit p is set when p's own
// declaration toward viewer is set and p shares radar [03 R-VIS-01 §4] pass 1
// [03 R-VIS-01 §7]. The viewer's declaration toward p is not read. The
// viewer's own bit is never set; its units are already its own.
func (s *Service) RadarShareOwners(viewer int) uint16 {
	if s == nil || viewer < 0 || viewer >= len(s.Players) {
		return 0
	}
	var mask uint16
	for owner := range s.Players {
		p := &s.Players[owner]
		if owner != viewer && p.ShareRadar && p.Allies[viewer] {
			mask |= 1 << uint(owner)
		}
	}
	return mask
}

// ShareTickSeats runs the automatic dispatcher of [05 R-SHARE-01 §3] once
// for every human seat, ascending (DESIGN_MULTIPLAYER §6.3 "Automatic
// sharing"). humans has bit p set for each human seat p still in the battle;
// a finally removed seat is not one — retail's removal clears every row
// toward the removed slot, and its sender refuses a slot carrying a removal
// reason [05 R-SHARE-01 §1] [05 R-SHARE-01 §4]. Each seat's whole dispatch —
// metal, then energy, then the explored-map pass — completes before the next
// seat's, so a later seat sees the stock an earlier one moved.
//
// It is the online form only: it does not read Networked, and ShareTick
// stays the single-player path. The caller invokes it where retail's
// executor did, once per advancing sub-tick after the twelve phases, with the
// already-advanced global tick, so the first resource pass is at tick 60 and
// the first mapping pass at tick 450 [05 R-SHARE-01 §3]. No random number is
// drawn.
//
// A candidate is retail's remote human — control byte 3 with the human kind
// byte — which in the common world is another seat in humans: the giver's
// own hosted computers and every other machine's computers are never
// candidates [05 R-SHARE-01 §1] [05 R-SHARE-01 §3]. The remaining gates are
// retail's: the record exists with an active control byte, it is not
// eliminated, and the giver's own declaration toward it is set — the
// candidate's declaration is not read.
//
// copyExplored receives each qualifying mapping request as (source,
// recipient) at once, in seat order and then ascending recipient order
// (Q25, DESIGN_MULTIPLAYER §6.7); the session binds it to the visibility
// merge. A nil callback drops the requests.
func (s *Service) ShareTickSeats(tick uint32, w *units.World, humans uint16, copyExplored func(source, recipient int)) {
	if s == nil {
		return
	}
	resources := tick%60 == 0
	mapping := tick%450 == 0
	if !resources && !mapping {
		return
	}
	for seat := 0; seat < len(s.Players); seat++ {
		if humans&(1<<uint(seat)) == 0 {
			continue
		}
		if resources {
			s.shareSeatResource(seat, Metal, w, humans)
			s.shareSeatResource(seat, Energy, w, humans)
		}
		if mapping {
			s.shareSeatMapping(seat, w, humans, copyExplored)
		}
	}
}

// shareCandidate is the candidate scan's record gates for slot c, given the
// giver src [05 R-SHARE-01 §3]: the record exists with an active control
// byte, it is seated (always, for a slot 0..9 here), it is not eliminated, it
// is another human seat (retail's remote human), and the giver has declared
// alliance toward it.
func (s *Service) shareCandidate(src, c int, w *units.World, humans uint16) bool {
	if c == src || humans&(1<<uint(c)) == 0 {
		return false
	}
	d := &s.Players[c]
	return d.Exists && isActiveState(d.ControllerState) && !PlayerEliminated(w, c) && s.Players[src].Allies[c]
}

// shareSeatResource is the 60-tick pass for one resource of one seat
// [05 R-SHARE-01 §3].
func (s *Service) shareSeatResource(seat int, res Res, w *units.World, humans uint16) {
	src := &s.Players[seat]
	var enabled bool
	var threshold, ratio float32
	switch res {
	case Metal:
		enabled, threshold, ratio = src.AutoShareMetal, src.MetalShareThreshold, 0.33333334
	case Energy:
		enabled, threshold, ratio = src.AutoShareEnergy, src.EnergyShareThreshold, 0.5
	default:
		return
	}
	// Source gate: the option bit, and stock strictly above the threshold;
	// an unordered comparison fails it [05 R-SHARE-01 §3] step 1.
	if !enabled || !(threshold < src.Stock[res]) {
		return
	}
	// Ascending scan; every qualifying candidate replaces the last, so the
	// last one wins. The stock test is strict, and its accepted outcomes are
	// "less" and "unordered", so it rejects only a stock at or above the
	// giver's [05 R-SHARE-01 §3] step 2.
	chosen := -1
	for c := 0; c < len(s.Players); c++ {
		if !s.shareCandidate(seat, c, w, humans) {
			continue
		}
		if s.Players[c].Stock[res] >= src.Stock[res] {
			continue
		}
		chosen = c
	}
	if chosen < 0 {
		return
	}
	dst := &s.Players[chosen]
	// amount = min(gap, excess × ratio), whose ratio is a single-precision
	// constant. The excess, the capacity gap and the product stay at the
	// 53-bit working precision of [01 §8] and are compared there; only the
	// chosen amount is rounded to single, and an unordered comparison
	// chooses the product [05 R-SHARE-01 §3] step 3. For a
	// threshold whose difference from the stock is single-representable —
	// zero, the default, among them — this equals an all-single evaluation;
	// they part only for an unrepresentable excess, such as one a negative
	// threshold makes. No gate stands between this and the transfer helper:
	// a stock above a fallen capacity makes a negative gap, and the helper's
	// signed arithmetic then runs [05 R-SHARE-01 §2].
	product := (float64(src.Stock[res]) - float64(threshold)) * float64(ratio)
	gap := float64(dst.Capacity[res]) - float64(dst.Stock[res])
	amount := float32(gap)
	if !(product >= gap) {
		amount = float32(product)
	}
	// The helper with its debit flag: clamp to live stock, drop a zero or
	// unordered amount, debit, credit the recipient's production bucket
	// [05 R-SHARE-01 §2]. Retail's recipient applies the same credit from
	// the packet without the debit [05 R-SHARE-01 §4]; one world applies it
	// once.
	s.transfer(src, dst, res, amount)
}

// shareSeatMapping is the 450-tick explored-map pass for one seat
// [05 R-SHARE-01 §3]: with option bit 5 set, every qualifying slot, in
// ascending order, is asked to copy the seat's explored tiles. There is no
// stock test and no last-candidate rule, and the request carries no map
// data: the recipient's merge copies the source's history as it stands when
// the request applies [05 R-SHARE-01 §6] (Q25).
func (s *Service) shareSeatMapping(seat int, w *units.World, humans uint16, copyExplored func(source, recipient int)) {
	if !s.Players[seat].AutoShareSensor {
		return
	}
	for c := 0; c < len(s.Players); c++ {
		if !s.shareCandidate(seat, c, w, humans) {
			continue
		}
		if copyExplored != nil {
			copyExplored(seat, c)
		}
	}
}
