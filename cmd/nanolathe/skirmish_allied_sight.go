package main

// The Allied Sight option (docs/DESIGN_INTERFACE_HUD_INPUT.md §2.6 "Allied
// sight", docs/DESIGN_SESSIONS_AI_SAVE.md "Allied shared sight"): whether a
// team's players share line of sight, radar and the explored map. The
// skirmish setup screen offers it under the player box, beside Players; the
// online lobby offers it to the host in the same slot, which a skirmish
// room's wave controls leave free.

import (
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

const (
	alliedSightButton = "AlliedSight"
	alliedSightLabel  = "ALLIEDSIGHTLABEL"
	alliedSightSource = "NANOLATHE_ALLIED_SIGHT"
	// alliedSightStages are the button's captions by stage: 0 separate, 1
	// shared.
	alliedSightStages = "Separate|Shared"
	// The skirmish screen's slot, the second column under the player box.
	alliedSightX, alliedSightLabelY, alliedSightY = 175, 290, 306
)

// alliedSightHelp is the button's help for a shared or separate choice.
func alliedSightHelp(shared bool) string {
	if shared {
		return "Teammates share line of sight, radar and the explored map."
	}
	return "Each player sees only what its own units see, as in the original game."
}

// alliedSightGadgets are the option's heading, cloned from the Location
// label, and its two-stage button, cloned from Mapping, at x.
func alliedSightGadgets(window *gui.Window, x int32) []gui.Gadget {
	mapping := window.GadgetIndex("Mapping")
	label := -1
	for i := range window.Gadgets {
		if window.Gadgets[i].Kind == gui.KindLabel && window.Gadgets[i].Text == "Location" {
			label = i
			break
		}
	}
	if mapping < 0 || label < 0 {
		return nil
	}
	l := window.Gadgets[label]
	l.Name, l.SourceName, l.Text = alliedSightLabel, alliedSightSource, "Allied Sight"
	l.Rect.X, l.Rect.Y = x, alliedSightLabelY
	b := window.Gadgets[mapping]
	b.Name, b.SourceName, b.Art = alliedSightButton, alliedSightSource, window.Gadgets[mapping].Name
	b.Text, b.Labels, b.Stages = alliedSightStages, strings.Split(alliedSightStages, "|"), 2
	b.Rect.X, b.Rect.Y = x, alliedSightY
	return []gui.Gadget{l, b}
}

// installSkirmishAlliedSight adds the option to the skirmish setup screen.
func installSkirmishAlliedSight(window *gui.Window) {
	if window == nil {
		return
	}
	window.Gadgets = append(window.Gadgets, alliedSightGadgets(window, alliedSightX)...)
}

// refreshSkirmishAlliedSight shows the setup's choice.
func (g *gameShell) refreshSkirmishAlliedSight(p *ui.Panel) {
	if g.survivalMenu {
		return
	}
	p.SetStageAt(p.Index(alliedSightButton), boolInt(g.setup.AlliedSight))
	p.SetHelp(alliedSightButton, alliedSightHelp(g.setup.AlliedSight))
}
