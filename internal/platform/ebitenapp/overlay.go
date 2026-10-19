package ebitenapp

import (
	"image/color"
	"math"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
)

// NativeOverlay is a host-owned layer drawn at the display's own pixel size
// above the authored canvas: the map browser's minimap preview keeps its full
// detail at larger windows while the retail menu stays 640×480. While the
// overlay is active the app lays the window out at the device resolution,
// scales the authored canvas into it itself, and hands the overlay the same
// transform, so a logical rectangle lands on its device pixels. A full screen
// supersedes the overlay, and the battle surface is untouched.
type NativeOverlay interface {
	// Active reports whether the overlay owns a layer this frame.
	Active() bool
	// Draw paints the overlay into the device-resolution screen. scale and the
	// offsets are the transform the authored canvas was drawn with.
	Draw(screen *ebiten.Image, scale, offsetX, offsetY float64)
}

// overlayActive reports whether the native overlay draws this frame.
func (a *app) overlayActive() bool {
	return a.options.NativeOverlay != nil && !a.screenActive() && a.options.NativeOverlay.Active()
}

// layoutOverlay lays the window out at the display resolution and records the
// canvas transform, mirroring a full screen's own layout.
func (a *app) layoutOverlay(outsideWidth, outsideHeight int) (int, int) {
	scale := a.screenScale.get(outsideWidth, outsideHeight, time.Now())
	deviceW := max(1, int(float64(outsideWidth)*scale))
	deviceH := max(1, int(float64(outsideHeight)*scale))
	if w, h := a.c.Size(); w > 0 && h > 0 {
		a.overlayScale = math.Min(float64(deviceW)/float64(w), float64(deviceH)/float64(h))
		a.overlayOffsetX = (float64(deviceW) - float64(w)*a.overlayScale) / 2
		a.overlayOffsetY = (float64(deviceH) - float64(h)*a.overlayScale) / 2
	}
	nativeScroll.viewport(deviceW, deviceH, outsideWidth, outsideHeight)
	a.layoutW, a.layoutH = deviceW, deviceH
	return deviceW, deviceH
}

// logicalPointer maps a device-pixel cursor back to the authored canvas while
// the overlay owns the layout.
func (a *app) logicalPointer(x, y int) (int, int) {
	if a.overlayScale <= 0 {
		return x, y
	}
	return int((float64(x) - a.overlayOffsetX) / a.overlayScale), int((float64(y) - a.overlayOffsetY) / a.overlayScale)
}

// drawOverlayCanvas presents the authored canvas into the device-resolution
// screen: the letterbox is filled, the canvas is scaled with the recorded
// transform, and the overlay draws above it.
func (a *app) drawOverlayCanvas(screen *ebiten.Image, img *ebiten.Image) {
	screen.Fill(color.Black)
	opts := &ebiten.DrawImageOptions{}
	opts.GeoM.Scale(a.overlayScale, a.overlayScale)
	opts.GeoM.Translate(a.overlayOffsetX, a.overlayOffsetY)
	opts.Filter = ebiten.FilterLinear
	screen.DrawImage(img, opts)
	if a.options.NativeOverlay != nil {
		a.options.NativeOverlay.Draw(screen, a.overlayScale, a.overlayOffsetX, a.overlayOffsetY)
	}
}
