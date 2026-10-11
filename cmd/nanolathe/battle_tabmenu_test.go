package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// sharingFixtureGadget is one authored record of a fixture window, written in
// the GUI file's own section shape [fmt gui].
type sharingFixtureGadget struct {
	kind                int
	name, text          string
	x, y, w, h          int
	attribs, active     int
	stages, assoc       int
	quickkey, grayedout int
}

// writeSharingFixture authors one small window under dir/guis. The records
// are ours, shaped like the stock windows' controls the screens use.
func writeSharingFixture(t *testing.T, dir, file, crdefault, escdefault string, header [4]int, gadgets []sharingFixtureGadget) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "[GADGET0]\n{\n[COMMON]\n{\nid=0;\nassoc=0;\nname=%s;\nxpos=%d;\nypos=%d;\nwidth=%d;\nheight=%d;\nattribs=0;\nactive=1;\n}\ntotalgadgets=%d;\n[VERSION]\n{\nmajor=1;\nminor=0;\nrevision=0;\n}\npanel=;\ncrdefault=%s;\nescdefault=%s;\ndefaultfocus=;\n}\n",
		file, header[0], header[1], header[2], header[3], len(gadgets), crdefault, escdefault)
	for i, g := range gadgets {
		fmt.Fprintf(&b, "[GADGET%d]\n{\n[COMMON]\n{\nid=%d;\nassoc=%d;\nname=%s;\nxpos=%d;\nypos=%d;\nwidth=%d;\nheight=%d;\nattribs=%d;\nactive=%d;\n}\n",
			i+1, g.kind, g.assoc, g.name, g.x, g.y, g.w, g.h, g.attribs, g.active)
		switch g.kind {
		case 1:
			fmt.Fprintf(&b, "status=0;\ntext=%s;\nquickkey=%d;\ngrayedout=%d;\nstages=%d;\n", g.text, g.quickkey, g.grayedout, g.stages)
		case 4:
			fmt.Fprintf(&b, "range=100;\nthick=10;\nknobpos=0;\nknobsize=10;\n")
		case 5:
			fmt.Fprintf(&b, "text=%s;\nlink=;\n", g.text)
		case 6:
			b.WriteString("hotornot=1;\n")
		}
		b.WriteString("}\n")
	}
	path := filepath.Join(dir, "guis", file)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// sharingFixtureFS mounts the three fixture windows.
