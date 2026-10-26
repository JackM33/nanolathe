package ebitenapp

import (
	"math"
	"runtime"
	"slices"
	"sync"
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
//   - Moving one finger over the battle viewport pans the camera with the
//     middle-button drag; anywhere else (menus, sidebar, minimap) it is a left
//     drag, so sliders and lists keep working.
//   - Holding a finger still arms it, with a short vibration. Moving an armed
//     finger is a left drag from where it first touched, which draws a
//     selection box; lifting it without moving is a right click.
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
	touchLongPress  = 450 * time.Millisecond // a still finger held this long is armed
	touchClickHold  = 70 * time.Millisecond  // synthesized press length; spans two 30 Hz services
	touchDragAnchor = 35 * time.Millisecond  // a drag's first service sees the press at its start
	touchEdgeInset  = 4                      // logical pixels inside the canvas a lifted pointer parks
)

type touchPhase uint8

const (
	touchIdle      touchPhase = iota
	touchPending              // one finger down: not yet a tap, drag or hold
	touchArmed                // a held finger: a drag selects, a lift right-clicks
	touchDragging             // one finger moving with the left or middle button held
	touchClicking             // a tap's or hold's synthesized press is held
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
	middle         bool
	until          time.Time // end of the current synthesized press or drag anchor
	pair           [2]ebiten.TouchID
	lastCX, lastCY float64
	lastSpread     float64
	touchFirst     bool   // the host has no mouse cursor to fall back on
	onArm          func() // feedback when a held finger arms; nil in tests
}

var nativeTouch = touchAdapter{
	touchFirst: runtime.GOOS == "android" || runtime.GOOS == "ios",
	onArm: func() {
		ebiten.Vibrate(&ebiten.VibrateOptions{Duration: 25 * time.Millisecond, Magnitude: 0.6})
	},
}

// touchPanRegion is the battle viewport, where one finger pans the camera
// rather than dragging the left button. The battle republishes it every host
// step while the camera takes drags; it lapses once the battle stops, so
// menus and modal windows keep left drags.
type touchPanRegion struct {
	mu             sync.Mutex
	x0, y0, x1, y1 int32
	at             time.Time
}

// touchPanLapse outlasts a host step and the first steps after a paused
// battle wakes, and is short enough that a menu shown after a battle drags.
const touchPanLapse = 500 * time.Millisecond

var nativeTouchPan touchPanRegion

// SetTouchPanRegion publishes the battle viewport [x0,x1) x [y0,y1) in
// framebuffer pixels as the place where one finger pans (docs/ANDROID.md §3).
// The battle calls it every host step in which middle-button drags pan.
func SetTouchPanRegion(x0, y0, x1, y1 int32) {
	nativeTouchPan.mu.Lock()
	defer nativeTouchPan.mu.Unlock()
	nativeTouchPan.x0, nativeTouchPan.y0, nativeTouchPan.x1, nativeTouchPan.y1 = x0, y0, x1, y1
	nativeTouchPan.at = time.Now()
}

func (r *touchPanRegion) contains(x, y int32, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.at.IsZero() && now.Sub(r.at) < touchPanLapse && x >= r.x0 && x < r.x1 && y >= r.y0 && y < r.y1
}

// TouchFirstHost reports a host whose primary pointer is a touch screen, where
// a tap stands in for keys a keyboard would press (docs/ANDROID.md §3).
func TouchFirstHost() bool { return nativeTouch.touchFirst }

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
	panAt := func(x, y int32) bool { return nativeTouchPan.contains(x, y, now) }
	t.step(sample, t.ids, ebiten.TouchPosition, panAt, mx, my, now)
}

func (t *touchAdapter) step(sample *sampledInput, ids []ebiten.TouchID, pos func(ebiten.TouchID) (int, int), panAt func(x, y int32) bool, mouseX, mouseY int, now time.Time) {
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
				// The drag presses where the finger first touched, so the
				// camera, a slider or a list moves from under it.
				if panAt != nil && panAt(t.startX, t.startY) {
					t.middle, pressed.Middle = true, true
				} else {
					t.left, pressed.Left = true, true
				}
				t.until = now.Add(touchDragAnchor)
				t.phase = touchDragging
			} else if now.Sub(t.startAt) >= touchLongPress {
				t.phase = touchArmed
				if t.onArm != nil {
					t.onArm()
				}
			}
		}
	case touchArmed:
		switch {
		case len(ids) >= 2:
			t.beginPair(sample, ids, pos)
		case !slices.Contains(ids, t.primary):
			t.right, pressed.Right = true, true
			t.until = now.Add(touchClickHold)
			t.phase = touchClicking
		default:
			if x, y := pos(t.primary); t.beyondSlop(x, y) {
				// A selection box starts where the finger first touched.
				t.left, pressed.Left = true, true
				t.until = now.Add(touchDragAnchor)
				t.phase = touchDragging
			}
		}
	case touchDragging:
		if !slices.Contains(ids, t.primary) {
			t.left, t.middle = false, false
			t.phase = touchLifting
			break
		}
		if !now.Before(t.until) {
			x, y := pos(t.primary)
			t.x, t.y = int32(x), int32(y)
		}
	case touchClicking:
		if !now.Before(t.until) {
			t.left, t.right = false, false
			// A finger already down again is a new gesture next poll, so a
			// quick second tap still reaches the double-click recognizer.
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
		cx, cy, spread := pairGeometry(t.pair, pos)
		sample.panX += cx - t.lastCX
		sample.panY += cy - t.lastCY
		if t.lastSpread > 0 && spread > 0 && spread != t.lastSpread {
			// The battle's smooth pinch doubles a trackpad's magnification
			// (pinchSensitivity in cmd/nanolathe/trackpad.go); half the log
			// of the distance ratio keeps the map under the fingers. That is
			// a quarter of the log of the squared ratio, which needs no root.
			sample.pinches = append(sample.pinches, input.PinchEvent{Delta: math.Log(spread/t.lastSpread) / 4})
		}
		t.lastCX, t.lastCY, t.lastSpread = cx, cy, spread
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
	sample.buttons = input.MouseButtons{Left: t.left, Middle: t.middle, Right: t.right}
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
	t.lastCX, t.lastCY, t.lastSpread = pairGeometry(t.pair, pos)
	t.x, t.y = int32(t.lastCX), int32(t.lastCY)
	t.left, t.right, t.middle = false, false, false
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

// pairGeometry is two fingers' midpoint and the square of their distance.
func pairGeometry(pair [2]ebiten.TouchID, pos func(ebiten.TouchID) (int, int)) (cx, cy, spread float64) {
	ax, ay := pos(pair[0])
	bx, by := pos(pair[1])
	cx, cy = float64(ax+bx)/2, float64(ay+by)/2
	dx, dy := float64(bx-ax), float64(by-ay)
	return cx, cy, dx*dx + dy*dy
}
