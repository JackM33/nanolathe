package relay

import (
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/netproto"
)

// Display names and chat, hosted protocol version 8 (DESIGN_MULTIPLAYER
// §12.2). The relay forwards a line to the seats its sender names and
// interprets nothing in it. A line never enters the stream: it is never a
// submit, grant, acknowledgement or checksum, and no tick depends on it.
// This is Nanolathe online policy, not a retail mechanic.

const (
	// HostedMaxChatBytes bounds a chat line's text; HostedMaxNameBytes a
	// display name's.
	HostedMaxChatBytes = 128
	HostedMaxNameBytes = 16
	// A seat may send a burst of hostedChatBurst lines, then one line each
	// hostedChatInterval. The relay drops the excess; a client refuses it
	// before sending.
	hostedChatBurst    = 5
	hostedChatInterval = time.Second
	// hostedChatInbox is how many unread lines a client keeps.
	hostedChatInbox = 64
	// A frame the room can do without is dropped once its peer already holds
	// this many queued frames or would hold more than this many bytes, so it
	// never takes the room grants need under the peer's queue bounds.
	hostedOfferFrames = 16
	hostedOfferBytes  = 64 << 10
)

// HostedChatLine is one received chat line and its sender's seat.
type HostedChatLine struct {
	Seat uint8
	Text string
}

// ErrHostedChatRate is SendChat's refusal of a line beyond the sender's rate
// limit: a burst of five lines, then one line each 1.25 seconds, a little
// slower than the relay allows. Nothing is sent.
var ErrHostedChatRate = hostedError("chat rate", "at most 5 lines at once, then one line each 1.25 seconds")

// ValidHostedName reports whether name may be a display name: at most 16
// bytes, each printable ASCII (0x20..0x7E) other than < and >, with no
// leading or trailing space. The empty name means none.
func ValidHostedName(name string) error {
	if len(name) > HostedMaxNameBytes {
		return hostedError("player name", "at most 16 bytes")
	}
	for i := 0; i < len(name); i++ {
		if c := name[i]; c < 0x20 || c > 0x7e || c == '<' || c == '>' {
			return hostedError("player name", "printable ASCII other than < and >")
		}
	}
	if name != "" && (name[0] == ' ' || name[len(name)-1] == ' ') {
		return hostedError("player name", "no leading or trailing space")
	}
	return nil
}

// ValidHostedChat reports whether text may be a chat line: 1 to 128 bytes,
// each printable ASCII (0x20..0x7E).
func ValidHostedChat(text string) error {
	if len(text) == 0 || len(text) > HostedMaxChatBytes {
		return hostedError("chat line", "1 to 128 bytes")
	}
	for i := 0; i < len(text); i++ {
		if c := text[i]; c < 0x20 || c > 0x7e {
			return hostedError("chat line", "printable ASCII")
		}
	}
	return nil
}

// hostedChatRate is one sender's rate limit, a burst of hostedChatBurst
// lines then one line each interval: hostedChatInterval at the relay, and
// the longer hostedClientChatInterval at the sending client, so network
// jitter that bunches a client's lines never takes them past the relay's
// limit. next is when the line after the last allowed one would be due if
// every line were spaced evenly; a line is allowed while that is at most
// burst-1 intervals ahead. Host wall time is fine here: chat changes no
// simulation state.
type hostedChatRate struct {
	next  time.Time
	every time.Duration // zero means hostedChatInterval
}

// hostedClientChatInterval is the sending client's spacing, a quarter
// longer than the relay's.
const hostedClientChatInterval = hostedChatInterval * 5 / 4

func (r *hostedChatRate) allow(now time.Time) bool {
	every := r.every
	if every == 0 {
		every = hostedChatInterval
	}
	due := r.next
	if due.Before(now) {
		due = now
	}
	if due.Sub(now) > (hostedChatBurst-1)*every {
		return false
	}
	r.next = due.Add(every)
	return true
}

// hostedRecipientMask holds a bit for each seat a client may name.
const hostedRecipientMask = 1<<HostedMaxSeats - 1

