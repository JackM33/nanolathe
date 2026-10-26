package ebitenapp

import (
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/input"
)

func TestHostTextSeparatesRepeatedKeys(t *testing.T) {
	var h hostText
	h.runes = []rune("ab")
	h.queue = []input.Key{input.KeyBackspace, input.KeyBackspace}
	now := time.Unix(0, 0)
	poll := func(after time.Duration) sampledInput {
		now = now.Add(after)
		var sample sampledInput
		h.apply(&sample, now)
		return sample
	}
	first := poll(0)
	if string(first.characters) != "ab" || !first.pressedKeys[input.KeyBackspace] || !first.heldKeys[input.KeyBackspace] {
		t.Fatalf("first poll %q pressed %v held %v; want the text and a backspace press", string(first.characters), first.pressedKeys[input.KeyBackspace], first.heldKeys[input.KeyBackspace])
	}
	if held := poll(hostKeyHold / 2); held.pressedKeys[input.KeyBackspace] || !held.heldKeys[input.KeyBackspace] || len(held.characters) != 0 {
		t.Fatalf("mid-hold poll should keep holding without a new edge or text")
	}
	if gap := poll(hostKeyHold); gap.heldKeys[input.KeyBackspace] {
		t.Fatalf("the hold should release before the next press")
	}
	if second := poll(hostKeyGap); !second.pressedKeys[input.KeyBackspace] {
		t.Fatalf("the second backspace should press after the gap")
	}
}

func TestHostShiftHoldsShift(t *testing.T) {
	h := hostText{shift: true}
	var sample sampledInput
	h.apply(&sample, time.Unix(0, 0))
	if !sample.modifiers.Shift || !sample.heldModifiers.Shift || !sample.keys[input.KeyShift] || !sample.heldKeys[input.KeyShift] {
		t.Fatalf("a held host Shift should hold the modifier and the key")
	}
}
