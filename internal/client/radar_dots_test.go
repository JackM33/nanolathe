package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// Modern policy: a contact can supply an attack handle without identification.
func TestRadarDotStylesAndAttackBounds(t *testing.T) {
	for _, zoom := range []camera.Zoom{camera.ZoomUnit / 4, strategicModelCut, (strategicMarkerOn + strategicModelCut) / 2, camera.ZoomUnit, camera.ZoomUnit * 2} {
		t.Run(zoom.String(), func(t *testing.T) {
			c, f := iconLayoutFixture(t)
			c.cam.Zoom, c.cam.Scale = zoom, zoom.Step()
			u := &f.Units[0]
			u.Owner, u.DirectVisibilityKnown, u.DirectlyVisible = 1, true, false
			u.X = numeric.FixedFromInt(int64(180 * int32(camera.ZoomUnit) / int32(zoom)))
			u.Y = numeric.FixedFromInt(40)
			u.Z = numeric.FixedFromInt(int64(90*int32(camera.ZoomUnit)/int32(zoom) + 20))
			p := &f.Radar.Contacts[0]
			p.Owner, p.X, p.Y, p.Z = u.Owner, u.X, u.Y, u.Z
			f.Radar.MappingLOS = 3
			for style := 0; style <= 2; style++ {
				c.SetRadarDots(style)
				c.drawStrategicMarkers(f)
				want := 1
				if style == 0 {
					want = 0
				}
				if len(c.markerArena) != want {
					t.Fatalf("style %d: %d marks, want %d", style, len(c.markerArena), want)
				}
				if _, _, hit := c.PickPresentedUnit(f, 180, 90, 0); hit {
					t.Fatalf("style %d identified a hidden unit", style)
				}
				wantHandle := pool.Handle(0)
				if style == 2 {
					wantHandle = u.Slot
				}
				if h := c.PickRadarDot(f, 180, 90, 0); h != wantHandle {
					t.Fatalf("style %d: hit %d, want %d", style, h, wantHandle)
				}
			}
			m := c.markerArena[0]
			if m.IconAtlas != nil || m.Alpha != 255 {
				t.Fatalf("dot revealed art: %+v", m)
			}
			left, top := m.X-m.Size/2, m.Y-m.Size/2
			if c.PickRadarDot(f, left, top, 0) != u.Slot || c.PickRadarDot(f, left+m.Size, top, 0) != 0 {
				t.Fatal("pick bounds diverged from drawn bounds")
			}
			if c.PickRadarDot(f, m.X, m.Y, 1) != 0 {
				t.Fatal("another viewer picked the contact")
			}
			// The damage blink hides the drawn dot on its off phase, but the
			// target holds steady through it, as the minimap's hover list
			// does [03 §3.9] "HOT list" (issue #110).
			p.BlinkSuppress = 1
			c.drawStrategicMarkers(f)
			if len(c.markerArena) != 0 {
				t.Fatal("off-phase blink drew the dot")
			}
			if c.PickRadarDot(f, m.X, m.Y, 0) != u.Slot {
				t.Fatal("off-phase blink lost the attack target")
			}
			f.Radar.BlinkPhase = 1
			c.drawStrategicMarkers(f)
			if len(c.markerArena) != 1 || c.PickRadarDot(f, m.X, m.Y, 0) != u.Slot {
				t.Fatal("on-phase blink lost contact")
			}
			p.Visible = false
			if c.PickRadarDot(f, m.X, m.Y, 0) != 0 {
				t.Fatal("lost admission retained target")
			}
			p.Visible = true
			f.MainViewRadarDots = false
			c.drawStrategicMarkers(f)
			if len(c.markerArena) != 0 || c.PickRadarDot(f, m.X, m.Y, 0) != 0 {
				t.Fatal("Strict/Community admitted the feature")
			}
		})
	}
}

func TestRadarDotOverlapUsesDrawOrderAndVisibleIconPriority(t *testing.T) {
	c, f := iconLayoutFixture(t)
	c.SetRadarDots(2)
	f.Units[0].Owner, f.Units[0].DirectVisibilityKnown = 1, true
	f.Radar.Contacts[0].Owner = 1
	p := f.Radar.Contacts[0]
	p.Handle = 8
	f.Radar.Contacts = append(f.Radar.Contacts, p)
	if h := c.PickRadarDot(f, 244, 163, 0); h != 8 {
		t.Fatalf("overlap hit %d, want last drawn 8", h)
	}
	u := f.Units[0]
	u.Slot, u.Owner, u.DirectlyVisible = 9, 0, true
	f.Units = append(f.Units, u)
	if h := c.PickRadarDot(f, 244, 163, 0); h != 0 {
		t.Fatal("dot picked through visible icon")
	}
	if h, _, ok := c.PickPresentedUnit(f, 244, 163, 0); !ok || h != 9 {
		t.Fatal("visible icon lost priority")
	}
	f.Units = f.Units[:1]
	c.SetEnhanced(false)
	if c.PickRadarDot(f, 244, 163, 0) != 0 {
		t.Fatal("Classic gained attackable dots")
	}
	c.SetEnhanced(true)
	// No art means no drawn dot and therefore no contact hit.
	c.SetStrategicBlipArt(nil)
	if c.PickRadarDot(f, 244, 163, 0) != 0 {
		t.Fatal("undrawn contact was pickable")
	}
}
