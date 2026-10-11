package relay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/netproto"
)

// awaitChat drains a client until it holds as many lines as want, then
// compares them in order.
func awaitChat(t *testing.T, drain func([]HostedChatLine) []HostedChatLine, want ...HostedChatLine) {
	t.Helper()
	var got []HostedChatLine
	end := time.Now().Add(3 * time.Second)
	for len(got) < len(want) {
		got = drain(got)
		if time.Now().After(end) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("chat %+v, want %+v", got, want)
	}
}

// awaitChatTotals waits until the relay has counted exactly these chat
// deliveries and drops.
func awaitChatTotals(t *testing.T, s *HostedServer, relayed, dropped uint64) {
	t.Helper()
	end := time.Now().Add(3 * time.Second)
	for {
		s.mu.Lock()
		r, d := s.stats.chatRelayed, s.stats.chatDropped
		s.mu.Unlock()
		if r == relayed && d == dropped {
			return
		}
		if time.Now().After(end) {
			t.Fatalf("chat relayed/dropped %d/%d, want %d/%d", r, d, relayed, dropped)
		}
		time.Sleep(time.Millisecond)
	}
}

// The protocol's message kinds keep their numbers, so a version-7 client
// reads every frame the relay sends it as it always has (§12.2).
func TestHostedMessageKindsAreFixed(t *testing.T) {
	kinds := []uint8{hostedHelloMessage, hostedWelcomeMessage, hostedDescribeMessage, hostedDescriptionMessage, hostedLobbyMessage,
		hostedReadyMessage, hostedStartMessage, hostedStartedMessage, hostedTeamMessage, hostedSideMessage, hostedConfigurationMessage,
		hostedColorMessage, hostedProgressMessage, hostedOpeningReadyMessage, hostedChatMessage}
	for i, kind := range kinds {
		if kind != uint8(8+i) {
			t.Fatalf("message %d has kind %d", i, kind)
		}
	}
}

// Names and chat text are printable ASCII within their bounds; a name has no
// < or > and no leading or trailing space (§12.2).
func TestHostedNameAndChatCodecs(t *testing.T) {
	for _, name := range []string{"", "Ann", "a b", "~!@#$%^&*()_+={}"} {
		if err := ValidHostedName(name); err != nil {
			t.Fatalf("name %q: %v", name, err)
		}
	}
	for _, name := range []string{"<Ann", "Ann>", " Ann", "Ann ", " ", "An\x00n", "An\x7fn", "An\tn", "\xc3\xa9", "seventeen bytes!!"} {
		if err := ValidHostedName(name); err == nil {
			t.Fatalf("accepted name %q", name)
		}
	}
	for _, text := range []string{"x", " spaced ", "<b>not markup</b>", strings.Repeat("y", HostedMaxChatBytes)} {
		if err := ValidHostedChat(text); err != nil {
			t.Fatalf("text %q: %v", text, err)
		}
	}
	bad := []string{"", strings.Repeat("y", HostedMaxChatBytes+1), "a\x00b", "a\x7fb", "a\nb", "\xc3\xa9"}
	for _, text := range bad {
		if err := ValidHostedChat(text); err == nil {
			t.Fatalf("accepted text %q", text)
		}
	}
	// A request keeps every recipient bit: the relay, not the codec, ignores
	// the ones that name no receiving seat.
	for _, recipients := range []uint16{0, 1, 1<<9 | 1, 0xffff} {
		body := encodeHostedChatRequest(recipients, "hello")
		if got, text, err := decodeHostedChatRequest(body); err != nil || got != recipients || text != "hello" {
			t.Fatalf("request %#x: %#x %q %v", recipients, got, text, err)
		}
	}
	request := encodeHostedChatRequest(3, "hello")
	malformed := [][]byte{append(bytes.Clone(request), 0), request[:len(request)-1], {hostedChatMessage}, {hostedChatMessage, 0x80, 0x80, 0x04, 1, 'x'}}
	for _, text := range bad {
		var w netproto.Writer
		w.U8(hostedChatMessage)
		w.U16(1)
		w.Text(text)
		malformed = append(malformed, w.Bytes())
	}
	for _, body := range malformed {
		if _, _, err := decodeHostedChatRequest(body); err == nil {
			t.Fatalf("accepted request %q", body)
		}
	}
	line := encodeHostedChatLine(9, "hi")
	if got, err := decodeHostedChatLine(line); err != nil || got != (HostedChatLine{Seat: 9, Text: "hi"}) {
		t.Fatalf("line: %+v %v", got, err)
	}
	for _, body := range [][]byte{encodeHostedChatLine(10, "hi"), encodeHostedChatLine(0, ""), encodeHostedChatLine(0, "a\x7f"), append(bytes.Clone(line), 0)} {
		if _, err := decodeHostedChatLine(body); err == nil {
			t.Fatalf("accepted line %q", body)
		}
	}
	// A client refuses before writing: bits past the tenth seat, bad text,
	// or a connection that does not speak version 8.
	conn, other := net.Pipe()
	defer conn.Close()
	defer other.Close()
	c := newHostedClient(conn)
	c.version = hostedVersion
	for _, send := range []func() error{
		func() error { return c.SendChat(1<<HostedMaxSeats, "hi") },
		func() error { return c.SendChat(1, "a\x00b") },
		func() error { return c.SendChat(1, "") },
		func() error { return (&LocalClient{}).SendChat(1, "hi") },
	} {
		if err := send(); err == nil {
			t.Fatal("sent an invalid chat line")
		}
	}
	if tr := c.Traffic(); tr.MessagesOut != 0 {
		t.Fatalf("a refused line was written: %+v", tr)
	}
}

