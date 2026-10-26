package main

// Tall orders windows: host layout for side panels authored for a taller
// screen than the rail (Modern host layout policy, HUD design §3.3 "Tall
// orders windows"). TA: Escalation's GEN window is 640 rows for a 768-row
// screen: it comments out the ORDERS/BUILD pair and puts every order below
// the build area, so on a 480-row rail a non-builder showed an empty panel
// and a builder's composed pages had no way to reach the full Orders page.

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// The stock side window's tab records: two 59×19 buttons at (3, 4) and
// (65, 4) in association group 8, above build products from row 27. These
// are exactly the records Escalation's GEN window comments out.
const (
	stockTabY, stockTabH, stockTabW = 4, 19, 59
	stockTabAssoc                   = 8
	stockTabAttribs                 = 16
	stockProductTop                 = 27
)

// compactOrdersWindow packs an orders window whose controls reach below the
// rail into rows from the rail top, as the dedicated Orders page does,
// compressing only gaps. A window that fits, or one carrying product slots,
// keeps its authored layout; so does one the composition cannot fit.
func (h *retailBattleHUD) compactOrdersWindow(b *battleSession, f *frame.Frame, base *gui.Window, art *formats.GAF) *gui.Window {
	if base == nil || b == nil || b.cl == nil || f == nil {
		return nil
	}
	width, height := b.railSize()
	if !railWindowReachesBelow(base, int32(height)) {
		return nil
	}
	for i, g := range base.Gadgets {
		if i > 0 && g.Active != 0 && sidebarProductSlot(g) {
			return nil
		}
	}
	key := expandedSidebarKey{base: base, width: int32(width), height: int32(height), ordersOnly: true}
	if key == h.expandedSidebar.key && h.expandedSidebar.window != nil {
		return h.expandedSidebar.window
	}
	orders, safe := h.sidebarScaffold(base, art)
	if !safe {
		return nil
	}
	// The orders window is both sources: nothing is build-only.
	c := &sidebarProductCatalog{pages: []*sidebarCommandScaffold{nil, orders}, orders: orders}
	if !h.sidebarCombinedCommands(c) {
		return nil
	}
	spacing, fits := sidebarFitSpacing(int32(height)-128-c.upperHeight-c.lowerHeight, c.spacing)
	if !fits {
		return nil
	}
	h.sidebarPaging = sidebarRowPaging{flat: b.expandedSidebarActive()}
	window := h.composeSidebarWindow(c, height, c.commands, spacing, 128+c.upperHeight, nil, 0)
	h.expandedSidebar.key = key
	h.expandedSidebar.selection = append([]pool.Handle(nil), f.Selection.Handles...)
	return window
}

// sidebarStockTabs supplies the ORDERS/BUILD pair, in the stock placement and
// the side's own tab art, to a builder whose pages author none. Without art
// for both tabs the side gets none.
func (h *retailBattleHUD) sidebarStockTabs() []sidebarProduct {
	if !h.stockTabsChecked {
		h.stockTabsChecked = true
		prefix := sideNamePrefix(h.side)
		tab := func(command string, x int32) gui.Gadget {
			name := prefix + command
			return gui.Gadget{Kind: gui.KindButton, Name: name, Art: name, Assoc: stockTabAssoc, Attribs: stockTabAttribs, Active: 1,
				Rect: gui.Rect{X: x, Y: stockTabY, W: stockTabW, H: stockTabH, RawX: x, RawY: stockTabY}}
		}
		w := &gui.Window{Name: "stock tabs", Focus: -1, OriginY: 128, Rect: gui.Rect{Y: 128, W: 128, H: 352, RawY: 128}}
		w.Header.TotalGadgets = 2
		w.Gadgets = []gui.Gadget{{Kind: gui.KindPanel, Active: 1, Rect: w.Rect}, tab("ORDERS", 3), tab("BUILD", 65)}
		complete := true
		for _, g := range w.Gadgets[1:] {
			if h.gadgetArtEntry(g, nil) == nil {
				complete = false
			}
		}
		if complete {
			h.ensureWindowBuilt(w.Name, w, nil)
			h.stockTabs = w
		}
	}
	if h.stockTabs == nil {
		return nil
	}
	tabs := make([]sidebarProduct, 0, 2)
	for i := 1; i < len(h.stockTabs.Gadgets); i++ {
		tabs = append(tabs, sidebarProduct{source: sidebarGadgetSource{h.stockTabs, nil, i}, rect: h.stockTabs.PlacedRect(i)})
	}
	return tabs
}

// sidebarTallWindow reports an orders window authored taller than the stock
// rail, whose header ends below row 480.
func sidebarTallWindow(w *gui.Window) bool {
	return w != nil && len(w.Gadgets) > 0 && w.Rect.Y+w.Rect.H > retailScreenH
}
