package gpurender

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

func TestModelSeedChoosesNativeFinalDimensions(t *testing.T) {
	g := directSubject(10, 20, 16, 12)
	g.CachedSeed = drawlist.ModelCachedSeed{Width: 16, Height: 12, OriginX: 3, OriginY: 5}
	p := modelPlacePacket{g: g, region: modelDirectRegion{bounds: image.Rect(10, 20, 26, 32)}}
	if got := modelSeedForPacket(&p); got != modelSeedRaw {
		t.Fatalf("equal dimensions=%v", got)
	}
	p.region.bounds.Max.X++
	if got := modelSeedForPacket(&p); got != modelSeedResized {
		t.Fatalf("enlarged union=%v", got)
	}
	p.group = directSubject(0, 0, 32, 32)
	if got := modelSeedForPacket(&p); got != modelSeedRaw {
		t.Fatalf("child own dimensions=%v", got)
	}
	p.shadow = true
	if got := modelSeedForPacket(&p); got != modelSeedNone {
		t.Fatalf("shadow opted into seed=%v", got)
	}
}

// Every source byte retains the same saturated result at the reduction
// endpoints and across the full difference of two signed height words.
func TestModelSeedDeltaPreservesSaturatedByteDomain(t *testing.T) {
	for _, delta := range []int32{-65535, -32769, -32768, -256, -255, -254, 0, 254, 255, 256, 32767, 32768, 65535} {
		reduced := modelSeedDelta(delta)
		if reduced < -255 || reduced > 255 {
			t.Fatalf("delta%d reduced%d", delta, reduced)
		}
		for key := int32(0); key <= 255; key++ {
			want := max(int64(0), min(int64(255), int64(key)+int64(delta)))
			got := max(0, min(255, key+reduced))
			if int64(got) != want {
				t.Fatalf("key%d delta%d got%d want%d", key, delta, got, want)
			}
		}
	}
}

