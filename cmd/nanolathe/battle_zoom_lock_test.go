package main

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

func preferredZoomTestBattle(percent int) *battleSession {
	b := zoomTestBattle()
	p := settings.DefaultPresentation()
	p.ZoomLockPercent = percent
	b.hostPresentation = &p
	b.syncCameraControls()
	return b
}

// Saved Steps must not turn the first policy sync into a zoom gesture. Native
// entry and explicit framing are separate from the preferred stop (§16.8).
func TestSteppedZoomLockPreservesEntryFraming(t *testing.T) {
	for _, renderer := range []string{"classic", "modern"} {
		for _, entry := range []camera.Zoom{0, camera.ZoomUnit, camera.ZoomMax} {
			t.Run(fmt.Sprintf("%s-entry%d", renderer, entry), func(t *testing.T) {
				b := zoomTestBattle()
				p := settings.DefaultPresentation()
				p.ZoomStyle, p.ZoomLockPercent = settings.ZoomStepped, 120
				b.hostPresentation = &p
				b.followExecutor(renderer == "modern")
				before := b.cam.PresentationView()
				b.syncCameraControls() // The client installs policy before entry zoom.
				if b.cam.EffectiveZoom() != camera.ZoomUnit || b.cam.PresentationView() != before {
					t.Fatal("saved Steps moved the composed native camera")
				}
				applyEntryZoom(Options{Renderer: renderer, Zoom: entry}, b)
				want := entry
				if want == 0 {
					want = camera.ZoomUnit
				}
				b.syncCameraControls()
				if b.cam.EffectiveZoom() != want || b.cam.EffectiveScale() != want.Step() {
					t.Fatalf("entry factor = %d, scale = %d, want %d", b.cam.EffectiveZoom(), b.cam.EffectiveScale(), want)
				}
			})
		}
	}
	// A restart/capture's fractional factor is explicit framing too.
	b := preferredZoomTestBattle(120)
	b.hostPresentation.ZoomStyle = settings.ZoomStepped
	b.cameraStyleSeen = false
	b.followExecutor(true)
	b.cam.SetZoomAbout(500, 300, 1536)
	before := b.cam.PresentationView()
	b.syncCameraControls()
	if b.cam.EffectiveZoom() != 1536 || b.cam.PresentationView() != before {
		t.Fatal("first Steps sync replaced restart framing")
	}
}

func TestSteppedZoomNormalizationRequiresEnhancedTransition(t *testing.T) {
	for _, enhanced := range []bool{false, true} {
		b := preferredZoomTestBattle(120)
		b.followExecutor(enhanced)
		b.hostPresentation.ZoomStyle = settings.ZoomStepped
		b.syncCameraControls()
		want := camera.ZoomUnit
		if enhanced {
			want = 1229
		}
		if b.cam.EffectiveZoom() != want {
			t.Fatalf("enhanced=%v: Steps transition factor = %d, want %d", enhanced, b.cam.EffectiveZoom(), want)
		}
		if !enhanced {
			b.toggleViewScale(false)
			if b.cam.EffectiveZoom() != camera.ZoomMax || b.cam.EffectiveScale() != camera.ViewScaleDetail {
				t.Fatal("Classic F9 lost its exact detail scale")
			}
		}
	}
}

func TestClassicZoomPolicyChangeKeepsNativeScaleOnSmallMap(t *testing.T) {
	b := zoomTestBattle()
	b.cam.ViewW, b.cam.ViewH, b.cam.MapW, b.cam.MapH = 1920, 1080, 1600, 3968
	b.sess = &session.Session{Gameplay: gameplay.Modern}
	b.followExecutor(false)
	b.syncCameraControls()
	b.sess.Gameplay = gameplay.Strict31
	b.syncCameraControls()
	if b.cam.MinZoom() <= camera.ZoomUnit {
		t.Fatal("fixture does not exercise the legacy viewport floor")
	}
	if b.cam.EffectiveZoom() != camera.ZoomUnit || b.cam.EffectiveScale() != camera.ViewScaleNative {
		t.Fatal("Strict's viewport floor replaced Classic's exact native scale")
	}
}

