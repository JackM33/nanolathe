package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/relay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
)

type fakeNetClient struct {
	fakeGrantStream
	grants []relay.LocalGrant
	seq    uint64
}

func (c *fakeNetClient) Submit([]byte) (uint64, error) { c.seq++; return c.seq, nil }
func (c *fakeNetClient) ReadGrant() (relay.LocalGrant, error) {
	g := c.grants[0]
	c.grants = c.grants[1:]
	return g, nil
}

// fakeReportingClient adds a hosted relay connection's reports.
type fakeReportingClient struct {
	fakeNetClient
	progress relay.HostedMatchProgress
	reported bool
	traffic  relay.LocalTraffic
}

func (c *fakeReportingClient) Progress() (relay.HostedMatchProgress, bool) {
	return c.progress, c.reported
}
func (c *fakeReportingClient) Traffic() relay.LocalTraffic { return c.traffic }

// netClock is a settable host clock for onlineNetStats.
type netClock struct{ at time.Time }

func (c *netClock) now() time.Time          { return c.at }
func (c *netClock) advance(d time.Duration) { c.at = c.at.Add(d) }

func netTestSession() *session.Session {
	return newTestBattle(testCatalogON05(), testWorldON05(20, 20)).sess
}

// netTestStats wraps c for slot 0 of humans seats on a settable clock.
func netTestStats(c *fakeNetClient, humans int) (*onlineNetStats, *netClock) {
	clock := &netClock{at: time.Unix(1000, 0)}
	n := newOnlineNetStats(c, 0, humans)
	n.now = clock.now
	return n, clock
}

func netOverlayText(n *onlineNetStats, sess *session.Session) string {
	return n.overlay(sess).String()
}

// grantOwn has the relay grant this seat's next order, sent trip ago.
func grantOwn(t *testing.T, n *onlineNetStats, c *fakeNetClient, clock *netClock, tick uint32, trip time.Duration) {
	t.Helper()
	sequence, err := n.Submit(nil)
	if err != nil {
		t.Fatal(err)
	}
	clock.advance(trip)
	c.grants = append(c.grants, relay.LocalGrant{Tick: tick, Commands: []relay.LocalCommand{{Seat: n.slot, Sequence: sequence}}})
	if _, err := n.ReadGrant(); err != nil {
		t.Fatal(err)
	}
}

func TestOnlineNetStatsMeasureOrdersStallsAndRate(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	sess := b.sess
	fake := &fakeNetClient{grants: []relay.LocalGrant{{Tick: 5, Commands: []relay.LocalCommand{{Seat: 1, Sequence: 1}, {Seat: 0, Sequence: 1}}}}}
	n := newOnlineNetStats(fake, 1, 2)
	start := time.Now()
	if _, err := n.Submit(nil); err != nil {
		t.Fatal(err)
	}
	n.submitted[1] = start
	if _, err := n.ReadGrant(); err != nil {
		t.Fatal(err)
	}
	sess.Clock.GlobalTick = 1
	n.observe(sess, start.Add(10*time.Millisecond))
	sess.Clock.GlobalTick = 5
	n.observe(sess, start.Add(80*time.Millisecond))
	if len(n.latency) != 1 || n.latency[0] != 80 {
		t.Fatalf("latency %v", n.latency)
	}
	sess.Clock.GlobalTick = 6
	n.observe(sess, start.Add(380*time.Millisecond))
	if n.stalls != 1 {
		t.Fatalf("stalls %d", n.stalls)
	}
	lines := n.overlay(sess).String()
	for _, want := range []string{"tick 6", "Order latency  p50 80 ms", "Player 2 (You)  playing"} {
		if !strings.Contains(lines, want) {
			t.Fatalf("overlay %q lacks %q", lines, want)
		}
	}
	summary := n.summary(sess, "ended")
	for _, want := range []string{"online match ended", "6 ticks", "p50 80 ms", "stalls 1"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary %q lacks %q", summary, want)
		}
	}
}

