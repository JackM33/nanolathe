package gpurender

import (
	"fmt"
	"image"
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// spriteSubject is a flat offered packet: one face of one index over its box.
func spriteSubject(ax, ay, w, h int32, color uint8, body uint64) *drawlist.ModelGeometry {
	g := directSubject(ax, ay, w, h, directFace(0, 0, w, h, color, 20, 20))
	g.Cache = drawlist.ModelCacheKey{Body: body, Revision: 1}
	g.SpriteLevel = 2
	return g
}

// The strategic sprites' placement (§16.13): an offered packet is placed
// through the lane and baked on its first frame and takes no region on the
// next; one under a battle light stays on the lane unbaked; a finish switch
// change bakes again.
func TestStrategicSpritesBakeThenDraw(t *testing.T) {
	skipAfterDeviceLoop(t)
	r := newPlaceRenderer(t)
	r.lighting.lights = r.lighting.lights[:0]
	a := spriteSubject(40, 40, 96, 64, 200, 21)
	b := spriteSubject(300, 200, 64, 64, 120, 22)
	lit := spriteSubject(400, 60, 64, 64, 160, 23)
	r.lighting.lights = append(r.lighting.lights, battleLight{position: [3]float32{430, 90, 10}, color: [3]float32{1, 1, 1}, radius: 40})
	var list drawlist.List
	for _, g := range []*drawlist.ModelGeometry{a, b, lit} {
		list.RecordModel(drawlist.Model{Geometry: g})
	}
	d := &r.modelDirect
	frame := func() {
		r.modelStats = ModelStats{}
		r.placeModelDirectFrame(&list)
	}

	frame()
	if got := r.modelStats.DirectSpriteBakes; got != 2 {
		t.Fatalf("first frame baked %d sprites, want the two unlit subjects", got)
	}
	for _, g := range []*drawlist.ModelGeometry{a, b, lit} {
		if region, ok := d.regions[g]; !ok || !region.ok {
			t.Fatal("a first sighting was not placed through the lane")
		}
	}

	frame()
	if len(d.sprites.draws) != 2 || r.modelStats.DirectSpriteBakes != 0 {
		t.Fatalf("second frame drew %d sprites and baked %d, want 2 and 0", len(d.sprites.draws), r.modelStats.DirectSpriteBakes)
	}
	for _, g := range []*drawlist.ModelGeometry{a, b} {
		if _, placed := d.regions[g]; placed {
			t.Fatal("a sprite took a region on the lane page")
		}
	}
	if _, drawn := d.sprites.draws[lit]; drawn {
		t.Fatal("the subject under a battle light was drawn from a sprite")
	}
	if region, ok := d.regions[lit]; !ok || !region.ok {
		t.Fatal("the subject under a battle light left the lane")
	}

	r.metalGlint = !r.metalGlint
	frame()
	if len(d.sprites.draws) != 0 || r.modelStats.DirectSpriteBakes != 2 {
		t.Fatalf("after a finish switch: %d sprites drawn, %d baked; want 0 and 2", len(d.sprites.draws), r.modelStats.DirectSpriteBakes)
	}
}

// Sprite pages are given up least recently used first, and never one drawn
// from this frame: a view with more sprites than the pages draws the rest
// through the lane rather than evicting what it is drawing.
func TestStrategicSpritePagesEvictLeastRecentUnused(t *testing.T) {
	var s modelSpriteStore
	s.entries = make(map[modelSpriteKey]modelSprite)
	// At level one a box this size is one whole page with its pad.
	box := image.Rect(0, 0, 2*(modelSpritePageSize-2*modelSpritePad), 2*(modelSpritePageSize-2*modelSpritePad))
	s.frame = 1
	for i := range modelSpriteMaxPages {
		sp, ok := s.alloc(box, 1)
		if !ok || sp.page != int32(i) {
			t.Fatalf("sprite %d placed on page %d (ok %v), want a page of its own", i, sp.page, ok)
		}
		s.entries[modelSpriteKey{body: uint64(i + 1), level: 1}] = sp
	}
	if _, ok := s.alloc(box, 1); ok {
		t.Fatal("a sprite evicted a page used this frame")
	}
	s.frame++
	s.pages[0].used = s.frame // page 0 is drawn from this frame
	sp, ok := s.alloc(box, 1)
	if !ok || sp.page != 1 {
		t.Fatalf("eviction chose page %d (ok %v), want page 1, the least recent not drawn this frame", sp.page, ok)
	}
	if _, kept := s.entries[modelSpriteKey{body: 2, level: 1}]; kept {
		t.Fatal("the evicted page's sprite survived")
	}
	if _, kept := s.entries[modelSpriteKey{body: 1, level: 1}]; !kept {
		t.Fatal("a sprite on a page drawn this frame was forgotten")
	}
}

// checkModelSpriteDevicePixels draws two offered packets in a world region at a
// quarter scale (§16.13): the first frame commits them through the lane and
// bakes them, the second draws them from their sprites. A flat face's interior
// is the lane's colour exactly, and its edge is not lost.
func checkModelSpriteDevicePixels() error {
	pal := fixturePalette()
	const w, h = 160, 120
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	a := spriteSubject(40, 40, 96, 64, 200, 21)
	b := spriteSubject(300, 200, 64, 64, 120, 22)
	var list drawlist.List
	list.RecordClear()
	list.RecordWorld(drawlist.WorldSpace{Begin: true, Factor: 0.25, Step: camera.ViewScaleNative, RecordW: 4 * w, RecordH: 4 * h})
	list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: 0, Y: 0, W: 4 * w, H: 4 * h}, Index: 7, Style: drawlist.FillSolid})
	list.RecordModel(drawlist.Model{Geometry: a})
	list.RecordModel(drawlist.Model{Geometry: b})
	list.RecordWorld(drawlist.WorldSpace{})
	list.RecordExpand()
	read := func() []byte {
		p := make([]byte, w*h*4)
		r.Execute(&list, w, h).ReadPixels(p)
		return p
	}
	lane := read()
	if got := r.modelStats; got.DirectSpriteBakes != 2 || got.DirectSprites != 0 {
		return fmt.Errorf("sprite fixture: first frame baked %d and drew %d sprites, want 2 and 0", got.DirectSpriteBakes, got.DirectSprites)
	}
	sprite := read()
	if got := r.modelStats; got.DirectSprites != 2 || got.DirectSubjects != 0 {
		return fmt.Errorf("sprite fixture: second frame drew %d sprites and %d lane subjects, want 2 and 0", got.DirectSprites, got.DirectSubjects)
	}
	// The boxes at a quarter: a is (10,10)-(34,26), b (75,50)-(91,66). Their
	// interiors, a screen pixel in from the edge, are the face's colour.
	ink := func(p []byte, x0, y0, x1, y1 int) int {
		sum := 0
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				sum += int(p[(y*w+x)*4]) - 7
			}
		}
		return sum
	}
	for _, box := range [][4]int{{10, 10, 34, 26}, {75, 50, 91, 66}} {
		for y := box[1] + 1; y < box[3]-1; y++ {
			for x := box[0] + 1; x < box[2]-1; x++ {
				i := (y*w + x) * 4
				if sprite[i] != lane[i] || sprite[i+1] != lane[i+1] || sprite[i+2] != lane[i+2] {
					return fmt.Errorf("sprite fixture: interior pixel (%d,%d) is %v from the sprite, %v from the lane", x, y, sprite[i:i+3], lane[i:i+3])
				}
			}
		}
		// The whole box, a pixel of margin included: the sprite's soft edge
		// spends the face's ink over the same area the lane's hard edge does.
		l, s := ink(lane, box[0]-1, box[1]-1, box[2]+1, box[3]+1), ink(sprite, box[0]-1, box[1]-1, box[2]+1, box[3]+1)
		if s < l*9/10 || s > l*11/10 {
			return fmt.Errorf("sprite fixture: box %v holds %d ink from the sprite, %d from the lane", box, s, l)
		}
	}
	return nil
}

