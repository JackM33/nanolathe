package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
)

func setActivityTestHold(s *economy.Service, owner uint8, a economy.Activity, modern bool) {
	s.Rules = economy.StrictRules{}
	if modern {
		s.Rules = economy.ModernRules{}
	}
	s.HoldActive[owner][a] = true
}

func TestActivityHoldConstructionKeepsWakeWithoutFreeWork(t *testing.T) {
	for _, modern := range []bool{false, true} {
		s, b, target, _ := reclaimFixture(t, 10, 16)
		target.Remaining, target.Def.BuildCostEnergy = 1, 100
		setActivityTestHold(s.Economy, b.Owner, economy.ActivityConstruction, modern)
		beforeHealth, beforeRemaining := target.Health, target.Remaining
		committed := s.sharedStep(b, target, 30, 1)
		if committed == modern || target.Pending&0x8000 == 0 {
			t.Fatalf("modern=%v committed=%v wake=%x", modern, committed, target.Pending)
		}
		cost := s.Economy.UnitBuckets(b.Handle)
		if modern {
			if target.Health != beforeHealth || target.Remaining != beforeRemaining || cost[economy.Energy].Requested != 0 || cost[economy.Metal].Requested != 0 {
				t.Fatal("held construction progressed or requested resources")
			}
			s.Economy.HoldActive[b.Owner][economy.ActivityConstruction] = false
			if !s.sharedStep(b, target, 30, 2) || target.Remaining >= beforeRemaining || cost[economy.Energy].Requested <= 0 {
				t.Fatal("resume did not perform paid work")
			}
		} else if target.Remaining >= beforeRemaining || cost[economy.Energy].Accepted <= 0 {
			t.Fatal("Strict bypass changed construction")
		}
	}
}

func TestActivityHoldHealingSuppressesPacketAndDemand(t *testing.T) {
	for _, modern := range []bool{false, true} {
		for _, passive := range []bool{false, true} {
			s, b, target, _ := reclaimFixture(t, 10, 16)
			target.Def.BuildTime, target.Def.BuildCostEnergy = 100, 100
			payer := b
			if passive {
				payer = target
			}
			setActivityTestHold(s.Economy, payer.Owner, economy.ActivityHealing, modern)
			committed := false
			if passive {
				committed = s.RepairPassive(target, 30)
			} else {
				committed = s.Repair(b, target, 30)
			}
			want := float32(1)
			if modern {
				want = 0
			}
			if committed == modern || target.Health != 10+int32(want) || s.Economy.UnitBuckets(payer.Handle)[economy.Energy].Requested != want {
				t.Fatalf("modern=%v passive=%v healing or payment mismatch", modern, passive)
			}
		}
	}
}

func TestActivityHoldUnitReclaimRetainsPulse(t *testing.T) {
	for _, modern := range []bool{false, true} {
		s, b, target, n := reclaimFixture(t, 100, 16)
		n.Phase, n.Param2 = 5, 16
		setActivityTestHold(s.Economy, b.Owner, economy.ActivityReclaim, modern)
		s.unitReclaimVisit(b, n, 0, 1)
		if modern {
			if target.Health != 100 || n.Param2 != 16 || b.RevealDeadline != 0 {
				t.Fatal("held reclaim spent pulse or touched target")
			}
			s.Economy.HoldActive[b.Owner][economy.ActivityReclaim] = false
			s.unitReclaimVisit(b, n, 0, 2)
		}
		if target.Health >= 100 || n.Param2 != 2 {
			t.Fatalf("modern=%v reclaim failed to apply retained pulse", modern)
		}
	}
}
