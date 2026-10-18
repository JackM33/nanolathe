package main

// Chosen start positions on the skirmish setup screen
// (docs/DESIGN_INTERFACE_HUD_INPUT.md §2.6 "Start positions",
// docs/DESIGN_SESSIONS_AI_SAVE.md "Chosen start positions"). Each live row
// has a Start button beside the player box that names the map's StartPos<n>
// the player begins on, or Auto to leave it to the Location setting; the map
// picker's preview numbers the starts so the choice can be read.

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

const (
	skirmishStartSource = "NANOLATHE_SKIRMISH_START"
	skirmishStartPrefix = "StartPos"
	// skirmishStartX puts the column in the free strip between the player
	// box (which ends near x 395) and the option column at x 476.
	skirmishStartX = 400
	// skirmishStartHeadingY is the baked column headings' line.
	skirmishStartHeadingY = 56
)

// installSkirmishStartColumn appends each row's Start button, a copy of the
// row's Metal button (the same 46-pixel art) moved beside the box, and a
// heading cloned from the Location label. It runs after the row builder, so
// the rows already have their places.
func installSkirmishStartColumn(window *gui.Window) {
	if window == nil {
		return
	}
	for i := 0; i < session.SkirmishMaxPlayers; i++ {
		metal := window.GadgetIndex("Metal" + strconv.Itoa(i))
		if metal < 0 {
			continue
		}
		b := window.Gadgets[metal]
		b.Name, b.SourceName = skirmishStartPrefix+strconv.Itoa(i), skirmishStartSource
		b.Rect.X = skirmishStartX
		window.Gadgets = append(window.Gadgets, b)
	}
	for i := range window.Gadgets {
		if window.Gadgets[i].Kind == gui.KindLabel && window.Gadgets[i].Text == "Location" {
			l := window.Gadgets[i]
			l.Text, l.SourceName = "Start", skirmishStartSource
			l.Rect.X, l.Rect.Y, l.Rect.W = skirmishStartX, skirmishStartHeadingY, 46
			window.Gadgets = append(window.Gadgets, l)
			break
		}
	}
}

// skirmishLivePlayers counts the shown rows that are players, the count the
// battle's schema is chosen by (skirmishConfigForStart compacts to it).
func (g *gameShell) skirmishLivePlayers() int {
	live := 0
	for i := 0; i < g.shownSkirmishRows(); i++ {
		if g.retailControllers[i] != 0 {
			live++
		}
	}
	return live
}

// mapStartPositions are the selected map's start positions under the schema
// a battle of players players runs, in authored order. Special.ID is the
// stored number, one less than the authored StartPos<n>.
func mapStartPositions(d *retailMapData, players int) []mission.Special {
	if d == nil || d.ota == nil {
		return nil
	}
	schema, err := mission.SelectNetworkSchema(d.ota, players)
	if err != nil {
		return nil
	}
	var starts []mission.Special
	for _, sch := range d.ota.Schemas {
		if sch.Name != schema.Name {
			continue
		}
		for _, sp := range mission.DecodeSpecials(sch.Section) {
			if sp.Kind == 1 {
				starts = append(starts, sp)
			}
		}
		break
	}
	return starts
}

// skirmishStartChoices are the authored start numbers the selected map
// offers the current rows, ascending and once each.
func (g *gameShell) skirmishStartChoices() []int {
	var choices []int
	for _, sp := range mapStartPositions(g.mapDataFor(g.setup.MapName), g.skirmishLivePlayers()) {
		if n := int(sp.ID) + 1; n >= 1 && n <= session.SkirmishMaxStart && !slices.Contains(choices, n) {
			choices = append(choices, n)
		}
	}
	slices.Sort(choices)
	return choices
}

// cycleSkirmishStart steps a row's start through Auto and the map's starts,
// skipping any another live row holds: forward on the left button, back on
// the right.
func (g *gameShell) cycleSkirmishStart(slot, delta int) {
	if slot < 0 || slot >= session.SkirmishMaxPlayers {
		return
	}
	options := []int{0}
	for _, n := range g.skirmishStartChoices() {
		taken := false
		for i := 0; i < g.shownSkirmishRows(); i++ {
			if i != slot && g.retailControllers[i] != 0 && g.setup.Players[i].Start == n {
				taken = true
				break
			}
		}
		if !taken {
			options = append(options, n)
		}
	}
	at := max(slices.Index(options, g.setup.Players[slot].Start), 0)
	g.setup.Players[slot].Start = options[cycleInt(at, 0, len(options)-1, delta)]
}