func sharingFixtureFS(t *testing.T) *vfs.FS {
	t.Helper()
	dir := t.TempDir()
	writeSharingFixture(t, dir, "tabmenu.gui", "CANCEL", "CANCEL", [4]int{130, -33, 510, 33}, []sharingFixtureGadget{
		{kind: 1, name: "OPTIONS", text: "Options Menu", x: 8, y: 6, w: 120, h: 21, attribs: 2, active: 1, quickkey: 'O'},
		{kind: 1, name: "SHARE", text: "Share", x: 255, y: 6, w: 120, h: 20, attribs: 2, active: 1, quickkey: 'S'},
		{kind: 1, name: "ALLIES", text: "Allies", x: 132, y: 6, w: 120, h: 20, attribs: 2, active: 1, quickkey: 'A'},
		{kind: 1, name: "CANCEL", x: 191, y: 6, w: 114, h: 20, attribs: 2, active: 0},
		{kind: 1, name: "CONTROL", text: "Control", x: 379, y: 6, w: 120, h: 20, attribs: 2, active: 1},
	})
	writeSharingFixture(t, dir, "share.gui", "OK", "CANCEL", [4]int{324, -429, 233, 396}, []sharingFixtureGadget{
		{kind: 1, name: "CANCEL", text: "Cancel", x: 65, y: 348, w: 96, h: 31, attribs: 2, active: 1},
		{kind: 1, name: "OK", text: "OK", x: 65, y: 312, w: 96, h: 31, attribs: 2, active: 1},
		{kind: 4, name: "METAL", x: 49, y: 188, w: 120, h: 16, attribs: 1, active: 1, assoc: 20},
		{kind: 4, name: "ENERGY", x: 49, y: 222, w: 120, h: 16, attribs: 1, active: 1, assoc: 21},
		{kind: 1, name: "SHARUNIT", text: "Chosen Units|Chosen Units", x: 53, y: 246, w: 120, h: 20, attribs: 0x4001, active: 1, stages: 2},
		{kind: 5, name: "METAL#", text: "METAL#", x: 174, y: 191, w: 88, h: 15, attribs: 0x11, active: 1},
		{kind: 5, name: "ENERGY#", text: "ENERGY#", x: 175, y: 223, w: 72, h: 13, attribs: 0x11, active: 1},
		{kind: 2, name: "PLYRLIST", x: 39, y: 59, w: 133, h: 101, attribs: 1, active: 1, assoc: 50},
		{kind: 1, name: "MAPINFO", text: "Map Info|Map Info", x: 53, y: 271, w: 120, h: 20, attribs: 0x4001, active: 1, stages: 2},
	})
	allies := []sharingFixtureGadget{}
	for r := range 10 {
		allies = append(allies,
			sharingFixtureGadget{kind: 6, name: fmt.Sprintf("LOGO%d", r), x: 10, y: 12 + 23*r, w: 130, h: 20, active: 1},
			sharingFixtureGadget{kind: 5, name: fmt.Sprintf("PLAYER%d", r), x: 14, y: 14 + 23*r, w: 120, h: 15, attribs: 0x1a, active: 1})
	}
	for r := range 10 {
		allies = append(allies, sharingFixtureGadget{kind: 1, name: "ALLYx", x: 145, y: 12 + 23*r, w: 38, h: 20, attribs: 1, active: 1, stages: 4})
	}
	for r := range 10 {
		allies = append(allies, sharingFixtureGadget{kind: 1, name: "TEAMICONSx", x: 193, y: 12 + 23*r, w: 38, h: 20, attribs: 1, active: 1, stages: 12})
	}
	allies = append(allies,
		sharingFixtureGadget{kind: 1, name: "VICTORY", text: "Off|On", x: 69, y: 268, w: 120, h: 20, attribs: 1, active: 1, stages: 2},
		sharingFixtureGadget{kind: 1, name: "OK", text: "OK", x: 79, y: 297, w: 96, h: 31, attribs: 2, active: 1})
	writeSharingFixture(t, dir, "allies.gui", "OK", "OK", [4]int{240, 108, 241, 339}, allies)
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	return fs
}

// sharingTestBattle is a battle with a fake online driver, the fixture
// windows and a committed online frame for seat 0 among three humans and a
// computer; edit adjusts that frame before it is published.
func sharingTestBattle(t *testing.T, edit func(f *frame.Frame)) (*battleSession, *testLocalBattleDriver) {
	t.Helper()
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	b.sess.Econ = &economy.Service{}
	b.hud = &retailBattleHUD{fs: sharingFixtureFS(t)}
	b.shell = &gameShell{presentation: settings.DefaultPresentation(), frontend: ui.NewFrontend(modeMenuMain)}
	d := &testLocalBattleDriver{}
	b.multiplayer = &battleMultiplayer{driver: d}
	b.cl.SetFocused(true)
	publishSharingFrame(t, b, edit)
	return b, d
}

// publishSharingFrame commits an online frame: seats 0..2 human, seat 3 a
// computer, every one alive and on no team, seat 0 local and able to share.
func publishSharingFrame(t *testing.T, b *battleSession, edit func(f *frame.Frame)) {
	t.Helper()
	f := b.sess.Snapshot.BeginWrite()
	f.Sharing = frame.SharingView{Online: true, Seat: 0, MayShare: true, SharedVictoryChangeable: true}
	for slot := range 4 {
		row := &f.Players[slot]
		row.Present, row.Controller, row.LiveUnits, row.Team = true, 1, 1, alliesNoTeam
		row.Name = fmt.Sprintf("Player %d", slot+1)
		row.GiftTarget = slot != 0
		row.AllianceTarget = slot == 1 || slot == 2
	}
	f.Players[3].Controller = 2
	f.Economy = append(f.Economy, frame.EconomyView{Player: 0, Metal: 750.9, Energy: 2000})
	if edit != nil {
		edit(f)
	}
	tick := b.currentTick() + 1
	if err := b.sess.Snapshot.Publish(tick); err != nil {
		t.Fatal(err)
	}
}

// sharingCueSpy records the alias of every cue the battle plays.
type sharingCueSpy struct{ aliases []string }

func (s *sharingCueSpy) PlaySample(sample *audio.Sample, _, _ float64) error {
	s.aliases = append(s.aliases, sample.Alias)
	return nil
}

