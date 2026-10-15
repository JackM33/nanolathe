package relay

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/netproto"
)

// DESIGN_MULTIPLAYER §12.2: command schemas belong to the clients. One relay
// serves old and future schemas without a rebuild, but never mixes them in
// either a lobby or a command-line room. Refusal must not consume a seat.
func TestHostedCommandSchemaIsPerRoom(t *testing.T) {
	for _, schema := range []uint16{1, 2, netproto.CommandSchemaVersion + 1, 65535} {
		for _, auto := range []bool{false, true} {
			t.Run(fmt.Sprintf("schema%d/auto%t", schema, auto), func(t *testing.T) {
				s := listenWebSocketTest(t, HostedConfig{InsecureLoopback: true}, false, hostedDefaultTimeouts)
				address := "ws://" + s.Addr() + "/relay"
				options := HostedDialOptions{InsecureLoopback: true}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				hello := localTestHello(0)
				hello.Identity.Protocol = schema
				var clients [2]*LocalClient
				var host *HostedLobby
				var code string
				var err error
				if auto {
					clients[0], code, err = DialHosted(ctx, address, "", hello, options)
				} else {
					host, err = OpenHostedLobby(ctx, address, "", hello, []byte{1}, 2, options)
					if err == nil {
						defer host.Close()
						code = host.Code()
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					for _, c := range clients {
						if c != nil {
							_ = c.Close()
						}
					}
				}()
				wrong := hello
				wrong.Identity.Protocol = 1
				if schema == 1 {
					wrong.Identity.Protocol = 2
				}
				// Both API forms reach admission before any Ready digests exist.
				bad, _, refusal := DialHosted(ctx, address, code, wrong, options)
				if bad != nil {
					_ = bad.Close()
				}
				if refusal == nil || !strings.Contains(refusal.Error(), "identical command schemas") {
					t.Fatalf("mixed schema admitted or misdiagnosed: %v", refusal)
				}
				if auto {
					clients[1], _, err = DialHosted(ctx, address, code, hello, options)
					if err != nil {
						t.Fatal(err)
					}
				} else {
					joiner, err := OpenHostedLobby(ctx, address, code, hello, nil, 0, options)
					if err != nil {
						t.Fatal(err)
					}
					defer joiner.Close()
					for _, l := range []*HostedLobby{host, joiner} {
						awaitLobby(t, l, "both matching seats", present(true))
						if err := l.SetReady(true, [32]byte{1}, [32]byte{2}); err != nil {
							t.Fatal(err)
						}
					}
					awaitLobby(t, host, "both ready", func(st HostedLobbyState, err error) bool {
						return err == nil && st.Seats[0].Ready && st.Seats[1].Ready
					})
					if err := host.Start(); err != nil {
						t.Fatal(err)
					}
					for i, l := range []*HostedLobby{host, joiner} {
						awaitLobby(t, l, "Started", func(st HostedLobbyState, err error) bool { return err == nil && st.Started })
						clients[i] = openingBattleTest(t, l)
					}
				}
				for _, c := range clients {
					_ = c.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
				}
				if g := readLocalPair(t, clients); g.Tick != 1 {
					t.Fatalf("first grant: %+v", g)
				}
			})
		}
	}
}
