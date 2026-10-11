package main

// The MULTI entry (DESIGN_MULTIPLAYER §16.6.2, DESIGN_INTERFACE_HUD_INPUT
// "Online games"): a chooser over MAINMENU that says what online play is and
// offers Create Game and Join Game, the room-code popup Join opens, and the
// name and server popups off the main path. The chooser is the authored
// message window's frame; the popups are retail's address window (TCP.GUI),
// recaptioned. The lobby is in online_lobby.go; the network and composition
// work in online_flow.go.

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/platform/ebitenapp"
	"github.com/nanolathe-gg/nanolathe/internal/relay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// defaultOnlineServer is the server online play uses until the player names
// another (DESIGN_MULTIPLAYER §16.6).
const defaultOnlineServer = "relay.nanolathe.gg"

// onlineClipboardAvailable reports whether the lobby can offer Copy.
func onlineClipboardAvailable() bool { return ebitenapp.HostClipboardWritable() }

type onlinePhase uint8

const (
	onlineIdle       onlinePhase = iota
	onlineDescribing             // join: reading the room's configuration
	onlineConnecting             // opening the room
	onlineInLobby
)

// onlineView is which popup of the entry is showing.
type onlineView uint8

const (
	onlineChooser     onlineView = iota // Create Game or Join Game
	onlineCodeEntry                     // the room code Join asks for
	onlineServerEntry                   // the server address
	onlineNameEntry                     // the player's display name
)

// onlineScreen is a shell's online entry and, once a room is open, its lobby
// (gameShell.online). The lobby replaces the entry's popup; leaving it opens
// the chooser again.
type onlineScreen struct {
	panel  *ui.Panel
	assets *retailPanelAssets
	view   onlineView

	phase          onlinePhase
	status, detail string
	job            *onlineJob
	// name is the player's display name, "" for none: the settings key
	// `nickname`, read when the screen opens and written when the player
	// changes it (settings.StoreNickname).
	name string

	room onlineRoomState
}

func (g *gameShell) onlinePanelActive() bool {
	return g != nil && g.online != nil && g.online.panel != nil && g.activePanel() == g.online.panel
}

// onlinePanelAssets supplies the drawing of the entry's popups — no backdrop,
// so they draw as authored panels — and the lobby's backdrop.
func (g *gameShell) onlinePanelAssets(p *ui.Panel) *retailPanelAssets {
	if g == nil || g.online == nil || p == nil {
		return nil
	}
	switch p {
	case g.online.panel:
		return g.online.assets
	case g.online.room.panel:
		return g.online.room.assets
	}
	return nil
}

// onlineServerAddress turns the typed server into a relay address: a bare
// host is the WebSocket relay at wss://host/relay; host:port is native TLS;
// a wss:// URL is taken as typed. A ws:// URL is accepted only on a numeric
// loopback address, as an explicit choice of plaintext for a relay on this
// computer. Everything else follows the command line's validation
// (validateHostedAddress).
func onlineServerAddress(typed string) (string, relay.HostedDialOptions, error) {
	address := strings.TrimSpace(typed)
	if address == "" {
		address = defaultOnlineServer
	}
	if !strings.Contains(address, "://") {
		if _, _, err := net.SplitHostPort(address); err != nil {
			address = "wss://" + address + "/relay"
		}
	}
	options := relay.HostedDialOptions{InsecureLoopback: strings.HasPrefix(strings.ToLower(address), "ws://")}
	if err := validateHostedAddress(Options{RelayAddress: address, RelayInsecureLoopback: options.InsecureLoopback}); err != nil {
		return "", options, err
	}
	return address, options, nil
}

