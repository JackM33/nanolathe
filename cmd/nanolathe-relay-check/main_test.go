package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/netproto"
	"github.com/nanolathe-gg/nanolathe/internal/relay"
)

func TestRelayCheckCreatesDescribesJoinsAndLeavesOnlyItsRoom(t *testing.T) {
	for _, transport := range []string{"tcp", "websocket"} {
		t.Run(transport, func(t *testing.T) {
			finished := make(chan relay.FinishedRoom, 2)
			server, address := listenTestRelay(t, transport, func(room relay.FinishedRoom) { finished <- room })
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			options := relay.HostedDialOptions{InsecureLoopback: true}
			hello := relay.LocalHello{Identity: netproto.Identity{
				Protocol: netproto.CommandSchemaVersion,
				Rules:    netproto.RuleIdentity{Name: "unrelated-room"},
			}}
			other, err := relay.OpenHostedLobby(ctx, address, "", hello, []byte("another lobby"), 2, options)
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close()
			var out bytes.Buffer
			if err := run(ctx, []string{"--address", address, "--insecure-loopback"}, &out); err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf("Relay compatible: command schema %d at %s (create, describe, join; both seats present)\n", netproto.CommandSchemaVersion, address)
			if out.String() != want {
				t.Fatalf("output %q, want %q", out.String(), want)
			}
			select {
			case room := <-finished:
				if room.Ticks != 0 || room.Orders != 0 || server.Status().Totals.Matches != 0 {
					t.Fatalf("compatibility check started a battle: %+v", room)
				}
			case <-ctx.Done():
				t.Fatal("compatibility check left its room open")
			}
			if _, err := relay.DescribeHostedRoom(ctx, address, other.Code(), options); err != nil {
				t.Fatalf("unrelated room was affected: %v", err)
			}
		})
	}
}

func TestRelayCheckReportsRefusal(t *testing.T) {
	server, address := listenTestRelay(t, "websocket", nil)
	server.Drain()
	var out bytes.Buffer
	err := run(context.Background(), []string{"--address", address, "--insecure-loopback"}, &out)
	for _, want := range []string{"logical path create", address, fmt.Sprintf("command schema %d", netproto.CommandSchemaVersion), "restarting"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %v, want %q", err, want)
		}
	}
	if out.Len() != 0 {
		t.Fatalf("reported success after refusal: %q", out.String())
	}
}

func TestRelayCheckBoundsLobbyWait(t *testing.T) {
	_, address := listenTestRelay(t, "websocket", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	hello := relay.LocalHello{Identity: netproto.Identity{
		Protocol: netproto.CommandSchemaVersion,
		Rules:    netproto.RuleIdentity{Name: "relay-check"},
	}}
	lobby, err := relay.OpenHostedLobby(ctx, address, "", hello, []byte("waiting"), 2, relay.HostedDialOptions{InsecureLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	defer lobby.Close()
	ctx, cancelWait := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancelWait()
	if err := awaitBothSeats(ctx, lobby); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting for an absent joiner: %v", err)
	}
}

func listenTestRelay(t *testing.T, transport string, finished func(relay.FinishedRoom)) (*relay.HostedServer, string) {
	t.Helper()
	config := relay.HostedConfig{InsecureLoopback: true, Finished: finished}
	var server *relay.HostedServer
	var err error
	if transport == "websocket" {
		server, err = relay.ListenHostedWebSocket("127.0.0.1:0", config, false)
	} else {
		server, err = relay.ListenHosted("127.0.0.1:0", config)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	address := server.Addr()
	if transport == "websocket" {
		address = "ws://" + address + "/relay"
	}
	return server, address
}
