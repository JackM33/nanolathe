package main

// The networked session's allies window, ALLIES.GUI
// [07 "Tab options menu and manual exit"]: one row per seated player with its
// ally button and team icon, and the shared-victory box. An ally click is one
// HumanDeclareAlliance and OK one HumanSharedVictory (DESIGN_MULTIPLAYER §7.1,
// §6.7 Q22).

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// alliesNoTeam is the team symbol "none"; 0..4 are teams [05 R-SHARE-01 §1].
const alliesNoTeam = 5

// alliesWindow is the open window: its rows, the victory box, and each ally
// click this client has sent that the committed frame does not show yet.
type alliesWindow struct {
	sharingWindow
	rows    []alliesRow
	victory int
	pending [frame.PlayerRowSlots]alliesPending
}

// alliesRow is one shown row: the slot it names and the gadgets the row
// builder gave it. Rows are packed upward, so row r uses the r-th name, logo,
// ally and team gadgets whatever slot it names; retail renames the name and
// ally gadgets after the slot so a click resolves by slot number, which this
// table does instead [07 "Tab options menu and manual exit"].
type alliesRow struct {
	slot   uint8
	name   string
	player int
	ally   int
	// logo is the row's LOGO surface and colour the player's lobby colour
	// byte, the frame number the row builder binds to it.
	logo   int
	colour uint8
}

// alliesPending is a declaration this client sent, shown until the committed
// frame carries it: retail's click writes the local row at once, while a
// seat command applies some ticks later.
type alliesPending struct{ set, value bool }

// alliesRowShown is the row builder's test for a slot: a seated player that
// is not watching and not eliminated [07 "Tab options menu and manual exit"],
// the same predicate the score panel's rows use [07 R-HUD-04 §1]. Every
// online row has a colour.
func alliesRowShown(row frame.PlayerRow) bool {
	return hud.ScoreSlot{Present: row.Present, Controller: row.Controller, Side: row.Side,
		LiveUnits: row.LiveUnits, Auxiliary: row.Auxiliary, Watcher: row.Watcher}.Qualifies()
}

// alliesTeamIcon is a team icon's frame: 10 for no team, the symbol × 2 + 1
// for a team of one and the symbol × 2 for two or more
// [07 "Tab options menu and manual exit"].
func alliesTeamIcon(team uint8, members int) int {
	if team >= alliesNoTeam || members == 0 {
		return 10
	}
	if members == 1 {
		return int(team)*2 + 1
	}
	return int(team) * 2
}

