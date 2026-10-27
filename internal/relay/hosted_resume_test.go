package relay

import (
	"bytes"
	"context"
	"crypto/tls"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/netproto"
)

// resumeTestPair is an auto-start room of two version-9 seats that have both
// read Started, so each holds a credential.
func resumeTestPair(t *testing.T, timeouts hostedTimeouts) (*HostedServer, [2]*LocalClient, string) {
	t.Helper()
	s := listenHostedTest(t, HostedConfig{InsecureLoopback: true}, timeouts)
	var clients [2]*LocalClient
	code := ""
	for seat := range uint8(2) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		c, joined, err := DialHosted(ctx, s.Addr(), code, localTestHello(seat), HostedDialOptions{InsecureLoopback: true})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if c.version != hostedResumeVersion {
			t.Fatalf("seat %d spoke version %d", seat, c.version)
		}
		t.Cleanup(func() { _ = c.Close() })
		c.idle = 0
		clients[seat], code = c, joined
	}
	return s, clients, code
}

type resumeRead struct {
	grant LocalGrant
	err   error
}

// readResume reads one grant within a bound, however many resumes it takes.
func readResume(t *testing.T, c *LocalClient) (LocalGrant, error) {
	t.Helper()
	done := make(chan resumeRead, 1)
	go func() {
		g, err := c.ReadGrant()
		done <- resumeRead{g, err}
	}()
	select {
	case r := <-done:
		return r.grant, r.err
	case <-time.After(5 * time.Second):
		t.Fatal("no grant within five seconds")
		return LocalGrant{}, nil
	}
}

// stepResume reads and acknowledges the next grant on every client. The
// clients read at once, as each real seat's reader does: a direct seat sends
// its opening marker only when it reads Started.
func stepResume(t *testing.T, tick uint32, clients ...*LocalClient) []LocalGrant {
	t.Helper()
	reads := make([]chan resumeRead, len(clients))
	for i, c := range clients {
		reads[i] = make(chan resumeRead, 1)
		go func() {
			g, err := c.ReadGrant()
			reads[i] <- resumeRead{g, err}
		}()
	}
	grants := make([]LocalGrant, len(clients))
	for i := range clients {
		var r resumeRead
		select {
		case r = <-reads[i]:
		case <-time.After(5 * time.Second):
			t.Fatalf("client %d: no grant %d within five seconds", i, tick)
		}
		if r.err != nil || r.grant.Tick != tick {
			t.Fatalf("client %d grant %d: %+v %v", i, tick, r.grant, r.err)
		}
		grants[i] = r.grant
	}
	for _, c := range clients {
		if err := c.Acknowledge(tick, [32]byte{byte(tick)}, false, false); err != nil {
			t.Fatal(err)
		}
	}
	return grants
}

// awaitProgress waits for a report that satisfies ok.
func awaitProgress(t *testing.T, c *LocalClient, ok func(HostedMatchProgress) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if p, have := c.Progress(); have && ok(p) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	p, _ := c.Progress()
	t.Fatalf("progress never matched: %+v", p)
}

