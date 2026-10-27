package relay

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/netproto"
)

// Version 9 reconnection (DESIGN_MULTIPLAYER §16.9). At Start the relay gives
// each version-9 seat a random credential. A seat whose connection drops is
// held out of play rather than ending the room: the room seals no tick more
// than 30 past its last acknowledgement, so the others wait, and it may come
// back within the rejoin grace with its credential. A returning client that
// still holds its battle resumes from the last grant it received; one that
// restarted describes the room again, composes the battle from tick zero and
// resumes from there, replaying the room's kept grants at full speed. None of
// this reaches a tick: grants, commands and their order are exactly those the
// other seats ran.
const (
	// hostedRejoinGrace bounds how long a dropped seat may stay away before
	// the room ends without a result. It is host policy, not simulation time.
	hostedRejoinGrace = 3 * time.Minute
	// hostedClientRejoinWindow bounds a client's own attempts: a little
	// longer than the relay's grace, so the relay's answer decides.
	hostedClientRejoinWindow = hostedRejoinGrace + 30*time.Second
	hostedResumeFirstWait    = 500 * time.Millisecond
	hostedResumeMostWait     = 5 * time.Second
	// A room keeps every grant it sealed so a restarted seat can replay the
	// match. Past this many bytes it keeps the newer half, never fewer than
	// hostedKeptHistoryTicks, so a dropped seat that kept its battle can still
	// resume; a restart then can no longer replay from tick zero.
	hostedMaxHistoryBytes  = 32 << 20
	hostedKeptHistoryTicks = 2 * 60 * 30
	// hostedBacklogWriteTimeout bounds one kept grant's write to a returning
	// seat, which reads it only as fast as it can run the tick.
	hostedBacklogWriteTimeout = 30 * time.Second
	// A dropped client keeps the orders and acknowledgements it could not
	// send, to resend after resuming (§11.2).
	hostedMaxUnconfirmed = 64
	hostedAckRing        = 64
	hostedCredentialSize = 32
)

// HostedRejoinTicket names a seat of a started match and proves it is that
// seat's player: a client that restarted rejoins with it
// (DescribeHostedRejoin, ResumeHosted). The credential is a secret.
type HostedRejoinTicket struct {
	Code       string
	Seat       uint8
	Credential [hostedCredentialSize]byte
}

// HostedRejoinDescription is what a restarted seat composes its battle from:
// the room's base configuration and the lobby as it stood at Start, with
// State.Started set and State.Slot the seat's slot.
type HostedRejoinDescription struct {
	Config []byte
	State  HostedLobbyState
}

// HostedConnection is a version-9 battle client's connection as the player
// sees it. It never reaches the simulation.
type HostedConnection struct {
	// Resumable reports that the relay gave this seat a credential, so a
	// lost connection is retried instead of ending the battle.
	Resumable bool
	// Reconnecting reports a lost connection being retried since Since.
	Reconnecting bool
	Since        time.Time
	// Resumes counts completed resumes; Sealed is the newest tick the relay
	// had sealed at the latest one, which a battle that fell behind runs
	// through at full speed.
	Resumes int
	Sealed  uint32
}

// hostedRefusal is the relay's answer refusing a resume or rejoin: retrying
// cannot change it.
type hostedRefusal struct{ message string }

func (r hostedRefusal) Error() string { return r.message }

// hostedOlderRelay reports a relay that refused this client's protocol
// version, which an older relay does by naming the version the client sent.
func hostedOlderRelay(err error) bool {
	return err != nil && strings.Contains(err.Error(), "this client sent version")
}

func newHostedCredential() ([hostedCredentialSize]byte, error) {
	var c [hostedCredentialSize]byte
	if _, err := rand.Read(c[:]); err != nil {
		return c, localIOError("seat credential", err)
	}
	return c, nil
}

// ---------------------------------------------------------------------------
// Messages.

func encodeHostedResume(ticket HostedRejoinTicket, from uint32) ([]byte, error) {
	if !validHostedCode(ticket.Code) || ticket.Seat >= HostedMaxSeats {
		return nil, hostedError("rejoin ticket", "a six-character invitation and a seat below 10")
	}
	var w netproto.Writer
	w.U8(hostedResumeMessage)
	w.U16(hostedResumeVersion)
	w.Text(ticket.Code)
	w.U8(ticket.Seat)
	w.Digest(ticket.Credential)
	w.U32(from)
	return w.Bytes(), nil
}

