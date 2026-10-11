package session

import "github.com/nanolathe-gg/nanolathe/internal/economy"

// In-battle alliance declarations and shared victory online
// (DESIGN_MULTIPLAYER §6.7 Q22, §7.1): the Allies window's ally click and its
// OK, carried as seat commands 43 and 44. Retail keeps two eleven-byte rows
// per player slot, each machine its own copy; the common world keeps one
// directed matrix — economy.Player.Allies, row A of every slot — whose row B
// is its transpose, so every replica and every seat's victory sweep read the
// same declarations (Q22). Admission is Nanolathe's receiver contract
// (§7.2); the gates it applies are the window's [07 "Tab options menu and
// manual exit"], and the write is the alliance writer's [05 R-SHARE-01 §1].

// setTeams records each configured row's lobby team symbol, once at entry
// (setOnlineSeatCommands). A nil state, a session with no online result rows,
// records nothing.
func (r *onlineResultState) setTeams(seats []MatchSeat) {
	if r == nil {
		return
	}
	for i := range r.teams {
		r.teams[i] = SkirmishDefaultAllyGroup
		if i < len(seats) {
			r.teams[i] = seats[i].AllyGroup
		}
	}
}

// sameLobbyTeam reports whether a and b carry the same team symbol other
// than "none": the window greys such a row's ally button, because a
// battleroom team cannot be broken in battle [07 "Tab options menu and
// manual exit"].
func (r *onlineResultState) sameLobbyTeam(a, b uint8) bool {
	return r != nil && int(a) < len(r.teams) && int(b) < len(r.teams) &&
		r.teams[a] != SkirmishDefaultAllyGroup && r.teams[a] == r.teams[b]
}

// lobbyTeamOfTwo reports whether seat's team holds two or more seated rows,
// which greys the window's VICTORY box [07 "Tab options menu and manual
// exit"] — the same rule battleroom propagation applies to the bit
// [05 R-SHARE-01 §1] and the configuration's matchTeamOfTwo.
func (r *onlineResultState) lobbyTeamOfTwo(seat uint8) bool {
	if r == nil || int(seat) >= len(r.teams) || r.teams[seat] == SkirmishDefaultAllyGroup {
		return false
	}
	for j := range r.teams {
		if j != int(seat) && r.teams[j] == r.teams[seat] {
			return true
		}
	}
	return false
}

// onlineHumanRow reports a configured human row that has not been finally
// removed.
func (s *Session) onlineHumanRow(seat uint8) bool {
	cfg := s.seatCommands.online
	return cfg != nil && int(seat) < len(cfg.roles) && cfg.roles[seat] == MatchRoleHuman && !s.seatCommands.removed[seat]
}

// onlineSeated is the window's row test for a slot: a record that exists
// with an active control byte, not watching and not an observer
// [07 "Tab options menu and manual exit"]; a finally removed seat's record is
// cleared [05 R-SHARE-01 §1] item 7.
func (s *Session) onlineSeated(seat uint8) bool {
	p := s.playerRecord(int(seat))
	return p != nil && p.Exists && p.ControllerState >= 1 && p.ControllerState <= 3 &&
		!p.Watcher && !p.IsObserver && !s.seatCommands.removed[seat]
}

// admitDeclareAlliance is the ally button's activity, checked by every
// receiver [07 "Tab options menu and manual exit"]: the issuer — already a
// playing human seat (onlineSeatRole) — is not eliminated; the target is
// another live, seated human, never a computer (the button is inactive for a
// computer row, so its in-battle alliances stay the battleroom's
// [05 R-SHARE-01 §1]); and the two are not on one lobby team.
func (s *Session) admitDeclareAlliance(seat uint8, p *DeclareAlliancePayload) string {
	if s.onlineResults == nil || s.Econ == nil {
		return "an online battle"
	}
	if s.ownerEliminated(int(seat)) {
		return "a declaring seat that is not eliminated"
	}
	target := p.Target
	if target == seat || !s.onlineHumanRow(target) || !s.onlineSeated(target) || s.ownerEliminated(int(target)) {
		return "another live human seat as the declaration's target"
	}
	if s.onlineResults.sameLobbyTeam(seat, target) {
		return "a target outside the issuer's lobby team; a battleroom team cannot be broken in battle"
	}
	return ""
}

// applyDeclareAlliance is the alliance writer with (issuer, target, value,
// force 0) on the one matrix [05 R-SHARE-01 §1]: the declarer's row-A entry
// toward the target takes the value. The writer's other stores fall away:
// its row-B mirror for a computer target needs a computer target, which
// admission refuses, and row B is the matrix's transpose rather than a copy
// (Q22). Nothing else changes — no delay, consent or test of units in flight
// [05 R-SHARE-01 §1] — and every consumer reads the matrix live: hostility in
// order resolution, the target registry and both computer players, the guard
// join, the automatic shares, share radar and the kind-3 victory sweep. The
// window's announcement line is presentation and stays on the client.
func (s *Session) applyDeclareAlliance(seat uint8, p *DeclareAlliancePayload) CommandOutcome {
	s.Econ.Players[seat].Allies[p.Target] = p.Value
	return CommandApplied
}

// admitSharedVictory is the VICTORY box's activity: greyed for a watcher —
// which onlineSeatRole already refuses — and for a seat whose lobby team has
// two or more seated rows [07 "Tab options menu and manual exit"].
func (s *Session) admitSharedVictory(seat uint8) string {
	if s.onlineResults == nil {
		return "an online battle"
	}
	if s.onlineResults.lobbyTeamOfTwo(seat) {
		return "a seat outside a lobby team of two or more; its shared-victory bit is the team's"
	}
	return ""
}

// applySharedVictory is the window's OK writing the box into the issuer's
// shared-victory bit [07 "Tab options menu and manual exit"]; the kind-3
// sweep reads it (onlineVictorySweep).
func (s *Session) applySharedVictory(seat uint8, on bool) CommandOutcome {
	s.onlineResults.sharedVictory[seat] = on
	return CommandApplied
}

// allianceRowB is row B of seat: each slot's declaration toward seat, the
// transpose of the matrix rather than a copy of it (Q22).
func allianceRowB(econ *economy.Service, seat int) [10]bool {
	var row [10]bool
	if econ == nil || seat < 0 || seat >= len(econ.Players) {
		return row
	}
	for j := range econ.Players {
		row[j] = econ.Players[j].Allies[seat]
	}
	return row
}
