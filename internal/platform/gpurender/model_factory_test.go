package gpurender

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// An attached child's finished transparent pixels contribute neither colour
// nor key to the staging image [03 R-REN-03A §4]. These authored rectangles
// separate that contract from stock art, palette shading, and COB animation.
func checkFactoryRevealDevicePixels() error {
	pal := fixturePalette()
	const w, h = 176, 72
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	for _, secondPage := range []bool{false, true} {
		for _, doubled := range []bool{false, true} {
			for _, scale := range []int32{1, 2} {
				face := func(x0, y0, x1, y1 int32, color uint8, key int32) drawlist.ModelFace {
					return directFace(x0*scale, y0*scale, x1*scale, y1*scale, color, key, key)
				}
				plate := directSubject(8, 8, 74*scale, 26*scale,
					face(0, 0, 72, 24, 200, 12), face(40, 0, 48, 24, 210, 80))
				child := directSubject(8, 8, 66*scale, 26*scale,
					face(0, 0, 8, 24, 100, 5), face(8, 0, 24, 24, 100, 20),
					face(24, 0, 40, 24, 100, 40), face(40, 0, 64, 24, 100, 20))
				child.Reveal = &drawlist.ModelReveal{Floor: 10, Line: 30, Below: -1, Band: 250, Above: -2}
				child.Outline = []drawlist.ModelFace{face(24, 4, 40, 20, 60, 40)}
				// The opaque sibling tests both a key below an erased child face
				// and a tie with a visible one. The final erased sibling must not
				// suppress either earlier child's finished colour.
				sibling := directSubject(8, 8, 66*scale, 26*scale,
					face(28, 0, 36, 24, 180, 5), face(56, 0, 64, 24, 180, 20))
				erased := directSubject(8, 8, 66*scale, 26*scale, face(56, 0, 64, 24, 100, 40))
				erased.Reveal = child.Reveal
				if doubled {
					// Reveal exists ONLY on the selected doubled packet: group
					// eligibility must not rely on the native packet's marker.
					for _, g := range []*drawlist.ModelGeometry{child, erased} {
						ss := *g
						ss.Scale, ss.Width, ss.Height = 2, g.Width*2, g.Height*2
						ss.Faces = make([]drawlist.ModelFace, len(g.Faces))
						for i, f := range g.Faces {
							ss.Faces[i] = f
							ss.Faces[i].Vertices = append([]drawlist.ModelVertex(nil), f.Vertices...)
							for j := range ss.Faces[i].Vertices {
								ss.Faces[i].Vertices[j].X *= 2
								ss.Faces[i].Vertices[j].Y *= 2
							}
						}
						ss.Outline = nil // Outlines always come from the native packet.
						g.Supersample, g.Reveal = &ss, nil
					}
				}
				plate.Children = []drawlist.ModelChild{{Geometry: child, KeyDelta: 10}, {Geometry: sibling, KeyDelta: 10}, {Geometry: erased, KeyDelta: 10}}
				var list drawlist.List
				list.RecordClear()
				list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: w, H: h}, Index: 7, Style: drawlist.FillSolid})
				if secondPage {
					list.RecordModel(drawlist.Model{Geometry: pageFiller(h, 2046, 2044, 7, 10)})
				}
				list.RecordModel(drawlist.Model{Geometry: plate})
				list.RecordExpand()
				img := r.Execute(&list, w, h)
				if img == nil {
					return fmt.Errorf("factory reveal fixture returned no image")
				}
				pix := make([]byte, w*h*4)
				img.ReadPixels(pix)
				for _, sample := range []struct {
					name string
					x, y int32
					want uint8
				}{
					{"erased child preserves plate", 26, 12, 200},
					{"below reveal keeps child", 4, 12, 100},
					{"reveal band remains", 16, 12, 250},
					{"outline remains", 24, 12, 60},
					{"higher factory face occludes child", 44, 12, 210},
					{"erased key admits lower sibling", 32, 12, 180},
					{"equal key admits later sibling before erased sibling", 60, 12, 180},
					{"plate beside children", 68, 12, 200},
				} {
					x, y := 8+sample.x*scale, 8+sample.y*scale
					name := fmt.Sprintf("factory %s (scale=%d doubled=%v secondPage=%v)", sample.name, scale, doubled, secondPage)
					if err := checkExactIndex(name, pix, int(y*w+x)*4, &pal, sample.want); err != nil {
						return err
					}
				}
				if r.modelStats.DirectOverflow != 0 || (secondPage && r.modelStats.DirectPages != 2) {
					return fmt.Errorf("factory reveal atlas: overflow=%d pages=%d secondPage=%v", r.modelStats.DirectOverflow, r.modelStats.DirectPages, secondPage)
				}
			}
		}
	}
	return nil
}