type hostedResumeRequest struct {
	ticket HostedRejoinTicket
	from   uint32
}

func decodeHostedResume(body []byte) (hostedResumeRequest, error) {
	r := netproto.NewReader(body, hostedError)
	if r.U8() != hostedResumeMessage {
		r.Abort(hostedError("handshake", "a resume"))
	}
	if v := r.U16(); v != hostedResumeVersion {
		r.Abort(hostedVersionError(v))
	}
	var q hostedResumeRequest
	q.ticket.Code = r.Text(hostedCodeLength)
	q.ticket.Seat = r.U8()
	q.ticket.Credential = r.Digest()
	q.from = r.U32()
	if r.Err() == nil && (!validHostedCode(q.ticket.Code) || q.ticket.Seat >= HostedMaxSeats) {
		r.Abort(hostedError("rejoin ticket", "a six-character invitation and a seat below 10"))
	}
	return q, r.End()
}

// hostedResumed is the relay's acceptance: the seat's slot, the last tick the
// relay holds its acknowledgement for, the last client sequence it accepted
// and the newest tick it has sealed.
type hostedResumed struct {
	slot     uint8
	acked    uint32
	accepted uint64
	sealed   uint32
}

func encodeHostedResumed(m hostedResumed) []byte {
	var w netproto.Writer
	w.U8(hostedResumedMessage)
	w.U8(m.slot)
	w.U32(m.acked)
	w.U64(m.accepted)
	w.U32(m.sealed)
	return w.Bytes()
}

func decodeHostedResumed(body []byte) (hostedResumed, error) {
	r := netproto.NewReader(body, hostedError)
	switch r.U8() {
	case hostedResumedMessage:
	case localRefusedMessage, localFailedMessage:
		message := r.Text(localMaxErrorBytes)
		if err := r.End(); err != nil {
			return hostedResumed{}, err
		}
		return hostedResumed{}, hostedRefusal{message}
	default:
		r.Abort(hostedError("resume", "a resumed match or a refusal"))
	}
	m := hostedResumed{slot: r.U8(), acked: r.U32(), accepted: r.U64(), sealed: r.U32()}
	if r.Err() == nil && (m.slot >= HostedMaxSeats || m.acked > m.sealed) {
		r.Abort(hostedError("resume", "a slot below 10 and an acknowledgement no later than the sealed tick"))
	}
	return m, r.End()
}

func encodeHostedRejoinDescribe(ticket HostedRejoinTicket) ([]byte, error) {
	if !validHostedCode(ticket.Code) || ticket.Seat >= HostedMaxSeats {
		return nil, hostedError("rejoin ticket", "a six-character invitation and a seat below 10")
	}
	var w netproto.Writer
	w.U8(hostedRejoinDescribeMessage)
	w.U16(hostedResumeVersion)
	w.Text(ticket.Code)
	w.U8(ticket.Seat)
	w.Digest(ticket.Credential)
	return w.Bytes(), nil
}

func decodeHostedRejoinDescribe(body []byte) (HostedRejoinTicket, error) {
	r := netproto.NewReader(body, hostedError)
	r.U8()
	if v := r.U16(); v != hostedResumeVersion {
		r.Abort(hostedVersionError(v))
	}
	t := HostedRejoinTicket{Code: r.Text(hostedCodeLength), Seat: r.U8(), Credential: r.Digest()}
	if r.Err() == nil && (!validHostedCode(t.Code) || t.Seat >= HostedMaxSeats) {
		r.Abort(hostedError("rejoin ticket", "a six-character invitation and a seat below 10"))
	}
	return t, r.End()
}

func encodeHostedRejoinDescription(slot uint8, config []byte, state HostedLobbyState) []byte {
	lobby := encodeHostedLobby(state, hostedResumeVersion)
	var w netproto.Writer
	w.U8(hostedRejoinDescriptionMessage)
	w.U8(slot)
	w.U32(uint32(len(config)))
	w.Raw(config)
	w.U32(uint32(len(lobby)))
	w.Raw(lobby)
	return w.Bytes()
}

