package relay

import (
	"bufio"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// websocketConn turns binary messages into the existing envelope byte stream.
// One caller reads; writes, automatic pongs and keepalives share writeMu. Frame
// and cumulative fragmented-message bounds are checked before reading payload
// bytes, without allocating an entire WebSocket message (RFC 6455 §5).
type websocketConn struct {
	net.Conn
	reader                  *bufio.Reader
	client                  bool
	limit                   uint64
	timeout                 time.Duration
	remaining, messageBytes uint64
	fragmented              bool
	mask                    [4]byte
	maskOffset              uint8
	readErr                 error
	writeMu                 sync.Mutex
	deadlineMu              sync.Mutex
	writeDeadline           time.Time
	closeSent               bool // writeMu
	// rtt is the last ping's round trip in nanoseconds; each ping carries
	// its send time since epoch so the pong needs no bookkeeping.
	epoch    time.Time
	rtt      atomic.Int64
	once     sync.Once
	done     chan struct{}
	pingDone chan struct{}
}

func newWebSocketConn(conn net.Conn, reader *bufio.Reader, client bool, limit int, timeout, pingInterval time.Duration) *websocketConn {
	c := &websocketConn{Conn: conn, reader: reader, client: client, limit: uint64(limit), timeout: timeout, epoch: time.Now(), done: make(chan struct{}), pingDone: make(chan struct{})}
	if pingInterval > 0 {
		go c.keepalive(pingInterval)
	} else {
		close(c.pingDone)
	}
	return c
}

func (c *websocketConn) shutdown() {
	c.once.Do(func() { close(c.done); _ = c.Conn.Close() })
}

func (c *websocketConn) Close() error {
	c.shutdown()
	<-c.pingDone
	return nil
}

func (c *websocketConn) keepalive(interval time.Duration) {
	defer close(c.pingDone)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			var sent [8]byte
			binary.BigEndian.PutUint64(sent[:], uint64(time.Since(c.epoch)))
			if err := c.writeControl(9, sent[:]); err != nil {
				// After this side's close frame the owner drains and closes
				// the stream; stop pinging without cutting that short.
				if err != errWebSocketCloseSent {
					c.shutdown()
				}
				return
			}
		}
	}
}

func (c *websocketConn) SetDeadline(t time.Time) error {
	if err := c.SetReadDeadline(t); err != nil {
		return err
	}
	return c.SetWriteDeadline(t)
}

func (c *websocketConn) SetWriteDeadline(t time.Time) error {
	c.deadlineMu.Lock()
	c.writeDeadline = t
	c.deadlineMu.Unlock()
	return c.Conn.SetWriteDeadline(t)
}

func (c *websocketConn) Read(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}
	if c.readErr != nil {
		return 0, c.readErr
	}
	defer func() {
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			c.readErr = err
			c.shutdown()
		}
	}()
	for c.remaining == 0 {
		if err := c.readHeader(); err != nil {
			return 0, err
		}
	}
	p = p[:min(uint64(len(p)), c.remaining)]
	n, err = c.reader.Read(p)
	if !c.client {
		for i := range p[:n] {
			p[i] ^= c.mask[c.maskOffset&3]
			c.maskOffset++
		}
	}
	c.remaining -= uint64(n)
	return n, err
}

func (c *websocketConn) readHeader() error {
	var header [8]byte
	if _, err := io.ReadFull(c.reader, header[:2]); err != nil {
		return err
	}
	fin, opcode := header[0]&0x80 != 0, header[0]&15
	masked := header[1]&0x80 != 0
	if header[0]&0x70 != 0 || masked == c.client {
		return hostedError("WebSocket frame", "no reserved bits and the correct masking direction")
	}
	size := uint64(header[1] & 127)
	switch size {
	case 126:
		if _, err := io.ReadFull(c.reader, header[:2]); err != nil {
			return err
		}
		size = uint64(binary.BigEndian.Uint16(header[:2]))
		if size < 126 {
			return hostedError("WebSocket length", "the shortest encoding")
		}
	case 127:
		if _, err := io.ReadFull(c.reader, header[:]); err != nil {
			return err
		}
		size = binary.BigEndian.Uint64(header[:])
		if size < 65536 || size>>63 != 0 {
			return hostedError("WebSocket length", "the shortest nonnegative encoding")
		}
	}
	if opcode >= 8 {
		if !fin || size > 125 || (opcode != 8 && opcode != 9 && opcode != 10) {
			return hostedError("WebSocket control", "a known final control frame of at most 125 bytes")
		}
	} else {
		if (opcode != 0 && opcode != 2) || (opcode == 0) != c.fragmented {
			return hostedError("WebSocket message", "binary data with ordered continuation frames")
		}
		if !c.fragmented {
			c.messageBytes = 0
		}
		if size > c.limit-c.messageBytes {
			return hostedError("WebSocket message", "a message within the relay envelope limit")
		}
		c.messageBytes += size
		c.fragmented = !fin
	}
	c.maskOffset = 0
	if masked {
		if _, err := io.ReadFull(c.reader, c.mask[:]); err != nil {
			return err
		}
	}
	if opcode < 8 {
		c.remaining = size
		return nil
	}
	var control [125]byte
	body := control[:int(size)]
	if _, err := io.ReadFull(c.reader, body); err != nil {
		return err
	}
	if masked {
		for i := range body {
			body[i] ^= c.mask[i&3]
		}
	}
	switch opcode {
	case 8:
		if !validWebSocketClose(body) {
			return hostedError("WebSocket close", "a valid close status and UTF-8 reason")
		}
		_ = c.writeControl(8, body)
		// A WebSocket close is never the relay's explicit both-ended frame.
		return io.ErrUnexpectedEOF
	case 9:
		return c.writeControl(10, body)
	default:
		// A pong echoing one of this side's pings measures the round trip;
		// unsolicited pongs are permitted by RFC 6455 §5.5.3.
		if !c.client && len(body) == 8 {
			if sent := time.Duration(binary.BigEndian.Uint64(body)); sent > 0 && sent <= time.Since(c.epoch) {
				c.rtt.Store(int64(time.Since(c.epoch) - sent))
			}
		}
		return nil
	}
}