// encodeHostedChatRequest is a client's line: its recipients, bit n naming
// seat n, then its text.
func encodeHostedChatRequest(recipients uint16, text string) []byte {
	var w netproto.Writer
	w.U8(hostedChatMessage)
	w.U16(recipients)
	w.Text(text)
	return w.Bytes()
}

// decodeHostedChatRequest reads a client's line. Recipient bits past the
// room's seats are kept: the relay ignores them, as it ignores bits naming
// the sender or a seat that cannot receive chat.
func decodeHostedChatRequest(body []byte) (uint16, string, error) {
	r := netproto.NewReader(body, hostedError)
	if r.U8() != hostedChatMessage {
		r.Abort(hostedError("chat line", "a chat message"))
	}
	recipients := r.U16()
	text := r.Text(HostedMaxChatBytes)
	if err := r.End(); err != nil {
		return 0, "", err
	}
	if err := ValidHostedChat(text); err != nil {
		return 0, "", err
	}
	return recipients, text, nil
}

// encodeHostedChatLine is the relay's delivery: the sender's seat, then the
// text.
func encodeHostedChatLine(seat uint8, text string) []byte {
	var w netproto.Writer
	w.U8(hostedChatMessage)
	w.U8(seat)
	w.Text(text)
	return w.Bytes()
}

func decodeHostedChatLine(body []byte) (HostedChatLine, error) {
	r := netproto.NewReader(body, hostedError)
	if r.U8() != hostedChatMessage {
		r.Abort(hostedError("chat line", "a chat message"))
	}
	line := HostedChatLine{Seat: r.U8(), Text: r.Text(HostedMaxChatBytes)}
	if line.Seat >= HostedMaxSeats {
		r.Abort(hostedError("chat line", "a sender seat below 10"))
	}
	if err := r.End(); err != nil {
		return HostedChatLine{}, err
	}
	if err := ValidHostedChat(line.Text); err != nil {
		return HostedChatLine{}, err
	}
	return line, nil
}

// SendChat sends a chat line to the seats recipients names, bit n naming seat
// n; seats keep their numbers after Start. The relay ignores bits naming this
// seat, an absent or departed seat, or a seat whose client cannot receive
// chat. SendChat refuses text ValidHostedChat refuses, a bit past the tenth
// seat and a line beyond the rate limit (ErrHostedChatRate), sending nothing.
// It may be called from any goroutine, in the lobby or the battle.
func (c *LocalClient) SendChat(recipients uint16, text string) error {
	if c == nil {
		return localError("client", "a connected client")
	}
	if c.version < hostedChatVersion {
		return hostedError("chat", "a hosted relay connection")
	}
	if recipients&^hostedRecipientMask != 0 {
		return hostedError("chat recipients", "seats 0 to 9")
	}
	if err := ValidHostedChat(text); err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.chatRate.every = hostedClientChatInterval
	if !c.chatRate.allow(time.Now()) {
		return ErrHostedChatRate
	}
	return c.writeFrame(encodeHostedChatRequest(recipients, text))
}

// DrainChat appends the chat lines received since the last call, oldest
// first, to dst and returns it. The client keeps at most 64 unread lines,
// dropping the oldest. It never waits for the reader. A lobby and the battle
// client it hands over share one inbox.
func (c *LocalClient) DrainChat(dst []HostedChatLine) []HostedChatLine {
	if c == nil {
		return dst
	}
	c.chatMu.Lock()
	defer c.chatMu.Unlock()
	dst = append(dst, c.chat...)
	clear(c.chat)
	c.chat = c.chat[:0]
	return dst
}

// recordChat keeps a received line for DrainChat. The relay sends chat only
// to version-8 seats, so a line on another connection is a protocol error.
func (c *LocalClient) recordChat(body []byte) error {
	if c.version < hostedChatVersion {
		return hostedError("server message", "no chat line to a seat that cannot receive chat")
	}
	line, err := decodeHostedChatLine(body)
	if err != nil {
		return err
	}
	c.chatMu.Lock()
	defer c.chatMu.Unlock()
	if len(c.chat) == hostedChatInbox {
		copy(c.chat, c.chat[1:])
		c.chat = c.chat[:len(c.chat)-1]
	}
	c.chat = append(c.chat, line)
	return nil
}