// checkFactoryPageTurnDevicePixels draws a factory building a revealed product
// twice: alone, and after an off-screen filler that leaves the first atlas
// page room for the factory's group region but not for its product's own
// region, so the group's allocations turn the page. The composite must be the
// same: every face draws into the page of its own region, the one its commit
// samples, whichever page that is.
func checkFactoryPageTurnDevicePixels() error {
	pal := fixturePalette()
	const w, h = 96, 48
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	for _, doubled := range []bool{false, true} {
		plate := directSubject(8, 8, 74, 26, directFace(0, 0, 72, 24, 200, 12, 12), directFace(40, 0, 48, 24, 210, 80, 80))
		child := directSubject(8, 8, 66, 26, directFace(0, 0, 8, 24, 100, 5, 5), directFace(8, 0, 40, 24, 100, 20, 40), directFace(40, 0, 64, 24, 120, 20, 20))
		child.Reveal = &drawlist.ModelReveal{Floor: 10, Line: 30, Below: -1, Band: 250, Above: -2}
		if doubled {
			ss := *child
			ss.Scale, ss.Width, ss.Height = 2, child.Width*2, child.Height*2
			ss.Faces = make([]drawlist.ModelFace, len(child.Faces))
			for i, f := range child.Faces {
				ss.Faces[i] = f
				ss.Faces[i].Vertices = append([]drawlist.ModelVertex(nil), f.Vertices...)
				for j := range ss.Faces[i].Vertices {
					ss.Faces[i].Vertices[j].X *= 2
					ss.Faces[i].Vertices[j].Y *= 2
				}
			}
			child.Supersample, child.Reveal = &ss, nil
		}
		plate.Children = []drawlist.ModelChild{{Geometry: child, KeyDelta: 10}}
		var pix [2][]byte
		for turn := range 2 {
			var list drawlist.List
			list.RecordClear()
			list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: w, H: h}, Index: 7, Style: drawlist.FillSolid})
			if turn == 1 {
				// A 3,884 × 4,084 region: the factory's 152-texel-wide region
				// fits beside it, its product's 136 more do not, and no shelf
				// fits below it. Its one recorded row is the field's own
				// index.
				list.RecordModel(drawlist.Model{Geometry: pageFiller(h, 1940, 2040, 7, 10)})
			}
			list.RecordModel(drawlist.Model{Geometry: plate})
			list.RecordExpand()
			img := r.Execute(&list, w, h)
			if img == nil {
				return fmt.Errorf("factory page turn fixture returned no image")
			}
			d := &r.modelDirect
			if group, product := d.regions[plate], d.regions[child]; turn == 1 && (group.page != 0 || product.page != 1) {
				return fmt.Errorf("factory page turn (doubled=%v): the group region is on page %d and the product's on %d, want 0 and 1", doubled, group.page, product.page)
			}
			pix[turn] = make([]byte, w*h*4)
			img.ReadPixels(pix[turn])
		}
		for i := 0; i < len(pix[0]); i += 4 {
			if !bytes.Equal(pix[0][i:i+4], pix[1][i:i+4]) {
				return fmt.Errorf("factory page turn (doubled=%v) pixel (%d,%d): %v after the page turn, %v alone", doubled, i/4%w, i/4/w, pix[1][i:i+4], pix[0][i:i+4])
			}
		}
	}
	return nil
}

