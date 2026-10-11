package main

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/relay"
)

// fakeChatLink is a hosted connection's chat.
type fakeChatLink struct {
	sent  []fakeChatSend
	inbox []relay.HostedChatLine
	err   error
}

type fakeChatSend struct {
	recipients uint16
	text       string
}

func (f *fakeChatLink) SendChat(recipients uint16, text string) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, fakeChatSend{recipients, text})
	return nil
}

func (f *fakeChatLink) DrainChat(dst []relay.HostedChatLine) []relay.HostedChatLine {
	dst = append(dst, f.inbox...)
	f.inbox = nil
	return dst
}

// chatTestRows are an online battle's records as slot 0 sees them: Alice
// (local), Bob her ally and Cara her enemy, humans; an allied and a hostile
// computer; and four empty slots.
func chatTestRows() [frame.PlayerRowSlots]frame.PlayerRow {
	var rows [frame.PlayerRowSlots]frame.PlayerRow
	for slot, name := range []string{"Alice", "Bob", "Cara", "Allied AI", "Hostile AI"} {
		rows[slot] = frame.PlayerRow{Present: true, Name: name, Controller: 1}
	}
	rows[3].Controller, rows[4].Controller = 2, 2
	rows[0].Allies[0], rows[0].Allies[1], rows[0].Allies[3] = true, true, true
	return rows
}

// chatTestState is a room whose seats 0, 3 and 4 were present at Start, as
// slots 0, 1 and 2.
func chatTestState() relay.HostedLobbyState {
	var state relay.HostedLobbyState
	for _, seat := range []int{0, 3, 4} {
		state.Seats[seat].Present = true
	}
	return state
}

func chatTestSeats() onlineChatSeats { return onlineChatSeatsOf(chatTestState()) }

// chatTestBattle is an online battle at slot 0 with a fake chat link, the
// committed rows of chatTestRows and seats of chatTestSeats, counting cues.
func chatTestBattle(t *testing.T) (*battleSession, *fakeChatLink, *int) {
	t.Helper()
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	t.Cleanup(b.cl.Close)
	b.cl.MessageRing().TextLines = 30
	b.sess.Econ = &economy.Service{}
	rows := chatTestRows()
	for slot, row := range rows {
		p := &b.sess.Econ.Players[slot]
		p.Exists, p.Name, p.ControllerState, p.Allies = row.Present, row.Name, row.Controller, row.Allies
	}
	staging := b.sess.Snapshot.BeginWrite()
	staging.Players = rows
	if err := b.sess.Snapshot.Publish(1); err != nil {
		t.Fatal(err)
	}
	link := &fakeChatLink{}
	b.multiplayer = &battleMultiplayer{driver: &testLocalBattleDriver{}, chat: onlineChat{link: link}}
	b.adoptOnlineChatSeats(chatTestState())
	cues := 0
	previous := playChatArrival
	playChatArrival = func(*battleSession) { cues++ }
	t.Cleanup(func() { playChatArrival = previous })
	return b, link, &cues
}

func chatLines(b *battleSession) []frame.MessageLine { return b.cl.MessageRing().Visible() }

// The one-line prefix: a digit, a or e (either case) followed by `,`, `:` or
// `;` selects the recipients and loses both characters; anything else is
// ordinary text [07 §5 "Chat"].
func TestChatPrefixParsing(t *testing.T) {
	for _, c := range []struct {
		line, rest string
		want       chatPrefix
	}{
		{"a,hi", "hi", chatPrefix{present: true, mode: chatToAllies}},
		{"A:hi", "hi", chatPrefix{present: true, mode: chatToAllies}},
		{"e;gg", "gg", chatPrefix{present: true, mode: chatToEnemies}},
		{"E,", "", chatPrefix{present: true, mode: chatToEnemies}},
		{"3:go", "go", chatPrefix{present: true, mode: chatToCustom, digit: 3}},
		{"0;x", "x", chatPrefix{present: true, mode: chatToCustom, digit: 0}},
		{"x:hello", "x:hello", chatPrefix{}},
		{"ab", "ab", chatPrefix{}},
		{"3", "3", chatPrefix{}},
		{"+atm", "+atm", chatPrefix{}},
		{" a,hi", " a,hi", chatPrefix{}},
		{"", "", chatPrefix{}},
	} {
		if got, rest := parseChatPrefix(c.line); got != c.want || rest != c.rest {
			t.Errorf("%q: %+v %q, want %+v %q", c.line, got, rest, c.want, c.rest)
		}
	}
}

