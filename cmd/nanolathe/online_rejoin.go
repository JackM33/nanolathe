package main

// Rejoining an online battle after this game stopped (DESIGN_MULTIPLAYER
// §16.9). While a battle runs, the seat's rejoin ticket is kept beside the
// settings file; a battle that ends, or that the player leaves, removes it. A
// game that stopped without either — a crash, a killed app — finds the ticket
// on its next visit to MULTI, which offers Rejoin: it asks the relay for the
// room as it stood at Start, mounts the room's mod and content as a join
// does, composes the same battle from tick zero and resumes, replaying the
// match at full speed.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/relay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// onlineRejoinFile is the kept ticket, beside settings.json.
const onlineRejoinFile = "online-rejoin.json"

// onlineRejoinAge bounds how old a kept ticket may be and still be offered;
// the relay decides whether the match still holds the seat.
const onlineRejoinAge = 12 * time.Hour

// onlineRejoinTimeout bounds the description and the resume dial.
const onlineRejoinTimeout = 15 * time.Second

// onlineRejoinRecord is a kept ticket and where its relay is.
type onlineRejoinRecord struct {
	Server     string    `json:"server"`
	Code       string    `json:"code"`
	Seat       uint8     `json:"seat"`
	Credential string    `json:"credential"`
	Started    time.Time `json:"started"`
}

func (r onlineRejoinRecord) ticket() (relay.HostedRejoinTicket, bool) {
	raw, err := hex.DecodeString(r.Credential)
	t := relay.HostedRejoinTicket{Code: r.Code, Seat: r.Seat}
	if err != nil || len(raw) != len(t.Credential) {
		return t, false
	}
	copy(t.Credential[:], raw)
	return t, true
}

// onlineRejoinPath is the kept ticket's file, or "" when the settings
// directory is unknown.
var onlineRejoinPath = func() string {
	p, err := settings.Path()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(p), onlineRejoinFile)
}

