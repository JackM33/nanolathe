package relay

import (
	"io"
	"math"
	"slices"
	"sync/atomic"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/netproto"
)

// HostedMatchProgress is the relay's latest report on a running hosted match:
// how far every seat has acknowledged and how far the relay has compared
// their reports (DESIGN_MULTIPLAYER §16.5.2). It is host diagnostics only;
// nothing in it reaches the simulation.
type HostedMatchProgress struct {
	// Agreed is the last tick whose ended bits, and at every 30th tick unit
	// checksums, the relay has compared across every playing seat.
	Agreed uint32
	// Sealed is the newest tick the relay had sealed when it made the
	// report. A seat's lag is Sealed less its Acked, and the relay seals no
	// further while the slowest playing seat lags by its 30-tick lead bound.
	Sealed uint32
	// Seats holds one entry per slot, in slot order.
	Seats []HostedSeatProgress
}

// HostedSeatProgress is one slot in a HostedMatchProgress.
type HostedSeatProgress struct {
	Playing bool // still in the match and compared; false once it left
	// Connected is false while a playing seat's connection is lost and the
	// relay holds the match for it to resume (§16.9). A relay older than
	// version 9 reports every playing seat connected.
	Connected bool
	Final     bool          // its result is final, so it may leave
	Acked     uint32        // the last tick it acknowledged
	RTT       time.Duration // the relay's latest ping round trip; 0 when unmeasured
}

// LocalTraffic counts a client's relay messages since it connected, and their
// bytes including each message's length prefix.
type LocalTraffic struct {
	MessagesIn, MessagesOut uint64
	BytesIn, BytesOut       uint64
}

// Progress returns the relay's latest report on the running match, and false
// before the first one or on a relay that sends none. It never waits for the
// reader and may be called from any goroutine.
func (c *LocalClient) Progress() (HostedMatchProgress, bool) {
	c.progressMu.Lock()
	defer c.progressMu.Unlock()
	if c.progress == nil {
		return HostedMatchProgress{}, false
	}
	p := *c.progress
	p.Seats = slices.Clone(p.Seats)
	return p, true
}

// Traffic returns the client's relay message counts so far, the hello and
// lobby included. It may be called from any goroutine.
func (c *LocalClient) Traffic() LocalTraffic {
	return LocalTraffic{
		MessagesIn: c.traffic.messagesIn.Load(), MessagesOut: c.traffic.messagesOut.Load(),
		BytesIn: c.traffic.bytesIn.Load(), BytesOut: c.traffic.bytesOut.Load(),
	}
}

// Progress report seat flags.
const (
	progressPlaying = 1 << iota
	progressFinal
	progressConnected // version 9
	progressFlagsMask = progressPlaying | progressFinal
)

// encodeHostedProgress is one report: the agreed and sealed ticks and the
// slot count, then per slot its flags, last acknowledged tick and round trip
// in microseconds. A version-9 report adds the connected flag.
func encodeHostedProgress(p *hostedProgress, sealed uint32, rtt func(slot int) time.Duration, connected bool) []byte {
	var w netproto.Writer
	w.U8(hostedProgressMessage)
	w.U32(p.compared)
	w.U32(sealed)
	w.U8(uint8(p.n))
	for slot := range p.n {
		var flags uint8
		if p.active[slot] {
			flags |= progressPlaying
		}
		if p.final[slot] {
			flags |= progressFinal
		}
		if connected && p.active[slot] && !p.away[slot] {
			flags |= progressConnected
		}
		w.U8(flags)
		w.U32(p.acked[slot])
		w.U32(uint32(min(max(rtt(slot).Microseconds(), 0), math.MaxUint32)))
	}
	return w.Bytes()
}

func decodeHostedProgress(body []byte, version uint16) (HostedMatchProgress, error) {
	mask := uint8(progressFlagsMask)
	if version >= hostedResumeVersion {
		mask |= progressConnected
	}
	r := netproto.NewReader(body, hostedError)
	if r.U8() != hostedProgressMessage {
		r.Abort(hostedError("progress report", "a progress report"))
	}
	p := HostedMatchProgress{Agreed: r.U32(), Sealed: r.U32()}
	if p.Agreed > p.Sealed {
		r.Abort(hostedError("progress report", "an agreed tick no later than the sealed tick"))
	}
	n := int(r.U8())
	if n < 1 || n > HostedMaxSeats {
		r.Abort(hostedError("progress report", "1 to 10 slots"))
	}
	p.Seats = make([]HostedSeatProgress, n)
	for i := range p.Seats {
		flags := r.U8()
		if flags&^mask != 0 {
			r.Abort(hostedError("progress report", "the playing, final and connected bits"))
		}
		connected := flags&progressPlaying != 0
		if version >= hostedResumeVersion {
			connected = flags&progressConnected != 0
		}
		p.Seats[i] = HostedSeatProgress{Playing: flags&progressPlaying != 0, Connected: connected, Final: flags&progressFinal != 0, Acked: r.U32(), RTT: time.Duration(r.U32()) * time.Microsecond}
		if p.Seats[i].Acked > p.Sealed {
			r.Abort(hostedError("progress report", "acknowledged ticks no later than the sealed tick"))
		}
	}
	if err := r.End(); err != nil {
		return HostedMatchProgress{}, err
	}
	return p, nil
}

// recordProgress keeps a report for Progress.
func (c *LocalClient) recordProgress(body []byte) error {
	p, err := decodeHostedProgress(body, c.version)
	if err != nil {
		return err
	}
	c.progressMu.Lock()
	c.progress = &p
	c.progressMu.Unlock()
	return nil
}

// trafficCounter counts whole envelopes as they are read and written.
type trafficCounter struct {
	messagesIn, messagesOut atomic.Uint64
	bytesIn, bytesOut       atomic.Uint64
}

func envelopeBytes(body []byte) uint64 {
	return uint64(len(body) + netproto.UvarintLen(uint64(len(body))))
}

func (t *trafficCounter) read(r io.Reader, limit int) ([]byte, error) {
	body, err := readLocalFrame(r, limit)
	if err == nil {
		t.messagesIn.Add(1)
		t.bytesIn.Add(envelopeBytes(body))
	}
	return body, err
}

func (t *trafficCounter) write(w io.Writer, body []byte) error {
	err := writeLocalFrame(w, body)
	if err == nil {
		t.messagesOut.Add(1)
		t.bytesOut.Add(envelopeBytes(body))
	}
	return err
}
