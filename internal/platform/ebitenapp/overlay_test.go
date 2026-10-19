package ebitenapp

import "testing"

// The device-pixel cursor maps back onto the authored canvas through the same
// transform the canvas was scaled with, so pointer input stays in logical
// pixels while the native overlay owns the layout.
func TestLogicalPointerMapsDeviceCursorToCanvas(t *testing.T) {
	a := &app{overlayScale: 2, overlayOffsetX: 40, overlayOffsetY: 20}
	if x, y := a.logicalPointer(40+2*100, 20+2*50); x != 100 || y != 50 {
		t.Fatalf("logical = %d,%d", x, y)
	}
	// Without an overlay the cursor passes through untouched.
	plain := &app{}
	if x, y := plain.logicalPointer(123, 45); x != 123 || y != 45 {
		t.Fatalf("passthrough = %d,%d", x, y)
	}
}