// Lobby state keeps version 7's exact bytes; version 8 adds the chat bit and
// a name per seat (§12.2, §16.6.1).
func TestHostedLobbyEncodingByVersion(t *testing.T) {
	st := HostedLobbyState{Size: 3, Mismatch: true, ConfigVersion: 300}
	st.Seats[0] = HostedSeatState{Present: true, Ready: true, Team: 1, Name: "Host", Chat: true}
	st.Seats[1] = HostedSeatState{Present: true, Team: 2, Side: 1, Color: 3}
	v7 := []byte{12, 3, 16, 0xac, 0x02, 3, 1, 0, 0, 1, 2, 1, 3, 0, 0, 0, 0}
	v8 := []byte{12, 3, 16, 0xac, 0x02, 7, 1, 0, 0, 4, 'H', 'o', 's', 't', 1, 2, 1, 3, 0, 0, 0, 0, 0, 0}
	if got := encodeHostedLobby(st, 7); !bytes.Equal(got, v7) {
		t.Fatalf("version-7 lobby state %v, want %v", got, v7)
	}
	if got := encodeHostedLobby(st, 8); !bytes.Equal(got, v8) {
		t.Fatalf("version-8 lobby state %v, want %v", got, v8)
	}
	old := st
	old.Seats[0].Name, old.Seats[0].Chat = "", false
	if got, err := decodeHostedLobby(v7, 7); err != nil || got != old {
		t.Fatalf("version-7 decode: %+v %v", got, err)
	}
	if got, err := decodeHostedLobby(v8, 8); err != nil || got != st {
		t.Fatalf("version-8 decode: %+v %v", got, err)
	}
	for _, body := range [][]byte{
		append(bytes.Clone(v8), 0),
		v7,
		{12, 2, 32, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},               // an unknown flag
		{12, 2, 0, 0, 9, 0, 0, 0, 0, 0, 0, 0, 0, 0},                // an unknown seat bit
		{12, 2, 0, 0, 1, 0, 0, 0, 3, '<', 'x', '>', 0, 0, 0, 0, 0}, // a name with < and >
	} {
		if _, err := decodeHostedLobby(body, 8); err == nil {
			t.Fatalf("accepted version-8 lobby state %v", body)
		}
	}
}