// onlineRefusalText says in plain words why the relay or this client refused,
// for the status line. Unrecognised failures keep their own short reason.
func onlineRefusalText(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	var restricted *content.RestrictionsError
	has := func(parts ...string) bool {
		for _, part := range parts {
			if !strings.Contains(text, part) {
				return false
			}
		}
		return true
	}
	switch {
	case has("logical path room code,"):
		return "Room codes are six letters and digits."
	case has("logical path room,", "existing invitation"):
		return "No game has that code. Check the code with your friend."
	case has("unoccupied"):
		return "That game is full or has already started."
	case has("logical path room,", "an open room"):
		return "That game has closed."
	case has("logical path room host,"):
		return "The host left, so the game closed."
	case has("logical path room wait,"):
		return "The game closed after waiting 30 minutes without starting."
	case has("logical path room capacity,"), has("logical path connection capacity,"):
		return "The server is full. Try again later."
	case has("logical path hello "):
		rest := text[strings.Index(text, "logical path hello ")+len("logical path hello "):]
		field, _, _ := strings.Cut(rest, ",")
		return "Your game differs from the host's (" + field + ")."
	case has("hosted protocol version"):
		return "The server runs a different version of online play. Every player and the server need matching releases."
	case has("logical path mod,", "archive digest"):
		return "Online games need a mod installed from its archive, which identifies it to the other players. Reinstall this mod from its zip."
	case has("logical path mounted content,"):
		return "Online games need the base game or an installed mod. Restart without extra --root or --mod-config content."
	case errors.As(err, &restricted):
		return "The unit restrictions cannot be applied: " + onlineRestrictionIssues(restricted) + "."
	case has("logical path relay address,"), has("logical path relay URL,"), has("logical path WebSocket transport,"), has("logical path plaintext relay,"):
		return "That is not a server address. Use a host name such as " + defaultOnlineServer + ", host:port, or wss://host/relay."
	case has("logical path hosted dial,"), has("logical path WebSocket"), has("logical path hosted handshake,"), has("logical path envelope length,"):
		return "Could not reach the server: " + onlineCause(text)
	case has("logical path lobby,"):
		return "You left the game."
	}
	return noticeReason(err)
}

// onlineRestrictionIssues names each refused restriction and why.
func onlineRestrictionIssues(e *content.RestrictionsError) string {
	parts := make([]string, len(e.Issues))
	for i, issue := range e.Issues {
		parts[i] = issue.Unit + " (" + restrictionReasonText(issue.Reason) + ")"
	}
	return strings.Join(parts, ", ")
}

// onlineCause is the last clause of a transport failure: the network's own
// words after the diagnostic's expectation.
func onlineCause(text string) string {
	if i := strings.LastIndex(text, "message: "); i >= 0 {
		return strings.TrimSpace(text[i+len("message: "):])
	}
	if i := strings.LastIndex(text, ": "); i >= 0 {
		return strings.TrimSpace(text[i+2:])
	}
	return text
}

// ---------------------------------------------------------------------------
// The popups.

const (
	onlineCodeFont   = "fonts/hatt14.fnt"
	onlineChooserGUI = "guis/msgbox.gui"
	onlineAddressGUI = "guis/tcp.gui"
	onlineServerMax  = 100
	onlineCodeMax    = 16
	onlineIntro      = "Play online with friends: create a game and send them its code, or join with the code a friend sent you."
	onlineCodePrompt = "Enter the room code your friend sent you"
	onlineHostPrompt = "Enter the server address"
	onlineNamePrompt = "Enter the name other players see"
	onlineNameHint   = "Up to 16 letters, digits, spaces and punctuation. Leave it empty to play as Player 1, Player 2 and so on."
)

// onlinePopupTemplate loads an authored popup window from the mounted
// content. Tests substitute authored windows of the same shape.
var onlinePopupTemplate = func(g *gameShell, logical string) (*gui.Window, error) {
	window, err := g.cs.loadGUI(logical)
	if err != nil {
		return nil, retailFrontendAssetError(g.cs, "online popup GUI unavailable", logical, "the authored popup window", err)
	}
	return window, nil
}

// onlinePlace sizes and centres a popup window and its panel record.
func onlinePlace(window *gui.Window, w, h int32) {
	window.Rect = gui.Rect{X: (retailScreenW - w) / 2, Y: (retailScreenH - h) / 2, W: w, H: h}
	window.OriginX, window.OriginY = window.Rect.X, window.Rect.Y
	if len(window.Gadgets) > 0 {
		window.Gadgets[0].Rect = window.Rect
	}
}

