package relay

import (
	"bytes"
	"encoding/json"
	"html/template"
	"slices"
	"sync"
	"time"
)

// The relay's public status (DESIGN_MULTIPLAYER §12.5): per-room and
// per-seat play statistics, served at /status and /status.json. It never
// shows a room code (an invitation), an address or process-level resource
// use; those stay in the operator's logs.

// Why a room ended, as counted on the status page.
const (
	finishCompleted    = "completed"
	finishHostLeft     = "host left the lobby"
	finishExpired      = "lobby expired"
	finishStalled      = "no progress"
	finishDisconnected = "player disconnected"
	finishDiverged     = "simulations diverged"
	finishFlooded      = "command flood"
	finishProtocol     = "protocol error"
	finishServer       = "relay closing"
)

const (
	statusRecentRooms = 20
	statusRateWindow  = 10 // seconds of history behind per-second rates
)

// rateWindow counts events per wall-clock second over the last few seconds.
type rateWindow struct {
	second [statusRateWindow]int64
	count  [statusRateWindow]uint32
}

func (w *rateWindow) add(now time.Time) {
	sec := now.Unix()
	i := sec % statusRateWindow
	if w.second[i] != sec {
		w.second[i], w.count[i] = sec, 0
	}
	w.count[i]++
}

// rate is the mean per second over the completed seconds of the window.
func (w *rateWindow) rate(now time.Time) float64 {
	sec := now.Unix()
	var n uint32
	for i := range statusRateWindow {
		if age := sec - w.second[i]; age >= 1 && age <= statusRateWindow {
			n += w.count[i]
		}
	}
	return float64(n) / statusRateWindow
}

// roomStats is a room's running statistics, owned by its goroutine.
type roomStats struct {
	created, startedAt time.Time
	bytesIn, bytesOut  uint64
	orders             uint64
	ticks              rateWindow
	orderRate          rateWindow
	ackLag             [HostedMaxSeats]time.Duration
}

func (s *roomStats) tickAt(now time.Time) { s.ticks.add(now) }

func (s *roomStats) orderAt(now time.Time) {
	s.orders++
	s.orderRate.add(now)
}

func (s *roomStats) fill(snap *roomSnapshot) {
	now := time.Now()
	snap.bytesIn, snap.bytesOut, snap.orders = s.bytesIn, s.bytesOut, s.orders
	snap.tickRate, snap.orderRate = s.ticks.rate(now), s.orderRate.rate(now)
}

type seatSnapshot struct {
	seat, slot     int
	present, ready bool
	team, side     uint8
	acked, behind  uint32
	final, left    bool
	rtt, ackLag    time.Duration
}

type roomSnapshot struct {
	id                  uint64
	size                int
	created, startedAt  time.Time
	started             bool
	ticks, compared     uint32
	tickRate, orderRate float64
	orders              uint64
	bytesIn, bytesOut   uint64
	queued              int
	seats               []seatSnapshot
}

// roomStatusBox publishes a room's latest snapshot to status readers.
type roomStatusBox struct {
	mu   sync.Mutex
	snap roomSnapshot
}

func (b *roomStatusBox) set(s roomSnapshot) {
	b.mu.Lock()
	b.snap = s
	b.mu.Unlock()
}

func (b *roomStatusBox) get() roomSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.snap
	s.seats = slices.Clone(s.seats)
	return s
}

// serverStats are totals since the relay started, protected by server.mu.
type serverStats struct {
	started           time.Time
	rooms, matches    uint64
	completed, failed uint64
	failedBy          map[string]uint64
	peakPlayers       int
	recent            []FinishedRoom
	// Chat lines the relay queued for a recipient, and lines and deliveries
	// it dropped.
	chatRelayed, chatDropped uint64
}

// recordChat counts chat deliveries, never their text or anyone's name.
func (s *HostedServer) recordChat(relayed, dropped uint64) {
	s.mu.Lock()
	s.stats.chatRelayed += relayed
	s.stats.chatDropped += dropped
	s.mu.Unlock()
}

func (s *HostedServer) recordStarted() {
	s.mu.Lock()
	s.stats.matches++
	s.mu.Unlock()
}

