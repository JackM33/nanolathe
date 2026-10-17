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
	s.openAt(200, 200, 200, 200, 7, []hud.RadialGroup{
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
	x, y = radialPolar(200, 200, 0, radialItemR) // the fan's only item, in Metal's own direction
	s.step(radialPointer{X: x, Y: y})
	sel, consumed := s.step(radialPointer{X: x, Y: y, MiddleReleased: true})
	if sel != "ARMMEX" || !consumed || s.open {
		t.Fatalf("release on item: sel=%q consumed=%v open=%v, want ARMMEX/true/false", sel, consumed, s.open)
	}
}

// Items fan out around their category's slot in authored order clockwise,
// radialItemStep apart, so the direction that expanded a category keeps
// naming its middle items. Every item is found at its own angle; a partial
// fan names nothing beyond its last half-step, and a full page rings the
// whole circle.
func TestRadialFanCentresOnCategory(t *testing.T) {
	for n := 1; n <= radialItemsPerPage; n++ {
		for i := 0; i < n; i++ {
			deg := radialItemAngle(hud.RadialFactory, i, n)
			if got := radialItemAt(hud.RadialFactory, n, deg); got != i {
				t.Fatalf("n=%d item %d at %.1f degrees resolves to %d", n, i, deg, got)
			}
		}
	}
	slot := radialSlotAngle(hud.RadialFactory) // 120
	if got := radialItemAngle(hud.RadialFactory, 1, 3); got != slot {
		t.Fatalf("middle of three sits at %.1f, want the slot's %.1f", got, slot)
	}
	if got := radialItemAt(hud.RadialFactory, 3, slot+1.5*radialItemStep+1); got != -1 {
		t.Fatalf("beyond a partial fan resolves to %d, want -1", got)
	}
	if got := radialItemAt(hud.RadialFactory, radialItemsPerPage, slot+180); got < 0 {
		t.Fatalf("opposite a full page resolves to %d, want an item", got)
	}
}

// A quick tap inside the dead-zone leaves the menu open sticky; clicks then
// choose a category and an item. Energy owns the 60-degree slot and fans
// its two items either side of it.
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
	x, y = radialPolar(200, 200, radialItemAngle(hud.RadialEnergy, 1, 2), radialItemR)
	sel, _ := s.step(radialPointer{X: x, Y: y, LeftPressed: true})
	if sel != "ARMWIN" || s.open {
		t.Fatalf("item click: sel=%q open=%v, want ARMWIN/false", sel, s.open)
	}
}

// A held flick picks by direction alone, so one that overshoots the ring
// still selects; a sticky click that far out dismisses instead.
func TestRadialOvershoot(t *testing.T) {
	s := radialTestState()
	x, y := radialPolar(200, 200, 120, 55) // Factory slot
	s.step(radialPointer{X: x, Y: y})
	x, y = radialPolar(200, 200, 120, 3*radialItemR)
	s.step(radialPointer{X: x, Y: y})
	if sel, _ := s.step(radialPointer{X: x, Y: y, MiddleReleased: true}); sel != "ARMLAB" || s.open {
		t.Fatalf("held overshoot: sel=%q open=%v, want ARMLAB/closed", sel, s.open)
	}
	s = radialTestState()
	s.step(radialPointer{X: 200, Y: 200, MiddleReleased: true})
	x, y = radialPolar(200, 200, 120, 55)
	s.step(radialPointer{X: x, Y: y, LeftPressed: true})
	x, y = radialPolar(200, 200, 120, radialDismissR+10)
	if sel, _ := s.step(radialPointer{X: x, Y: y, LeftPressed: true}); sel != "" || s.open {
		t.Fatalf("sticky click far out: sel=%q open=%v, want none/closed", sel, s.open)
	}
}

