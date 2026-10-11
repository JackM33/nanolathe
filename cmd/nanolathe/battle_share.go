package main

// The networked session's transfer dialog, SHARE.GUI [05 R-SHARE-01 §5]
// [07 R-HUD-03 §9]: a player list, a metal and an energy slider with their
// read-outs, the units and map-information boxes, and the confirm button,
// whose one press becomes one HumanShareGift (DESIGN_MULTIPLAYER §7.1).

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// The dialog's two resources, in the confirm's order: metal, then energy
// [05 R-SHARE-01 §5].
const (
	shareMetal = iota
	shareEnergy
	shareResources
)

// shareScreen is the open dialog: its window, the slot each list row names,
// and each slider's gadget, read-out label and range word.
type shareScreen struct {
	sharingWindow
	targets []uint8
	list    int
	slider  [shareResources]int
	readout [shareResources]int
	// rangeWord is each slider's range word, ftol of the live stock when the
	// dialog opened [07 R-HUD-03 §9].
	rangeWord [shareResources]int32
}

// openShareScreen opens the dialog, from `h` or the strip's `SHARE`, on top
// of the strip when the strip is open. Both openers need a local player
// without the watcher bit [05 R-SHARE-01 §5][07 R-CAM-01 §2]. Neither the `h`
// key nor the opener plays a cue; from the strip, the strip's `BigButton` is
// the only one [07 "Tab options menu and manual exit"].
func (b *battleSession) openShareScreen(cl *client.Client) {
	cur, ok := b.sharingFrame()
	if !ok || !cur.Sharing.MayShare {
		return
	}
	// The list offers every surviving seated slot other than the giver that
	// the receiver would admit — computers and remote players alike, enemies
	// included — in slot order [05 R-SHARE-01 §5].
	var targets []uint8
	var names []string
	for slot := range cur.Players {
		if cur.Players[slot].GiftTarget {
			targets = append(targets, uint8(slot))
			names = append(names, cur.Players[slot].Name)
		}
	}
	// With no slot to offer, retail's opener closes the dialog again before it
	// is ever composed, so nothing is seen or heard and a strip beneath stays
	// the top window [05 R-SHARE-01 §5][07 "Tab options menu and manual
	// exit"]; not opening it is the same.
	if len(targets) == 0 {
		return
	}
	w, ok := b.loadSharingWindow(shareGUI, "share", "the transfer dialog [07 R-HUD-03 §9]")
	if !ok {
		return
	}
	s := shareScreen{list: w.window.GadgetIndex("PLYRLIST"), targets: targets}
	var sliders *formats.GAFEntry
	if b.hud.common != nil {
		sliders, _ = b.hud.common.Find("SLIDERS")
	}
	for k, names := range [shareResources][2]string{{"METAL", "METAL#"}, {"ENERGY", "ENERGY#"}} {
		s.slider[k] = buildShareSlider(w.window, names[0], sliders)
		s.readout[k] = w.window.GadgetIndex(names[1])
	}
	b.finishSharingWindow(&w)
	s.sharingWindow = w
	// The fill starts the selection and the top on row 0, so a confirm with
	// no click gives to the first listed player [05 R-SHARE-01 §5].
	if s.list > 0 {
		w.panel.FillTextListAt(s.list, names, nil, b.shell.retailTextHeight())
	}
	// Each range word is ftol of that resource's live stock, the knob starts
	// at 0, and both read-outs therefore start at "0" [07 R-HUD-03 §9].
	if econ := sharingEconomy(cur); econ != nil {
		s.rangeWord[shareMetal] = numeric.TruncateFloat32ToLow32(econ.Metal)
		s.rangeWord[shareEnergy] = numeric.TruncateFloat32ToLow32(econ.Energy)
	}
	for _, i := range s.slider {
		if i > 0 {
			w.panel.SetSliderKnobAt(i, 0)
		}
	}
	b.sharing.share = s
	b.showSharingModal(ui.BattleModalShare, cl)
}

