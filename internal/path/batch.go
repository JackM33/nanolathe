package path

import "github.com/nanolathe-gg/nanolathe/internal/pool"

// Nanolathe Modern parallel path batches (docs/DESIGN_MOVEMENT_PATH.md
// "Modern parallel path batches"; docs/INVARIANTS.md I1 "Parallel simulation
// exception").
//
// The retail scheduler holds one search at a time: a request is admitted, its
// search runs in 100-pop slices until it publishes, and only then is the next
// request polled [04 R-PATH-01 §6]. Under the Modern rule set the scheduler
// instead latches up to a fixed number of admitted searches and expands them
// in rounds. A round is fixed before it starts — which searches, in admission
// order, and how many heap pops each may take — and is a function of
// simulation state alone. The searches of a round expand concurrently, each on
// its own storage, against inputs nothing writes until the round has joined;
// the simulation goroutine then charges every search's work and publishes the
// finished ones in admission order. Which goroutine ran which search, and how
// many ran at once, cannot reach anything the scheduler computes.

// batchProvider is the optional boundary of Modern parallel path batches. A
// provider that does not implement it, or answers zero, keeps the retail
// single-request scheduler [04 R-PATH-01 §6].
type batchProvider interface {
	// PathBatch is asked once per scheduler call. searches > 0 selects the
	// batch scheduler: at most that many admitted searches are latched at
	// once, and no search expands more than pops heap pops in one call.
	PathBatch() (searches int, pops int32)
	// OpenBatch readies a latched request's search on the simulation
	// goroutine: request setup when it has no open search, nothing when it
	// has. slot is the batch place the request holds, stable while it is
	// latched. It returns the search a round may resume, or nil with the
	// finished result of a request that ended during setup; the WorkResult
	// carries the setup work to charge either way.
	OpenBatch(r Request, scale int32, slot int) (Search, WorkResult)
	// SettleBatch takes, on the simulation goroutine and in admission order,
	// what one round's resume of the request's search reported. It answers
	// what the scheduler charges and publishes, as a SearchFunc slice does.
	SettleBatch(r Request, scale int32, slot int, slice BatchSlice) WorkResult
}

// BatchSlice is one search's part of a round: the route, status and finish
// its resume reported and the work it did (heap pops and kernel probes).
type BatchSlice struct {
	Points []Point
	Status Status
	Done   bool
	Pops   int
}

// latchedSearch is one admitted request of a Modern batch.
type latchedSearch struct {
	req    Request
	player int
	scale  int32
	// slot is the request's batch place, the lowest free one at admission.
	// Movement keys per-search storage by it.
	slot int
	// spent counts the pops expanded in the current call. It is reset at the
	// start of every call, so it carries nothing between ticks.
	spent int32
}

// latchedPlayer reports whether player owns a latched search.
func (s *Scheduler) latchedPlayer(player int) bool {
	for i := range s.latched {
		if s.latched[i].player == player {
			return true
		}
	}
	return false
}

// latchedIndex is the place of unit's latched search, or -1.
func (s *Scheduler) latchedIndex(unit pool.Handle) int {
	for i := range s.latched {
		if s.latched[i].req.Unit == unit {
			return i
		}
	}
	return -1
}

// removeLatched removes the latched search at i, keeping the order of the
// rest.
func (s *Scheduler) removeLatched(i int) {
	s.latched = append(s.latched[:i], s.latched[i+1:]...)
}

// freeSlot is the lowest batch place no latched search holds.
func (s *Scheduler) freeSlot() int {
	for slot := 0; ; slot++ {
		held := false
		for i := range s.latched {
			if s.latched[i].slot == slot {
				held = true
				break
			}
		}
		if !held {
			return slot
		}
	}
}

// recordBatchTrace keeps the opt-in diagnostic copy of one batch result.
func (s *Scheduler) recordBatchTrace(req Request, work WorkResult, tick uint32) {
	if !s.traceEnabled {
		return
	}
	goal := DescribeGoal(req.Goal)
	s.recordTrace(Trace{Request: RequestTrace{Unit: req.Unit, Player: req.Player, Start: req.Start, Goal: goal, Activation: req.Activation}, Goal: goal, Points: append([]Point(nil), work.Points...), Status: work.Status, Done: work.Done, Tick: tick})
}