// A menu shifted away from a viewport edge opens with the pointer off its
// centre. Until the pointer moves, that offset is no gesture: a tap leaves
// the menu sticky rather than picking whatever lay under the press.
func TestRadialShiftedOpenTapIsNoPick(t *testing.T) {
	var s buildRadialState
	px, py := radialPolar(200, 200, 60, radialItemR) // Energy's item ring
	s.openAt(200, 200, px, py, 7, radialTestState().groups)
	s.step(radialPointer{X: px + 2, Y: py - 1}) // press jitter
	if s.hoverCat != -1 || s.hoverItem != -1 {
		t.Fatalf("unmoved press hovers cat=%d item=%d, want none", s.hoverCat, s.hoverItem)
	}
	if sel, _ := s.step(radialPointer{X: px, Y: py, MiddleReleased: true}); sel != "" || !s.open || !s.sticky {
		t.Fatalf("shifted tap: sel=%q open=%v sticky=%v, want none/open/sticky", sel, s.open, s.sticky)
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
	x, y := radialPolar(200, 200, 180, radialItemR) // Defence slot: no such group
	s.step(radialPointer{X: x, Y: y})
	if sel, _ := s.step(radialPointer{X: x, Y: y, MiddleReleased: true}); sel != "" || s.open {
		t.Fatalf("empty slot flick: sel=%q open=%v, want none/closed", sel, s.open)
	}
}

// A category larger than one ring pages with the wheel, wrapping, and the
// second page's lone item sits in the category's own direction.
func TestRadialWheelPagesItems(t *testing.T) {
	var s buildRadialState
	products := make([]string, 0, radialItemsPerPage+1)
	for i := 0; i < radialItemsPerPage+1; i++ {
		products = append(products, string(rune('A'+i)))
	}
	s.openAt(200, 200, 200, 200, 7, []hud.RadialGroup{{Category: hud.RadialMisc, Products: products}})
	x, y := radialPolar(200, 200, 300, 55) // Misc slot
	s.step(radialPointer{X: x, Y: y})
	if s.page != 0 || s.pages() != 2 {
		t.Fatalf("pages: page=%d pages=%d, want 0/2", s.page, s.pages())
	}
	s.step(radialPointer{X: x, Y: y, Wheel: 1})
	if s.page != 1 {
		t.Fatalf("wheel down: page=%d, want 1", s.page)
	}
	x, y = radialPolar(200, 200, 300, radialItemR)
	s.step(radialPointer{X: x, Y: y})
	sel, _ := s.step(radialPointer{X: x, Y: y, MiddleReleased: true})
	if want := products[radialItemsPerPage]; sel != want {
		t.Fatalf("second page select = %q, want %q", sel, want)
	}
}

// The glass is drawn as row spans of shade, and a pixel covered twice would
// be faded twice. Each span set must cover exactly the sector's pixels —
// radius within r0..r1, direction clockwise from a0 to a1 held gap pixels
// off both edges — once each, for narrow, reflex and whole sweeps.
func TestRadialSectorSpansCoverOnce(t *testing.T) {
	cases := []struct{ r0, r1, a0, a1, gap float64 }{
		{0, 30, 0, 360, 0},
		{33, 84, -30, 30, 1},
		{33, 84, 210, 270, 1},
		{88, 160, 6, 114, 0},
		{88, 160, 150, 330, 1},
		{88, 160, 300, 590, 1}, // reflex, across the top
		{26, 30, 80, 108, 0},
	}
	for _, tc := range cases {
		hits := map[[2]int]int{}
		radialSectorSpans(0, 0, tc.r0, tc.r1, tc.a0, tc.a1, tc.gap, func(x0, x1, y int) {
			for x := x0; x <= x1; x++ {
				hits[[2]int{x, y}]++
			}
		})
		d0x, d0y := radialDir(tc.a0)
		d1x, d1y := radialDir(tc.a1)
		n := int(tc.r1) + 1
		for y := -n; y <= n; y++ {
			for x := -n; x <= n; x++ {
				fx, fy := float64(x), float64(y)
				rr := fx*fx + fy*fy
				in := rr >= tc.r0*tc.r0 && rr <= tc.r1*tc.r1
				if in && tc.a1-tc.a0 < 360 {
					c0 := d0x*fy-d0y*fx >= tc.gap-1e-9
					c1 := fx*d1y-fy*d1x >= tc.gap-1e-9
					if tc.a1-tc.a0 > 180 {
						in = c0 || c1
					} else {
						in = c0 && c1
					}
				}
				want := 0
				if in {
					want = 1
				}
				if got := hits[[2]int{x, y}]; got != want {
					t.Fatalf("sector %+v pixel (%d,%d) covered %d times, want %d", tc, x, y, got, want)
				}
			}
		}
	}
}