func TestPinchPreferredZoomLockAndDeparture(t *testing.T) {
	b := preferredZoomTestBattle(120)
	const lock camera.Zoom = 1229
	b.cam.SetZoomAbout(500, 300, camera.ZoomMax)
	apply := func(event input.PinchEvent) {
		b.applyTrackpadGestures(&input.MouseState{Pinches: []input.PinchEvent{event}}, true, 500, 300)
	}
	apply(input.PinchEvent{Began: true, Delta: -.6})
	if b.cam.EffectiveZoom() != lock || !b.gestures.pinchLatched {
		t.Fatal("large pinch missed the configured stop")
	}
	apply(input.PinchEvent{Delta: -.5, Ended: true})
	if b.cam.EffectiveZoom() != lock {
		t.Fatal("a held pinch passed the configured stop")
	}
	apply(input.PinchEvent{Began: true, Delta: -.01, Ended: true})
	if got := b.cam.EffectiveZoom(); got >= lock || got <= lock*97/100 {
		t.Fatalf("a small new pinch did not leave the lock immediately: %d", got)
	}
	apply(input.PinchEvent{Began: true, Delta: -.4, Ended: true})
	if got := b.cam.EffectiveZoom(); got >= lock*85/100 || got <= b.cam.MinZoom() {
		t.Fatalf("a deliberate fresh pinch did not leave the lock: %d", got)
	}
	if b.zoom.LockZoom != lock {
		t.Fatal("direct pinch lost the wheel's configured stop")
	}
}

func TestPinchPreferredLockLeavesNativeFree(t *testing.T) {
	b := preferredZoomTestBattle(150)
	b.cam.SetZoomAbout(500, 300, 819)
	b.applyTrackpadGestures(&input.MouseState{Pinches: []input.PinchEvent{{Began: true, Delta: .15, Ended: true}}}, true, 500, 300)
	if got := b.cam.EffectiveZoom(); got <= camera.ZoomUnit || got >= b.zoomLock()*85/100 || b.gestures.pinchLatched {
		t.Fatalf("native retained its old pinch detent: %d", got)
	}
}

func TestSteppedPinchUsesPreferredStop(t *testing.T) {
	b := preferredZoomTestBattle(120)
	b.hostPresentation.ZoomStyle = settings.ZoomStepped
	b.syncCameraControls()
	b.cam.SetZoomAbout(500, 300, camera.ZoomMax)
	b.applyTrackpadGestures(&input.MouseState{Pinches: []input.PinchEvent{{Began: true, Delta: -.2, Ended: true}}}, true, 500, 300)
	if got := b.zoom.Target(b.cam); got != 1229 {
		t.Fatalf("stepped pinch chose %d instead of preferred stop", got)
	}
}

func TestPreferredLockChangeRetiresOldInput(t *testing.T) {
	b := preferredZoomTestBattle(120)
	b.gestures.pinchActive = true
	b.millisSource = &fakeMillisSource{}
	b.wheelZoom(400, 250, .7)
	b.hostPresentation.ZoomLockPercent = 150
	b.syncCameraControls()
	b.wheelZoom(400, 250, .3)
	if b.gestures.pinchActive || b.zoom.Target(b.cam) != camera.ZoomUnit || b.zoom.LockZoom != 1536 {
		t.Fatal("changing the lock retained an old pinch or wheel fraction")
	}
}

func TestPreferredLockOverviewAndLegacyBoundary(t *testing.T) {
	for _, from := range []camera.Zoom{921, 1126} {
		b := preferredZoomTestBattle(120)
		b.cam.SetZoomAbout(500, 300, from)
		b.toggleViewScale(true)
		b.toggleViewScale(true)
		want := from
		if from == 1126 {
			want = 1229
		}
		if b.cam.EffectiveZoom() != want {
			t.Fatalf("overview return from %d = %d, want %d", from, b.cam.EffectiveZoom(), want)
		}
	}
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Community39} {
		b := preferredZoomTestBattle(120)
		b.sess = &session.Session{Gameplay: mode}
		b.hostPresentation.Overview = settings.OverviewMegamap
		b.syncCameraControls()
		if b.zoomLock() != camera.ZoomUnit || b.zoom.LockZoom != camera.ZoomUnit {
			t.Fatalf("%s adopted the Modern lock", mode)
		}
	}
	b := preferredZoomTestBattle(120)
	b.sess = &session.Session{Gameplay: gameplay.Community39}
	b.syncCameraControls()
	if b.zoomLock() != 1229 || b.zoom.LockZoom != 1229 {
		t.Fatal("Community camera zoom ignored the host's preferred lock")
	}
	b = preferredZoomTestBattle(1)
	if b.zoomLock() != b.cam.MinZoom() || b.hostPresentation.ZoomLockPercent != 1 {
		t.Fatal("map floor did not clamp the active lock independently of the preference")
	}
}