// The relay round trip ends at the grant that carries an order, the order
// latency at the tick that runs it; the overlay's percentiles cover only the
// recent orders.
func TestOnlineNetRelayRoundTripRecentWindow(t *testing.T) {
	sess := netTestSession()
	c := &fakeNetClient{}
	n, clock := netTestStats(c, 2)
	grantOwn(t, n, c, clock, 3, 40*time.Millisecond)
	clock.advance(50 * time.Millisecond)
	sess.Clock.GlobalTick = 3
	n.observe(sess, clock.at)
	text := netOverlayText(n, sess)
	for _, want := range []string{"Relay round trip  p50 40 ms  p95 40 ms", "Order latency  p50 90 ms  p95 90 ms"} {
		if !strings.Contains(text, want) {
			t.Fatalf("overlay %q lacks %q", text, want)
		}
	}
	for i := range onlineLatencyWindow * 2 {
		grantOwn(t, n, c, clock, uint32(4+i), 500*time.Millisecond)
	}
	for i := range onlineLatencyWindow {
		grantOwn(t, n, c, clock, uint32(200+i), 30*time.Millisecond)
	}
	if text := netOverlayText(n, sess); !strings.Contains(text, "Relay round trip  p50 30 ms  p95 30 ms") {
		t.Fatalf("older orders reached the overlay's window: %q", text)
	}
	// The summary covers the whole match.
	if summary := n.summary(sess, "ended"); !strings.Contains(summary, "relay round trip p50 500 ms, p95 500 ms over 193 orders") {
		t.Fatalf("summary %q", summary)
	}
}

// Grant jitter is each arrival gap's distance from the 30 Hz spacing, over
// the last few seconds of grants only.
func TestOnlineNetGrantJitterWindow(t *testing.T) {
	sess := netTestSession()
	c := &fakeNetClient{}
	n, clock := netTestStats(c, 2)
	read := func(tick uint32) {
		c.grants = append(c.grants, relay.LocalGrant{Tick: tick})
		if _, err := n.ReadGrant(); err != nil {
			t.Fatal(err)
		}
	}
	if text := netOverlayText(n, sess); !strings.Contains(text, "jitter --") {
		t.Fatalf("jitter without grants: %q", text)
	}
	read(1)
	for tick := uint32(2); tick <= 20; tick++ {
		clock.advance(onlineGrantInterval)
		read(tick)
	}
	if text := netOverlayText(n, sess); !strings.Contains(text, "Grants  20 buffered  jitter p95 0 ms") {
		t.Fatalf("steady grants: %q", text)
	}
	clock.advance(onlineGrantInterval + 100*time.Millisecond)
	read(21)
	if text := netOverlayText(n, sess); !strings.Contains(text, "jitter p95 100 ms") {
		t.Fatalf("a late grant: %q", text)
	}
	clock.advance(onlineJitterWindow + time.Millisecond)
	if text := netOverlayText(n, sess); !strings.Contains(text, "jitter --") {
		t.Fatalf("grants older than the window: %q", text)
	}
}

