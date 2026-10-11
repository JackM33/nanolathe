package main

// In-battle chat over the hosted relay (DESIGN_MULTIPLAYER §3.3 "Chat",
// §12.2). The retail contract is [07 §5 "Chat"]: a committed TALK line is
// composed as `<name> text`, sent to the recipients its mode or one-line
// prefix selects, then posted locally with class 4 and no speaker; a received
// line is posted with class 8 and its sender's slot as speaker, which draws
// the sender's logo and plays MessageArrived [07 R-FE-02 §12]
// [07 R-HUD-03 §14.3]. A line travels only through the hosted connection's
// chat messages: it never enters the lockstep stream, a checksum, the
// simulation or a replay, and both directions run on the host step outside
// every tick [I6]. The loopback play test and replay playback have no chat
// link, so their lines are posted locally only.

import (
	"errors"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/lockstep"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/relay"
)

// The message ring's chat fields [07 R-HUD-03 §14.3].
const (
	chatClassLocal    uint8 = 4  // the ordinary chat commit [07 R-FE-02 §12]
	chatClassReceived uint8 = 8  // a received chat packet [07 §5 "Chat"]
	chatNoSpeaker     uint8 = 10 // the no-speaker sentinel: no logo, no cue
)

// chatRecipientMode is retail's persistent recipient-mode byte
// [07 §5 "Chat"]: every eligible other player, the players the local player
// has declared alliance toward in its own row A, the players it has not, or
// the custom per-player marks the one-line prefix writes. The battle opens
// TALK.GUI, whose form cannot change the mode, so it stays chatToAll; see the
// TODO(question) at openTalk for the expanded form's unknowns.
type chatRecipientMode uint8

const (
	chatToAll chatRecipientMode = iota
	chatToAllies
	chatToEnemies
	chatToCustom
)

// onlineChatLink is the hosted relay connection's chat (relay.LocalClient):
// SendChat addresses relay seats, bit n naming seat n, and DrainChat returns
// the lines received since the last call with their senders' seats.
type onlineChatLink interface {
	SendChat(recipients uint16, text string) error
	DrainChat(dst []relay.HostedChatLine) []relay.HostedChatLine
}

// onlineChat is an online battle's chat state.
type onlineChat struct {
	link  onlineChatLink
	seats onlineChatSeats
	inbox []relay.HostedChatLine // DrainChat's reused buffer
}

// onlineChatSeats maps battle slots to relay seats. The relay numbers seats
// as players arrive in the room and keeps those numbers after Start, while
// the battle seats the present players, in seat order, as its first slots
// and the host's computers after them, so a seat's slot is its rank among the
// seats present at Start (relay.HostedLobbyState.Slot, onlineSeatsOf). The
// lobby's start supplies the map (adoptOnlineChatSeats). Without one, as for
// the command-line hosted play test whose two seats are both present, slot n
// of a human row is seat n.
type onlineChatSeats struct {
	adopted bool
	seat    [frame.PlayerRowSlots]int8 // by slot; -1 for none
}

// onlineChatSeatsOf is the map for the room state at Start.
func onlineChatSeatsOf(state relay.HostedLobbyState) onlineChatSeats {
	m := onlineChatSeats{adopted: true}
	for i := range m.seat {
		m.seat[i] = -1
	}
	slot := 0
	for seat := 0; seat < relay.HostedMaxSeats && slot < len(m.seat); seat++ {
		if state.Seats[seat].Present {
			m.seat[slot] = int8(seat)
			slot++
		}
	}
	return m
}

// adoptOnlineChatSeats installs the seat map of the room state the battle
// started from. The online lobby's start calls it once the battle has its
// multiplayer driver.
func (b *battleSession) adoptOnlineChatSeats(state relay.HostedLobbyState) {
	if b != nil && b.multiplayer != nil {
		b.multiplayer.chat.seats = onlineChatSeatsOf(state)
	}
}

