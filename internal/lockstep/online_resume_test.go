package lockstep_test

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/lockstep"
	"github.com/nanolathe-gg/nanolathe/internal/relay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// cutProxy forwards TCP connections to a relay and can cut every one it
// carries, or refuse new ones, as a lost route does.
type cutProxy struct {
	ln      net.Listener
	target  string
	mu      sync.Mutex
	conns   []net.Conn
	refused bool
}

func newCutProxy(t *testing.T, target string) *cutProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &cutProxy{ln: ln, target: target}
	t.Cleanup(func() { _ = ln.Close(); p.cut() })
	go func() {
		for {
			in, err := ln.Accept()
			if err != nil {
				return
			}
			p.mu.Lock()
			refused := p.refused
			p.mu.Unlock()
			if refused {
				_ = in.Close()
				continue
			}
			out, err := net.Dial("tcp", target)
			if err != nil {
				_ = in.Close()
				continue
			}
			p.mu.Lock()
			p.conns = append(p.conns, in, out)
			p.mu.Unlock()
			go func() { _, _ = io.Copy(out, in); _ = out.Close() }()
			go func() { _, _ = io.Copy(in, out); _ = in.Close() }()
		}
	}()
	return p
}

func (p *cutProxy) cut() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		_ = c.Close()
	}
	p.conns = nil
}

func (p *cutProxy) refuse() {
	p.mu.Lock()
	p.refused = true
	p.mu.Unlock()
	p.cut()
}

// resumeSeat is one seat's battle: its session, driver and connection.
type resumeSeat struct {
	sess   *session.Session
	driver *lockstep.LocalDriver
	conn   *relay.LocalClient
}

func newResumeSession(t *testing.T, fs vfs.FSOps, config session.EffectiveMatchConfig, seat uint8) (*session.Session, relay.LocalHello) {
	t.Helper()
	inputs, err := session.FreezeMatchInputs(fs, nil, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := session.MatchJoin{Inputs: inputs, Config: config}.Identity()
	if err != nil {
		t.Fatal(err)
	}
	s, err := session.NewPlaytestSkirmish(inputs, config, seat, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PrepareGrantedBattle(); err != nil {
		t.Fatal(err)
	}
	return s, relay.LocalHello{Seat: seat, Identity: identity, InitialChecksum: s.UnitStateChecksum()}
}

// runSeats pumps every seat's driver, as each window host does, until every
// seat has run through tick, failing on any driver error — among them the
// relay's own failure when two seats' 30-tick checksums disagree.
func runSeats(t *testing.T, through uint32, seats ...*resumeSeat) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		done := true
		for i, s := range seats {
			if s.sess.Clock.GlobalTick >= through {
				continue
			}
			done = false
			if _, err := s.driver.Pump(); err != nil {
				t.Fatalf("seat %d at tick %d: %v", i, s.sess.Clock.GlobalTick, err)
			}
		}
		if done {
			return
		}
		if time.Now().After(deadline) {
			for i, s := range seats {
				t.Logf("seat %d at tick %d, connection %+v", i, s.sess.Clock.GlobalTick, s.conn.Connection())
			}
			t.Fatalf("seats did not reach tick %d", through)
		}
		time.Sleep(4 * time.Millisecond)
	}
}

// Two seats play a real online battle through a real relay. Seat 1's route
// is cut: its client resumes on its own and play goes on. Then seat 1's
// game stops for good: a fresh battle composed from tick zero rejoins with
// the seat's ticket, runs the match it missed at full speed, and plays on.
// The relay compares both seats' unit checksums every 30 ticks throughout,
// so a resumed or replayed battle that computed anything differently would
// fail the room (DESIGN_MULTIPLAYER §16.9).
func TestOnlineBattleSurvivesADropAndARestart(t *testing.T) {
	fs := portableFS(t)
	config := onlineConfig(t)
	server, err := relay.ListenHosted("127.0.0.1:0", relay.HostedConfig{InsecureLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	proxy := newCutProxy(t, server.Addr())
	options := relay.HostedDialOptions{InsecureLoopback: true}

	seats := [2]*resumeSeat{{}, {}}
	code := ""
	for seat := range uint8(2) {
		sess, hello := newResumeSession(t, fs, config, seat)
		address := server.Addr()
		if seat == 1 {
			address = proxy.ln.Addr().String()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		conn, joined, err := relay.DialHosted(ctx, address, code, hello, options)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		code = joined
		driver, err := lockstep.NewPacedDriver(sess, conn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = driver.Close() })
		seats[seat] = &resumeSeat{sess: sess, driver: driver, conn: conn}
	}
	runSeats(t, 90, seats[0], seats[1])

	// The route drops; seat 1's reader resumes on its own.
	proxy.cut()
	runSeats(t, 240, seats[0], seats[1])
	if c := seats[1].conn.Connection(); c.Resumes != 1 || c.Reconnecting {
		t.Fatalf("seat 1 after the drop: %+v", c)
	}

	// Seat 1's game stops for good: nothing it holds survives but its
	// ticket. The relay holds the match while seat 0 waits.
	ticket, ok := seats[1].conn.RejoinTicket()
	if !ok {
		t.Fatal("seat 1 has no ticket")
	}
	proxy.refuse()
	sess, _ := newResumeSession(t, fs, config, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	described, err := relay.DescribeHostedRejoin(ctx, server.Addr(), ticket, options)
	cancel()
	if err != nil || described.State.Slot != 1 {
		t.Fatalf("description: %+v %v", described.State, err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	conn, err := relay.ResumeHosted(ctx, server.Addr(), ticket, options)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	driver, err := lockstep.NewPacedDriver(sess, conn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = driver.Close() })
	sealed := conn.Connection().Sealed
	if sealed < 240 {
		t.Fatalf("the relay had sealed only %d ticks", sealed)
	}
	driver.FastForward(sealed)
	rejoined := &resumeSeat{sess: sess, driver: driver, conn: conn}
	start := time.Now()
	runSeats(t, sealed, rejoined)
	if elapsed := time.Since(start); elapsed > time.Duration(sealed)*time.Second/30/2 {
		t.Fatalf("replaying %d ticks took %v, no faster than half real time", sealed, elapsed)
	}
	runSeats(t, sealed+150, seats[0], rejoined)
}