// Traffic rates come from the connection's own counters over the last
// second; a connection without counters says so.
func TestOnlineNetTrafficRates(t *testing.T) {
	sess := netTestSession()
	if n, _ := netTestStats(&fakeNetClient{}, 2); !strings.Contains(netOverlayText(n, sess), "Traffic  not reported") {
		t.Fatal("a connection without counters reported traffic")
	}
	c := &fakeReportingClient{}
	n, clock := netTestStats(&c.fakeNetClient, 2)
	n.Client = c
	n.observe(sess, clock.at)
	if text := netOverlayText(n, sess); !strings.Contains(text, "Traffic  measuring") {
		t.Fatalf("one reading: %q", text)
	}
	// A burst more than a second ago leaves the rates.
	c.traffic = relay.LocalTraffic{MessagesIn: 500, BytesIn: 50000}
	clock.advance(500 * time.Millisecond)
	n.observe(sess, clock.at)
	for range 4 {
		c.traffic.MessagesIn += 15
		c.traffic.BytesIn += 1536
		c.traffic.MessagesOut += 1
		c.traffic.BytesOut += 512
		clock.advance(500 * time.Millisecond)
		n.observe(sess, clock.at)
	}
	if text := netOverlayText(n, sess); !strings.Contains(text, "In 3.0 KB/s 30 msg/s  Out 1.0 KB/s 2 msg/s") {
		t.Fatalf("rates: %q", text)
	}
	// The summary gives the connection's totals on its one line.
	c.traffic.BytesIn = 3 << 20
	summary := n.summary(sess, "ended")
	if !strings.Contains(summary, "; traffic in 3.0 MB (560 messages), out 2.0 KB (4 messages); ") || strings.Contains(summary, "\n") {
		t.Fatalf("summary %q", summary)
	}
}

// reportingStats is slot 0 of three human seats on a connection carrying a
// relay report sealed at tick 1240, with the newest grant at tick 1240.
func reportingStats(t *testing.T, seats []relay.HostedSeatProgress) (*onlineNetStats, *fakeReportingClient) {
	t.Helper()
	c := &fakeReportingClient{reported: true, progress: relay.HostedMatchProgress{Agreed: 1230, Sealed: 1240, Seats: seats}}
	n, _ := netTestStats(&c.fakeNetClient, len(seats))
	n.Client = c
	c.grants = []relay.LocalGrant{{Tick: 1240}}
	if _, err := n.ReadGrant(); err != nil {
		t.Fatal(err)
	}
	return n, c
}

// The relay's report drives the checksum line and each seat's row: a seat
// the relay no longer compares has left, a seat's lag is measured from the
// report's sealed tick, and the slowest playing seat holds the match once it
// is the relay's lead bound behind it.
func TestOnlineNetRelayReportRows(t *testing.T) {
	sess := netTestSession()
	n, c := reportingStats(t, []relay.HostedSeatProgress{
		{Playing: true, Acked: 1238, RTT: 38 * time.Millisecond},
		{Playing: false, Final: true, Acked: 900, RTT: 70 * time.Millisecond},
		{Playing: true, Acked: 1235, RTT: 52400 * time.Microsecond},
	})
	text := netOverlayText(n, sess)
	for _, want := range []string{"Checksums agreed through tick 1230", "State  Ping  Behind", "Player 1 (You)  playing  38 ms  2", "Player 2  left  --  --", "Player 3  playing  52 ms  5"} {
		if !strings.Contains(text, want) {
			t.Fatalf("overlay %q lacks %q", text, want)
		}
	}
	if strings.Contains(text, "Checksum sent") || strings.Contains(text, "No match report") {
		t.Fatalf("a reported match claims no report: %q", text)
	}
	// Grants newer than the report are not lag: a report up to a second old
	// would otherwise read as everyone falling 30 ticks behind.
	n.mu.Lock()
	n.received = 1275
	n.mu.Unlock()
	if text := netOverlayText(n, sess); !strings.Contains(text, "Player 1 (You)  playing  38 ms  2") || strings.Contains(text, "holding") {
		t.Fatalf("lag measured from the newest grant: %q", text)
	}
	c.progress.Agreed = 1260
	if text := netOverlayText(n, sess); !strings.Contains(text, "Checksums agreed through tick 1260") {
		t.Fatalf("the checksum line does not follow the report: %q", text)
	}
	// 29 ticks behind is inside the window; 30 is the bound, where the relay
	// waits. The seat that left, further behind, never holds the match.
	c.progress.Seats[2].Acked = 1211
	if o := n.overlay(sess); o.seats[2].holding || o.seats[1].holding || o.seats[2].cells[1] != "playing" {
		t.Fatalf("29 ticks behind: %q", o.String())
	}
	c.progress.Seats[2].Acked = 1210
	o := n.overlay(sess)
	if !o.seats[2].holding || o.seats[0].holding || o.seats[1].holding || !strings.Contains(o.String(), "Player 3  holding  52 ms  30") {
		t.Fatalf("30 ticks behind: %q", o.String())
	}
	// Every playing seat as far behind, as while the opening holds them all,
	// is no one seat holding the others back.
	c.progress.Seats[0].Acked = 1210
	if o := n.overlay(sess); o.seats[0].holding || o.seats[2].holding {
		t.Fatalf("seats tied at the bound: %q", o.String())
	}
	c.progress.Seats[0].Acked = 1238
	// An unmeasured ping reads as no value, not zero; one under a
	// millisecond reads "<1 ms"; a ping of a second or more reads in seconds.
	c.progress.Seats[0].RTT = 0
	c.progress.Seats[2].RTT = 1300 * time.Millisecond
	if text := netOverlayText(n, sess); !strings.Contains(text, "Player 1 (You)  playing  --  2") || !strings.Contains(text, "Player 3  holding  1.3 s  30") {
		t.Fatalf("unmeasured or long ping: %q", text)
	}
	c.progress.Seats[0].RTT = 300 * time.Microsecond
	if text := netOverlayText(n, sess); !strings.Contains(text, "Player 1 (You)  playing  <1 ms  2") {
		t.Fatalf("loopback ping: %q", text)
	}
}