// take returns the cues played since the last take.
func (s *sharingCueSpy) take() string {
	got := strings.Join(s.aliases, ",")
	s.aliases = nil
	return got
}

// installSharingCues gives the battle an audio service whose interface cues
// each resolve to their own small authored sample, and records what plays.
func installSharingCues(t *testing.T, b *battleSession) *sharingCueSpy {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sounds"), 0o755); err != nil {
		t.Fatal(err)
	}
	aliases := []string{"SmallButton", "BigButton", "Options", "Previous"}
	for _, alias := range aliases {
		// Independently authored raw unsigned PCM [fmt wav].
		if err := os.WriteFile(filepath.Join(root, "sounds", strings.ToLower(alias)+".wav"), []byte{128, 129, 127, 128}, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	svc := audio.NewService(fs)
	for _, alias := range aliases {
		svc.Registry.RegisterPath(alias, "sounds/"+strings.ToLower(alias)+".wav")
	}
	previous := audio.GlobalOutput()
	t.Cleanup(func() { audio.SetGlobalOutput(previous) })
	spy := &sharingCueSpy{}
	audio.SetGlobalOutput(spy)
	b.sess.Audio = svc
	return spy
}

func pressToken(b *battleSession, token input.Token) {
	in := b.cl.Input()
	in.ShortcutTokenMode = true
	in.EnqueueToken(token)
	b.viewerStep(0, b.cl)
}

// Online, Tab opens and closes the strip with the Options preference, and the
// strip shows SHARE and ALLIES to a seat that may share and greys CONTROL
// [07 "Tab options menu and manual exit"]; chat holds Tab
// [07 R-CAM-01 §2].
func TestOnlineTabTogglesTheStrip(t *testing.T) {
	b, _ := sharingTestBattle(t, nil)
	pressToken(b, input.Token{Kind: input.TokenEdit, Key: input.KeyTab})
	if b.battleState().Modal() != ui.BattleModalTabMenu {
		t.Fatalf("Tab opened modal %d, want the strip", b.battleState().Modal())
	}
	p, w := b.sharing.tab.panel, b.sharing.tab.window
	if !p.ActiveOf("SHARE") || !p.ActiveOf("ALLIES") || p.ActiveOf("CANCEL") {
		t.Fatal("strip buttons not shown as the opener shows them")
	}
	if i := w.GadgetIndex("CONTROL"); p.ActiveAt(i) && w.Gadgets[i].GrayedOut&1 == 0 {
		t.Fatal("CONTROL is clickable; Nanolathe online offers no host player control")
	}
	if b.sess.Clock.Paused || b.battleState().Paused() {
		t.Fatal("the strip paused an online battle")
	}
	pressToken(b, input.Token{Kind: input.TokenEdit, Key: input.KeyTab})
	if b.battleState().Modal() != ui.BattleModalClosed {
		t.Fatalf("second Tab left modal %d", b.battleState().Modal())
	}
	// The strip's own quickkey opens the window its button names.
	pressToken(b, input.Token{Kind: input.TokenEdit, Key: input.KeyTab})
	pressToken(b, input.Token{Kind: input.TokenText, Rune: 'a'})
	if b.battleState().Modal() != ui.BattleModalAllies {
		t.Fatalf("ALLIES quickkey opened modal %d", b.battleState().Modal())
	}
}

// With the Overview preference Tab keeps the overview, and F2 takes the strip
// so it stays reachable; Tab no longer opens it.
func TestOnlineOverviewPreferenceMovesTheStripToF2(t *testing.T) {
	b, _ := sharingTestBattle(t, nil)
	b.shell.presentation.Overview = settings.OverviewMegamap
	pressToken(b, input.Token{Kind: input.TokenEdit, Key: input.KeyF2})
	if b.battleState().Modal() != ui.BattleModalTabMenu {
		t.Fatalf("F2 opened modal %d, want the strip", b.battleState().Modal())
	}
	pressToken(b, input.Token{Kind: input.TokenEdit, Key: input.KeyF2})
	if b.battleState().Modal() != ui.BattleModalClosed {
		t.Fatalf("second F2 left modal %d", b.battleState().Modal())
	}
	pressToken(b, input.Token{Kind: input.TokenEdit, Key: input.KeyTab})
	if b.battleState().Modal() == ui.BattleModalTabMenu {
		t.Fatal("Tab opened the strip under the Overview preference")
	}
}

// `h` opens the transfer dialog for a seat that may share, and a seat that
// may not share gets nothing [07 R-CAM-01 §2][05 R-SHARE-01 §5].
func TestOnlineHOpensShareOnlyForASharingSeat(t *testing.T) {
	b, _ := sharingTestBattle(t, func(f *frame.Frame) { f.Sharing.MayShare = false })
	pressToken(b, input.Token{Kind: input.TokenText, Rune: 'h'})
	if b.battleState().Modal() != ui.BattleModalClosed {
		t.Fatal("h opened a window for a watcher")
	}
	b, _ = sharingTestBattle(t, nil)
	pressToken(b, input.Token{Kind: input.TokenText, Rune: 'H'})
	if b.battleState().Modal() != ui.BattleModalClosed {
		t.Fatal("H opened the transfer dialog")
	}
	pressToken(b, input.Token{Kind: input.TokenText, Rune: 'h'})
	if b.battleState().Modal() != ui.BattleModalShare {
		t.Fatalf("h opened modal %d, want SHARE", b.battleState().Modal())
	}
	s := &b.sharing.share
	items, selected, _, _ := s.panel.ListValuesAt(s.list)
	if strings.Join(items, ",") != "Player 2,Player 3,Player 4" || selected != 0 {
		t.Fatalf("target list %q selected %d", items, selected)
	}
}

// A single-player battle keeps its keys: Tab opens the options window and `h`
// does nothing [07 R-CAM-01 §2].
func TestSinglePlayerKeepsTabAndIgnoresH(t *testing.T) {
	b, _ := sharingTestBattle(t, nil)
	b.multiplayer = nil
	pressToken(b, input.Token{Kind: input.TokenText, Rune: 'h'})
	if b.battleState().Modal() != ui.BattleModalClosed {
		t.Fatal("h opened a window in single player")
	}
	pressToken(b, input.Token{Kind: input.TokenEdit, Key: input.KeyTab})
	if b.battleState().Modal() != ui.BattleModalOptions {
		t.Fatalf("Tab opened modal %d in single player, want options", b.battleState().Modal())
	}
}

// The slider read-back truncates `knob / (travel − 1) × range` and reads 0
// below a travel of 2 [07 R-HUD-03 §9].
func TestShareReadBack(t *testing.T) {
	for _, c := range []struct {
		knob, travel int
		rangeWord    int32
		want         int32
	}{
		{0, 72, 750, 0},
		{71, 72, 750, 750},
		{1, 72, 750, 10}, // 750/71 = 10.56
		{35, 72, 750, 369},
		{10, 1, 750, 0},
		{3, 4, -9, -9},
	} {
		if got := shareReadBack(c.knob, c.travel, c.rangeWord); got != c.want {
			t.Errorf("readback(%d, %d, %d) = %d, want %d", c.knob, c.travel, c.rangeWord, got, c.want)
		}
	}
}

// The dialog's confirm sends one gift: the selected row's slot, both whole
// read-backs, the selection when the units box is checked and the map box
// [05 R-SHARE-01 §5].
func TestShareConfirmSendsOneGift(t *testing.T) {
	b, d := sharingTestBattle(t, nil)
	b.openShareScreen(b.cl)
	s := &b.sharing.share
	metal := s.slider[shareMetal]
	if metal <= 0 || s.rangeWord[shareMetal] != 750 || s.rangeWord[shareEnergy] != 2000 {
		t.Fatalf("slider %d ranges %v", metal, s.rangeWord)
	}
	travel := int(s.window.Gadgets[metal].Range)
	if travel != int(s.window.Gadgets[metal].Rect.W-s.window.Gadgets[metal].Rect.H) {
		t.Fatal("the dialog did not override the travel to width less height")
	}
	if text, _ := s.labelText(s.readout[shareMetal], s.window.Gadgets[s.readout[shareMetal]]); text != "0" {
		t.Fatalf("metal read-out %q at open, want 0", text)
	}
	s.panel.SetSliderKnobAt(metal, travel-1)
	s.panel.ListAt(s.list).SetSelected(1)
	s.panel.SetStageAt(s.window.GadgetIndex("MAPINFO"), 1)
	b.activateShareGadget(s.window.GadgetIndex("OK"), b.cl)
	if b.battleState().Modal() != ui.BattleModalClosed || len(d.commands) != 1 {
		t.Fatalf("modal %d, %d commands", b.battleState().Modal(), len(d.commands))
	}
	got := d.commands[0]
	want := session.HumanShareGiftCommand{Target: 2, Metal: 750, Energy: 0, MapInfo: true}
	if got.Kind != session.HumanShareGift || got.ShareGift.Target != want.Target || got.ShareGift.Metal != want.Metal ||
		got.ShareGift.Energy != want.Energy || got.ShareGift.MapInfo != want.MapInfo || len(got.ShareGift.Handles) != 0 {
		t.Fatalf("gift %+v", got.ShareGift)
	}
	// CANCEL and a confirmation with nothing to give send nothing.
	b.openShareScreen(b.cl)
	b.activateShareGadget(b.sharing.share.window.GadgetIndex("OK"), b.cl)
	b.openShareScreen(b.cl)
	b.activateShareGadget(b.sharing.share.window.GadgetIndex("CANCEL"), b.cl)
	if len(d.commands) != 1 || b.battleState().Modal() != ui.BattleModalClosed {
		t.Fatalf("%d commands after empty confirm and cancel", len(d.commands))
	}
}

// The allies window builds one row per live seated player, shows the ally
// button only for another live human, greys a teammate's, flips the shown
// declaration on a click and writes the victory box on OK only when it
// changed [07 "Tab options menu and manual exit"].
func TestAlliesWindowRowsClicksAndVictory(t *testing.T) {
	b, d := sharingTestBattle(t, func(f *frame.Frame) {
		f.Players[0].Team, f.Players[2].Team = 1, 1
		f.Players[2].AllianceTarget = false
		f.Players[1].Allies[0] = true // seat 1 declared toward us: row B
		f.Players[0].AlliedBy[1] = true
		for slot := range 4 {
			f.Players[slot].Logo = uint8(slot + 3)
		}
	})
	cues := installSharingCues(t, b)
	b.openAlliesWindow(b.cl)
	a := &b.sharing.allies
	if b.battleState().Modal() != ui.BattleModalAllies || len(a.rows) != 4 {
		t.Fatalf("modal %d rows %d", b.battleState().Modal(), len(a.rows))
	}
	// Each shown row's LOGO surface is shown, not hot, and carries the
	// player's colour byte as its frame number.
	for r, row := range a.rows {
		if row.logo != a.window.GadgetIndex(fmt.Sprintf("LOGO%d", r)) || !a.panel.ActiveAt(row.logo) ||
			a.window.Gadgets[row.logo].HotOrNot&1 != 0 || row.colour != uint8(row.slot)+3 {
			t.Fatalf("row %d logo %d colour %d", r, row.logo, row.colour)
		}
	}
	if a.panel.ActiveOf("LOGO4") {
		t.Fatal("a LOGO surface past the shown rows is shown")
	}
	b.activateSharingGadget(a.victory, b.cl)
	if got := cues.take(); b.battleState().Modal() != ui.BattleModalAllies || got != "Options" {
		t.Fatalf("VICTORY: modal %d, cues %q", b.battleState().Modal(), got)
	}
	a.panel.SetStageAt(a.victory, 0)
	if a.rows[0].ally > 0 || a.rows[3].ally > 0 {
		t.Fatal("an ally button for the local row or the computer")
	}
	if a.rows[2].ally <= 0 || a.window.Gadgets[a.rows[2].ally].GrayedOut&1 == 0 {
		t.Fatal("the teammate's ally button is not shown greyed")
	}
	ally := a.rows[1].ally
	if ally <= 0 || a.panel.StageAt(ally) != 2 {
		t.Fatalf("seat 2's indicator %d, want 2 (theirs only)", a.panel.StageAt(ally))
	}
	if name, _ := a.labelText(a.rows[1].player, a.window.Gadgets[a.rows[1].player]); name != "Player 2" {
		t.Fatalf("row name %q", name)
	}
	b.activateSharingGadget(ally, b.cl)
	if len(d.commands) != 1 || d.commands[0].Kind != session.HumanDeclareAlliance ||
		d.commands[0].DeclareAlliance != (session.HumanDeclareAllianceCommand{Target: 1, Value: true}) {
		t.Fatalf("ally click sent %+v", d.commands)
	}
	if a.panel.StageAt(ally) != 3 {
		t.Fatalf("indicator after the click %d, want 3 (mutual)", a.panel.StageAt(ally))
	}
	lines := b.cl.MessageRing().Visible()
	if len(lines) == 0 || lines[len(lines)-1].Text != "<"+b.sess.Econ.Players[0].Name+">  allied with Player 2" {
		t.Fatalf("announcement %+v", lines)
	}
	// A second click before the grant flips the shown value back.
	b.activateSharingGadget(ally, b.cl)
	if d.commands[1].DeclareAlliance.Value {
		t.Fatal("second click did not break the pending alliance")
	}
	// OK with the box unchanged sends nothing; toggled, it sends the new bit.
	cues.take()
	b.activateSharingGadget(a.window.GadgetIndex("OK"), b.cl)
	if got := cues.take(); len(d.commands) != 2 || b.battleState().Modal() != ui.BattleModalClosed || got != "Options" {
		t.Fatalf("OK wrote an unchanged victory box or played %q", got)
	}
	b.openAlliesWindow(b.cl)
	a = &b.sharing.allies
	a.panel.SetStageAt(a.victory, 1)
	b.activateSharingGadget(a.window.GadgetIndex("OK"), b.cl)
	if len(d.commands) != 3 || d.commands[2].Kind != session.HumanSharedVictory || !d.commands[2].SharedVictory.On {
		t.Fatalf("OK sent %+v", d.commands)
	}
}

// Online, the share commands become seat commands carrying the value the
// press produces from the shown frame, and post retail's replies
// [07 R-CAM-01 §6].
func TestOnlineShareCommandsSendSeatCommands(t *testing.T) {
	b, d := sharingTestBattle(t, func(f *frame.Frame) { f.Players[0].ShareEnergy = true })
	for _, line := range []string{"+ShareMetal", "+shareenergy", "+ShareAll", "+SetShareMetal 300x", "+Give 1 25 METAL"} {
		b.dispatchLocalCommand(line)
	}
	if len(d.commands) != 5 {
		t.Fatalf("%d commands: %+v", len(d.commands), d.commands)
	}
	if c := d.commands[0]; c.Kind != session.HumanShareMetal || !c.ShareOption.On {
		t.Fatalf("ShareMetal %+v", c)
	}
	if c := d.commands[1]; c.Kind != session.HumanShareEnergy || c.ShareOption.On {
		t.Fatalf("ShareEnergy %+v", c)
	}
	if c := d.commands[2]; c.Kind != session.HumanShareAll || c.ShareAll != (session.HumanShareAllCommand{Metal: true, Energy: false, Mapping: true, Radar: true}) {
		t.Fatalf("ShareAll %+v", c)
	}
	if c := d.commands[3]; c.Kind != session.HumanSetShareMetal || c.ShareThreshold.Amount != 300 {
		t.Fatalf("SetShareMetal %+v", c)
	}
	if c := d.commands[4]; c.Kind != session.HumanGive || c.Give.Player != 1 || c.Give.Resource != economy.Metal || c.Give.Amount != 25 {
		t.Fatalf("Give %+v", c)
	}
	var texts []string
	for _, line := range b.cl.MessageRing().Visible() {
		texts = append(texts, line.Text)
	}
	want := []string{"Toggled ShareMetal to: ON", "Toggled ShareEnergy to: OFF", "Toggled ShareMetal to: ON", "Toggled ShareEnergy to: OFF",
		"Toggled ShareMapping to: ON", "Toggled ShareRadar to: ON", "OK.  Will share metal if above 300"}
	if strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Fatalf("replies %q", texts)
	}
	// A seat that may not share gets the refusal and sends nothing.
	b, d = sharingTestBattle(t, func(f *frame.Frame) { f.Sharing.MayShare = false })
	b.dispatchLocalCommand("+ShareMetal")
	if len(d.commands) != 0 {
		t.Fatal("a watcher's share command was sent")
	}
}

