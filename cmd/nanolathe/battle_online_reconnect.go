package main

// What an online battle shows while a connection is lost (DESIGN_MULTIPLAYER
// §16.9): the relay holds the match for a dropped seat, the dropped client
// resumes on its own, and a battle that resumed behind, or rejoined after
// this game stopped, runs the ticks it is owed at full speed. A banner over
// the world view says which is happening; the message column records each
// change. None of it reaches a tick.

import (
	"fmt"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/client"
)

// onlineCatchUp is the driver's fast-forward (lockstep.LocalDriver).
type onlineCatchUp interface {
	FastForward(through uint32)
	FastForwarding() (uint32, bool)
}

func (m *battleMultiplayer) catchUp() onlineCatchUp {
	if m == nil {
		return nil
	}
	c, _ := m.driver.(onlineCatchUp)
	return c
}

// fastForwarding reports a catch-up in progress and the tick it runs to.
func (m *battleMultiplayer) fastForwarding() (uint32, bool) {
	if c := m.catchUp(); c != nil {
		return c.FastForwarding()
	}
	return 0, false
}

// onlineSlotName is how the battle names a slot's player.
func (b *battleSession) onlineSlotName(slot int) string {
	if name := onlinePlayerName(b.sess, slot); name != "" {
		return name
	}
	return onlineDefaultName(slot)
}

// followOnlineConnection reads the hosted connection before each pump: a
// lost connection and its resume, which starts a catch-up to the tick the
// relay had sealed, and other players dropping and coming back.
func (b *battleSession) followOnlineConnection() {
	m := b.multiplayer
	if m == nil || m.hosted == nil {
		return
	}
	conn := m.hosted.Connection()
	if conn.Reconnecting && !m.reconnecting {
		m.reconnecting = true
		b.onlineNotice("Connection to the server lost. Reconnecting...")
	}
	if conn.Resumes != m.resumes {
		if m.reconnecting {
			b.onlineNotice("Reconnected.")
		}
		m.resumes, m.reconnecting = conn.Resumes, false
		if c := m.catchUp(); c != nil {
			c.FastForward(conn.Sealed)
		}
	}
	if _, fast := m.fastForwarding(); fast != m.catchingUp {
		m.catchingUp = fast
		if !fast {
			b.onlineNotice("Caught up with the game.")
		}
	}
	p, ok := m.hosted.Progress()
	if !ok || conn.Reconnecting {
		return
	}
	now := time.Now()
	for slot, seat := range p.Seats {
		if slot >= len(m.awaySince) || b.sess == nil || slot == int(b.sess.LocalOwner) {
			continue
		}
		away := seat.Playing && !seat.Connected
		switch {
		case away && m.awaySince[slot].IsZero():
			m.awaySince[slot] = now
			b.onlineNotice(b.onlineSlotName(slot) + " lost their connection. The game waits for them.")
		case !away && !m.awaySince[slot].IsZero():
			m.awaySince[slot] = time.Time{}
			if seat.Playing {
				b.onlineNotice(b.onlineSlotName(slot) + " is back.")
			}
		}
	}
}

// onlineConnectionBanner is the banner's line, or "" when play is normal.
func (b *battleSession) onlineConnectionBanner(now time.Time) string {
	m := b.multiplayer
	if m == nil || m.hosted == nil || b.sess == nil || b.sess.Clock == nil {
		return ""
	}
	if conn := m.hosted.Connection(); conn.Reconnecting {
		return "Connection lost. Reconnecting... " + onlineClock(now.Sub(conn.Since))
	}
	if through, fast := m.fastForwarding(); fast && through > 0 {
		return fmt.Sprintf("Catching up with the game: %d%%", uint64(b.sess.Clock.GlobalTick)*100/uint64(through))
	}
	for slot, since := range m.awaySince {
		if !since.IsZero() {
			return fmt.Sprintf("Waiting for %s to reconnect... %s of %s", b.onlineSlotName(slot), onlineClock(now.Sub(since)), onlineClock(onlineRejoinGrace))
		}
	}
	return ""
}

// onlineRejoinGrace is how long the relay holds a dropped player, as the
// relay's own policy states it (internal/relay hosted_resume.go).
const onlineRejoinGrace = 3 * time.Minute

// onlineClock is a wait as minutes and seconds.
func onlineClock(d time.Duration) string {
	s := int(max(d, 0) / time.Second)
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// drawOnlineConnectionBanner centres the banner over the world view, below
// the quick buttons, in the interface face on the GUI's black with the
// buttons' light outline. It draws in the strips' virtual surface, so it
// scales with the rest of the touch interface.
func (h *retailBattleHUD) drawOnlineConnectionBanner(c *client.Client, b *battleSession) {
	if h == nil || c == nil || b == nil {
		return
	}
	font := h.guiFont
	if font == nil {
		font = h.console
	}
	text := b.onlineConnectionBanner(time.Now())
	if font == nil || text == "" {
		return
	}
	sw, sh := b.surfaceSize()
	width, _ := b.railRegion().VirtualSize(int(sw), int(sh))
	w, lineH := client.MeasureText(font, text), int(font.Height)
	const pad = 8
	left := onlineBannerLeft
	x := left + (width-left-w)/2
	y := quickButtonTop + quickButtonH + 2*quickButtonGap + pad
	c.UIFillRect(x-pad, y-pad, w+2*pad, lineH+2*pad, h.guiColor(7))
	c.UIFillRect(x-pad+1, y-pad+1, w+2*pad-2, lineH+2*pad-2, h.guiColor(0))
	c.UIText(font, text, x, y, h.guiColor(15))
}

// onlineBannerLeft is the side panel's width in the strips' surface, which
// the banner centres beside.
const onlineBannerLeft = 128