// TestModelSpriteDevicePixels names the strategic sprites' device check; the
// hidden loop in TestMain runs it.
func TestModelSpriteDevicePixels(t *testing.T) {
	if os.Getenv("NANOLATHE_GPU_DEVICE_TEST") != "1" {
		t.Skip("set NANOLATHE_GPU_DEVICE_TEST=1 for real-device sprite fixtures")
	}
	if deviceFixtureResult != nil {
		t.Fatalf("device fixture loop: %v", deviceFixtureResult)
	}
}

// The display palette is rewritten in place (the gamma setting), so the
// strategic caches that bake colour compare its generation: a change drops
// every sprite and every feature layer, and an unchanged one keeps them.
func TestStrategicCachesFollowTheDisplayPalette(t *testing.T) {
	var s modelSpriteStore
	s.entries = map[modelSpriteKey]modelSprite{{body: 1, level: 1}: {ok: true}}
	s.beginFrame(false, false, 0)
	if len(s.entries) != 1 {
		t.Fatal("an unchanged display palette dropped the sprites")
	}
	s.beginFrame(false, false, 1)
	if len(s.entries) != 0 {
		t.Fatal("a display palette change kept the sprites")
	}
	var l featureLayerStore
	l.entries = map[featureLayerKey]featureLayerEntry{{id: 1, revision: 1, level: 2}: {}}
	l.beginFrame(1, nil, 0)
	if len(l.entries) != 1 {
		t.Fatal("an unchanged display palette dropped the feature layers")
	}
	l.beginFrame(2, nil, 1)
	if len(l.entries) != 0 {
		t.Fatal("a display palette change kept the feature layers")
	}
}
