package main

// Online battle measurements: the network overlay and the end-of-match summary
// (DESIGN_INTERFACE_HUD_INPUT "Online games"). The host wraps the grant stream
// to see grants arrive and its own commands carried and assigned to ticks, the
// pump reports the ticks it executed, and the connection's own counters give
// its traffic. These are host timings only; nothing here reaches the
// simulation [I6].

import (
	"fmt"
	"image"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/lockstep"
	"github.com/nanolathe-gg/nanolathe/internal/relay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// onlineStallGap is the gap between executed ticks the host counts as a
// stall: three tick intervals at 30 ticks a second. A host measurement
// threshold, not a simulation value.
const onlineStallGap = 100 * time.Millisecond

// onlineLatencyWindow is how many recent orders the overlay's percentiles
// cover; onlineLatencyKeep bounds what the summary keeps.
const (
	onlineLatencyWindow = 64
	onlineLatencyKeep   = 1 << 14
)

// onlineGrantInterval is the relay's steady spacing of grants at 30 ticks a
// second (DESIGN_MULTIPLAYER §16.5.2); a grant's jitter is how far its gap
// from the one before strays from it. The overlay's jitter covers the grants
// of the last onlineJitterWindow and its traffic rates the last
// onlineTrafficWindow. Host measurement windows, not simulation values.
const (
	onlineGrantInterval = time.Second / 30
	onlineJitterWindow  = 5 * time.Second
	onlineTrafficWindow = time.Second
)

// onlineTrafficReporter is a connection that counts its own relay messages.
// It is type-asserted from the wrapped client, never assumed: a transport
// without it leaves the overlay's traffic unreported.
type onlineTrafficReporter interface {
	Traffic() relay.LocalTraffic
}

// onlineProgressReporter is a connection that carries the hosted relay's
// match report (DESIGN_MULTIPLAYER §16.5.2). It is type-asserted like
// onlineTrafficReporter: an older relay, or the loopback play-test relay,
// sends no report, and the overlay then shows only this client's own view.
type onlineProgressReporter interface {
	Progress() (relay.HostedMatchProgress, bool)
}

// onlineRelayLead is the relay's lead bound: it seals no grant more than this
// many ticks past the slowest playing seat's acknowledgement, so a seat that
// far behind holds every other seat (DESIGN_MULTIPLAYER §16.5.2).
const onlineRelayLead = 30

// onlineNetStats wraps a battle's grant stream.
type onlineNetStats struct {
	lockstep.Client
	slot   uint8
	humans int
	now    func() time.Time // the host clock; a test seam

	mu        sync.Mutex
	submitted map[uint64]time.Time
	assigned  []onlineAssigned // own orders granted, awaiting execution
	received  uint32           // the newest grant's tick
	relayTrip []float64        // milliseconds from Submit to the grant carrying it, newest last
	lastGrant time.Time        // when the newest grant arrived
	gaps      []onlineGap      // grant arrivals within onlineJitterWindow

	// Game-goroutine state from observe.
	started, firstTick, lastTick time.Time
	lastTickNo                   uint32
	recent                       []time.Time // execution times in the last second
	latency                      []float64   // milliseconds, newest last
	stalls                       int
	stalled                      time.Duration
	window                       time.Time
	windowTicks                  int
	worstRate                    float64
	haveWorst                    bool
	summarized                   bool
	traffic                      []onlineTrafficSample // counter readings over onlineTrafficWindow
}

type onlineAssigned struct {
	tick uint32
	at   time.Time
}

// onlineGap is one grant's arrival and its jitter in milliseconds.
type onlineGap struct {
	at     time.Time
	jitter float64
}

type onlineTrafficSample struct {
	at time.Time
	relay.LocalTraffic
}

func newOnlineNetStats(c lockstep.Client, slot uint8, humans int) *onlineNetStats {
	return &onlineNetStats{Client: c, slot: slot, humans: humans, now: time.Now, submitted: map[uint64]time.Time{}}
}

func onlineMillis(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// Submit records when this seat sent each order.
func (n *onlineNetStats) Submit(payload []byte) (uint64, error) {
	sequence, err := n.Client.Submit(payload)
	if err == nil {
		at := n.now()
		n.mu.Lock()
		n.submitted[sequence] = at
		n.mu.Unlock()
	}
	return sequence, err
}

// ReadGrant runs on the driver's reader goroutine. It notes each grant's
// arrival, and for this seat's orders the relay round trip — Submit to the
// grant that carries the order — and the tick each was given.
func (n *onlineNetStats) ReadGrant() (relay.LocalGrant, error) {
	grant, err := n.Client.ReadGrant()
	if err != nil {
		return grant, err
	}
	at := n.now()
	n.mu.Lock()
	defer n.mu.Unlock()
	n.received = grant.Tick
	if !n.lastGrant.IsZero() {
		n.gaps = append(n.gaps, onlineGap{at: at, jitter: onlineMillis((at.Sub(n.lastGrant) - onlineGrantInterval).Abs())})
	}
	n.lastGrant = at
	n.gaps = onlineRecentGaps(n.gaps, at)
	for _, c := range grant.Commands {
		if c.Seat != n.slot {
			continue
		}
		if sent, ok := n.submitted[c.Sequence]; ok {
			delete(n.submitted, c.Sequence)
			n.relayTrip = append(n.relayTrip, onlineMillis(at.Sub(sent)))
			n.assigned = append(n.assigned, onlineAssigned{tick: grant.Tick, at: sent})
		}
	}
	if len(n.relayTrip) > onlineLatencyKeep {
		n.relayTrip = n.relayTrip[len(n.relayTrip)-onlineLatencyKeep:]
	}
	return grant, nil
}

// onlineRecentGaps drops the arrivals older than onlineJitterWindow at now.
func onlineRecentGaps(gaps []onlineGap, now time.Time) []onlineGap {
	cut := 0
	for cut < len(gaps) && now.Sub(gaps[cut].at) > onlineJitterWindow {
		cut++
	}
	return gaps[cut:]
}

// observe runs after every pump on the game goroutine: executed ticks, their
// rate and stalls, and the latency of orders whose tick has now run.
func (n *onlineNetStats) observe(sess *session.Session, now time.Time) {
	if n == nil || sess == nil || sess.Clock == nil {
		return
	}
	if n.started.IsZero() {
		n.started = now
	}
	tick := sess.Clock.GlobalTick
	if tick != n.lastTickNo {
		if n.firstTick.IsZero() {
			n.firstTick, n.window = now, now
		} else if gap := now.Sub(n.lastTick); gap > onlineStallGap && !sess.OnlineBattleEnded() {
			n.stalls++
			n.stalled += gap
		}
		ran := int(tick - n.lastTickNo)
		for range ran {
			n.recent = append(n.recent, now)
		}
		n.windowTicks += ran
		n.lastTick, n.lastTickNo = now, tick
		n.mu.Lock()
		keep := n.assigned[:0]
		for _, a := range n.assigned {
			if a.tick <= tick {
				n.latency = append(n.latency, float64(now.Sub(a.at))/float64(time.Millisecond))
				continue
			}
			keep = append(keep, a)
		}
		n.assigned = keep
		n.mu.Unlock()
		if len(n.latency) > onlineLatencyKeep {
			n.latency = n.latency[len(n.latency)-onlineLatencyKeep:]
		}
	}
	cut := 0
	for cut < len(n.recent) && now.Sub(n.recent[cut]) > time.Second {
		cut++
	}
	n.recent = n.recent[cut:]
	if t, ok := n.Client.(onlineTrafficReporter); ok {
		// Keep the newest reading at least a window old as the rates' base.
		n.traffic = append(n.traffic, onlineTrafficSample{at: now, LocalTraffic: t.Traffic()})
		for len(n.traffic) > 2 && now.Sub(n.traffic[1].at) >= onlineTrafficWindow {
			n.traffic = n.traffic[1:]
		}
	}
	if !n.firstTick.IsZero() && now.Sub(n.window) >= time.Second && !sess.OnlineBattleEnded() {
		rate := float64(n.windowTicks) / now.Sub(n.window).Seconds()
		if !n.haveWorst || rate < n.worstRate {
			n.worstRate, n.haveWorst = rate, true
		}
		n.window, n.windowTicks = now, 0
	}
}

// percentiles are the 50th and 95th of the newest count samples.
func onlinePercentiles(samples []float64, count int) (p50, p95 float64, n int) {
	if count > 0 && len(samples) > count {
		samples = samples[len(samples)-count:]
	}
	if len(samples) == 0 {
		return 0, 0, 0
	}
	sorted := slices.Clone(samples)
	slices.Sort(sorted)
	at := func(q float64) float64 { return sorted[min(len(sorted)-1, int(q*float64(len(sorted))))] }
	return at(0.5), at(0.95), len(sorted)
}

// onlineNetOverlay is the network overlay's text: summary lines, then one row
// per human seat.
type onlineNetOverlay struct {
	lines   []string
	heading []string // the seat columns' headings; none without a relay report
	seats   []onlineNetSeat
}

// onlineNetSeat is one human seat's row: its name and state and, with a relay
// report, its ping to the relay and how many ticks it is behind.
type onlineNetSeat struct {
	cells   []string
	holding bool // the relay waits for this seat at its lead bound
}

// String is the overlay as text, a row's cells two spaces apart.
func (o onlineNetOverlay) String() string {
	rows := slices.Clone(o.lines)
	if len(o.heading) > 0 {
		rows = append(rows, strings.TrimSpace(strings.Join(o.heading, "  ")))
	}
	for _, seat := range o.seats {
		rows = append(rows, strings.Join(seat.cells, "  "))
	}
	return strings.Join(rows, "\n")
}

// overlay is the network overlay's text for the committed tick.
func (n *onlineNetStats) overlay(sess *session.Session) onlineNetOverlay {
	now := n.now()
	n.mu.Lock()
	received := n.received
	trip50, trip95, trips := onlinePercentiles(n.relayTrip, onlineLatencyWindow)
	n.gaps = onlineRecentGaps(n.gaps, now)
	jitters := make([]float64, len(n.gaps))
	for i, g := range n.gaps {
		jitters[i] = g.jitter
	}
	n.mu.Unlock()
	tick := sess.Clock.GlobalTick
	buffered := 0
	if received > tick {
		buffered = int(received - tick)
	}
	jitter := "--"
	if _, p95, count := onlinePercentiles(jitters, 0); count > 0 {
		jitter = fmt.Sprintf("p95 %.0f ms", p95)
	}
	p50, p95, orders := onlinePercentiles(n.latency, onlineLatencyWindow)
	var progress relay.HostedMatchProgress
	reported := false
	if r, ok := n.Client.(onlineProgressReporter); ok {
		progress, reported = r.Progress()
	}
	o := onlineNetOverlay{lines: []string{
		fmt.Sprintf("Online  tick %d  %d ticks/s  stalls %d", tick, len(n.recent), n.stalls),
		fmt.Sprintf("Grants  %d buffered  jitter %s", buffered, jitter),
		onlineLatencyLine("Relay round trip", trip50, trip95, trips),
		onlineLatencyLine("Order latency", p50, p95, orders),
		n.trafficLine(),
	}}
	if !reported {
		o.lines = append(o.lines, "No match report from the relay")
		// TODO(question): with no relay report a seat that left after its
		// final result still reads as defeated (or won); the grant stream
		// carries no departure. Settled wherever the relay sends its match
		// report (DESIGN_MULTIPLAYER §16.5.2); a relay that sends none would
		// need a departure notice in the stream.
		for slot := range n.humans {
			o.seats = append(o.seats, onlineNetSeat{cells: []string{onlineSeatName(onlinePlayerName(sess, slot), slot, n.slot), onlineSeatResult(sess, slot)}})
		}
		return o
	}
	o.lines = append(o.lines, fmt.Sprintf("Checksums agreed through tick %d", progress.Agreed))
	o.heading = []string{"", "State", "Ping", "Behind"}
	// A seat's lag is the report's sealed tick less its acknowledgement, both
	// read by the relay at one moment; this client's newest grant would add
	// the report's age. The relay seals ahead of the slowest playing seat by
	// at most its lead bound; at the bound everyone waits for that seat. When
	// every playing seat is as far behind, as while the opening holds them
	// all, no seat holds the others back.
	sealed := progress.Sealed
	slowest, fastest, playing := uint32(0), uint32(0), false
	for slot := 0; slot < n.humans && slot < len(progress.Seats); slot++ {
		if s := progress.Seats[slot]; s.Playing {
			if !playing {
				slowest, fastest = s.Acked, s.Acked
			}
			slowest, fastest, playing = min(slowest, s.Acked), max(fastest, s.Acked), true
		}
	}
	holding := playing && fastest > slowest && sealed >= slowest && sealed-slowest >= onlineRelayLead
	for slot := range n.humans {
		row := onlineNetSeat{cells: []string{onlineSeatName(onlinePlayerName(sess, slot), slot, n.slot), onlineSeatResult(sess, slot), "--", "--"}}
		if slot < len(progress.Seats) {
			switch s := progress.Seats[slot]; {
			case !s.Playing:
				// A seat the relay no longer compares has left the match.
				row.cells[1] = "left"
			default:
				if s.RTT > 0 {
					row.cells[2] = onlinePing(s.RTT)
				}
				row.cells[3] = strconv.FormatUint(uint64(sealed-min(s.Acked, sealed)), 10)
				if holding && s.Acked == slowest {
					row.cells[1], row.holding = "holding", true
				}
			}
		}
		o.seats = append(o.seats, row)
	}
	return o
}

// onlinePing is a relay ping in whole milliseconds, under a millisecond as
// "<1 ms", or in tenths of a second from one second, so the column stays
// narrow.
func onlinePing(rtt time.Duration) string {
	if rtt < time.Millisecond {
		return "<1 ms"
	}
	if ms := math.Round(onlineMillis(rtt)); ms < 1000 {
		return fmt.Sprintf("%.0f ms", ms)
	}
	return fmt.Sprintf("%.1f s", rtt.Seconds())
}

// onlineNetNameBytes is the longest name the seat table shows whole; a
// longer one shows its first onlineNetNameBytes-2 bytes and "..", so ten
// seats of the longest names still fit beside or below the +fps panel at
// 640x480. A host display bound, not a name rule.
const onlineNetNameBytes = 10

// onlineSeatName is a seat row's label: the player's name, marked "(You)" for
// this seat. An online session names every human, by display name or
// "Player n" (session.NewOnlineMatchRequest); a record without one reads
// "Player n" here too.
func onlineSeatName(name string, slot int, local uint8) string {
	if name == "" {
		name = fmt.Sprintf("Player %d", slot+1)
	}
	if len(name) > onlineNetNameBytes {
		name = name[:onlineNetNameBytes-2] + ".."
	}
	if uint8(slot) == local {
		return name + " (You)"
	}
	return name
}

// onlinePlayerName is slot's player record name, empty when there is none.
func onlinePlayerName(sess *session.Session, slot int) string {
	if sess == nil || sess.Econ == nil || slot < 0 || slot >= len(sess.Econ.Players) {
		return ""
	}
	return sess.Econ.Players[slot].Name
}

// onlineSeatResult is a seat's state from the shared simulation's results.
func onlineSeatResult(sess *session.Session, slot int) string {
	r := sess.ResultForSeat(uint8(slot))
	switch {
	case !r.Ended:
		return "playing"
	case r.Kind == "victory":
		return "won"
	}
	return "defeated"
}

func onlineLatencyLine(label string, p50, p95 float64, count int) string {
	if count == 0 {
		return label + "  no samples yet"
	}
	return fmt.Sprintf("%s  p50 %.0f ms  p95 %.0f ms", label, p50, p95)
}

// trafficLine is the connection's message rates over the last
// onlineTrafficWindow, from its own counters.
func (n *onlineNetStats) trafficLine() string {
	if _, ok := n.Client.(onlineTrafficReporter); !ok {
		return "Traffic  not reported"
	}
	if len(n.traffic) < 2 {
		return "Traffic  measuring"
	}
	first, last := n.traffic[0], n.traffic[len(n.traffic)-1]
	seconds := last.at.Sub(first.at).Seconds()
	if seconds <= 0 {
		return "Traffic  measuring"
	}
	rate := func(a, b uint64) float64 { return float64(b-a) / seconds }
	return fmt.Sprintf("In %.1f KB/s %.0f msg/s  Out %.1f KB/s %.0f msg/s",
		rate(first.BytesIn, last.BytesIn)/1024, rate(first.MessagesIn, last.MessagesIn),
		rate(first.BytesOut, last.BytesOut)/1024, rate(first.MessagesOut, last.MessagesOut))
}

// summary is the end-of-match line on standard error.
func (n *onlineNetStats) summary(sess *session.Session, ending string) string {
	duration := time.Duration(0)
	if !n.firstTick.IsZero() {
		duration = n.lastTick.Sub(n.firstTick)
	}
	ticks := uint32(0)
	if sess != nil && sess.Clock != nil {
		ticks = sess.Clock.GlobalTick
	}
	average := 0.0
	if duration > 0 {
		average = float64(ticks) / duration.Seconds()
	}
	p50, p95, orders := onlinePercentiles(n.latency, 0)
	n.mu.Lock()
	trip50, trip95, trips := onlinePercentiles(n.relayTrip, 0)
	n.mu.Unlock()
	var b strings.Builder
	fmt.Fprintf(&b, "nanolathe: online match %s after %s and %d ticks; ticks/s average %.1f", ending, duration.Round(100*time.Millisecond), ticks, average)
	if n.haveWorst {
		fmt.Fprintf(&b, ", worst %.1f", n.worstRate)
	}
	if trips > 0 {
		fmt.Fprintf(&b, "; relay round trip p50 %.0f ms, p95 %.0f ms over %d orders", trip50, trip95, trips)
	}
	if orders > 0 {
		fmt.Fprintf(&b, "; order latency p50 %.0f ms, p95 %.0f ms over %d orders", p50, p95, orders)
	}
	if t, ok := n.Client.(onlineTrafficReporter); ok {
		total := t.Traffic()
		fmt.Fprintf(&b, "; traffic in %s (%d messages), out %s (%d messages)", onlineBytes(total.BytesIn), total.MessagesIn, onlineBytes(total.BytesOut), total.MessagesOut)
	}
	fmt.Fprintf(&b, "; stalls %d (%.1f s)", n.stalls, n.stalled.Seconds())
	return b.String()
}

// onlineBytes is a byte total in kilobytes, or megabytes from one megabyte.
func onlineBytes(n uint64) string {
	if n < 1<<20 {
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}

// netOverlayShown reports the network overlay: an online battle with +net, or
// with the FPS display.
func (b *battleSession) netOverlayShown() bool {
	if b == nil || b.multiplayer == nil || b.multiplayer.net == nil || b.sess == nil || b.sess.Clock == nil {
		return false
	}
	shown := b.netVisible
	if b.shell != nil {
		shown = b.shell.netVisible
	}
	return shown || b.fpsShown()
}

// The +fps panel the window host draws over the top right of the frame, in
// the HUD's pixels: 350 by 284, 6 from the top and right edges
// (internal/platform/ebitenapp fps_overlay.go). The network overlay keeps
// clear of it whenever both are shown.
const (
	onlineFPSPanelW      = 350
	onlineFPSPanelH      = 284
	onlineFPSPanelMargin = 6
)

// onlineNetLayout is the network overlay measured in its font — the summary
// lines and the seat table's columns — and where it is placed.
type onlineNetLayout struct {
	step          int   // line pitch
	space         int   // the font's space advance
	lines, linesW int   // summary lines and the widest of them
	seats         int   // seat rows
	heading       bool  // the seat table has a heading row
	columns       []int // seat-table column widths

	x, y     int // the first line's pen
	backdrop image.Rectangle
	gap      int // between a row's cells
	blockW   int // one block of seat rows
	blocks   int // seat-row blocks side by side
	perBlock int // seat rows in each block
	fits     bool
}

func onlineNetMeasure(font *formats.FNT, o onlineNetOverlay) onlineNetLayout {
	l := onlineNetLayout{step: int(font.Height) + 1, space: client.MeasureText(font, " "), lines: len(o.lines), seats: len(o.seats), heading: len(o.heading) > 0}
	for _, line := range o.lines {
		l.linesW = max(l.linesW, client.MeasureText(font, line))
	}
	widen := func(cells []string) {
		for i, cell := range cells {
			if i == len(l.columns) {
				l.columns = append(l.columns, 0)
			}
			l.columns[i] = max(l.columns[i], client.MeasureText(font, cell))
		}
	}
	widen(o.heading)
	for _, seat := range o.seats {
		widen(seat.cells)
	}
	return l
}

// arrange lays the seat rows out in blocks side by side with gap between
// cells, and sizes the backdrop.
func (l onlineNetLayout) arrange(blocks, gap int) onlineNetLayout {
	l.blocks, l.gap = blocks, gap
	l.perBlock = (l.seats + blocks - 1) / blocks
	l.blockW = 0
	for i, w := range l.columns {
		if i > 0 {
			l.blockW += gap
		}
		l.blockW += w
	}
	rows := l.perBlock
	if l.heading && l.seats > 0 {
		rows++
	}
	w := max(l.linesW, blocks*l.blockW+(blocks-1)*l.blockGap())
	l.backdrop = image.Rect(0, 0, w+6, (l.lines+rows)*l.step+3)
	return l
}

func (l onlineNetLayout) blockGap() int { return 2 * l.space }

// place puts the overlay at the top left of the world view, below the
// resource strip and right of the rail. With the +fps panel shown it stays
// beside the panel when it fits there, and otherwise goes below it, its seat
// rows then in two blocks when one block is too tall for the space left.
// Cells are two spaces apart, closing to one where the width is short.
func (l onlineNetLayout) place(screenW, screenH int, fps bool, messages int, chrome camera.ChromeInsets) onlineNetLayout {
	const clearance = 2
	cam := camera.Camera{Chrome: chrome}
	rail, strip, footer := cam.ChromeInset()
	left, top := int(rail)+2, int(strip)+4
	// The message column shares the corner; its visible lines stay readable
	// above the overlay, which moves down while they show.
	top = max(top, messages+clearance)
	right, bottom := screenW-clearance, screenH-int(footer)-clearance
	type candidate struct{ y, blocks, right int }
	candidates := []candidate{{top, 1, right}, {top, 2, right}}
	var panel image.Rectangle
	if fps {
		px := max(0, screenW-onlineFPSPanelW-onlineFPSPanelMargin)
		panel = image.Rect(px, onlineFPSPanelMargin, px+onlineFPSPanelW, onlineFPSPanelMargin+onlineFPSPanelH)
		below := max(top, panel.Max.Y+2*clearance)
		candidates = []candidate{{top, 1, min(right, panel.Min.X-clearance)}, {below, 1, right}, {below, 2, right}}
	}
	for i, c := range candidates {
		l = l.arrange(c.blocks, l.widestGap(c.blocks, c.right-left-6))
		l.backdrop = l.backdrop.Add(image.Pt(left, c.y))
		l.x, l.y = left+3, c.y+2
		l.fits = l.backdrop.Max.X <= right && l.backdrop.Max.Y <= bottom && (!fps || !l.backdrop.Inset(-clearance).Overlaps(panel))
		if l.fits || i == len(candidates)-1 {
			break
		}
	}
	return l
}

// widestGap is the widest cell gap, from one space to two, that keeps blocks
// of seat rows within width.
func (l onlineNetLayout) widestGap(blocks, width int) int {
	narrow, wide := l.space, 2*l.space
	if len(l.columns) < 2 {
		return wide
	}
	cells := 0
	for _, w := range l.columns {
		cells += w
	}
	fit := ((width-(blocks-1)*l.blockGap())/blocks - cells) / (len(l.columns) - 1)
	return max(narrow, min(wide, fit))
}

// drawOnlineNetwork draws the network overlay at the top left of the world
// view, in the console face the clock and +bps use: the summary lines, then
// the seat rows in columns, numbers right-aligned, a holding seat in red.
func (h *retailBattleHUD) drawOnlineNetwork(c *client.Client, b *battleSession) {
	if h == nil || c == nil || h.console == nil || !b.netOverlayShown() {
		return
	}
	o := b.multiplayer.net.overlay(b.sess)
	screenW, screenH := c.Size()
	railInset, topInset, bottomInset := b.cam.ChromeInset()
	l := onlineNetMeasure(h.console, o).place(screenW, screenH, b.fpsShown(), c.MessageColumnBottom(), camera.ChromeInsets{Left: railInset, Top: topInset, Bottom: bottomInset})
	r := l.backdrop
	c.UIFillRect(r.Min.X, r.Min.Y, r.Dx(), r.Dy(), h.guiColor(0))
	for i, line := range o.lines {
		c.UIText(h.console, line, l.x, l.y+i*l.step, h.guiColor(15))
	}
	row := func(cells []string, x, y int, color byte) {
		for i, cell := range cells {
			pen := x
			if i >= 2 {
				pen += l.columns[i] - client.MeasureText(h.console, cell)
			}
			c.UIText(h.console, cell, pen, y, color)
			x += l.columns[i] + l.gap
		}
	}
	top := l.y + len(o.lines)*l.step
	for block := range l.blocks {
		x, y := l.x+block*(l.blockW+l.blockGap()), top
		if l.heading {
			row(o.heading, x, y, h.guiColor(15))
			y += l.step
		}
		for _, seat := range o.seats[min(len(o.seats), block*l.perBlock):min(len(o.seats), (block+1)*l.perBlock)] {
			color := h.guiColor(15)
			if seat.holding {
				color = h.guiColor(12)
			}
			row(seat.cells, x, y, color)
			y += l.step
		}
	}
}

// summarizeOnline writes the end-of-match summary once, at the battle's exit.
func (m *battleMultiplayer) summarizeOnline(sess *session.Session) {
	if m == nil || m.net == nil || m.net.summarized {
		return
	}
	m.net.summarized = true
	ending := "left"
	switch {
	case m.failure != nil:
		ending = "stopped (" + onlineRefusalText(m.failure) + ")"
	case sess != nil && sess.OnlineBattleEnded():
		ending = "ended"
	case sess != nil && sess.ResultForSeat(sess.LocalOwner).Ended:
		ending = "left after this player's defeat"
	}
	fmt.Fprintln(os.Stderr, m.net.summary(sess, ending))
}