// A version-9 seat whose connection drops keeps its seat: the relay holds the
// others within 30 ticks, the seat's own reader resumes from the last grant it
// received, and the orders and acknowledgements it could not send arrive after
// the resume, in order, so the match goes on with nothing lost or repeated
// (§16.9).
func TestHostedDroppedSeatResumesWhereItWas(t *testing.T) {
	_, clients, _ := resumeTestPair(t, hostedDefaultTimeouts)
	for tick := uint32(1); tick <= 3; tick++ {
		stepResume(t, tick, clients[0], clients[1])
	}
	if _, ok := clients[1].RejoinTicket(); !ok || !clients[1].Connection().Resumable {
		t.Fatal("a version-9 seat received no credential")
	}
	// Lose seat 1's connection, then keep playing locally: an order and an
	// acknowledgement of a tick it already holds wait for the resume.
	_ = clients[1].currentConn().Close()
	if _, err := clients[1].Submit([]byte("while away")); err != nil {
		t.Fatal(err)
	}
	// Seat 0 keeps running until the 30-tick lead bound holds it.
	tick := uint32(4)
	for ; tick <= 3+hostedMaxAhead; tick++ {
		g, err := readResume(t, clients[0])
		if err != nil || g.Tick != tick {
			t.Fatalf("held room grant %d: %+v %v", tick, g, err)
		}
		if err := clients[0].Acknowledge(tick, [32]byte{byte(tick)}, false, false); err != nil {
			t.Fatal(err)
		}
	}
	awaitProgress(t, clients[0], func(p HostedMatchProgress) bool {
		return len(p.Seats) == 2 && p.Seats[1].Playing && !p.Seats[1].Connected
	})
	// The away seat resumes through its reader and receives every grant it
	// missed, the first carrying its kept order.
	var commands []LocalCommand
	for want := uint32(4); want < tick; want++ {
		g, err := readResume(t, clients[1])
		if err != nil || g.Tick != want {
			t.Fatalf("resumed grant %d: %+v %v", want, g, err)
		}
		commands = append(commands, g.Commands...)
		if err := clients[1].Acknowledge(want, [32]byte{byte(want)}, false, false); err != nil {
			t.Fatal(err)
		}
	}
	if conn := clients[1].Connection(); conn.Resumes != 1 || conn.Reconnecting || conn.Sealed < 3 {
		t.Fatalf("connection after the resume: %+v", conn)
	}
	// The order was accepted after the resume, so it rides a later grant if
	// not one of these.
	for len(commands) == 0 {
		grants := stepResume(t, tick, clients[0], clients[1])
		commands = grants[1].Commands
		tick++
	}
	if len(commands) != 1 || commands[0].Seat != 1 || commands[0].Sequence != 1 || !bytes.Equal(commands[0].Payload, []byte("while away")) {
		t.Fatalf("the kept order: %+v", commands)
	}
	awaitProgress(t, clients[0], func(p HostedMatchProgress) bool { return p.Seats[1].Connected })
	// Play goes on, compared as before.
	for end := tick + 31; tick <= end; tick++ {
		stepResume(t, tick, clients[0], clients[1])
	}
}

// A seat whose client restarted rejoins with its ticket: it describes the
// room, composes from tick zero and replays every kept grant at its own speed
// while the others wait. Its acknowledgements of ticks the relay already
// holds only show progress, and its next order follows the relay's last
// accepted sequence (§16.9).
func TestHostedRestartedSeatRejoinsFromTickZero(t *testing.T) {
	s, clients, code := resumeTestPair(t, hostedDefaultTimeouts)
	if _, err := clients[1].Submit([]byte("before")); err != nil {
		t.Fatal(err)
	}
	var tick uint32
	for tick = 1; tick <= 10; tick++ {
		stepResume(t, tick, clients[0], clients[1])
	}
	ticket, ok := clients[1].RejoinTicket()
	if !ok || ticket.Code != code || ticket.Seat != 1 {
		t.Fatalf("ticket %+v %v", ticket, ok)
	}
	// The client is gone without a word, as a crashed process is.
	_ = clients[1].currentConn().Close()

	ctx := context.Background()
	options := HostedDialOptions{InsecureLoopback: true}
	described, err := DescribeHostedRejoin(ctx, s.Addr(), ticket, options)
	if err != nil {
		t.Fatal(err)
	}
	// A command-line room carries no base configuration; a lobby's does.
	if !described.State.Started || described.State.Slot != 1 || !described.State.Seats[0].Present || !described.State.Seats[1].Present || len(described.Config) != 0 {
		t.Fatalf("description %+v", described)
	}
	back, err := ResumeHosted(ctx, s.Addr(), ticket, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = back.Close() })
	back.idle = 0
	if conn := back.Connection(); !conn.Resumable || conn.Sealed < tick-1 {
		t.Fatalf("connection %+v", conn)
	}
	var orders int
	for want := uint32(1); want < tick; want++ {
		g, err := readResume(t, back)
		if err != nil || g.Tick != want {
			t.Fatalf("replayed grant %d: %+v %v", want, g, err)
		}
		orders += len(g.Commands)
		if err := back.Acknowledge(want, [32]byte{byte(want)}, false, false); err != nil {
			t.Fatal(err)
		}
	}
	if orders != 1 {
		t.Fatalf("the replay carried %d of seat 1's orders, want its one", orders)
	}
	if seq, err := back.Submit([]byte("after")); err != nil || seq != 2 {
		t.Fatalf("the next order's sequence: %d %v", seq, err)
	}
	for end := tick + 31; tick <= end; tick++ {
		stepResume(t, tick, clients[0], back)
	}
}

