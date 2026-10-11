package main

// The networked session's Tab strip, TABMENU.GUI, and what its three windows
// share (DESIGN_MULTIPLAYER §6.4, §7.1). Retail offers the strip, the transfer
// dialog SHARE.GUI and the allies window ALLIES.GUI only in a multiplayer
// session [07 R-CAM-01 §2] [07 "Tab options menu and manual exit"]; an online
// battle is that session kind, and a single-player battle never reaches any
// of this. SHARE.GUI is battle_share.go's and ALLIES.GUI battle_allies.go's.
//
// The windows read only the committed frame [I6]: the local seat's standing
// (frame.SharingView) and each row's sharing and alliance halves. Every act
// is a typed command through the battle's one submission path, which online
// becomes a seat command (submitHumanCommand), so a window offers exactly
// what every replica's receiver admits.
//
// None of the three pauses anything: retail's network mode takes no local
// pause [07 §11], and an online battle has none. Each is a battle modal while
// it is up (ui.SharingModal), so the world takes no input under it. The strip
// stays open beneath the window one of its buttons opens — SHARE, ALLIES or
// the options root — and is the top window again when that window closes
// [07 "Tab options menu and manual exit"] (ui.BattleState.StripBeneath).
// TODO(question): whether retail's battle input still reaches the world while
// the strip or one of its windows is open is not settled; a trace of the
// battle input pass's window-stack gate with TABMENU.GUI on top would settle
// it.

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// The three windows' hard-coded names, none side-prefixed
// [07 "Tab options menu and manual exit"][07 R-HUD-03 §9].
const (
	tabMenuGUI = "guis/tabmenu.gui"
	shareGUI   = "guis/share.gui"
	alliesGUI  = "guis/allies.gui"
)

// battleSharing is the host state of the three windows. Each window is parsed
// afresh at every open, as retail's window-open routine does, so nothing an
// earlier open changed survives into the next.
type battleSharing struct {
	tab    sharingWindow
	share  shareScreen
	allies alliesWindow
	// navigation is the window context's key-navigation switch as these
	// windows see it. The strip's opener turns it on, and the strip's close
	// and the options root's close turn it off; SHARE and ALLIES leave it
	// alone, so their Enter and Escape defaults act only while it is on
	// [07 "Tab options menu and manual exit"][07 R-WGT-01 §1].
	navigation bool
}

// sharingWindow is one opened window: its parsed record, its own GAF and its
// retained widget state.
type sharingWindow struct {
	window *gui.Window
	page   *formats.GAF
	panel  *ui.Panel
}

func (w *sharingWindow) clear() { *w = sharingWindow{} }

// sharingFrame is the committed frame of a live online battle that offers the
// networked session's windows, or false. A replay playback offers none: it
// takes no command of its own. The windows draw through the front end's
// painters, which a battle entered without the shell (the command-line
// play test) does not have, so such a battle keeps its single-player keys.
func (b *battleSession) sharingFrame() (*frame.Frame, bool) {
	if b == nil || b.multiplayer == nil || b.playback != nil || b.shell == nil || b.hud == nil {
		return nil, false
	}
	cur, ok := b.currentSnapshot()
	if !ok || cur == nil || !cur.Sharing.Online {
		return nil, false
	}
	return cur, true
}

// stripKey reports whether this frame's residual key is the one that toggles
// the strip. Retail's strip key is Tab [07 R-CAM-01 §2]. With the Overview
// preference Tab keeps the overview, so F2 (unmodified) takes the strip
// instead and the strip stays reachable (DESIGN_INTERFACE_HUD_INPUT §3.15).
func (b *battleSession) stripKey(keyDown func(input.Key) bool, ctrl, shift bool) bool {
	if b.hostPreferences().Overview == settings.OverviewMegamap {
		return keyDown(input.KeyF2) && !ctrl && !shift
	}
	return keyDown(input.KeyTab)
}

// shareKeyToken reports the `h` token, 0x68, which retail's dispatcher
// binds to the transfer dialog in a multiplayer session [07 R-CAM-01 §2].
// Only the lowercase character is the key; `H` has no case.
func shareKeyToken(in *input.State) bool {
	return in != nil && in.ShortcutTokenMode && in.ShortcutToken.Kind == input.TokenText && in.ShortcutToken.Rune == 'h'
}

