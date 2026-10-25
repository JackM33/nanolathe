package ebitenapp

import (
	"math"
	"runtime"
	"slices"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// Touch screens have no hover and no second button, so the adapter turns
// finger gestures into the mouse the rest of the host already reads
// (docs/ANDROID.md §3). This is Nanolathe host policy; retail only ever read a
// mouse.
//
//   - A tap is a left click where the finger first touched.
//   - Holding a finger still is a right click.
//   - Moving one finger is a left drag from where it first touched.
//   - Two fingers pan the battle camera and pinch to zoom through the direct
//     pan and pinch fields a trackpad fills (DESIGN_GPU_RENDERER §16.6).
//   - A finger that lands in the letterbox or on the canvas border only moves
//     the pointer there, so it edge-scrolls without clicking.
//
// Until a finger touches, and again once a real mouse moves, samples pass
// through unchanged. A touch-first host starts owned, because its idle cursor
// reads (0, 0), which would edge-scroll.
const (
	touchSlop       = 8                      // logical pixels a finger may wander and still tap or hold
	touchLongPress  = 450 * time.Millisecond // a still finger held this long right-clicks
	touchClickHold  = 70 * time.Millisecond  // synthesized press length; spans two 30 Hz services
	touchDragAnchor = 35 * time.Millisecond  // a drag's first service sees the press at its start
	touchEdgeInset  = 4                      // logical pixels inside the canvas a lifted pointer parks
)

type touchPhase uint8

const (
	touchIdle      touchPhase = iota
	touchPending              // one finger down: not yet a tap, drag or hold
	touchDragging             // one finger moving with the left button held
	touchClicking             // a tap's synthesized left press is held
	touchHeld                 // a hold right-clicked; wait for every finger to lift
	touchTwoFinger            // two fingers pan and pinch
	touchEdge                 // a finger in the letterbox or border moves the pointer only
	touchLifting              // a gesture ended; ignore fingers until all lift
)

type touchAdapter struct {
	ids     []ebiten.TouchID
	phase   touchPhase
	started bool // a touch-first host's initial ownership is settled

	// owned is set while touch, not the mouse, places the pointer. The cursor
	// position recorded when touch took over is how a real mouse reclaims it.
	owned          bool
	mouseX, mouseY int

	width, height int // current logical canvas, from Layout

	primary        ebiten.TouchID
	startX, startY int32
	startAt        time.Time
	x, y           int32
	left, right    bool
	until          time.Time // end of the current synthesized press or drag anchor
	pair           [2]ebiten.TouchID
	lastCX, lastCY float64
	lastDistance   float64
	touchFirst     bool // the host has no mouse cursor to fall back on
}

var nativeTouch = touchAdapter{touchFirst: runtime.GOOS == "android" || runtime.GOOS == "ios"}

// bounds records the logical canvas touches are reported in. Layout runs on
// the game goroutine before each Update's input poll.
func (t *touchAdapter) bounds(w, h int) {
	t.width, t.height = w, h
}

// apply polls the touch screen and, while touch owns the pointer, replaces
// the sample's pointer, buttons and gestures with the translated ones.
func (t *touchAdapter) apply(sample *sampledInput, now time.Time) {
	t.ids = ebiten.AppendTouchIDs(t.ids[:0])
	mx, my := ebiten.CursorPosition()
	t.step(sample, t.ids, ebiten.TouchPosition, mx, my, now)
}

func (t *touchAdapter) step(sample *sampledInput, ids []ebiten.TouchID, pos func(ebiten.TouchID) (int, int), mouseX, mouseY int, now time.Time) {
	if !t.started && t.touchFirst && t.width > 0 && t.height > 0 {
		t.started = true
		t.owned, t.mouseX, t.mouseY = true, mouseX, mouseY
		t.x, t.y = int32(t.width/2), int32(t.height/2)
	}
	if len(ids) == 0 && t.phase == touchIdle {
		if !t.owned {
			return
		}
		if mouseX != t.mouseX || mouseY != t.mouseY || sample.buttons != (input.MouseButtons{}) {
			t.owned = false
			return
		}
		t.park()
		sample.x, sample.y = t.x, t.y
		return
	}
	if !t.owned {
		t.owned, t.mouseX, t.mouseY = true, mouseX, mouseY
	}

	var pressed input.MouseButtons
	switch t.phase {
	case touchIdle:
		t.begin(sample, ids, pos, now)
	case touchPending:
		switch {
		case len(ids) >= 2:
			t.beginPair(sample, ids, pos)
		case !slices.Contains(ids, t.primary):
			// Lifted before moving or holding: a tap.
			t.left, pressed.Left = true, true
			t.until = now.Add(touchClickHold)
			t.phase = touchClicking
		default:
			x, y := pos(t.primary)
			if t.beyondSlop(x, y) {
				// The drag presses where the finger first touched, so a
				// selection box or slider starts under it.
				t.left, pressed.Left = true, true
				t.until = now.Add(touchDragAnchor)
				t.phase = touchDragging
			} else if now.Sub(t.startAt) >= touchLongPress {
				t.right, pressed.Right = true, true
				t.until = now.Add(touchClickHold)
				t.phase = touchHeld
			}
		}
	case touchDragging:
		if !slices.Contains(ids, t.primary) {
			t.left = false
			t.phase = touchLifting
			break
		}
		if !now.Before(t.until) {
			x, y := pos(t.primary)
			t.x, t.y = int32(x), int32(y)
		}
	case touchClicking:
		if !now.Before(t.until) {
			t.left = false
			// A finger already down again is a new gesture next poll, so a
			// quick second tap still reaches the double-click recognizer.
			t.phase = touchIdle
		}
	case touchHeld:
		if t.right && !now.Before(t.until) {
			t.right = false
		}
		if len(ids) == 0 && !t.right {
			t.phase = touchIdle
		}
	case touchTwoFinger:
		if len(ids) < 2 || !slices.Contains(ids, t.pair[0]) || !slices.Contains(ids, t.pair[1]) {
			sample.pinches = append(sample.pinches, input.PinchEvent{Ended: true})
			if len(ids) >= 2 {
				t.beginPair(sample, ids, pos)
			} else {
				t.phase = touchLifting
			}
			break
		}
		cx, cy, distance := pairGeometry(t.pair, pos)
		sample.panX += cx - t.lastCX
		sample.panY += cy - t.lastCY
		if t.lastDistance > 0 && distance > 0 && distance != t.lastDistance {
			// The battle's smooth pinch doubles a trackpad's magnification
			// (pinchSensitivity in cmd/nanolathe/trackpad.go); half the log
			// of the distance ratio keeps the map under the fingers.
			sample.pinches = append(sample.pinches, input.PinchEvent{Delta: math.Log(distance/t.lastDistance) / 2})
		}
		t.lastCX, t.lastCY, t.lastDistance = cx, cy, distance
		t.x, t.y = int32(cx), int32(cy)
	case touchEdge:
		if !slices.Contains(ids, t.primary) {
			t.phase = touchLifting
			break
		}
		x, y := pos(t.primary)
		t.x, t.y = int32(x), int32(y)
	}
	if t.phase == touchLifting && len(ids) == 0 {
		t.phase = touchIdle
	}
	if t.phase == touchIdle {
		t.park()
	}

	sample.x, sample.y = t.x, t.y
	sample.buttons = input.MouseButtons{Left: t.left, Right: t.right}
	sample.heldButtons = sample.buttons
	sample.pressedButtons = pressed
}

func (t *touchAdapter) begin(sample *sampledInput, ids []ebiten.TouchID, pos func(ebiten.TouchID) (int, int), now time.Time) {
	if len(ids) >= 2 {
		t.beginPair(sample, ids, pos)
		return
	}
	t.primary = ids[0]
	x, y := pos(t.primary)
	t.startX, t.startY = int32(x), int32(y)
	t.x, t.y = t.startX, t.startY
	t.startAt = now
	t.phase = touchPending
	if t.onBorder(t.x, t.y) {
		t.phase = touchEdge
	}
}

func (t *touchAdapter) beginPair(sample *sampledInput, ids []ebiten.TouchID, pos func(ebiten.TouchID) (int, int)) {
	t.pair = [2]ebiten.TouchID{ids[0], ids[1]}
	t.lastCX, t.lastCY, t.lastDistance = pairGeometry(t.pair, pos)
	t.x, t.y = int32(t.lastCX), int32(t.lastCY)
	t.left, t.right = false, false
	t.phase = touchTwoFinger
	sample.pinches = append(sample.pinches, input.PinchEvent{Began: true, X: t.x, Y: t.y, Positioned: true})
}

func (t *touchAdapter) beyondSlop(x, y int) bool {
	dx, dy := x-int(t.startX), y-int(t.startY)
	return dx*dx+dy*dy > touchSlop*touchSlop
}

// onBorder reports a point outside the canvas or within its edge-scroll
// border. An unknown canvas has no border.
func (t *touchAdapter) onBorder(x, y int32) bool {
	if t.width <= 0 || t.height <= 0 {
		return false
	}
	w, h := int32(t.width), int32(t.height)
	return x < touchEdgeInset || y < touchEdgeInset || x >= w-touchEdgeInset || y >= h-touchEdgeInset
}

// park keeps a lifted pointer off the border, where it would edge-scroll
// until the next touch.
func (t *touchAdapter) park() {
	if t.width <= 0 || t.height <= 0 {
		return
	}
	w, h := int32(t.width), int32(t.height)
	t.x = min(max(t.x, touchEdgeInset), w-1-touchEdgeInset)
	t.y = min(max(t.y, touchEdgeInset), h-1-touchEdgeInset)
}

func pairGeometry(pair [2]ebiten.TouchID, pos func(ebiten.TouchID) (int, int)) (cx, cy, distance float64) {
	ax, ay := pos(pair[0])
	bx, by := pos(pair[1])
	cx, cy = float64(ax+bx)/2, float64(ay+by)/2
	return cx, cy, math.Hypot(float64(bx-ax), float64(by-ay))
}