// sharingEconomy is the local seat's committed ledger row.
func sharingEconomy(cur *frame.Frame) *frame.EconomyView {
	for i := range cur.Economy {
		if cur.Economy[i].Player == cur.Sharing.Seat {
			return &cur.Economy[i]
		}
	}
	return nil
}

// buildShareSlider builds one horizontal slider as the window builder builds
// every kind-4 gadget, its two arrow buttons appended and the bar shrunk
// between them [07 R-WGT-01 §5], and then applies the dialog's override: the
// travel is the bar's width less its height, the knob a square of its height
// [07 R-WGT-01 §5][07 R-HUD-03 §9]. The opener writes it after the build, so
// the width is the shrunken bar's. It returns the bar's index, or -1.
func buildShareSlider(window *gui.Window, name string, sliders *formats.GAFEntry) int {
	i := window.GadgetIndex(name)
	if i <= 0 || window.Gadgets[i].Kind != gui.KindScrollBar {
		return -1
	}
	built, arrows := gui.BuildSlider(window.Gadgets[i], retailSliderArt(sliders, window.Gadgets[i].Rect))
	built.Range = int16(built.Rect.W - built.Rect.H)
	built.KnobSize = int16(built.Rect.H)
	built.KnobPos = 0
	window.Gadgets[i] = built
	for j := range arrows {
		arrows[j].ButtonArt = sliders
		arrows[j].ButtonArtResolved = true
	}
	window.Gadgets = append(window.Gadgets, arrows...)
	return i
}

// shareReadBack is a slider's whole read-back, `ftol(knob / (travel − 1) ×
// range)` with the knob read as a signed 16-bit word, the range a 32-bit
// integer and the quotient formed at working precision; a travel below 2
// reads 0 [07 R-HUD-03 §9][05 R-SHARE-01 §2].
func shareReadBack(knob, travel int, rangeWord int32) int32 {
	if travel < 2 {
		return 0
	}
	return numeric.TruncateFloat64ToLow32(float64(int16(knob)) / float64(travel-1) * float64(rangeWord))
}

// shareValue is resource k's current read-back.
func (s *shareScreen) shareValue(k int) int32 {
	i := s.slider[k]
	if s.panel == nil || i <= 0 || i >= len(s.window.Gadgets) {
		return 0
	}
	return shareReadBack(s.panel.SliderKnobAt(i), int(s.window.Gadgets[i].Range), s.rangeWord[k])
}

// labelText supplies the two read-outs' text: `"%d"` of each slider's
// read-back, which retail writes at open and again from each slider's change
// callback [07 R-HUD-03 §9]; reading the knob at every paint shows the same
// text. Every other label keeps its authored text.
func (s *shareScreen) labelText(index int, _ gui.Gadget) (string, bool) {
	for k := range s.readout {
		if index == s.readout[k] && index > 0 {
			return fmt.Sprintf("%d", s.shareValue(k)), true
		}
	}
	return "", false
}

// activateShareGadget is the dialog's callback
// [07 "Tab options menu and manual exit"][05 R-SHARE-01 §5]:
//
//   - `SHARUNIT` and `MAPINFO` play `Options` and keep the dialog open; the
//     widget service has already toggled the box;
//   - `OK` plays `Options`, confirms and closes the dialog;
//   - `CANCEL` plays `Previous` and closes the dialog;
//   - anything else keeps the dialog open.
//
// Closing the dialog leaves a strip beneath it on top.
func (b *battleSession) activateShareGadget(index int, cl *client.Client) {
	s := &b.sharing.share
	switch gui.CallbackName(s.window.Gadgets[index].Name) {
	case "SHARUNIT", "MAPINFO":
		b.playUICue(cl, "Options")
	case "OK":
		b.playUICue(cl, "Options")
		b.confirmShare()
		b.closeSharingTop()
	case "CANCEL":
		b.playUICue(cl, "Previous")
		b.closeSharingTop()
	}
}

