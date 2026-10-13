//go:build retail

package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
)

func TestEconomyActivityRetailSaveResumesHeldUnitsOnly(t *testing.T) {
	f := loadRetailFixture(t)
	f.cfg.Gameplay = gameplay.Modern
	s := f.session(t)
	stepRetail(s, 5)
	u := placeCompleteRetailUnit(t, s, "armrad", s.LocalOwner, 900<<16, 900<<16)
	if !economyUpkeepCandidate(u) {
		t.Fatal("reference radar does not satisfy the upkeep control contract")
	}
	u.SetActivationEdge(true)
	manual := placeCompleteRetailUnit(t, s, "armrad", s.LocalOwner, 1000<<16, 900<<16)
	manual.SetActivationEdge(true)
	for a := economy.ActivityGeneration; a < economy.ActivityCount; a++ {
		if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanEconomyActivity, EconomyActivity: HumanEconomyActivityCommand{Activity: a, Pause: true}}); err != nil {
			t.Fatal(err)
		}
	}
	stepRetail(s, 1)
	if u.Activated {
		t.Fatal("hold did not switch off reference radar")
	}
	s.applyHumanCommand(HumanCommand{Kind: HumanActivation, Activation: HumanActivationCommand{Unit: manual.Handle}}, s.Clock.GlobalTick+1)
	loaded, err := LoadRetailSaveWithDeps(retailBankOf(t, s), RetailLoadDeps{FS: f.fs, Catalog: f.cat, Gameplay: gameplay.Modern, UnitLimit: s.Skirmish.UnitLimit, SimSeed: 7, CRTSeed: 7})
	if err != nil {
		t.Fatal(err)
	}
	dst := loaded.Battle.Session
	if dst.Econ.HoldActive != ([10][economy.ActivityCount]bool{}) || len(dst.economyUpkeepRestore) != 0 {
		t.Fatal("load retained an activity hold")
	}
	restored := dst.Units.Unit(loaded.Battle.StableUnit[uint16(u.Handle)])
	if restored == nil || !restored.Activated {
		t.Fatal("load did not reactivate temporarily held radar")
	}
	restoredManual := dst.Units.Unit(loaded.Battle.StableUnit[uint16(manual.Handle)])
	if restoredManual == nil || restoredManual.Activated {
		t.Fatal("load overrode manual off intent")
	}
	if u.Activated || manual.Activated || !s.Econ.ActivityHeld(s.LocalOwner, economy.ActivityUpkeep) {
		t.Fatal("saving changed the live battle")
	}
}
