package session

// Online results are per human seat, without watching, removal or respawn
// (DESIGN_MULTIPLAYER §16.4.1, §16.6). Each row below is the countdown and
// result that its owner's machine would retain [08 R-TRIG-01 §6]. A computer
// seat has no row: it plays until it is destroyed or the battle ends, and the
// battle ends once every human row has latched (§6.5, §6.6).
type onlineSeatResult struct {
	present bool
	latch   EndLatch
	armed   bool // Tick zero is a valid first true due, not an unarmed sentinel.
	result  Result
}

type onlineResultState struct {
	seats [10]onlineSeatResult
	// sharedVictory is each row's shared-victory bit: set at entry exactly
	// for members of teams of two or more, as battleroom team propagation
	// leaves it [05 R-SHARE-01 §1], and afterwards written only by the
	// issuing seat's SharedVictory command (online_alliance.go). It enters
	// the runtime checkpoint (checkpoint_runtime.go).
	sharedVictory [10]bool
	// teams is each row's lobby team symbol, 0..4 a team and 5 none
	// (SkirmishDefaultAllyGroup), written once before the first tick from
	// the configuration (setTeams). Teams are fixed for the battle: a
	// battleroom team cannot be broken in battle
	// [07 "Tab options menu and manual exit"].
	teams [10]uint8
	// hosted is each computer row's host seat and -1 for every other row,
	// written once at entry from the configuration (setOnlineVisionTeams).
	// Under Strict 3.1 a host's countdown is mirrored onto its computers'
	// records (SeatRules.ComputersStopWithHost).
	hosted [10]int8
}

func newOnlineResultState(seats [10]bool) *onlineResultState {
	state := &onlineResultState{}
	for player := range state.hosted {
		state.hosted[player] = -1
		state.teams[player] = SkirmishDefaultAllyGroup
	}
	for player, present := range seats {
		if present {
			state.seats[player] = onlineSeatResult{present: true, latch: NewEndLatch()}
		}
	}
	return state
}

// evaluateOnlineSeatResult is called inside this player's settlement due,
// after its deadline advances and before settlement gates [08 R-TRIG-01 §6].
// The caller owns the due; this method adds no deadline or catch-up loop.
func (s *Session) evaluateOnlineSeatResult(player int, tick uint32) {
	if s == nil || s.onlineResults == nil || s.Units == nil || s.Econ == nil ||
		player < 0 || player >= len(s.onlineResults.seats) {
		return
	}
	row := &s.onlineResults.seats[player]
	if !row.present || row.latch.IsEnding() {
		return
	}
	s.processPendingCommanderDeaths(tick)
	// Defeat wins a simultaneous wipe: the defeat predicate is the own live
	// count, evaluated before the victory sweep [08 R-SKIR-01 §3]. A row whose
	// watch-mode bit is set polls victory instead of defeat
	// [08 R-TRIG-01 §6].
	if !s.Econ.Players[player].Watcher && s.Units.LiveCountForPlayer(player) == 0 {
		s.advanceOnlineSeatResult(player, tick, false, true)
		return
	}
	// Survival has no victory: an empty attacker between waves must never
	// arm the countdown (DESIGN_SURVIVAL §8).
	if s.Survival != nil {
		return
	}
	if s.onlineVictorySweep(player) {
		s.advanceOnlineSeatResult(player, tick, true, true)
	}
	// A false due retains both the countdown and its pending view.
}

// onlineVictorySweep is the kind-3 victory sweep for the local row local
// [08 R-SKIR-01 §3] "Victory detection". It is false at once under the
// Deathmatch rule. Otherwise every other row j that is seated, has
// controller 1, 2 or 3 and is not watching is visited in slot order: a j
// that has created nothing denies victory; a j with no live unit is skipped;
// any other j needs both rows' shared-victory bits, both directions of the
// alliance between local and j, and j's declaration toward every seated row
// that is not eliminated (live units, or nothing created yet) — local, j
// itself and watchers included. With no surviving j the sweep is true.
//
// One shared directed matrix holds the declarations (§6.7 Q22): row B of the
// research, local.B[j], is j's declaration toward local. The matrix starts as
// the entry's team rows and changes only through DeclareAlliance, the
// shared-victory bits only through SharedVictory, each at its command's tick
// and stream position (online_alliance.go); every row's sweep reads the same
// matrix, so a third seat sees the declarations the sender and the recipient
// see.
func (s *Session) onlineVictorySweep(local int) bool {
	if CommanderDeathMode(s.Skirmish.CommanderDeath) == CommanderDeathDeathmatch {
		return false
	}
	seated := func(i int) bool {
		p := &s.Econ.Players[i]
		return p.Exists && (p.ControllerState == 1 || p.ControllerState == 2 || p.ControllerState == 3)
	}
	for j := range s.Econ.Players {
		if j == local || !seated(j) || s.Econ.Players[j].Watcher || s.Econ.Players[j].IsObserver {
			continue
		}
		if s.Units.CreatedCountForPlayer(j) == 0 {
			return false
		}
		if s.Units.LiveCountForPlayer(j) == 0 {
			continue
		}
		if !s.onlineResults.sharedVictory[j] || !s.onlineResults.sharedVictory[local] ||
			!s.Econ.DeclaresAlliance(uint8(local), uint8(j)) || !s.Econ.DeclaresAlliance(uint8(j), uint8(local)) {
			return false
		}
		for k := range s.Econ.Players {
			if !s.Econ.Players[k].Exists {
				continue
			}
			if s.Units.LiveCountForPlayer(k) == 0 && s.Units.CreatedCountForPlayer(k) != 0 {
				continue // eliminated
			}
			if !s.Econ.DeclaresAlliance(uint8(j), uint8(k)) {
				return false
			}
		}
	}
	return true
}

