package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// Online sharing (DESIGN_MULTIPLAYER §6.3, §6.7, §7.1): the receiver's
// admission and application of the share toggles, thresholds and SHARE-screen
// gifts (seat kinds 35..42), the per-seat automatic dispatcher, share radar's
// owner mask and the frame's sharing publication. Retail runs every one of
// these only in a networked session [07 R-CAM-01 §6] [05 R-SHARE-01 §3]
// [05 R-SHARE-01 §5]; a single-player session refuses the commands and keeps
// its inert ShareTick. Each command acts for the issuing seat: retail's
// handlers act for the machine's local human, and a watcher can neither type
// a `+` line nor open the SHARE screen, which onlineSeatRole already refuses.
// The alliance kinds 43 and 44 are online_alliance.go's.

// sharingSeatKind reports the sharing and alliance kinds of command schema
// version 2.
func sharingSeatKind(k SeatCommandKind) bool {
	return k >= SeatShareMetal && k <= SeatSharedVictory
}

// onlineOnlyHumanKind reports the local kinds that exist only online.
func onlineOnlyHumanKind(k HumanCommandKind) bool {
	return k >= HumanShareMetal && k <= HumanSharedVictory
}

// admitOnlineSharing is the state-dependent half of a sharing or alliance
// command's admission, after the issuer's role, the schema, the permission
// and the actors have passed (admitSeatCommand). Other kinds pass.
func (s *Session) admitOnlineSharing(seat uint8, c *SeatCommand) string {
	switch c.Kind {
	case SeatShareMetal, SeatShareEnergy, SeatShareMapping, SeatShareRadar, SeatShareAll, SeatSetShareMetal, SeatSetShareEnergy:
		if s.Econ == nil {
			return "a playing seat"
		}
	case SeatShareGift:
		return s.admitShareGift(seat, &c.ShareGift)
	case SeatDeclareAlliance:
		return s.admitDeclareAlliance(seat, &c.DeclareAlliance)
	case SeatSharedVictory:
		return s.admitSharedVictory(seat)
	}
	return ""
}

// applyOnlineSharing applies an admitted sharing or alliance command for the
// issuing seat at phase 1, in stream order (§6.7 Q25).
func (s *Session) applyOnlineSharing(seat uint8, c *SeatCommand, tick uint32) CommandOutcome {
	player := int(seat)
	switch c.Kind {
	// The four toggles: the option takes the value the client's flip
	// produced [07 R-CAM-01 §6] [05 R-SHARE-01 §3]. The status line and the
	// player-record resend are presentation and transport.
	case SeatShareMetal:
		s.Econ.SetShareOption(player, economy.ShareOptionMetal, c.ShareMetal.On)
	case SeatShareEnergy:
		s.Econ.SetShareOption(player, economy.ShareOptionEnergy, c.ShareEnergy.On)
	case SeatShareMapping:
		s.Econ.SetShareOption(player, economy.ShareOptionMapping, c.ShareMapping.On)
	case SeatShareRadar:
		s.Econ.SetShareOption(player, economy.ShareOptionRadar, c.ShareRadar.On)
	case SeatShareAll:
		// The four toggles in their sequence [07 R-CAM-01 §6].
		p := &c.ShareAll
		s.Econ.SetShareOption(player, economy.ShareOptionMetal, p.Metal)
		s.Econ.SetShareOption(player, economy.ShareOptionEnergy, p.Energy)
		s.Econ.SetShareOption(player, economy.ShareOptionMapping, p.Mapping)
		s.Econ.SetShareOption(player, economy.ShareOptionRadar, p.Radar)
	case SeatSetShareMetal:
		// The typed integer, stored as single precision or replaced by the
		// storage capacity when above it [07 R-CAM-01 §6] [05 R-SHARE-01 §3].
		s.Econ.SetShareThreshold(player, economy.Metal, c.SetShareMetal.Amount)
	case SeatSetShareEnergy:
		s.Econ.SetShareThreshold(player, economy.Energy, c.SetShareEnergy.Amount)
	case SeatShareGift:
		return s.applyShareGift(seat, &c.ShareGift, tick)
	case SeatDeclareAlliance:
		return s.applyDeclareAlliance(seat, &c.DeclareAlliance)
	case SeatSharedVictory:
		return s.applySharedVictory(seat, c.SharedVictory.On)
	}
	return CommandApplied
}