// seatOf is slot's relay seat when the slot is a human row.
func (s onlineChatSeats) seatOf(rows *[frame.PlayerRowSlots]frame.PlayerRow, slot int) (uint8, bool) {
	if slot < 0 || slot >= len(rows) || !chatHumanRow(rows[slot]) {
		return 0, false
	}
	if !s.adopted {
		return uint8(slot), true
	}
	if s.seat[slot] < 0 {
		return 0, false
	}
	return uint8(s.seat[slot]), true
}

// slotOf is the human slot seated at a relay seat.
func (s onlineChatSeats) slotOf(rows *[frame.PlayerRowSlots]frame.PlayerRow, seat uint8) (int, bool) {
	for slot := range rows {
		if got, ok := s.seatOf(rows, slot); ok && got == seat {
			return slot, true
		}
	}
	return 0, false
}

// chatHumanRow is an occupied player record a human controls: controller
// byte 1 (local) or 3 (remote), not 2 (computer) [05 R-SHARE-01 §1].
func chatHumanRow(row frame.PlayerRow) bool {
	return row.Present && (row.Controller == 1 || row.Controller == 3)
}

// onlineChatLinkOf finds the hosted connection's chat under the battle's
// client wrappers: the network measurements and the replay recorder pass
// neither SendChat nor DrainChat through, so this looks beneath them. The
// loopback play-test connection is never wrapped in measurements, so it has
// no link.
func onlineChatLinkOf(c lockstep.Client) onlineChatLink {
	for c != nil {
		if link, ok := c.(onlineChatLink); ok {
			return link
		}
		switch w := c.(type) {
		case *onlineNetStats:
			c = w.Client
		case reportingOnlineReplayClient:
			link, _ := w.reporter.(onlineChatLink)
			return link
		default:
			return nil
		}
	}
	return nil
}

// onlineChatLink is the battle's chat link, or nil offline, in the loopback
// play test and in replay playback.
func (b *battleSession) onlineChatLink() onlineChatLink {
	if b == nil || b.multiplayer == nil {
		return nil
	}
	m := b.multiplayer
	if m.chat.link == nil && m.net != nil {
		m.chat.link = onlineChatLinkOf(m.net.Client)
	}
	return m.chat.link
}

// chatRoster is the committed player rows a line is addressed and named by
// [I6], or the session's records before the first publication.
func (b *battleSession) chatRoster() *[frame.PlayerRowSlots]frame.PlayerRow {
	if cur, ok := b.currentSnapshot(); ok && int(b.sess.LocalOwner) < len(cur.Players) && cur.Players[b.sess.LocalOwner].Present {
		return &cur.Players
	}
	var rows [frame.PlayerRowSlots]frame.PlayerRow
	if b != nil && b.sess != nil && b.sess.Econ != nil {
		for i := range rows {
			p := &b.sess.Econ.Players[i]
			rows[i] = frame.PlayerRow{Present: p.Exists, Name: p.Name, Logo: p.Logo, Controller: p.ControllerState, Side: p.Side, Watcher: p.Watcher || p.IsObserver, Allies: p.Allies}
		}
	}
	return &rows
}

// localChatWatcher reports a local player who may not open chat: one with
// the watcher bit [07 §5 "Chat"] or, online, one whose own result is final,
// which the committed frame carries for this seat.
func (b *battleSession) localChatWatcher() bool {
	if b == nil || b.sess == nil {
		return false
	}
	if sessionLocalIsWatcher(b.sess) {
		return true
	}
	cur, ok := b.currentSnapshot()
	return ok && b.multiplayer != nil && cur.Result.Ended
}

// chatPrefix is a line's one-line recipient prefix [07 §5 "Chat"].
type chatPrefix struct {
	present bool
	mode    chatRecipientMode // chatToAllies, chatToEnemies or chatToCustom
	digit   int               // chatToCustom's digit, 0..9
}

