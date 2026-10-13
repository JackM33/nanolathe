package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// These lock Nanolathe's presentation choices, not retail behavior (§16.6).
func TestLegacyPinchStopsAtNativeUntilANewGesture(t *testing.T) {
	b := zoomTestBattle()
	b.sess = &session.Session{Gameplay: gameplay.Strict31}
	b.cam.Zoom = camera.ZoomMax
	feed := func(events ...input.PinchEvent) {
		b.applyTrackpadGestures(&input.MouseState{Pinches: events}, true, 400, 250)
		for i := 0; i < 100; i++ {
			b.zoom.Step(b.cam)
		}
	}
	feed(input.PinchEvent{Began: true, Delta: -0.05})
	if b.zoom.Target(b.cam) != camera.ZoomMax {
		t.Fatal("small pinch crossed threshold")
	}
	feed(input.PinchEvent{Delta: -0.08})
	if b.cam.EffectiveZoom() != camera.ZoomUnit {
		t.Fatal("pinch did not settle at native")
	}
	for i := 0; i < 30; i++ {
		feed(input.PinchEvent{Delta: -0.4})
	}
	feed(input.PinchEvent{Delta: 1}, input.PinchEvent{Ended: true})
	if b.zoom.Target(b.cam) != camera.ZoomUnit {
		t.Fatal("continued or reversed pinch skipped native")
	}
	feed(input.PinchEvent{Began: true, Delta: -0.2, Ended: true})
	if b.cam.EffectiveZoom() != camera.ZoomUnit/4 {
		t.Fatal("fresh pinch did not reach overview")
	}
	// Multiple complete gestures in one poll retain their own boundaries.
	feed(input.PinchEvent{Began: true, Delta: 0.2, Ended: true}, input.PinchEvent{Began: true, Delta: 0.2, Ended: true})
	if b.cam.EffectiveZoom() != camera.ZoomMax {
		t.Fatal("batched gestures merged")
	}
	// A gesture spent against the limit cannot reverse into a zoom later.
	feed(input.PinchEvent{Began: true, Delta: 0.3}, input.PinchEvent{Delta: -1})
	if b.cam.EffectiveZoom() != camera.ZoomMax {
		t.Fatal("limit did not spend gesture")
	}
}

func TestPinchCancellationAndViewportOwnership(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		b := zoomTestBattle()
		b.sess = &session.Session{Gameplay: gameplay.Strict31}
		b.applyTrackpadGestures(&input.MouseState{Pinches: []input.PinchEvent{{Began: true, Delta: 0.05}}}, true, 400, 250)
		b.applyTrackpadGestures(&input.MouseState{Pinches: []input.PinchEvent{{Cancelled: cancel, Delta: 0.5}}, PanX: 200}, cancel, 400, 250)
		b.applyTrackpadGestures(&input.MouseState{Pinches: []input.PinchEvent{{Delta: 0.5}}}, true, 400, 250)
		if b.zoom.Target(b.cam) != camera.ZoomUnit {
			t.Fatal("cancelled/blocked pinch reactivated")
		}
		if !cancel && b.cam.X != 400 {
			t.Fatal("blocked scroll panned")
		}
		b.applyTrackpadGestures(&input.MouseState{Pinches: []input.PinchEvent{{Began: true, Delta: 0.2, Ended: true}}}, true, 400, 250)
		if b.zoom.Target(b.cam) != camera.ZoomMax {
			t.Fatal("new gesture stayed blocked")
		}
	}
}

func TestPinchAnchorsAtGestureStart(t *testing.T) {
	b := zoomTestBattle()
	b.cam.X, b.cam.Z = 2000, 1500
	px, py := int32(400), int32(250)
	wx, wz := b.cam.X+px, b.cam.Z+py
	b.applyTrackpadGestures(&input.MouseState{Pinches: []input.PinchEvent{{Began: true}}}, true, px, py)
	b.applyTrackpadGestures(&input.MouseState{Pinches: []input.PinchEvent{{Delta: 0.2, Ended: true}}}, true, px+100, py+50)
	for i := 0; i < 100; i++ {
		b.zoom.Step(b.cam)
	}
	z := b.cam.EffectiveZoom()
	if gx, gy := z.Project(wx-b.cam.X), z.Project(wz-b.cam.Z); gx < px-2 || gx > px || gy < py-2 || gy > py {
		t.Fatalf("pinch start anchor moved to (%d,%d)", gx, gy)
	}
}

func TestBrowserPinchUsesItsBatchedStartAnchor(t *testing.T) {
	b := zoomTestBattle()
	b.cam.X, b.cam.Z = 2000, 1500
	px, py := int32(400), int32(250)
	wx, wz := b.cam.X+px, b.cam.Z+py
	b.applyTrackpadGestures(&input.MouseState{Pinches: []input.PinchEvent{
		{Began: true, Positioned: true, X: px, Y: py}, {Delta: 0.2, Ended: true},
	}}, true, px+100, py+50)
	for range 100 {
		b.zoom.Step(b.cam)
	}
	z := b.cam.EffectiveZoom()
	if gx, gy := z.Project(wx-b.cam.X), z.Project(wz-b.cam.Z); gx < px-2 || gx > px || gy < py-2 || gy > py {
		t.Fatalf("browser pinch followed later mouse position: (%d,%d)", gx, gy)
	}
}

