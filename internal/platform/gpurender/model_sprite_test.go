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
	g.Sprite, g.SpriteLevel = true, 2
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

// A frame whose sprites the pages have no room for, on pages that are all in
// view, gives up the page least of which is in view at the start of the next
// frame, when that is at most half of it (§16.13): the pages come to hold what
// is in view rather than what was baked while the view moved. A page more
// than half in view is kept, and a view that is short of room on such pages
// draws the rest through the lane rather than turning its sprites over.
func TestModelSpritePagesRetireWhatIsOutOfView(t *testing.T) {
	var s modelSpriteStore
	s.entries = make(map[modelSpriteKey]modelSprite)
	// At level zero a box this size is a quarter page with its pad.
	side := int(modelSpritePageSize/2 - 2*modelSpritePad)
	box := image.Rect(0, 0, side, side)
	next := uint64(1)
	fill := func(n int) {
		t.Helper()
		for range n {
			sp, ok := s.alloc(box, 0)
			if !ok {
				t.Fatal("a sprite found no room on pages with room")
			}
			s.entries[modelSpriteKey{body: next}] = sp
			next++
		}
	}
	// inView draws n of each page's sprites this frame, and page 5's one.
	inView := func(n int) {
		for p := range int32(modelSpriteMaxPages) {
			want := n
			if p == 5 {
				want = 1
			}
			for _, e := range s.entries {
				if e.page == p && want > 0 {
					s.pages[p].markLive(s.frame, e.w, e.h)
					want--
				}
			}
		}
	}
	s.beginFrame(false, false, false, 0)
	fill(4 * modelSpriteMaxPages)
	s.beginFrame(false, false, false, 0)
	inView(3)
	if _, ok := s.alloc(box, 0); ok {
		t.Fatal("a sprite took room on pages that are all in view")
	}
	s.short = 1
	s.chooseRetirement()
	if !s.retiring || s.retire != 5 {
		t.Fatalf("retiring %v page %d, want page 5, a quarter in view", s.retiring, s.retire)
	}
	before := len(s.entries)
	s.beginFrame(false, false, false, 0)
	if len(s.entries) != before-4 {
		t.Fatalf("%d sprites left of %d, want the retired page's four forgotten", len(s.entries), before)
	}
	fill(4)
	for _, e := range s.entries {
		if e.page == 5 {
			s.pages[5].markLive(s.frame, e.w, e.h)
		}
	}
	for p := range int32(modelSpriteMaxPages) {
		if p != 5 {
			for _, e := range s.entries {
				if e.page == p {
					s.pages[p].markLive(s.frame, e.w, e.h)
				}
			}
		}
	}
	s.short = 1
	s.chooseRetirement()
	if s.retiring {
		t.Fatalf("pages wholly in view retired page %d", s.retire)
	}
}

// A rectangle a page cannot hold leaves its shelves as they were: the open
// shelf keeps its room for a smaller one.
func TestModelSpritePageKeepsItsShelfOnAMiss(t *testing.T) {
	var p modelSpritePage
	var x, y int32
	if !p.place(modelSpritePageSize-100, modelSpritePageSize-50, &x, &y) {
		t.Fatal("an empty page refused a rectangle it holds")
	}
	if p.place(200, 60, &x, &y) {
		t.Fatal("a page placed a rectangle taller than its room")
	}
	if !p.place(100, 40, &x, &y) || x != modelSpritePageSize-100 || y != 0 {
		t.Fatalf("a rectangle that fits the open shelf went to (%d,%d), ok false or not beside the first", x, y)
	}
}

// The sprite pages lie on as few sheets as the device's largest texture side
// allows (§16.13 "Storage"): all eight on one sheet from 8192 up, four to a
// sheet at 4096, and a texture each below that or before the device answers.
func TestModelSpriteSheetLayout(t *testing.T) {
	for _, c := range []struct{ side, cols, rows int }{
		{0, 1, 1}, {2048, 1, 1}, {4096, 2, 2}, {8192, 4, 2}, {16384, 4, 2},
	} {
		cols, rows := modelSpriteSheetLayout(c.side)
		if int(cols) != c.cols || int(rows) != c.rows {
			t.Errorf("side %d: %d×%d pages to a sheet, want %d×%d", c.side, cols, rows, c.cols, c.rows)
		}
	}
}