func validWebSocketClose(body []byte) bool {
	if len(body) == 0 {
		return true
	}
	if len(body) == 1 || !utf8.Valid(body[2:]) {
		return false
	}
	// RFC 6455 §7.4 and the IANA WebSocket Close Code registry: exclude
	// reserved/non-wire statuses; permit registered and application ranges.
	code := binary.BigEndian.Uint16(body[:2])
	return (code >= 1000 && code <= 1003) || (code >= 1007 && code <= 1014) || (code >= 3000 && code <= 4999)
}

func (c *websocketConn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.deadlineMu.Lock()
	deadline := minDeadline(c.writeDeadline, time.Now().Add(c.timeout))
	c.deadlineMu.Unlock()
	if err := c.Conn.SetWriteDeadline(deadline); err != nil {
		return 0, err
	}
	if err := c.writeFrame(2, p); err != nil {
		c.shutdown()
		return 0, err
	}
	return len(p), nil
}

func (c *websocketConn) writeControl(opcode byte, p []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	// Data-writer deadlines may have expired while the room waited. Each
	// control gets its own bounded write, without extending a data write.
	if err := c.Conn.SetWriteDeadline(time.Now().Add(c.timeout)); err != nil {
		return err
	}
	return c.writeFrame(opcode, p)
}

// errWebSocketCloseSent refuses frames after this side's close frame. It is
// net.ErrClosed to callers that only ask whether the stream is closed.
var errWebSocketCloseSent = fmt.Errorf("WebSocket close sent: %w", net.ErrClosed)

func (c *websocketConn) writeFrame(opcode byte, p []byte) error {
	select {
	case <-c.done:
		return net.ErrClosed
	default:
	}
	if c.closeSent {
		return errWebSocketCloseSent
	}
	if opcode == 8 {
		c.closeSent = true
	}
	var header [14]byte
	header[0] = 0x80 | opcode
	n := 2
	switch {
	case len(p) < 126:
		header[1] = byte(len(p))
	case len(p) <= 65535:
		header[1] = 126
		binary.BigEndian.PutUint16(header[2:4], uint16(len(p)))
		n = 4
	default:
		header[1] = 127
		binary.BigEndian.PutUint64(header[2:10], uint64(len(p)))
		n = 10
	}
	var mask [4]byte
	if c.client {
		if _, err := rand.Read(mask[:]); err != nil {
			return err
		}
		header[1] |= 0x80
		copy(header[n:], mask[:])
		n += 4
	}
	if err := websocketWriteAll(c.Conn, header[:n]); err != nil {
		return err
	}
	if !c.client {
		return websocketWriteAll(c.Conn, p)
	}
	// Copy bounded chunks so client masking never mutates a caller's buffer.
	scratch := make([]byte, min(len(p), 32<<10))
	for len(p) != 0 {
		n := min(len(p), len(scratch))
		for i := range n {
			scratch[i] = p[i] ^ mask[i&3]
		}
		if err := websocketWriteAll(c.Conn, scratch[:n]); err != nil {
			return err
		}
		p = p[n:] // Every nonfinal chunk is divisible by the four-byte mask period.
	}
	return nil
}

func websocketWriteAll(w io.Writer, p []byte) error {
	for len(p) != 0 {
		n, err := w.Write(p)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		p = p[n:]
	}
	return nil
}
