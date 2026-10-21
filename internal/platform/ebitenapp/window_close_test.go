package ebitenapp

import (
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/client"
)

// The close button is latched from its one-tick report and handed to the
// client once; the window waits for the game's own exit request, and closes
// anyway when the grace period passes (docs/DESIGN_SESSIONS_AI_SAVE.md §5,
// "Autosave, quick save and quick load").
func TestWindowCloseIsHandedToTheGameThenForced(t *testing.T) {
	c, err := client.New(client.Options{Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	pressed := false
	saved := windowBeingClosed
	windowBeingClosed = func() bool { return pressed }
	t.Cleanup(func() { windowBeingClosed = saved })
	a := &app{c: c}
	start := time.Unix(1000, 0)

	a.serviceWindowCloseAt(start)
	if c.WindowClosing() || a.exitPending {
		t.Fatal("a closed window was reported without a close request")
	}
	pressed = true
	a.serviceWindowCloseAt(start)
	pressed = false // Ebitengine reports the press for one tick only
	if !c.WindowClosing() || a.exitPending {
		t.Fatalf("close request: client told %t, exit forced %t", c.WindowClosing(), a.exitPending)
	}
	a.serviceWindowCloseAt(start.Add(windowCloseGrace))
	if a.exitPending {
		t.Fatal("the exit was forced before the grace period passed")
	}
	a.serviceWindowCloseAt(start.Add(windowCloseGrace + time.Millisecond))
	if !a.exitPending {
		t.Fatal("a game that never answered kept the window open")
	}
}