// openAlliesWindow opens the window from the strip's `ALLIES`, its only
// opener, on top of the strip [07 "Tab options menu and manual exit"], and
// runs the row builder once: rows, logo surfaces, ally buttons, team icons
// and the victory box.
func (b *battleSession) openAlliesWindow(cl *client.Client) {
	cur, ok := b.sharingFrame()
	if !ok || !cur.Sharing.MayShare {
		return
	}
	w, ok := b.loadSharingWindow(alliesGUI, "allies", "the allies window [07 \"Tab options menu and manual exit\"]")
	if !ok {
		return
	}
	window := w.window
	// The opener numbers the `ALLYx` and `TEAMICONSx` templates in record
	// order; the row builder then uses `PLAYER<r>`, `LOGO<r>`, `ALLY<r>` and
	// `TEAMICONS<r>` for row r.
	var allyAt, teamAt []int
	for i := 1; i < len(window.Gadgets); i++ {
		switch gui.CallbackName(window.Gadgets[i].Name) {
		case "ALLYx":
			allyAt = append(allyAt, i)
		case "TEAMICONSx":
			teamAt = append(teamAt, i)
		}
	}
	b.finishSharingWindow(&w)
	a := alliesWindow{sharingWindow: w, victory: window.GadgetIndex("VICTORY")}
	// The builder hides every template row before it shows the live ones.
	for r := range frame.PlayerRowSlots {
		w.panel.SetActive(fmt.Sprintf("PLAYER%d", r), false)
		w.panel.SetActive(fmt.Sprintf("LOGO%d", r), false)
	}
	for _, i := range append(append([]int(nil), allyAt...), teamAt...) {
		w.panel.SetActiveAt(i, false)
	}
	local := cur.Sharing.Seat
	me := cur.Players[local]
	localLive := alliesRowShown(me)
	members := func(team uint8) int {
		n := 0
		for slot := range cur.Players {
			if team < alliesNoTeam && cur.Players[slot].Team == team && alliesRowShown(cur.Players[slot]) {
				n++
			}
		}
		return n
	}
	for slot := range cur.Players {
		row := cur.Players[slot]
		r := len(a.rows)
		if !alliesRowShown(row) || r >= frame.PlayerRowSlots {
			continue
		}
		entry := alliesRow{slot: uint8(slot), name: row.Name, player: window.GadgetIndex(fmt.Sprintf("PLAYER%d", r)), ally: -1,
			logo: window.GadgetIndex(fmt.Sprintf("LOGO%d", r)), colour: row.Logo}
		w.panel.SetActiveAt(entry.player, true)
		// The LOGO surface is shown, made not hot so it never takes a click,
		// and carries the player's colour frame (alliesGadgetPainter).
		w.panel.SetActiveAt(entry.logo, true)
		if entry.logo > 0 {
			window.Gadgets[entry.logo].HotOrNot &^= 1
		}
		// The ally button is active only for a live remote human while the
		// local player is not eliminated — never for a computer or the local
		// row — and greyed for a teammate: a battleroom team cannot be broken
		// in battle [07 "Tab options menu and manual exit"]. The receiver's
		// own admission (AllianceTarget) governs every other case.
		human := row.Controller == 1 || row.Controller == 3
		teammate := me.Team < alliesNoTeam && row.Team == me.Team
		if r < len(allyAt) && uint8(slot) != local && localLive && human && (row.AllianceTarget || teammate) {
			entry.ally = allyAt[r]
			w.panel.SetActiveAt(entry.ally, true)
			if teammate {
				window.Gadgets[entry.ally].GrayedOut |= 1
			}
		}
		// Team icons are shown, and greyed in battle.
		if r < len(teamAt) {
			w.panel.SetActiveAt(teamAt[r], true)
			window.Gadgets[teamAt[r]].GrayedOut |= 1
			w.panel.SetStageAt(teamAt[r], alliesTeamIcon(row.Team, members(row.Team)))
		}
		a.rows = append(a.rows, entry)
	}
	// The box starts at the local shared-victory bit and is greyed for a
	// watcher or a seat whose team has two or more seated rows.
	if a.victory > 0 {
		w.panel.SetStageAt(a.victory, boolInt(me.SharedVictory))
		if !cur.Sharing.SharedVictoryChangeable {
			window.Gadgets[a.victory].GrayedOut |= 1
		}
	}
	b.sharing.allies = a
	b.refreshAlliesWindow(cur)
	b.showSharingModal(ui.BattleModalAllies, cl)
}

// alliesDeclared is the local seat's row-A entry toward slot as this client
// shows it: its own unapplied click, else the committed matrix.
func (a *alliesWindow) alliesDeclared(cur *frame.Frame, slot uint8) bool {
	if a.pending[slot].set {
		return a.pending[slot].value
	}
	return cur.Players[cur.Sharing.Seat].Allies[slot]
}

// refreshAlliesWindow sets each ally button's stage to the indicator
// `B << 1 | A` of the local seat toward that row — 0 none, 1 mine only, 2
// theirs only, 3 mutual [07 "Tab options menu and manual exit"] — from the
// committed frame, which carries every declaration once it applies. A pending
// click retires when the frame shows it, or when the row's button would no
// longer be active.
func (b *battleSession) refreshAlliesWindow(cur *frame.Frame) {
	a := &b.sharing.allies
	if a.panel == nil {
		return
	}
	mine := cur.Players[cur.Sharing.Seat]
	for _, row := range a.rows {
		if p := a.pending[row.slot]; p.set && (mine.Allies[row.slot] == p.value || !cur.Players[row.slot].AllianceTarget) {
			a.pending[row.slot] = alliesPending{}
		}
		if row.ally > 0 {
			a.panel.SetStageAt(row.ally, boolInt(mine.AlliedBy[row.slot])<<1|boolInt(a.alliesDeclared(cur, row.slot)))
		}
	}
}

// labelText supplies each shown row's player name; the builder writes it
// into the row's name label at open [07 "Tab options menu and manual exit"].
func (a *alliesWindow) labelText(index int, _ gui.Gadget) (string, bool) {
	for _, row := range a.rows {
		if row.player == index && index > 0 {
			return row.name, true
		}
	}
	return "", false
}

