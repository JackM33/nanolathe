package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The constructor patrol caption observes the bound policy at publication;
// drawing never reads preferences or a live queue (DESIGN_INTERFACE_HUD_INPUT
// "Constructor patrol button labels", [I6]). A future queued patrol alone is
// insufficient: only the active patrol or its retained producer receipt counts.
func (s *Session) publishedPatrolWork(u *units.Unit) uint8 {
	if s.Rules.Base != gameplay.Modern || s.Rules.Orders == nil || u == nil || u.Def == nil || !u.Def.Builder || u.Def.BMCode != 1 || !u.Def.MobileStandOrders {
		return 0
	}
	q := orders.QueueOfUnit(u)
	if q == nil || q.Head() == nil {
		return 0
	}
	active := q.Head()
	if ordinal := q.WorkAssignmentOrdinal(active); ordinal > 0 {
		active = q.PrimaryAt(ordinal - 1)
	}
	name := orders.DescriptorFor(active.ID).Name
	if name != "RepairPatrol" && name != "VTOL_RepairPatrol" {
		return 0
	}
	option := s.Rules.Orders.PatrolWork(orders.PatrolWorkRequest{Builder: u})
	if option > orders.PatrolAssistOnly {
		return 0
	}
	return uint8(option) + 1
}
