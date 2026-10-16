package relay

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func websocketTestFrame(first byte, payload []byte, masked bool) []byte {
	frame := []byte{first, 0}
	switch {
	case len(payload) < 126:
		frame[1] = byte(len(payload))
	case len(payload) <= 65535:
		frame[1] = 126
		frame = binary.BigEndian.AppendUint16(frame, uint16(len(payload)))
	default:
		frame[1] = 127
		frame = binary.BigEndian.AppendUint64(frame, uint64(len(payload)))
	}
	mask := [4]byte{11, 201, 17, 42}
	if masked {
		frame[1] |= 128
		frame = append(frame, mask[:]...)
	}
	for i, b := range payload {
		if masked {
			b ^= mask[i%4]
		}
		frame = append(frame, b)
	}
	return frame
}

type websocketMemoryConn struct {
	in     *bytes.Reader
	out    bytes.Buffer
	closed bool
}

func (c *websocketMemoryConn) Read(p []byte) (int, error)       { return c.in.Read(p) }
func (c *websocketMemoryConn) Write(p []byte) (int, error)      { return c.out.Write(p) }
func (c *websocketMemoryConn) Close() error                     { c.closed = true; return nil }
func (c *websocketMemoryConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *websocketMemoryConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *websocketMemoryConn) SetDeadline(time.Time) error      { return nil }
func (c *websocketMemoryConn) SetReadDeadline(time.Time) error  { return nil }
func (c *websocketMemoryConn) SetWriteDeadline(time.Time) error { return nil }

func memoryWebSocket(input []byte, client bool, limit int) (*websocketConn, *websocketMemoryConn) {
	raw := &websocketMemoryConn{in: bytes.NewReader(input)}
	return newWebSocketConn(raw, bufio.NewReaderSize(raw, 16), client, limit, time.Second, 0), raw
}

func TestWebSocketFragmentationAndControlInterleaving(t *testing.T) {
	wire := websocketTestFrame(0x02, []byte("abc"), true)
	wire = append(wire, websocketTestFrame(0x89, []byte("echo"), true)...)
	wire = append(wire, websocketTestFrame(0x8a, []byte("unsolicited"), true)...)
	wire = append(wire, websocketTestFrame(0x00, nil, true)...)
	wire = append(wire, websocketTestFrame(0x80, []byte("defg"), true)...)
	wire = append(wire, websocketTestFrame(0x82, nil, true)...)
	wire = append(wire, websocketTestFrame(0x82, []byte("h"), true)...)
	c, raw := memoryWebSocket(wire, false, 7)
	defer c.Close()
	var got []byte
	for range 8 {
		var b [1]byte
		if _, err := io.ReadFull(c, b[:]); err != nil {
			t.Fatal(err)
		}
		got = append(got, b[0])
	}
	if string(got) != "abcdefgh" {
		t.Fatalf("fragment stream %q", got)
	}
	if want := []byte{0x8a, 4, 'e', 'c', 'h', 'o'}; !bytes.Equal(raw.out.Bytes(), want) {
		t.Fatalf("ping response %x, want %x", raw.out.Bytes(), want)
	}
}

func TestWebSocketCanonicalLengthsAndIncrementalMasking(t *testing.T) {
	for _, size := range []int{1, 125, 126, 65535, 65536, 70001} {
		for _, client := range []bool{false, true} {
			payload := make([]byte, size)
			for i := range payload {
				payload[i] = byte(i*19 + 3)
			}
			c, raw := memoryWebSocket(websocketTestFrame(0x82, payload, !client), client, size)
			var got bytes.Buffer
			// An odd chunk size forces masks to continue across Read calls.
			chunk := make([]byte, 113)
			for got.Len() < size {
				n, err := c.Read(chunk[:min(len(chunk), size-got.Len())])
				if err != nil {
					t.Fatal(err)
				}
				got.Write(chunk[:n])
			}
			if !bytes.Equal(got.Bytes(), payload) {
				t.Fatalf("mask/length changed %d bytes (client %v)", size, client)
			}
			_ = c.Close()
			if !raw.closed {
				t.Fatal("Close did not release raw socket")
			}
		}
	}
	// A huge announced message is refused without receiving its payload.
	wire := []byte{0x82, 127}
	wire = binary.BigEndian.AppendUint64(wire, 1<<40)
	c, raw := memoryWebSocket(wire, true, localMaxGrantFrame+5)
	var b [1]byte
	if _, err := c.Read(b[:]); err == nil || errors.Is(err, io.EOF) || !raw.closed {
		t.Fatalf("oversized announced length: %v", err)
	}
	_ = c.Close()
}