// A sender may send a burst of five lines, then one a second; a long silence
// refills no more than the burst. A client applies the same limit before it
// sends anything (§12.2).
func TestHostedChatRateLimit(t *testing.T) {
	var rate hostedChatRate
	start := time.Now()
	for i := range hostedChatBurst {
		if !rate.allow(start) {
			t.Fatalf("line %d of the burst refused", i)
		}
	}
	for _, at := range []time.Duration{0, 999 * time.Millisecond} {
		if rate.allow(start.Add(at)) {
			t.Fatalf("line after the burst allowed at %v", at)
		}
	}
	if !rate.allow(start.Add(time.Second)) || rate.allow(start.Add(time.Second)) {
		t.Fatal("not one line a second after the burst")
	}
	later := start.Add(time.Hour)
	for i := range hostedChatBurst {
		if !rate.allow(later) {
			t.Fatalf("line %d after a silence refused", i)
		}
	}
	if rate.allow(later) {
		t.Fatal("a silence refilled more than the burst")
	}

	conn, server := net.Pipe()
	defer conn.Close()
	defer server.Close()
	go func() { _, _ = io.Copy(io.Discard, server) }()
	c := newHostedClient(conn)
	c.version = hostedVersion
	for range hostedChatBurst {
		if err := c.SendChat(2, "hello"); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.SendChat(2, "hello"); !errors.Is(err, ErrHostedChatRate) {
		t.Fatalf("line beyond the burst: %v", err)
	}
	if tr := c.Traffic(); tr.MessagesOut != hostedChatBurst {
		t.Fatalf("refused line was written: %+v", tr)
	}
}

// A chat offer is dropped once the peer holds 16 queued frames or would hold
// more than 64 KiB; grants still fit behind it, up to the peer's own bounds.
func TestHostedPeerOfferBounds(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	line := encodeHostedChatLine(1, "hello")
	p := newHostedPeer(a, LocalHello{}, time.Second)
	for range hostedOfferFrames - 1 {
		if err := p.enqueue([]byte{localGrantMessage}, false); err != nil {
			t.Fatal(err)
		}
	}
	if !p.offer(line) {
		t.Fatal("refused a line below the frame bound")
	}
	if p.offer(line) {
		t.Fatal("offered a line past 16 queued frames")
	}
	for len(p.out) < hostedMaxQueuedFrames {
		if err := p.enqueue([]byte{localGrantMessage}, false); err != nil {
			t.Fatalf("a grant after chat: %v", err)
		}
	}

	p = newHostedPeer(a, LocalHello{}, time.Second)
	payload := make([]byte, hostedOfferBytes-frameCharge(line)-3)
	if frameCharge(payload)+frameCharge(line) != hostedOfferBytes {
		t.Fatal("fixture does not fill the byte bound exactly")
	}
	if err := p.enqueue(payload, false); err != nil {
		t.Fatal(err)
	}
	if !p.offer(line) {
		t.Fatal("refused a line that fills 64 KiB exactly")
	}
	if p.offer(line) {
		t.Fatal("offered a line past 64 KiB")
	}
}

// A room of version-7 and version-8 seats: the version-7 seat reads lobby
// state in its own encoding, is never named in a delivery and never receives
// chat; version-8 seats see names and who can chat; and after Start a line
// reaches the seats it names but not one that left (§12.2, §16.6.1).
func TestHostedChatMixedVersionRoom(t *testing.T) {
	timeouts := hostedDefaultTimeouts
	timeouts.report = 20 * time.Millisecond
	s := listenHostedTest(t, HostedConfig{InsecureLoopback: true}, timeouts)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	open := func(version uint16, room, name string, config []byte, size int) *HostedLobby {
		t.Helper()
		l, err := openHostedLobby(ctx, version, s.Addr(), room, versionTestHello(version, 0), config, size, HostedDialOptions{InsecureLoopback: true, Name: name})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = l.Close() })
		return l
	}
	host := open(8, "", "Host", []byte{1}, 4)
	old := open(7, host.Code(), "", nil, 0)
	ann := open(8, host.Code(), "Ann", nil, 0)
	twin := open(8, host.Code(), "Ann", nil, 0) // names need not differ
	want := [4]HostedSeatState{{Present: true, Name: "Host", Chat: true}, {Present: true, Color: 1}, {Present: true, Color: 2, Name: "Ann", Chat: true}, {Present: true, Color: 3, Name: "Ann", Chat: true}}
	for _, l := range []*HostedLobby{host, ann} {
		awaitLobby(t, l, "every name", func(st HostedLobbyState, err error) bool {
			return err == nil && [4]HostedSeatState(st.Seats[:4]) == want
		})
	}
	// The version-7 lobby decodes only its own encoding: a name, the chat
	// bit or a chat line would end it.
	awaitLobby(t, old, "four seats", func(st HostedLobbyState, err error) bool { return err == nil && st.Seats[3].Present })
	if err := old.SendChat(1, "hi"); err == nil {
		t.Fatal("a version-7 client sent chat")
	}

	// Every seat named, the sender and seats past the room included: only
	// the version-8 seats receive it.
	if err := host.SendChat(1<<HostedMaxSeats-1, "hello room"); err != nil {
		t.Fatal(err)
	}
	awaitChat(t, ann.DrainChat, HostedChatLine{Seat: 0, Text: "hello room"})
	awaitChat(t, twin.DrainChat, HostedChatLine{Seat: 0, Text: "hello room"})
	// A line naming only the version-7 seat reaches nobody and drops nothing.
	if err := ann.SendChat(1<<1, "to the old client"); err != nil {
		t.Fatal(err)
	}
	if err := ann.SendChat(1<<0, "to the host"); err != nil {
		t.Fatal(err)
	}
	awaitChat(t, host.DrainChat, HostedChatLine{Seat: 2, Text: "to the host"})
	awaitChatTotals(t, s, 3, 0)

	readyAll(t, host, host, old, ann, twin)
	if err := host.Start(); err != nil {
		t.Fatal(err)
	}
	var battles [4]*LocalClient
	for i, l := range []*HostedLobby{host, old, ann, twin} {
		awaitLobby(t, l, "Started", func(st HostedLobbyState, err error) bool { return err == nil && st.Started })
		battles[i] = openingBattleTest(t, l)
		_ = battles[i].conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	}
	tick := uint32(0)
	step := func(clients ...*LocalClient) {
		t.Helper()
		tick++
		for _, c := range clients {
			if g, err := c.ReadGrant(); err != nil || g.Tick != tick {
				t.Fatalf("grant %d: %+v %v", tick, g, err)
			}
		}
		for _, c := range clients {
			if err := c.Acknowledge(tick, [32]byte{}, false, c == battles[3] && tick >= 3); err != nil {
				t.Fatal(err)
			}
		}
	}
	for range 5 {
		step(battles[:]...)
	}
	// Seat 3's result is final; it leaves, and the relay reports it gone
	// before the host's next line.
	_ = battles[3].Close()
	for {
		step(battles[:3]...)
		if p, ok := battles[0].Progress(); ok && !p.Seats[3].Playing {
			break
		}
		if tick > hostedMaxAhead {
			t.Fatal("the departure was never reported")
		}
	}
	if err := battles[0].SendChat(1<<1|1<<2|1<<3, "after start"); err != nil {
		t.Fatal(err)
	}
	var lines []HostedChatLine
	for len(lines) == 0 && tick < 2*hostedMaxAhead {
		step(battles[:3]...)
		lines = battles[2].DrainChat(lines)
	}
	if !slices.Equal(lines, []HostedChatLine{{Seat: 0, Text: "after start"}}) {
		t.Fatalf("seat 2 after Start: %+v", lines)
	}
	// One delivery more: none to the version-7 seat or the one that left.
	awaitChatTotals(t, s, 4, 0)
	if lines := battles[0].DrainChat(nil); len(lines) != 0 {
		t.Fatalf("the sender received its own line: %+v", lines)
	}
	if st := s.Status(); st.Totals.ChatRelayed != 4 || st.Totals.ChatDropped != 0 {
		t.Fatalf("status chat totals: %+v", st.Totals)
	}
}

