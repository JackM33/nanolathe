//go:build retail

package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// The stock TABMENU.GUI, SHARE.GUI and ALLIES.GUI author every control the
// three windows drive, with the kinds and defaults the screens rely on
// [07 "Tab options menu and manual exit"][07 R-HUD-03 §9][05 R-SHARE-01 §5].
func TestSharingWindowsRetail(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatalf("mount retail: %v", err)
	}
	defer fs.Close()
	load := func(path string) *gui.Window {
		w, err := gui.LoadWithTranslation(fs, path, nil)
		if err != nil {
			t.Fatalf("load %s: %v", path, err)
		}
		return w
	}
	need := func(w *gui.Window, name string, kind gui.Kind) int {
		i := w.GadgetIndex(name)
		if i <= 0 || w.Gadgets[i].Kind != kind {
			t.Fatalf("%s has no kind-%d %s", w.Name, kind, name)
		}
		return i
	}
	tab := load(tabMenuGUI)
	for _, name := range []string{"OPTIONS", "SHARE", "ALLIES", "CONTROL"} {
		need(tab, name, gui.KindButton)
	}
	// CANCEL is authored inactive, and both defaults name it.
	if i := need(tab, "CANCEL", gui.KindButton); tab.Gadgets[i].Active != 0 || tab.EnterDefaultIndex() != i || tab.EscapeDefaultIndex() != i {
		t.Fatal("TABMENU's CANCEL is not the inactive default")
	}
	// The strip is authored 33 rows above the surface bottom.
	if tab.Gadgets[0].Rect.RawY != -tab.Rect.H {
		t.Fatalf("TABMENU authored at y %d for a %d-row strip", tab.Gadgets[0].Rect.RawY, tab.Rect.H)
	}

	share := load(shareGUI)
	list := need(share, "PLYRLIST", gui.KindListBox)
	for _, name := range []string{"METAL", "ENERGY"} {
		i := need(share, name, gui.KindScrollBar)
		if share.Gadgets[i].Attribs&1 == 0 {
			t.Fatalf("SHARE's %s is not a horizontal slider", name)
		}
	}
	need(share, "METAL#", gui.KindLabel)
	need(share, "ENERGY#", gui.KindLabel)
	for _, name := range []string{"SHARUNIT", "MAPINFO"} {
		if i := need(share, name, gui.KindButton); share.Gadgets[i].Stages != 2 {
			t.Fatalf("SHARE's %s is not a two-stage box", name)
		}
	}
	if share.EnterDefaultIndex() != need(share, "OK", gui.KindButton) || share.EscapeDefaultIndex() != need(share, "CANCEL", gui.KindButton) {
		t.Fatal("SHARE's defaults are not OK and CANCEL")
	}
	// The list's bar shares its assoc.
	bar := need(share, "SLIDER", gui.KindScrollBar)
	if share.Gadgets[bar].Assoc != share.Gadgets[list].Assoc {
		t.Fatal("SHARE's list bar is not associated with PLYRLIST")
	}
	// The dialog's override leaves a travel of the built bar's width less its
	// height, inside the bar.
	common, err := formats.LoadGAFFile(fs, "anims/commongui.gaf")
	if err != nil {
		t.Fatal(err)
	}
	sliders, _ := common.Find("SLIDERS")
	before := len(share.Gadgets)
	metal := buildShareSlider(share, "METAL", sliders)
	built := share.Gadgets[metal]
	if len(share.Gadgets) != before+2 || int32(built.Range) != built.Rect.W-built.Rect.H || int32(built.KnobSize) != built.Rect.H || built.Range < 2 {
		t.Fatalf("built METAL: %d gadgets, range %d, knob %d, rect %+v", len(share.Gadgets)-before, built.Range, built.KnobSize, built.Rect)
	}

	allies := load(alliesGUI)
	for r := range frame.PlayerRowSlots {
		need(allies, fmt.Sprintf("PLAYER%d", r), gui.KindLabel)
		need(allies, fmt.Sprintf("LOGO%d", r), gui.KindSurface)
	}
	var ally, team int
	for _, g := range allies.Gadgets {
		switch gui.CallbackName(g.Name) {
		case "ALLYx":
			ally++
		case "TEAMICONSx":
			team++
		}
	}
	if ally != frame.PlayerRowSlots || team != frame.PlayerRowSlots {
		t.Fatalf("ALLIES authors %d ally and %d team templates", ally, team)
	}
	need(allies, "VICTORY", gui.KindButton)
	if ok := need(allies, "OK", gui.KindButton); allies.EnterDefaultIndex() != ok || allies.EscapeDefaultIndex() != ok {
		t.Fatal("ALLIES's defaults are not OK")
	}
	art, err := formats.LoadGAFFile(fs, "anims/allies.gaf")
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := art.Find("ALLYx"); !ok || len(e.Frames) != 4 {
		t.Fatal("allies.gaf has no four-stage ALLYx")
	}
	if e, ok := art.Find("TEAMICONSx"); !ok || len(e.Frames) != 12 {
		t.Fatal("allies.gaf has no twelve-frame TEAMICONSx")
	}
}

// sharingCapture writes cl's frame to NANOLATHE_ONLINE_CAPTURE when it names a
// directory, for review.
func sharingCapture(t *testing.T, cl *client.Client, name string) {
	t.Helper()
	out := os.Getenv("NANOLATHE_ONLINE_CAPTURE")
	if out == "" {
		return
	}
	f, err := os.Create(filepath.Join(out, os.Getenv("NANOLATHE_ONLINE_CAPTURE_PREFIX")+name))
	if err != nil {
		t.Fatal(err)
	}
	err = png.Encode(f, cl.ComposeFrame())
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
}