// keepOnlineRejoin writes the battle's ticket; a battle without one (an
// older relay) clears any earlier.
func keepOnlineRejoin(server string, conn any) {
	hosted, ok := conn.(*relay.LocalClient)
	ticket, have := relay.HostedRejoinTicket{}, false
	if ok {
		ticket, have = hosted.RejoinTicket()
	}
	path := onlineRejoinPath()
	if path == "" {
		return
	}
	if !have {
		forgetOnlineRejoin()
		return
	}
	data, err := json.Marshal(onlineRejoinRecord{Server: server, Code: ticket.Code, Seat: ticket.Seat, Credential: hex.EncodeToString(ticket.Credential[:]), Started: time.Now().UTC()})
	if err == nil {
		err = os.MkdirAll(filepath.Dir(path), 0o755)
	}
	if err == nil {
		// The ticket proves the seat: readable by this user alone.
		tmp := path + ".part"
		if err = os.WriteFile(tmp, data, 0o600); err == nil {
			err = os.Rename(tmp, path)
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: online rejoin ticket: %v\n", err)
	}
}

// forgetOnlineRejoin removes the kept ticket.
func forgetOnlineRejoin() {
	if path := onlineRejoinPath(); path != "" {
		_ = os.Remove(path)
	}
}

// keptOnlineRejoin is the kept ticket when it is recent enough to offer.
func keptOnlineRejoin() (onlineRejoinRecord, bool) {
	path := onlineRejoinPath()
	if path == "" {
		return onlineRejoinRecord{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return onlineRejoinRecord{}, false
	}
	var r onlineRejoinRecord
	if json.Unmarshal(data, &r) != nil || r.Server == "" || time.Since(r.Started) > onlineRejoinAge {
		return onlineRejoinRecord{}, false
	}
	if _, ok := r.ticket(); !ok {
		return onlineRejoinRecord{}, false
	}
	return r, true
}

// onlineRejoin is a room being rejoined: its ticket and the room as it stood
// at Start.
type onlineRejoin struct {
	ticket relay.HostedRejoinTicket
	state  relay.HostedLobbyState
}

// onlineRejoined is a rejoin composed and resumed, ready to enter.
type onlineRejoined struct {
	prepared *onlinePrepared
	conn     *relay.LocalClient
	state    relay.HostedLobbyState
	address  string
	code     string
}

// startOnlineRejoin asks the kept ticket's relay for the room.
func (g *gameShell) startOnlineRejoin() {
	s := g.online
	if s == nil || s.job != nil || s.phase != onlineIdle {
		return
	}
	record, ok := keptOnlineRejoin()
	ticket, valid := record.ticket()
	if !ok || !valid {
		g.onlineIdleStatus("There is no game to rejoin.")
		return
	}
	if err := onlineContentRefusal(g.cs); err != nil {
		g.onlineFail(err)
		return
	}
	address, options, err := onlineServerAddress(record.Server)
	if err != nil {
		g.onlineFail(err)
		return
	}
	options.Name = s.name
	s.phase, s.status = onlineDescribing, "Rejoining game "+ticket.Code+"..."
	s.detail = "Server " + address
	s.run(func(ctx context.Context) onlineJobResult {
		ctx, cancel := context.WithTimeout(ctx, onlineRejoinTimeout)
		defer cancel()
		d, err := relay.DescribeHostedRejoin(ctx, address, ticket, options)
		if err != nil {
			return onlineJobResult{err: err, rejoinGone: true}
		}
		return onlineJobResult{described: &onlineRoom{address: address, options: options, code: ticket.Code, config: d.Config,
			rejoin: &onlineRejoin{ticket: ticket, state: d.State}}}
	})
	g.refreshOnlinePanel()
}

// adoptOnlineRejoin mounts the room's mod, as a join does, then composes the
// battle at the seat's slot and resumes it from tick zero.
func (g *gameShell) adoptOnlineRejoin(room onlineRoom, remounted bool) {
	s := g.online
	if s == nil || room.rejoin == nil {
		return
	}
	base, err := session.DecodeMatchConfig(room.config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: online rejoin: %v\n", err)
		g.onlineIdleStatus("This game's settings could not be read. It may be running a different release.")
		return
	}
	if want := base.Request().Mod; want != matchModOf(g.cs.mod) {
		if remounted {
			g.onlineIdleStatus("This game's mod " + onlineModName(want) + " could not be selected.")
			return
		}
		selector, refusal, fetch := g.onlineModPlan(want)
		if fetch {
			g.onlineIdleStatus("This game needs " + onlineModName(want) + ", which is no longer installed.")
			return
		}
		if refusal != "" {
			g.onlineIdleStatus(refusal)
			return
		}
		s.phase, s.status = onlineConnecting, "Loading "+onlineModName(want)+" for this game..."
		g.refreshOnlinePanel()
		pendingContentReload = &contentReloadRequest{
			selector: selector,
			mod:      settings.ModSelection{ID: want.ID, Version: want.Version},
			mutators: g.opts.Mutators,
			online:   &room,
		}
		return
	}
	request := base.Request()
	seats, slot, ok := onlineSeatsOf(room.rejoin.state, room.rejoin.ticket.Seat, onlineSettingsOf(request).survival)
	if !ok || slot != room.rejoin.state.Slot {
		g.onlineIdleStatus("This game's seats could not be matched to yours.")
		return
	}
	cs, opts, ticket, state := g.cs, g.opts, room.rejoin.ticket, room.rejoin.state
	s.phase, s.status = onlineConnecting, "Rejoining game "+room.code+": preparing the battle..."
	s.run(func(ctx context.Context) onlineJobResult {
		cat, err := cs.compileCatalog(nil)
		if err != nil {
			return onlineJobResult{err: err}
		}
		archives, err := baseArchives(cs)
		if err != nil {
			return onlineJobResult{err: err}
		}
		known := onlineKnownAddons(ctx, func(known []knownAddon) bool { return roomAddonsUnknown(cs, known, request.RoomContent) })
		plan := resolveRoomContent(cs, archives, installedMapPackages(), known, request.RoomContent)
		if len(plan.missing) > 0 {
			return onlineJobResult{refusal: roomMissingText(plan.missing)}
		}
		ready := composeOnlineReady(ctx, cs, cat, opts, base, onlineSettingsOf(request), seats, slot, onlineRoomKey{}, plan)
		if ready.err != nil {
			return onlineJobResult{err: ready.err}
		}
		dial, cancel := context.WithTimeout(ctx, onlineRejoinTimeout)
		defer cancel()
		conn, err := relay.ResumeHosted(dial, room.address, ticket, room.options)
		if err != nil {
			ready.prepared.close()
			return onlineJobResult{err: err, rejoinGone: true}
		}
		return onlineJobResult{rejoined: &onlineRejoined{prepared: ready.prepared, conn: conn, state: state, address: room.address, code: room.code}}
	})
	g.refreshOnlinePanel()
}

// enterOnlineRejoin enters the rejoined battle, which then runs the match it
// missed at full speed.
func (g *gameShell) enterOnlineRejoin(r *onlineRejoined) {
	humans := onlineHumanCount(r.prepared.config.Request())
	if err := g.enterOnlineBattle(r.prepared, r.conn, r.code, humans, r.address); err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: online rejoin: %v\n", err)
		r.prepared.close()
		g.onlineIdleStatus("The game could not start: " + noticeReason(err))
		return
	}
	g.battle.adoptOnlineChatSeats(r.state)
	g.battle.onlineNotice("Rejoined game " + r.code + ": catching up with the others...")
	g.closeOnlineScreen()
}

// onlineRejoinRefusal says why a kept ticket no longer rejoins, and forgets it
// when the match is gone.
func onlineRejoinRefusal(err error) string {
	text := err.Error()
	switch {
	case strings.Contains(text, "a game that is still running"), strings.Contains(text, "a player still in the game"):
		forgetOnlineRejoin()
		return "That game has ended."
	case strings.Contains(text, "logical path rejoin ticket,"):
		forgetOnlineRejoin()
		return "That game no longer has your seat."
	case strings.Contains(text, "logical path rejoin history,"):
		forgetOnlineRejoin()
		return "That game has run too long to replay from the start."
	case strings.Contains(text, "this client sent version"), strings.Contains(text, "a hosted hello or room description request"):
		return "The server does not support rejoining. Both players need a server that runs this release."
	}
	return onlineRefusalText(err)
}