func TestWebSocketMalformedFramesAbort(t *testing.T) {
	for _, tc := range []struct {
		name   string
		wire   []byte
		client bool
		limit  int
	}{
		{"unmasked-client", websocketTestFrame(0x82, []byte{1}, false), false, 10},
		{"masked-server", websocketTestFrame(0x82, []byte{1}, true), true, 10},
		{"reserved-bit", websocketTestFrame(0xc2, []byte{1}, false), true, 10},
		{"text", websocketTestFrame(0x81, []byte{1}, false), true, 10},
		{"reserved-data", websocketTestFrame(0x83, []byte{1}, false), true, 10},
		{"reserved-control", websocketTestFrame(0x8b, nil, false), true, 10},
		{"unexpected-continuation", websocketTestFrame(0x80, nil, false), true, 10},
		{"nested-message", append(websocketTestFrame(0x02, nil, false), websocketTestFrame(0x82, nil, false)...), true, 10},
		{"unfinished-ping", websocketTestFrame(0x09, nil, false), true, 10},
		{"oversized-ping", websocketTestFrame(0x89, make([]byte, 126), false), true, 200},
		{"nonshort-16", []byte{0x82, 126, 0, 125}, true, 1000},
		{"nonshort-64", []byte{0x82, 127, 0, 0, 0, 0, 0, 0, 255, 255}, true, 100000},
		{"negative-64", []byte{0x82, 127, 128, 0, 0, 0, 0, 1, 0, 0}, true, 100000},
		{"message-bound", websocketTestFrame(0x82, []byte{1, 2}, false), true, 1},
		{"fragment-bound", append(websocketTestFrame(0x02, []byte{1}, false), websocketTestFrame(0x80, []byte{2}, false)...), true, 1},
		{"close-one-byte", websocketTestFrame(0x88, []byte{3}, false), true, 10},
		{"close-reserved", websocketTestFrame(0x88, []byte{3, 237}, false), true, 10},
		{"close-invalid-utf8", websocketTestFrame(0x88, []byte{3, 232, 255}, false), true, 10},
		{"truncated-mask", []byte{0x82, 129, 1, 2}, false, 10},
		{"truncated-body", []byte{0x82, 2, 1}, true, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, raw := memoryWebSocket(tc.wire, tc.client, tc.limit)
			defer c.Close()
			_, err := io.ReadAll(c)
			if err == nil || errors.Is(err, io.EOF) || !raw.closed {
				t.Fatalf("did not abort: %v, closed %v", err, raw.closed)
			}
			var b [1]byte
			if _, again := c.Read(b[:]); again != err {
				t.Fatalf("did not retain stream failure: %v / %v", err, again)
			}
		})
	}
}

func TestWebSocketCloseReplyAndNoNormalEOF(t *testing.T) {
	for _, body := range [][]byte{nil, {3, 232}, {3, 233, 'b', 'y', 'e'}} {
		c, raw := memoryWebSocket(websocketTestFrame(0x88, body, true), false, 10)
		var b [1]byte
		if _, err := c.Read(b[:]); err != io.ErrUnexpectedEOF {
			t.Fatalf("close awarded EOF: %v", err)
		}
		if !bytes.Equal(raw.out.Bytes(), websocketTestFrame(0x88, body, false)) {
			t.Fatalf("close reply: %x", raw.out.Bytes())
		}
		if _, err := c.Write([]byte{1}); err == nil {
			t.Fatal("data sent after close")
		}
		_ = c.Close()
	}
}

// A ping already in flight must not turn the relay's bounded drain into an
// immediate close with unread client data (DESIGN_MULTIPLAYER §16.5.1).
func TestWebSocketSentCloseDrainsThroughPing(t *testing.T) {
	payload := []byte("late acknowledgment")
	wire := websocketTestFrame(0x89, []byte("in-flight ping"), true)
	wire = append(wire, websocketTestFrame(0x82, payload, true)...)
	c, raw := memoryWebSocket(wire, false, 1024)
	defer c.Close()
	closeHostedWrite(c)
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(c, got); err != nil || !bytes.Equal(got, payload) || raw.closed {
		t.Fatalf("ping interrupted closing drain: data=%q closed=%v err=%v", got, raw.closed, err)
	}
	if want := websocketTestFrame(0x88, []byte{3, 232}, false); !bytes.Equal(raw.out.Bytes(), want) {
		t.Fatalf("sent more frames after close: %x", raw.out.Bytes())
	}
}

