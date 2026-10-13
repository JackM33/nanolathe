package main

import (
	"image"
	"math"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

func TestEconomyReportDemandPercentagesAndAllocationIdentity(t *testing.T) {
	f := &frame.Frame{ViewingPlayer: 0, Units: []frame.UnitView{
		{Slot: 1, AllocationSerial: 11, Owner: 0},
		{Slot: 2, AllocationSerial: 22, Owner: 0},
		{Slot: 3, AllocationSerial: 33, Owner: 1},
	}}
	flow := func(h pool.Handle, serial uint64, owner uint8, a economy.Activity, made, demand float32) frame.EconomyFlowView {
		return frame.EconomyFlowView{Player: owner, Kind: uint8(a), Unit: pool.UnitRef{Handle: h, Serial: serial},
			Produced: [2]float32{0, made}, Requested: [2]float32{0, demand}}
	}
	f.EconomyFlows = []frame.EconomyFlowView{
		flow(1, 11, 0, economy.ActivityConstruction, 0, 10),
		flow(2, 22, 0, economy.ActivityConstruction, 0, 40),
		flow(1, 9, 0, economy.ActivityConstruction, 0, 20), // old allocation: still part of the report, never a target
		flow(3, 33, 0, economy.ActivityWeapons, 0, 10),     // captured since settlement
		flow(0, 0, 0, economy.ActivityOther, 0, 20),
		flow(2, 22, 0, economy.ActivityGeneration, 75, 0),
		flow(1, 11, 0, economy.ActivityReclaim, 25, 0),
		flow(3, 33, 1, economy.ActivityWeapons, 0, 900),
	}
	r := economyReport(f, economy.Energy)
	if r.produced != 100 || r.requested != 100 || r.otherDemand != 20 {
		t.Fatalf("report mixed owners or denominators: %+v", r)
	}
	if r.rows[0].activity != economy.ActivityGeneration || r.rows[0].share != 0.75 {
		t.Fatal("production did not use its own denominator")
	}
	construction := r.rows[2]
	if construction.activity != economy.ActivityConstruction || construction.amount != 70 || construction.share != 0.7 || len(construction.contributors) != 2 || construction.contributors[0].unit.Slot != 2 {
		t.Fatalf("demand or descending current allocation navigation changed: %+v", construction)
	}
	for _, row := range r.rows {
		if row.activity == economy.ActivityWeapons && len(row.contributors) != 0 {
			t.Fatal("captured unit remained a navigation target")
		}
	}
	f.EconomyFlows = append(f.EconomyFlows, flow(1, 11, 0, economy.ActivityHealing, 0, float32(math.NaN())))
	if r := economyReport(f, economy.Energy); !r.nonfinite || r.requested != 100 {
		t.Fatal("exceptional accounting values corrupted every percentage")
	}
}

func TestEconomyPanelFitsScaledHUDAndMapsEveryControl(t *testing.T) {
	b, cl := economyPanelFixture(t)
	f, _ := b.currentSnapshot()
	b.economyPanel = economyPanelState{open: true, resource: economy.Energy}
	// Adjacent resource groups have deliberately generous text bounds. The
	// metal readout's bound reaches the energy reminder: painted badges win.
	b.hud.anchors[hud.AnchorMetalBar] = hud.Rect{X1: 215, Y1: 12, X2: 330, Y2: 14}
	b.hud.anchors[hud.AnchorEnergyBar] = hud.Rect{X1: 469, Y1: 12, X2: 580, Y2: 14}
	b.hud.anchors[hud.AnchorMetal0] = hud.Rect{X1: 216, Y1: 0, X2: 216, Y2: 0}
	b.hud.anchors[hud.AnchorEnergy0] = hud.Rect{X1: 469, Y1: 0, X2: 469, Y2: 0}
	b.hud.anchors[hud.AnchorMetalConsumed] = hud.Rect{X1: 358, Y1: 18, X2: 358, Y2: 18}
	for _, size := range [][3]int{{640, 480, 1}, {640, 480, 2}, {1280, 720, 2}, {1920, 1080, 2}, {2560, 1440, 3}} {
		cl.Resize(size[0], size[1])
		b.chromeK = int32(size[2])
		panel, region := b.economyPanelRect(), b.economyPanelRegion()
		physical := image.Rect(panel.Min.X*int(region.Scale), panel.Min.Y*int(region.Scale), panel.Max.X*int(region.Scale), panel.Max.Y*int(region.Scale))
		if !physical.In(image.Rect(0, 0, size[0], size[1])) {
			t.Fatalf("%v clipped the panel: %v", size, physical)
		}
		for _, resource := range []economy.Res{economy.Metal, economy.Energy} {
			badge := b.hud.economyPauseIndicatorRect(resource)
			x, y := int32(badge.Min.X+badge.Dx()/2)*b.chromeK, int32(badge.Min.Y+badge.Dy()/2)*b.chromeK
			if hit := b.economyPanelHit(f, x, y); hit.kind != 1 || hit.resource != resource {
				t.Fatalf("%v reminder for %v did not open its resource: %+v", size, resource, hit)
			}
		}
		for i, row := range economyReport(f, economy.Energy).rows {
			r := economyPanelRowRect(panel, i)
			for _, control := range []bool{false, true} {
				x, want := r.Min.X+40, uint8(3)
				if control {
					x, want = r.Min.X+14, 4
				}
				px, py := int32(x)*region.Scale, int32(r.Min.Y+5)*region.Scale
				hit := b.economyPanelHit(f, px, py)
				if hit.kind != want || hit.activity != row.activity || !b.economyPanelCovers(px, py) {
					t.Fatalf("%v row %d control=%t has mismatched hit %+v", size, i, control, hit)
				}
			}
		}
	}
}

func economyPanelFixture(t *testing.T) (*battleSession, *client.Client) {
	t.Helper()
	b, cl, _, first := resourceFixture(t, false)
	b.cl = cl
	second, err := b.sess.Units.Create(b.cat.Units["armcons"], 0, numeric.FixedFromInt(400), 0, numeric.FixedFromInt(400))
	if err != nil {
		t.Fatal(err)
	}
	b.sess.Step(b.sess.Clock.ScaledAnchor + 1)
	f, _ := b.currentSnapshot()
	a, _ := snapshotUnitByHandle(f, first)
	z, _ := snapshotUnitByHandle(f, second)
	f.EconomyHoldsAllowed = true
	f.EconomyFlows = []frame.EconomyFlowView{
		{Player: 0, Kind: uint8(economy.ActivityConstruction), Unit: unitViewRef(a), Requested: [2]float32{2, 20}},
		{Player: 0, Kind: uint8(economy.ActivityConstruction), Unit: unitViewRef(z), Requested: [2]float32{8, 80}},
	}
	b.hud.anchors[hud.AnchorEnergyBar] = hud.Rect{X1: 150, Y1: 2, X2: 280, Y2: 10}
	b.hud.anchors[hud.AnchorMetalBar] = hud.Rect{X1: 150, Y1: 18, X2: 280, Y2: 26}
	return b, cl
}

func economyPointer(b *battleSession, cl *client.Client, x, y int32, phase string) {
	dragInput(b, cl, x, y, input.MouseButtonLeft, phase, input.Modifiers{})
}

func TestEconomyPanelCaptureCycleAndSeparateControl(t *testing.T) {
	b, cl := economyPanelFixture(t)
	economyPointer(b, cl, 200, 6, "press")
	economyPointer(b, cl, 200, 6, "release")
	if !b.economyPanel.open || b.economyPanel.resource != economy.Energy {
		t.Fatal("bar click did not open energy")
	}
	f, _ := b.currentSnapshot()
	r := economyReport(f, economy.Energy).rows[2]
	row := economyPanelRowRect(b.economyPanelRect(), 2)
	for i := 0; i < 3; i++ {
		economyPointer(b, cl, int32(row.Min.X+40), int32(row.Min.Y+4), "press")
		economyPointer(b, cl, int32(row.Min.X+40), int32(row.Min.Y+4), "release")
		selected := b.localState().SelectedRefs()
		want := unitViewRef(r.contributors[i%2].unit)
		if len(selected) != 1 || selected[0] != want {
			t.Fatalf("cycle %d selected %v; want largest-first %v", i, selected, want)
		}
	}
	if len(b.sess.PendingHumanCommands()) != 0 {
		t.Fatal("navigation submitted an authoritative order")
	}
	economyPointer(b, cl, int32(row.Min.X+14), int32(row.Min.Y+4), "press")
	economyPointer(b, cl, int32(row.Min.X+14), int32(row.Min.Y+4), "release")
	commands := b.sess.PendingHumanCommands()
	if len(commands) != 1 || commands[0].Kind != session.HumanEconomyActivity || commands[0].EconomyActivity.Activity != economy.ActivityConstruction || !commands[0].EconomyActivity.Pause {
		t.Fatalf("row control submitted %+v", commands)
	}
	// A release outside the panel must neither navigate nor issue a world order.
	economyPointer(b, cl, int32(row.Min.X+40), int32(row.Min.Y+4), "press")
	economyPointer(b, cl, 620, 430, "release")
	if len(b.sess.PendingHumanCommands()) != 1 || b.battleState().Input.DragActive || b.economyPanel.captured {
		t.Fatal("captured release leaked to the world")
	}
}

func TestEconomyPanelOriginalBypassEscapeAndReadOnly(t *testing.T) {
	b, cl := economyPanelFixture(t)
	cl.SetEnhanced(false)
	economyPointer(b, cl, 200, 6, "press")
	economyPointer(b, cl, 200, 6, "release")
	if b.economyPanel.open {
		t.Fatal("Original presentation gained the dropdown")
	}
	cl.SetEnhanced(true)
	economyPointer(b, cl, 200, 6, "press")
	economyPointer(b, cl, 200, 6, "release")
	commandsBefore := len(b.sess.PendingHumanCommands())
	f, _ := b.currentSnapshot()
	f.EconomyHoldsAllowed = false
	b.activateEconomyPanel(f, economyPanelHit{kind: 4, activity: economy.ActivityConstruction})
	if len(b.sess.PendingHumanCommands()) != commandsBefore {
		t.Fatal("read-only rules dispatched a pause")
	}
	before := b.localState().SelectionCount()
	in := input.NewState()
	in.Kbd.SetKey(input.KeyEscape, true)
	b.handleInput(in, cl)
	if b.economyPanel.open || b.localState().SelectionCount() != before {
		t.Fatal("Escape did not close only the explanation")
	}
	// The host consumes Escape before handleInput in the production path.
	// Keep that earlier boundary from clearing selection behind the panel.
	b.economyPanel = economyPanelState{open: true, resource: economy.Energy}
	cl.Input().EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyEscape})
	b.viewerStep(0, cl)
	if b.economyPanel.open || b.localState().SelectionCount() != before {
		t.Fatal("viewer Escape did not close only the explanation")
	}
}