// parseChatPrefix reads the one-line recipient prefix: when the line's
// second character is `,`, `:` or `;`, a first character that is a digit
// sends the line to one slot, and `a` or `e` in either case to the local
// player's allies or enemies; those two characters are stripped. Any other
// line keeps its text and the stored mode [07 §5 "Chat"].
func parseChatPrefix(text string) (chatPrefix, string) {
	if len(text) < 2 || text[1] != ',' && text[1] != ':' && text[1] != ';' {
		return chatPrefix{}, text
	}
	switch c := text[0]; {
	case c >= '0' && c <= '9':
		return chatPrefix{present: true, mode: chatToCustom, digit: int(c - '0')}, text[2:]
	case c == 'a' || c == 'A':
		return chatPrefix{present: true, mode: chatToAllies}, text[2:]
	case c == 'e' || c == 'E':
		return chatPrefix{present: true, mode: chatToEnemies}, text[2:]
	}
	return chatPrefix{}, text
}

// chatPrefixSlot is the slot a prefix digit names.
//
// TODO(question): [07 §5 "Chat"] says the line "goes to that one slot" but
// not whether the digit is the slot index (0..9) or the slot plus one, as
// players are numbered on screen ("Player 1" is slot 0). This reads the
// digit as the slot index. A trace of the prefix parser's digit conversion,
// or a retail two-machine observation of "1:" in a two-player game, would
// settle it.
func chatPrefixSlot(digit int) int { return digit }

// chatRecipients resolves a line's relay seats: every seated other player,
// the players the local row A does or does not ally, or the marked slots,
// each by its seat when the slot has one. A slot without an identity has no
// seat: a computer, an empty record, or a human whose connection the relay
// has dropped. A marked slot without one discards the line, unsent and
// unposted (ok false); the other modes simply skip it. Watchers are
// recipients like anyone else [07 §5 "Chat"].
func chatRecipients(rows *[frame.PlayerRowSlots]frame.PlayerRow, local int, mode chatRecipientMode, marks [frame.PlayerRowSlots]bool, seats onlineChatSeats, progress *relay.HostedMatchProgress) (recipients uint16, ok bool) {
	for slot := range rows {
		want := false
		switch mode {
		case chatToCustom:
			want = marks[slot]
		case chatToAll:
			want = slot != local && rows[slot].Present
		case chatToAllies:
			want = slot != local && rows[slot].Present && rows[local].Allies[slot]
		case chatToEnemies:
			want = slot != local && rows[slot].Present && !rows[local].Allies[slot]
		}
		if !want {
			continue
		}
		seat, seated := seats.seatOf(rows, slot)
		if seated && progress != nil && slot < len(progress.Seats) && !progress.Seats[slot].Playing {
			seated = false
		}
		if !seated {
			if mode == chatToCustom {
				return 0, false
			}
			continue
		}
		if slot != local {
			recipients |= 1 << seat
		}
	}
	return recipients, true
}

// The refusals the sender finds before it reaches the link; chatRefusalText
// words every refusal for the message column, within its 63 characters.
var (
	errChatClosed  = errors.New("nanolathe: chat refused: logical path chat link, providers searched [hosted relay], expected an open game connection")
	errChatTooLong = errors.New("nanolathe: chat refused: logical path chat line, providers searched [hosted relay], expected at most 128 bytes")
	errChatText    = errors.New("nanolathe: chat refused: logical path chat line, providers searched [hosted relay], expected printable ASCII")
)

// chatRefusalText is a refused line's local message.
func chatRefusalText(err error) string {
	switch {
	case errors.Is(err, relay.ErrHostedChatRate):
		return "Chat not sent: too many lines at once. Wait a moment."
	case errors.Is(err, errChatTooLong):
		return "Chat not sent: the line is too long."
	case errors.Is(err, errChatText):
		return "Chat not sent: it has characters that cannot be sent."
	case errors.Is(err, errChatClosed):
		return "Chat not sent: the game connection is closed."
	}
	return "Chat not sent: the game connection refused it."
}

