package relay

import (
	"net"
	"sync"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/netproto"
)

type hostedOutput struct {
	body []byte
	last bool
	// report marks the queue position of the peer's latest progress report,
	// whose body the writer takes when it reaches this entry.
	report bool
}

type hostedPeer struct {
	conn          net.Conn
	hello         LocalHello
	version       uint16         // the protocol its hello spoke, answered in kind
	name          string         // version 8: the display name its hello carried
	chatRate      hostedChatRate // owned by the room goroutine
	room          *hostedRoom
	seat          uint8 // assigned at admission
	slot          uint8 // assigned at Start
	timeout       time.Duration
	helloDeadline time.Time
	out           chan hostedOutput
	written       chan struct{}
	stopped       chan struct{}
	once          sync.Once
	mu            sync.Mutex
	bytes         int
	pending       []byte // mu: the latest unwritten progress report, or nil
}

func newHostedPeer(conn net.Conn, hello LocalHello, timeout time.Duration) *hostedPeer {
	return &hostedPeer{conn: conn, hello: hello, timeout: timeout, out: make(chan hostedOutput, hostedMaxQueuedFrames), written: make(chan struct{}), stopped: make(chan struct{})}
}

func (p *hostedPeer) stop() { p.once.Do(func() { close(p.stopped) }) }

// rtt is the last WebSocket ping round trip, or zero when unmeasured.
func (p *hostedPeer) rtt() time.Duration {
	if c, ok := p.conn.(*websocketConn); ok {
		return time.Duration(c.rtt.Load())
	}
	return 0
}

// queuedBytes is what the peer's writer still holds.
func (p *hostedPeer) queuedBytes() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.bytes
}

// Charge queued and in-flight frames together. Neither a blocked writer nor
// tiny refusal floods may consume unbounded storage (§16.5.1).
func (p *hostedPeer) enqueue(body []byte, last bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := frameCharge(body)
	if n > hostedMaxQueuedBytes-p.bytes {
		return hostedError("peer write queue", "at most 1 MiB plus the frame header allowance")
	}
	select {
	case <-p.written:
		return hostedError("peer write queue", "an open writer")
	default:
	}
	select {
	case p.out <- hostedOutput{body: body, last: last}:
		p.bytes += n
		return nil
	default:
		return hostedError("peer write queue", "at most 64 queued frames")
	}
}

// report replaces the peer's unwritten progress report. At most one report
// waits in the queue, holding its place behind the frames queued before it,
// so a slow reader receives the latest state rather than a backlog. A report
// that does not fit the queue's bounds is dropped: diagnostics never fail a
// room (§16.5.2).
func (p *hostedPeer) report(body []byte) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	n, old := frameCharge(body), frameCharge(p.pending)
	if n-old > hostedMaxQueuedBytes-p.bytes {
		return false
	}
	if p.pending == nil {
		select {
		case <-p.written:
			return false
		default:
		}
		select {
		case p.out <- hostedOutput{report: true}:
		default:
			return false
		}
	}
	p.bytes += n - old
	p.pending = body
	return true
}

// offer queues a frame the room can do without, a chat line. It is dropped,
// and never fails the room, once the peer already holds 16 queued frames or
// would hold more than 64 KiB, so it neither waits for a slow reader nor
// takes the queue space grants need (§12.2).
func (p *hostedPeer) offer(body []byte) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := frameCharge(body)
	if len(p.out) >= hostedOfferFrames || n > hostedOfferBytes-p.bytes {
		return false
	}
	select {
	case <-p.written:
		return false
	default:
	}
	select {
	case p.out <- hostedOutput{body: body}:
		p.bytes += n
		return true
	default:
		return false
	}
}

// frameCharge is what a queued body costs the peer's byte budget.
func frameCharge(body []byte) int {
	if body == nil {
		return 0
	}
	return len(body) + netproto.UvarintLen(uint64(len(body)))
}

func minDeadline(a, b time.Time) time.Time {
	if a.IsZero() || b.Before(a) {
		return b
	}
	return a
}

func (p *hostedPeer) write() {
	defer close(p.written)
	defer p.conn.Close()
	firstDeadline := p.helloDeadline
	for {
		select {
		case <-p.stopped:
			return
		case message := <-p.out:
			body := message.body
			if message.report {
				p.mu.Lock()
				body, p.pending = p.pending, nil
				p.mu.Unlock()
				if body == nil {
					continue
				}
			}
			err := p.conn.SetWriteDeadline(minDeadline(firstDeadline, time.Now().Add(p.timeout)))
			firstDeadline = time.Time{}
			if err == nil {
				err = writeLocalFrame(p.conn, body)
			}
			p.mu.Lock()
			p.bytes -= frameCharge(body)
			p.mu.Unlock()
			if err != nil {
				p.room.send(hostedEvent{peer: p, err: err})
				return
			}
			if message.last {
				return
			}
		}
	}
}