// Without a relay report the overlay shows what this client measures and
// says the relay sent none; seats show only the shared results.
func TestOnlineNetNoRelayReport(t *testing.T) {
	sess := netTestSession()
	silent := &fakeReportingClient{}
	n, _ := netTestStats(&silent.fakeNetClient, 2)
	n.Client = silent
	plain, _ := netTestStats(&fakeNetClient{}, 2)
	for _, stats := range []*onlineNetStats{n, plain} {
		o := stats.overlay(sess)
		text := o.String()
		if !strings.Contains(text, "No match report from the relay") || strings.Contains(text, "Checksums agreed") || strings.Contains(text, "Behind") {
			t.Fatalf("no report: %q", text)
		}
		if len(o.seats) != 2 || len(o.seats[0].cells) != 2 || o.seats[0].cells[0] != "Player 1 (You)" || o.seats[1].cells[1] != "playing" {
			t.Fatalf("no-report seats: %q", text)
		}
	}
}

// netTestFont is a fixed-pitch stand-in for the side console face: 11 rows,
// glyphs 6 pixels wide and the space 7, close to the stock face's measure.
func netTestFont() *formats.FNT {
	f := &formats.FNT{Height: 11}
	for code := '!'; code <= '~'; code++ {
		f.Glyphs[code] = &formats.FNTGlyph{Width: 6, Height: 11, Bits: make([]byte, 9)}
	}
	f.Glyphs[' '] = &formats.FNTGlyph{Width: 7, Height: 11, Bits: make([]byte, 10)}
	return f
}

// netWidestName is a display name at its longest, session.OnlineNameMaxBytes.
const netWidestName = "WWWWWWWWWWWWWWWW"

// netWidestOverlay is the widest overlay seats make: the last seat local,
// every name at its longest, a seat defeated, a 999 ms ping, a seat holding
// the match and the summary at its longest.
func netWidestOverlay(seats int) onlineNetOverlay {
	o := onlineNetOverlay{
		lines: []string{
			"Online  tick 1234567  30 ticks/s  stalls 123",
			"Grants  32 buffered  jitter p95 999 ms",
			"Relay round trip  p50 999 ms  p95 999 ms",
			"Order latency  p50 999 ms  p95 999 ms",
			"In 99.9 KB/s 99 msg/s  Out 99.9 KB/s 99 msg/s",
			"Checksums agreed through tick 1234567",
		},
		heading: []string{"", "State", "Ping", "Behind"},
	}
	for slot := range seats {
		state := []string{"playing", "defeated", "holding", "left"}[slot%4]
		o.seats = append(o.seats, onlineNetSeat{cells: []string{onlineSeatName(netWidestName, slot, uint8(seats-1)), state, "999 ms", "999"}, holding: state == "holding"})
	}
	return o
}