// tickBatch is one scheduler call under Modern parallel path batches, after
// the call's shares have been added and total is the call's work. Polling,
// its charges, the idle-run batching and the bounded-work stop are retail's
// admission loop; what differs is that admission does not wait for the
// latched searches to finish, and that the latched searches expand in
// rounds.
func (s *Scheduler) tickBatch(tick uint32, bp batchProvider, maxSearches int, popCap int32, total int32, bound workBoundProvider, sweepStop bool) {
	// A request the retail scheduler latched before a switch to a batching
	// set becomes the first latched search; nothing it did is repeated.
	if s.active != nil {
		slot := s.freeSlot()
		s.latched = append(s.latched, latchedSearch{})
		copy(s.latched[1:], s.latched)
		s.latched[0] = latchedSearch{req: *s.active, player: s.activePlayer, scale: s.activeScale, slot: slot}
		s.active = nil
	}
	for i := range s.latched {
		s.latched[i].spent = 0
	}
	// Latched searches expand before anything is polled, as the retail loop
	// continues its active search first [04 R-PATH-01 §6]; the places they
	// free are then filled, and the new searches expand in the next round.
	for {
		var ran bool
		total, ran = s.runBatchRound(tick, bp, popCap, total)
		admitted := false
		if total > 0 && len(s.latched) < maxSearches {
			before := len(s.latched)
			total = s.admitBatch(tick, bp, maxSearches, total, bound, sweepStop)
			admitted = len(s.latched) > before
		}
		if !ran && !admitted {
			// Nothing has budget left this call and nothing new waits: the
			// rest waits for the next call's shares.
			break
		}
	}
	// What a swept player's searches did not spend is credited as polls, as
	// the retail loop credits it at the stop (docs/DESIGN_MOVEMENT_PATH.md
	// "Modern bounded path work").
	for p := range s.parked {
		s.serviceCount[p] += s.parked[p]
		s.parked[p] = 0
	}
}

// chargeBatch charges a batched search's work to its player: first from what
// the player's swept polling parked this call, then from its accumulator and
// the call's total, which may go below zero as a retail continuation's does
// [04 R-PATH-01 §6].
func (s *Scheduler) chargeBatch(player int, charge int32, total int32) int32 {
	if charge <= 0 {
		return total
	}
	from := min(charge, s.parked[player])
	s.parked[player] -= from
	s.accumulator[player] -= charge - from
	return total - (charge - from)
}

// admitBatch polls for requests while the batch has a free place and work
// remains, opening each admitted request's search on the simulation
// goroutine in admission order. Every charge is the retail admission loop's:
// one per poll, and 100 plus the setup steps per admission
// [04 R-PATH-01 §6]. A request that finishes during setup publishes at once,
// as a retail admission does.
func (s *Scheduler) admitBatch(tick uint32, bp batchProvider, maxSearches int, total int32, bound workBoundProvider, sweepStop bool) int32 {
	for total > 0 && len(s.latched) < maxSearches {
		if idle, ok := s.provider.(idleCandidateProvider); ok {
			total = s.skipIdleRounds(idle, bound, total)
			if total <= 0 {
				break
			}
		}
		player := s.nextPlayer()
		if player < 0 {
			break
		}
		if !s.provider.Eligible(player) {
			s.accumulator[player] = 0
			continue
		}
		req, poll := s.provider.Poll(player)
		s.serviceCount[player]++ // every visited candidate consumes one poll [04 R-PATH-01 §6]
		s.accumulator[player]--
		total--
		if sweepStop {
			// Nanolathe Modern bounded path work, as the retail loop applies
			// it: the player is polled no more this call and the rest of its
			// work leaves the total. In a batch that rest is parked rather
			// than credited at once — the player's latched searches may still
			// spend it this call — and what is left is credited as polls when
			// the call ends.
			if poll == PollRequest {
				s.sweepPolls[player] = 0
			} else if s.sweepPolls[player]++; s.accumulator[player] > 0 && s.swept(bound, player) {
				s.parked[player] += s.accumulator[player]
				total -= s.accumulator[player]
				s.accumulator[player] = 0
				continue
			}
		}
		if poll != PollRequest || s.accumulator[player] < 0 {
			continue
		}
		// A unit has one search. A request admitted for a unit whose earlier
		// request is still latched replaces it, as a cancellation followed by
		// an admission would; movement keeps the open search when the goal
		// and activation are unchanged.
		if i := s.latchedIndex(req.Unit); i >= 0 {
			s.removeLatched(i)
		}
		scale := s.ScaleFor(uint8(player))
		slot := s.freeSlot()
		search, work := bp.OpenBatch(req, scale, slot)
		charge := int32(100 + work.SetupSteps + work.Pops)
		s.accumulator[player] -= charge
		total -= charge
		if work.Done || search == nil {
			work.Done = true
			s.recordBatchTrace(req, work, tick)
			if s.publish != nil {
				s.publish(req, work.Points, work.Status)
			}
			continue
		}
		s.recordBatchTrace(req, work, tick)
		s.latched = append(s.latched, latchedSearch{req: req, player: player, scale: scale, slot: slot})
	}
	return total
}