func onlineLabel(name, text string, x, y, w, h int32, attribs uint32) gui.Gadget {
	return gui.Gadget{Kind: gui.KindLabel, Name: name, SourceName: name, Text: text, Active: 1, ColorF: 15, Attribs: attribs, Rect: gui.Rect{X: x, Y: y, W: w, H: h}}
}

// buildOnlineChooser shapes the authored message window's frame into the
// chooser: the sentence, the two prominent buttons cloned from the message
// window's OK, a status line, and below them Name and Server, each with what
// it holds beside it, and Cancel.
func buildOnlineChooser(window *gui.Window) {
	ok := gui.Gadget{Kind: gui.KindButton, Active: 1, ColorF: 15, Attribs: 2}
	if i := window.GadgetIndex("OK"); i >= 0 {
		ok = window.Gadgets[i]
	}
	button := func(name, text string, x, y, w, h int32) gui.Gadget {
		b := ok
		b.Name, b.SourceName, b.Art, b.Text, b.QuickKey, b.Labels = name, name, "", text, 0, nil
		b.Rect = gui.Rect{X: x, Y: y, W: w, H: h}
		return b
	}
	onlinePlace(window, 420, 236)
	window.Header.CrDefault, window.Header.EscDefault, window.Header.DefaultFocus = "", "PREVMENU", "CREATE"
	window.Gadgets = []gui.Gadget{
		window.Gadgets[0],
		onlineLabel("PROMPT", onlineIntro, 20, 16, 380, 48, 1),
		button("CREATE", "Create Game", 90, 76, 96, 31),
		button("JOIN", "Join Game", 234, 76, 96, 31),
		onlineLabel("STATUS", "", 20, 124, 380, 32, 1),
		button("NAME", "Name", 20, 174, 96, 20),
		onlineLabel("NAMELABEL", "", 124, 177, 172, 16, 1),
		button("SERVER", "Server", 20, 202, 96, 20),
		onlineLabel("SERVERNAME", "", 124, 205, 172, 16, 1),
		button("PREVMENU", "Cancel", 304, 202, 96, 20),
	}
}

// buildOnlineAddressWindow recaptions retail's address window: its prompt,
// its field (which takes focus and paints its own background), Join or OK,
// Cancel, and a status line under the field for refusals.
func buildOnlineAddressWindow(window *gui.Window, prompt, ok string, maxChars int16) {
	for i := range window.Gadgets {
		gad := &window.Gadgets[i]
		switch gad.Name {
		case "TEXT":
			gad.Text = prompt
		case "OK":
			gad.Text, gad.Labels, gad.QuickKey = ok, nil, 0
		case "PREV":
			gad.Text, gad.Labels, gad.QuickKey = "Cancel", nil, 0
		case "ADDRESS":
			gad.Text, gad.MaxChars, gad.Attribs = "", maxChars, 0x01
		}
	}
	window.Header.CrDefault, window.Header.EscDefault, window.Header.DefaultFocus = "OK", "PREV", "ADDRESS"
	window.Gadgets = append(window.Gadgets, onlineLabel("STATUS", "", 51, 96, 295, 46, 1))
}

func (g *gameShell) openOnlineScreenReporting() {
	if err := g.openOnlineScreen(); err != nil {
		reportRetailMessageError(g.showRetailMessage(err.Error()))
	}
}

// openOnlineScreen opens the chooser over the main menu.
func (g *gameShell) openOnlineScreen() error {
	if g == nil || g.cs == nil || g.cs.fs == nil {
		return fmt.Errorf("nanolathe: online screen: no mounted content")
	}
	if g.online != nil {
		g.closeOnlineScreen()
	}
	g.online = &onlineScreen{assets: &retailPanelAssets{}, name: storedOnlineName()}
	if err := g.showOnlineView(onlineChooser); err != nil {
		g.online = nil
		return err
	}
	return nil
}