// Every mode resolves through the local row A and the seat map; a computer,
// an empty record and a dropped human have no seat. A marked slot without one
// discards the line; the other modes skip it [07 §5 "Chat"].
func TestChatRecipientsFromAllianceRowAndSeats(t *testing.T) {
	rows := chatTestRows()
	seats := chatTestSeats()
	mark := func(slot int) (m [frame.PlayerRowSlots]bool) { m[slot] = true; return }
	var none [frame.PlayerRowSlots]bool
	bob, cara := uint16(1)<<3, uint16(1)<<4
	for _, c := range []struct {
		name  string
		mode  chatRecipientMode
		marks [frame.PlayerRowSlots]bool
		want  uint16
		ok    bool
	}{
		{"all", chatToAll, none, bob | cara, true},
		{"allies", chatToAllies, none, bob, true},
		{"enemies", chatToEnemies, none, cara, true},
		{"one human", chatToCustom, mark(2), cara, true},
		{"self", chatToCustom, mark(0), 0, true},
		{"a computer", chatToCustom, mark(3), 0, false},
		{"an empty slot", chatToCustom, mark(7), 0, false},
	} {
		if got, ok := chatRecipients(&rows, 0, c.mode, c.marks, seats, nil); got != c.want || ok != c.ok {
			t.Errorf("%s: %#x %v, want %#x %v", c.name, got, ok, c.want, c.ok)
		}
	}
	// Without the lobby's map a human row's slot is its seat.
	if got, _ := chatRecipients(&rows, 0, chatToAll, none, onlineChatSeats{}, nil); got != 1<<1|1<<2 {
		t.Fatalf("unadopted map: %#x", got)
	}
	// A seat the relay no longer compares has no identity.
	progress := &relay.HostedMatchProgress{Seats: []relay.HostedSeatProgress{{Playing: true}, {Playing: true}, {Playing: false, Final: true}}}
	if got, ok := chatRecipients(&rows, 0, chatToAll, none, seats, progress); got != bob || !ok {
		t.Fatalf("a dropped seat stayed a recipient: %#x", got)
	}
	if _, ok := chatRecipients(&rows, 0, chatToCustom, mark(2), seats, progress); ok {
		t.Fatal("a line marked for a dropped seat was not discarded")
	}
	// Seats map back to their slots; a seat absent at Start has none.
	for seat, want := range map[uint8]int{0: 0, 3: 1, 4: 2} {
		if got, ok := seats.slotOf(&rows, seat); !ok || got != want {
			t.Fatalf("seat %d: slot %d %v", seat, got, ok)
		}
	}
	if _, ok := seats.slotOf(&rows, 1); ok {
		t.Fatal("an absent seat has a slot")
	}
}

// A committed line is sent, stripped of its prefix, then posted locally as
// `<name> text`, class 4 with no speaker and no cue; a line for a slot with
// no identity is neither sent nor posted [07 §5 "Chat"][07 R-FE-02 §12].
func TestOnlineChatSendsThenPostsLocally(t *testing.T) {
	b, link, cues := chatTestBattle(t)
	bob, cara := uint16(1)<<3, uint16(1)<<4
	for _, c := range []struct {
		line   string
		send   *fakeChatSend
		posted string
	}{
		{"hello", &fakeChatSend{bob | cara, "hello"}, "<Alice> hello"},
		{"a,hi team", &fakeChatSend{bob, "hi team"}, "<Alice> hi team"},
		{"E;gg", &fakeChatSend{cara, "gg"}, "<Alice> gg"},
		{"1:psst", &fakeChatSend{bob, "psst"}, "<Alice> psst"},
		{"x:hello", &fakeChatSend{bob | cara, "x:hello"}, "<Alice> x:hello"},
		{"0:note", nil, "<Alice> note"},
		{"3:psst", nil, ""},
		{"7:psst", nil, ""},
	} {
		before := len(chatLines(b))
		sent := len(link.sent)
		b.commitLocalChat(c.line)
		lines := chatLines(b)
		switch {
		case c.send == nil && len(link.sent) != sent:
			t.Fatalf("%q sent %+v", c.line, link.sent[sent:])
		case c.send != nil && (len(link.sent) != sent+1 || link.sent[sent] != *c.send):
			t.Fatalf("%q sent %+v, want %+v", c.line, link.sent[sent:], *c.send)
		case c.posted == "" && len(lines) != before:
			t.Fatalf("%q posted %+v", c.line, lines[before:])
		case c.posted != "" && (len(lines) != before+1 || lines[before].Text != c.posted || lines[before].Class != 4 || lines[before].SpeakerSlot != 10):
			t.Fatalf("%q posted %+v, want class-4 %q", c.line, lines[before:], c.posted)
		}
	}
	if *cues != 0 {
		t.Fatalf("local posts played %d arrival cues", *cues)
	}
	if b.chat.mode != chatToAll {
		t.Fatal("a prefix changed the stored mode")
	}
}