// stepOnlineNoHumanEnd is the after-player-loop site, once per tick. It shares
// each seat's countdown with the won/lost due paths [08 R-SESS-01 §1]
// [08 R-TRIG-01 §6]; it is not another timer and can follow a true due this
// tick. A watching human is not playing [08 R-SESS-01 §1], and this site
// latches ending and lost directly, with no watch branch [08 R-TRIG-01 §6].
func (s *Session) stepOnlineNoHumanEnd(tick uint32) {
	if s == nil || s.onlineResults == nil || s.Units == nil || s.Econ == nil ||
		CommanderDeathMode(s.Skirmish.CommanderDeath) == CommanderDeathDeathmatch {
		return
	}
	// An ending latch is not a player-record mutation. A live winner still
	// counts here; all-seat completion is checked separately [08 R-SESS-01 §1].
	if s.onlineHumanStillPlaying(-1) {
		return
	}
	for player := range s.onlineResults.seats {
		row := &s.onlineResults.seats[player]
		if row.present && !row.latch.IsEnding() {
			s.advanceOnlineSeatResult(player, tick, false, false)
		}
	}
}

// advanceOnlineSeatResult refreshes presentation metadata on each true path;
// only the path crossing below zero writes terminal bits [08 R-TRIG-01 §6].
// due says the row's own settlement due runs the path, where a defeat that
// crosses below zero may take the watch branch (onlineWatchBranch); the
// after-loop site passes false.
func (s *Session) advanceOnlineSeatResult(player int, tick uint32, victory, due bool) {
	row := &s.onlineResults.seats[player]
	if !row.armed {
		row.armed = true
		row.result.ArmedTick = tick
	}
	var ended bool
	switch {
	case victory:
		ended = row.latch.AdvanceWin(true)
	case due && row.latch.Countdown == 0 && s.onlineWatchBranch(player):
		s.enterOnlineWatch(player)
	default:
		ended = row.latch.AdvanceLose(true)
	}
	p := &s.Econ.Players[player]
	p.GameEnded = row.latch.IsEnding()
	p.EndGameCountdown = int32(row.latch.Countdown)
	s.mirrorOnlineCountdown(player)

	winner, losers := s.resultTeamsForOwner(player, victory)
	scores := s.collectScores(winner, false)
	reason := ReasonAllUnits
	if CommanderDeathMode(s.Skirmish.CommanderDeath) != CommanderDeathContinues {
		reason = ReasonCommanderDeath
	}
	kind := "defeat"
	if victory {
		kind = "victory"
	}
	row.result = Result{
		Ended: ended, Kind: kind, WinnerTeam: winner,
		Winners: resultWinnersFor(winner, false), Losers: losers, Reason: reason,
		ArmedTick: row.result.ArmedTick, Countdown: row.latch.Countdown,
		Scores: scores, ColumnMaxima: resultColumnMaxima(scores),
	}
	if ended {
		row.result.Tick = tick
		// Each survivor's final result carries the Survival line, as the
		// single-player ended view does (DESIGN_SURVIVAL §8).
		row.result.Survival = s.survivalResult(tick)
	}
}

// mirrorOnlineCountdown copies host's countdown and ending bit onto the
// records of the computers it hosts when the bound seat policy says one
// machine's countdown gates them all, as retail's per-machine countdown gates
// its human and hosted computers' settlement [05 R-ECO-01 §1]
// [08 R-SKIR-01 §3]. The copy is taken at the host's own write, inside its
// due block, so a computer in a later row settles this tick on the new value,
// as retail's machine-wide words are read. Under the Modern policy the host's
// countdown is its own and its computers play on.
func (s *Session) mirrorOnlineCountdown(host int) {
	if !s.seatRules().ComputersStopWithHost() {
		return
	}
	src := &s.Econ.Players[host]
	for player, h := range s.onlineResults.hosted {
		if int(h) == host {
			p := &s.Econ.Players[player]
			p.GameEnded, p.EndGameCountdown = src.GameEnded, src.EndGameCountdown
		}
	}
}