// awaitSharing pumps both battles until done holds.
func awaitSharing(t *testing.T, shells []*gameShell, presenters []*client.Client, what string, done func() bool) {
	t.Helper()
	end := time.Now().Add(20 * time.Second)
	for !done() {
		for i, g := range shells {
			g.battle.pumpLocalMultiplayer(presenters[i])
			if mp := g.battle.multiplayer; mp == nil || mp.failure != nil {
				t.Fatalf("multiplayer stopped: %v", mp.failure)
			}
		}
		if time.Now().After(end) {
			t.Fatalf("never saw %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// Two players through a real local relay: Tab opens the strip, its SHARE
// gives metal, its ALLIES declares an alliance both clients see, with the
// announcement on the other seat, and `+ShareMetal` sets the issuing seat's
// option on both (DESIGN_MULTIPLAYER §6.7, §7.1). With
// NANOLATHE_ONLINE_CAPTURE naming a directory it writes the three windows.
func TestOnlineSharingScreensRetail(t *testing.T) {
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
		cl.SetFocused(true)
		presenters[i] = cl
	}
	pumpOnlineRetail(t, shells, 30, presenters[0])
	host, guest := shells[0].battle, shells[1].battle
	hostCl := presenters[0]
	cur, ok := host.currentSnapshot()
	if !ok || !cur.Sharing.Online || !cur.Sharing.MayShare || !cur.Players[1].GiftTarget || !cur.Players[1].AllianceTarget {
		t.Fatalf("host standing %+v", cur.Sharing)
	}
	press := func(token input.Token) {
		in := hostCl.Input()
		in.ShortcutTokenMode = true
		in.EnqueueToken(token)
		host.viewerStep(0, hostCl)
	}

	press(input.Token{Kind: input.TokenEdit, Key: input.KeyTab})
	if host.battleState().Modal() != ui.BattleModalTabMenu {
		t.Fatalf("Tab opened modal %d", host.battleState().Modal())
	}
	sharingCapture(t, hostCl, "strip.png")

	// SHARE: half the metal to the guest.
	host.activateSharingGadget(host.sharing.tab.window.GadgetIndex("SHARE"), hostCl)
	if host.battleState().Modal() != ui.BattleModalShare {
		t.Fatalf("SHARE opened modal %d", host.battleState().Modal())
	}
	s := &host.sharing.share
	metal := s.slider[shareMetal]
	s.panel.SetSliderKnobAt(metal, int(s.window.Gadgets[metal].Range)/2)
	s.panel.SetStageAt(s.window.GadgetIndex("MAPINFO"), 1)
	gift := s.shareValue(shareMetal)
	if gift < 100 {
		t.Fatalf("half the metal reads back %d", gift)
	}
	before := sharingEconomy(cur).Metal
	host.viewerStep(0, hostCl)
	sharingCapture(t, hostCl, "share.png")
	host.activateSharingGadget(s.window.GadgetIndex("OK"), hostCl)
	if host.battleState().Modal() != ui.BattleModalTabMenu {
		t.Fatalf("OK left modal %d, want the strip it was opened over", host.battleState().Modal())
	}
	awaitSharing(t, shells, presenters, "the gift leave the host's stock", func() bool {
		f, _ := host.currentSnapshot()
		return sharingEconomy(f).Metal < before-float32(gift)/2
	})

	// ALLIES, from the strip still open: declare toward the guest.
	host.activateSharingGadget(host.sharing.tab.window.GadgetIndex("ALLIES"), hostCl)
	a := &host.sharing.allies
	if host.battleState().Modal() != ui.BattleModalAllies || len(a.rows) != 2 || a.rows[1].slot != 1 || a.rows[1].ally <= 0 {
		t.Fatalf("allies modal %d rows %+v", host.battleState().Modal(), a.rows)
	}
	host.activateSharingGadget(a.rows[1].ally, hostCl)
	awaitSharing(t, shells, presenters, "the declaration on both clients", func() bool {
		h, _ := host.currentSnapshot()
		g, _ := guest.currentSnapshot()
		return h.Players[0].Allies[1] && g.Players[1].AlliedBy[0]
	})
	host.viewerStep(0, hostCl)
	if a.panel.StageAt(a.rows[1].ally) != 1 {
		t.Fatalf("host indicator %d, want 1 (mine only)", a.panel.StageAt(a.rows[1].ally))
	}
	sharingCapture(t, hostCl, "allies.png")
	hostName, guestName := host.sess.Econ.Players[0].Name, host.sess.Econ.Players[1].Name
	got := awaitChatLine(t, shells, presenters, guest, "<"+hostName+">  allied with "+guestName)
	if got.Class != 8 || got.SpeakerSlot != 0 {
		t.Fatalf("announcement %+v", got)
	}
	host.activateSharingGadget(a.window.GadgetIndex("OK"), hostCl)

	// The guest's window shows the host's declaration as theirs only.
	guestCl := presenters[1]
	guest.openAlliesWindow(guestCl)
	ga := &guest.sharing.allies
	if guest.battleState().Modal() != ui.BattleModalAllies || ga.panel.StageAt(ga.rows[0].ally) != 2 {
		t.Fatalf("guest indicator %d, want 2 (theirs only)", ga.panel.StageAt(ga.rows[0].ally))
	}
	sharingCapture(t, guestCl, "allies-guest.png")
	guest.activateSharingGadget(ga.window.GadgetIndex("OK"), guestCl)

	// `+ShareMetal` sets the host's option on both clients.
	host.commitLocalChat("+ShareMetal")
	awaitSharing(t, shells, presenters, "ShareMetal on both clients", func() bool {
		h, _ := host.currentSnapshot()
		g, _ := guest.currentSnapshot()
		return h.Players[0].ShareMetal && g.Players[0].ShareMetal
	})
}
