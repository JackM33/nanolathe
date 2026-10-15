package relay

import (
	"context"
	"io"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/netproto"
)

// A room of version-7 and version-8 seats plays as one; every seat receives
// the relay's progress reports, which follow the agreed tick and show a
// defeated seat that left as no longer playing (§12.2, §16.5.2).
func TestHostedProgressReachesEveryServedVersion(t *testing.T) {
	timeouts := hostedDefaultTimeouts
	timeouts.ping, timeouts.report = 20*time.Millisecond, 50*time.Millisecond
	s := listenWebSocketTest(t, HostedConfig{InsecureLoopback: true}, false, timeouts)
	address := "ws://" + s.Addr() + "/relay"
	options := HostedDialOptions{InsecureLoopback: true}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// Transport versions differ; every seat still speaks command schema 1.
	host, err := openHostedLobby(ctx, hostedVersion, address, "", versionTestHello(hostedMinVersion, 0), []byte{1}, 3, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close() })
	// A version-7 seat is welcomed in version 7, and describes in it; a
	// retired version is refused.
	if d, err := describeVersion(ctx, address, host.Code(), 7); err != nil || d != hostedDescriptionMessage {
		t.Fatalf("version-7 description: %d %v", d, err)
	}
	if d, err := describeVersion(ctx, address, host.Code(), 6); err != nil || d != localRefusedMessage {
		t.Fatalf("version-6 description: %d %v", d, err)
	}
	old, err := openHostedLobby(ctx, hostedMinVersion, address, host.Code(), versionTestHello(hostedMinVersion, 1), nil, 0, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = old.Close() })
	leaver, err := openHostedLobby(ctx, hostedVersion, address, host.Code(), versionTestHello(hostedMinVersion, 1), nil, 0, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = leaver.Close() })
	readyAll(t, host, host, old, leaver)
	if err := host.Start(); err != nil {
		t.Fatal(err)
	}
	var battles [3]*LocalClient
	for i, l := range []*HostedLobby{host, old, leaver} {
		awaitLobby(t, l, "Started", func(s HostedLobbyState, err error) bool { return err == nil && s.Started })
		battles[i] = openingBattleTest(t, l)
	}
	tick := uint32(0)
	var agreed []uint32
	measured := false
	step := func(end bool, clients ...*LocalClient) {
		t.Helper()
		tick++
		for _, c := range clients {
			if g, err := c.ReadGrant(); err != nil || g.Tick != tick {
				t.Fatalf("grant %d: %+v %v", tick, g, err)
			}
		}
		for _, c := range clients {
			final := c == battles[2] && tick >= 5
			if err := c.Acknowledge(tick, [32]byte{}, end, final); err != nil {
				t.Fatal(err)
			}
		}
		if p, ok := battles[0].Progress(); ok {
			if len(p.Seats) != 3 || p.Agreed > tick {
				t.Fatalf("report at tick %d: %+v", tick, p)
			}
			agreed = append(agreed, p.Agreed)
			measured = measured || p.Seats[0].RTT > 0
		}
	}
	for range 10 {
		step(false, battles[0], battles[1], battles[2])
	}
	// Slot 2 reported its result final at tick 5; it leaves.
	_ = battles[2].Close()
	for tick < 39 {
		step(false, battles[0], battles[1])
	}
	step(true, battles[0], battles[1])
	for _, c := range battles[:2] {
		for {
			if _, err := c.ReadGrant(); err == io.EOF {
				break
			} else if err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(agreed) == 0 || agreed[len(agreed)-1] <= agreed[0] {
		t.Fatalf("the agreed tick did not advance: %v", agreed)
	}
	// A loopback round trip usually falls within one tick of the Windows
	// monotonic clock (about 1ms), so it measures zero there.
	if !measured && runtime.GOOS != "windows" {
		t.Fatal("no report carried a measured round trip")
	}
	p, ok := battles[0].Progress()
	if !ok || !p.Seats[0].Playing || !p.Seats[1].Playing || p.Seats[2].Playing || !p.Seats[2].Final || p.Seats[2].Acked < 5 || p.Seats[0].Final {
		t.Fatalf("latest report after slot 2 left: %+v %v", p, ok)
	}
	if p, ok := battles[1].Progress(); !ok || len(p.Seats) != 3 {
		t.Fatalf("the version-7 seat's report: %+v %v", p, ok)
	}
	if _, ok := battles[2].Progress(); !ok {
		t.Fatal("the seat that left had received no report")
	}
	// The relay's messages and the seat's own, the lobby's included.
	for i, c := range battles[:2] {
		tr := c.Traffic()
		if tr.MessagesIn < 40+3 || tr.MessagesOut < 40+2 || tr.BytesIn < 2*tr.MessagesIn || tr.BytesOut < 2*tr.MessagesOut {
			t.Fatalf("seat %d traffic: %+v", i, tr)
		}
	}
	awaitHostedCapacity(t, s, 0, 0)
	if st := s.Status(); len(st.Recent) != 1 || st.Recent[0].Outcome != finishCompleted || st.Recent[0].Seat != -1 {
		t.Fatalf("recent rooms: %+v", st.Recent)
	}
}

// describeVersion asks for a room's description in a given version and
// returns the answer's message kind.
func describeVersion(ctx context.Context, address, room string, version uint16) (uint8, error) {
	conn, err := dialHostedTransport(ctx, address, HostedDialOptions{InsecureLoopback: true})
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	var w netproto.Writer
	w.U8(hostedDescribeMessage)
	w.U16(version)
	w.Text(room)
	if err := writeLocalFrame(conn, w.Bytes()); err != nil {
		return 0, err
	}
	body, err := readLocalFrame(conn, hostedMaxConfigBytes+64)
	if err != nil {
		return 0, err
	}
	return body[0], nil
}

// Traffic counts every envelope with its length prefix, from the hello on.
func TestHostedTrafficCountsBothDirections(t *testing.T) {
	s := listenHostedTest(t, HostedConfig{InsecureLoopback: true}, hostedDefaultTimeouts)
	c, code := dialHostedTest(t, s, "", 0)
	hello, err := encodeHostedHello("", hostedAutoStart, 2, localTestHello(0), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	welcome := encodeHostedWelcome(code, 0, hostedVersion)
	ready := 1 + 1 + 64 // kind, flag and two zero digests
	want := LocalTraffic{MessagesIn: 1, MessagesOut: 2, BytesIn: envelopeBytes(welcome), BytesOut: envelopeBytes(hello) + uint64(ready+1)}
	if got := c.Traffic(); got != want {
		t.Fatalf("after the hello: %+v, want %+v", got, want)
	}
	if _, err := c.Submit([]byte("order")); err != nil {
		t.Fatal(err)
	}
	if got := c.Traffic(); got.MessagesOut != 3 || got.BytesOut <= want.BytesOut+5 {
		t.Fatalf("after a submit: %+v", got)
	}
}

// A client speaks one version and refuses a relay that answers in another,
// naming both (§12.2).
func TestHostedClientRefusesAnotherWelcomeVersion(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	go func() {
		defer server.Close()
		if _, err := readLocalFrame(server, hostedMaxHandshakeFrame); err != nil {
			return
		}
		_ = writeLocalFrame(server, encodeHostedWelcome("CFHJKM", 0, hostedMinVersion))
	}()
	hello, err := encodeHostedHello("", hostedAutoStart, 2, localTestHello(0), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	var traffic trafficCounter
	if _, _, err := exchangeHostedHello(client, &traffic, hostedVersion, hello, "", 0); err == nil || !strings.Contains(err.Error(), "version 8; this relay answered version 7") {
		t.Fatalf("welcome in another version: %v", err)
	}
}

// Reports round-trip, and a malformed one is refused.
func TestHostedProgressReportEncoding(t *testing.T) {
	p := hostedProgress{n: 2, compared: 41}
	p.active[0], p.final[1], p.acked[0], p.acked[1] = true, true, 44, 41
	body := encodeHostedProgress(&p, 50, func(slot int) time.Duration { return time.Duration(slot+1) * 1500 * time.Microsecond })
	got, err := decodeHostedProgress(body)
	if err != nil || got.Agreed != 41 || got.Sealed != 50 || len(got.Seats) != 2 ||
		got.Seats[0] != (HostedSeatProgress{Playing: true, Acked: 44, RTT: 1500 * time.Microsecond}) ||
		got.Seats[1] != (HostedSeatProgress{Final: true, Acked: 41, RTT: 3 * time.Millisecond}) {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	early := encodeHostedProgress(&p, 43, func(int) time.Duration { return 0 })
	late := encodeHostedProgress(&p, 40, func(int) time.Duration { return 0 })
	for _, bad := range [][]byte{append(body, 0), body[:len(body)-1], {hostedProgressMessage, 0, 0}, {hostedProgressMessage, 0, 1, 4, 0, 0}, early, late} {
		if _, err := decodeHostedProgress(bad); err == nil {
			t.Fatalf("accepted malformed report %v", bad)
		}
	}
}

// The operator's record of an ended match carries its duration and the
// seat that ended it; a completed match names no seat.
func TestFinishedRoomRecord(t *testing.T) {
	var got []FinishedRoom
	s := &HostedServer{config: HostedConfig{Finished: func(f FinishedRoom) { got = append(got, f) }}}
	stats := &roomStats{startedAt: time.Now().Add(-2 * time.Second)}
	s.recordFinished(&hostedRoom{id: 1, size: 2}, stats, true, 2, 60, finishDisconnected, 1)
	s.recordFinished(&hostedRoom{id: 2, size: 2}, stats, true, 2, 90, finishCompleted, 1)
	if len(got) != 2 || got[0].Seat != 1 || got[0].DurationSeconds < 2 || got[1].Seat != -1 {
		t.Fatalf("records %+v", got)
	}
	for _, started := range []bool{false, true} {
		s.recordFinished(&hostedRoom{id: 3, size: 2}, stats, started, 2, 0, finishExpired, 1)
		f := got[len(got)-1]
		if (started && f.Seat != 1) || (!started && f.Seat != -1) {
			t.Fatalf("expiry with started=%v: %+v", started, f)
		}
	}
}

// Required broadcasts identify the recipient whose output queue failed,
// rather than the triggering sender or the grant timer (§12.5).
func TestHostedFailedRecipientAttribution(t *testing.T) {
	for _, playing := range []bool{false, true} {
		name := "lobby-event"
		if playing {
			name = "grant-timer"
		}
		t.Run(name, func(t *testing.T) {
			s, r, peers, _ := drainRoomTest(t, playing)
			if !r.send(hostedEvent{peer: peers[1], join: true}) {
				t.Fatal("join was not received")
			}
			deadline := time.Now().Add(time.Second)
			for {
				snap := r.status.get()
				if len(snap.seats) == 2 && snap.started == playing && peers[0].queuedBytes() == 0 && peers[1].queuedBytes() == 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("both peers did not finish their initial output")
				}
				time.Sleep(time.Millisecond)
			}
			// Hold the recipient's outstanding byte budget at its limit,
			// so its next required frame fails as a slow reader's would.
			peers[1].mu.Lock()
			peers[1].bytes = hostedMaxQueuedBytes
			peers[1].mu.Unlock()
			if playing {
				for _, p := range peers {
					if !r.send(hostedEvent{peer: p, body: []byte{hostedOpeningReadyMessage}}) {
						t.Fatal("opening readiness was not received")
					}
				}
			} else {
				body := append([]byte{hostedReadyMessage, 1}, make([]byte, 64)...)
				if !r.send(hostedEvent{peer: peers[0], body: body}) {
					t.Fatal("readiness was not received")
				}
			}
			awaitHostedCapacity(t, s, 0, 0)
			if st := s.Status(); len(st.Recent) != 1 || st.Recent[0].Outcome != finishDisconnected || st.Recent[0].Seat != 1 {
				t.Fatalf("recipient seat 1 failed its output budget: %+v", st.Recent)
			}
		})
	}
}