func (s *HostedServer) recordFinished(r *hostedRoom, stats *roomStats, started bool, players int, ticks uint32, reason string, seat int) {
	if reason == finishCompleted || reason == finishExpired || reason == finishServer {
		seat = -1
	}
	f := FinishedRoom{ID: r.id, Size: r.size, Players: players, Ticks: ticks, Orders: stats.orders, Outcome: reason, Seat: seat, EndedAt: time.Now().UTC()}
	if started {
		f.DurationSeconds = time.Since(stats.startedAt).Seconds()
	}
	s.mu.Lock()
	if reason == finishCompleted {
		s.stats.completed++
	} else if started {
		s.stats.failed++
		if s.stats.failedBy == nil {
			s.stats.failedBy = make(map[string]uint64)
		}
		s.stats.failedBy[reason]++
	}
	s.stats.recent = append(s.stats.recent, f)
	if len(s.stats.recent) > statusRecentRooms {
		s.stats.recent = slices.Delete(s.stats.recent, 0, len(s.stats.recent)-statusRecentRooms)
	}
	s.mu.Unlock()
	if s.config.Finished != nil {
		s.config.Finished(f)
	}
}

// HostedStatus is the relay's public status.
type HostedStatus struct {
	Protocol      int            `json:"protocol"`
	UptimeSeconds float64        `json:"uptime_seconds"`
	Connections   int            `json:"connections"`
	Lobbies       int            `json:"lobbies"`
	Matches       int            `json:"matches"`
	Players       int            `json:"players"`
	PeakPlayers   int            `json:"peak_players"`
	Totals        HostedTotals   `json:"totals"`
	Rooms         []RoomStatus   `json:"rooms"`
	Recent        []FinishedRoom `json:"recent"`
}

// HostedTotals counts rooms since the relay started. FailedBy counts started
// matches by how they ended without completing. ChatRelayed counts chat lines
// queued for a recipient, one per recipient; ChatDropped counts each line
// beyond its sender's rate limit and each delivery a recipient's full queue
// refused.
type HostedTotals struct {
	Rooms       uint64            `json:"rooms"`
	Matches     uint64            `json:"matches"`
	Completed   uint64            `json:"completed"`
	Failed      uint64            `json:"failed"`
	FailedBy    map[string]uint64 `json:"failed_by,omitempty"`
	ChatRelayed uint64            `json:"chat_relayed"`
	ChatDropped uint64            `json:"chat_dropped"`
}

// RoomStatus is one open room, identified by a sequence number, never its code.
type RoomStatus struct {
	ID              uint64       `json:"id"`
	Phase           string       `json:"phase"`
	Size            int          `json:"size"`
	Players         int          `json:"players"`
	AgeSeconds      float64      `json:"age_seconds"`
	DurationSeconds float64      `json:"duration_seconds"`
	Ticks           uint32       `json:"ticks"`
	TicksPerSecond  float64      `json:"ticks_per_second"`
	Orders          uint64       `json:"orders"`
	OrdersPerSecond float64      `json:"orders_per_second"`
	BytesIn         uint64       `json:"bytes_in"`
	BytesOut        uint64       `json:"bytes_out"`
	QueuedBytes     int          `json:"queued_bytes"`
	CheckedTick     uint32       `json:"checked_tick"`
	Seats           []SeatStatus `json:"seats"`
}

// SeatStatus is one seat of a room. Slot is -1 before the match starts.
type SeatStatus struct {
	Seat        int     `json:"seat"`
	Slot        int     `json:"slot"`
	Ready       bool    `json:"ready"`
	Team        int     `json:"team"`
	Side        int     `json:"side"`
	RTTMillis   float64 `json:"rtt_ms,omitempty"`
	AckLagMs    float64 `json:"ack_lag_ms,omitempty"`
	AckedTick   uint32  `json:"acked_tick,omitempty"`
	BehindTicks uint32  `json:"behind_ticks,omitempty"`
	Defeated    bool    `json:"defeated,omitempty"`
	Left        bool    `json:"left,omitempty"`
}

// FinishedRoom is one of the most recently closed rooms.
type FinishedRoom struct {
	ID              uint64  `json:"id"`
	Size            int     `json:"size"`
	Players         int     `json:"players"`
	DurationSeconds float64 `json:"duration_seconds"`
	Ticks           uint32  `json:"ticks"`
	Orders          uint64  `json:"orders"`
	Outcome         string  `json:"outcome"`
	// Seat is the seat whose message, departure or silence ended the room,
	// or -1: a completed match, an expired lobby or a closing relay. For a
	// divergence it is the seat whose report revealed the disagreement,
	// which need not be the one that diverged.
	Seat    int       `json:"seat"`
	EndedAt time.Time `json:"ended_at"`
}