// alliesGadgetPainter is the allies window's per-gadget hook for the battle
// window painter: each shown row's name, and each shown row's LOGO surface.
// The surface painter stretches the bound frame — entry `32xlogos`, the
// player's lobby colour byte — from its interior, source corners (1,1)
// (w-1,1) (w-1,h-1) (1,h-1), onto the corners (x,y) .. (x+w'-1,y+h'-1) of
// the surface rectangle; the quad raster leaves the far column and row, so
// the image covers w'-1 by h'-1, as the score panel's logo does. A compressed
// frame is blitted unscaled at the corner instead
// [07 R-WGT-01 §8][07 "Tab options menu and manual exit"][03 R-RAST-01 §1].
// The window painter visits gadgets in index order and calls this hook at
// each one's visit, so a logo surface authored before its row's name label,
// as the stock ones are, lies behind the name.
func (h *retailBattleHUD) alliesGadgetPainter(c *client.Client, a *alliesWindow) func(int, gui.Gadget) (string, bool) {
	return func(index int, gad gui.Gadget) (string, bool) {
		if gad.Kind == gui.KindSurface {
			h.drawAlliesLogo(c, a, index)
			return "", false
		}
		return a.labelText(index, gad)
	}
}

// drawAlliesLogo paints the shown row whose LOGO surface is index.
func (h *retailBattleHUD) drawAlliesLogo(c *client.Client, a *alliesWindow, index int) {
	if index <= 0 || a.window == nil {
		return
	}
	for _, row := range a.rows {
		if row.logo != index {
			continue
		}
		frame := h.sideLogoFrame(row.colour)
		if frame == nil {
			return
		}
		r, clip := a.window.PlacedRect(index), a.window.Rect
		if frame.Compressed != 0 {
			c.UIBlitClipped(frame, int(r.X), int(r.Y), int(clip.X), int(clip.Y), int(clip.W), int(clip.H))
			return
		}
		c.UIBlitFrameSourceRectScaledClipped(frame, 1, 1, int(frame.Width)-1, int(frame.Height)-1,
			int(r.X), int(r.Y), int(r.W)-1, int(r.H)-1, int(clip.X), int(clip.Y), int(clip.W), int(clip.H))
		return
	}
}

// activateAlliesGadget is the window's callback
// [07 "Tab options menu and manual exit"]:
//
//   - an ally button plays `Options`, flips the local declaration toward its
//     row, sends it, posts the announcement and refreshes the indicators,
//     leaving the window open;
//   - `VICTORY` plays `Options` and keeps the window open; the widget service
//     has already toggled the box;
//   - `OK` plays `Options`, writes the box into the local shared-victory bit,
//     sending it only when it changed, as retail broadcasts only a change, and
//     closes the window, leaving a strip beneath it on top;
//   - anything else keeps the window open.
func (b *battleSession) activateAlliesGadget(index int, cl *client.Client) {
	cur, ok := b.sharingFrame()
	if !ok {
		return
	}
	a := &b.sharing.allies
	for _, row := range a.rows {
		if row.ally != index {
			continue
		}
		b.playUICue(cl, "Options")
		value := !a.alliesDeclared(cur, row.slot)
		if b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanDeclareAlliance,
			DeclareAlliance: session.HumanDeclareAllianceCommand{Target: row.slot, Value: value}}) != nil {
			return
		}
		a.pending[row.slot] = alliesPending{set: true, value: value}
		b.announceAlliance(row.name, value)
		b.refreshAlliesWindow(cur)
		return
	}
	switch gui.CallbackName(a.window.Gadgets[index].Name) {
	case "VICTORY":
		b.playUICue(cl, "Options")
		return
	case "OK":
		b.playUICue(cl, "Options")
	default:
		return
	}
	if a.victory > 0 && cur.Sharing.SharedVictoryChangeable {
		on := a.panel.StageAt(a.victory)&1 != 0
		if on != cur.Players[cur.Sharing.Seat].SharedVictory {
			_ = b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanSharedVictory,
				SharedVictory: session.HumanSharedVictoryCommand{On: on}})
		}
	}
	b.closeSharingTop()
}

// announceAlliance posts the click's announcement, an ordinary chat line:
// after the composer's `<name> `, a space, the translated `allied with` or
// `broke alliance with`, a space and the target's name, to the current
// recipient mode's recipients and shown locally without the arrival cue
// [07 "Tab options menu and manual exit"].
func (b *battleSession) announceAlliance(target string, allied bool) {
	text := "broke alliance with"
	if allied {
		text = "allied with"
	}
	if captions := hudCaptionTranslator(b.hud); captions != nil {
		text = captions.Translate(text)
	}
	_ = b.sendChatLine(" " + text + " " + target)
}