// skirmishStartCaption is a row's Start button text.
func skirmishStartCaption(start int) string {
	if start == 0 {
		return "Auto"
	}
	return strconv.Itoa(start)
}

// refreshSkirmishStarts shows each live row's Start button with its choice;
// open rows and the Survival screen, which places its own commanders, have
// none.
func (g *gameShell) refreshSkirmishStarts(p *ui.Panel) {
	for i := 0; i < session.SkirmishMaxPlayers; i++ {
		name := skirmishStartPrefix + strconv.Itoa(i)
		live := !g.survivalMenu && i < g.setup.NumPlayers && g.retailControllers[i] != 0
		p.SetActive(name, live)
		if !live {
			continue
		}
		p.SetText(name, skirmishStartCaption(g.setup.Players[i].Start))
		p.SetStatus(name, 0)
		p.SetHelp(name, "Click to choose this player's start position; right click goes back. Auto follows the Location setting.")
	}
}

// skirmishStartError is the Start preflight's check of the chosen starts:
// each live row's choice must be on the map, and no two rows may share one.
// It returns "" when every choice can be placed.
func (g *gameShell) skirmishStartError() string {
	choices := g.skirmishStartChoices()
	var owner [session.SkirmishMaxStart + 1]bool
	for i := 0; i < g.shownSkirmishRows(); i++ {
		start := g.setup.Players[i].Start
		if g.retailControllers[i] == 0 || start == 0 {
			continue
		}
		if !slices.Contains(choices, start) {
			return fmt.Sprintf("Start position %d is not on this map for %d players.", start, g.skirmishLivePlayers())
		}
		if owner[start] {
			return fmt.Sprintf("Two players chose start position %d.", start)
		}
		owner[start] = true
	}
	return ""
}

// drawMapStartMarkers numbers the map picker preview's start positions for
// the current rows, each a small bevelled box centred on its start. The
// projection is the preview canvas's: the usable terrain, Width×16−32 by
// Height×16−128 world pixels, fitted and centred in the surface
// (retailMapPreviewCanvas).
func (g *gameShell) drawMapStartMarkers(c *client.Client, p *ui.Panel, d *retailMapData, r gui.Rect) {
	if d == nil || d.tnt == nil || p == nil || p.Window == nil {
		return
	}
	ew, eh := int64(d.tnt.Width)*16-32, int64(d.tnt.Height)*16-128
	if ew <= 0 || eh <= 0 || r.W < 8 || r.H < 8 {
		return
	}
	w, h := int64(r.W), int64(r.H)
	dstW, dstH, x0, y0 := w, h, int64(0), int64(0)
	if ew < eh {
		dstW = ew * w / eh
		x0 = (w - dstW) / 2
	} else {
		dstH = eh * h / ew
		y0 = (h - dstH) / 2
	}
	font := g.windowGadgetFont(p, p.Window.Gadgets[p.Index("MAPPIC")])
	measure, metric := g.retailTextMetrics(font)
	for _, sp := range mapStartPositions(d, g.skirmishLivePlayers()) {
		label := strconv.Itoa(int(sp.ID) + 1)
		bw := int32(max(measure(label)+4, 9))
		bh := int32(metric + 3)
		cx := int32(int64(r.X) + x0 + int64(sp.X)*dstW/ew)
		cy := int32(int64(r.Y) + y0 + int64(sp.Z)*dstH/eh)
		box := gui.Rect{X: cx - bw/2, Y: cy - bh/2, W: bw, H: bh}
		box.X = min(max(box.X, r.X), r.X+r.W-1-box.W)
		box.Y = min(max(box.Y, r.Y), r.Y+r.H-1-box.H)
		v := retailButtonVerdict(gui.Gadget{}, 0, 0, 0, 0, false)
		drawGUIBevel(c, box, g.guiColor(v.top), g.guiColor(v.bot), g.guiColor(v.fill))
		g.drawRetailStringSelected(c, label, int(box.X)+(int(box.W)-measure(label))/2, int(box.Y)+(int(box.H)-metric)/2, int(box.W), g.guiColor(15), 0, font)
	}
}
