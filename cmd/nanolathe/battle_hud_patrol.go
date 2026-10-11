package main

import (
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
)

// Constructor patrol button labels are a Modern presentation policy. The
// committed unit value follows the actual bound preference, never a guessed
// Hold=Reclaim correspondence (DESIGN_INTERFACE_HUD_INPUT "Constructor patrol button labels").
func constructorPatrolCaption(f *frame.Frame) string {
	if f == nil || f.CommandPage.MoveStance > 3 || len(f.Selection.Handles) == 0 {
		return ""
	}
	choice, count, mixed := uint8(0), 0, false
	for i := range f.Units {
		u := &f.Units[i]
		if !f.Selection.Contains(u.Slot) {
			continue
		}
		if u.PatrolWork == 0 || u.PatrolWork > 3 {
			return ""
		}
		count++
		if choice != 0 && choice != u.PatrolWork {
			mixed = true
		}
		choice = u.PatrolWork
	}
	if count != len(f.Selection.Handles) {
		return ""
	}
	if mixed {
		return "Mixed"
	}
	return [...]string{"", "Reclaim", "Both", "Assist"}[choice]
}

// The stock faction plate is already blank in frame 4. The rightmost 29
// columns carry only stance indicators; the authored caption ends before them
// (DESIGN_INTERFACE_HUD_INPUT "Constructor patrol button labels"). Other art
// layouts retain the generic plate rather than applying this stock crop.
const constructorPatrolIndicatorsLeft = 84

func constructorPatrolDarkPlate(entry *formats.GAFEntry, r gui.Rect) *formats.GAFFrame {
	if entry == nil || len(entry.Frames) != 6 || r.W != 113 || r.H != 21 ||
		(!strings.EqualFold(entry.Name, "ARMMOVEORD") && !strings.EqualFold(entry.Name, "CORMOVEORD")) {
		return nil
	}
	for _, ref := range entry.Frames[:5] {
		if ref.Frame == nil || ref.Frame.Width != 113 || ref.Frame.Height != 21 {
			return nil
		}
	}
	return entry.Frames[4].Frame
}

func (h *retailBattleHUD) drawConstructorPatrolCaption(c *client.Client, r gui.Rect, text string) {
	// Leave the authored plate's three indicators clear at the right. The
	// caption is our own pen; the gadget geometry and cycle input stay retail's.
	x, width := int(r.X)+6, int(r.W)-32
	if width <= 0 {
		return
	}
	if h.modalFont != nil {
		y := int(r.Y) + (int(r.H)-retailGAFTextHeight(h.modalFont))/2
		drawRetailGAFTextClipped(c, h.modalFont, text, x, y, width, int(r.X), int(r.Y), int(r.W), int(r.H))
	} else if h.guiFont != nil {
		y := int(r.Y) + (int(r.H)-int(h.guiFont.Height))/2
		c.UITextWidthClipped(h.guiFont, text, x, y, width, h.guiColor(0), int(r.X), int(r.Y), int(r.W), int(r.H))
	}
}

func constructorPatrolFooter(f *frame.Frame, hover hud.FooterHover) (hud.Footer, bool) {
	if hover.Gadget == hud.NoGadget || commandButtonName(hover.GadgetName) != "MOVEORD" {
		return hud.Footer{}, false
	}
	caption := constructorPatrolCaption(f)
	if caption == "" {
		return hud.Footer{}, false
	}
	stance := [...]string{"Hold Position", "Maneuver", "Roam", "Mixed"}[f.CommandPage.MoveStance]
	color := hud.FooterColor{Value: hud.FooterTextColor}
	return hud.Footer{Texts: []hud.FooterText{
		{Anchor: hud.AnchorName, Text: fmt.Sprintf("Patrol work: %s   Movement: %s", caption, stance), Color: color},
		{Anchor: hud.AnchorDescription, Text: "Click or press V to cycle movement stance. Set patrol work in Options > Builders.", Color: color},
	}}, true
}

// The footer uses the resolved gadget verdict too: authored-disabled buttons
// remain hover sources in retail, but must not advertise an available cycle.
func (h *retailBattleHUD) constructorPatrolFooter(b *battleSession, f *frame.Frame, hover hud.FooterHover) (hud.Footer, bool) {
	footer, ok := constructorPatrolFooter(f, hover)
	if !ok {
		return footer, false
	}
	window, _, err := h.windowForRequired(b, f)
	if err != nil {
		h.assetErr = err
		return hud.Footer{}, false
	}
	if window == nil || hover.Gadget < 0 || hover.Gadget >= len(window.Gadgets) {
		return hud.Footer{}, false
	}
	gad := window.Gadgets[hover.Gadget]
	command, isCommand := h.sidebarGadgetVerdict(window, gad, f, commandPageIsPaged(f), b.cat)
	if !isCommand || commandButtonName(gad.Name) != "MOVEORD" || gad.Active == 0 || gad.GrayedOut&1 != 0 || command.grey || command.hidden {
		return hud.Footer{}, false
	}
	return footer, true
}
