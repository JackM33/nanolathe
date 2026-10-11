// Command nanolathe-server hosts bounded relay rooms without assets.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/relay"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("nanolathe-server", flag.ContinueOnError)
	flags.SetOutput(out)
	address := flags.String("listen", "127.0.0.1:39032", "TCP listen address")
	certFile := flags.String("tls-cert", "", "TLS certificate chain PEM")
	keyFile := flags.String("tls-key", "", "TLS private key PEM")
	insecure := flags.Bool("insecure-loopback", false, "plaintext on numeric loopback for local tests only")
	websocket := flags.Bool("websocket", false, "serve WebSocket /relay and HTTP /healthz")
	proxyTLS := flags.Bool("behind-tls-proxy", false, "serve HTTP behind a trusted HTTPS proxy (requires --websocket)")
	maxRooms := flags.Int("max-rooms", 16, "maximum simultaneous rooms, 1..256")
	maxConnections := flags.Int("max-connections", 512, "maximum established and pending connections, 2..1024 and at least two per room")
	healthAddress := flags.String("health-listen", "", "also answer HTTP GET /healthz here, outside the relay's connection limit; never route it publicly")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || *maxRooms < 1 || *maxRooms > 256 || *maxConnections < 2**maxRooms || *maxConnections > 1024 {
		return serverError("arguments", "flags only, --max-rooms 1..256 and --max-connections 2..1024 with at least two per room")
	}
	if *proxyTLS && (!*websocket || *insecure || *certFile != "" || *keyFile != "") {
		return serverError("proxy transport", "--websocket without local TLS or loopback options")
	}
	out = &lockedWriter{w: out}
	config := relay.HostedConfig{InsecureLoopback: *insecure, MaxRooms: *maxRooms, MaxConnections: *maxConnections,
		Finished: func(f relay.FinishedRoom) { logFinishedRoom(out, f) }}
	transport := "TLS"
	if *proxyTLS {
		transport = "WebSocket behind HTTPS proxy"
	} else if *insecure {
		if *certFile != "" || *keyFile != "" {
			return serverError("TLS options", "either a certificate/key pair or --insecure-loopback")
		}
		transport = "plaintext loopback test"
	} else {
		if *certFile == "" || *keyFile == "" {
			return serverError("TLS options", "--tls-cert and --tls-key, or --insecure-loopback for a local test")
		}
		cert, err := tls.LoadX509KeyPair(*certFile, *keyFile)
		if err != nil {
			return fmt.Errorf("%w: %v", serverError(*certFile, "a readable matching TLS certificate/key pair"), err)
		}
		config.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}}
	}
	var server *relay.HostedServer
	var err error
	if *websocket {
		server, err = relay.ListenHostedWebSocket(*address, config, *proxyTLS)
	} else {
		server, err = relay.ListenHosted(*address, config)
	}
	if err != nil {
		return err
	}
	defer server.Close()
	if *healthAddress != "" {
		health, err := relay.ListenHealth(*healthAddress)
		if err != nil {
			return err
		}
		defer health.Close()
		fmt.Fprintf(out, "Nanolathe relay health on %s\n", health.Addr())
	}
	fmt.Fprintf(out, "Nanolathe relay listening on %s (%s, at most %d rooms and %d connections)\n", server.Addr(), transport, *maxRooms, *maxConnections)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			logServerStats(out, server.Status())
		}
	}
}

// logServerStats writes one line for the operator's logs. Process-level
// figures appear only here, never on the public status page
// (DESIGN_MULTIPLAYER §12.5).
func logServerStats(out io.Writer, st relay.HostedStatus) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	fmt.Fprintf(out, "relay: players=%d matches=%d lobbies=%d connections=%d started=%d completed=%d failed=%d chat=%d chat_dropped=%d heap=%dMiB sys=%dMiB goroutines=%d\n",
		st.Players, st.Matches, st.Lobbies, st.Connections, st.Totals.Matches, st.Totals.Completed, st.Totals.Failed,
		st.Totals.ChatRelayed, st.Totals.ChatDropped, mem.HeapAlloc>>20, mem.Sys>>20, runtime.NumGoroutine())
}

// logFinishedRoom writes one line per ended room, so a failure's reason,
// tick and seat outlive the status page's last 20 rooms and a restart. It
// carries no room code, address, name or chat (DESIGN_MULTIPLAYER §12.5).
func logFinishedRoom(out io.Writer, f relay.FinishedRoom) {
	fmt.Fprintf(out, "relay: room=%d outcome=%q seat=%d players=%d/%d ticks=%d orders=%d duration=%.0fs\n",
		f.ID, f.Outcome, f.Seat, f.Players, f.Size, f.Ticks, f.Orders, f.DurationSeconds)
}

// lockedWriter serializes the operator log's writers: the minute ticker and
// every room's goroutine.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func serverError(path, expected string) error {
	return fmt.Errorf("nanolathe: relay server setup failed: logical path %s, providers searched [command line], expected %s", path, expected)
}