func decodeHostedRejoinDescription(body []byte) (HostedRejoinDescription, error) {
	r := netproto.NewReader(body, hostedError)
	switch r.U8() {
	case hostedRejoinDescriptionMessage:
	case localRefusedMessage, localFailedMessage:
		message := r.Text(localMaxErrorBytes)
		if err := r.End(); err != nil {
			return HostedRejoinDescription{}, err
		}
		return HostedRejoinDescription{}, hostedRefusal{message}
	default:
		r.Abort(hostedError("rejoin description", "a description or a refusal"))
	}
	slot := r.U8()
	config := r.Raw(r.Count(hostedMaxConfigBytes, 1))
	lobby := r.Raw(r.Count(hostedMaxConfigBytes, 1))
	if err := r.End(); err != nil {
		return HostedRejoinDescription{}, err
	}
	state, err := decodeHostedLobby(lobby, hostedResumeVersion)
	if err != nil {
		return HostedRejoinDescription{}, err
	}
	if slot >= HostedMaxSeats {
		return HostedRejoinDescription{}, hostedError("rejoin description", "a slot below 10")
	}
	state.Started, state.Slot = true, slot
	return HostedRejoinDescription{Config: config, State: state}, nil
}

// encodeHostedStarted is Started in the seat's version: version 9 adds the
// seat's credential.
func encodeHostedStarted(slot uint8, version uint16, credential [hostedCredentialSize]byte) []byte {
	body := []byte{hostedStartedMessage, slot}
	if version >= hostedResumeVersion {
		body = append(body, credential[:]...)
	}
	return body
}

// decodeHostedStarted reads Started in the client's version.
func decodeHostedStarted(body []byte, version uint16) (uint8, [hostedCredentialSize]byte, error) {
	r := netproto.NewReader(body, hostedError)
	r.U8()
	slot := r.U8()
	var credential [hostedCredentialSize]byte
	if version >= hostedResumeVersion {
		credential = r.Digest()
	}
	if r.Err() == nil && slot >= HostedMaxSeats {
		r.Abort(hostedError("Started slot", "a slot below 10"))
	}
	return slot, credential, r.End()
}

// ---------------------------------------------------------------------------
// The relay's kept grants.

// grantHistory is a room's sealed grant bodies in tick order. The room
// goroutine appends; a returning seat's writer reads.
type grantHistory struct {
	mu     sync.Mutex
	first  uint32 // the tick of bodies[0]
	bodies [][]byte
	bytes  int
}

// historyEntryBytes charges each kept grant its slice header too.
const historyEntryBytes = 24

func (h *grantHistory) append(tick uint32, body []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.bodies) == 0 {
		h.first = tick
	}
	h.bodies = append(h.bodies, body)
	h.bytes += len(body) + historyEntryBytes
	if h.bytes <= hostedMaxHistoryBytes {
		return
	}
	n := 0
	for n < len(h.bodies)-hostedKeptHistoryTicks && h.bytes > hostedMaxHistoryBytes/2 {
		h.bytes -= len(h.bodies[n]) + historyEntryBytes
		n++
	}
	// Copy so the dropped bodies can be collected.
	h.bodies = append([][]byte(nil), h.bodies[n:]...)
	h.first += uint32(n)
}

// at returns the kept grant body of tick.
func (h *grantHistory) at(tick uint32) ([]byte, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.bodies) == 0 || tick < h.first || tick-h.first >= uint32(len(h.bodies)) {
		return nil, false
	}
	return h.bodies[tick-h.first], true
}

// keeps reports whether every tick after from up to sealed is kept.
func (h *grantHistory) keeps(from, sealed uint32) bool {
	if from >= sealed {
		return true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.bodies) != 0 && from+1 >= h.first && sealed-h.first < uint32(len(h.bodies))
}

// ---------------------------------------------------------------------------
// The relay's side of admission.

// rejoinable returns the started room whose seat the ticket proves, under
// s.mu. The room goroutine still decides whether that seat may come back.
func (s *HostedServer) rejoinable(ticket HostedRejoinTicket) (*hostedRoom, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	room := s.rooms[ticket.Code]
	if room == nil {
		return nil, hostedError("room", "a game that is still running")
	}
	select {
	case <-room.done:
		return nil, hostedError("room", "a game that is still running")
	default:
	}
	if !room.started || !room.issued[ticket.Seat] || subtle.ConstantTimeCompare(room.credentials[ticket.Seat][:], ticket.Credential[:]) != 1 {
		return nil, hostedError("rejoin ticket", "the credential this seat received when its game started")
	}
	return room, nil
}