// showOnlineView replaces the entry's popup with view's.
func (g *gameShell) showOnlineView(view onlineView) error {
	s := g.online
	logical := onlineChooserGUI
	if view != onlineChooser {
		logical = onlineAddressGUI
	}
	window, err := onlinePopupTemplate(g, logical)
	if err != nil {
		return err
	}
	switch view {
	case onlineChooser:
		buildOnlineChooser(window)
	case onlineCodeEntry:
		buildOnlineAddressWindow(window, onlineCodePrompt, "Join", onlineCodeMax)
	case onlineNameEntry:
		buildOnlineAddressWindow(window, onlineNamePrompt, "OK", session.OnlineNameMaxBytes)
	default:
		buildOnlineAddressWindow(window, onlineHostPrompt, "OK", onlineServerMax)
	}
	g.installRetailWindowButtonArt(window, nil)
	g.initializeRetailLabels(window)
	panel := ui.NewPanel(window)
	if panel == nil {
		return fmt.Errorf("nanolathe: online screen: panel construction failed")
	}
	if s.panel != nil && g.frontend.Panels.Top() == s.panel {
		s.panel.ResetPress()
		g.frontend.Panels.Pop()
	}
	s.panel, s.view = panel, view
	g.frontend.Panels.Push(panel)
	flushWindowTokens(clPtr)
	switch view {
	case onlineCodeEntry:
		panel.FocusEditor(panel.Index("ADDRESS"))
	case onlineServerEntry:
		server := g.onlineServer
		if server == "" {
			server = defaultOnlineServer
		}
		panel.SetText("ADDRESS", server)
		panel.FocusEditor(panel.Index("ADDRESS"))
	case onlineNameEntry:
		panel.SetText("ADDRESS", s.name)
		panel.FocusEditor(panel.Index("ADDRESS"))
	}
	g.refreshOnlinePanel()
	return nil
}

// showOnlineViewReporting shows view, or says on the status line why not.
func (g *gameShell) showOnlineViewReporting(view onlineView) {
	if err := g.showOnlineView(view); err != nil {
		g.online.status = onlineRefusalText(err)
		g.refreshOnlinePanel()
	}
}

// closeOnlineScreen leaves the online entry. A running job is abandoned (its
// room, if it opens one, is closed) and an open lobby is left.
func (g *gameShell) closeOnlineScreen() {
	s := g.online
	if s == nil {
		return
	}
	s.cancelJob()
	g.closeOnlineRoom()
	if g.frontend != nil && s.panel != nil && g.frontend.Panels.Top() == s.panel {
		s.panel.ResetPress()
		g.frontend.Panels.Pop()
	}
	g.online = nil
	flushWindowTokens(clPtr)
}

// onlineServerLabel is the server as the player chose it, for the chooser.
func (g *gameShell) onlineServerLabel() string {
	if g.onlineServer == "" {
		return defaultOnlineServer
	}
	return g.onlineServer
}

// refreshOnlinePanel writes the showing popup's status and greys what the
// current phase cannot do.
func (g *gameShell) refreshOnlinePanel() {
	s := g.online
	if s == nil || s.panel == nil {
		return
	}
	p := s.panel
	busy := s.phase != onlineIdle
	switch s.view {
	case onlineChooser:
		p.SetText("STATUS", g.fitDetail(s.status, 380, 2))
		p.SetText("NAMELABEL", g.fitOnlineLine(onlineNameLabel(s.name), 172))
		p.SetText("SERVERNAME", g.fitOnlineLine("Server: "+g.onlineServerLabel(), 172))
		retailGreyGadget(p.Window, "CREATE", busy)
		retailGreyGadget(p.Window, "JOIN", busy)
		retailGreyGadget(p.Window, "NAME", busy)
		retailGreyGadget(p.Window, "SERVER", busy)
	default:
		status := s.status
		switch {
		case status != "":
		case s.view == onlineCodeEntry:
			status = "Paste or type the six letters and digits, then choose Join."
		case s.view == onlineNameEntry:
			status = onlineNameHint
		}
		p.SetText("STATUS", g.fitDetail(status, 295, 3))
		retailGreyGadget(p.Window, "OK", busy)
	}
}