// confirmShare sends one confirmation: the selected row's slot, the two
// whole read-backs, the local selection when the units box is checked and
// the map-information box [05 R-SHARE-01 §5]. The receiver runs the steps in
// retail's order — metal, energy, units, map — and judges the target. A
// confirmation with nothing to give is a no-op everywhere, so it sends
// nothing.
func (b *battleSession) confirmShare() {
	s := &b.sharing.share
	if s.panel == nil || s.list <= 0 {
		return
	}
	list := s.panel.ListAt(s.list)
	if list == nil || list.Selected() < 0 || list.Selected() >= len(s.targets) {
		return
	}
	gift := session.HumanShareGiftCommand{
		Target: s.targets[list.Selected()],
		Metal:  s.shareValue(shareMetal),
		Energy: s.shareValue(shareEnergy),
	}
	if i := s.window.GadgetIndex("SHARUNIT"); i > 0 && s.panel.StageAt(i) != 0 {
		gift.Handles = append([]pool.Handle(nil), b.selectedHandlesInSlotOrder()...)
	}
	if i := s.window.GadgetIndex("MAPINFO"); i > 0 && s.panel.StageAt(i) != 0 {
		gift.MapInfo = true
	}
	if gift.Metal == 0 && gift.Energy == 0 && len(gift.Handles) == 0 && !gift.MapInfo {
		return
	}
	_ = b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanShareGift, ShareGift: gift})
}

// drawShareControls paints the list, its bar and the two sliders, which the
// battle modal painter has no arm for, with the front end's painters
// [07 R-WGT-01 §4][07 R-WGT-01 §5].
func (h *retailBattleHUD) drawShareControls(c *client.Client, b *battleSession) {
	s := &b.sharing.share
	g := b.shell
	if s.panel == nil || g == nil {
		return
	}
	for i, gad := range s.window.Gadgets {
		if i == 0 || !s.panel.ActiveAt(i) {
			continue
		}
		r := s.window.PlacedRect(i)
		switch {
		case gad.Kind == gui.KindListBox:
			g.drawRetailList(c, s.panel, i, gad, r)
		case gad.Kind == gui.KindScrollBar && (i == s.slider[shareMetal] || i == s.slider[shareEnergy]):
			h.drawShareSlider(c, s.panel.SliderKnobAt(i), gad, r)
		case gad.Kind == gui.KindScrollBar:
			g.drawRetailScrollbar(c, s.panel, i, gad, r)
		}
	}
}

// drawShareSlider paints one built horizontal slider: the track from frame
// base, base+1 tiled and base+2, and the knob from frames base+3..5 at
// `x + 3 + knob`, kept off the bar's right end, `min(knobsize, w − 6)` long
// [07 R-WGT-01 §5 "painting"]. The arrows are appended buttons the modal
// painter has already drawn.
func (h *retailBattleHUD) drawShareSlider(c *client.Client, knob int, gad gui.Gadget, r gui.Rect) {
	if h.common == nil || r.W <= r.H {
		return
	}
	e, ok := h.common.Find("SLIDERS")
	if !ok || len(e.Frames) < 20 {
		return
	}
	const base = 10
	track0, track1, track2 := e.Frames[base].Frame, e.Frames[base+1].Frame, e.Frames[base+2].Frame
	thumb0, thumb1, thumb2 := e.Frames[base+3].Frame, e.Frames[base+4].Frame, e.Frames[base+5].Frame
	if track0 == nil || track1 == nil || track2 == nil || thumb0 == nil || thumb1 == nil || thumb2 == nil {
		return
	}
	x, y := int(r.X), int(r.Y)
	drawRetailScrollbarTrack(c, track0, track1, track2, x, y, x+int(r.W), true)
	thumbX, length := retailOptionsKnobSpan(r, knob, int(gad.KnobSize), int(thumb0.Width))
	thumbY := y + int(track0.Height)/2 - int(thumb0.Height)/2
	drawRetailScrollbarThumbClipped(c, thumb0, thumb1, thumb2, thumbX, thumbY, length, true, r)
}
