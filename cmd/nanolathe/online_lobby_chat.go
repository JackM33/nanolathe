package main

// The online lobby's chat (DESIGN_MULTIPLAYER §12.2, §16.6.2). Players present
// in a room talk to each other before Start: Enter opens a text line under the
// message area, Enter sends it to every other present seat whose client
// carries chat, and Escape or a right click drops it. Sent and received lines
// show as "<name> text" in a small message area at the lower left that keeps
// the last few lines. This is Nanolathe's own lobby, not retail's battleroom,
// whose chat is not researched; only the "<name> text" form follows retail's
// chat line [07 "Chat"]. Chat is host presentation: it never enters the
// command stream, a replay or a checksum.

import (
	"errors"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/relay"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// The message area and its text line, in the lobby window's coordinates: the
// free band under the map field, left of the Ready button, and below it a row
// that runs on under Ready to the Start button's pipe.
const (
	onlineChatX     = 42
	onlineChatY     = 410
	onlineChatW     = 214
	onlineChatRows  = 4
	onlineChatLineY = 460
	onlineChatLineW = 402
	onlineChatLineH = 18
	// onlineChatKeep is how many lines the lobby keeps; only the newest that
	// fit the area are drawn.
	onlineChatKeep = 32
)

// The chat's own words.
const (
	onlineChatHint      = "Press Enter to chat."
	onlineChatLineHelp  = "Type a message for the other players. Enter sends it; Escape or a right click drops it."
	onlineChatOpenHelp  = "Press Enter, or click here, to chat with the other players."
	onlineChatRateText  = "Not sent: too many messages at once. Wait a moment, then press Enter again."
	onlineChatCharsText = "Not sent: messages can use letters, digits, spaces and punctuation only."
	onlineChatAloneText = "Not sent: no one else in the room can read chat."
)

// onlineLobbyChat is the room's chat: the lines shown, and whether the text
// line is open.
type onlineLobbyChat struct {
	// lines are the players' "<name> text" lines and the lobby's own notices,
	// such as why a line was not sent, oldest first.
	lines []string
	// editing is set while the text line is open: Enter there sends it.
	editing bool
	// swallowRight is set by the right press that dropped the line, until the
	// right button is free again, so that press does nothing else.
	swallowRight bool
}

func (c *onlineLobbyChat) add(line string) {
	c.lines = append(c.lines, line)
	if n := len(c.lines) - onlineChatKeep; n > 0 {
		c.lines = append(c.lines[:0], c.lines[n:]...)
	}
}

// seatDisplayName is how the lobby names a present seat: its display name
// (onlineLobbyName), or "Player n" for its rank among the present seats, the
// name every seat composes for it.
func (r *onlineRoomState) seatDisplayName(seat int) string {
	if seat < 0 || seat >= len(r.state.Seats) {
		return ""
	}
	if name := onlineLobbyName(r.state.Seats[seat]); name != "" {
		return name
	}
	rank := 0
	for i := 0; i < seat; i++ {
		if r.state.Seats[i].Present {
			rank++
		}
	}
	return onlineDefaultName(rank)
}

// chatRecipients names, one bit per seat, every other present seat whose
// client carries chat (protocol version 8). A seat without it is never
// offered chat.
func (r *onlineRoomState) chatRecipients() uint16 {
	var recipients uint16
	if r.lobby == nil {
		return 0
	}
	local := int(r.lobby.Seat())
	for i, seat := range r.state.Seats {
		if i != local && seat.Present && seat.Chat {
			recipients |= 1 << i
		}
	}
	return recipients
}

// chatOffered reports whether the text line is offered: while it is open,
// while it holds text the player has not sent, and whenever someone could
// read it.
func (r *onlineRoomState) chatOffered() bool {
	if r.panel == nil {
		return false
	}
	return r.chat.editing || r.panel.TextOf(lobbyChatLine) != "" || r.chatRecipients() != 0
}

// buildOnlineChatLine adds the text line to the lobby window and makes it the
// window's Enter default, so Enter opens it. It is retail's text box, as the
// address window's field is; it fills its own background.
func buildOnlineChatLine(window *gui.Window) {
	window.Gadgets = append(window.Gadgets, gui.Gadget{
		Kind: gui.KindTextBox, Name: lobbyChatLine, SourceName: lobbyChatLine, Active: 1, ColorF: 15, Attribs: 0x01,
		MaxChars: relay.HostedMaxChatBytes,
		Rect:     gui.Rect{X: onlineChatX, Y: onlineChatLineY, W: onlineChatLineW, H: onlineChatLineH},
	})
	window.Header.CrDefault = lobbyChatLine
}

// refreshOnlineChat writes the text line's state: shown while offered, and
// help that says how it works.
func (g *gameShell) refreshOnlineChat() {
	r := &g.online.room
	p := r.panel
	p.SetActive(lobbyChatLine, r.chatOffered())
	if r.chat.editing {
		p.SetHelp(lobbyChatLine, onlineChatLineHelp)
	} else {
		p.SetHelp(lobbyChatLine, onlineChatOpenHelp)
	}
}

// pollOnlineChat takes the lines received since the last step into the
// message area, named as the lobby names their senders. It runs only before
// Start: lines still waiting when the room starts stay in the shared inbox
// for the battle (relay.HostedLobby.DrainChat).
func (g *gameShell) pollOnlineChat() bool {
	r := &g.online.room
	lines := r.lobby.DrainChat(nil)
	for _, line := range lines {
		r.chat.add("<" + r.seatDisplayName(int(line.Seat)) + "> " + line.Text)
	}
	return len(lines) != 0
}

// activateOnlineChatLine is the text line's activation. With the line closed
// it is Enter, the window's Enter default, and opens it. While it is open,
// Escape is the window's escape default: a default key focuses the gadget it
// fires, so the line arrives with its capture held, and that drops it. Enter
// in the line releases the capture and fires it: that sends it.
func (g *gameShell) activateOnlineChatLine() {
	r := &g.online.room
	p := r.panel
	i := p.Index(lobbyChatLine)
	switch {
	case !r.chat.editing:
		g.openOnlineChat()
	case p.EditorCaptured() && p.EditorIndex() == i:
		g.closeOnlineChat(true)
	default:
		g.sendOnlineChat(p.TextOf(lobbyChatLine))
	}
	g.refreshOnlineLobby()
}

// openOnlineChat opens the text line. Escape then drops the line instead of
// leaving the room.
func (g *gameShell) openOnlineChat() {
	r := &g.online.room
	p := r.panel
	if !r.chatOffered() {
		return
	}
	p.SetActive(lobbyChatLine, true)
	if !p.FocusEditor(p.Index(lobbyChatLine)) {
		return
	}
	r.chat.editing = true
	p.Window.Header.EscDefault = lobbyChatLine
}

// closeOnlineChat closes the text line, emptying it when drop is set, and
// gives Escape back to Leave.
func (g *gameShell) closeOnlineChat(drop bool) {
	r := &g.online.room
	p := r.panel
	if drop {
		p.SetText(lobbyChatLine, "")
	}
	if p.EditorCaptured() && p.EditorIndex() == p.Index(lobbyChatLine) {
		p.SetFocus(-1)
	}
	r.chat.editing = false
	p.Window.Header.EscDefault = "PrevMenu"
}

// sendOnlineChat sends the typed line to every other present seat that
// carries chat and shows it as this player's. An empty line just closes. A
// line the relay or this client refuses is not shown as sent: the message
// area says why in plain words, and a line refused for its rate or its
// characters stays in the open text line to send again or correct.
func (g *gameShell) sendOnlineChat(typed string) {
	r := &g.online.room
	text := strings.TrimSpace(typed)
	if text == "" {
		g.closeOnlineChat(true)
		return
	}
	recipients := r.chatRecipients()
	if recipients == 0 {
		r.chat.add(onlineChatAloneText)
		g.closeOnlineChat(true)
		return
	}
	if relay.ValidHostedChat(text) != nil {
		r.chat.add(onlineChatCharsText)
		g.reopenOnlineChat()
		return
	}
	err := r.lobby.SendChat(recipients, text)
	switch {
	case err == nil:
		r.chat.add("<" + r.seatDisplayName(int(r.lobby.Seat())) + "> " + text)
		g.closeOnlineChat(true)
	case errors.Is(err, relay.ErrHostedChatRate):
		r.chat.add(onlineChatRateText)
		g.reopenOnlineChat()
	default:
		r.chat.add("Not sent: " + onlineRefusalText(err))
		g.closeOnlineChat(true)
	}
}

// reopenOnlineChat keeps the refused line open with its text.
func (g *gameShell) reopenOnlineChat() {
	r := &g.online.room
	r.chat.editing = false
	g.openOnlineChat()
}

// cancelOnlineChatOnRightPress drops the open text line on a right press
// anywhere, as Escape does, and keeps that press from doing anything else in
// the lobby until the right button is free again.
func (g *gameShell) cancelOnlineChatOnRightPress(in *input.State) {
	r := &g.online.room
	pressed, held := onlineRightPress(in)
	if r.chat.editing && pressed {
		g.closeOnlineChat(true)
		r.chat.swallowRight = true
		g.refreshOnlineLobby()
		return
	}
	if !pressed && !held {
		r.chat.swallowRight = false
	}
}

// syncOnlineChatCapture follows what the widget pass does to the text line's
// capture on its own: a click on the line opens it, a click elsewhere closes
// it with its text kept, and the right press that dropped it does not reopen
// it. It reports a change.
func (g *gameShell) syncOnlineChatCapture() bool {
	r := &g.online.room
	p := r.panel
	captured := p.EditorCaptured() && p.EditorIndex() == p.Index(lobbyChatLine)
	switch {
	case captured && r.chat.swallowRight:
		p.SetFocus(-1)
	case captured && !r.chat.editing:
		r.chat.editing = true
		p.Window.Header.EscDefault = lobbyChatLine
	case !captured && r.chat.editing:
		g.closeOnlineChat(false)
	default:
		return false
	}
	return true
}

// onlineRightPress reports whether this step's pointer record is a right
// press, and whether a right button event or hold is in this step at all.
func onlineRightPress(in *input.State) (pressed, active bool) {
	if in == nil || in.Mouse == nil {
		return false, false
	}
	if event, ok := in.CurrentPointer(); ok {
		switch event.Kind {
		case input.RightDown, input.RightDoubleClick:
			return true, true
		case input.RightUp:
			return false, true
		}
	} else if in.Mouse.Pressed(input.MouseButtonRight) {
		return true, true
	}
	return false, in.Mouse.Held(input.MouseButtonRight) || in.Mouse.Released(input.MouseButtonRight)
}

// onlineChatLayout is the message area's rows, newest last: each line wrapped
// to the area's width in the label face, of which the newest rows that fit
// are kept.
func onlineChatLayout(lines []string, measure func(string) int, width, rows int) []string {
	var out []string
	for i := len(lines) - 1; i >= 0 && len(out) < rows; i-- {
		wrapped := retailWrapLines(lines[i], measure, width)
		for j := len(wrapped) - 1; j >= 0 && len(out) < rows; j-- {
			out = append(out, wrapped[j])
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// drawOnlineChat paints the message area in the lobby's label face, and the
// hint on the closed, empty text line while someone could read a message.
func (g *gameShell) drawOnlineChat(c *client.Client, p *ui.Panel) {
	r := &g.online.room
	measure, metric, ok := g.retailLabelTextMetrics()
	if !ok {
		return
	}
	font := g.retailGAFLabelFont()
	var pal *palette.Tables
	if g.assets != nil {
		pal = g.assets.pal
	}
	draw := func(text string, x, y, width int) {
		if font == nil {
			c.UITextWidth(g.font, text, x, y, width, g.guiColor(15))
			return
		}
		drawRetailGAFTextLit(c, font, text, x, y, width, pal, 0)
	}
	pitch := max(metric, 1)
	for row, line := range onlineChatLayout(r.chat.lines, measure, onlineChatW, onlineChatRows) {
		draw(line, onlineChatX, onlineChatY+row*pitch, onlineChatW)
	}
	i := p.Index(lobbyChatLine)
	if i >= 0 && p.ActiveAt(i) && !r.chat.editing && p.TextAt(i) == "" && r.chatRecipients() != 0 {
		draw(onlineChatHint, onlineChatX, onlineChatLineY+3, onlineChatLineW)
	}
}