// The overlay stays inside the world view, clear of the resource strip and
// the bottom strip, and clear of the +fps panel when both are shown, at every
// display size from 640x480 up.
func TestOnlineNetOverlayPlacement(t *testing.T) {
	font := netTestFont()
	for _, size := range [][2]int{{640, 480}, {800, 600}, {1024, 768}, {1280, 720}, {1920, 1080}} {
		w, h := size[0], size[1]
		panel := image.Rect(w-onlineFPSPanelW-onlineFPSPanelMargin, onlineFPSPanelMargin, w-onlineFPSPanelMargin, onlineFPSPanelMargin+onlineFPSPanelH)
		world := image.Rect(hud.ChromeRailX, hud.ChromeStripHeight, w, int(hud.BottomStripY(int32(h))))
		for _, fps := range []bool{false, true} {
			for _, seats := range []int{2, 10} {
				l := onlineNetMeasure(font, netWidestOverlay(seats)).place(w, h, fps, 0, camera.ChromeInsets{})
				if !l.fits || !l.backdrop.In(world) || fps && l.backdrop.Overlaps(panel) {
					t.Errorf("%dx%d fps %v, %d seats: backdrop %v in world %v, panel %v", w, h, fps, seats, l.backdrop, world, panel)
				}
			}
		}
	}
	// Ten seats sit beside the panel where it leaves room, and below it in
	// two blocks where it does not.
	if l := onlineNetMeasure(font, netWidestOverlay(10)).place(1024, 768, true, 0, camera.ChromeInsets{}); l.blocks != 1 || l.backdrop.Min.Y > hud.ChromeStripHeight+4 {
		t.Fatalf("1024x768: %d blocks at %v", l.blocks, l.backdrop)
	}
	if l := onlineNetMeasure(font, netWidestOverlay(10)).place(640, 480, true, 0, camera.ChromeInsets{}); l.blocks != 2 || l.backdrop.Min.Y < onlineFPSPanelMargin+onlineFPSPanelH {
		t.Fatalf("640x480: %d blocks at %v", l.blocks, l.backdrop)
	}
	// Visible message lines keep the top left; the overlay starts below them.
	if l := onlineNetMeasure(font, netWidestOverlay(2)).place(1024, 768, false, 94, camera.ChromeInsets{}); l.backdrop.Min.Y < 94 || !l.fits {
		t.Fatalf("below three message lines: %v", l.backdrop)
	}
}

// netCaptureCase is one synthetic network state for the overlay capture.
type netCaptureCase struct {
	name          string
	width, height int
	fps, reported bool
	humans        int
	local         uint8
	messages      int // message-column lines showing
}

