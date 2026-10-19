package main

import (
	"context"
	"image"
	"image/color"
	"strconv"
	"strings"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/modfetch"
)

// This host-owned catalogue keeps the ordinary map chooser's preview intact.
// Transfer status uses the upper-right space, above the picture. The search
// box and the sort/kind/AI cycle buttons are host controls in the retail
// window's own style (docs/DESIGN_MODS_MUTATORS.md §5.6).
func buildMapsPreviewWindow(w *gui.Window) {
	for i := range w.Gadgets {
		gad := &w.Gadgets[i]
		switch gad.Name {
		case "STATUS":
			gad.Rect = gui.Rect{X: 352, Y: 86, W: 116, H: 48}
		case "STATUS2", "STATUS3":
			gad.Active = 0
		}
	}
	w.Gadgets = append(w.Gadgets,
		gui.Gadget{Name: "CATALOGUEPIC", Kind: gui.KindSurface, Active: 1, Rect: gui.Rect{X: 352, Y: 150, W: 116, H: 116}},
		// The legend and status sit under the picture, not over it, so an
		// annotated preview is never covered by its own key.
		gui.Gadget{Name: "PREVIEWSTATUS", Kind: gui.KindLabel, Active: 1, Attribs: gui.AttribInert, ColorF: 15, Rect: gui.Rect{X: 352, Y: 288, W: 116, H: 32}},
	)
	w.Gadgets = append(w.Gadgets,
		gui.Gadget{Kind: gui.KindTextBox, Name: "MAPSEARCH", SourceName: "MAPSEARCH", Active: 1,
			Attribs: 1, ColorF: 15, MaxChars: 64, Text: mapSearchHint, Rect: gui.Rect{X: 60, Y: 62, W: 230, H: 18}},
	)
	if i := w.GadgetIndex("LOAD"); i >= 0 {
		button := w.Gadgets[i]
		button.Name, button.SourceName, button.Text, button.QuickKey = "MAPTYPE", "MAPTYPE", "", 0
		button.Rect = gui.Rect{X: 357, Y: 268, W: 96, H: 18}
		w.Gadgets = append(w.Gadgets, button)
	}
}

func (state *mapsFetch) stopPreview() {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.previewSerial++
	if state.previewCancel != nil {
		state.previewCancel()
	}
}

// Called with the state lock. Each selection owns a generation, even when a
// cancelled transport completes late or the player returns to the same map.
func (state *mapsFetch) startPreviewLocked() {
	if state.selected < 0 || state.selected >= len(state.entries) {
		return
	}
	entry := state.entries[state.selected]
	key := entry.ID + "@" + entry.Version
	if entry.Preview != nil {
		key += ":" + entry.Preview.SHA256 + ":" + entry.Preview.URL
	}
	if key == state.previewKey {
		return
	}
	if state.previewCancel != nil {
		state.previewCancel()
	}
	state.previewSerial++
	serial := state.previewSerial
	state.previewKey, state.preview, state.previewPixels = key, nil, nil
	state.previewStatus = "No preview available"
	if entry.Preview == nil {
		return
	}
	state.previewStatus = "Loading preview..."
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	state.previewCancel = cancel
	c := &modfetch.Client{CatalogURL: modfetch.MapCatalogURL(), CacheDir: state.lib.Root}
	go func() {
		defer cancel()
		picture, err := c.FetchPreview(ctx, entry)
		state.finishPreview(serial, picture, err)
	}()
}

func (state *mapsFetch) finishPreview(serial uint64, picture image.Image, err error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.previewSerial != serial {
		return
	}
	state.preview, state.previewPixels, state.dirty = picture, nil, true
	switch {
	case err != nil:
		state.previewStatus = "Preview unavailable"
		state.preview = nil
	case picture == nil:
		state.previewStatus = "No preview available"
	default:
		state.previewStatus = ""
	}
}

