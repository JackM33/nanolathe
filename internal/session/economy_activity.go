package session

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

type economyUpkeepRestore struct {
	Unit  pool.UnitRef
	Owner uint8
}

func validEconomyActivity(a economy.Activity) bool {
	return a > economy.ActivityOther && a < economy.ActivityCount
}

func (s *Session) economyActivityHoldsAllowed() bool {
	return s != nil && s.Econ != nil && s.Econ.Rules != nil && s.Econ.Rules.TemporaryActivityHolds()
}

func (s *Session) applyEconomyActivity(owner uint8, c HumanEconomyActivityCommand) CommandOutcome {
	if !s.economyActivityHoldsAllowed() || owner >= 10 || !validEconomyActivity(c.Activity) {
		return CommandRejected
	}
	if !c.Pause {
		if !s.Econ.HoldActive[owner][c.Activity] {
			return CommandNoOp
		}
		s.releaseEconomyActivity(owner, c.Activity)
		return CommandApplied
	}
	// A repeated pause is idempotent. It does not turn off a unit whose
	// later manual activation overrode this hold.
	if s.Econ.ActivityHeld(owner, c.Activity) {
		return CommandNoOp
	}
	s.Econ.HoldActive[owner][c.Activity] = true
	if c.Activity == economy.ActivityUpkeep && s.Units != nil {
		// The normal ascending pool walk preserves callback order. This is
		// actual operational shutdown, never forgiveness of an expense.
		for _, u := range s.Units.Iter() {
			if !economyUpkeepCandidate(u) || u.Owner != owner || !u.Activated {
				continue
			}
			ref := s.Units.Reference(u.Handle)
			if ref.Serial == 0 {
				continue
			}
			// An earlier activation command may still be queued to execute.
			// Keep its intent authoritative instead of restoring over it later.
			if !hasQueuedActivation(u) {
				s.economyUpkeepRestore = append(s.economyUpkeepRestore, economyUpkeepRestore{Unit: ref, Owner: owner})
			}
			u.SetActivationEdge(false)
		}
	}
	return CommandApplied
}

func economyUpkeepCandidate(u *units.Unit) bool {
	return u != nil && u.Alive && u.Def != nil && u.Remaining == 0 &&
		u.Flags&units.BuildingClassStatus != 0 && u.Def.OnOffable && u.Def.EnergyUse > 0
}

func hasQueuedActivation(u *units.Unit) bool {
	q := orders.QueueOfUnit(u)
	if q == nil {
		return false
	}
	on, off := orders.Lookup("Activate"), orders.Lookup("Deactivate")
	for _, rows := range [][]*orders.Node{q.Primary(), q.Secondary()} {
		for _, n := range rows {
			if n != nil && (n.ID == on || n.ID == off) {
				return true
			}
		}
	}
	return false
}

func (s *Session) releaseEconomyActivity(owner uint8, a economy.Activity) {
	s.Econ.HoldActive[owner][a] = false
	if a != economy.ActivityUpkeep {
		return
	}
	kept := s.economyUpkeepRestore[:0]
	for _, r := range s.economyUpkeepRestore {
		if r.Owner != owner {
			kept = append(kept, r)
			continue
		}
		if s.Units != nil {
			u := s.Units.LookupReference(r.Unit)
			if economyUpkeepCandidate(u) && u.Owner == owner && !u.Activated {
				u.SetActivationEdge(true)
			}
		}
	}
	s.economyUpkeepRestore = kept
}

func (s *Session) releaseEconomyActivityHolds() {
	if s.Econ == nil {
		return
	}
	for owner := range s.Econ.HoldActive {
		for a := economy.ActivityGeneration; a < economy.ActivityCount; a++ {
			s.releaseEconomyActivity(uint8(owner), a)
		}
	}
}

func (s *Session) forgetEconomyUpkeepRestore(ref pool.UnitRef) {
	for i, r := range s.economyUpkeepRestore {
		if r.Unit == ref {
			copy(s.economyUpkeepRestore[i:], s.economyUpkeepRestore[i+1:])
			s.economyUpkeepRestore = s.economyUpkeepRestore[:len(s.economyUpkeepRestore)-1]
			return
		}
	}
}

func economyActivityError(path, expected string) error {
	return fmt.Errorf("nanolathe: economy activity control refused: logical path %s, providers searched [session], expected %s", path, expected)
}