// netCaptureStats is a running match's measurements for c: humans seats,
// the newest grant at tick+2, seat 2 gone, and with ten seats seat 4 holding
// the match at the relay's lead bound.
func netCaptureStats(c netCaptureCase, tick uint32, clock *netClock) *onlineNetStats {
	conn := &fakeReportingClient{reported: c.reported}
	n := newOnlineNetStats(conn, c.local, c.humans)
	n.now = clock.now
	n.received = tick + 2
	conn.progress.Agreed, conn.progress.Sealed = tick/30*30, tick+2
	for slot := range c.humans {
		seat := relay.HostedSeatProgress{Playing: true, Acked: tick + 2 - uint32(1+slot%3), RTT: time.Duration(24+17*slot) * time.Millisecond}
		switch {
		case slot == 2:
			seat = relay.HostedSeatProgress{Final: true, Acked: tick - 900}
		case slot == 4 && c.humans == 10:
			seat.Acked, seat.RTT = tick+2-onlineRelayLead, 212*time.Millisecond
		}
		conn.progress.Seats = append(conn.progress.Seats, seat)
	}
	for i := range 80 {
		n.relayTrip = append(n.relayTrip, float64(38+i%9*3))
		n.latency = append(n.latency, float64(74+i%11*3))
	}
	for i := range 150 {
		n.gaps = append(n.gaps, onlineGap{at: clock.at.Add(-time.Duration(150-i) * onlineGrantInterval), jitter: float64(i % 7)})
	}
	for range 30 {
		n.recent = append(n.recent, clock.at)
	}
	n.stalls = 1
	conn.traffic = relay.LocalTraffic{MessagesIn: 9000, BytesIn: 900000, MessagesOut: 400, BytesOut: 60000}
	n.traffic = []onlineTrafficSample{
		{at: clock.at.Add(-time.Second), LocalTraffic: conn.traffic},
		{at: clock.at, LocalTraffic: relay.LocalTraffic{MessagesIn: 9031, BytesIn: 903300, MessagesOut: 402, BytesOut: 60420}},
	}
	return n
}

