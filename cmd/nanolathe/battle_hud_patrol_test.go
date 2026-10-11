package main

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func TestConstructorPatrolCaptionAndHover(t *testing.T) {
	f := &frame.Frame{Units: []frame.UnitView{{Slot: 1, PatrolWork: 2}, {Slot: 2, PatrolWork: 2}}, Selection: frame.SelectionView{Handles: []pool.Handle{1, 2}}}
	hover := hud.FooterHover{Gadget: 4, GadgetName: "ARMMOVEORD"}
	for stance, name := range []string{"Hold Position", "Maneuver", "Roam", "Mixed"} {
		f.CommandPage.MoveStance = uint8(stance)
		if got := constructorPatrolCaption(f); got != "Both" {
			t.Fatalf("stance%d caption=%q", stance, got)
		}
		footer, ok := constructorPatrolFooter(f, hover)
		if !ok || !strings.Contains(footer.Texts[0].Text, name) || !strings.Contains(footer.Texts[1].Text, "cycle movement stance") {
			t.Fatalf("hover=%+v", footer)
		}
	}
	for choice, want := range []string{"", "Reclaim", "Both", "Assist"} {
		f.Units[0].PatrolWork, f.Units[1].PatrolWork = uint8(choice), uint8(choice)
		if got := constructorPatrolCaption(f); got != want {
			t.Fatalf("choice%d caption=%q want%q", choice, got, want)
		}
	}
	f.Units[0].PatrolWork = 1
	if got := constructorPatrolCaption(f); got != "Mixed" {
		t.Fatalf("differing preferences caption=%q", got)
	}
	f.Units[0].PatrolWork = 0
	if constructorPatrolCaption(f) != "" {
		t.Fatal("non-patrol selection changed stock button")
	}
	f.Units[0].PatrolWork = 3
	f.Selection.Handles = append(f.Selection.Handles, 3)
	if constructorPatrolCaption(f) != "" {
		t.Fatal("stale selection changed stock button")
	}
	f.Selection.Handles = f.Selection.Handles[:2]
	f.CommandPage.MoveStance = 4
	if constructorPatrolCaption(f) != "" {
		t.Fatal("disabled movement control changed stock button")
	}
	f.CommandPage.MoveStance = 0
	hover.GadgetName = "CORPATROL"
	if _, ok := constructorPatrolFooter(f, hover); ok {
		t.Fatal("patrol command received movement hover")
	}
	hover.GadgetName, hover.Gadget = "CORMOVEORD", hud.NoGadget
	if _, ok := constructorPatrolFooter(f, hover); ok {
		t.Fatal("absent hover received help")
	}
	f.Selection.Handles = nil
	if constructorPatrolCaption(f) != "" || constructorPatrolCaption(nil) != "" {
		t.Fatal("empty selection received a patrol caption")
	}
}

func TestConstructorPatrolFooterRespectsAuthoredDisabledGadget(t *testing.T) {
	f := &frame.Frame{Units: []frame.UnitView{{Slot: 1, PatrolWork: 2}}, Selection: frame.SelectionView{Handles: []pool.Handle{1}}}
	window := &gui.Window{Gadgets: []gui.Gadget{{}, {Kind: gui.KindButton, Active: 1, Name: "ARMMOVEORD", Rect: gui.Rect{W: 113, H: 21}}}}
	h := &retailBattleHUD{fs: vfs.New(), windows: map[string]*gui.Window{"gen": window}}
	b := &battleSession{hud: h, cat: &content.Catalog{}}
	hover := hud.FooterHover{Gadget: 1, GadgetName: "ARMMOVEORD"}
	if _, ok := h.constructorPatrolFooter(b, f, hover); !ok {
		t.Fatal("enabled button missing patrol help")
	}
	window.Gadgets[1].GrayedOut = 1
	if _, ok := h.constructorPatrolFooter(b, f, hover); ok {
		t.Fatal("authored-disabled button advertised a cycle")
	}
	window.Gadgets[1].GrayedOut = 0
	window.Gadgets[1].Active = 0
	if _, ok := h.constructorPatrolFooter(b, f, hover); ok {
		t.Fatal("inactive button advertised a cycle")
	}
}