// A + line runs on this machine and is then sent as chat to the recipients it
// would have had, or to everyone when the dispatched entry was a cheat
// [07 R-CAM-01 §6].
func TestOnlineChatCommandLinesDispatchThenSend(t *testing.T) {
	b, link, _ := chatTestBattle(t)
	b.chat.mode = chatToAllies
	b.commitLocalChat("+net")
	if !b.netVisible || len(link.sent) != 1 || link.sent[0] != (fakeChatSend{1 << 3, "+net"}) {
		t.Fatalf("+net: visible %v, sent %+v", b.netVisible, link.sent)
	}
	// Online no cheat dispatches: the refused line still goes out as chat.
	b.commitLocalChat("+atm")
	lines := chatLines(b)
	if len(link.sent) != 2 || link.sent[1] != (fakeChatSend{1 << 3, "+atm"}) || lines[len(lines)-2].Text != "This +command is unavailable in multiplayer games" || lines[len(lines)-1].Text != "<Alice> +atm" {
		t.Fatalf("+atm: sent %+v, lines %+v", link.sent, lines)
	}
	// A dispatched cheat forces everyone.
	if err := b.sendChat("+atm", true, true); err != nil || link.sent[2] != (fakeChatSend{1<<3 | 1<<4, "+atm"}) {
		t.Fatalf("cheat: %v %+v", err, link.sent)
	}
	// Which entries are cheats: a skirmish dispatches them, and nothing
	// else returns the cheat mask.
	single := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	t.Cleanup(single.cl.Close)
	for line, want := range map[string]bool{"+atm": true, "+View 1": true, "+Radar": true, "+net": false, "+bps": false, "hello": false} {
		if got := single.dispatchLocalCommand(line); got != want {
			t.Errorf("%q cheat %v, want %v", line, got, want)
		}
	}
}

// Received lines post `<sender> text` with class 8 and the sender's slot as
// speaker, one cue per accepted line, on the host step only: a background
// pump leaves them waiting [07 §5 "Chat"][07 R-HUD-03 §14.3].
func TestOnlineChatReceivedLinesPostWithSpeaker(t *testing.T) {
	b, link, cues := chatTestBattle(t)
	link.inbox = []relay.HostedChatLine{{Seat: 3, Text: "hi"}, {Seat: 4, Text: "+atm"}, {Seat: 1, Text: "ghost"}}
	b.pumpBackground(nil)
	if len(link.inbox) != 3 || len(chatLines(b)) != 0 {
		t.Fatal("a background pump drained chat")
	}
	b.pumpLocalMultiplayer(nil)
	lines := chatLines(b)
	if len(lines) != 2 || *cues != 2 {
		t.Fatalf("lines %+v, cues %d", lines, *cues)
	}
	for i, want := range []struct {
		text string
		slot uint8
	}{{"<Bob> hi", 1}, {"<Cara> +atm", 2}} {
		if lines[i].Text != want.text || lines[i].Class != 8 || lines[i].SpeakerSlot != want.slot {
			t.Fatalf("line %d: %+v, want class-8 %q from slot %d", i, lines[i], want.text, want.slot)
		}
	}
	if b.netVisible || len(b.sess.PendingHumanCommands()) != 0 {
		t.Fatal("a received + line was dispatched")
	}
	b.pumpLocalMultiplayer(nil)
	if len(chatLines(b)) != 2 || *cues != 2 {
		t.Fatal("a drained line was posted again")
	}
}

// A refused line is not posted; its refusal is, in words that fit the
// message column, and the link stays as it was.
func TestOnlineChatRefusals(t *testing.T) {
	b, link, _ := chatTestBattle(t)
	for _, c := range []struct {
		line, want string
		setup      func()
	}{
		{"hello", "Chat not sent: too many lines at once. Wait a moment.", func() { link.err = relay.ErrHostedChatRate }},
		{"héllo", "Chat not sent: it has characters that cannot be sent.", func() { link.err = nil }},
		{strings.Repeat("x", relay.HostedMaxChatBytes+1), "Chat not sent: the line is too long.", func() {}},
		{"hello", "Chat not sent: the game connection refused it.", func() { link.err = errors.New("write: broken pipe") }},
		{"hello", "Chat not sent: the game connection is closed.", func() { link.err, b.multiplayer.failure = nil, errors.New("test disconnect") }},
	} {
		c.setup()
		b.commitLocalChat(c.line)
		lines := chatLines(b)
		last := lines[len(lines)-1]
		if last.Text != c.want || len(c.want) > 63 || slices.ContainsFunc(lines, func(l frame.MessageLine) bool { return strings.HasPrefix(l.Text, "<Alice>") }) {
			t.Fatalf("%q: lines %+v, want refusal %q", c.line, lines, c.want)
		}
	}
	if len(link.sent) != 0 || b.multiplayer.chat.link != link {
		t.Fatal("a refused line was sent or the link was dropped")
	}
}

