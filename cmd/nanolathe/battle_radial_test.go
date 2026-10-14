package main

import (
	"math"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/hud"
)

// radialPolar returns the point at radius r and the given angle measured
// clockwise from the top, matching the ring layout both levels draw.
func radialPolar(cx, cy int32, degFromTop, r float64) (int32, int32) {
	a := degFromTop * math.Pi / 180
	return cx + int32(math.Round(r*math.Sin(a))), cy - int32(math.Round(r*math.Cos(a)))
}

func radialTestState() buildRadialState {
	var s buildRadialState
	s.openAt(200, 200, 7, []hud.RadialGroup{
		{Category: hud.RadialMetal, Products: []string{"ARMMEX"}},
		{Category: hud.RadialEnergy, Products: []string{"ARMSOLAR", "ARMWIN"}},
		{Category: hud.RadialFactory, Products: []string{"ARMLAB"}},
	})
	return s
}

// The expert gesture: hold, flick up through Metal, keep going to the item
// ring, release — the product under the flick is selected and the menu
// closes (DESIGN_INTERFACE_HUD_INPUT §3.18).
func TestRadialHoldFlickSelects(t *testing.T) {
	s := radialTestState()
	x, y := radialPolar(200, 200, 0, 55) // Metal slot, category band
	if sel, consumed := s.step(radialPointer{X: x, Y: y}); sel != "" || !consumed || !s.open {
		t.Fatalf("category flick: sel=%q consumed=%v open=%v", sel, consumed, s.open)
	}
	if s.category() != hud.RadialMetal {
		t.Fatalf("expanded category = %v, want Metal", s.category())
	}
	x, y = radialPolar(200, 200, 0, 122) // item ring, only item at the top
	s.step(radialPointer{X: x, Y: y})
	sel, consumed := s.step(radialPointer{X: x, Y: y, MiddleReleased: true})
	if sel != "ARMMEX" || !consumed || s.open {
		t.Fatalf("release on item: sel=%q consumed=%v open=%v, want ARMMEX/true/false", sel, consumed, s.open)
	}
}

// A quick tap inside the dead-zone leaves the menu open sticky; clicks then
// choose a category and an item. Energy owns the 60-degree slot, and its
// second item sits opposite the first.
func TestRadialTapStickyClicksSelect(t *testing.T) {
	s := radialTestState()
	s.step(radialPointer{X: 200, Y: 200, MiddleReleased: true})
	if !s.open || !s.sticky {
		t.Fatalf("tap: open=%v sticky=%v, want both true", s.open, s.sticky)
	}
	x, y := radialPolar(200, 200, 60, 55)
	if sel, _ := s.step(radialPointer{X: x, Y: y, LeftPressed: true}); sel != "" || s.category() != hud.RadialEnergy {
		t.Fatalf("category click: sel=%q category=%v, want none/Energy", sel, s.category())
	}
	// Crossing the Metal slot on the way to the item ring must not
	// retarget a sticky menu away from its clicked category.
	x, y = radialPolar(200, 200, 0, 55)
	s.step(radialPointer{X: x, Y: y})
	if s.category() != hud.RadialEnergy {
		t.Fatalf("sticky crossing retargeted to %v, want Energy", s.category())
	}
	x, y = radialPolar(200, 200, 180, 122)
	sel, _ := s.step(radialPointer{X: x, Y: y, LeftPressed: true})
	if sel != "ARMWIN" || s.open {
		t.Fatalf("item click: sel=%q open=%v, want ARMWIN/false", sel, s.open)
	}
}

// Releasing a hold in the category band expands sticky instead of
// cancelling, so a two-stage pick never punishes a short flick.
func TestRadialReleaseInCategoryBandStaysOpen(t *testing.T) {
	s := radialTestState()
	x, y := radialPolar(200, 200, 120, 55) // Factory slot
	s.step(radialPointer{X: x, Y: y})
	s.step(radialPointer{X: x, Y: y, MiddleReleased: true})
	if !s.open || !s.sticky || s.category() != hud.RadialFactory {
		t.Fatalf("band release: open=%v sticky=%v category=%v, want true/true/Factory", s.open, s.sticky, s.category())
	}
}

// Escape and right-click cancel without selecting, a second middle press
// toggles a sticky menu closed, and a flick into an empty category slot
// selects nothing.
func TestRadialCancels(t *testing.T) {
	s := radialTestState()
	if sel, _ := s.step(radialPointer{X: 200, Y: 200, Escape: true}); sel != "" || s.open {
		t.Fatalf("escape: sel=%q open=%v", sel, s.open)
	}
	s = radialTestState()
	if sel, _ := s.step(radialPointer{X: 200, Y: 200, RightPressed: true}); sel != "" || s.open {
		t.Fatalf("right click: sel=%q open=%v", sel, s.open)
	}
	s = radialTestState()
	s.step(radialPointer{X: 200, Y: 200, MiddleReleased: true})
	if sel, _ := s.step(radialPointer{X: 200, Y: 200, MiddlePressed: true}); sel != "" || s.open {
		t.Fatalf("middle toggle: sel=%q open=%v", sel, s.open)
	}
	s = radialTestState()
	x, y := radialPolar(200, 200, 180, 122) // Defence slot: no such group
	s.step(radialPointer{X: x, Y: y})
	if sel, _ := s.step(radialPointer{X: x, Y: y, MiddleReleased: true}); sel != "" || s.open {
		t.Fatalf("empty slot flick: sel=%q open=%v, want none/closed", sel, s.open)
	}
}

// A category larger than one ring pages with the wheel, wrapping, and the
// second page's item is selectable at the first item angle.
func TestRadialWheelPagesItems(t *testing.T) {
	var s buildRadialState
	products := make([]string, 0, radialItemsPerPage+1)
	for i := 0; i < radialItemsPerPage+1; i++ {
		products = append(products, string(rune('A'+i)))
	}
	s.openAt(200, 200, 7, []hud.RadialGroup{{Category: hud.RadialMisc, Products: products}})
	x, y := radialPolar(200, 200, 300, 55) // Misc slot
	s.step(radialPointer{X: x, Y: y})
	if s.page != 0 || s.pages() != 2 {
		t.Fatalf("pages: page=%d pages=%d, want 0/2", s.page, s.pages())
	}
	s.step(radialPointer{X: x, Y: y, Wheel: 1})
	if s.page != 1 {
		t.Fatalf("wheel down: page=%d, want 1", s.page)
	}
	x, y = radialPolar(200, 200, 0, 122)
	s.step(radialPointer{X: x, Y: y})
	sel, _ := s.step(radialPointer{X: x, Y: y, MiddleReleased: true})
	if want := products[radialItemsPerPage]; sel != want {
		t.Fatalf("second page select = %q, want %q", sel, want)
	}
}