// Enter and Escape reach the window's authored defaults when its own service
// leaves them unclaimed [07 R-FE-01 §12], through the key matrix, which acts
// only while the key-navigation switch the strip turned on is still on
// [07 "Tab options menu and manual exit"]: over the strip Escape cancels
// SHARE and Enter confirms it, and the strip's inactive CANCEL default leaves
// both keys without effect. A SHARE opened by `h` with no strip open takes
// both keys and does nothing, as does one opened from the strip after an
// options window the strip opened has closed.
func TestSharingWindowsTakeTheirAuthoredDefaults(t *testing.T) {
	b, d := sharingTestBattle(t, nil)
	pressToken(b, input.Token{Kind: input.TokenEdit, Key: input.KeyTab})
	pressToken(b, input.Token{Kind: input.TokenEdit, Key: input.KeyEscape})
	pressToken(b, input.Token{Kind: input.TokenEdit, Key: input.KeyEnter})
	if b.battleState().Modal() != ui.BattleModalTabMenu {
		t.Fatalf("Escape or Enter acted on the strip: modal %d", b.battleState().Modal())
	}
	shareButton := b.sharing.tab.window.GadgetIndex("SHARE")
	b.activateSharingGadget(shareButton, b.cl)
	pressToken(b, input.Token{Kind: input.TokenEdit, Key: input.KeyEscape})
	if b.battleState().Modal() != ui.BattleModalTabMenu || len(d.commands) != 0 {
		t.Fatalf("Escape on SHARE over the strip: modal %d, %d commands", b.battleState().Modal(), len(d.commands))
	}
	b.activateSharingGadget(shareButton, b.cl)
	s := &b.sharing.share
	s.panel.SetSliderKnobAt(s.slider[shareEnergy], 1)
	pressToken(b, input.Token{Kind: input.TokenEdit, Key: input.KeyEnter})
	if b.battleState().Modal() != ui.BattleModalTabMenu || len(d.commands) != 1 || d.commands[0].ShareGift.Energy == 0 {
		t.Fatalf("Enter on SHARE over the strip: modal %d, commands %+v", b.battleState().Modal(), d.commands)
	}
	// Tab closes the strip, and the switch with it.
	pressToken(b, input.Token{Kind: input.TokenEdit, Key: input.KeyTab})
	pressToken(b, input.Token{Kind: input.TokenText, Rune: 'h'})
	pressToken(b, input.Token{Kind: input.TokenEdit, Key: input.KeyEscape})
	pressToken(b, input.Token{Kind: input.TokenEdit, Key: input.KeyEnter})
	if b.battleState().Modal() != ui.BattleModalShare || len(d.commands) != 1 {
		t.Fatalf("SHARE opened by h acted on Escape or Enter: modal %d, %d commands", b.battleState().Modal(), len(d.commands))
	}
	b.activateShareGadget(b.sharing.share.window.GadgetIndex("CANCEL"), b.cl)
	// The options root opened from the strip turns the switch off as it closes.
	pressToken(b, input.Token{Kind: input.TokenEdit, Key: input.KeyTab})
	b.activateSharingGadget(b.sharing.tab.window.GadgetIndex("OPTIONS"), b.cl)
	b.closeBattleMenu()
	if b.battleState().Modal() != ui.BattleModalTabMenu {
		t.Fatalf("closing the options root over the strip left modal %d", b.battleState().Modal())
	}
	b.activateSharingGadget(shareButton, b.cl)
	pressToken(b, input.Token{Kind: input.TokenEdit, Key: input.KeyEscape})
	if b.battleState().Modal() != ui.BattleModalShare {
		t.Fatalf("Escape acted on SHARE after the options root closed: modal %d", b.battleState().Modal())
	}
}

