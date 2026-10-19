package main

import (
	"image"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/gui"
)

// The preview keeps its aspect inside the dialog's picture slot — the placed
// gadget rect, including the authored window's origin — and follows the
// canvas transform the app scaled the authored canvas with.
func TestMapPreviewPlacement(t *testing.T) {
	// The SELMAP window sits at (84,12); CATALOGUEPIC is authored at
	// (352,150,116,116), so its placed rect is (436,162,116,116).
	slot := gui.Rect{X: 436, Y: 162, W: 116, H: 116}
	square := image.Rect(0, 0, 232, 232)
	x, y, w, h := mapPreviewPlacement(square, slot, 1, 0, 0)
	if x != float64(slot.X) || y != float64(slot.Y) || w != float64(slot.W) || h != float64(slot.H) {
		t.Fatalf("square at scale 1 = %v %v %v %v", x, y, w, h)
	}
	// A wide picture fits the slot's width and is centred vertically.
	wide := image.Rect(0, 0, 232, 116)
	x, y, w, h = mapPreviewPlacement(wide, slot, 1, 0, 0)
	if w != float64(slot.W) || h != float64(slot.H)/2 || y != float64(slot.Y)+float64(slot.H)/4 {
		t.Fatalf("wide = %v %v %v %v", x, y, w, h)
	}
	// Scale 2 with a letterbox offset lands on the same logical rectangle in
	// device pixels.
	x, y, w, h = mapPreviewPlacement(square, slot, 2, 40, 20)
	if x != 40+2*float64(slot.X) || y != 20+2*float64(slot.Y) || w != 2*float64(slot.W) || h != 2*float64(slot.H) {
		t.Fatalf("square at scale 2 = %v %v %v %v", x, y, w, h)
	}
	// Degenerate input draws nothing.
	if x, y, w, h = mapPreviewPlacement(image.Rect(0, 0, 0, 0), slot, 1, 0, 0); x != 0 || y != 0 || w != 0 || h != 0 {
		t.Fatalf("empty bounds = %v %v %v %v", x, y, w, h)
	}
	if x, y, w, h = mapPreviewPlacement(square, gui.Rect{}, 1, 0, 0); x != 0 || y != 0 || w != 0 || h != 0 {
		t.Fatalf("empty slot = %v %v %v %v", x, y, w, h)
	}
}
