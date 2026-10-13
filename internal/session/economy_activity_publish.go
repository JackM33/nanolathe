package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// publishEconomyActivities copies the observer's last completed settlements.
// The frame never aliases the observer's scratch or retained report [I6].
func (s *Session) publishEconomyActivities(f *frame.Frame) {
	f.EconomyFlows = f.EconomyFlows[:0]
	f.EconomyHolds = f.EconomyHolds[:0]
	f.EconomyHoldsAllowed = s.Econ != nil && s.Econ.Rules != nil && s.Econ.Rules.TemporaryActivityHolds()
	if s.Econ == nil {
		return
	}
	for p := uint8(0); p < 10; p++ {
		if !s.Econ.Players[p].Exists {
			continue
		}
		s.economyActivityScratch = s.Econ.AppendActivityReport(s.economyActivityScratch[:0], p)
		for _, flow := range s.economyActivityScratch {
			f.EconomyFlows = append(f.EconomyFlows, frame.EconomyFlowView{
				Player: p, Kind: uint8(flow.Activity), Unit: flow.Unit,
				Produced: flow.Produced, Requested: flow.Requested,
			})
		}
		for a := economy.ActivityGeneration; a < economy.ActivityCount; a++ {
			if s.Econ.ActivityHeld(p, a) {
				f.EconomyHolds = append(f.EconomyHolds, frame.EconomyHoldView{Player: p, Kind: uint8(a)})
			}
		}
	}
}