// Chat the relay cannot use fails the room as any malformed client frame
// does: a chat message from a version-7 seat, which has none, or a line
// whose text is not printable ASCII.
func TestHostedChatMalformedFramesFailTheRoom(t *testing.T) {
	for _, version := range []uint16{7, 8} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			s := listenHostedTest(t, HostedConfig{InsecureLoopback: true}, hostedDefaultTimeouts)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			host := openSizedLobby(t, s.Addr(), 2, []byte{1}, HostedDialOptions{InsecureLoopback: true})
			sender, err := openHostedLobby(ctx, version, s.Addr(), host.Code(), versionTestHello(version, 1), nil, 0, HostedDialOptions{InsecureLoopback: true})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sender.Close() })
			body, want := encodeHostedChatRequest(1, "hello"), "client message"
			if version == 8 {
				body, want = []byte{hostedChatMessage, 1, 1, 0x7f}, "chat line"
			}
			if err := sender.send(body); err != nil {
				t.Fatal(err)
			}
			if _, err := awaitLobby(t, host, "the room failed", func(_ HostedLobbyState, err error) bool { return err != nil }); !strings.Contains(err.Error(), want) {
				t.Fatalf("malformed chat: %v", err)
			}
			awaitHostedCapacity(t, s, 0, 0)
		})
	}
}

