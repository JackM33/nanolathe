package main

// Drawing for the optional build radial (DESIGN_INTERFACE_HUD_INPUT
// §3.18). The menu is recorded into the same indexed battle HUD list as
// the rest of the chrome, after the world and side page and before any
// modal window, so the Classic and Enhanced executors present the same
// picture. Icons are the authored build-menu gadget pictures resolved
// through the sidebar's own lookup chain; no art is synthesized and no
// committed state is read beyond the menu's opening snapshot [I6].

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
)

// radialPoint is the ring position at the given clockwise-from-top angle.
func radialPoint(cx, cy int32, degFromTop float64, r int) (int, int) {
	a := degFromTop * math.Pi / 180
	return int(cx) + int(math.Round(float64(r)*math.Sin(a))),
		int(cy) - int(math.Round(float64(r)*math.Cos(a)))
}

// radialProductFrame resolves one product's gadget picture the way the
// sidebar does: a downloaded product's own gadget GAF first (cached on
// the HUD), then the builder's authored page GAFs — where the stock
// corpus keeps its build-menu pictures — then the shared interface GAFs
// [fmt gaf][07 R-HUD-03 §6].
func (h *retailBattleHUD) radialProductFrame(product, builder string, pages int) *formats.GAFFrame {
	if h == nil || product == "" {
		return nil
	}
	var entry *formats.GAFEntry
	if gaf := h.generatedProductGAF(product); gaf != nil {
		entry, _ = gaf.Find(product)
	}
	if entry == nil && builder != "" {
		base := strings.ToLower(strings.TrimSpace(builder))
		last := pages - 1 // the count includes the orders page 0
		if last < 1 {
			last = 1
		}
		for page := 1; page <= last && entry == nil; page++ {
			if gaf := h.resolvePageArt(base + strconv.Itoa(page)); gaf != nil {
				entry, _ = gaf.Find(product)
			}
		}
	}
	if entry == nil {
		entry = h.gadgetArtEntry(gui.Gadget{Kind: gui.KindButton, Name: product}, nil)
	}
	if entry == nil {
		return nil
	}
	for _, ref := range entry.Frames {
		if ref.Frame != nil {
			return ref.Frame
		}
	}
	return nil
}

func (h *retailBattleHUD) radialText(c *client.Client, text string, cx, y int) {
	drawRetailGAFText(c, h.modalFont, text, cx-retailGAFTextWidth(h.modalFont, text)/2, y, -1)
}

func (h *retailBattleHUD) drawBuildRadial(c *client.Client, b *battleSession) {
	if h == nil || c == nil || b == nil || !b.radial.open {
		return
	}
	s := &b.radial
	// A four-frame ease out from the press point; purely cosmetic, driven
	// by serviced host frames so a paused battle still animates.
	ease := 1.0
	if s.age < 4 {
		ease = 0.72 + 0.28*float64(s.age)/4
	}
	catR := int(float64(radialCategoryR) * ease)
	itemR := int(float64(radialItemR) * ease)
	cx, cy := int(s.cx), int(s.cy)

	// Dim the field under the rings so gadget art stays legible over
	// bright terrain, as a square the rings inscribe.
	extent := radialItemR + 36
	c.UIShadeRect(h.pal, cx-extent, cy-extent, 2*extent, 2*extent, -18)
	c.UIFrameRect(cx-extent, cy-extent, 2*extent, 2*extent, h.guiColor(17))

	textH := retailGAFTextHeight(h.modalFont)
	expanded := s.groupIndex()

	// The expanded category's current page of gadget pictures, authored
	// order from the top. Items paint before the category plates so a
	// plate the ring overlaps stays readable.
	items := s.pageItems()
	for i, product := range items {
		px, py := radialPoint(s.cx, s.cy, float64(i)*(360/float64(len(items))), itemR)
		c.UIFillRect(px-32, py-32, 64, 64, h.guiColor(20))
		var name string
		if b.cat != nil {
			if def, ok := b.cat.Unit(product); ok && def != nil {
				name = def.UnitName
			}
		}
		if name == "" {
			name = product
		}
		if f := h.radialProductFrame(name, s.builderName, s.builderPages); f != nil {
			c.UIBlit(f, px-int(f.Width)/2, py-int(f.Height)/2)
		} else {
			h.radialText(c, name, px, py-textH/2)
		}
		frame := h.guiColor(17)
		if i == s.hoverItem {
			frame = h.guiColor(15)
			c.UIFrameRect(px-34, py-34, 68, 68, frame)
		}
		c.UIFrameRect(px-32, py-32, 64, 64, frame)
	}

	// One labelled plate per offered category at its fixed slot; the
	// expanded one is framed bright.
	for gi, g := range s.groups {
		px, py := radialPoint(s.cx, s.cy, float64(g.Category)*(360.0/6), catR)
		label := g.Category.Label()
		if len(g.Products) > radialItemsPerPage && gi == expanded {
			label = fmt.Sprintf("%s %d/%d", label, s.page+1, s.pages())
		}
		w := retailGAFTextWidth(h.modalFont, label) + 12
		hh := textH + 8
		c.UIFillRect(px-w/2, py-hh/2, w, hh, h.guiColor(20))
		frame := h.guiColor(17)
		if gi == expanded {
			frame = h.guiColor(15)
		}
		c.UIFrameRect(px-w/2, py-hh/2, w, hh, frame)
		h.radialText(c, label, px, py-textH/2)
	}

	// Hub: the hovered product's name and build cost, or the expanded
	// category, so a sticky user always has a caption at the centre.
	hub := ""
	sub := ""
	if s.hoverItem >= 0 && s.hoverItem < len(items) && b.cat != nil {
		if def, ok := b.cat.Unit(items[s.hoverItem]); ok && def != nil {
			hub = def.Name
			if hub == "" {
				hub = def.UnitName
			}
			sub = fmt.Sprintf("%dM  %dE", int(def.BuildCostMetal), int(def.BuildCostEnergy))
		}
	}
	if hub == "" {
		if g, ok := s.group(); ok {
			hub = g.Category.Label()
		} else {
			hub = "Build"
		}
	}
	hubW := retailGAFTextWidth(h.modalFont, hub)
	if sub != "" {
		hubW = max(hubW, retailGAFTextWidth(h.modalFont, sub))
	}
	hubH := textH + 6
	if sub != "" {
		hubH += textH + 2
	}
	c.UIFillRect(cx-hubW/2-6, cy-hubH/2, hubW+12, hubH, h.guiColor(20))
	c.UIFrameRect(cx-hubW/2-6, cy-hubH/2, hubW+12, hubH, h.guiColor(17))
	h.radialText(c, hub, cx, cy-hubH/2+3)
	if sub != "" {
		h.radialText(c, sub, cx, cy-hubH/2+5+textH)
	}
}