// Interior samples discriminate the seed contract independently of Enhanced's
// edge coverage and RGB shading. Cold, warm and replay execute actual device
// passes, on both atlas pages and with native or doubled raster packets.
func checkModelSeedDevicePixels() error {
	const width, height = 248, 80
	pal := fixturePalette()
	for _, secondPage := range []bool{false, true} {
		for _, doubled := range []bool{false, true} {
			r, err := NewChecked(&pal, width, height)
			if err != nil {
				return err
			}
			for frame := 0; frame < 4; frame++ {
				if frame >= 2 {
					r.modelDirect.place = modelPlaceTuning{floor: 1, perHelper: 1}
				}
				var list drawlist.List
				list.RecordClear()
				list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: width, H: height}, Index: 7, Style: drawlist.FillSolid})
				if secondPage {
					list.RecordModel(drawlist.Model{Geometry: pageFiller(height, 2046, 2044, 7, 10)})
				}
				type sample struct {
					name string
					x, y int
					want byte
				}
				samples := []sample{}
				add := func(name string, index int, parent *drawlist.ModelGeometry, want byte) {
					x, y := int32(4+index%10*24), int32(4+index/10*24)
					parent.AnchorX, parent.AnchorY = x, y
					parent.Cache = drawlist.ModelCacheKey{Body: uint64(index + 1), Revision: 1}
					if frame == 3 {
						parent.Cache.Revision = 2
					}
					for _, ch := range parent.Children {
						ch.Geometry.AnchorX, ch.Geometry.AnchorY = x, y
					}
					if doubled {
						seedDoublePacket(parent)
					}
					list.RecordModel(drawlist.Model{Geometry: parent})
					samples = append(samples, sample{name, int(x + 4), int(y + 4), want})
				}
				seeded := func(color uint8, key int32) *drawlist.ModelGeometry {
					g := directSubject(0, 0, 16, 16, directFace(0, 0, 14, 14, color, key, key))
					g.CachedSeed = drawlist.ModelCachedSeed{Width: 16, Height: 16}
					return g
				}
				child := func(w int32, color uint8, key int32) *drawlist.ModelGeometry {
					return directSubject(0, 0, w, 16, directFace(0, 0, w-2, 14, color, key, key))
				}
				g := seeded(100, 1)
				g.Children = []drawlist.ModelChild{{Geometry: child(16, 180, 0)}}
				add("equal seed retains cached one", 0, g, 100)
				g = seeded(100, 1)
				g.Children = []drawlist.ModelChild{{Geometry: child(20, 180, 0)}}
				add("enlarged seed admits child zero", 1, g, 180)
				g = seeded(100, 1)
				g.Width = 20
				g.Faces = append(g.Faces, directFace(0, 0, 14, 14, 80, 0, 0))
				add("original cached winner survives seed tie", 2, g, 100)
				g = seeded(100, 0)
				g.LiveFaces = []drawlist.ModelFace{directFace(0, 0, 14, 14, 200, 1, 1)}
				g.Reveal = &drawlist.ModelReveal{Floor: 2, Line: 3, Below: 250, Band: -1, Above: -1}
				g.Children = []drawlist.ModelChild{{Geometry: child(20, 180, 0)}}
				add("live one survives enlarged union", 3, g, 200)
				g = seeded(1, 70)
				g.Children = []drawlist.ModelChild{{Geometry: child(20, 180, 60)}}
				add("transparent cached color retains key", 4, g, 7)
				g = seeded(1, 1)
				g.Children = []drawlist.ModelChild{{Geometry: child(20, 180, 0)}}
				add("transparent cached one loses only key", 5, g, 180)
				g = seeded(100, 1)
				g.Width = 20
				g.LiveFaces = []drawlist.ModelFace{directFace(0, 0, 18, 14, 200, 0, 0)}
				add("live zero follows resized seed", 6, g, 200)
				g = seeded(100, 10)
				cg := seeded(180, 1)
				cg.Width = 20
				cg.Faces = append(cg.Faces, directFace(0, 0, 14, 14, 80, 0, 0))
				g.Children = []drawlist.ModelChild{{Geometry: cg, KeyDelta: 10}}
				add("child seeds before saturated shift", 7, g, 180)
				// Changing only cargo size must change the seed mode on a retained hit.
				g = seeded(100, 1)
				cw, want := int32(16), byte(100)
				if frame >= 2 {
					cw, want = 20, 180
				}
				g.Children = []drawlist.ModelChild{{Geometry: child(cw, 180, 0)}}
				add("per-frame resize survives retained replay", 8, g, want)
				g = seeded(100, 1)
				g.Width = 20
				g.Reveal = &drawlist.ModelReveal{Floor: 1, Line: 2, Below: 250, Band: -1, Above: -1}
				add("reveal reads seeded key", 9, g, 250)

				// Existing ordinary-group hole semantics, paired with the old no-seed path.
				for n, key := range []int32{30, 20, 10} {
					for mode := 0; mode < 2; mode++ {
						g = seeded(100, 20)
						if mode == 0 {
							g.CachedSeed = drawlist.ModelCachedSeed{}
						}
						cg = child(16, 180, key)
						cg.Waterline = drawlist.ModelWaterlineErase
						cg.WaterlineKey = 50
						g.Children = []drawlist.ModelChild{{Geometry: cg}}
						want := byte(100)
						if key > 20 {
							want = 7
						}
						add(fmt.Sprintf("ordinary erased child key%d seed%d", key, mode), 10+n*2+mode, g, want)
					}
				}

				g = seeded(100, 255)
				cg = seeded(180, 250)
				cg.Faces = append(cg.Faces, directFace(0, 0, 14, 14, 80, 240, 240))
				g.Children = []drawlist.ModelChild{{Geometry: cg, KeyDelta: 20}}
				add("cached winner precedes saturating group tie", 16, g, 180)
				g = seeded(100, 0)
				cg = seeded(180, 10)
				g.Children = []drawlist.ModelChild{{Geometry: cg, KeyDelta: -20}}
				add("negative group shift saturates to zero", 17, g, 180)

				g = seeded(100, 20)
				g.Waterline = drawlist.ModelWaterlineErase
				g.WaterlineKey = 254
				cg = seeded(180, 0)
				g.Children = []drawlist.ModelChild{{Geometry: cg, KeyDelta: 32768}}
				add("wide positive delta survives carrier clip", 18, g, 180)
				g = seeded(100, 0)
				g.Waterline = drawlist.ModelWaterlineErase
				g.WaterlineKey = 0
				cg = seeded(180, 255)
				g.Children = []drawlist.ModelChild{{Geometry: cg, KeyDelta: -65535}}
				add("wide negative delta takes carrier clip", 19, g, 7)
				list.RecordExpand()
				img := r.Execute(&list, width, height)
				if img == nil {
					return fmt.Errorf("seed fixture returned no image")
				}
				pixels := make([]byte, width*height*4)
				img.ReadPixels(pixels)
				for _, s := range samples {
					at := (s.y*width + s.x) * 4
					if pixels[at] != s.want || pixels[at+1] != s.want || pixels[at+2] != s.want {
						return fmt.Errorf("seed page2=%v doubled=%v frame%d %s: RGB%v want%d", secondPage, doubled, frame, s.name, pixels[at:at+3], s.want)
					}
				}
				if frame >= 2 && r.modelDirect.placeMode != modelPlacedParallel {
					return fmt.Errorf("seed fixture did not exercise parallel placement")
				}
				if frame == 2 && r.modelStats.DirectRetained == 0 {
					return fmt.Errorf("seed fixture did not exercise retained replay")
				}
				if frame == 3 && r.modelStats.DirectWarm == 0 {
					return fmt.Errorf("seed fixture did not exercise warm append")
				}
				if !secondPage && !doubled && frame == 2 {
					seedStats := r.modelStats
					baseline := list.Clone()
					baseline.VisitModels(func(m drawlist.Model) { clearModelSeedFixture(m.Geometry) })
					bareRenderer, err := NewChecked(&pal, width, height)
					if err != nil {
						return err
					}
					bareRenderer.Execute(&baseline, width, height)
					bareStats := bareRenderer.modelStats
					if bareStats.DirectPasses != 2 || seedStats.DirectPasses != 6 || seedStats.DirectPages != bareStats.DirectPages {
						return fmt.Errorf("seed pass/page cost: seeded %d/%d, absent %d/%d", seedStats.DirectPasses, seedStats.DirectPages, bareStats.DirectPasses, bareStats.DirectPages)
					}
					if seedStats.DirectGroupScratchBytes <= 0 || seedStats.DirectGroupScratchBytes != 8*r.modelDirect.groups.colour.Bounds().Dx()*r.modelDirect.groups.colour.Bounds().Dy() || bareStats.DirectGroupScratchBytes != 0 {
						return fmt.Errorf("seed scratch accounting: seeded%d absent%d", seedStats.DirectGroupScratchBytes, bareStats.DirectGroupScratchBytes)
					}
					if dir := os.Getenv("NANOLATHE_SEED_CAPTURE"); dir != "" {
						if err := os.MkdirAll(dir, 0755); err != nil {
							return err
						}
						f, err := os.Create(filepath.Join(dir, "cached-seed-device.png"))
						if err != nil {
							return err
						}
						err = png.Encode(f, &image.RGBA{Pix: pixels, Stride: width * 4, Rect: image.Rect(0, 0, width, height)})
						closeErr := f.Close()
						if err != nil {
							return err
						}
						if closeErr != nil {
							return closeErr
						}
						fmt.Printf("seed fixture: passes %d -> %d, pages %d -> %d, submitted vertices %d -> %d, merge scratch %d -> %d bytes\n", bareStats.DirectPasses, seedStats.DirectPasses, bareStats.DirectPages, seedStats.DirectPages, bareStats.SubmittedVertices, seedStats.SubmittedVertices, bareStats.DirectGroupScratchBytes, seedStats.DirectGroupScratchBytes)
					}
				}
			}
		}
	}
	return nil
}

func seedDoublePacket(g *drawlist.ModelGeometry) {
	ss := *g
	ss.Width, ss.Height, ss.Scale = g.Width*2, g.Height*2, 2
	ss.Children = nil
	ss.Supersample = nil
	scaleFaces := func(src []drawlist.ModelFace) []drawlist.ModelFace {
		out := make([]drawlist.ModelFace, len(src))
		for i, f := range src {
			out[i] = f
			out[i].Vertices = append([]drawlist.ModelVertex(nil), f.Vertices...)
			for j := range out[i].Vertices {
				out[i].Vertices[j].X *= 2
				out[i].Vertices[j].Y *= 2
			}
		}
		return out
	}
	ss.Faces, ss.LiveFaces = scaleFaces(g.Faces), scaleFaces(g.LiveFaces)
	g.Supersample = &ss
	for _, ch := range g.Children {
		seedDoublePacket(ch.Geometry)
	}
}

func clearModelSeedFixture(g *drawlist.ModelGeometry) {
	if g == nil {
		return
	}
	g.CachedSeed = drawlist.ModelCachedSeed{}
	for _, child := range g.Children {
		clearModelSeedFixture(child.Geometry)
	}
	clearModelSeedFixture(g.Supersample)
}
