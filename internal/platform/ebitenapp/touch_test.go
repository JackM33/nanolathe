package ebitenapp

import (
	"math"
	"testing"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// touchScript drives the adapter with fake fingers on a 640x480 canvas whose
// battle viewport, where one finger pans, starts at x 130.
type touchScript struct {
	t       *testing.T
	adapter touchAdapter
	fingers map[ebiten.TouchID][2]int
	now     time.Time
	armed   int
}

func newTouchScript(t *testing.T, touchFirst bool) *touchScript {
	s := &touchScript{t: t, fingers: map[ebiten.TouchID][2]int{}, now: time.Unix(0, 0)}
	s.adapter.touchFirst = touchFirst
	s.adapter.onArm = func() { s.armed++ }
	s.adapter.bounds(640, 480)
	return s
}

func (s *touchScript) poll(after time.Duration) sampledInput {
	s.now = s.now.Add(after)
	var ids []ebiten.TouchID
	for id := ebiten.TouchID(0); id < 4; id++ {
		if _, ok := s.fingers[id]; ok {
			ids = append(ids, id)
		}
	}
	sample := sampledInput{}
	pos := func(id ebiten.TouchID) (int, int) { return s.fingers[id][0], s.fingers[id][1] }
	panAt := func(x, y int32) bool { return x >= 130 }
	s.adapter.step(&sample, ids, pos, panAt, 0, 0, s.now)
	return sample
}

func (s *touchScript) want(sample sampledInput, x, y int32, held, pressed input.MouseButtons) {
	s.t.Helper()
	if sample.x != x || sample.y != y || sample.heldButtons != held || sample.pressedButtons != pressed {
		s.t.Fatalf("sample at (%d,%d) held %+v pressed %+v; want (%d,%d) held %+v pressed %+v",
			sample.x, sample.y, sample.heldButtons, sample.pressedButtons, x, y, held, pressed)
	}
}

func TestTouchGesturesTranslateToMouse(t *testing.T) {
	none, left, right := input.MouseButtons{}, input.MouseButtons{Left: true}, input.MouseButtons{Right: true}
	middle := input.MouseButtons{Middle: true}

	t.Run("tap clicks where the finger landed", func(t *testing.T) {
		s := newTouchScript(t, false)
		s.fingers[0] = [2]int{100, 100}
		s.want(s.poll(0), 100, 100, none, none)
		s.fingers[0] = [2]int{103, 101} // within slop
		s.want(s.poll(16*time.Millisecond), 100, 100, none, none)
		delete(s.fingers, 0)
		s.want(s.poll(16*time.Millisecond), 100, 100, left, left)
		s.want(s.poll(16*time.Millisecond), 100, 100, left, none)
		s.want(s.poll(touchClickHold), 100, 100, none, none)
	})

	t.Run("one finger pans the battle viewport with a middle drag", func(t *testing.T) {
		s := newTouchScript(t, false)
		s.fingers[0] = [2]int{300, 200}
		s.poll(0)
		s.fingers[0] = [2]int{330, 200}
		s.want(s.poll(16*time.Millisecond), 300, 200, middle, middle)
		s.fingers[0] = [2]int{360, 220}
		s.want(s.poll(touchDragAnchor), 360, 220, middle, none)
		delete(s.fingers, 0)
		s.want(s.poll(16*time.Millisecond), 360, 220, none, none)
	})

	t.Run("one finger outside the viewport drags the left button", func(t *testing.T) {
		s := newTouchScript(t, false)
		s.fingers[0] = [2]int{60, 100}
		s.poll(0)
		s.fingers[0] = [2]int{60, 140}
		s.want(s.poll(16*time.Millisecond), 60, 100, left, left)
		delete(s.fingers, 0)
		s.want(s.poll(touchDragAnchor), 60, 100, none, none)
	})

	t.Run("a held finger lifted in place right-clicks", func(t *testing.T) {
		s := newTouchScript(t, false)
		s.fingers[0] = [2]int{200, 200}
		s.poll(0)
		s.want(s.poll(touchLongPress), 200, 200, none, none)
		if s.armed != 1 {
			t.Fatalf("armed %d times; want one vibration", s.armed)
		}
		delete(s.fingers, 0)
		s.want(s.poll(16*time.Millisecond), 200, 200, right, right)
		s.want(s.poll(touchClickHold), 200, 200, none, none)
	})

	t.Run("a held finger that moves draws a selection box", func(t *testing.T) {
		s := newTouchScript(t, false)
		s.fingers[0] = [2]int{200, 200}
		s.poll(0)
		s.poll(touchLongPress)
		s.fingers[0] = [2]int{260, 240}
		s.want(s.poll(16*time.Millisecond), 200, 200, left, left)
		s.fingers[0] = [2]int{300, 260}
		s.want(s.poll(touchDragAnchor), 300, 260, left, none)
		delete(s.fingers, 0)
		s.want(s.poll(16*time.Millisecond), 300, 260, none, none)
	})

	t.Run("two fingers pan and pinch without clicking", func(t *testing.T) {
		s := newTouchScript(t, false)
		s.fingers[0], s.fingers[1] = [2]int{100, 100}, [2]int{200, 100}
		began := s.poll(0)
		s.want(began, 150, 100, none, none)
		if len(began.pinches) != 1 || !began.pinches[0].Began || !began.pinches[0].Positioned || began.pinches[0].X != 150 {
			t.Fatalf("pinch start %+v; want one positioned start at x 150", began.pinches)
		}
		s.fingers[0], s.fingers[1] = [2]int{110, 100}, [2]int{230, 100}
		moved := s.poll(16 * time.Millisecond)
		if moved.panX != 20 || moved.panY != 0 {
			t.Fatalf("pan (%v,%v); want (20,0)", moved.panX, moved.panY)
		}
		if len(moved.pinches) != 1 || math.Abs(moved.pinches[0].Delta-math.Log(1.2)/2) > 1e-9 {
			t.Fatalf("pinch %+v; want delta log(1.2)/2", moved.pinches)
		}
		delete(s.fingers, 1)
		ended := s.poll(16 * time.Millisecond)
		if len(ended.pinches) != 1 || !ended.pinches[0].Ended || ended.heldButtons != none {
			t.Fatalf("lift %+v; want a pinch end and no buttons", ended)
		}
		delete(s.fingers, 0)
		s.want(s.poll(touchLongPress), 170, 100, none, none)
	})
}

func TestTouchPointerAvoidsEdgeScroll(t *testing.T) {
	s := newTouchScript(t, true)
	// A touch-first host never shows its (0, 0) idle cursor.
	s.want(s.poll(0), 320, 240, input.MouseButtons{}, input.MouseButtons{})
	// A finger in the letterbox moves the pointer there and never clicks.
	s.fingers[0] = [2]int{-30, 200}
	s.want(s.poll(0), -30, 200, input.MouseButtons{}, input.MouseButtons{})
	s.want(s.poll(touchLongPress), -30, 200, input.MouseButtons{}, input.MouseButtons{})
	// Lifted, it parks inside the border.
	delete(s.fingers, 0)
	s.want(s.poll(16*time.Millisecond), touchEdgeInset, 200, input.MouseButtons{}, input.MouseButtons{})
}

func TestTouchYieldsToAMovedMouse(t *testing.T) {
	s := newTouchScript(t, false)
	if sample := s.poll(0); sample.x != 0 || sample.y != 0 || sample.heldButtons != (input.MouseButtons{}) || s.adapter.owned {
		t.Fatalf("untouched sample changed: %+v", sample)
	}
	s.fingers[0] = [2]int{50, 60}
	s.poll(0)
	delete(s.fingers, 0)
	s.poll(touchClickHold)
	s.poll(touchClickHold)
	sample := sampledInput{x: 7, y: 9}
	s.adapter.step(&sample, nil, nil, nil, 7, 9, s.now)
	if sample.x != 7 || sample.y != 9 || s.adapter.owned {
		t.Fatalf("moved mouse at (%d,%d) owned %v; want the mouse's (7,9) and touch released", sample.x, sample.y, s.adapter.owned)
	}
}
