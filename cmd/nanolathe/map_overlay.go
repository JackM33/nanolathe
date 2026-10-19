package main

// The map browser's preview is host content, so it draws at the display's own
// resolution: the retail dialog stays authored 640×480, scaled as always, and
// the minimap keeps its full detail at larger windows
// (internal/platform/ebitenapp/overlay.go).

import (
	"image"
	"math"
	"sync"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
)

// The picture slot in the More maps dialog is the CATALOGUEPIC gadget's
// placed rect: the authored window's own origin is part of it, exactly as the
// canvas draw path uses it.
func mapPreviewSlot() (gui.Rect, bool) {
	panel := mapsFetchPanel
	if panel == nil || panel.Window == nil {
		return gui.Rect{}, false
	}
	index := panel.Index("CATALOGUEPIC")
	if index < 0 {
		return gui.Rect{}, false
	}
	return panel.Window.PlacedRect(index), true
}

// mapsPreviewOverlay draws the selected preview above the scaled canvas.
type mapsPreviewOverlay struct {
	shell func() *gameShell

	mu     sync.Mutex
	image  *ebiten.Image
	serial uint64
}

// mapPreviewState is the preview and its generation, under the fetch state's
// own lock.
func mapPreviewState() (image.Image, uint64) {
	state := mapsFetchUI
	if state == nil {
		return nil, 0
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.preview, state.previewSerial
}

func (o *mapsPreviewOverlay) Active() bool {
	if shell := o.shell(); shell == nil || !shell.mapsFetchActive() {
		return false
	}
	preview, _ := mapPreviewState()
	return preview != nil
}

func (o *mapsPreviewOverlay) Draw(screen *ebiten.Image, scale, offsetX, offsetY float64) {
	preview, serial := mapPreviewState()
	slot, ok := mapPreviewSlot()
	if preview == nil || !ok || screen == nil {
		return
	}
	o.mu.Lock()
	if o.image == nil || o.serial != serial {
		o.image = ebiten.NewImageFromImage(preview)
		o.serial = serial
	}
	img := o.image
	o.mu.Unlock()
	if img == nil {
		return
	}
	x, y, width, height := mapPreviewPlacement(preview.Bounds(), slot, scale, offsetX, offsetY)
	opts := &ebiten.DrawImageOptions{}
	bounds := preview.Bounds()
	opts.GeoM.Scale(width/float64(bounds.Dx()), height/float64(bounds.Dy()))
	opts.GeoM.Translate(x, y)
	opts.Filter = ebiten.FilterLinear
	screen.DrawImage(img, opts)
}

// mapPreviewPlacement fits a preview into the dialog's picture slot,
// preserving its aspect, and maps the result through the canvas transform the
// app scaled the authored canvas with.
func mapPreviewPlacement(bounds image.Rectangle, slot gui.Rect, scale, offsetX, offsetY float64) (x, y, width, height float64) {
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 || scale <= 0 || slot.W <= 0 || slot.H <= 0 {
		return 0, 0, 0, 0
	}
	fit := math.Min(float64(slot.W)/float64(bounds.Dx()), float64(slot.H)/float64(bounds.Dy()))
	width = float64(bounds.Dx()) * fit * scale
	height = float64(bounds.Dy()) * fit * scale
	x = offsetX + (float64(slot.X)+(float64(slot.W)-float64(bounds.Dx())*fit)/2)*scale
	y = offsetY + (float64(slot.Y)+(float64(slot.H)-float64(bounds.Dy())*fit)/2)*scale
	return x, y, width, height
}