func millis(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// Status reports the relay's public status. It holds no room code, address
// or process-level resource figure.
func (s *HostedServer) Status() HostedStatus {
	s.mu.Lock()
	out := HostedStatus{Protocol: hostedVersion, UptimeSeconds: time.Since(s.stats.started).Seconds(), Connections: len(s.conns),
		Totals: HostedTotals{Rooms: s.stats.rooms, Matches: s.stats.matches, Completed: s.stats.completed, Failed: s.stats.failed,
			ChatRelayed: s.stats.chatRelayed, ChatDropped: s.stats.chatDropped}}
	if len(s.stats.failedBy) != 0 {
		out.Totals.FailedBy = make(map[string]uint64, len(s.stats.failedBy))
		for k, v := range s.stats.failedBy {
			out.Totals.FailedBy[k] = v
		}
	}
	rooms := make([]*hostedRoom, 0, len(s.rooms))
	for _, r := range s.rooms {
		rooms = append(rooms, r)
	}
	out.Recent = slices.Clone(s.stats.recent)
	slices.Reverse(out.Recent)
	s.mu.Unlock()
	slices.SortFunc(rooms, func(a, b *hostedRoom) int {
		if a.id < b.id {
			return -1
		}
		return 1
	})
	now := time.Now()
	for _, r := range rooms {
		snap := r.status.get()
		rs := RoomStatus{ID: snap.id, Phase: "lobby", Size: snap.size, Players: len(snap.seats), AgeSeconds: now.Sub(snap.created).Seconds(),
			Ticks: snap.ticks, TicksPerSecond: snap.tickRate, Orders: snap.orders, OrdersPerSecond: snap.orderRate,
			BytesIn: snap.bytesIn, BytesOut: snap.bytesOut, QueuedBytes: snap.queued, CheckedTick: snap.compared}
		if snap.started {
			rs.Phase, rs.DurationSeconds = "playing", now.Sub(snap.startedAt).Seconds()
			out.Matches++
		} else {
			out.Lobbies++
		}
		for _, seat := range snap.seats {
			rs.Seats = append(rs.Seats, SeatStatus{Seat: seat.seat, Slot: seat.slot, Ready: seat.ready, Team: int(seat.team), Side: int(seat.side),
				RTTMillis: millis(seat.rtt), AckLagMs: millis(seat.ackLag), AckedTick: seat.acked, BehindTicks: seat.behind,
				Defeated: seat.final, Left: seat.left})
			if !seat.left {
				out.Players++
			}
		}
		out.Rooms = append(out.Rooms, rs)
	}
	s.mu.Lock()
	s.stats.peakPlayers = max(s.stats.peakPlayers, out.Players)
	out.PeakPlayers = s.stats.peakPlayers
	s.mu.Unlock()
	return out
}

var statusPageTemplate = template.Must(template.New("status").Funcs(template.FuncMap{
	"secs": func(v float64) string { return (time.Duration(v) * time.Second).Round(time.Second).String() },
	"kib":  func(v any) string { return template.HTMLEscapeString(formatKiB(v)) },
}).Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="5"><title>Nanolathe relay</title>
<style>
:root{color-scheme:light dark;--bg:#f6f6f4;--fg:#1d1d1b;--muted:#6b6b66;--line:#d8d8d2;--card:#fff}
@media (prefers-color-scheme:dark){:root{--bg:#141413;--fg:#e8e8e3;--muted:#9a9a93;--line:#33332f;--card:#1c1c1a}}
body{margin:0;padding:16px;background:var(--bg);color:var(--fg);font:14px/1.45 system-ui,sans-serif}
h1{font-size:20px;margin:0 0 4px}h2{font-size:15px;margin:24px 0 8px}.muted{color:var(--muted)}
.tiles{display:flex;flex-wrap:wrap;gap:8px;margin-top:12px}.tile{background:var(--card);border:1px solid var(--line);border-radius:8px;padding:8px 12px;min-width:96px}
.tile b{display:block;font-size:20px}table{border-collapse:collapse;width:100%;background:var(--card);border:1px solid var(--line);border-radius:8px;overflow:hidden}
th,td{text-align:left;padding:6px 8px;border-bottom:1px solid var(--line);white-space:nowrap}th{color:var(--muted);font-weight:500}
.wrap{overflow-x:auto}.room{margin-bottom:12px}
</style></head><body>
<h1>Nanolathe relay</h1>
<div class="muted">Protocol {{.Protocol}} · up {{secs .UptimeSeconds}} · refreshes every 5 seconds · <a href="/status.json">JSON</a></div>
<div class="tiles">
<div class="tile"><b>{{.Players}}</b>players</div><div class="tile"><b>{{.Matches}}</b>matches</div>
<div class="tile"><b>{{.Lobbies}}</b>lobbies</div><div class="tile"><b>{{.Connections}}</b>connections</div>
<div class="tile"><b>{{.PeakPlayers}}</b>peak players</div><div class="tile"><b>{{.Totals.Matches}}</b>matches started</div>
<div class="tile"><b>{{.Totals.Completed}}</b>completed</div><div class="tile"><b>{{.Totals.Failed}}</b>ended early</div>
<div class="tile"><b>{{.Totals.ChatRelayed}}</b>chat lines</div><div class="tile"><b>{{.Totals.ChatDropped}}</b>chat dropped</div>
</div>
{{if .Totals.FailedBy}}<p class="muted">Ended early: {{range $k, $v := .Totals.FailedBy}}{{$k}} {{$v}} · {{end}}</p>{{end}}
<h2>Open rooms</h2>
{{if not .Rooms}}<p class="muted">No open rooms.</p>{{end}}
{{range .Rooms}}<div class="room wrap"><table>
<tr><th colspan="8">Room {{.ID}} · {{.Phase}} · {{.Players}}/{{.Size}} players · {{if eq .Phase "playing"}}{{secs .DurationSeconds}} · tick {{.Ticks}} · {{printf "%.1f" .TicksPerSecond}} ticks/s · {{printf "%.1f" .OrdersPerSecond}} orders/s ({{.Orders}} total) · checked to tick {{.CheckedTick}}{{else}}open {{secs .AgeSeconds}}{{end}} · in {{kib .BytesIn}} / out {{kib .BytesOut}} · queued {{kib .QueuedBytes}}</th></tr>
<tr><th>Seat</th><th>Team</th><th>Side</th><th>State</th><th>RTT</th><th>Ack lag</th><th>Acked</th><th>Behind</th></tr>
{{range .Seats}}<tr><td>{{.Seat}}{{if ge .Slot 0}} (slot {{.Slot}}){{end}}</td><td>{{if .Team}}{{.Team}}{{else}}—{{end}}</td><td>{{.Side}}</td>
<td>{{if .Left}}left{{else if .Defeated}}defeated{{else if .Ready}}ready{{else}}not ready{{end}}</td>
<td>{{if .RTTMillis}}{{printf "%.0f" .RTTMillis}} ms{{else}}—{{end}}</td><td>{{if .AckLagMs}}{{printf "%.0f" .AckLagMs}} ms{{else}}—{{end}}</td>
<td>{{if .AckedTick}}{{.AckedTick}}{{else}}—{{end}}</td><td>{{if .BehindTicks}}{{.BehindTicks}}{{else}}—{{end}}</td></tr>{{end}}
</table></div>{{end}}
<h2>Recently closed</h2>
{{if not .Recent}}<p class="muted">None yet.</p>{{else}}<div class="wrap"><table>
<tr><th>Room</th><th>Players</th><th>Duration</th><th>Ticks</th><th>Orders</th><th>Outcome</th><th>Seat</th><th>Ended (UTC)</th></tr>
{{range .Recent}}<tr><td>{{.ID}}</td><td>{{.Players}}/{{.Size}}</td><td>{{secs .DurationSeconds}}</td><td>{{.Ticks}}</td><td>{{.Orders}}</td><td>{{.Outcome}}</td><td>{{if ge .Seat 0}}{{.Seat}}{{else}}—{{end}}</td><td>{{.EndedAt.Format "15:04:05"}}</td></tr>{{end}}
</table></div>{{end}}
</body></html>`))

func formatKiB(v any) string {
	var n float64
	switch x := v.(type) {
	case uint64:
		n = float64(x)
	case int:
		n = float64(x)
	}
	switch {
	case n >= 1<<20:
		return template.HTMLEscapeString(trimFloat(n/(1<<20)) + " MiB")
	case n >= 1<<10:
		return trimFloat(n/(1<<10)) + " KiB"
	default:
		return trimFloat(n) + " B"
	}
}

func trimFloat(v float64) string {
	b, _ := json.Marshal(float64(int64(v*10)) / 10)
	return string(b)
}

// statusPage renders path, or reports that it is not a status page.
func (s *HostedServer) statusPage(path string) (contentType string, body []byte, ok bool) {
	switch path {
	case "/status":
		var buf bytes.Buffer
		if err := statusPageTemplate.Execute(&buf, s.Status()); err != nil {
			return "", nil, false
		}
		return "text/html; charset=utf-8", buf.Bytes(), true
	case "/status.json":
		b, err := json.MarshalIndent(s.Status(), "", "  ")
		if err != nil {
			return "", nil, false
		}
		return "application/json", append(b, '\n'), true
	}
	return "", nil, false
}
