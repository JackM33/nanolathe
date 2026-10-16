package gpurender

import (
	"image"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// Model sprites (docs/DESIGN_GPU_RENDERER.md §16.13). A battlefield's wrecks are
// most of the model lane's work, and none of it changes from one frame to the
// next: each one, and its shadow, is rasterized onto the frame's atlas page,
// every face of it, to be resolved under the same pixels as the frame before. A
// packet the recorder offers (ModelGeometry.Sprite) is drawn once through the
// lane as ever, and its finished region is then resolved onto a retained sprite
// page at the offered level, 2^-level texels per record pixel. From the next
// frame the packet takes no region and no faces: its commit is one quad from
// the sprite page, in the commit's own place in record order
// (sceneOpModelSprite).
//
// Level zero, offered outside the strategic view, is the commit's own resolve:
// a sprite texel is a record pixel, the mean of its covered 2×2 block and their
// share, so the quad draws what the commit would. The shadow comes with it as a
// sprite of its own, the silhouette resolved the same way, and its commit punches
// the pixels whose body texel is wholly covered and composites the ALP
// half-colour, as the lane's shadow commit does (sceneOpModelSpriteShadow).
// The strategic view's levels average every texel of the doubled raster a
// sprite texel covers — sixteen at level one, a thousand at level four — so a
// sprite is the subject supersampled at the size it is seen, where the lane's
// commit takes the one pixel under each screen pixel; that view casts no
// feature shadow.
//
// A bake reads the colour page the commit reads, after the page passes, so a
// sprite is the subject's own finished colour: palette, shading, finish and
// glint. Two inputs of that colour are per frame and are kept out: a packet with
// a battle light near it is neither baked nor drawn from its sprite while the
// light lasts, and the finish switches drop every sprite when they change, as
// they drop the retained store; so do a change of display palette (the gamma
// setting), which the lane resolves every frame, and of the Supersample
// switch's resolve.
//
// A sprite is keyed on the packet's cache key and its level. A positive level's
// key leaves out the half-pixel offset — half a record pixel, at most a quarter
// of a sprite texel — and level zero's keeps it, since there it is half a
// texel. The pages are shelf-packed and evicted a page at a time, least
// recently drawn first; a page drawn from this frame is never evicted. When a
// frame cannot bake for want of room on pages that are all in view, the page
// least of which is in view is given up at the start of the next frame, if
// that is at most half of it: its sprites in view go through the lane once and
// bake again into the room, so the pages come to hold what is in view rather
// than what was baked while the view moved. A view needing more than the pages
// draws the rest through the lane.
//
// The pages are regions of one texture, a sheet, where the device's largest
// texture side allows. Every sprite and sprite shadow binds its page's texture
// in the same scene slot, and a device draw binds one texture there, so when
// each page was a texture of its own, a sprite whose page differed from the
// sprite before it in painter order ended the run. The pages come to hold
// sprites baked at different times, so a view's wrecks lie on every page in no
// order: after a long battle's panning a zoomed-out frame split into a device
// draw per wreck, two thousand and more, and the backend's fixed cost per draw
// took the frame rate into the sixties. On one sheet they are one run.
const (
	modelSpritePageSize = 2048
	// modelSpriteMaxPages bounds the sprite pages at 128 MiB. Level zero is a
	// texel per record pixel: at 0.44×, near the strategic cut, one screen of a
	// dense wreck field in a 1600×1034 window takes about 2.3 pages, and a
	// larger window or a wider factor proportionally more.
	modelSpriteMaxPages = 8
	// modelSpritePad is the transparent border each sprite is baked with, so
	// the bilinear draw fades its edge rather than reading a neighbour.
	modelSpritePad = 1
	// modelSpriteSheetColumns bounds a sheet's width at four pages, 8192
	// texels: two rows of four hold all eight pages.
	modelSpriteSheetColumns = 4
)

// modelSpriteSheetLayout is the columns and rows of pages one sheet holds on a
// device whose largest texture side is maxSide: all eight pages from 8192 up,
// four from 4096, and below that, or before the device has answered (0), one
// page to a texture.
func modelSpriteSheetLayout(maxSide int) (cols, rows int32) {
	n := max(int32(maxSide/modelSpritePageSize), 1)
	cols = min(n, modelSpriteSheetColumns)
	rows = min(n, (modelSpriteMaxPages+cols-1)/cols)
	return cols, rows
}

type modelSpriteKey struct {
	body, revision uint64
	halfX, halfY   int32
	lane           drawlist.ModelCacheLane
	level          uint8
}

// modelSprite is one baked sprite: its page, its padded rectangle in the
// texels of the page's texture, and the record box it covers.
type modelSprite struct {
	page       int32
	x, y, w, h int32
	// bw, bh is the subject's world box the sprite covers, in record pixels.
	bw, bh int32
	level  uint8
	ok     bool
}

type modelSpritePage struct {
	// img is the page's texture and ox, oy the page's origin on it: a model
	// sprite page's sheet, which it shares with other pages, or a texture of
	// its own at the origin. The shelves are in the page's own texels.
	img                    *ebiten.Image
	ox, oy                 int32
	shelfX, shelfY, shelfH int32
	// used is the last frame a sprite on this page was drawn or baked.
	used uint64
	// live is the texels of the sprites drawn from the page on frame
	// liveFrame, the measure of how much of it is in view.
	live      int64
	liveFrame uint64
}

// modelSpriteDraw is one packet drawn from its sprites this frame: its body's
// and, at level zero, its shadow's (shadow.ok false for a packet without one).
type modelSpriteDraw struct {
	body, shadow modelSprite
}

// modelSpriteBake is one bake queued for the page goroutine: the subject's
// region on the lane page and the sprite it fills.
type modelSpriteBake struct {
	region modelDirectRegion
	sprite modelSprite
}

type modelSpriteStore struct {
	pages []modelSpritePage
	// sheets are the textures the pages lie on, cols×rows pages to each; the
	// layout is fixed when the first sprite is placed (modelSpriteSheetLayout).
	sheets     []*ebiten.Image
	cols, rows int32
	entries    map[modelSpriteKey]modelSprite
	// draws are this frame's packets drawn from a sprite, and pending the
	// offered packets placed through the lane that bake once placed.
	draws   map[*drawlist.ModelGeometry]modelSpriteDraw
	pending []*drawlist.ModelGeometry
	bakes   []modelSpriteBake
	frame   uint64
	shader  *ebiten.Shader
	opts    ebiten.DrawTrianglesShaderOptions
	verts   []ebiten.Vertex
	idx     []uint32
	// metalGlint and materials are the finish switches the sprites were baked
	// under, single the Supersample switch's single-sample resolve and palette
	// the display palette generation; a change drops them all.
	metalGlint, materials, single bool
	palette                       uint64
	// short is this frame's sprites the pages had no room for, and retire,
	// when retiring, the page given up at the start of the next frame.
	short    int
	retire   int32
	retiring bool
}

// modelSpriteKeyOf is the key of packet g's sprite at level: g is the body
// packet or its shadow, which takes the body's level.
func modelSpriteKeyOf(g *drawlist.ModelGeometry, level uint8) modelSpriteKey {
	k := modelSpriteKey{body: g.Cache.Body, revision: g.Cache.Revision, lane: g.Cache.Lane, level: level}
	if level == 0 {
		k.halfX, k.halfY = g.Cache.HalfX, g.Cache.HalfY
	}
	return k
}

// beginFrame starts a frame's sprite bookkeeping and drops every sprite when
// the finish switches, the resolve or the display palette changed.
func (s *modelSpriteStore) beginFrame(metalGlint, materials, single bool, palette uint64) {
	s.frame++
	if s.draws == nil {
		s.draws = make(map[*drawlist.ModelGeometry]modelSpriteDraw)
	} else {
		clear(s.draws)
	}
	s.pending, s.bakes, s.short = s.pending[:0], s.bakes[:0], 0
	if s.retiring {
		s.retiring = false
		s.forgetPage(s.retire)
		s.pages[s.retire].reset()
	}
	if s.metalGlint != metalGlint || s.materials != materials || s.single != single || s.palette != palette {
		s.metalGlint, s.materials, s.single, s.palette = metalGlint, materials, single, palette
		s.dropAll()
	}
}

// dropAll forgets every sprite and empties the pages.
func (s *modelSpriteStore) dropAll() {
	clear(s.entries)
	for i := range s.pages {
		s.pages[i].reset()
	}
	s.retiring = false
}

// forgetPage forgets the sprites a page held.
func (s *modelSpriteStore) forgetPage(page int32) {
	for k, e := range s.entries {
		if e.page == page {
			delete(s.entries, k)
		}
	}
}

// markLive counts a w×h sprite drawn from the page this frame.
func (p *modelSpritePage) markLive(frame uint64, w, h int32) {
	if p.liveFrame != frame {
		p.live, p.liveFrame = 0, frame
	}
	p.live += int64(w) * int64(h)
	p.used = frame
}

// modelSpriteOffered reports whether the lane may draw g from a sprite: the
// recorder's offer, and the packet shapes a single region commit draws whole.
// Only level zero carries a shadow, and only a structure's projected one,
// which is a region of its own with a retained key.
func modelSpriteOffered(g *drawlist.ModelGeometry) bool {
	if !g.Sprite || !g.Cache.Reusable() || len(g.Children) != 0 ||
		len(g.LiveFaces) != 0 || len(g.Outline) != 0 || g.Reveal != nil || g.ReflectWater ||
		g.Waterline != drawlist.ModelWaterlineNone || g.Digger || g.Cloaked ||
		g.WreckEmission != ([3]float32{}) {
		return false
	}
	sg := g.Shadow
	return sg == nil || g.SpriteLevel == 0 && !sg.Silhouette && sg.Cache.Reusable() &&
		len(sg.Children) == 0 && len(sg.LiveFaces) == 0 && len(sg.Outline) == 0 && !modelWorldBounds(sg).Empty()
}

// spriteDraw decides, for one offered packet before placement, whether this
// frame draws it from its sprites (true) or places it through the lane
// (false), queueing the bakes for the latter. A packet with a shadow is drawn
// from sprites only when both are held, so its shadow commit always has the
// body's sprite to punch against. A battle light near the subject or its
// shadow keeps it on the lane and unbaked: its colour pass is lit this frame.
func (r *Renderer) spriteDraw(g *drawlist.ModelGeometry) bool {
	s := &r.modelDirect.sprites
	if s.shader == nil || !modelSpriteOffered(g) {
		return false
	}
	wb := modelWorldBounds(g)
	if wb.Empty() {
		return false
	}
	lb := wb
	if g.Shadow != nil {
		lb = lb.Union(modelWorldBounds(g.Shadow))
	}
	if lights := r.lighting.near(float32(lb.Min.X+lb.Max.X)*0.5, float32(lb.Min.Y+lb.Max.Y)*0.5, float32(lb.Dx()+lb.Dy())*0.5); lights.count != 0 {
		return false
	}
	body, ok := s.held(g, g.SpriteLevel)
	draw := modelSpriteDraw{body: body}
	if ok && g.Shadow != nil {
		draw.shadow, ok = s.held(g.Shadow, g.SpriteLevel)
	}
	if !ok {
		s.pending = append(s.pending, g)
		return false
	}
	s.draws[g] = draw
	s.pages[draw.body.page].markLive(s.frame, draw.body.w, draw.body.h)
	if draw.shadow.ok {
		s.pages[draw.shadow.page].markLive(s.frame, draw.shadow.w, draw.shadow.h)
	}
	return true
}

// held is packet g's sprite at level, when one is held for its key at its
// current size.
func (s *modelSpriteStore) held(g *drawlist.ModelGeometry, level uint8) (modelSprite, bool) {
	wb := modelWorldBounds(g)
	e, ok := s.entries[modelSpriteKeyOf(g, level)]
	if !ok || e.bw != int32(wb.Dx()) || e.bh != int32(wb.Dy()) {
		return modelSprite{}, false
	}
	return e, true
}

// planSpriteBakes allocates a sprite for every offered packet the frame placed
// on a page and queues its bake. A packet the pages cannot take this frame is
// offered again on the next.
func (r *Renderer) planSpriteBakes() {
	d := &r.modelDirect
	s := &d.sprites
	for _, g := range s.pending {
		r.planSpriteBake(g, g.SpriteLevel)
		if g.Shadow != nil {
			r.planSpriteBake(g.Shadow, g.SpriteLevel)
		}
	}
	r.modelStats.DirectSpriteBakes = len(s.bakes)
	r.modelStats.DirectSpriteShort = s.short
	r.modelStats.DirectSpritePages = len(s.pages)
	if s.short != 0 {
		s.chooseRetirement()
	}
}

// chooseRetirement picks, on a frame the pages had no room for, the page whose
// sprites in view cover the least of it, and gives it up at the start of the
// next frame if that is at most half the page. A page more than half in view
// is kept: a view that needs more than the pages draws the rest through the
// lane rather than turning its sprites over every frame.
func (s *modelSpriteStore) chooseRetirement() {
	best, least := -1, int64(0)
	for i := range s.pages {
		p := &s.pages[i]
		live := p.live
		if p.liveFrame != s.frame {
			live = 0
		}
		if best < 0 || live < least {
			best, least = i, live
		}
	}
	if best >= 0 && 2*least <= modelSpritePageSize*modelSpritePageSize {
		s.retire, s.retiring = int32(best), true
	}
}

// planSpriteBake allocates and queues the bake of packet g's sprite at level,
// a body or a shadow, unless one is held or the lane did not place it. A held
// one's page counts as used this frame, so baking its partner cannot give it
// up.
func (r *Renderer) planSpriteBake(g *drawlist.ModelGeometry, level uint8) {
	d := &r.modelDirect
	s := &d.sprites
	region, ok := d.regions[g]
	if !ok || !region.ok {
		return
	}
	key := modelSpriteKeyOf(g, level)
	if e, done := s.entries[key]; done {
		s.pages[e.page].markLive(s.frame, e.w, e.h)
		return
	}
	sp, ok := s.alloc(region.bounds, level)
	if !ok {
		if sp.w <= modelSpritePageSize && sp.h <= modelSpritePageSize {
			s.short++
		}
		return
	}
	// The page's texture exists before the page goroutine starts: the
	// executing goroutine reads it while the bakes run (bindsSpritePage).
	s.ensureSheet(sp.page)
	if s.entries == nil {
		s.entries = make(map[modelSpriteKey]modelSprite)
	}
	s.entries[key] = sp
	s.bakes = append(s.bakes, modelSpriteBake{region: region, sprite: sp})
}

// alloc reserves a padded sprite for a world box at level on the shelf packer.
// A full set of pages gives up the least recently used page this frame has not
// drawn from: its sprites are forgotten and its shelves start over.
func (s *modelSpriteStore) alloc(bounds image.Rectangle, level uint8) (modelSprite, bool) {
	shift := int32(level)
	w := (int32(bounds.Dx())+(1<<shift)-1)>>shift + 2*modelSpritePad
	h := (int32(bounds.Dy())+(1<<shift)-1)>>shift + 2*modelSpritePad
	sp := modelSprite{w: w, h: h, bw: int32(bounds.Dx()), bh: int32(bounds.Dy()), level: level, ok: true}
	page, x, y, evicted, ok := allocSpritePage(&s.pages, w, h, s.frame, modelSpriteMaxPages)
	if !ok {
		// The size is kept so the caller can tell a sprite no page could ever
		// hold from one the pages have no room for.
		return modelSprite{w: w, h: h}, false
	}
	if evicted >= 0 {
		s.forgetPage(int32(evicted))
	}
	ox, oy := s.pageOrigin(page)
	sp.page, sp.x, sp.y = page, ox+x, oy+y
	return sp, true
}

// pageOrigin is page's origin on its sheet. The first call fixes the sheets'
// layout from the device's largest texture side.
func (s *modelSpriteStore) pageOrigin(page int32) (ox, oy int32) {
	if s.cols == 0 {
		s.cols, s.rows = modelSpriteSheetLayout(ebiten.MaxImageSize())
	}
	at := page % (s.cols * s.rows)
	return at % s.cols * modelSpritePageSize, at / s.cols * modelSpritePageSize
}

// ensureSheet allocates the sheet page lies on at its first use, on the
// executing goroutine during placement (planSpriteBakes), and binds the page
// to its region of it.
func (s *modelSpriteStore) ensureSheet(page int32) {
	p := &s.pages[page]
	if p.img != nil {
		return
	}
	sheet := int(page / (s.cols * s.rows))
	for len(s.sheets) <= sheet {
		s.sheets = append(s.sheets, nil)
	}
	if s.sheets[sheet] == nil {
		s.sheets[sheet] = ebiten.NewImageWithOptions(image.Rect(0, 0, int(s.cols*modelSpritePageSize), int(s.rows*modelSpritePageSize)), &ebiten.NewImageOptions{Unmanaged: true})
	}
	p.img = s.sheets[sheet]
	p.ox, p.oy = s.pageOrigin(page)
}

// allocSpritePage places a w×h rectangle on a set of retained pages, shelf
// packed: on the first page with room, else a new page while there are fewer
// than maxPages, else the least recently used page not used this frame,
// emptied, whose index it reports as evicted (otherwise -1) so its owner
// forgets what the page held. A page used this frame is never given up.
func allocSpritePage(pages *[]modelSpritePage, w, h int32, frame uint64, maxPages int) (page, x, y int32, evicted int, ok bool) {
	evicted = -1
	if w > modelSpritePageSize || h > modelSpritePageSize {
		return 0, 0, 0, evicted, false
	}
	ps := *pages
	for i := range ps {
		if ps[i].place(w, h, &x, &y) {
			ps[i].used = frame
			return int32(i), x, y, evicted, true
		}
	}
	victim := -1
	if len(ps) < maxPages {
		ps = append(ps, modelSpritePage{})
		*pages = ps
		victim = len(ps) - 1
	} else {
		for i := range ps {
			if ps[i].used != frame && (victim < 0 || ps[i].used < ps[victim].used) {
				victim = i
			}
		}
		if victim < 0 {
			return 0, 0, 0, evicted, false
		}
		ps[victim].reset()
		evicted = victim
	}
	if !ps[victim].place(w, h, &x, &y) {
		return 0, 0, 0, evicted, false
	}
	ps[victim].used = frame
	return int32(victim), x, y, evicted, true
}

// reset empties a page for reuse. Its region of its texture is cleared as well
// as its shelves: a bake writes only its own rectangle, and the bilinear draw's
// edge tap reaches the texel beside it, which must be transparent rather than an
// evicted sprite's. A page on a sheet clears its own region alone, so the
// pages beside it keep their sprites.
func (p *modelSpritePage) reset() {
	p.shelfX, p.shelfY, p.shelfH = 0, 0, 0
	p.live, p.liveFrame = 0, 0
	if p.img == nil {
		return
	}
	region := image.Rect(int(p.ox), int(p.oy), int(p.ox)+modelSpritePageSize, int(p.oy)+modelSpritePageSize)
	if p.img.Bounds() == region {
		p.img.Clear()
		return
	}
	p.img.SubImage(region).(*ebiten.Image).Clear()
}

// place puts a w×h rectangle on the page's shelves, at (x, y). A rectangle
// that does not fit leaves the shelves as they were, so the open shelf keeps
// its room for a smaller one.
func (p *modelSpritePage) place(w, h int32, x, y *int32) bool {
	sx, sy, sh := p.shelfX, p.shelfY, p.shelfH
	if sx+w > modelSpritePageSize {
		sx, sy, sh = 0, sy+sh, 0
	}
	if sy+h > modelSpritePageSize {
		return false
	}
	*x, *y = sx, sy
	p.shelfX, p.shelfY, p.shelfH = sx+w, sy, max(sh, h)
	return true
}

// ensureImage allocates a page's own texture on first use: a feature layer
// page's (bakeFeatureLayer).
func (p *modelSpritePage) ensureImage() *ebiten.Image {
	if p.img == nil {
		p.img = ebiten.NewImageWithOptions(image.Rect(0, 0, modelSpritePageSize, modelSpritePageSize), &ebiten.NewImageOptions{Unmanaged: true})
	}
	return p.img
}

// bindsSpritePage reports whether img is a sprite sheet.
func (s *modelSpriteStore) bindsSpritePage(img *ebiten.Image) bool {
	for _, sheet := range s.sheets {
		if sheet != nil && sheet == img {
			return true
		}
	}
	return false
}

// bakeModelSprites averages each queued bake's region of the lane's colour page
// into its sprite, after the page passes have finished the region. It runs on
// the page goroutine with the passes, and the commits that bind a sprite page
// join it first (bindsModelPage). One draw per source page and sprite sheet.
func (r *Renderer) bakeModelSprites(a *deviceAcct) {
	d := &r.modelDirect
	s := &d.sprites
	if len(s.bakes) == 0 || s.shader == nil {
		return
	}
	s.opts.Blend = ebiten.BlendCopy
	// Level zero takes the commit's resolve, so the Supersample switch's
	// single-sample resolve takes the block's top-left texel alone (§17.5).
	single := float32(0)
	if s.single {
		single = 1
	}
	for src := range d.pages {
		for _, img := range s.sheets {
			s.verts, s.idx = s.verts[:0], s.idx[:0]
			for i := range s.bakes {
				b := &s.bakes[i]
				if int(b.region.page) != src || s.pages[b.sprite.page].img != img {
					continue
				}
				// Sprite texel (i, j) of the padded rectangle averages the
				// k×k doubled texels from (A + k·(i, j)), where A puts the
				// pad's first texel k texels before the region's origin; the
				// region's own texels bound what is read.
				sp, rg := b.sprite, b.region
				k := float32(int32(2) << sp.level)
				point := float32(0)
				if sp.level == 0 {
					point = single
				}
				ax := float32(rg.x) - k*float32(modelSpritePad) - k*float32(sp.x)
				ay := float32(rg.y) - k*float32(modelSpritePad) - k*float32(sp.y)
				base := uint32(len(s.verts))
				for _, c := range [4][2]int32{{0, 0}, {sp.w, 0}, {sp.w, sp.h}, {0, sp.h}} {
					s.verts = append(s.verts, ebiten.Vertex{
						DstX: float32(sp.x + c[0]), DstY: float32(sp.y + c[1]),
						ColorR: float32(rg.x), ColorG: float32(rg.y),
						ColorB: float32(rg.x) + 2*float32(rg.bounds.Dx()), ColorA: float32(rg.y) + 2*float32(rg.bounds.Dy()),
						Custom0: ax, Custom1: ay, Custom2: k, Custom3: point,
					})
				}
				s.idx = append(s.idx, base, base+1, base+2, base, base+2, base+3)
			}
			if len(s.idx) == 0 {
				continue
			}
			s.opts.Images = [4]*ebiten.Image{d.pages[src].colour}
			a.beginPass(img)
			a.submitted(len(s.verts), len(s.idx))
			img.DrawTrianglesShader32(s.verts, s.idx, s.shader, &s.opts)
		}
	}
}

// commitModelSprite is commitModelDirect for a packet drawn from its sprite:
// one quad over the padded sprite's world rectangle, in the commit's place in
// record order, that the world transform shrinks to the screen. A positive
// level is sampled bilinearly. Level zero samples as the commit does: the
// texel under the pixel, which is the commit's resolve of the block under it,
// or bilinearly between the four nearest where the commit's fractional-zoom
// filter does (§16.3).
func (r *Renderer) commitModelSprite(g *drawlist.ModelGeometry, sp modelSprite) {
	s := &r.modelDirect.sprites
	b := modelWorldBounds(g)
	step := int32(1) << sp.level
	// The padded rectangle starts one sprite texel, step record pixels, before
	// the box.
	wx0, wy0 := int32(b.Min.X)-modelSpritePad*step, int32(b.Min.Y)-modelSpritePad*step
	wx1, wy1 := wx0+sp.w*step, wy0+sp.h*step
	x0, y0 := max(wx0, 0), max(wy0, 0)
	x1, y1 := min(wx1, int32(r.clipW())), min(wy1, int32(r.clipH()))
	if x0 >= x1 || y0 >= y1 {
		return
	}
	if !r.sched.begin(schedOpaque, int(x0), int(y0), int(x1), int(y1), [4]*ebiten.Image{3: s.pages[sp.page].img}) {
		return
	}
	inv := 1 / float32(step)
	sx0 := float32(sp.x) + float32(x0-wx0)*inv
	sy0 := float32(sp.y) + float32(y0-wy0)*inv
	nearest := float32(0)
	if sp.level == 0 && (r.worldFilterLane() == 0 || r.modelSingleSample) {
		nearest = 1
	}
	r.sched.quad(schedOpaque,
		float32(x0), float32(y0), float32(x1), float32(y1),
		sx0, sy0, sx0+float32(x1-x0)*inv, sy0+float32(y1-y0)*inv,
		[4]float32{}, [4]float32{0, nearest, 0, sceneOpModelSprite})
	r.modelStats.DirectSprites++
}

// commitModelSpriteShadow is commitModelDirectShadow for a packet drawn from
// its sprites: one quad over the shadow's clipped world rectangle whose
// fragment takes the shadow sprite's texel under the pixel (source 3),
// punches it where the body sprite's texel at the same pixel (source 2, or
// source 3 when both sprites share a texture, as every page of a sheet does;
// Custom0/1 texels away, inside the body sprite's rectangle in Color) is
// wholly covered, and composites the ALP half-colour
// (sceneOpModelSpriteShadow, sceneModelPageSlots). Both sprites
// are level zero, a texel to a record pixel, so this is the lane's shadow
// commit on resolved texels.
func (r *Renderer) commitModelSpriteShadow(g *drawlist.ModelGeometry, sd modelSpriteDraw) {
	s := &r.modelDirect.sprites
	sh, bd := sd.shadow, sd.body
	sb, bb := modelWorldBounds(g.Shadow), modelWorldBounds(g)
	x0, y0 := max(sb.Min.X, 0), max(sb.Min.Y, 0)
	x1, y1 := min(sb.Max.X, r.clipW()), min(sb.Max.Y, r.clipH())
	if x0 >= x1 || y0 >= y1 {
		r.modelStats.Shadows++
		return
	}
	imgs := [4]*ebiten.Image{3: s.pages[sh.page].img}
	onePage := float32(1)
	if bodyPage := s.pages[bd.page].img; bodyPage != imgs[3] {
		imgs[2], onePage = bodyPage, 0
	}
	if !r.sched.begin(schedOpaque, x0, y0, x1, y1, imgs) {
		return
	}
	// A record pixel's shadow texel is the padded rectangle's origin, one pad
	// before the box, plus its offset from the shadow's box; its body texel is
	// the same pixel's in the body sprite, this constant vector away.
	sx0 := float32(sh.x+modelSpritePad) + float32(x0-sb.Min.X)
	sy0 := float32(sh.y+modelSpritePad) + float32(y0-sb.Min.Y)
	kx := float32(bd.x-sh.x) + float32(sb.Min.X-bb.Min.X)
	ky := float32(bd.y-sh.y) + float32(sb.Min.Y-bb.Min.Y)
	r.sched.quad(schedOpaque,
		float32(x0), float32(y0), float32(x1), float32(y1),
		sx0, sy0, sx0+float32(x1-x0), sy0+float32(y1-y0),
		[4]float32{float32(bd.x), float32(bd.y), float32(bd.x + bd.w), float32(bd.y + bd.h)},
		[4]float32{kx, ky, onePage, sceneOpModelSpriteShadow})
	r.modelStats.Shadows++
	r.modelStats.DirectSpriteShadows++
}

// modelSpriteBakeShaderSource averages a sprite texel's k×k block of the lane's
// colour page (source 0) into premultiplied colour and coverage. A doubled texel
// is covered when its alpha is set (modelCoveredColorAt); the block's origin is
// Custom0/1 plus k times the texel's place in the page, Custom2 is k, and
// Color bounds the region's own texels. Custom3 set is the single-sample
// resolve, level zero only: the block's top-left texel at the whole weight.
const modelSpriteBakeShaderSource = `//kage:unit pixels
package main

func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	p := floor(dstPos.xy - imageDstOrigin())
	k := custom.z
	o := custom.xy + p*k
	if custom.w > 0.5 {
		if o.x >= color.r && o.y >= color.g && o.x < color.b && o.y < color.a {
			c := imageSrc0At(imageSrc0Origin() + o + vec2(0.5))
			if c.a > 0.5 {
				return vec4(c.rgb, 1.0)
			}
		}
		return vec4(0.0)
	}
	sum := vec3(0.0)
	cover := 0.0
	for j := 0; j < 32; j++ {
		if float(j) < k {
			for i := 0; i < 32; i++ {
				if float(i) < k {
					t := o + vec2(float(i), float(j))
					if t.x >= color.r && t.y >= color.g && t.x < color.b && t.y < color.a {
						c := imageSrc0At(imageSrc0Origin() + t + vec2(0.5))
						if c.a > 0.5 {
							sum += c.rgb
							cover += 1.0
						}
					}
				}
			}
		}
	}
	return vec4(sum, cover) / (k * k)
}
`
