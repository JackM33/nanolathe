//go:build retail

package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// awaitChatLine pumps every battle until b's message column shows want.
func awaitChatLine(t *testing.T, shells []*gameShell, presenters []*client.Client, b *battleSession, want string) frame.MessageLine {
	t.Helper()
	end := time.Now().Add(10 * time.Second)
	for {
		for i, g := range shells {
			g.battle.pumpLocalMultiplayer(presenters[i])
			if mp := g.battle.multiplayer; mp == nil || mp.failure != nil {
				t.Fatalf("multiplayer stopped: %v", mp.failure)
			}
		}
		for _, line := range b.cl.MessageRing().Visible() {
			if line.Text == want {
				return line
			}
		}
		if time.Now().After(end) {
			t.Fatalf("never showed %q: %+v", want, b.cl.MessageRing().Visible())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// Two players chat through a real local relay: a line reaches the other
// seat as class 8 with its sender's slot, a one-slot line reaches only that
// slot, and the client's rate limit refuses a burst with a local message
// (DESIGN_MULTIPLAYER §12.2, [07 §5 "Chat"]). With NANOLATHE_ONLINE_CAPTURE
// naming a directory it also writes the host's frame with the lines showing.
func TestOnlineChatRelayRetail(t *testing.T) {
	shells := onlineRetailRoom(t, 2, false, []uint8{0, 0})
	presenters := make([]*client.Client, len(shells))
	for i, g := range shells {
		cl, err := client.New(client.Options{Buffer: g.battle.sess.Snapshot, Width: retailScreenW, Height: retailScreenH})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(cl.Close)
		installBattleClient(cl, g.battle)
		cl.PrepareBattlePresentation()
		presenters[i] = cl
	}
	pumpOnlineRetail(t, shells, 30, presenters[0])
	host, guest := shells[0].battle, shells[1].battle
	hostName, guestName := host.sess.Econ.Players[0].Name, host.sess.Econ.Players[1].Name
	if hostName == "" || guestName == "" || guestName != guest.sess.Econ.Players[1].Name {
		t.Fatalf("players %q and %q", hostName, guestName)
	}

	guest.commitLocalChat("hello from the guest")
	got := awaitChatLine(t, shells, presenters, host, "<"+guestName+"> hello from the guest")
	if got.Class != 8 || got.SpeakerSlot != 1 {
		t.Fatalf("received %+v, want class 8 from slot 1", got)
	}
	host.commitLocalChat("1:gl hf")
	got = awaitChatLine(t, shells, presenters, guest, "<"+hostName+"> gl hf")
	if got.Class != 8 || got.SpeakerSlot != 0 {
		t.Fatalf("received %+v, want class 8 from slot 0", got)
	}
	host.commitLocalChat("café?")
	guest.commitLocalChat("+net")
	got = awaitChatLine(t, shells, presenters, host, "<"+guestName+"> +net")
	if host.netOverlayShown() {
		t.Fatal("a received + line ran on the receiving machine")
	}
	want := []string{"<" + guestName + "> hello from the guest", "<" + hostName + "> gl hf", "Chat not sent: it has characters that cannot be sent.", "<" + guestName + "> +net"}
	lines := host.cl.MessageRing().Visible()
	if len(lines) != len(want) {
		t.Fatalf("host lines %+v", lines)
	}
	for i, line := range lines {
		if line.Text != want[i] {
			t.Fatalf("host line %d %q, want %q", i, line.Text, want[i])
		}
	}

	if out := os.Getenv("NANOLATHE_ONLINE_CAPTURE"); out != "" {
		t.Logf("logos: host %d, guest %d", host.sess.Econ.Players[0].Logo, host.sess.Econ.Players[1].Logo)
		for i, name := range []string{"chat.png", "chat-guest.png"} {
			f, err := os.Create(filepath.Join(out, os.Getenv("NANOLATHE_ONLINE_CAPTURE_PREFIX")+name))
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(f, presenters[i].ComposeFrame())
			if closeErr := f.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	// The relay's client refuses a burst past its limit before sending it.
	const rate = "Chat not sent: too many lines at once. Wait a moment."
	refused := 0
	for i := range 8 {
		host.commitLocalChat(fmt.Sprintf("burst %d", i+1))
		lines := host.cl.MessageRing().Visible()
		if lines[len(lines)-1].Text == rate {
			refused = i + 1
			break
		}
	}
	if refused < 2 || refused > 7 {
		t.Fatalf("the burst was refused at line %d", refused)
	}
	burst := fmt.Sprintf("<%s> burst %d", hostName, refused-1)
	awaitChatLine(t, shells, presenters, guest, burst)
}