func (g *gameShell) drawMapsPreview(c *client.Client, r gui.Rect) {
	state := mapsFetchUI
	if state == nil || c.PaletteTables() == nil {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.preview == nil {
		return
	}
	pal := c.PaletteTables().Base
	if state.previewPixels == nil || state.previewPalette != pal {
		state.previewPixels = mapPreviewIndexed(state.preview, pal, int(r.W), int(r.H))
		state.previewPalette = pal
	}
	c.UIBlitIndexed(state.previewPixels, int(r.W), int(r.H), int(r.X), int(r.Y), int(r.W), int(r.H))
}

// Preview PNG indices belong to their own palette. Match their actual RGB
// colours into the active display palette, including a mod-provided palette.
func mapPreviewIndexed(src image.Image, pal [256][4]byte, width, height int) []byte {
	if src == nil || width <= 0 || height <= 0 || src.Bounds().Empty() {
		return nil
	}
	pixels := make([]byte, width*height)
	cache := make(map[color.RGBA]byte)
	nearest := func(c color.RGBA) byte {
		if index, ok := cache[c]; ok {
			return index
		}
		best, distance := byte(0), int(^uint(0)>>1)
		for i, p := range pal {
			r, g, b := int(c.R)-int(p[0]), int(c.G)-int(p[1]), int(c.B)-int(p[2])
			d := r*r + g*g + b*b
			if d < distance {
				best, distance = byte(i), d
			}
		}
		cache[c] = best
		return best
	}
	black := nearest(color.RGBA{A: 255})
	for i := range pixels {
		pixels[i] = black
	}
	bounds := src.Bounds()
	w, h := width, height
	if bounds.Dx()*height > bounds.Dy()*width {
		h = max(1, bounds.Dy()*width/bounds.Dx())
	} else {
		w = max(1, bounds.Dx()*height/bounds.Dy())
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBAModel.Convert(src.At(bounds.Min.X+x*bounds.Dx()/w, bounds.Min.Y+y*bounds.Dy()/h)).(color.RGBA)
			pixels[(y+(height-h)/2)*width+x+(width-w)/2] = nearest(c)
		}
	}
	return pixels
}

// drawMapsLegend paints the catalogue's preview key above the picture: one
// coloured dot and its name per legend entry, in the catalogue's order
// (docs/DESIGN_MODS_MUTATORS.md §5.6). It draws nothing when the selected
// entry carries no legend or its preview is not loaded.
func (g *gameShell) drawMapsLegend(c *client.Client, r gui.Rect) {
	state := mapsFetchUI
	if state == nil || c == nil || c.PaletteTables() == nil {
		return
	}
	state.mu.Lock()
	var legend []modfetch.LegendEntry
	if state.selected >= 0 && state.selected < len(state.entries) && state.preview != nil {
		legend = state.entries[state.selected].Legend
	}
	state.mu.Unlock()
	if len(legend) == 0 {
		return
	}
	pal := c.PaletteTables().Base
	// The drawn retail font is wider than the panel's measurer reports, so the
	// two key columns are fixed: dot, label and clip width per column.
	columns := []struct{ dot, label, width int }{{0, 8, 42}, {50, 58, 58}}
	const markerSize = 6
	textHeight := g.retailTextHeight()
	// Keep the entire key above the preview's metal frame.
	textY := int(r.Y) - 15 - textHeight
	markerY := textY + (textHeight-markerSize)/2
	for i, item := range legend {
		if i >= len(columns) {
			break
		}
		rr, gg, bb, ok := parseHexColor(item.Color)
		if !ok {
			continue
		}
		column := columns[i]
		c.UIFillRect(int(r.X)+column.dot, markerY, markerSize, markerSize, paletteIndexForRGB(pal, rr, gg, bb))
		g.drawRetailStringLit(c, mapCatalogueText(item.Label), int(r.X)+column.label, textY, column.width, g.guiColor(15), 0)
	}
}

// parseHexColor reads a "#rrggbb" legend colour.
func parseHexColor(text string) (r, g, b uint8, ok bool) {
	text = strings.TrimPrefix(strings.TrimSpace(text), "#")
	if len(text) != 6 {
		return 0, 0, 0, false
	}
	value, err := strconv.ParseUint(text, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return uint8(value >> 16), uint8(value >> 8), uint8(value), true
}

// paletteIndexForRGB maps an RGB colour to the nearest display-palette index.
func paletteIndexForRGB(pal [256][4]byte, r, g, b uint8) byte {
	best, distance := byte(0), 1<<30
	for i, entry := range pal {
		dr := int(r) - int(entry[0])
		dg := int(g) - int(entry[1])
		db := int(b) - int(entry[2])
		d := dr*dr + dg*dg + db*db
		if d < distance {
			best, distance = byte(i), d
		}
	}
	return best
}