// admitShareGift is the SHARE confirm's target test [05 R-SHARE-01 §5]: the
// target exists with an active control byte, is a seated row, does not carry
// the watcher bit and is not eliminated; it is not the giver, which the
// screen's list never offers. There is no alliance test — enemies may
// receive — and both humans and computers, any machine's, are offered.
//
// Nanolathe scenario rule (DESIGN_SURVIVAL): the Survival attacker is not a
// gift target. It is a scenario row, not a player the survivors' screen
// offers.
func (s *Session) admitShareGift(seat uint8, p *ShareGiftPayload) string {
	if s.onlineResults == nil || s.Econ == nil {
		return "an online battle"
	}
	target := p.Target
	cfg := s.seatCommands.online
	if target == seat || cfg == nil || int(target) >= len(cfg.roles) {
		return "a gift target other than the giver"
	}
	switch cfg.roles[target] {
	case MatchRoleHuman, MatchRoleComputer:
	case MatchRoleSurvivalAttacker:
		return "a gift target other than the Survival attacker"
	default:
		return "a seated gift target"
	}
	if !s.onlineSeated(target) || s.ownerEliminated(int(target)) {
		return "a seated gift target that is neither watching nor eliminated"
	}
	return ""
}

// applyShareGift is one SHARE confirmation, in retail's order — metal, then
// energy, then the units, then the map information — each step complete
// before the next and none gating a later one [05 R-SHARE-01 §5].
//
// Each amount goes through the transfer helper with the debit flag set, so it
// is clamped to the giver's live stock and credited to the target's
// production bucket for its next settlement; zero is a no-op
// [05 R-SHARE-01 §2]. The whole read-back reaches the helper as single
// precision, as retail converts it.
//
// The units are the issuer's live actors: a stale reference drops out and a
// live foreign one was refused whole at admission (§7.4.3). The ownership
// transfer visits them in ascending unit-identifier order, the order retail's
// gather walks the giver's slice, and leaves out what the screen leaves out
// (construction.GiftUnits). Its branch is the target's machine's
// (giftCopyRule). Every replacement takes the completion posture the capture
// transfer gives its own (the Capture seam): movement state, then its
// coverage publication.
//
// Map information is the mapping-grid merge from the giver to the target, at
// once and in stream order (§6.7 Q25) [05 R-SHARE-01 §6].
func (s *Session) applyShareGift(seat uint8, p *ShareGiftPayload, tick uint32) CommandOutcome {
	did := false
	if p.Metal != 0 {
		s.Econ.Transfer(seat, p.Target, economy.Metal, float32(p.Metal))
		did = true
	}
	if p.Energy != 0 {
		s.Econ.Transfer(seat, p.Target, economy.Energy, float32(p.Energy))
		did = true
	}
	handles := make([]pool.Handle, 0, len(p.Actors))
	for _, ref := range p.Actors {
		if s.commandActor(seat, ref) != nil {
			handles = append(handles, ref.Handle)
		}
	}
	if len(handles) != 0 && s.Build != nil {
		for _, r := range s.Build.GiftUnits(seat, p.Target, handles, s.giftCopyRule(seat, p.Target), tick) {
			if r.Outcome == construction.GiftTransferred {
				s.CompleteUnit(r.Replacement)
			}
		}
		did = true
	}
	if p.MapInfo && s.Vis != nil {
		s.Vis.CopyExplored(visibility.PlayerID(seat), visibility.PlayerID(p.Target))
		did = true
	}
	if !did {
		return CommandNoOp
	}
	return CommandApplied
}