// A ticket proves one seat of one running match, and a dropped seat that
// stays away past the grace ends the match without a result.
func TestHostedRejoinRefusalsAndGrace(t *testing.T) {
	timeouts := hostedDefaultTimeouts
	timeouts.rejoin = 300 * time.Millisecond
	s, clients, _ := resumeTestPair(t, timeouts)
	stepResume(t, 1, clients[0], clients[1])
	ticket, _ := clients[1].RejoinTicket()
	options := HostedDialOptions{InsecureLoopback: true}
	forged := ticket
	forged.Credential[0] ^= 1
	other := ticket
	other.Seat = 0
	for _, bad := range []HostedRejoinTicket{forged, other, {Code: "CFHJKM", Seat: 1, Credential: ticket.Credential}} {
		if _, err := DescribeHostedRejoin(context.Background(), s.Addr(), bad, options); err == nil {
			t.Fatalf("described with %+v", bad)
		}
		if c, err := ResumeHosted(context.Background(), s.Addr(), bad, options); err == nil {
			_ = c.Close()
			t.Fatalf("resumed with %+v", bad)
		}
	}
	// Seat 1 vanishes and never returns.
	_ = clients[1].currentConn().Close()
	clients[1].closed.Store(true)
	for {
		_, err := readResume(t, clients[0])
		if err == nil {
			continue
		}
		if !strings.Contains(err.Error(), "reconnection") {
			t.Fatalf("an expired grace: %v", err)
		}
		break
	}
	awaitHostedCapacity(t, s, 0, 0)
	if st := s.Status(); len(st.Recent) != 1 || st.Recent[0].Outcome != finishDisconnected || st.Recent[0].Seat != 1 {
		t.Fatalf("recent rooms: %+v", st.Recent)
	}
}

// Closing a version-9 client says the player left on purpose, which ends the
// match at once rather than holding the seat.
func TestHostedLeaveEndsMatchAtOnce(t *testing.T) {
	s, clients, _ := resumeTestPair(t, hostedDefaultTimeouts)
	stepResume(t, 1, clients[0], clients[1])
	_ = clients[1].Close()
	start := time.Now()
	for {
		_, err := readResume(t, clients[0])
		if err == nil {
			continue
		}
		if !strings.Contains(err.Error(), "a player left") {
			t.Fatalf("a leaving player: %v", err)
		}
		break
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("the relay held a seat that left on purpose")
	}
	awaitHostedCapacity(t, s, 0, 0)
	if st := s.Status(); len(st.Recent) != 1 || st.Recent[0].Outcome != finishLeft {
		t.Fatalf("recent rooms: %+v", st.Recent)
	}
}

// A connected version-9 seat that stops running, as a suspended phone app
// does, is dropped rather than failing the room, and resumes when it runs
// again.
func TestHostedStalledSeatIsHeldForResume(t *testing.T) {
	timeouts := hostedDefaultTimeouts
	timeouts.progress = 150 * time.Millisecond
	_, clients, _ := resumeTestPair(t, timeouts)
	stepResume(t, 1, clients[0], clients[1])
	// Seat 1 stops acknowledging; seat 0 runs on to the lead bound.
	for tick := uint32(2); tick <= 1+hostedMaxAhead; tick++ {
		g, err := readResume(t, clients[0])
		if err != nil || g.Tick != tick {
			t.Fatalf("grant %d: %+v %v", tick, g, err)
		}
		if err := clients[0].Acknowledge(tick, [32]byte{byte(tick)}, false, false); err != nil {
			t.Fatal(err)
		}
	}
	awaitProgress(t, clients[0], func(p HostedMatchProgress) bool { return !p.Seats[1].Connected })
	// Held, not failed: seat 0 still gets reports and no failure.
	time.Sleep(3 * timeouts.progress)
	// Seat 1 runs again: what it had queued, then the resume.
	for tick := uint32(2); tick <= 1+hostedMaxAhead+5; tick++ {
		if tick > 1+hostedMaxAhead {
			g, err := readResume(t, clients[0])
			if err != nil || g.Tick != tick {
				t.Fatalf("grant %d after the resume: %+v %v", tick, g, err)
			}
			if err := clients[0].Acknowledge(tick, [32]byte{byte(tick)}, false, false); err != nil {
				t.Fatal(err)
			}
		}
		g, err := readResume(t, clients[1])
		if err != nil || g.Tick != tick {
			t.Fatalf("seat 1 grant %d: %+v %v", tick, g, err)
		}
		if err := clients[1].Acknowledge(tick, [32]byte{byte(tick)}, false, false); err != nil {
			t.Fatal(err)
		}
	}
	if clients[1].Connection().Resumes != 1 {
		t.Fatalf("connection %+v", clients[1].Connection())
	}
}