// openTabMenu opens the strip. The Tab key plays `SmallButton` on the press
// that opens the strip and on the one that closes it
// [07 "Tab options menu and manual exit"].
//
// The opener shows `ALLIES` and `SHARE` only for a local player who is not a
// watcher (the "some other slot is not watching" term counts empty slots, so
// it always holds), and `CONTROL` only on the machine of the host player
// while the lobby lock bit is clear; a watcher keeps `OPTIONS` alone. The lock
// bit is only ever preset from an online-library configuration record
// [08 R-SKIR-01 §7], which Nanolathe has none of, so it is clear.
func (b *battleSession) openTabMenu(cl *client.Client) {
	cur, ok := b.sharingFrame()
	if !ok {
		return
	}
	w, ok := b.loadSharingWindow(tabMenuGUI, "tabmenu", "the Tab strip [07 \"Tab options menu and manual exit\"]")
	if !ok {
		return
	}
	b.finishSharingWindow(&w)
	b.playUICue(cl, "SmallButton")
	mayShare := cur.Sharing.MayShare
	w.panel.SetActive("ALLIES", mayShare)
	w.panel.SetActive("SHARE", mayShare)
	if i := w.window.GadgetIndex("CONTROL"); i > 0 {
		w.panel.SetActiveAt(i, mayShare && b.controlsHostPlayer(cur))
		// CONTROL.GUI is the host's player control: watching allowed and
		// rejecting a player [07 R-FE-01 §7]. An online room offers neither —
		// watchers are not offered yet (DESIGN_MULTIPLAYER §11.4), and only a
		// vote removes a seat (§11.1) — so the button stays greyed.
		w.window.Gadgets[i].GrayedOut |= 1
	}
	b.sharing.tab = w
	b.sharing.navigation = true
	b.showSharingModal(ui.BattleModalTabMenu, cl)
}

// controlsHostPlayer reports whether this client seats the room's host, the
// relay's seat 0 that created the room (onlineRoomState.isHost). A slot's seat
// is its rank among the seats present at Start (onlineChatSeats).
func (b *battleSession) controlsHostPlayer(cur *frame.Frame) bool {
	if b == nil || b.multiplayer == nil || cur == nil {
		return false
	}
	seat, ok := b.multiplayer.chat.seats.seatOf(&cur.Players, int(cur.Sharing.Seat))
	return ok && seat == 0
}

// closeTabMenu closes the strip; Tab's close plays the same cue as its open.
func (b *battleSession) closeTabMenu(cl *client.Client, cue bool) {
	if b.battleState().Modal() != ui.BattleModalTabMenu {
		return
	}
	if cue {
		b.playUICue(cl, "SmallButton")
	}
	b.closeSharingTop()
}

// activateTabMenuButton runs one strip button
// [07 "Tab options menu and manual exit"]. `OPTIONS`, `SHARE` and `ALLIES`
// play `BigButton` and open the options window, the transfer dialog and the
// allies window on top of the strip, which stays open beneath; `CONTROL` would
// do the same with the control window, but it is greyed here (openTabMenu).
// `CANCEL` closes the top window, the strip itself, with no cue; it is
// authored inactive and the opener never shows it, so only its route is kept.
//
// The options root turns the key-navigation switch off when it closes, and no
// window of the strip's can open over the strip while that root is up, so the
// switch is off from `OPTIONS` on.
func (b *battleSession) activateTabMenuButton(name string, cl *client.Client) {
	switch gui.CallbackName(name) {
	case "OPTIONS":
		b.playUICue(cl, "BigButton")
		b.sharing.navigation = false
		b.openBattleMenu()
	case "SHARE":
		b.playUICue(cl, "BigButton")
		b.openShareScreen(cl)
	case "ALLIES":
		b.playUICue(cl, "BigButton")
		b.openAlliesWindow(cl)
	case "CANCEL":
		b.closeSharingTop()
	}
}

// loadSharingWindow parses one window, binds its own GAF (`anims\<name>`,
// when there is one) and builds it as every battle child is built
// [07 R-WGT-01 §12].
func (b *battleSession) loadSharingWindow(logical, name, ctx string) (sharingWindow, bool) {
	h := b.hud
	window := loadGUIOptional(h.fs, logical, ctx, hudCaptionTranslator(h))
	if window == nil || len(window.Gadgets) == 0 {
		return sharingWindow{}, false
	}
	page := h.resolvePageArt(name)
	h.installWindow(window, page)
	return sharingWindow{window: window, page: page}, true
}

// finishSharingWindow captures the built record's widget state and places it.
func (b *battleSession) finishSharingWindow(w *sharingWindow) {
	w.panel = ui.NewPanel(w.window)
	b.hud.placeSharingWindow(w.window)
}

