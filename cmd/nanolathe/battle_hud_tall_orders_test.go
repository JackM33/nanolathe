package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
)

// tallOrdersFixture reshapes the expanded fixture's windows the way TA:
// Escalation authors them: a 640-row panel from row 128 with no ORDERS/BUILD
// pair and every order below the build area (HUD §3.3 "Tall orders windows").
func tallOrdersFixture(t *testing.T) (*battleSession, []*gui.Window) {
	t.Helper()
	b, _, sources := expandedSidebarFixture(t)
	for _, w := range sources {
		kept := w.Gadgets[:0]
		for _, g := range w.Gadgets {
			if command := commandButtonName(g.Name); command != "ORDERS" && command != "BUILD" {
				kept = append(kept, g)
			}
		}
		w.Gadgets = kept
		w.Rect = gui.Rect{Y: 128, W: 128, H: 640, RawY: 128}
		w.Gadgets[0].Rect = w.Rect
		for i := range w.Gadgets {
			switch w.Gadgets[i].Name {
			case "REPAIR":
				w.Gadgets[i].Rect.Y = 511
			case "CAPTURE":
				w.Gadgets[i].Rect.X, w.Gadgets[i].Rect.Y = 66, 511
			case "MOVE":
				w.Gadgets[i].Rect.Y = 543
			case "ATTACK":
				w.Gadgets[i].Rect.Y = 607
			}
		}
	}
	return b, sources
}

func requireReachable(t *testing.T, w *gui.Window, height int32, names ...string) {
	t.Helper()
	for _, name := range names {
		i := expandedIndex(t, w, name)
		r := w.PlacedRect(i)
		if w.Gadgets[i].Active == 0 || r.Y < 128 || r.Y+r.H > height || w.HitTest(r.X+r.W/2, r.Y+r.H/2) != i {
			t.Fatalf("%s is not reachable on a %d-row rail: %+v", name, height, r)
		}
	}
}

func TestTallOrdersWindowPacksIntoTheRail(t *testing.T) {
	b, sources := tallOrdersFixture(t)
	orders := sources[2]
	// A non-builder (or several units) opens the side's orders window.
	paletteCallbackFrame(t, b, func(f *frame.Frame) { f.CommandPage = frame.CommandPageView{} })
	b.cl.Resize(1280, 480)
	w := expandedWindow(t, b)
	if w == orders {
		t.Fatal("a 640-row orders window was shown as authored on a 480-row rail")
	}
	requireReachable(t, w, 480, "REPAIR", "CAPTURE", "MOVE", "ATTACK")
	if _, active := b.hud.expandedSidebarPaging(b, f0(t, b)); active {
		t.Fatal("compacted orders took over build-page navigation")
	}
	// Where the authored window fits, it stays exactly as authored.
	b.cl.Resize(1280, 1080)
	if expandedWindow(t, b) != orders {
		t.Fatal("a fitting orders window was recomposed")
	}
}

func TestStockOrdersWindowIsNeverCompacted(t *testing.T) {
	b, _, sources := expandedSidebarFixture(t)
	paletteCallbackFrame(t, b, func(f *frame.Frame) { f.CommandPage = frame.CommandPageView{} })
	b.cl.Resize(640, 480)
	if expandedWindow(t, b) != sources[2] {
		t.Fatal("the stock 352-row orders window was recomposed")
	}
}

func TestTallOrdersBuilderGetsStockTabs(t *testing.T) {
	for _, art := range []bool{true, false} {
		b, _ := tallOrdersFixture(t)
		b.hud.common = &formats.GAF{}
		if art {
			b.hud.common.Entries = []formats.GAFEntry{{Name: "ORDERS"}, {Name: "BUILD"}}
		}
		b.cl.Resize(1280, 480)
		w := expandedWindow(t, b)
		has := func(command string) int {
			for i, g := range w.Gadgets {
				if commandButtonName(g.Name) == command && g.Active != 0 {
					return i
				}
			}
			return -1
		}
		if !art {
			if has("ORDERS") >= 0 || has("BUILD") >= 0 {
				t.Fatal("tabs were supplied without the side's tab art")
			}
			continue
		}
		requireReachable(t, w, 480, "ORDERS", "BUILD")
		if len(sidebarVisibleProducts(w)) == 0 {
			t.Fatal("the tabbed build page shows no products")
		}
		paletteCallbackClick(t, b, b.cl, w, has("ORDERS"), false, false)
		w = expandedWindow(t, b)
		if b.hud.sidebarPaging.state.Page != 0 {
			t.Fatal("ORDERS did not open the Orders page")
		}
		requireReachable(t, w, 480, "REPAIR", "CAPTURE", "MOVE", "ATTACK", "BUILD")
		paletteCallbackClick(t, b, b.cl, w, has("BUILD"), false, false)
		if b.hud.sidebarPaging.state.Page == 0 || len(sidebarVisibleProducts(expandedWindow(t, b))) == 0 {
			t.Fatal("BUILD did not return to the build page")
		}
	}
}

func f0(t *testing.T, b *battleSession) *frame.Frame {
	t.Helper()
	f, ok := b.currentSnapshot()
	if !ok {
		t.Fatal("no committed frame")
	}
	return f
}
