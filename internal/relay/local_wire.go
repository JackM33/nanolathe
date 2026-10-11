package relay

import (
	"fmt"
	"io"

	"github.com/nanolathe-gg/nanolathe/internal/netproto"
)

// This framing is private to the development-only transport. All fields use
// netproto primitives; it is not a revision of the gameplay command schema
// (DESIGN_MULTIPLAYER §16.4.2).
const (
	localHelloMessage uint8 = iota + 1
	localSubmitMessage
	localAckMessage
	localGrantMessage
	localRefusedMessage
	localFailedMessage
	localDoneMessage

	localMaxCommands     = 64
	localMaxPendingBytes = 8 << 20
	localMaxHelloBytes   = 1024
	localMaxClientFrame  = netproto.MaxCommandBytes + 32
	localMaxGrantFrame   = localMaxPendingBytes + 2048
	localMaxErrorBytes   = 1024
)

func localError(path, expected string) error {
	return fmt.Errorf("nanolathe: relay stream failed: logical path %s, providers searched [relay transport], expected %s", path, expected)
}

func localIOError(path string, err error) error {
	// Only an explicit both-ended message is normal EOF. A socket departure
	// aborts the prototype rather than looking like a completed battle.
	if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return fmt.Errorf("%w: %w", localError(path, "a complete transport message"), err)
}

// Read the bounded envelope length before allocating. The netproto reader
// rejects overflow and non-shortest spellings of the u32 prefix.
func readLocalFrame(r io.Reader, limit int) ([]byte, error) {
	var prefix [5]byte
	for i := range prefix {
		if _, err := io.ReadFull(r, prefix[i:i+1]); err != nil {
			return nil, localIOError("envelope length", err)
		}
		if prefix[i]&0x80 != 0 && i < len(prefix)-1 {
			continue
		}
		p := netproto.NewReader(prefix[:i+1], localError)
		n := p.U32()
		if err := p.End(); err != nil {
			return nil, err
		}
		if n == 0 || uint64(n) > uint64(limit) {
			return nil, localError("envelope length", fmt.Sprintf("1..%d bytes", limit))
		}
		body := make([]byte, int(n))
		if _, err := io.ReadFull(r, body); err != nil {
			return nil, localIOError("envelope body", err)
		}
		return body, nil
	}
	return nil, localError("envelope length", "a shortest u32 length")
}

func writeLocalFrame(w io.Writer, body []byte) error {
	var frame netproto.Writer
	frame.U32(uint32(len(body)))
	frame.Raw(body)
	b := frame.Bytes()
	for len(b) != 0 {
		n, err := w.Write(b)
		if err != nil {
			return localIOError("write", err)
		}
		if n == 0 {
			return localIOError("write", io.ErrShortWrite)
		}
		b = b[n:]
	}
	return nil
}

// encodeLocalHello writes a seat's hello naming command schema schema: the
// build's own for the loopback relay and a version-8 hosted hello, schema 1
// for a version-7 one.
func encodeLocalHello(h LocalHello, schema uint16) ([]byte, error) {
	// Validate through the same bounded primitive reader before any network IO.
	// The small preliminary limits also bound writer allocation on local input.
	if len(h.Identity.Rules.Name) > netproto.MaxKeyBytes || len(h.Identity.Mod.ID) > 255 || len(h.Identity.Mod.Version) > 255 {
		return nil, localError("hello identity", "rule/mod names of at most 255 bytes")
	}
	var w netproto.Writer
	w.U8(localHelloMessage)
	w.U8(h.Seat)
	w.U16(h.Identity.Protocol)
	w.Digest(h.Identity.Build)
	w.Digest(h.Identity.Content)
	w.Digest(h.Identity.Map)
	w.Key(h.Identity.Rules.Name)
	w.U8(h.Identity.Rules.Base)
	w.Digest(h.Identity.Rules.Community)
	w.Text(h.Identity.Mod.ID)
	w.Text(h.Identity.Mod.Version)
	w.Digest(h.Identity.Mod.Archive)
	w.Digest(h.Identity.Configuration)
	w.Digest(h.InitialChecksum)
	_, err := decodeLocalHello(w.Bytes(), schema)
	return w.Bytes(), err
}

func decodeLocalHello(body []byte, schema uint16) (LocalHello, error) {
	r := netproto.NewReader(body, localError)
	if r.U8() != localHelloMessage {
		r.Abort(localError("hello", "hello as the first message"))
	}
	h := LocalHello{Seat: r.U8()}
	h.Identity.Protocol = r.U16()
	h.Identity.Build = r.Digest()
	h.Identity.Content = r.Digest()
	h.Identity.Map = r.Digest()
	h.Identity.Rules.Name = r.Key()
	h.Identity.Rules.Base = r.U8()
	h.Identity.Rules.Community = r.Digest()
	h.Identity.Mod.ID = r.Text(255)
	h.Identity.Mod.Version = r.Text(255)
	h.Identity.Mod.Archive = r.Digest()
	h.Identity.Configuration = r.Digest()
	h.InitialChecksum = r.Digest()
	if h.Seat >= HostedMaxSeats && h.Seat != hostedAnySeat {
		r.Abort(localError("hello seat", "a seat below 10, or any seat"))
	}
	if h.Identity.Protocol != schema {
		r.Abort(localError("hello protocol", fmt.Sprintf("command schema version %d", schema)))
	}
	return h, r.End()
}

func encodeLocalGrant(g LocalGrant) []byte {
	var w netproto.Writer
	w.U8(localGrantMessage)
	w.U32(g.Tick)
	w.U64(g.Position)
	w.U32(uint32(len(g.Commands)))
	for _, c := range g.Commands {
		w.U8(c.Seat)
		w.U64(c.Sequence)
		w.U64(c.Position)
		w.U32(uint32(len(c.Payload)))
		w.Raw(c.Payload)
	}
	return w.Bytes()
}

func decodeLocalGrant(r *netproto.Reader) (LocalGrant, error) {
	g := LocalGrant{Tick: r.U32(), Position: r.U64()}
	n := r.Count(localMaxCommands, 4)
	if n > 0 {
		g.Commands = make([]LocalCommand, n)
	}
	bytes := 0
	for i := range g.Commands {
		c := &g.Commands[i]
		c.Seat, c.Sequence, c.Position = r.U8(), r.U64(), r.U64()
		n := r.Count(netproto.MaxCommandBytes, 1)
		bytes += n
		if c.Seat >= HostedMaxSeats || c.Sequence == 0 || c.Position == 0 || bytes > localMaxPendingBytes {
			r.Abort(localError("grant command", "a valid seat/sequence/position within the pending byte limit"))
		}
		c.Payload = r.Raw(n)
	}
	return g, r.End()
}

// The build identity is advisory and never compared: a rehearsal digest, not a
// self-reported build, shows that two seats simulate alike (DESIGN_MULTIPLAYER
// §16.7).
func localIdentityDifference(a, b LocalHello) string {
	switch {
	case a.Identity.Protocol != b.Identity.Protocol:
		return "protocol"
	case a.Identity.Content != b.Identity.Content:
		return "content"
	case a.Identity.Map != b.Identity.Map:
		return "map"
	case a.Identity.Rules != b.Identity.Rules:
		return "rules"
	case a.Identity.Mod != b.Identity.Mod:
		return "mod"
	case a.Identity.Configuration != b.Identity.Configuration:
		return "configuration"
	case a.InitialChecksum != b.InitialChecksum:
		return "initial checksum"
	default:
		return ""
	}
}