// giftCopyRule chooses the ownership transfer's branch for a gift from giver
// to target (DESIGN_MULTIPLAYER §6.3 "Unit transfer"). Retail's giving
// machine chooses by the target's control byte: a player it simulates — its
// human or a computer it hosts — takes the local branch, a remote peer the
// remote one [05 R-SHARE-01 §5] [05 R-WORK-01 §15]. The giver is a human
// seat, so the only targets its machine simulates are the computers it
// hosts.
func (s *Session) giftCopyRule(giver, target uint8) construction.GiftCopyRule {
	if s.onlineResults != nil && int(target) < len(s.onlineResults.hosted) && s.onlineResults.hosted[target] == int8(giver) {
		return construction.GiftCopyLocal
	}
	return construction.GiftCopyRemote
}

// onlineShareHumans is the automatic dispatcher's human mask: bit p for every
// human seat still in the battle, a finally removed one excluded
// (economy.ShareTickSeats).
func (s *Session) onlineShareHumans() uint16 {
	var mask uint16
	for seat := range s.onlineResults.seats {
		if s.onlineResults.seats[seat].present && !s.seatCommands.removed[seat] {
			mask |= 1 << uint(seat)
		}
	}
	return mask
}

// stepOnlineSharing is the automatic dispatcher of an online battle, at
// retail's site: once per advancing sub-tick after the twelve phases, with
// the already-advanced tick [05 R-SHARE-01 §3]. Every human seat dispatches,
// ascending (§6.3 "Automatic sharing"), and each periodic map share applies
// on its request tick in that order (§6.7 Q25).
func (s *Session) stepOnlineSharing(tick uint32) {
	s.Econ.ShareTickSeats(tick, s.Units, s.onlineShareHumans(), s.shareCopyExplored)
}

// shareCopyExplored applies one periodic map share: the recipient copies the
// source's explored tiles as they stand now [05 R-SHARE-01 §6] (Q25).
func (s *Session) shareCopyExplored(source, recipient int) {
	if s.Vis != nil {
		s.Vis.CopyExplored(visibility.PlayerID(source), visibility.PlayerID(recipient))
	}
}

// publishOnlineSharing copies what the Tab strip's SHARE and ALLIES screens
// read into the committed frame [I6]: each slot's share options, row B of the
// alliance matrix (row A is PlayerRow.Allies), shared-victory bit and lobby
// team, and whether this client's seat may name the slot as a gift target or
// a declaration target now — the receiver's own admission, so the screens
// offer exactly what every replica admits. Outside an online battle the
// fields stay zero. It reads only and runs after publishPlayerRows.
func (s *Session) publishOnlineSharing(f *frame.Frame) {
	f.Sharing = frame.SharingView{}
	if s == nil || s.onlineResults == nil || s.Econ == nil || s.seatCommands.online == nil {
		return
	}
	local := s.LocalOwner
	issue := s.onlineSeatRole(local) == ""
	f.Sharing = frame.SharingView{
		Online:                  true,
		Seat:                    local,
		MayShare:                issue,
		SharedVictoryChangeable: issue && s.admitSharedVictory(local) == "",
	}
	for i := 0; i < frame.PlayerRowSlots && i < len(s.Econ.Players); i++ {
		p := &s.Econ.Players[i]
		row := &f.Players[i]
		row.AlliedBy = allianceRowB(s.Econ, i)
		row.SharedVictory = s.onlineResults.sharedVictory[i]
		row.Team = s.onlineResults.teams[i]
		row.ShareMetal, row.ShareEnergy = p.AutoShareMetal, p.AutoShareEnergy
		row.ShareMapping, row.ShareRadar = p.AutoShareSensor, p.ShareRadar
		row.GiftTarget = issue && s.admitShareGift(local, &ShareGiftPayload{Target: uint8(i)}) == ""
		row.AllianceTarget = issue && s.admitDeclareAlliance(local, &DeclareAlliancePayload{Target: uint8(i)}) == ""
	}
}