// onlineWatchBranch reports whether a defeat crossing below zero on player's
// due takes retail's watch branch rather than the direct ending
// [08 R-SKIR-01 §3] "The watch branch". The branch needs a multiplayer
// battle — this one — a local player not already removed, and either the
// host's watching-allowed bit or a live computer hosted on this machine.
// Online watching is refused at entry (§16.6), so only the second disjunct
// is reachable; it is the Strict 3.1 machine of §6.6, where one countdown
// gates the human and the computers it hosts (SeatRules.ComputersStopWithHost).
// Under the Modern seat policy a hosted computer never takes its host's
// countdown, and the host takes its own direct ending (§6.6, Q30).
func (s *Session) onlineWatchBranch(player int) bool {
	if !s.seatRules().ComputersStopWithHost() || s.seatCommands.removed[player] {
		return false
	}
	return s.onlineHostsLiveComputer(player)
}

// onlineHostsLiveComputer is retail's first live-player counter for host's
// machine, nonzero [08 R-SESS-01 §1]: a computer row host hosts whose record
// exists with a side other than 10 and controller 2, and that is live — a
// live unit, or nothing created yet.
func (s *Session) onlineHostsLiveComputer(host int) bool {
	for row, h := range s.onlineResults.hosted {
		if int(h) != host {
			continue
		}
		p := &s.Econ.Players[row]
		if p.Exists && p.Side != 10 && p.ControllerState == 2 &&
			(s.Units.LiveCountForPlayer(row) != 0 || s.Units.CreatedCountForPlayer(row) == 0) {
			return true
		}
	}
	return false
}

// onlineHumanStillPlaying is retail's second live-player counter, nonzero
// [08 R-SESS-01 §1]: a present human row other than except whose record
// exists with a side other than 10, whose watch-mode bit is clear and that is
// live.
func (s *Session) onlineHumanStillPlaying(except int) bool {
	for player := range s.onlineResults.seats {
		if player == except || !s.onlineResults.seats[player].present {
			continue
		}
		p := &s.Econ.Players[player]
		if p.Exists && p.Side != 10 && p.ControllerState == 1 && !p.Watcher &&
			(s.Units.LiveCountForPlayer(player) != 0 || s.Units.CreatedCountForPlayer(player) == 0) {
			return true
		}
	}
	return false
}

// enterOnlineWatch is the watch branch on the due whose decrement takes the
// countdown from 0 to −1 [08 R-SKIR-01 §3] "The watch branch", "A hosting
// machine's own defeat and its computer". It writes neither the ending bit
// nor the countdown, which the decrement leaves at −1, so the countdown
// mirrored onto the hosted computers lets their settlement gate pass again on
// their next due — the sixth from the arming one [05 R-ECO-01 §1]. It sets
// the row's watch-mode bit, after which the row polls victory instead of
// defeat and is no longer a human still playing; and, when another human is
// still playing, it clears the latch's won bit. The host's units are gone,
// so its sensor pass is a defeated viewer's (stepSensorPhaseFor). The row's
// result keeps the defeat its lost path wrote, and the row is out of play but
// not ended (onlineBattleEnded).
//
// Retail's branch also clears the machine's mapping and line-of-sight mode
// bits and rebuilds visibility. That is the watcher-entry transition of
// DESIGN_MULTIPLAYER §6.3 and Q24, which is not built online: the watching
// perspective and the computers borrowing it keep the battle's mode. The
// announcement line is presentation.
func (s *Session) enterOnlineWatch(player int) {
	row := &s.onlineResults.seats[player]
	row.latch.Countdown--
	row.latch.Pending = 0
	s.Econ.Players[player].Watcher = true
	if s.onlineHumanStillPlaying(player) {
		row.latch.Bits &^= LatchBitWin1
	}
}

func (s *Session) onlineSeatEnded(player int) bool {
	return s != nil && s.onlineResults != nil && player >= 0 && player < len(s.onlineResults.seats) &&
		s.onlineResults.seats[player].present && s.onlineResults.seats[player].latch.IsEnding()
}

// onlineBattleEnded controls shared simulation termination. GetResult's local
// projection must not stop the other human's world (DESIGN_MULTIPLAYER §16.4.1).
// The battle ends once every human row is out of play: ended, or watching
// after the watch branch. A watching row is not ended — its block, its
// sensor pass and its computers run on while another human still plays —
// but it never blocks the end: retail's watching machine is ended by its
// no-human site once the playing humans have left [08 R-SKIR-01 §3]
// [08 R-SESS-01 §1], and departures are not modelled online yet (§11.1).
func (s *Session) onlineBattleEnded() bool {
	if s == nil || s.onlineResults == nil {
		return false
	}
	present := false
	for player := range s.onlineResults.seats {
		row := &s.onlineResults.seats[player]
		if row.present {
			present = true
			if !row.latch.IsEnding() && !(s.Econ != nil && s.Econ.Players[player].Watcher) {
				return false
			}
		}
	}
	return present
}

// ResultForSeat returns a detached pending or final online result, or zero for
// an absent seat or an ordinary session (DESIGN_MULTIPLAYER §16.4.1).
func (s *Session) ResultForSeat(player uint8) Result {
	if s == nil || s.onlineResults == nil || int(player) >= len(s.onlineResults.seats) ||
		!s.onlineResults.seats[player].present {
		return Result{}
	}
	r := copyResult(s.onlineResults.seats[player].result)
	r.Survival = r.Survival.Copy()
	return r
}