// fitOnlineLine is text on one line of width in the label face, cut short
// with "..." where it does not fit: a server address has no spaces to break
// at, so it loses its end rather than all of itself.
func (g *gameShell) fitOnlineLine(text string, width int) string {
	measure, _, ok := g.retailLabelTextMetrics()
	if !ok {
		measure = g.retailTextWidth
	}
	return onlineFitCaption(text, "", measure, width)
}

// onlineNameLabel is what the chooser shows beside Name.
func onlineNameLabel(name string) string {
	if name == "" {
		return "Name: none chosen"
	}
	return "Name: " + name
}

// storedOnlineName is the settings key `nickname`, the display name the player
// chose last; "" for none or an unreadable file.
func storedOnlineName() string {
	s, err := settings.Load()
	if err != nil {
		return ""
	}
	return s.Nickname
}

// onlineNameRefusal says in plain words why typed cannot be a display name,
// or "" when it can (session.ValidateOnlineName). Spaces at either end are
// trimmed first, and an empty name chooses none.
func onlineNameRefusal(typed string) string {
	name := strings.TrimSpace(typed)
	switch {
	case name == "":
		return ""
	case len(name) > session.OnlineNameMaxBytes:
		return fmt.Sprintf("A name can be at most %d characters long.", session.OnlineNameMaxBytes)
	case session.ValidateOnlineName(name) != nil:
		return "A name can use letters, digits, spaces and punctuation, but not < or >."
	}
	return ""
}

// rememberOnlineName keeps typed, trimmed, as the player's display name for
// this visit and the next.
func (g *gameShell) rememberOnlineName(typed string) {
	name := strings.TrimSpace(typed)
	if name == g.online.name {
		return
	}
	g.online.name = name
	if err := settings.StoreNickname(name); err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: %v\n", err)
	}
}

// syncOnlineCodeField shows a typed or pasted room code in capitals, as the
// relay spells codes; spaces and dashes stay until Join normalizes them. It
// runs every shell step the online screen is open, which is also when a room
// opening or open asks a browser page to keep following it while hidden
// (onlineBackgroundStep).
func (g *gameShell) syncOnlineCodeField() {
	s := g.online
	if s != nil && (s.job != nil || s.room.lobby != nil) {
		watchOnlineBackground(g, clPtr)
	}
	if s == nil || s.panel == nil || s.view != onlineCodeEntry {
		return
	}
	if text := s.panel.TextOf("ADDRESS"); text != strings.ToUpper(text) {
		s.panel.SetText("ADDRESS", strings.ToUpper(text))
	}
}

// drawOnlineExtras paints what the online windows add to the authored
// painters: an empty, uncaptured field's background, which the text-box
// painter skips with its text, and the lobby's room code and chat.
func (g *gameShell) drawOnlineExtras(c *client.Client, p *ui.Panel) {
	s := g.online
	if s == nil || p == nil || p.Window == nil {
		return
	}
	if p == s.panel {
		if i := p.Index("ADDRESS"); i >= 0 && p.TextAt(i) == "" && !(p.EditorCaptured() && p.EditorIndex() == i) {
			r := p.Window.PlacedRect(i)
			c.UIFillRect(int(r.X), int(r.Y), int(r.W), int(r.H), 0)
		}
		return
	}
	if p == s.room.panel && s.room.lobby != nil {
		g.drawOnlineRoomCode(c, p)
		g.drawOnlineChat(c, p)
	}
}