// A page's sprites are placed in its own region of its sheet, and a page given
// up starts over in that region (§16.13 "Storage").
func TestModelSpritePagesTileTheirSheets(t *testing.T) {
	skipAfterDeviceLoop(t)
	for _, side := range []int{8192, 4096} {
		var s modelSpriteStore
		s.entries = make(map[modelSpriteKey]modelSprite)
		s.cols, s.rows = modelSpriteSheetLayout(side)
		perSheet := s.cols * s.rows
		// At level one a box this size is one whole page with its pad.
		box := image.Rect(0, 0, 2*(modelSpritePageSize-2*modelSpritePad), 2*(modelSpritePageSize-2*modelSpritePad))
		s.frame = 1
		for i := range int32(modelSpriteMaxPages) {
			sp, ok := s.alloc(box, 1)
			if !ok || sp.page != i {
				t.Fatalf("side %d: sprite %d placed on page %d (ok %v), want a page of its own", side, i, sp.page, ok)
			}
			at := i % perSheet
			if sp.x != at%s.cols*modelSpritePageSize || sp.y != at/s.cols*modelSpritePageSize {
				t.Fatalf("side %d: page %d's sprite at (%d,%d), want its page's origin", side, i, sp.x, sp.y)
			}
			s.ensureSheet(sp.page)
			if s.pages[i].img != s.sheets[i/perSheet] {
				t.Fatalf("side %d: page %d is not on sheet %d", side, i, i/perSheet)
			}
			if b := s.pages[i].img.Bounds(); b.Dx() != int(s.cols*modelSpritePageSize) || b.Dy() != int(s.rows*modelSpritePageSize) {
				t.Fatalf("side %d: sheet is %v, want %d×%d pages", side, b, s.cols, s.rows)
			}
		}
		if want := modelSpriteMaxPages / int(perSheet); len(s.sheets) != want {
			t.Fatalf("side %d: %d sheets, want %d", side, len(s.sheets), want)
		}
		s.frame++
		for i := range s.pages {
			s.pages[i].used = s.frame
		}
		s.pages[5].used = 1
		sp, ok := s.alloc(box, 1)
		at := int32(5) % perSheet
		if !ok || sp.page != 5 || sp.x != at%s.cols*modelSpritePageSize || sp.y != at/s.cols*modelSpritePageSize {
			t.Fatalf("side %d: after giving up page 5 a sprite went to page %d at (%d,%d), want page 5's origin", side, sp.page, sp.x, sp.y)
		}
	}
}