// describeRejoin answers a restarted seat with what it composes its battle
// from. The connection then closes.
func (s *HostedServer) describeRejoin(body []byte) []byte {
	ticket, err := decodeHostedRejoinDescribe(body)
	if err != nil {
		return localFailureBody(localRefusedMessage, err)
	}
	room, err := s.rejoinable(ticket)
	if err != nil {
		return localFailureBody(localRefusedMessage, err)
	}
	s.mu.Lock()
	slot, config, state := room.slots[ticket.Seat], room.config, room.startState
	s.mu.Unlock()
	return encodeHostedRejoinDescription(slot, config, state)
}

// serveResume admits a returning seat's connection and hands it to its room,
// which accepts or refuses it, then reads its frames as serve does.
func (s *HostedServer) serveResume(conn net.Conn, body []byte, handshakeDeadline time.Time) {
	request, err := decodeHostedResume(body)
	var room *hostedRoom
	if err == nil {
		room, err = s.rejoinable(request.ticket)
	}
	if err != nil {
		if writeLocalFrame(conn, localFailureBody(localRefusedMessage, err)) == nil {
			s.finish(conn)
		}
		return
	}
	peer := newHostedPeer(conn, LocalHello{Seat: request.ticket.Seat}, s.timeouts.write)
	peer.version, peer.helloDeadline, peer.linger = hostedResumeVersion, handshakeDeadline, s.timeouts.linger
	peer.room, peer.seat = room, request.ticket.Seat
	_ = conn.SetDeadline(time.Time{})
	go peer.write()
	if !room.send(hostedEvent{peer: peer, resume: &request}) {
		_ = peer.conn.Close()
		peer.stop()
		<-peer.written
		return
	}
	s.readPeer(conn, room, peer)
}