// checkModelPagesConcurrentDevicePixels draws one frame with the model lane's
// page passes run beside Replay and again with them run inline, and requires
// the same composite and the same device accounting: the concurrent path only
// moves when the passes are issued, never what they draw or what reads them
// (prepareModelDirect). The frame has a construction group whose product's
// region turns the atlas page, so the group merges and both pages take part.
func checkModelPagesConcurrentDevicePixels() error {
	pal := fixturePalette()
	const w, h = 96, 48
	var pix [2][]byte
	var stats [2]ModelStats
	for inline := range 2 {
		r, err := NewChecked(&pal, w, h)
		if err != nil {
			return err
		}
		r.modelDirect.pagesInline = inline == 1
		plate := directSubject(8, 8, 74, 26, directFace(0, 0, 72, 24, 200, 12, 12), directFace(40, 0, 48, 24, 210, 80, 80))
		child := directSubject(8, 8, 66, 26, directFace(0, 0, 8, 24, 100, 5, 5), directFace(8, 0, 40, 24, 100, 20, 40), directFace(40, 0, 64, 24, 120, 20, 20))
		child.Reveal = &drawlist.ModelReveal{Floor: 10, Line: 30, Below: -1, Band: 250, Above: -2}
		plate.Children = []drawlist.ModelChild{{Geometry: child, KeyDelta: 10}}
		var list drawlist.List
		list.RecordClear()
		list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: w, H: h}, Index: 7, Style: drawlist.FillSolid})
		list.RecordModel(drawlist.Model{Geometry: pageFiller(h, 1940, 2040, 7, 10)})
		list.RecordModel(drawlist.Model{Geometry: plate})
		list.RecordModel(drawlist.Model{Geometry: directSubject(60, 30, 26, 14, directFace(0, 0, 24, 12, 150, 30, 30))})
		list.RecordExpand()
		img := r.Execute(&list, w, h)
		if img == nil {
			return fmt.Errorf("concurrent page passes fixture returned no image")
		}
		if r.modelDirect.pagesPending {
			return fmt.Errorf("Execute returned with the page passes still pending (inline=%v)", inline == 1)
		}
		pix[inline] = make([]byte, w*h*4)
		img.ReadPixels(pix[inline])
		stats[inline] = r.ModelStats()
	}
	if !bytes.Equal(pix[0], pix[1]) {
		return fmt.Errorf("concurrent page passes drew a different composite from inline ones")
	}
	c, i := stats[0], stats[1]
	if c.DirectPasses != i.DirectPasses || c.SubmittedVertices != i.SubmittedVertices || c.DeviceDraws != i.DeviceDraws ||
		c.DirectGroupMerges != i.DirectGroupMerges || c.DirectPages != 2 {
		return fmt.Errorf("concurrent page passes accounting passes=%d vertices=%d draws=%d merges=%d pages=%d, inline passes=%d vertices=%d draws=%d merges=%d",
			c.DirectPasses, c.SubmittedVertices, c.DeviceDraws, c.DirectGroupMerges, c.DirectPages, i.DirectPasses, i.SubmittedVertices, i.DeviceDraws, i.DirectGroupMerges)
	}
	return nil
}

// Captures are separately callable so the unfixed renderer can produce the
// same visual evidence even when the authored regression above fails. Every
// pose and fraction is published by a real session running the stock COB;
// only resources are replenished to keep the construction moving.
func checkFactoryRevealCaptures() error {
	dir, root := os.Getenv("NANOLATHE_FACTORY_CAPTURE"), os.Getenv("NANOLATHE_RETAIL_ASSETS")
	if dir == "" {
		return nil
	}
	if root == "" {
		return fmt.Errorf("factory captures require NANOLATHE_RETAIL_ASSETS")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		return err
	}
	defer fs.Close()
	preview, err := client.NewModelPreviewRenderer(fs)
	if err != nil {
		return err
	}
	for _, pair := range [][2]string{{"armlab", "armpw"}, {"corlab", "corak"}} {
		if err := captureFactoryBuild(fs, preview, dir, pair[0], pair[1]); err != nil {
			return err
		}
	}
	return nil
}

