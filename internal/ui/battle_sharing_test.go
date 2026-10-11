package ui

import "testing"

// The networked session's windows open and close with no pause intent and
// are not the options root: ARMOPT is not drawn beneath them, and Escape's
// back transition closes them outright [07 "Tab options menu and manual
// exit"][07 §11].
func TestSharingModalsCarryNoPauseAndNoOptionsLayer(t *testing.T) {
	for _, m := range []BattleModal{BattleModalTabMenu, BattleModalShare, BattleModalAllies} {
		s := NewProductionBattleState()
		if !s.OpenSharing(m) || s.Modal() != m {
			t.Fatalf("open %d: modal %d", m, s.Modal())
		}
		if s.HasOptionsLayer() || s.HasExitLayer() {
			t.Fatalf("modal %d reports an options or exit layer", m)
		}
		if intent := s.Back(); intent != (BattleScheduleIntent{}) || s.Modal() != BattleModalClosed {
			t.Fatalf("back from %d: intent %+v modal %d", m, intent, s.Modal())
		}
		s.OpenSharing(m)
		s.CloseSharing()
		if s.Modal() != BattleModalClosed {
			t.Fatalf("close %d left modal %d", m, s.Modal())
		}
	}
	s := NewProductionBattleState()
	if s.OpenSharing(BattleModalOptions) || s.Modal() != BattleModalClosed {
		t.Fatal("OpenSharing opened the options root")
	}
	s.OpenOptions()
	s.CloseSharing()
	if s.Modal() != BattleModalOptions || !s.HasOptionsLayer() {
		t.Fatal("CloseSharing closed the options root")
	}
	if s.CloseOptions(); s.Modal() != BattleModalClosed || s.StripBeneath() {
		t.Fatal("the options root opened with no strip did not close to the battle")
	}
}

// The Tab strip stays open beneath every window its buttons open — SHARE,
// ALLIES and the options root — and is the top window again when that window
// closes; a window opened with no strip open closes to the battle [07 "Tab
// options menu and manual exit"].
func TestTabStripStaysOpenBeneathItsWindows(t *testing.T) {
	for _, m := range []BattleModal{BattleModalShare, BattleModalAllies} {
		s := NewProductionBattleState()
		s.OpenSharing(BattleModalTabMenu)
		s.OpenSharing(m)
		if s.Modal() != m || !s.StripBeneath() || s.HasOptionsLayer() {
			t.Fatalf("modal %d over the strip: top %d, strip beneath %v", m, s.Modal(), s.StripBeneath())
		}
		s.CloseSharing()
		if s.Modal() != BattleModalTabMenu || s.StripBeneath() {
			t.Fatalf("closing %d left modal %d", m, s.Modal())
		}
		s.OpenSharing(m)
		if s.Back(); s.Modal() != BattleModalTabMenu {
			t.Fatalf("back from %d left modal %d", m, s.Modal())
		}
		s.CloseSharing()
		if s.Modal() != BattleModalClosed {
			t.Fatalf("closing the strip left modal %d", s.Modal())
		}
		s.OpenSharing(m)
		if s.CloseSharing(); s.Modal() != BattleModalClosed {
			t.Fatalf("%d opened with no strip closed to modal %d", m, s.Modal())
		}
	}
	s := NewProductionBattleState()
	s.OpenSharing(BattleModalTabMenu)
	s.OpenOptions()
	s.ShowExit()
	if s.Modal() != BattleModalExit || !s.StripBeneath() || !s.HasOptionsLayer() {
		t.Fatal("the options chain over the strip lost the strip")
	}
	if s.Back(); s.Modal() != BattleModalOptions {
		t.Fatalf("back from EXITMENU left modal %d", s.Modal())
	}
	if s.Back(); s.Modal() != BattleModalTabMenu {
		t.Fatalf("closing the options root over the strip left modal %d", s.Modal())
	}
	// Dropping the networked windows leaves an options root on top open.
	s.OpenOptions()
	s.DropSharing()
	if s.Modal() != BattleModalOptions || s.StripBeneath() {
		t.Fatal("DropSharing closed the options root or kept the strip")
	}
	if s.CloseOptions(); s.Modal() != BattleModalClosed {
		t.Fatalf("a dropped strip came back: modal %d", s.Modal())
	}
}
