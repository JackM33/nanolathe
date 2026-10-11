package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The composed adapter's slot verbs, the ones the air order executors call,
// drop the Aim request with a target they clear under Modern only. An already
// empty slot raises no TargetCleared and keeps its latch, and Strict and
// Community keep retail's untouched Aim state [04 R-ORD-01 §7].
// Nanolathe Modern policy: DESIGN_UNITS_ORDERS_COB "Modern aim re-issue".
func TestOrderSlotVerbsDropAimOnlyUnderModern(t *testing.T) {
	verbs := []struct {
		name  string
		flags uint8
		call  func(*units.Unit, int) bool
	}{
		{"release", units.SlotFlagEnabled | units.SlotFlagAutonomous, combat.ReleaseWeaponSlot},
		{"inhibit", units.SlotFlagEnabled, combat.InhibitWeaponSlot},
		{"stop", units.SlotFlagEnabled, combat.StopWeaponFiring},
	}
	for _, set := range reservedRuleSets() {
		_, modern := set.Orders.(*orders.ModernRules)
		s := &Session{Rules: set}
		for _, verb := range verbs {
			for _, targeted := range []bool{true, false} {
				u := &units.Unit{}
				slot := &u.Slots[0]
				slot.Flags = verb.flags | units.SlotFlagAimLatch
				slot.Aim = cob.AimSlot{IssueBit: true}
				if targeted {
					slot.Target = units.Target{Kind: units.TargetUnit, Unit: 7}
				}
				if !s.orderSlotVerb(u, 0, verb.call) || slot.Target.Kind != units.TargetNone {
					t.Fatalf("%s %s targeted=%v: verb did not clear", set.Name, verb.name, targeted)
				}
				dropped := modern && targeted
				if slot.Aim.IssueBit == dropped || (slot.Flags&units.SlotFlagAimLatch == 0) != dropped {
					t.Fatalf("%s %s targeted=%v: aim request dropped=%v, want %v", set.Name, verb.name, targeted, !slot.Aim.IssueBit, dropped)
				}
			}
		}
	}
}