// A client offers version 9 first and speaks version 8, without
// reconnection, to a relay that refuses version 9 by name.
func TestHostedClientFallsBackToAnOlderRelay(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	served := make(chan uint16, 2)
	go func() {
		for range 2 {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			body, err := readLocalFrame(conn, hostedMaxHandshakeFrame)
			if err != nil {
				_ = conn.Close()
				return
			}
			r := netproto.NewReader(body, hostedError)
			r.U8()
			version := r.U16()
			served <- version
			if version > hostedChatVersion {
				// The words a version-8 relay refuses with.
				_ = writeLocalFrame(conn, localFailureBody(localRefusedMessage, hostedError("handshake", "hosted protocol version 7 or 8; this client sent version 9")))
			} else {
				_ = writeLocalFrame(conn, encodeHostedWelcome("CFHJKM", 0, version))
			}
			time.Sleep(50 * time.Millisecond)
			_ = conn.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, _, err := DialHosted(ctx, listener.Addr().String(), "", localTestHello(0), HostedDialOptions{InsecureLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if first, second := <-served, <-served; first != hostedResumeVersion || second != hostedChatVersion || c.version != hostedChatVersion {
		t.Fatalf("offered %d then %d, spoke %d", first, second, c.version)
	}
}

// The relay keeps every grant until it holds more than its bound, then the
// newer half, never fewer than two minutes of ticks.
func TestGrantHistoryKeepsTheNewestWithinItsBound(t *testing.T) {
	var h grantHistory
	body := make([]byte, 2<<10) // shared: the history counts, the test holds one
	n := uint32(hostedMaxHistoryBytes/(len(body)+historyEntryBytes)) + 1000
	for tick := uint32(1); tick <= n; tick++ {
		h.append(tick, body)
	}
	if h.bytes > hostedMaxHistoryBytes || h.first <= 1 || !h.keeps(n-hostedKeptHistoryTicks, n) || h.keeps(0, n) {
		t.Fatalf("history kept %d bytes from tick %d of %d", h.bytes, h.first, n)
	}
	if _, ok := h.at(h.first - 1); ok {
		t.Fatal("a dropped grant was returned")
	}
	if got, ok := h.at(n); !ok || len(got) != len(body) {
		t.Fatal("the newest grant was not kept")
	}
	if !h.keeps(n, n) {
		t.Fatal("nothing to resend must always be kept")
	}
}

// A LAN relay is plaintext WebSocket that admits only this computer's own
// and private-network peers, and a client dials one only at such a numeric
// address (DESIGN_MULTIPLAYER §16.10).
func TestLANRelayPlaysAndRefusesTheInternet(t *testing.T) {
	for _, bad := range []HostedConfig{{LAN: true, InsecureLoopback: true}, {LAN: true, TLSConfig: &tls.Config{}}} {
		if s, err := ListenHostedWebSocket("127.0.0.1:0", bad, false); err == nil {
			_ = s.Close()
			t.Fatalf("accepted %+v", bad)
		}
	}
	if s, err := ListenHosted("127.0.0.1:0", HostedConfig{LAN: true}); err == nil {
		_ = s.Close()
		t.Fatal("a LAN relay without WebSocket was accepted")
	}
	s, err := ListenHostedWebSocket("127.0.0.1:0", HostedConfig{LAN: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	address := "ws://" + s.Addr() + "/relay"
	options := HostedDialOptions{LAN: true}
	var clients [2]*LocalClient
	code := ""
	for seat := range uint8(2) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		c, joined, err := DialHosted(ctx, address, code, localTestHello(seat), options)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		c.idle = 0
		clients[seat], code = c, joined
	}
	stepResume(t, 1, clients[0], clients[1])
	for _, public := range []string{"ws://192.0.2.1:39032/relay", "ws://relay.example.test:39032/relay"} {
		if _, _, err := hostedWebSocketURL(public, options); err == nil {
			t.Fatalf("a LAN client would dial %s", public)
		}
	}
	if _, _, err := hostedWebSocketURL("wss://relay.example.test/relay", options); err == nil {
		t.Fatal("the LAN switch was taken with TLS")
	}
	for addr, want := range map[string]bool{"192.168.1.20:5": true, "10.1.2.3:5": true, "172.31.0.1:5": true, "127.0.0.1:5": true, "[fd00::1]:5": true, "[fe80::1]:5": true,
		"8.8.8.8:5": false, "172.32.0.1:5": false, "[2001:db8::1]:5": false} {
		tcp, err := net.ResolveTCPAddr("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		if lanPeer(tcp) != want {
			t.Errorf("peer %s admitted %v, want %v", addr, !want, want)
		}
	}
}