// writeBacklog streams the kept grants from through to, which a returning
// seat reads as fast as it runs them. It stops early when the peer stops.
func (p *hostedPeer) writeBacklog(from, to uint32) error {
	for tick := from; tick <= to; tick++ {
		select {
		case <-p.stopped:
			return nil
		default:
		}
		body, ok := p.room.history.at(tick)
		if !ok {
			return hostedError("rejoin history", "every grant since the resumed tick")
		}
		if err := p.conn.SetWriteDeadline(time.Now().Add(hostedBacklogWriteTimeout)); err != nil {
			return err
		}
		if err := writeLocalFrame(p.conn, body); err != nil {
			return err
		}
		if tick == ^uint32(0) {
			break
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// The client's side.

// hostedResume is a version-9 battle client's reconnection state. Its
// writeMu fields are guarded by the client's writeMu; the rest by mu.
type hostedResume struct {
	address string
	options HostedDialOptions
	ticket  HostedRejoinTicket
	slot    uint8

	// writeMu: whether frames go out now, and what a resume resends.
	connected   bool
	acks        []hostedSentFrame // the latest acknowledgements, in tick order
	unconfirmed []hostedSentFrame // orders the relay has not sealed, by sequence

	mu           sync.Mutex
	reconnecting bool
	since        time.Time
	resumes      int
	sealed       uint32
}

// hostedSentFrame is an acknowledgement (key: its tick) or an order (key: its
// client sequence) with its encoded frame.
type hostedSentFrame struct {
	key  uint64
	body []byte
}

func (r *hostedResume) state() HostedConnection {
	r.mu.Lock()
	defer r.mu.Unlock()
	return HostedConnection{Resumable: true, Reconnecting: r.reconnecting, Since: r.since, Resumes: r.resumes, Sealed: r.sealed}
}

// RejoinTicket returns the credential the relay gave this seat at Start, and
// false on a relay older than version 9 or before Started.
func (c *LocalClient) RejoinTicket() (HostedRejoinTicket, bool) {
	if c == nil {
		return HostedRejoinTicket{}, false
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.resume == nil {
		return HostedRejoinTicket{}, false
	}
	return c.resume.ticket, true
}

// Connection reports whether this client can resume and whether it is
// resuming now. It never waits and may be called from any goroutine.
func (c *LocalClient) Connection() HostedConnection {
	if c == nil {
		return HostedConnection{}
	}
	c.writeMu.Lock()
	r := c.resume
	c.writeMu.Unlock()
	if r == nil {
		return HostedConnection{}
	}
	return r.state()
}

// adoptStarted keeps the slot and, from a version-9 relay, the credential
// Started carried, which makes the client resumable.
func (c *LocalClient) adoptStarted(slot uint8, credential [hostedCredentialSize]byte) {
	c.started.Store(true)
	if c.version < hostedResumeVersion || c.dial == nil {
		return
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.resume = &hostedResume{address: c.dial.address, options: c.dial.options, slot: slot, connected: true,
		ticket: HostedRejoinTicket{Code: c.dial.code, Seat: c.dial.seat, Credential: credential}}
}

// hostedDial is where a hosted client connected, for a resume.
type hostedDial struct {
	address string
	options HostedDialOptions
	code    string
	seat    uint8
}

// keepSent appends a frame to a bounded resend list.
func keepSent(list []hostedSentFrame, key uint64, body []byte, limit int) []hostedSentFrame {
	if len(list) == limit {
		copy(list, list[1:])
		list = list[:limit-1]
	}
	return append(list, hostedSentFrame{key: key, body: body})
}

// lostLocked marks the connection lost after a failed write, closing it so
// the grant reader notices and resumes. The caller holds writeMu.
func (c *LocalClient) lostLocked() {
	if c.resume != nil {
		c.resume.connected = false
	}
	_ = c.currentConn().Close()
}

// ownSlot is a resumable seat's slot.
func (c *LocalClient) ownSlot() (uint8, bool) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.resume == nil {
		return 0, false
	}
	return c.resume.slot, true
}

// confirmOwn drops the orders a grant has sealed for this seat.
func (c *LocalClient) confirmOwn(sequence uint64) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if r := c.resume; r != nil {
		r.unconfirmed = dropThrough(r.unconfirmed, sequence)
	}
}

func dropThrough(list []hostedSentFrame, key uint64) []hostedSentFrame {
	n := 0
	for n < len(list) && list[n].key <= key {
		n++
	}
	if n == 0 {
		return list
	}
	return append(list[:0], list[n:]...)
}

func (c *LocalClient) currentConn() net.Conn {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	return c.conn
}

// resumable reports whether a lost connection should be resumed.
func (c *LocalClient) resumable() *hostedResume {
	if c.closed.Load() || c.finished.Load() || !c.started.Load() {
		return nil
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.resume
}

// reconnect resumes the match on a new connection after cause ended the old
// one, retrying until the relay accepts, refuses or the window passes.
func (c *LocalClient) reconnect(r *hostedResume, cause error) error {
	c.writeMu.Lock()
	r.connected = false
	c.writeMu.Unlock()
	_ = c.currentConn().Close()
	r.mu.Lock()
	r.reconnecting, r.since = true, time.Now()
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.reconnecting = false
		r.mu.Unlock()
	}()
	end := time.Now().Add(hostedClientRejoinWindow)
	wait := hostedResumeFirstWait
	for {
		if c.closed.Load() {
			return cause
		}
		conn, resumed, err := dialHostedResume(context.Background(), r.address, r.options, r.ticket, c.readTick)
		if err == nil {
			if err = c.adoptResumed(r, conn, resumed); err == nil {
				return nil
			}
			_ = conn.Close()
		}
		var refused hostedRefusal
		if errors.As(err, &refused) {
			return err
		}
		if c.closed.Load() {
			return cause
		}
		if time.Now().Add(wait).After(end) {
			return errors.Join(hostedError("reconnection", "the relay back within the rejoin window"), cause)
		}
		select {
		case <-time.After(wait):
		case <-c.closing:
			return cause
		}
		wait = min(2*wait, hostedResumeMostWait)
	}
}

// adoptResumed makes conn the client's connection and resends what the
// relay has not seen: acknowledgements after its last, orders after the last
// it accepted, and the opening marker.
func (c *LocalClient) adoptResumed(r *hostedResume, conn net.Conn, m hostedResumed) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closed.Load() {
		return hostedError("client", "an open client")
	}
	if m.slot != r.slot {
		return hostedRefusal{hostedError("resume", "the seat's own slot").Error()}
	}
	c.connMu.Lock()
	c.conn = hostedClientConn{Conn: conn}
	c.connMu.Unlock()
	next := uint64(m.acked) + 1
	for _, a := range r.acks {
		if a.key < next {
			continue
		}
		if a.key != next {
			return hostedRefusal{hostedError("resume", "the acknowledgements the relay is missing").Error()}
		}
		if err := c.writeFrame(a.body); err != nil {
			return err
		}
		next++
	}
	r.unconfirmed = dropThrough(r.unconfirmed, m.accepted)
	for _, command := range r.unconfirmed {
		if err := c.writeFrame(command.body); err != nil {
			return err
		}
	}
	// A restarted client numbers its next order after the relay's last.
	c.sequence = max(c.sequence, m.accepted)
	if c.openingReady {
		if err := c.writeFrame([]byte{hostedOpeningReadyMessage}); err != nil {
			return err
		}
	}
	r.connected = true
	r.mu.Lock()
	r.resumes++
	r.sealed = m.sealed
	r.mu.Unlock()
	return nil
}

// dialHostedResume connects and asks to resume the ticket's seat from tick
// from, returning the connection positioned at its first resent grant.
func dialHostedResume(ctx context.Context, address string, options HostedDialOptions, ticket HostedRejoinTicket, from uint32) (net.Conn, hostedResumed, error) {
	body, err := encodeHostedResume(ticket, from)
	if err != nil {
		return nil, hostedResumed{}, hostedRefusal{err.Error()}
	}
	ctx, cancel := context.WithTimeout(ctx, hostedDefaultTimeouts.handshake)
	defer cancel()
	conn, err := dialHostedTransport(ctx, address, options)
	if err != nil {
		return nil, hostedResumed{}, err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	var m hostedResumed
	if err = writeLocalFrame(conn, body); err == nil {
		var response []byte
		if response, err = readLocalFrame(conn, localMaxErrorBytes+64); err == nil {
			m, err = decodeHostedResumed(response)
		}
	}
	stopped := stop()
	if expired := handshakeExpired(ctx); err == nil && (!stopped || expired != nil) {
		err = localIOError("resume", context.DeadlineExceeded)
	}
	if err != nil {
		_ = conn.Close()
		return nil, hostedResumed{}, err
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, m, nil
}

// DescribeHostedRejoin asks a running match for what a restarted seat
// composes its battle from. address is host:port or a wss://host/relay URL.
func DescribeHostedRejoin(ctx context.Context, address string, ticket HostedRejoinTicket, options HostedDialOptions) (HostedRejoinDescription, error) {
	body, err := encodeHostedRejoinDescribe(ticket)
	if err != nil {
		return HostedRejoinDescription{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, hostedDefaultTimeouts.handshake)
	defer cancel()
	conn, err := dialHostedTransport(ctx, address, options)
	if err != nil {
		return HostedRejoinDescription{}, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if err := writeLocalFrame(conn, body); err != nil {
		return HostedRejoinDescription{}, err
	}
	response, err := readLocalFrame(conn, 2*hostedMaxConfigBytes+64)
	if err != nil {
		if expired := handshakeExpired(ctx); expired != nil {
			return HostedRejoinDescription{}, localIOError("rejoin description", expired)
		}
		return HostedRejoinDescription{}, err
	}
	return decodeHostedRejoinDescription(response)
}

// ResumeHosted rejoins a started match from tick zero for a client that
// restarted: the battle composed from DescribeHostedRejoin runs the kept
// grants at full speed (Connection().Sealed) and then plays on. Its first
// acknowledgements, of ticks the relay already holds, only show progress.
func ResumeHosted(ctx context.Context, address string, ticket HostedRejoinTicket, options HostedDialOptions) (*LocalClient, error) {
	conn, m, err := dialHostedResume(ctx, address, options, ticket, 0)
	if err != nil {
		return nil, err
	}
	c := newHostedClient(conn)
	c.version = hostedResumeVersion
	c.dial = &hostedDial{address: address, options: options, code: ticket.Code, seat: ticket.Seat}
	c.started.Store(true)
	c.sequence = m.accepted
	c.resume = &hostedResume{address: address, options: options, ticket: ticket, slot: m.slot, connected: true, resumes: 1, sealed: m.sealed}
	return c, nil
}