// placeSharingWindow puts a window at its authored origin, as the window
// initializer leaves one opened without a placement flag: a window reaching
// past the surface's right or bottom edge is recentred on that axis, and then
// the build counts a negative `ypos` up from the surface bottom, so the
// strip's `-33` is the live surface's last 33 rows and SHARE's `-429` ends on
// the strip's top edge; later repaints test the resolved `y` again
// [07 R-HUD-05][07 R-WGT-01 §12]. Beside a magnified rail the window moves
// right by the width the rail gained, as TALK.GUI's left edge does
// (DESIGN_INTERFACE_HUD_INPUT "Modern UI scale").
func (h *retailBattleHUD) placeSharingWindow(window *gui.Window) {
	if h == nil || window == nil || len(window.Gadgets) == 0 {
		return
	}
	sw, sh := h.screenW, h.screenH
	if sw <= 0 || sh <= 0 {
		sw, sh = retailScreenW, retailScreenH
	}
	header := window.Gadgets[0].Rect
	w, height := window.Rect.W, window.Rect.H
	x := header.RawX + railInset(max(h.chromeScale, 1)) - railInset(1)
	y := header.RawY
	if x+w > sw {
		x = (sw - w) / 2
	}
	if y+height > sh {
		y = (sh - height) / 2
	}
	if y < 0 {
		y += sh
	}
	if y+height > sh {
		y = (sh - height) / 2
	}
	x, y = max(x, 0), max(y, 0)
	window.Rect.X, window.Rect.Y = x, y
	window.OriginX, window.OriginY = x, y
	window.Gadgets[0].Rect.X, window.Gadgets[0].Rect.Y = x, y
}

// showSharingModal makes the window the top modal. Opening a window flushes
// the producer ring so no earlier key reaches it [07 R-WGT-01 §1].
func (b *battleSession) showSharingModal(m ui.BattleModal, cl *client.Client) {
	b.endDragScroll(cl)
	state := b.battleState()
	if !state.OpenSharing(m) {
		return
	}
	flushWindowTokens(cl)
	state.Input.DragActive = false
	state.Input.HUDCaptured = false
}

// closeSharingTop closes whichever of the three windows is on top and drops
// its state. A strip beneath it is the top window again; closing the strip
// turns the key-navigation switch off
// [07 "Tab options menu and manual exit"].
func (b *battleSession) closeSharingTop() {
	state := b.battleState()
	switch state.Modal() {
	case ui.BattleModalTabMenu:
		b.sharing.tab.clear()
		b.sharing.navigation = false
	case ui.BattleModalShare:
		b.sharing.share = shareScreen{}
	case ui.BattleModalAllies:
		b.sharing.allies = alliesWindow{}
	default:
		return
	}
	state.CloseSharing()
	b.developer.quickkeysDisabled = false
}

// closeSharingModal closes every one of the three windows, a strip beneath
// another window included, and drops their state: the battle has stopped
// offering them (a final result, or no committed online frame).
func (b *battleSession) closeSharingModal() {
	state := b.battleState()
	if !ui.SharingModal(state.Modal()) && !state.StripBeneath() {
		return
	}
	if ui.SharingModal(state.Modal()) {
		b.developer.quickkeysDisabled = false
	}
	b.sharing = battleSharing{}
	state.DropSharing()
}

// sharingWindowOnTop is the open window's state, or nil.
func (b *battleSession) sharingWindowOnTop() *sharingWindow {
	switch b.battleState().Modal() {
	case ui.BattleModalTabMenu:
		return &b.sharing.tab
	case ui.BattleModalShare:
		return &b.sharing.share.sharingWindow
	case ui.BattleModalAllies:
		return &b.sharing.allies.sharingWindow
	}
	return nil
}