func captureFactoryBuild(fs *vfs.FS, preview *client.ModelPreviewRenderer, dir, factoryName, productName string) error {
	rng.SeedGlobal(1, 1)
	battle, err := headless.ComposeFreshBattle(headless.FreshBattleRequest{
		Kind: headless.ScenarioDirectOTA, Map: "Great Divide", LocalOwner: -1,
		SimulationSeed: 1, CRTSeed: 1, FS: fs,
	})
	if err != nil {
		return err
	}
	sess := battle.Session
	for tick := int32(1); tick <= 30; tick++ {
		sess.Step(tick)
	}
	def, ok := sess.Catalog.Unit(factoryName)
	if !ok || def == nil {
		return fmt.Errorf("factory capture: missing %s", factoryName)
	}
	var x, z numeric.Fixed
	found := false
	for _, u := range sess.Units.Iter() {
		if u != nil && u.Alive && u.Owner == sess.LocalOwner && u.Def != nil && strings.HasSuffix(strings.ToLower(u.Def.UnitName), "com") {
			x, z, found = u.X+numeric.Fixed(10<<20), u.Z, true
			break
		}
	}
	if !found {
		return fmt.Errorf("factory capture: local commander absent")
	}
	handle, err := sess.Units.Create(def, sess.LocalOwner, x, sess.World.HeightAt(x, z), z)
	if err != nil {
		return err
	}
	factory := sess.Units.Unit(handle)
	if err := sess.Build.RegisterBuildingPlacement(factory); err != nil {
		return err
	}
	if err := construction.QueueFactoryBuild(factory, productName, 1, sess.Catalog); err != nil {
		return err
	}
	stage := 0
	for tick := int32(31); tick <= 6000; tick++ {
		for i := range sess.Econ.Players {
			sess.Econ.Players[i].Stock = [2]float32{1e8, 1e8}
			sess.Econ.Players[i].Capacity = [2]float32{1e8, 1e8}
		}
		sess.Step(tick)
		cur := sess.Snapshot.Current()
		if cur == nil {
			continue
		}
		var carrier, child *frame.UnitView
		for i := range cur.Units {
			u := &cur.Units[i]
			if u.Slot == handle {
				carrier = u
			}
			if u.Carrier == handle && u.BuildRemaining > 0 {
				child = u
			}
		}
		if carrier == nil || child == nil || (stage == 1 && child.BuildRemaining > .5) {
			continue
		}
		label := "early"
		if stage == 1 {
			label = "mid"
		}
		product := *child
		product.X, product.Y, product.Z = child.X-carrier.X, child.Y-carrier.Y, child.Z-carrier.Z
		product.NoShadow = true
		for _, scale := range []float32{1, 2} {
			opts := client.ModelPreviewOptions{
				Model: carrier.Model, Owner: carrier.OwnerColor, Heading: carrier.Heading, Pitch: carrier.Pitch, Bank: carrier.Bank,
				Width: 320, Height: 240, Scale: scale, Background: 100, Structure: true, KeyPlane: true,
				PiecePoses: carrier.Pieces, Children: []frame.UnitView{product},
			}
			classic, err := preview.RecordModel(opts)
			if err != nil {
				return err
			}
			modern, err := preview.RecordGeometry(opts)
			if err != nil {
				return err
			}
			r, err := NewChecked(modern.Palette, opts.Width, opts.Height)
			if err != nil {
				return err
			}
			img := r.Execute(&modern.List, opts.Width, opts.Height)
			if img == nil || r.modelStats.GPU == 0 || r.modelStats.DirectOverflow != 0 {
				return fmt.Errorf("factory capture %s: normal GPU path unavailable", factoryName)
			}
			pix := make([]byte, opts.Width*opts.Height*4)
			img.ReadPixels(pix)
			base := fmt.Sprintf("%s-%s-%s-scale-%g", factoryName, productName, label, scale)
			for _, shot := range []struct {
				name string
				pic  *image.RGBA
			}{{"classic", classic.Image}, {"modern", &image.RGBA{Pix: pix, Stride: opts.Width * 4, Rect: image.Rect(0, 0, opts.Width, opts.Height)}}} {
				f, err := os.Create(filepath.Join(dir, base+"-"+shot.name+".png"))
				if err != nil {
					return err
				}
				err = png.Encode(f, shot.pic)
				closeErr := f.Close()
				if err != nil {
					return err
				}
				if closeErr != nil {
					return closeErr
				}
			}
			metadata, err := json.MarshalIndent(struct {
				Source  string
				Tick    int32
				Options client.ModelPreviewOptions
			}{"Committed session/COB pose; resources replenished; relative placement preserved; shadows suppressed", tick, opts}, "", "  ")
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dir, base+".json"), metadata, 0644); err != nil {
				return err
			}
		}
		stage++
		if stage == 2 {
			return nil
		}
	}
	return fmt.Errorf("factory capture %s/%s reached %d of 2 construction stages", factoryName, productName, stage)
}
