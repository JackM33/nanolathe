package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// clickQuickButton presses and releases the left button on button i, through
// the same service the battle's input pass runs.
func clickQuickButton(t *testing.T, b *battleSession, i int) {
	t.Helper()
	x, y, w, h := b.quickButtonRect(i)
	var mouse input.MouseState
	mouse.SetPosition(float32(x+w/2), float32(y+h/2))
	mouse.SetButton(input.MouseButtonLeft, true)
	if !b.serviceQuickButtons(mouse) {
		t.Fatalf("button %d did not take its press", i)
	}
	mouse.ResetEdges()
	mouse.SetButton(input.MouseButtonLeft, false)
	if !b.serviceQuickButtons(mouse) {
		t.Fatalf("button %d did not take its release", i)
	}
	applyPendingBattleCommands(b)
}

func TestQuickButtonsRunTheirSelectionActions(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	at := func(n int64) numeric.Fixed { return numeric.FixedFromInt(n * 16) }
	first := placeUnit(b, "armcons", at(8), at(8))
	second := placeUnit(b, "armcons", at(12), at(8))
	solar := placeUnit(b, "armsolar", at(20), at(20))
	replaceSelectionForTest(t, b, first)

	clickQuickButton(t, b, 1) // SAME TYPE
	f, _ := b.currentSnapshot()
	selected := map[uint32]bool{}
	for _, h := range f.Selection.Handles {
		selected[uint32(h)] = true
	}
	if len(f.Selection.Handles) != 2 || !selected[uint32(second.Handle)] || selected[uint32(solar.Handle)] {
		t.Fatalf("Same type selected %v; want both constructors", f.Selection.Handles)
	}
	clickQuickButton(t, b, 0) // DESELECT
	if b.hasSelection() {
		t.Fatal("Deselect left a selection")
	}

	// This fixture compiles no unit categories, so no constructor is idle in
	// the cycle's sense: CON then keeps the selection instead of clearing it.
	replaceSelectionForTest(t, b, solar)
	clickQuickButton(t, b, 2)
	f, _ = b.currentSnapshot()
	if len(f.Selection.Handles) != 1 || f.Selection.Handles[0] != solar.Handle {
		t.Fatalf("Con with no idle constructor changed the selection to %v", f.Selection.Handles)
	}

	// A press that starts off the buttons is not theirs.
	var mouse input.MouseState
	mouse.SetPosition(300, 300)
	mouse.SetButton(input.MouseButtonLeft, true)
	if b.serviceQuickButtons(mouse) {
		t.Fatal("a world press was taken by the buttons")
	}
	// Turned off, they take nothing.
	p := b.hostPreferences()
	p.QuickButtons = 0
	b.hostPresentation = &p
	x, y, w, h := b.quickButtonRect(0)
	mouse.SetPosition(float32(x+w/2), float32(y+h/2))
	if b.serviceQuickButtons(mouse) {
		t.Fatal("hidden buttons took a press")
	}
}