// runBatchRound expands the latched searches that have budget this round,
// concurrently, then charges and publishes in admission order.
//
// Budgets are handed out in admission order. Every latched search continues
// on the call's combined total whatever its player's balance, as the retail
// scheduler's one active search does, and is charged to its own player
// [04 R-PATH-01 §6]; positive credit is required only to admit a request. A
// player's searches may also spend the work its swept polling parked this
// call, which no other player's search may. No search takes more than it has
// left of the call's per-search pops, and a round never hands out more than
// the call has left. A search left without budget waits for the next call;
// what a finished search did not use is handed out again in the next round.
// Work is handed out in whole retail slices, so a batch of one search with no
// per-search cap is the retail scheduler. It reports whether any search had
// budget.
//
// The members are named by unit before anything is opened or published, and
// each is looked up again before it is touched: a publication that cancelled
// a later member's request leaves nothing of it to settle.
func (s *Scheduler) runBatchRound(tick uint32, bp batchProvider, popCap int32, total int32) (int32, bool) {
	round := &s.round
	round.reset()
	for i := range s.latched {
		if s.latched[i].spent < popCap {
			round.units = append(round.units, s.latched[i].req.Unit)
		}
	}
	if len(round.units) == 0 {
		return total, false
	}
	// Open (or find) each member's search on the simulation goroutine. A
	// search that has to be set up again — its storage was dropped while it
	// was latched — charges its setup as a retail continuation does.
	ran := false
	for _, unit := range round.units {
		i := s.latchedIndex(unit)
		if i < 0 {
			continue
		}
		m := s.latched[i]
		p := m.player
		// What this round has not yet handed out, of the call's total and of
		// the player's parked work, in whole retail slices: the retail loop
		// runs a 100-pop slice while any work remains [04 R-PATH-01 §6].
		left := max(total-round.pool, 0) + s.parked[p] - round.parked[p]
		budget := min(popCap-m.spent, (left+popsPerRequest-1)/popsPerRequest*popsPerRequest)
		if budget <= 0 {
			continue
		}
		ran = true
		search, work := bp.OpenBatch(m.req, m.scale, m.slot)
		total = s.chargeBatch(p, int32(work.SetupSteps+work.Pops), total)
		if work.Done || search == nil {
			work.Done = true
			s.removeLatched(i)
			s.recordBatchTrace(m.req, work, tick)
			if s.publish != nil {
				s.publish(m.req, work.Points, work.Status)
			}
			continue
		}
		fromParked := max(min(budget, s.parked[p]-round.parked[p]), 0)
		round.parked[p] += fromParked
		round.pool += budget - fromParked
		round.add(unit, search, int(budget))
	}
	round.run()
	// Settle in admission order.
	for k := range round.items {
		it := &round.items[k]
		i := s.latchedIndex(it.unit)
		if i < 0 {
			continue
		}
		m := s.latched[i]
		work := bp.SettleBatch(m.req, m.scale, m.slot, BatchSlice{Points: it.points, Status: it.status, Done: it.done, Pops: it.pops})
		total = s.chargeBatch(m.player, int32(work.SetupSteps+work.Pops), total)
		s.latched[i].spent += int32(it.pops)
		s.recordBatchTrace(m.req, work, tick)
		if work.Done {
			s.removeLatched(i)
			if s.publish != nil {
				s.publish(m.req, work.Points, work.Status)
			}
		}
	}
	round.release()
	return total, ran
}
