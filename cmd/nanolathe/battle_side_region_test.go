package main

import "testing"

// Hiding the minimap raises the side rail by the radar rows, so a doubled
// rail on a 704-row surface lays out as a 480-row rail, while the strips keep
// their place and the radar answers no hit (docs/ANDROID.md §2).
func TestHiddenMinimapRaisesSideRail(t *testing.T) {
	b := &battleSession{chromeK: 2, sideOffsetY: -sideRadarRows * 2, hud: &retailBattleHUD{minimapAnchorOK: true, minimapHidden: true}}
	if _, h := b.sideRegion().VirtualSize(1564, 704); h != 480 {
		t.Fatalf("side rail virtual height %d; want 480", h)
	}
	if r := b.railRegion(); r.OffsetY != 0 {
		t.Fatalf("strip region offset %d; want 0", r.OffsetY)
	}
	if x, y := b.sidePointer(10, 0); x != 5 || y != sideRadarRows {
		t.Fatalf("top-left tap maps to (%d,%d); want (5,%d)", x, y, sideRadarRows)
	}
	if _, ok := b.hud.minimapRect(); ok {
		t.Fatal("a hidden minimap still has a rectangle")
	}
}
