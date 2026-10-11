package main

import (
	"context"
	"github.com/nanolathe-gg/nanolathe/internal/relay"
	"io"
	"strings"
	"testing"
)

func TestServerRequiresExplicitTransport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, args := range [][]string{
		nil, {"--tls-cert", "missing.pem"},
		{"--behind-tls-proxy"}, {"--websocket", "--behind-tls-proxy", "--insecure-loopback"},
		{"--websocket", "--behind-tls-proxy", "--tls-key", "key.pem"}, {"--insecure-loopback", "--tls-cert", "cert.pem"},
		{"--insecure-loopback", "--listen", "0.0.0.0:0"}, {"--insecure-loopback", "--max-rooms", "257"},
		{"--insecure-loopback", "--max-rooms", "100", "--max-connections", "199"}, {"--insecure-loopback", "--max-connections", "1025"},
		{"--insecure-loopback", "unexpected-argument"},
	} {
		if err := run(ctx, args, io.Discard); err == nil {
			t.Fatalf("admitted %v", args)
		}
	}
	var out strings.Builder
	if err := run(ctx, []string{"--insecure-loopback", "--listen", "127.0.0.1:0"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "plaintext loopback test") {
		t.Fatal("test transport not identified")
	}
	if err := run(ctx, []string{"--websocket", "--behind-tls-proxy", "--listen", "127.0.0.1:0"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, []string{"--help"}, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestHealthListenFlag(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out strings.Builder
	if err := run(ctx, []string{"--insecure-loopback", "--listen", "127.0.0.1:0", "--health-listen", "127.0.0.1:0"}, &out); err != nil || !strings.Contains(out.String(), "relay health on 127.0.0.1:") {
		t.Fatalf("health flag: %v %q", err, out.String())
	}
}

func TestServerStatsLine(t *testing.T) {
	var out strings.Builder
	logServerStats(&out, relay.HostedStatus{Players: 3, Matches: 1})
	if !strings.HasPrefix(out.String(), "relay: players=3 matches=1 ") || !strings.Contains(out.String(), "heap=") {
		t.Fatalf("stats line: %q", out.String())
	}
}

// A finished room's operator line names its outcome and seat, and nothing
// that identifies a player or an invitation.
func TestFinishedRoomLogLine(t *testing.T) {
	var out strings.Builder
	logFinishedRoom(&out, relay.FinishedRoom{ID: 7, Size: 4, Players: 3, Ticks: 900, Orders: 12, Outcome: "command flood", Seat: 2, DurationSeconds: 31.6})
	if got, want := out.String(), "relay: room=7 outcome=\"command flood\" seat=2 players=3/4 ticks=900 orders=12 duration=32s\n"; got != want {
		t.Fatalf("log line %q, want %q", got, want)
	}
}
