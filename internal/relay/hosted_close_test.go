package relay

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Fixture clients rarely close promptly, so every test that awaits released
// capacity would otherwise wait out the production linger. Tests of the
// linger itself set their own.
func TestMain(m *testing.M) {
	hostedDefaultTimeouts.linger = 50 * time.Millisecond
	os.Exit(m.Run())
}

// A client still sending after the relay's last frame receives that frame on
// every transport: the relay half-closes and reads through the late bytes, so
// its close is never a reset, which Windows delivers ahead of unread data. A
// client that keeps sending and never closes still releases its capacity
// once the linger passes (§16.5.1).
func TestHostedLastFrameSurvivesLateClientWrites(t *testing.T) {
	for _, transport := range []string{"tcp", "tls", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			timeouts := hostedDefaultTimeouts
			timeouts.linger = 500 * time.Millisecond
			finished := make(chan FinishedRoom, 1)
			config := HostedConfig{InsecureLoopback: true, Finished: func(f FinishedRoom) { finished <- f }}
			options := HostedDialOptions{InsecureLoopback: true}
			if transport == "tls" || transport == "wss" {
				serverTLS, clientTLS := hostedTestTLS(t)
				config.InsecureLoopback, config.TLSConfig = false, serverTLS
				options = HostedDialOptions{TLSConfig: clientTLS}
			}
			var s *HostedServer
			address, dial := "", DialHosted
			if transport == "ws" || transport == "wss" {
				s = listenWebSocketTest(t, config, false, timeouts)
				address, dial = transport+"://"+s.Addr()+"/relay", DialHostedWebSocket
			} else {
				s = listenHostedTest(t, config, timeouts)
				address = s.Addr()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			c, _, err := dial(ctx, address, "", localTestHello(0), options)
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = c.Close() })
			c.idle = 0
			_ = c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))

			// An unknown message fails the room. Finished runs only after the
			// relay's writer has flushed the failure and half-closed.
			if err := writeLocalFrame(c.conn, []byte{255}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-finished:
			case <-time.After(3 * time.Second):
				t.Fatal("the room did not finish")
			}
			lingerStarted := time.Now()
			if transport == "ws" || transport == "wss" {
				// A keepalive can cross the relay's close before this client
				// reads it. Its refused pong must not abort the drain.
				stream := c.conn.(hostedClientConn).Conn.(*websocketConn)
				if err := stream.writeControl(9, []byte("in-flight ping")); err != nil {
					t.Fatal(err)
				}
			}
			// The client has not read the failure and is still sending, as a
			// battle client acknowledges every tick.
			if err := c.Acknowledge(1, [32]byte{}, false, false); err != nil {
				t.Fatalf("the relay stopped reading before its linger: %v", err)
			}
			stop, sent := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(sent)
				for {
					select {
					case <-stop:
						return
					case <-time.After(5 * time.Millisecond):
					}
					if c.Acknowledge(1, [32]byte{}, false, false) != nil {
						return
					}
				}
			}()
			defer func() { close(stop); <-sent }()
			if _, err := c.ReadGrant(); err == nil || !strings.Contains(err.Error(), "acknowledgment or leave after hello") {
				t.Fatalf("late writes lost the relay's reason: %v", err)
			}
			awaitHostedCapacity(t, s, 0, 0)
			if elapsed := time.Since(lingerStarted); elapsed < timeouts.linger/2 {
				t.Fatalf("connection closed before its linger: %v", elapsed)
			}
		})
	}
}
