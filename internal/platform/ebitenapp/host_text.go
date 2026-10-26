package ebitenapp

import (
	"sync"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// A host keyboard outside Ebitengine delivers text and editing keys here.
// Android's soft keyboard does: it commits whole strings, and its editing
// keys arrive as a press and release inside one frame, which polling can
// miss (docs/ANDROID.md §3). Characters join the next poll's character batch;
// each key is held for two 30 Hz services and released for one before the
// next, so repeated presses each produce a fresh edge.
const (
	hostKeyHold = 70 * time.Millisecond
	hostKeyGap  = 40 * time.Millisecond
)

type hostText struct {
	mu      sync.Mutex
	runes   []rune
	queue   []input.Key
	held    input.Key // zero while no key is held
	until   time.Time // end of the current hold or gap
	pending bool      // the gap after a release has not ended
}

var nativeHostText hostText

// hostKeys are the editing keys a host keyboard may press by name.
var hostKeys = map[string]input.Key{
	"backspace": input.KeyBackspace,
	"delete":    input.KeyDelete,
	"enter":     input.KeyEnter,
	"escape":    input.KeyEscape,
	"tab":       input.KeyTab,
}

// InjectText queues typed characters for the next input poll. It is safe to
// call from any thread.
func InjectText(text string) {
	nativeHostText.mu.Lock()
	defer nativeHostText.mu.Unlock()
	nativeHostText.runes = append(nativeHostText.runes, []rune(text)...)
}

// InjectKey queues one press of a named editing key ("backspace", "delete",
// "enter", "escape" or "tab") and reports whether the name is known. It is
// safe to call from any thread.
func InjectKey(name string) bool {
	key, ok := hostKeys[name]
	if !ok {
		return false
	}
	nativeHostText.mu.Lock()
	defer nativeHostText.mu.Unlock()
	nativeHostText.queue = append(nativeHostText.queue, key)
	return true
}

func (h *hostText) apply(sample *sampledInput, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.runes) > 0 {
		sample.characters = append(sample.characters, h.runes...)
		h.runes = h.runes[:0]
	}
	if h.held != 0 && !now.Before(h.until) {
		h.held, h.pending, h.until = 0, true, now.Add(hostKeyGap)
	}
	if h.pending && !now.Before(h.until) {
		h.pending = false
	}
	if h.held == 0 && !h.pending && len(h.queue) > 0 {
		h.held, h.until = h.queue[0], now.Add(hostKeyHold)
		h.queue = h.queue[1:]
		sample.pressedKeys[h.held] = true
	}
	if h.held != 0 {
		sample.keys[h.held] = true
		sample.heldKeys[h.held] = true
	}
}