// The strip sits on the surface's last 33 rows and SHARE on its top edge, a
// negative `ypos` counting up from the bottom [07 R-WGT-01 §12][07 R-HUD-05].
// Each strip button plays `BigButton` and opens its window on top of the
// strip, which stays open beneath and is the top window again when that
// window closes; `CANCEL` closes the strip with no cue
// [07 "Tab options menu and manual exit"].
func TestTabStripButtonsPlayBigButtonAndStayBeneath(t *testing.T) {
	b, _ := sharingTestBattle(t, nil)
	cues := installSharingCues(t, b)
	pressToken(b, input.Token{Kind: input.TokenEdit, Key: input.KeyTab})
	tab := b.sharing.tab.window
	if got := cues.take(); b.battleState().Modal() != ui.BattleModalTabMenu || got != "SmallButton" {
		t.Fatalf("Tab: modal %d, cues %q", b.battleState().Modal(), got)
	}
	if tab.Rect.X != 130 || tab.Rect.Y != retailScreenH-33 {
		t.Fatalf("strip at (%d,%d), want (130,%d)", tab.Rect.X, tab.Rect.Y, retailScreenH-33)
	}
	for _, c := range []struct {
		button string
		modal  ui.BattleModal
		close  string
		cues   string
	}{
		{"SHARE", ui.BattleModalShare, "CANCEL", "Previous"},
		{"ALLIES", ui.BattleModalAllies, "OK", "Options"},
	} {
		b.activateSharingGadget(tab.GadgetIndex(c.button), b.cl)
		state := b.battleState()
		if got := cues.take(); state.Modal() != c.modal || !state.StripBeneath() || got != "BigButton" {
			t.Fatalf("%s: modal %d, strip beneath %v, cues %q", c.button, state.Modal(), state.StripBeneath(), got)
		}
		w := b.sharingWindowOnTop().window
		if c.modal == ui.BattleModalShare && w.Rect.Y != retailScreenH-429 {
			t.Fatalf("SHARE at y %d, want %d", w.Rect.Y, retailScreenH-429)
		}
		b.activateSharingGadget(w.GadgetIndex(c.close), b.cl)
		if got := cues.take(); state.Modal() != ui.BattleModalTabMenu || b.sharing.tab.window != tab || got != c.cues {
			t.Fatalf("%s %s: modal %d, cues %q", c.button, c.close, state.Modal(), got)
		}
	}
	b.activateSharingGadget(tab.GadgetIndex("OPTIONS"), b.cl)
	if got := cues.take(); b.battleState().Modal() != ui.BattleModalOptions || !b.battleState().StripBeneath() || got != "BigButton" {
		t.Fatalf("OPTIONS: modal %d, cues %q", b.battleState().Modal(), got)
	}
	b.closeBattleMenu()
	if b.battleState().Modal() != ui.BattleModalTabMenu || b.sharing.tab.window != tab {
		t.Fatalf("closing the options root left modal %d", b.battleState().Modal())
	}
	b.activateTabMenuButton("CANCEL", b.cl)
	if got := cues.take(); b.battleState().Modal() != ui.BattleModalClosed || got != "" {
		t.Fatalf("CANCEL: modal %d, cues %q", b.battleState().Modal(), got)
	}
}