func TestEconomyPanelExplicitPlayAndPersistentReminder(t *testing.T) {
	b, cl := economyPanelFixture(t)
	f, _ := b.currentSnapshot()
	b.economyPanel = economyPanelState{open: true, resource: economy.Energy}
	f.EconomyHolds = []frame.EconomyHoldView{{Player: 0, Kind: uint8(economy.ActivityConstruction)}, {Player: 1, Kind: uint8(economy.ActivityWeapons)}}
	if economyPausedCount(f) != 1 {
		t.Fatal("reminder included another player's paused activities")
	}
	row := economyPanelRowRect(b.economyPanelRect(), 2)
	x, y := int32(row.Min.X+14), int32(row.Min.Y+4)
	economyPointer(b, cl, x, y, "press")
	economyPointer(b, cl, x, y, "release")
	commands := b.sess.PendingHumanCommands()
	if len(commands) != 1 || commands[0].Kind != session.HumanEconomyActivity || commands[0].EconomyActivity.Pause {
		t.Fatal("Play did not send an explicit resume")
	}
}

func TestEconomyPanelPrecedesMegamapPointer(t *testing.T) {
	b, cl := economyPanelFixture(t)
	b.setSurfaceSize(640, 480)
	b.setMegamapShown(true, cl)
	b.economyPanel = economyPanelState{open: true, resource: economy.Energy}
	r := economyPanelRowRect(b.economyPanelRect(), 2)
	for _, p := range []image.Point{{r.Min.X + 40, r.Min.Y + 4}, {620, 430}} {
		in := input.NewState()
		in.EnqueuePointer(input.PointerEvent{Kind: input.LeftDown, X: int32(p.X), Y: int32(p.Y), Buttons: input.MouseButtons{Left: true}})
		in.PublishPointer()
		sample := b.serviceMegamapPointer(in, b.pointerSample(in, 0), cl)
		if b.megamap.pressOwned[0] || sample.Pointer.Kind != input.LeftDown {
			t.Fatalf("megamap stole dropdown or outside-close press at %v", p)
		}
	}
	b.economyPanel.open, b.economyPanel.captured = false, true
	in := input.NewState()
	in.EnqueuePointer(input.PointerEvent{Kind: input.LeftUp, X: 620, Y: 430})
	in.PublishPointer()
	if sample := b.serviceMegamapPointer(in, b.pointerSample(in, 0), cl); sample.Pointer.Kind != input.LeftUp {
		t.Fatal("megamap stole the captured outside release")
	}
}