func TestTrackpadPanPreservesSmallDeltasAcrossZoomLevels(t *testing.T) {
	for _, zoom := range camera.ZoomSteps {
		b := zoomTestBattle()
		b.cam.Zoom = zoom
		for i := 0; i < 8; i++ {
			b.applyTrackpadGestures(&input.MouseState{PanX: 0.5, PanY: -0.25}, true, 400, 250)
		}
		if b.cam.X != 400-int32(4*camera.ZoomUnit/zoom) || b.cam.Z != 300+int32(2*camera.ZoomUnit/zoom) {
			t.Fatalf("at %v small deltas lost: (%d,%d)", zoom, b.cam.X, b.cam.Z)
		}
	}
}

func TestTrackpadCameraPassOwnership(t *testing.T) {
	for _, gate := range []string{"viewport", "classic", "unfocused", "chrome", "modal"} {
		t.Run(gate, func(t *testing.T) {
			b := newTestBattle(testCatalogON05(), testWorldON05(300, 300))
			p := settings.DefaultPresentation()
			p.ZoomStyle = settings.ZoomStepped
			b.hostPresentation = &p
			b.millisSource = &fakeMillisSource{}
			cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 640, Height: 480})
			if err != nil {
				t.Fatal(err)
			}
			cl.SetCamera(b.cam)
			cl.SetFocused(true)
			// Classic runs from the start: a modern-to-classic switch would
			// itself return the view to 1x (DESIGN_GPU_RENDERER §14.6).
			cl.SetEnhanced(gate != "classic")
			m := cl.Input().Mouse
			m.SetPosition(320, 240)
			b.viewerStep(0, cl)
			b.cam.X, b.cam.Z, b.cam.Zoom = 1000, 1000, camera.ZoomMax
			switch gate {
			case "unfocused":
				cl.SetFocused(false)
			case "chrome":
				m.SetPosition(50, 200)
			case "modal":
				b.openBattleMenu()
			}
			m.PanX, m.PanY = 20, 10
			m.Pinches = []input.PinchEvent{{Began: true, Delta: -0.2}}
			b.viewerStep(0, cl)
			if gate == "viewport" {
				if b.zoom.Target(b.cam) != camera.ZoomUnit {
					t.Fatal("viewport pinch was not dispatched")
				}
			} else if b.cam.X != 1000 || b.cam.Z != 1000 || b.zoom.Target(b.cam) != camera.ZoomMax {
				t.Fatalf("blocked camera input leaked: %+v", b.cam)
			}
		})
	}
}

func TestSmoothPinchStopsAtLockWithoutStickiness(t *testing.T) {
	for _, percent := range []int{100, 120} {
		for _, direction := range []float64{-1, 1} {
			b := preferredZoomTestBattle(percent)
			lock := b.zoomLock()
			start := camera.ZoomMax
			if direction > 0 {
				start = lock / 2
			}
			b.cam.SetZoomAbout(400, 250, start)
			feed := func(events ...input.PinchEvent) {
				b.applyTrackpadGestures(&input.MouseState{Pinches: events}, true, 400, 250)
			}
			feed(input.PinchEvent{Began: true, Delta: direction})
			if b.cam.EffectiveZoom() != lock || !b.gestures.pinchLatched {
				t.Fatal("large crossing skipped the lock")
			}
			feed(input.PinchEvent{Delta: direction}, input.PinchEvent{Delta: -direction, Ended: true})
			if b.cam.EffectiveZoom() != lock {
				t.Fatal("same gesture escaped the lock")
			}
			feed(input.PinchEvent{Began: true, Delta: direction * .01, Ended: true})
			near := b.cam.EffectiveZoom()
			if direction < 0 && (near >= lock || near <= lock*97/100) ||
				direction > 0 && (near <= lock || near >= lock*103/100) {
				t.Fatalf("small fresh gesture could not settle near lock %d: %d", lock, near)
			}
			feed(input.PinchEvent{Began: true})
			if b.cam.EffectiveZoom() != near || b.gestures.pinchLatched {
				t.Fatal("gesture start snapped a nearby factor")
			}
			feed(input.PinchEvent{Delta: -direction * .002, Ended: true})
			closer := b.cam.EffectiveZoom()
			if direction < 0 && (closer <= near || closer >= lock) ||
				direction > 0 && (closer >= near || closer <= lock) || b.gestures.pinchLatched {
				t.Fatalf("approach snapped early: %d -> %d, lock %d", near, closer, lock)
			}
			feed(input.PinchEvent{Began: true, Delta: -direction * .02})
			if b.cam.EffectiveZoom() != lock || !b.gestures.pinchLatched {
				t.Fatal("return crossing failed to stop at the lock")
			}
		}
	}
}

func TestSteppedPinchVisitsFourStopsOnePerGesture(t *testing.T) {
	b := zoomTestBattle()
	p := settings.DefaultPresentation()
	p.ZoomStyle = settings.ZoomStepped
	b.hostPresentation = &p
	b.cam.Zoom = camera.ZoomMax
	for _, want := range []camera.Zoom{camera.ZoomUnit, camera.ZoomUnit / 4, b.cam.MinZoom()} {
		b.applyTrackpadGestures(&input.MouseState{Pinches: []input.PinchEvent{{Began: true, Delta: -3}, {Delta: -3, Ended: true}}}, true, 400, 250)
		for range 100 {
			b.zoom.Step(b.cam)
		}
		if b.cam.EffectiveZoom() != want {
			t.Fatalf("stepped pinch reached %v, want %v", b.cam.EffectiveZoom(), want)
		}
	}
}