// The transfer dialog's cues: `h` and the opener play none, the boxes and OK
// play `Options`, and with no player to offer the dialog closes at once, so
// `h` does nothing and the strip's SHARE leaves the strip on top
// [07 "Tab options menu and manual exit"][05 R-SHARE-01 §5].
func TestShareDialogCuesAndEmptyList(t *testing.T) {
	b, _ := sharingTestBattle(t, nil)
	cues := installSharingCues(t, b)
	pressToken(b, input.Token{Kind: input.TokenText, Rune: 'h'})
	if got := cues.take(); b.battleState().Modal() != ui.BattleModalShare || got != "" {
		t.Fatalf("h: modal %d, cues %q", b.battleState().Modal(), got)
	}
	s := &b.sharing.share
	for _, box := range []string{"SHARUNIT", "MAPINFO"} {
		b.activateSharingGadget(s.window.GadgetIndex(box), b.cl)
		if got := cues.take(); b.battleState().Modal() != ui.BattleModalShare || got != "Options" {
			t.Fatalf("%s: modal %d, cues %q", box, b.battleState().Modal(), got)
		}
	}
	b.activateSharingGadget(s.window.GadgetIndex("OK"), b.cl)
	if got := cues.take(); b.battleState().Modal() != ui.BattleModalClosed || got != "Options" {
		t.Fatalf("OK: modal %d, cues %q", b.battleState().Modal(), got)
	}

	b, _ = sharingTestBattle(t, func(f *frame.Frame) {
		for slot := range f.Players {
			f.Players[slot].GiftTarget = false
		}
	})
	cues = installSharingCues(t, b)
	pressToken(b, input.Token{Kind: input.TokenText, Rune: 'h'})
	if got := cues.take(); b.battleState().Modal() != ui.BattleModalClosed || got != "" {
		t.Fatalf("h with no target: modal %d, cues %q", b.battleState().Modal(), got)
	}
	pressToken(b, input.Token{Kind: input.TokenEdit, Key: input.KeyTab})
	cues.take()
	b.activateSharingGadget(b.sharing.tab.window.GadgetIndex("SHARE"), b.cl)
	if got := cues.take(); b.battleState().Modal() != ui.BattleModalTabMenu || b.battleState().StripBeneath() || got != "BigButton" {
		t.Fatalf("SHARE with no target: modal %d, cues %q", b.battleState().Modal(), got)
	}
}
