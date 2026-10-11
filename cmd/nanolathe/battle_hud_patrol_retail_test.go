//go:build retail

package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Real stock GUI/art, real session publication and the ordinary click path.
// An optional output directory captures the production composition for review.
func TestRetailConstructorPatrolButtonLabels(t *testing.T) {
	opts := Options{Root: testsupport.RetailRoot(t), Map: "ashap plateau", Seed: 1, Gameplay: gameplay.Modern, GameplaySet: true}
	cs, err := openContent(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	pal := retailPaletteForTest(t, cs)
	for _, key := range []string{"armcv", "armca", "corcv", "corca"} {
		t.Run(key, func(t *testing.T) {
			rng.SeedGlobal(1, 1)
			sess, cat, err := newBattleSession(opts, cs)
			if err != nil {
				t.Fatal(err)
			}
			def, ok := cat.Unit(key)
			if !ok {
				t.Fatalf("missing constructor %s", key)
			}
			handle, err := sess.Units.Create(def, sess.LocalOwner, numeric.FixedFromInt(600), 0, numeric.FixedFromInt(600))
			if err != nil {
				t.Fatal(err)
			}
			u := sess.Units.Unit(handle)
			sess.Movement.EnsureUnit(u)
			sess.BindStagedOrderQueue(u)
			if def.CanFly {
				sess.Movement.SetMoverMode(u, 2)
				if !sess.Movement.PlaceUnit(orders.PlaceRequest{Unit: u.Handle, X: u.X, Y: u.Y + numeric.FixedFromInt(int64(def.CruiseAlt)), Z: u.Z}) {
					t.Fatal("airborne fixture placement failed")
				}
			}
			u.Flags &^= 1 << 22 // show the orders page
			q := orders.QueueOfUnit(u)
			id := orders.Resolve(9, u, nil, nil)
			q.PushHead(id, orders.NewNodeForOrder(id, 0, numeric.FixedFromInt(1800), u.Y, u.Z, 0, u.Handle, false))
			preferences := orders.BuilderOptions{Patrol: [3]orders.PatrolWorkOption{orders.PatrolReclaimOnly, orders.PatrolBoth, orders.PatrolAssistOnly}}
			sess.Build.OrderBinding.SetBuilderOptions(func(uint8) orders.BuilderOptions { return preferences })
			cam := &camera.Camera{ViewW: 640, ViewH: 480, MapW: int32(sess.World.CellW * 16), MapH: int32(sess.World.CellH * 16)}
			cam.JumpToBattleViewCenter(600, 600)
			b := &battleSession{sess: sess, cat: cat, cam: cam, local: testSelection(u)}
			b.hud, err = loadRetailBattleHUD(cs.fs, sess, cat, pal, nil, newBattleWindowContext(cs, nil))
			if err != nil {
				t.Fatal(err)
			}
			step := int32(0)
			for stance, want := range []string{"Reclaim", "Both", "Assist"} {
				u.Flags = u.Flags & ^(units.StandingFieldMask<<units.StandingMoveShift) | uint32(stance)<<units.StandingMoveShift
				for i := 0; i < 5; i++ {
					step++
					sess.Step(step)
				}
				f, _ := b.currentSnapshot()
				if got := constructorPatrolCaption(f); got != want {
					t.Fatalf("stance%d caption=%q want%q mobile=%t head=%s mover=%d frame=%+v", stance, got, want, def.MobileStandOrders, orders.DescriptorFor(q.Head().ID).Name, u.Move.Mode, f.CommandPage)
				}
				window, _, err := b.hud.windowForRequired(b, f)
				if err != nil {
					t.Fatal(err)
				}
				var rect gui.Rect
				for i, gad := range window.Gadgets {
					if commandButtonName(gad.Name) == "MOVEORD" {
						rect = window.PlacedRect(i)
					}
				}
				if rect.W <= 0 || rect.H <= 0 {
					t.Fatal("missing movement gadget")
				}
				b.hud.updateHoveredGadget(b, f, rect.X+rect.W/2, rect.Y+rect.H/2)
				if footer, ok := b.hud.constructorPatrolFooter(b, f, b.footerHover(f)); !ok || len(footer.Texts) != 2 {
					t.Fatal("movement button has no patrol hover")
				}
				if dir := os.Getenv("NANOLATHE_PATROL_HUD_SHOTS"); dir != "" {
					if err := os.MkdirAll(dir, 0755); err != nil {
						t.Fatal(err)
					}
					cl, err := client.New(client.Options{Buffer: sess.Snapshot, Width: 640, Height: 480})
					if err != nil {
						t.Fatal(err)
					}
					cl.SetTerrain(sess.World)
					cl.SetCamera(cam)
					cl.SetPalette(pal)
					cl.SetFNT(b.hud.console)
					cl.SetModelFS(cs.unmappedMount)
					cl.SetUIStage(battleHUDUIStage{hud: b.hud, battle: b})
					cl.Input().Mouse.SetPosition(float32(rect.X+rect.W/2), float32(rect.Y+rect.H/2))
					file, err := os.Create(filepath.Join(dir, fmt.Sprintf("%s-%s.png", key, want)))
					if err != nil {
						t.Fatal(err)
					}
					err = png.Encode(file, cl.ComposeFrame())
					closeErr := file.Close()
					if err != nil {
						t.Fatal(err)
					}
					if closeErr != nil {
						t.Fatal(closeErr)
					}
					t.Logf("%s %s movement rectangle=%+v", key, want, rect)
				}
				if !hudConsumeClick(b.hud, b, rect.X+rect.W/2, rect.Y+rect.H/2) {
					t.Fatal("movement click not consumed")
				}
				for i := 0; i < 5; i++ {
					step++
					sess.Step(step)
				}
				next, _ := b.currentSnapshot()
				if next.CommandPage.MoveStance != uint8((stance+1)%3) {
					t.Fatalf("click changed stance to%d", next.CommandPage.MoveStance)
				}
			}
		})
	}
}

// The cleared caption area must contain only the original blank texture, and
// the entire indicator strip must retain the actual stance's stock pixels.
// This catches an off-by-one crop leaking old lettering or truncating a light.
func TestRetailConstructorPatrolDarkPlatePixels(t *testing.T) {
	fs := vfs.New()
	if err := fs.MountGameDirectory(testsupport.RetailRoot(t)); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	common, err := formats.LoadGAFFile(fs, "anims/commongui.gaf")
	if err != nil {
		t.Fatal(err)
	}
	fonts, err := formats.LoadGAFFile(fs, "anims/hattfont12.gaf")
	if err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile("palettes/palette.pal")
	if err != nil {
		t.Fatal(err)
	}
	pal, err := formats.LoadPAL(data)
	if err != nil {
		t.Fatal(err)
	}
	tables, err := palette.Load(fs)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ARMMOVEORD", "CORMOVEORD"} {
		t.Run(name, func(t *testing.T) {
			entry, ok := common.Find(name)
			if !ok {
				t.Fatal("missing stock movement art")
			}
			r := gui.Rect{X: 4, Y: 4, W: 113, H: 21}
			window := &gui.Window{Rect: gui.Rect{W: 128, H: 40}, Gadgets: []gui.Gadget{{}, {
				Kind: gui.KindButton, Active: 1, Name: name, Attribs: guiAttribCycle, Rect: r,
				ButtonArtResolved: true, ButtonArt: entry,
			}}}
			h := &retailBattleHUD{fs: fs, common: common, modalFont: &fonts.Entries[0], windows: map[string]*gui.Window{"gen": window}}
			b := &battleSession{hud: h, cat: &content.Catalog{}}
			buffer := frame.NewBuffer()
			cl, err := client.New(client.Options{Buffer: buffer, Width: 128, Height: 40})
			if err != nil {
				t.Fatal(err)
			}
			cl.SetPalette(tables)
			cl.SetUIStage(sidebarDrawStage{b})
			for stance := uint8(0); stance < 4; stance++ {
				f := buffer.BeginWrite()
				f.Selection.Handles = []pool.Handle{1, 2}
				f.Units = []frame.UnitView{{Slot: 1, PatrolWork: 1}, {Slot: 2, PatrolWork: 1}}
				f.CommandPage.MoveStance = stance
				if err := buffer.Publish(uint32(stance) + 1); err != nil {
					t.Fatal(err)
				}
				im := cl.ComposeFrame()
				for y := 0; y < 21; y++ {
					for x := 65; x < 113; x++ {
						source := entry.Frames[4].Frame
						if x >= 84 {
							source = entry.Frames[stance].Frame
						}
						pixel, visible := source.At(x, y)
						if !visible {
							continue
						}
						if got, want := im.RGBAAt(int(r.X)+x, int(r.Y)+y), pal.Colors[pixel]; got != want {
							t.Fatalf("stance%d pixel(%d,%d)=%v want%v", stance, x, y, got, want)
						}
					}
				}
			}
			// A different authored frame layout must use the existing generic
			// plate rather than assuming that a fourth frame is blank.
			other := *entry
			other.Frames = entry.Frames[:3]
			window.Gadgets[1].ButtonArt = &other
			trace := &sidebarSpriteTrace{}
			cl.RecordFrame().Replay(trace)
			generic, _ := common.Find("stagebuttn3")
			found := false
			for _, sprite := range trace.sprites {
				if sprite.Frame == generic.Frames[3].Frame {
					found = true
				}
			}
			if !found {
				t.Fatal("different art layout lost its generic mixed-stance fallback")
			}
		})
	}
}