// A watcher, or online a seat whose result is final, cannot open chat
// [07 §5 "Chat"].
func TestChatWatcherAndFinalSeatCannotOpenTalk(t *testing.T) {
	b, _, _ := chatTestBattle(t)
	b.hud = &retailBattleHUD{talkWin: testTalkWindow()}
	in := b.cl.Input()
	b.sess.Econ.Players[0].Watcher = true
	if b.openTalk(in, b.cl) {
		t.Fatal("a watcher opened chat")
	}
	b.sess.Econ.Players[0].Watcher = false
	staging := b.sess.Snapshot.BeginWrite()
	staging.Players = chatTestRows()
	staging.Result = frame.ResultView{Ended: true, Kind: "defeat"}
	if err := b.sess.Snapshot.Publish(2); err != nil {
		t.Fatal(err)
	}
	if b.openTalk(in, b.cl) {
		t.Fatal("a defeated online seat opened chat")
	}
	b.multiplayer = nil
	if !b.openTalk(in, b.cl) || !b.chat.active {
		t.Fatal("an offline battle's result closed chat")
	}
}

// Without a chat link — offline, the loopback play test, replay playback —
// a line is posted locally only, prefix and all, as single-player posts it.
func TestChatWithoutLinkPostsLocallyOnly(t *testing.T) {
	b, link, _ := chatTestBattle(t)
	b.multiplayer = &battleMultiplayer{driver: &testLocalBattleDriver{}} // loopback: no measurements, no link
	b.commitLocalChat("3:hi")
	if lines := chatLines(b); len(lines) != 1 || lines[0].Text != "<Alice> 3:hi" || lines[0].Class != 4 || len(link.sent) != 0 {
		t.Fatalf("loopback line: %+v", lines)
	}
	b.multiplayer = nil
	b.commitLocalChat("hello")
	if lines := chatLines(b); len(lines) != 2 || lines[1].Text != "<Alice> hello" {
		t.Fatalf("offline line: %+v", lines)
	}
}

// fakeChatReporter is a hosted connection: reports and chat.
type fakeChatReporter struct {
	fakeReportingClient
	fakeChatLink
}

// The link is found under the measurements and the replay recorder, and only
// on a connection that has one.
func TestOnlineChatLinkUnderWrappers(t *testing.T) {
	hosted := &fakeChatReporter{}
	recorded := reportingOnlineReplayClient{onlineReplayClient: &onlineReplayClient{}, reporter: hosted}
	for _, c := range []struct {
		name   string
		client *onlineNetStats
		want   onlineChatLink
	}{
		{"direct", newOnlineNetStats(hosted, 0, 2), hosted},
		{"recorded", newOnlineNetStats(recorded, 0, 2), hosted},
		{"recorded without reports", newOnlineNetStats(&onlineReplayClient{}, 0, 2), nil},
		{"no chat", newOnlineNetStats(&fakeNetClient{}, 0, 2), nil},
	} {
		if got := onlineChatLinkOf(c.client); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
	b := &battleSession{multiplayer: &battleMultiplayer{net: newOnlineNetStats(recorded, 0, 2)}}
	if b.onlineChatLink() != hosted {
		t.Fatal("the battle did not find its link")
	}
}

// The Allies window's announcement is an ordinary line through the chat
// sender: sent to the current recipients, posted with class 4 and no speaker,
// never read for a prefix or a + command [07 R-FE-01 §7].
func TestChatLineForAnnouncements(t *testing.T) {
	b, link, cues := chatTestBattle(t)
	if err := b.sendChatLine(" allied with Bob"); err != nil {
		t.Fatal(err)
	}
	if err := b.sendChatLine("a,+atm"); err != nil {
		t.Fatal(err)
	}
	lines := chatLines(b)
	if len(link.sent) != 2 || link.sent[0] != (fakeChatSend{1<<3 | 1<<4, " allied with Bob"}) || link.sent[1].text != "a,+atm" || link.sent[1].recipients != 1<<3|1<<4 {
		t.Fatalf("sent %+v", link.sent)
	}
	if len(lines) != 2 || lines[0].Text != "<Alice>  allied with Bob" || lines[0].Class != 4 || lines[0].SpeakerSlot != 10 || *cues != 0 || len(b.sess.PendingHumanCommands()) != 0 {
		t.Fatalf("posted %+v with %d cues", lines, *cues)
	}
	link.err = relay.ErrHostedChatRate
	if err := b.sendChatLine(" broke alliance with Bob"); !errors.Is(err, relay.ErrHostedChatRate) {
		t.Fatalf("refusal %v", err)
	}
}