// drawOnlineRoomCode draws the lobby's room code in the larger face, in the
// GUI's heading colour, spaced and split in two groups of three so it can be
// read aloud.
func (g *gameShell) drawOnlineRoomCode(c *client.Client, p *ui.Panel) {
	room := &g.online.room
	i := p.Index(lobbyCode)
	if i < 0 {
		return
	}
	font := room.codeFont
	if font == nil {
		font = g.font
	}
	if font == nil {
		return
	}
	r := p.Window.PlacedRect(i)
	color := g.guiColor(onlineHeadingColor)
	step := client.MeasureText(font, "W") + 6
	x := int(r.X)
	for n, ch := range room.lobby.Code() {
		if n == 3 {
			x += step / 2
		}
		c.UITextWidth(font, string(ch), x, int(r.Y), -1, color)
		x += step
	}
}

// onlineHeadingColor is the GUI colour field the room code is drawn in: the
// SELMAP window's own heading colour, the gold of the Mods & Mutators list
// headings.
const onlineHeadingColor = 0xcd

// activateOnlineScreenGadget routes a button of the entry's popups.
func (g *gameShell) activateOnlineScreenGadget(name string) bool {
	if !g.onlinePanelActive() {
		return false
	}
	s := g.online
	g.playMenuCue("SmallButton")
	switch s.view {
	case onlineChooser:
		switch name {
		case "CREATE":
			g.startOnlineCreate()
		case "JOIN":
			s.status = ""
			g.showOnlineViewReporting(onlineCodeEntry)
		case "SERVER":
			s.status = ""
			g.showOnlineViewReporting(onlineServerEntry)
		case "NAME":
			s.status = ""
			g.showOnlineViewReporting(onlineNameEntry)
		case "PREVMENU":
			// Cancel stops a room still opening; otherwise it leaves.
			if s.job != nil {
				s.cancelJob()
				g.onlineIdleStatus("")
				return true
			}
			g.closeOnlineScreen()
		}
	case onlineCodeEntry:
		switch name {
		case "ADDRESS":
			// Escape in the field clears it and fires it empty: nothing to
			// join, and a second Escape cancels.
			if strings.TrimSpace(s.panel.TextOf("ADDRESS")) != "" {
				g.startOnlineJoin()
			}
		case "OK":
			g.startOnlineJoin()
		case "PREV":
			s.cancelJob()
			s.phase, s.status = onlineIdle, ""
			g.showOnlineViewReporting(onlineChooser)
		}
	case onlineServerEntry:
		switch name {
		case "OK", "ADDRESS":
			typed := s.panel.TextOf("ADDRESS")
			if name == "ADDRESS" && strings.TrimSpace(typed) == "" {
				return true // Escape cleared the field; OK on it means the default
			}
			if _, _, err := onlineServerAddress(typed); err != nil {
				s.status = onlineRefusalText(err)
				g.refreshOnlinePanel()
				return true
			}
			g.rememberOnlineServer(typed)
			s.status = ""
			g.showOnlineViewReporting(onlineChooser)
		case "PREV":
			s.status = ""
			g.showOnlineViewReporting(onlineChooser)
		}
	case onlineNameEntry:
		switch name {
		case "OK", "ADDRESS":
			// Enter in the field is OK; an emptied field chooses no name.
			// Escape is the window's Cancel, which keeps the old one.
			typed := s.panel.TextOf("ADDRESS")
			if refusal := onlineNameRefusal(typed); refusal != "" {
				s.status = refusal
				g.refreshOnlinePanel()
				s.panel.FocusEditor(s.panel.Index("ADDRESS"))
				return true
			}
			g.rememberOnlineName(typed)
			s.status = ""
			g.showOnlineViewReporting(onlineChooser)
		case "PREV":
			s.status = ""
			g.showOnlineViewReporting(onlineChooser)
		}
	}
	return true
}

// activateOnlineGadget routes a button on the online entry or its lobby.
func (g *gameShell) activateOnlineGadget(name string) bool {
	return g.activateOnlineLobbyGadget(name) || g.activateOnlineScreenGadget(name)
}

// rememberOnlineServer keeps the chosen server for the next visit.
func (g *gameShell) rememberOnlineServer(typed string) {
	typed = strings.TrimSpace(typed)
	if typed == defaultOnlineServer {
		typed = ""
	}
	if typed != g.onlineServer {
		g.onlineServer = typed
		g.saveSettings()
	}
}
