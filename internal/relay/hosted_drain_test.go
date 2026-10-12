package relay

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// Draining for a restart refuses new rooms, joins and descriptions and
// closes waiting lobbies, while a running match plays on
// (DESIGN_MULTIPLAYER §12.5).
func TestHostedDrainKeepsRunningMatches(t *testing.T) {
	s := listenHostedTest(t, HostedConfig{InsecureLoopback: true}, hostedDefaultTimeouts)
	clients, _ := hostedTestPair(t, s)
	lobby, code := dialHostedTest(t, s, "", 0)
	play := func(ticks int) {
		t.Helper()
		errs := make(chan error, len(clients))
		for _, c := range clients {
			go func() {
				for range ticks {
					_ = c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
					g, err := c.ReadGrant()
					if err == nil {
						err = c.Acknowledge(g.Tick, [32]byte{}, false, false)
					}
					if err != nil {
						errs <- err
						return
					}
				}
				errs <- nil
			}()
		}
		for range clients {
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
		}
	}
	play(10)
	s.Drain()
	if _, err := lobby.ReadGrant(); err == nil || !strings.Contains(err.Error(), "restarting") {
		t.Fatalf("a waiting lobby was not closed for the restart: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, room := range []string{"", code} {
		if _, _, err := DialHosted(ctx, s.Addr(), room, localTestHello(1), HostedDialOptions{InsecureLoopback: true}); err == nil || !strings.Contains(err.Error(), "restarting") {
			t.Fatalf("admitted room %q while draining: %v", room, err)
		}
	}
	if _, err := describeTest(s.Addr(), code, HostedDialOptions{InsecureLoopback: true}); err == nil || !strings.Contains(err.Error(), "restarting") {
		t.Fatalf("described a room while draining: %v", err)
	}
	play(40)
	if st := s.Status(); !st.Draining || st.Matches != 1 || st.Lobbies != 0 {
		t.Fatalf("draining status: draining %v, %d matches, %d lobbies", st.Draining, st.Matches, st.Lobbies)
	}
}

// A join already being processed when Drain returns must not auto-start
// the waiting lobby (DESIGN_MULTIPLAYER §12.5).
func TestHostedDrainRejectsPendingAutoStart(t *testing.T) {
	s, r, peers, _ := drainRoomTest(t, true)
	// The join has been received, but its configuration cannot be queued
	// until the peer's writer releases this mutex. Drain wins that interval.
	peers[1].mu.Lock()
	joined := r.send(hostedEvent{peer: peers[1], join: true})
	s.Drain()
	peers[1].mu.Unlock()
	if !joined {
		t.Fatal("join was not received")
	}
	awaitHostedCapacity(t, s, 0, 0)
	st := s.Status()
	if st.Totals.Matches != 0 || len(st.Recent) != 1 || st.Recent[0].Outcome != finishServer || st.Recent[0].Seat != -1 {
		t.Fatalf("pending lobby started or was not closed for shutdown: %+v", st)
	}
}

// Started can reach one peer before the next progress snapshot is ready.
// Shutdown must still count that match (DESIGN_MULTIPLAYER §12.5).
func TestHostedDrainCountsStartedMatchBeforeSnapshot(t *testing.T) {
	s, r, peers, hostStarted := drainRoomTest(t, false)
	if !r.send(hostedEvent{peer: peers[1], join: true}) {
		t.Fatal("join was not received")
	}
	ready := append([]byte{hostedReadyMessage, 1}, make([]byte, 64)...)
	for _, p := range peers {
		if !r.send(hostedEvent{peer: p, body: ready}) {
			t.Fatal("readiness was not received")
		}
	}
	deadline := time.Now().Add(time.Second)
	for {
		snap := r.status.get()
		if len(snap.seats) == 2 && snap.seats[0].ready && snap.seats[1].ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("both seats did not become ready")
		}
		time.Sleep(time.Millisecond)
	}
	// Start reaches the host while the room waits to queue it to the
	// joiner, before begin can publish its playing snapshot.
	peers[1].mu.Lock()
	defer peers[1].mu.Unlock()
	if !r.send(hostedEvent{peer: peers[0], body: []byte{hostedStartMessage}}) {
		t.Fatal("start was not received")
	}
	select {
	case <-hostStarted:
	case <-time.After(time.Second):
		t.Fatal("host did not receive Started")
	}
	s.Drain()
	if r.status.get().started {
		t.Fatal("fixture did not hold the old lobby snapshot")
	}
	if st := s.Status(); !st.Draining || st.Matches != 1 || st.Lobbies != 0 {
		t.Fatalf("shutdown did not count the started match: %+v", st)
	}
	select {
	case <-r.done:
		t.Fatal("drain ended a started match")
	default:
	}
}

// drainRoomTest uses ordinary admission and the real room loop, with pipe
// peers so tests can hold one writer at a lifecycle transition.
func drainRoomTest(t *testing.T, autoStart bool) (*HostedServer, *hostedRoom, [2]*hostedPeer, <-chan struct{}) {
	t.Helper()
	s := listenHostedTest(t, HostedConfig{InsecureLoopback: true}, hostedDefaultTimeouts)
	var peers [2]*hostedPeer
	var clients [2]net.Conn
	for i := range peers {
		server, client := net.Pipe()
		clients[i] = client
		peers[i] = newHostedPeer(server, localTestHello(uint8(i)), time.Second)
		peers[i].version = hostedVersion
	}
	t.Cleanup(func() {
		for i, p := range peers {
			p.stop()
			_ = p.conn.Close()
			_ = clients[i].Close()
		}
	})
	var flags uint8
	if autoStart {
		flags = hostedAutoStart
	}
	r, _, err := s.admit("", flags, 2, []byte{1}, peers[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.admit(r.code, 0, 0, nil, peers[1]); err != nil {
		t.Fatal(err)
	}
	for _, p := range peers {
		p.room = r
		go p.write()
	}
	hostStarted := make(chan struct{})
	go func() {
		for {
			body, err := readLocalFrame(clients[0], localMaxGrantFrame)
			if err != nil {
				return
			}
			if body[0] == hostedStartedMessage {
				close(hostStarted)
			}
		}
	}()
	go func() { _, _ = io.Copy(io.Discard, clients[1]) }()
	go r.run()
	return s, r, peers, hostStarted
}