// Wrecks drawn from sprites on different pages of one sheet share a device
// run however painter order alternates between the pages; with a texture to
// each page every alternation ended the run (§16.13 "Storage").
func TestModelSpritesOnOneSheetShareARun(t *testing.T) {
	skipAfterDeviceLoop(t)
	runs := func(side int) int {
		r := newPlaceRenderer(t)
		s := &r.modelDirect.sprites
		s.entries = make(map[modelSpriteKey]modelSprite)
		s.cols, s.rows = modelSpriteSheetLayout(side)
		box := image.Rect(0, 0, 2*(modelSpritePageSize-2*modelSpritePad), 2*(modelSpritePageSize-2*modelSpritePad))
		var on [2]modelSprite
		for i := range on {
			sp, ok := s.alloc(box, 1)
			if !ok {
				t.Fatal("no room for a page-sized sprite on empty pages")
			}
			s.ensureSheet(sp.page)
			on[i] = modelSprite{page: sp.page, x: sp.x + 1, y: sp.y + 1, w: 18, h: 18, ok: true}
		}
		r.sched.resetFrame(640, 480)
		for i := range 6 {
			r.commitModelSprite(spriteSubject(int32(10+40*i), 10, 16, 16, 120, uint64(i+1)), on[i%2])
		}
		if r.sched.nphase != 1 {
			t.Fatalf("side %d: six opaque sprites compiled %d phases, want 1", side, r.sched.nphase)
		}
		return len(r.sched.phases[0].batch[schedOpaque].runs)
	}
	if got := runs(8192); got != 1 {
		t.Fatalf("sprites alternating between two pages of one sheet compiled %d runs, want 1", got)
	}
	if got := runs(2048); got != 6 {
		t.Fatalf("sprites alternating between two textures compiled %d runs, want 6: the fixture no longer alternates", got)
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
	s.beginFrame(false, false, false, 0)
	if len(s.entries) != 1 {
		t.Fatal("an unchanged display palette dropped the sprites")
	}
	s.beginFrame(false, false, false, 1)
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

// shadowedSpriteSubject is a level-zero offer with a structure shadow: a
// doubled lane whose edges fall on odd texels, so its border pixels are
// partly covered, and a shadow reaching right and below the body, so part of
// it lies under the body and part beside it.
func shadowedSpriteSubject(ax, ay int32, body uint64) *drawlist.ModelGeometry {
	g := directSubject(ax, ay, 24, 16, directFace(0, 0, 24, 16, 150, 50, 50))
	g.Supersample = &drawlist.ModelGeometry{Eligible: true, KeyPlane: true, Scale: 2, Width: 48, Height: 32, Faces: []drawlist.ModelFace{
		directFace(1, 1, 45, 29, 150, 50, 50),
		directFace(5, 3, 21, 15, 180, 60, 60),
		{Color: 60, Vertices: []drawlist.ModelVertex{{X: 26, Y: 5, Key: 70}, {X: 45, Y: 5, Key: 70}, {X: 26, Y: 28, Key: 70}}},
	}}
	g.Cache = drawlist.ModelCacheKey{Body: body, Revision: 1, HalfX: 1, HalfY: 1}
	g.Sprite = true
	sg := directSubject(ax+9, ay+7, 26, 12, directFace(0, 0, 26, 12, fixtureShadowIndex, 10, 10))
	sg.Supersample = &drawlist.ModelGeometry{Eligible: true, KeyPlane: true, Scale: 2, Width: 52, Height: 24, Faces: []drawlist.ModelFace{
		directFace(1, 1, 49, 21, fixtureShadowIndex, 10, 10),
		{Color: fixtureShadowIndex, Vertices: []drawlist.ModelVertex{{X: 3, Y: 21, Key: 10}, {X: 49, Y: 21, Key: 10}, {X: 49, Y: 23, Key: 10}}},
	}}
	sg.Cache = drawlist.ModelCacheKey{Body: body, Revision: 1, HalfX: 1, HalfY: 1, Lane: drawlist.ModelCacheLaneShadow}
	g.Shadow = sg
	return g
}

// Level-zero sprites carry their shadows (§16.13): a shadowed offer is placed
// through the lane with its shadow and both are baked on its first frame;
// from the next neither takes a region. A shadow whose sprite was given up
// brings the body back to the lane with it, and only the shadow bakes again;
// a shadow projected per frame keeps its body on the lane; the Supersample
// switch's resolve bakes everything again.
func TestModelSpritesCarryTheirShadows(t *testing.T) {
	skipAfterDeviceLoop(t)
	r := newPlaceRenderer(t)
	r.lighting.lights = r.lighting.lights[:0]
	w := shadowedSpriteSubject(40, 40, 31)
	loose := shadowedSpriteSubject(200, 40, 32)
	loose.Shadow.Cache = drawlist.ModelCacheKey{}
	var list drawlist.List
	for _, g := range []*drawlist.ModelGeometry{w, loose} {
		list.RecordModel(drawlist.Model{Geometry: g})
	}
	d := &r.modelDirect
	frame := func() {
		r.modelStats = ModelStats{}
		r.placeModelDirectFrame(&list)
	}
	placed := func(g *drawlist.ModelGeometry) bool {
		region, ok := d.regions[g]
		return ok && region.ok
	}

	frame()
	if got := r.modelStats.DirectSpriteBakes; got != 2 {
		t.Fatalf("first frame baked %d sprites, want the offer's body and shadow", got)
	}
	if !placed(w) || !placed(w.Shadow) {
		t.Fatal("a first sighting's body or shadow was not placed through the lane")
	}

	frame()
	sd, drawn := d.sprites.draws[w]
	if !drawn || !sd.body.ok || !sd.shadow.ok || r.modelStats.DirectSpriteBakes != 0 {
		t.Fatalf("second frame: drawn %v with body %v and shadow %v, %d baked; want both drawn and none baked",
			drawn, sd.body.ok, sd.shadow.ok, r.modelStats.DirectSpriteBakes)
	}
	if _, ok := d.regions[w]; ok {
		t.Fatal("a sprite took a region on the lane page")
	}
	if _, ok := d.regions[w.Shadow]; ok {
		t.Fatal("a sprite's shadow took a region on the lane page")
	}
	if _, ok := d.sprites.draws[loose]; ok || !placed(loose) || !placed(loose.Shadow) {
		t.Fatal("a packet whose shadow is projected per frame left the lane")
	}

	delete(d.sprites.entries, modelSpriteKeyOf(w.Shadow, 0))
	frame()
	if _, ok := d.sprites.draws[w]; ok || !placed(w) || !placed(w.Shadow) {
		t.Fatal("a body whose shadow's sprite was given up was drawn from its sprite")
	}
	if got := r.modelStats.DirectSpriteBakes; got != 1 {
		t.Fatalf("baked %d sprites, want the shadow alone", got)
	}

	r.setSupersample(false)
	frame()
	if _, ok := d.sprites.draws[w]; ok || r.modelStats.DirectSpriteBakes != 2 {
		t.Fatalf("after the resolve changed: drawn %v, %d baked; want the lane and both baked again", ok, r.modelStats.DirectSpriteBakes)
	}
}

// checkModelSpriteShadowDevicePixels draws shadowed level-zero offers through
// the lane, then from their sprites, and requires the same picture (§16.13):
// at the native step every pixel within one level of the lane's — the
// interiors exact, a partly covered edge rounded once more — and with the
// Supersample switch's single-sample resolve every pixel exact; at a
// fractional zoom, where the sample positions are interpolated across
// different quads, all but a sliver of pixels.
func checkModelSpriteShadowDevicePixels() error {
	pal := fixturePalette()
	const w, h, field = 96, 64, 100
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	a := shadowedSpriteSubject(10, 10, 31)
	b := shadowedSpriteSubject(50, 30, 32)
	plain := spriteSubject(70, 6, 20, 14, 120, 33)
	plain.SpriteLevel = 0
	build := func(factor float32) drawlist.List {
		var list drawlist.List
		list.RecordClear()
		rw, rh := int32(w), int32(h)
		if factor != 1 {
			rw, rh = int32(float32(w)/factor), int32(float32(h)/factor)
			list.RecordWorld(drawlist.WorldSpace{Begin: true, Factor: factor, Step: camera.ViewScaleNative, RecordW: rw, RecordH: rh})
		}
		list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: 0, Y: 0, W: rw, H: rh}, Index: field, Style: drawlist.FillSolid})
		for _, g := range []*drawlist.ModelGeometry{a, b, plain} {
			list.RecordModel(drawlist.Model{Geometry: g})
		}
		if factor != 1 {
			list.RecordWorld(drawlist.WorldSpace{})
		}
		list.RecordExpand()
		return list
	}
	read := func(list *drawlist.List) []byte {
		p := make([]byte, w*h*4)
		r.Execute(list, w, h).ReadPixels(p)
		return p
	}
	compare := func(what string, list drawlist.List, tolerance int, maxOff int) error {
		lane := read(&list)
		if got := r.modelStats; got.DirectSpriteBakes != 5 || got.DirectSprites != 0 {
			return fmt.Errorf("sprite shadow fixture, %s: first frame baked %d and drew %d sprites, want 5 and 0", what, got.DirectSpriteBakes, got.DirectSprites)
		}
		sprite := read(&list)
		if got := r.modelStats; got.DirectSprites != 3 || got.DirectSpriteShadows != 2 || got.DirectSubjects != 0 || got.DirectShadows != 0 {
			return fmt.Errorf("sprite shadow fixture, %s: second frame drew %d sprites, %d sprite shadows, %d lane subjects and %d lane shadows; want 3, 2, 0, 0",
				what, got.DirectSprites, got.DirectSpriteShadows, got.DirectSubjects, got.DirectShadows)
		}
		off, shaded := 0, 0
		for i := 0; i < len(lane); i += 4 {
			if lane[i] != lane[0] {
				shaded++
			}
			for c := range 3 {
				if diff := int(sprite[i+c]) - int(lane[i+c]); diff > tolerance || diff < -tolerance {
					off++
					break
				}
			}
		}
		if shaded == 0 {
			return fmt.Errorf("sprite shadow fixture, %s: the lane drew nothing over the field", what)
		}
		if off > maxOff {
			return fmt.Errorf("sprite shadow fixture, %s: %d pixels differ from the lane's by more than %d, want at most %d", what, off, tolerance, maxOff)
		}
		return nil
	}
	if err := compare("native", build(1), 1, 0); err != nil {
		return err
	}
	r.setSupersample(false)
	if err := compare("single sample", build(1), 0, 0); err != nil {
		return err
	}
	r.setSupersample(true)
	return compare("three quarters", build(0.75), 1, w*h/100)
}