func TestWebSocketWriterMaskingAndLength(t *testing.T) {
	for _, client := range []bool{false, true} {
		for _, size := range []int{0, 125, 126, 65535, 65536, 70001} {
			c, raw := memoryWebSocket(nil, client, 100000)
			payload := bytes.Repeat([]byte{91}, size)
			if n, err := c.Write(payload); err != nil || n != size {
				t.Fatalf("Write: %d %v", n, err)
			}
			wire := raw.out.Bytes()
			if wire[0] != 0x82 || (wire[1]&128 != 0) != client {
				t.Fatalf("frame flags: %x", wire[:2])
			}
			n := 2
			wantLength := uint64(size)
			length := uint64(wire[1] & 127)
			if size >= 65536 {
				if length != 127 {
					t.Fatal("noncanonical large length")
				}
				length = binary.BigEndian.Uint64(wire[2:10])
				n = 10
			} else if size >= 126 {
				if length != 126 {
					t.Fatal("noncanonical medium length")
				}
				length = uint64(binary.BigEndian.Uint16(wire[2:4]))
				n = 4
			}
			if length != wantLength {
				t.Fatalf("length %d != %d", length, wantLength)
			}
			if client {
				mask := wire[n : n+4]
				n += 4
				for i, b := range wire[n:] {
					if b^mask[i%4] != 91 {
						t.Fatalf("mask changed payload at %d", i)
					}
				}
			} else if !bytes.Equal(wire[n:], payload) {
				t.Fatal("server changed payload")
			}
			if !bytes.Equal(payload, bytes.Repeat([]byte{91}, size)) {
				t.Fatal("Write mutated caller buffer")
			}
			_ = c.Close()
		}
	}
}

func TestWebSocketKeepaliveAndCloseUnblocksIO(t *testing.T) {
	a, b := net.Pipe()
	c := newWebSocketConn(a, bufio.NewReader(a), false, 100, time.Second, 5*time.Millisecond)
	defer c.Close()
	defer b.Close()
	// An old data deadline must not kill an idle room's periodic ping.
	_ = c.SetWriteDeadline(time.Now().Add(-time.Second))
	_ = b.SetDeadline(time.Now().Add(time.Second))
	// Each ping carries its send time, so an echoing pong measures the
	// round trip for the status page.
	var ping [10]byte
	if _, err := io.ReadFull(b, ping[:]); err != nil || ping[0] != 0x89 || ping[1] != 8 {
		t.Fatalf("keepalive: %x / %v", ping, err)
	}
	readDone := make(chan error, 1)
	go func() { var p [1]byte; _, err := c.Read(p[:]); readDone <- err }()
	// A pipe echoes within one tick of a coarse monotonic clock (about 1ms on
	// Windows), which would measure a zero round trip; let the clock pass the
	// ping's send time first.
	for sent := time.Duration(binary.BigEndian.Uint64(ping[2:])); time.Since(c.epoch) <= sent; {
		time.Sleep(time.Millisecond)
	}
	if err := websocketWriteAll(b, websocketTestFrame(0x8a, ping[2:], true)); err != nil {
		t.Fatal(err)
	}
	if err := websocketWriteAll(b, websocketTestFrame(0x8a, nil, true)); err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(time.Second); c.rtt.Load() <= 0; {
		if time.Now().After(end) {
			t.Fatal("an echoed ping did not record its round trip")
		}
		time.Sleep(time.Millisecond)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("Close left Read blocked")
	}
	select {
	case <-c.pingDone:
	default:
		t.Fatal("Close left keepalive running")
	}
	// A blocked data write and a queued keepalive are both released by Close.
	a, b = net.Pipe()
	defer b.Close()
	c = newWebSocketConn(a, bufio.NewReader(a), false, 100, time.Second, time.Millisecond)
	writeDone := make(chan error, 1)
	go func() { _, err := c.Write([]byte("blocked")); writeDone <- err }()
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-writeDone:
		if err == nil {
			t.Fatal("blocked Write succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("Close left Write blocked")
	}
	// With no reader, a ping's own timeout terminates its socket and goroutine.
	a, b = net.Pipe()
	defer b.Close()
	c = newWebSocketConn(a, bufio.NewReader(a), false, 100, 15*time.Millisecond, time.Millisecond)
	select {
	case <-c.pingDone:
	case <-time.After(time.Second):
		t.Fatal("blocked keepalive did not expire")
	}
	_ = c.Close()
}

func TestWebSocketConcurrentControlAndDataWrites(t *testing.T) {
	a, b := net.Pipe()
	c := newWebSocketConn(a, bufio.NewReader(a), false, 100, time.Second, 0)
	defer c.Close()
	defer b.Close()
	_ = b.SetReadDeadline(time.Now().Add(time.Second))
	done := make(chan error, 2)
	go func() {
		for range 30 {
			if _, err := c.Write([]byte{3, 7, 11}); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	go func() {
		for range 30 {
			if err := c.writeControl(9, []byte{17}); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for range 60 {
		var h [2]byte
		if _, err := io.ReadFull(b, h[:]); err != nil {
			t.Fatal(err)
		}
		var p [3]byte
		if (h[0] != 0x82 || h[1] != 3) && (h[0] != 0x89 || h[1] != 1) {
			t.Fatalf("interleaved header: %x", h)
		}
		if _, err := io.ReadFull(b, p[:h[1]]); err != nil {
			t.Fatal(err)
		}
		if (h[0] == 0x82 && p != [3]byte{3, 7, 11}) || (h[0] == 0x89 && p[0] != 17) {
			t.Fatalf("interleaved payload: %x", p)
		}
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