// TestOnlineNetOverlayCapture draws the network overlay over a retail battle
// at the reviewed sizes, with the +fps panel's place outlined in yellow, and
// checks it clears the resource strip and that panel. A review diagnostic,
// run only when NANOLATHE_NET_CAPTURE names an output directory.
func TestOnlineNetOverlayCapture(t *testing.T) {
	out := os.Getenv("NANOLATHE_NET_CAPTURE")
	if out == "" {
		t.Skip("NANOLATHE_NET_CAPTURE is unset")
	}
	opts := Options{Root: testsupport.RetailRoot(t), Map: "ashap plateau", Seed: 1}
	cs, err := openContent(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	rng.SeedGlobal(1, 1)
	sess, cat, err := newBattleSession(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	for step := int32(1); step <= 30; step++ {
		sess.Step(step)
	}
	pal := retailPaletteForTest(t, cs)
	h, err := loadRetailBattleHUD(cs.fs, sess, cat, pal, nil, newBattleWindowContext(cs, nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range [][2]int{{640, 480}, {800, 600}, {1024, 768}} {
		if l := onlineNetMeasure(h.console, netWidestOverlay(10)).place(size[0], size[1], true, 0, camera.ChromeInsets{}); !l.fits {
			t.Errorf("%dx%d: the widest ten-seat overlay does not fit beside or below the +fps panel: %v", size[0], size[1], l.backdrop)
		}
	}
	clock := &netClock{at: time.Unix(1000, 0)}
	cases := []netCaptureCase{
		{"2seat", 640, 480, false, true, 2, 0, 0},
		{"2seat-fps", 640, 480, true, true, 2, 1, 0},
		{"10seat", 640, 480, false, true, 10, 9, 0},
		{"10seat-fps", 640, 480, true, true, 10, 9, 0},
		{"10seat-fps-800", 800, 600, true, true, 10, 0, 0},
		{"10seat-fps-1024", 1024, 768, true, true, 10, 0, 0},
		{"noreport", 640, 480, false, false, 2, 0, 0},
		{"noreport-10seat-fps", 640, 480, true, false, 10, 9, 0},
		{"2seat-messages", 640, 480, false, true, 2, 0, 2},
	}
	const tick = 5400
	for _, c := range cases {
		cam := &camera.Camera{ViewW: int32(c.width), ViewH: int32(c.height), MapW: int32(sess.World.CellW * 16), MapH: int32(sess.World.CellH * 16)}
		centerBattleStartCamera(sess, cam)
		b := &battleSession{sess: sess, cat: cat, cam: cam, hud: h, netVisible: true, fpsVisible: c.fps}
		b.multiplayer = &battleMultiplayer{net: netCaptureStats(c, tick, clock)}
		executed := sess.Clock.GlobalTick
		sess.Clock.GlobalTick = tick
		o := b.multiplayer.net.overlay(sess)
		cl, err := client.New(client.Options{Buffer: sess.Snapshot, Width: c.width, Height: c.height})
		if err != nil {
			t.Fatal(err)
		}
		cl.SetTerrain(sess.World)
		cl.SetCamera(cam)
		cl.SetPalette(pal)
		cl.SetFNT(h.console)
		cl.ConfigureMessageLines(10, 0)
		for i := range c.messages {
			cl.MessageRing().Append(fmt.Sprintf("<Player> message line %d", i+1), 1, 0, 10, tick)
		}
		l := onlineNetMeasure(h.console, o).place(c.width, c.height, c.fps, cl.MessageColumnBottom(), cam.Chrome)
		if c.messages > 0 && l.backdrop.Min.Y < cl.MessageColumnBottom() {
			t.Errorf("%s: the overlay covers the message column", c.name)
		}
		cl.SetModelFS(cs.unmappedMount)
		cl.SetUIStage(battleHUDUIStage{hud: h, battle: b})
		img := cl.ComposeFrame()
		sess.Clock.GlobalTick = executed
		if c.fps {
			x := c.width - onlineFPSPanelW - onlineFPSPanelMargin
			outline := image.Rect(x, onlineFPSPanelMargin, x+onlineFPSPanelW, onlineFPSPanelMargin+onlineFPSPanelH)
			yellow := color.RGBA{255, 220, 0, 255}
			for px := outline.Min.X; px < outline.Max.X; px++ {
				img.Set(px, outline.Min.Y, yellow)
				img.Set(px, outline.Max.Y-1, yellow)
			}
			for py := outline.Min.Y; py < outline.Max.Y; py++ {
				img.Set(outline.Min.X, py, yellow)
				img.Set(outline.Max.X-1, py, yellow)
			}
		}
		path := filepath.Join(out, "net-unit-"+c.name+".png")
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		encodeErr := png.Encode(f, img)
		if err := f.Close(); err != nil || encodeErr != nil {
			t.Fatalf("write %s: %v %v", path, encodeErr, err)
		}
		t.Logf("%s: backdrop %v, %d block(s), fits %v\n%s", path, l.backdrop, l.blocks, l.fits, o)
		if !l.fits {
			t.Errorf("%s: the overlay overlaps the resource strip, bottom strip or +fps panel", c.name)
		}
	}
}

func TestOnlineNetOverlayClearsScaledChrome(t *testing.T) {
	for _, chrome := range []camera.ChromeInsets{{Left: 257}, {Left: 257, Top: 64, Bottom: 64}} {
		cam := &camera.Camera{Chrome: chrome}
		left, top, bottom := cam.ChromeInset()
		world := image.Rect(int(left), int(top), 2560, 1440-int(bottom))
		messages := int(top) + 20 + 3*12
		for _, fps := range []bool{false, true} {
			l := onlineNetMeasure(netTestFont(), netWidestOverlay(10)).place(2560, 1440, fps, messages, chrome)
			if !l.fits || !l.backdrop.In(world) || l.backdrop.Min.Y < messages+2 {
				t.Fatalf("chrome %+v fps %v: overlay %v covers chrome or messages in %v", chrome, fps, l.backdrop, world)
			}
		}
	}
}

// Each seat row shows its player's name, the local one marked, a long one
// shortened to keep ten seats inside the world view.
func TestOnlineNetSeatNames(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	b.sess.Econ = &economy.Service{}
	b.sess.Econ.Players[0].Name, b.sess.Econ.Players[1].Name = "Alice", "Bartholomew Long"
	n, _ := netTestStats(&fakeNetClient{}, 3)
	o := n.overlay(b.sess)
	for i, want := range []string{"Alice (You)", "Bartholo..", "Player 3"} {
		if got := o.seats[i].cells[0]; got != want {
			t.Fatalf("seat %d %q, want %q", i, got, want)
		}
	}
}