// handleSharingInput drives the open window through the indexed widget
// service every battle child uses [07 R-WGT-01 §§1-3]. An event the window's
// service leaves unclaimed reaches the window's authored Enter and Escape
// defaults — `crdefault` and `escdefault`, which the window-open routine
// binds when the button is active and not greyed [07 R-FE-01 §12] — through
// the window key matrix, which runs only while the key-navigation switch is
// on [07 R-WGT-01 §1]. SHARE and ALLIES take the key either way; with the
// switch on, Enter confirms both with `OK`, Escape cancels SHARE and accepts
// ALLIES [07 "Tab options menu and manual exit"]. The strip's `CANCEL`
// default is authored inactive, so neither key acts on the strip.
func (b *battleSession) handleSharingInput(in *input.State, cl *client.Client) {
	w := b.sharingWindowOnTop()
	if w == nil || w.window == nil || w.panel == nil || in == nil {
		b.closeSharingModal()
		return
	}
	cur, ok := b.sharingFrame()
	if !ok {
		// The battle stopped offering the windows.
		b.closeSharingModal()
		return
	}
	if b.battleState().Modal() != ui.BattleModalTabMenu && !cur.Sharing.MayShare {
		// A seat that stops being able to share (its result is final) loses
		// the window that acts for it; the strip beneath stays.
		b.closeSharingTop()
		return
	}
	b.hud.placeSharingWindow(w.window)
	if b.battleState().Modal() == ui.BattleModalAllies {
		b.refreshAlliesWindow(cur)
	}
	result := b.serviceSharingPanel(w, in)
	if result.Fired && result.FiredIndex > 0 && result.FiredIndex < len(w.window.Gadgets) {
		b.activateSharingGadget(result.FiredIndex, cl)
		return
	}
	if result.ConsumedTokens != 0 {
		return
	}
	modalInput := *in
	modalInput.ShortcutTokenMode = in.ShortcutTokenMode || in.PendingTokens() != 0
	if tokens := in.PeekTokens(); len(tokens) != 0 {
		modalInput.ShortcutToken = tokens[0]
	}
	kbd := battleShortcutKeyboard(&modalInput)
	index := -1
	switch {
	case kbd != nil && kbd.KeyDown(input.KeyEscape):
		index = w.window.EscapeDefaultIndex()
	case kbd != nil && kbd.KeyDown(input.KeyEnter):
		index = w.window.EnterDefaultIndex()
	default:
		return
	}
	in.DiscardTokens(1)
	if !b.sharing.navigation {
		return
	}
	if index > 0 && index < len(w.window.Gadgets) && w.panel.ActiveAt(index) && w.window.Gadgets[index].GrayedOut&1 == 0 {
		b.activateSharingGadget(index, cl)
	}
}

// serviceSharingPanel is the zero-token battle child adapter
// (serviceBattleChildPanel) with the list metric the transfer dialog's player
// list needs.
func (b *battleSession) serviceSharingPanel(w *sharingWindow, in *input.State) ui.ServiceResult {
	panel, window := w.panel, w.window
	frame := pointerFrame(in, in.PeekTokens(), false)
	frame.DisableQuickKeys = b.developer.quickkeysDisabled
	if in.Kbd != nil {
		frame.AltHeld = in.Kbd.KeyHeld(input.KeyAlt)
	}
	if b.millisSource != nil {
		frame.TimerAdvanced = panel.TimerAdvanced(clock.ScaledNow(b.millisSource.Millis32()))
	}
	result := panel.ServiceFrame(frame, ui.WidgetHooks{
		Metric: func(int) int { return b.shell.retailTextHeight() },
		ArtFrames: func(index int) int {
			if index < 0 || index >= len(window.Gadgets) {
				return 0
			}
			return b.hud.modalGadgetArtFrames(window.Gadgets[index], w.page)
		},
	})
	in.DiscardTokens(result.ConsumedTokens)
	return result
}

// activateSharingGadget routes one fired gadget to the open window's callback.
func (b *battleSession) activateSharingGadget(index int, cl *client.Client) {
	w := b.sharingWindowOnTop()
	if w == nil || index <= 0 || index >= len(w.window.Gadgets) {
		return
	}
	name := w.window.Gadgets[index].Name
	switch b.battleState().Modal() {
	case ui.BattleModalTabMenu:
		b.activateTabMenuButton(name, cl)
	case ui.BattleModalShare:
		b.activateShareGadget(index, cl)
	case ui.BattleModalAllies:
		b.activateAlliesGadget(index, cl)
	}
}

// drawSharingWindows paints the open windows over the battle, through the
// battle modal family's painter for their panels, buttons and labels and the
// front end's for the transfer dialog's list and sliders: first the strip
// when it is open, on top or beneath the window one of its buttons opened,
// then SHARE or ALLIES on top of it [07 "Tab options menu and manual exit"].
// It reads only the windows' own presentation state [I6]; the result screen
// closes them before it takes the frame (viewerStep).
func (h *retailBattleHUD) drawSharingWindows(c *client.Client, b *battleSession) {
	if h == nil || c == nil || b == nil || b.shell == nil {
		return
	}
	state := b.battleState()
	if tab := &b.sharing.tab; tab.window != nil && tab.panel != nil && (state.Modal() == ui.BattleModalTabMenu || state.StripBeneath()) {
		h.placeSharingWindow(tab.window)
		h.drawGUIWindowState(c, tab.window, tab.page, "", tab.panel, nil)
	}
	switch state.Modal() {
	case ui.BattleModalShare:
		if s := &b.sharing.share; s.window != nil && s.panel != nil {
			h.drawGUIWindowState(c, s.window, s.page, "", s.panel, s.labelText)
			h.drawShareControls(c, b)
		}
	case ui.BattleModalAllies:
		if a := &b.sharing.allies; a.window != nil && a.panel != nil {
			h.drawGUIWindowState(c, a.window, a.page, "", a.panel, h.alliesGadgetPainter(c, a))
		}
	}
}