// sendChat is the chat sender. Offline, in the loopback play test and in
// replay playback the line is posted locally only, as single-player posts it.
// Online it reads the one-line prefix when prefixed, forces everyone when
// everyone (a dispatched cheat), sends the text to the recipients' seats and
// then posts `<name> text` locally; the stored mode is never changed. The
// receivers compose `<name>` from their own copy of the sender's record, so
// only the text travels. A line no seat receives is still posted, and the
// relay carries no empty line, so a prefix with nothing after it is posted
// only. A refused line is not posted: its refusal is, in plain words.
func (b *battleSession) sendChat(text string, prefixed, everyone bool) error {
	link := b.onlineChatLink()
	if link == nil {
		b.postLocalChat(text)
		return nil
	}
	mode := b.chat.mode
	var marks [frame.PlayerRowSlots]bool
	if prefixed {
		prefix, rest := parseChatPrefix(text)
		if prefix.present {
			mode, text = prefix.mode, rest
			if prefix.mode == chatToCustom {
				marks[chatPrefixSlot(prefix.digit)] = true
			}
		}
	}
	if everyone {
		mode = chatToAll
	}
	m := b.multiplayer
	var progress *relay.HostedMatchProgress
	if m.net != nil {
		if r, ok := m.net.Client.(onlineProgressReporter); ok {
			if p, reported := r.Progress(); reported {
				progress = &p
			}
		}
	}
	recipients, ok := chatRecipients(b.chatRoster(), int(b.sess.LocalOwner), mode, marks, m.chat.seats, progress)
	if !ok {
		return nil
	}
	if recipients != 0 && text != "" {
		err := errChatClosed
		switch {
		case m.driver == nil || m.failure != nil:
		case len(text) > relay.HostedMaxChatBytes:
			err = errChatTooLong
		case relay.ValidHostedChat(text) != nil:
			err = errChatText
		default:
			err = link.SendChat(recipients, text)
		}
		if err != nil {
			b.onlineNotice(chatRefusalText(err))
			return err
		}
	}
	b.postLocalChat(text)
	return nil
}

// sendChatLine sends text, a line already composed after the chat composer's
// `<name> `, through the chat sender to the current recipient mode's
// recipients, and posts `<name> text` locally with class 4 and no speaker, so
// it plays no arrival cue. It reads no recipient prefix and no + command. The
// Allies window's announcement is such a line: the text is a space, the
// translated `allied with` or `broke alliance with`, a space and the target's
// name, sent after the flip [07 R-FE-01 §7]. A refusal is posted as a short
// local message and returned.
func (b *battleSession) sendChatLine(text string) error {
	if b == nil || b.sess == nil {
		return nil
	}
	return b.sendChat(text, false, false)
}

// playChatArrival plays the arrival cue for a received line; tests count it.
var playChatArrival = func(b *battleSession) { b.playUICue(nil, "MessageArrived") }

// drainOnlineChat posts every chat line received since the last frame as
// `<sender name> text`, class 8, with the sender's slot as speaker, so the
// column stamps the sender's logo and the ring plays MessageArrived once for
// each line it accepts [07 §5 "Chat"][07 R-HUD-03 §14.3]. A received line is
// only displayed, never dispatched [07 R-CAM-01 §6]. It runs on the host
// step, never inside a tick.
func (b *battleSession) drainOnlineChat() {
	link := b.onlineChatLink()
	if link == nil {
		return
	}
	m := b.multiplayer
	m.chat.inbox = link.DrainChat(m.chat.inbox[:0])
	if len(m.chat.inbox) == 0 {
		return
	}
	rows := b.chatRoster()
	ring := b.messageRing()
	tick := b.currentTick()
	for _, line := range m.chat.inbox {
		slot, ok := m.chat.seats.slotOf(rows, line.Seat)
		if !ok || ring == nil {
			continue
		}
		if ring.Append("<"+rows[slot].Name+"> "+line.Text, chatClassReceived, pool.Handle(0), uint8(slot), tick) {
			playChatArrival(b)
		}
	}
	clear(m.chat.inbox)
}