// A chat flood beyond the sender's rate limit is dropped and counted at the
// relay. The room never fails, every grant keeps coming to both seats, the
// lines within the limit arrive, and the room completes (§12.2, §16.5.1). A
// slow reader's queue bound is TestHostedPeerOfferBounds's: a stall built from
// socket buffers depends on each host's kernel buffering.
func TestHostedChatFloodKeepsGrantsFlowing(t *testing.T) {
	s := listenHostedTest(t, HostedConfig{InsecureLoopback: true}, hostedDefaultTimeouts)
	options := HostedDialOptions{InsecureLoopback: true}
	host := openSizedLobby(t, s.Addr(), 2, []byte{1}, options)
	guest := openLobbyTest(t, s.Addr(), host.Code(), 1, nil, options)
	readyAll(t, host, host, guest)
	if err := host.Start(); err != nil {
		t.Fatal(err)
	}
	var clients [2]*LocalClient
	for i, l := range []*HostedLobby{host, guest} {
		awaitLobby(t, l, "Started", func(st HostedLobbyState, err error) bool { return err == nil && st.Started })
		clients[i] = openingBattleTest(t, l)
		_ = clients[i].conn.SetReadDeadline(time.Now().Add(time.Minute))
	}
	sender := clients[0]
	for i := range hostedChatBurst {
		if err := sender.SendChat(1<<1, fmt.Sprint("line ", i)); err != nil {
			t.Fatal(err)
		}
	}
	awaitChatTotals(t, s, hostedChatBurst, 0)
	// The sender's limit is spent: a raw flood is dropped at the relay, one
	// count a line.
	const flood = 20
	for range flood {
		if err := sender.writeMessage(encodeHostedChatRequest(1<<1, "flood")); err != nil {
			t.Fatal(err)
		}
	}
	awaitChatTotals(t, s, hostedChatBurst, flood)
	const ticks = 40
	for tick := uint32(1); tick <= ticks; tick++ {
		if g := readLocalPair(t, clients); g.Tick != tick {
			t.Fatalf("grant %d during the flood: %+v", tick, g)
		}
		ackLocalPair(t, clients, tick, [32]byte{}, tick == ticks)
	}
	lines := clients[1].DrainChat(nil)
	if len(lines) != hostedChatBurst {
		t.Fatalf("received %d lines, want %d: %+v", len(lines), hostedChatBurst, lines)
	}
	for i, l := range lines {
		if l.Seat != 0 || l.Text != fmt.Sprint("line ", i) {
			t.Fatalf("line %d: %+v", i, l)
		}
	}
	for _, c := range clients {
		if err := hostedReadError(t, c); err != io.EOF {
			t.Fatalf("completion after a chat flood: %v", err)
		}
	}
	awaitHostedCapacity(t, s, 0, 0)
	if st := s.Status(); len(st.Recent) != 1 || st.Recent[0].Outcome != finishCompleted {
		t.Fatalf("recent rooms: %+v", st.Recent)
	}
}

// FuzzHostedClientFrames feeds arbitrary bytes to the relay's decoders of a
// client's hello and chat line, and the client's of lobby state and chat:
// none may panic, and whatever one accepts re-encodes to the same bytes.
func FuzzHostedClientFrames(f *testing.F) {
	creator, _ := encodeHostedHello("", hostedAutoStart, 4, localTestHello(0), []byte("configuration"), "Ann")
	joiner, _ := encodeHostedHello("CFHJKM", 0, 0, LocalHello{Seat: hostedAnySeat, Identity: localTestHello(1).Identity}, nil, "")
	old, _ := encodeHostedHelloVersion(7, "", 0, 2, versionTestHello(7, 0), []byte{1}, "")
	st := HostedLobbyState{Size: 2, ConfigVersion: 1}
	st.Seats[0] = HostedSeatState{Present: true, Name: "Host", Chat: true}
	for _, seed := range [][]byte{creator, joiner, old, encodeHostedChatRequest(5, "hello"), encodeHostedChatLine(2, "hi"),
		encodeHostedLobby(st, 8), {hostedChatMessage, 0xff, 0x03, 0}, {},
		{hostedLobbyMessage, 48}, // a room of 48 seats once indexed past the ten
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		if h, err := decodeHostedHello(body); err == nil {
			again, err := encodeHostedHelloVersion(h.version, h.code, h.flags, h.size, h.hello, h.config, h.name)
			if err != nil || !bytes.Equal(again, body) {
				t.Fatalf("hello %+v re-encodes to %v %v", h, again, err)
			}
		}
		if recipients, text, err := decodeHostedChatRequest(body); err == nil {
			if again := encodeHostedChatRequest(recipients, text); !bytes.Equal(again, body) {
				t.Fatalf("chat request re-encodes to %v", again)
			}
		}
		if line, err := decodeHostedChatLine(body); err == nil {
			if again := encodeHostedChatLine(line.Seat, line.Text); !bytes.Equal(again, body) {
				t.Fatalf("chat line re-encodes to %v", again)
			}
		}
		if st, err := decodeHostedLobby(body, hostedVersion); err == nil {
			if again := encodeHostedLobby(st, hostedVersion); !bytes.Equal(again, body) {
				t.Fatalf("lobby state re-encodes to %v", again)
			}
		}
	})
}
