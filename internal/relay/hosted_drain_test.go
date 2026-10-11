package relay

import (
	"context"
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
