package gpurender

import (
	"image"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// Strategic sprites (docs/DESIGN_GPU_RENDERER.md §16.13). In the strategic
// view a battlefield's wrecks are most of the model lane's work, and none of it
// changes from one frame to the next: each one is rasterized onto the frame's
// atlas page, every face of it, to be shrunk to a few screen pixels by the world
// transform. A packet the recorder offers (ModelGeometry.SpriteLevel) is drawn
// once through the lane as ever, and its finished region is then averaged down
// onto a retained sprite page at the offered level, 2^-level texels per record
// pixel. From the next frame the packet takes no region and no faces: its commit
// is one bilinear quad from the sprite page, in the commit's own place in record
// order (sceneOpModelSprite).
//
// The bake averages every texel of the doubled raster a sprite texel covers —
// sixteen at level one, a thousand at level four — so a sprite is the subject
// supersampled at the size it is seen, where the lane's commit takes the one
// pixel under each screen pixel. It reads the colour page the commit reads,
// after the page passes, so a sprite is the subject's own finished colour:
// palette, shading, finish and glint. Two inputs of that colour are per frame
// and are kept out: a packet with a battle light near it is neither baked nor
// drawn from its sprite while the light lasts, and the finish switches drop
// every sprite when they change, as they drop the retained store; so does a
// change of display palette (the gamma setting), which the lane resolves
// every frame.
//
// A sprite is keyed on the packet's cache key less its half-pixel offset — half
// a record pixel, at most a quarter of a sprite texel — and on the level.
// The pages are shelf-packed and evicted a page at a time, least recently drawn
// first; a page drawn from this frame is never evicted, so a view holding more
// sprites than the pages draws the rest through the lane.
const (
	modelSpritePageSize = 2048
	modelSpriteMaxPages = 4
	// modelSpritePad is the transparent border each sprite is baked with, so
	// the bilinear draw fades its edge rather than reading a neighbour.
	modelSpritePad = 1
)

type modelSpriteKey struct {
	body, revision uint64
	lane           drawlist.ModelCacheLane
	level          uint8
}

// modelSprite is one baked sprite: its padded rectangle on its page, the record
// box it covers, and the frame it was last drawn or baked on.
type modelSprite struct {
	page       int32
	x, y, w, h int32
	// bw, bh is the subject's world box the sprite covers, in record pixels.
	bw, bh int32
	level  uint8
	ok     bool
}

type modelSpritePage struct {
	img                    *ebiten.Image
	shelfX, shelfY, shelfH int32
	// used is the last frame a sprite on this page was drawn or baked.
	used uint64
}

// modelSpriteBake is one bake queued for the page goroutine: the subject's
// region on the lane page and the sprite it fills.
type modelSpriteBake struct {
	region modelDirectRegion
	sprite modelSprite
}

type modelSpriteStore struct {
	pages   []modelSpritePage
	entries map[modelSpriteKey]modelSprite
	// draws are this frame's packets drawn from a sprite, and pending the
	// offered packets placed through the lane that bake once placed.
	draws   map[*drawlist.ModelGeometry]modelSprite
	pending []*drawlist.ModelGeometry
	bakes   []modelSpriteBake
	frame   uint64
	shader  *ebiten.Shader
	opts    ebiten.DrawTrianglesShaderOptions
	verts   []ebiten.Vertex
	idx     []uint32
	// metalGlint and materials are the finish switches the sprites were baked
	// under, and palette the display palette generation; a change drops them
	// all.
	metalGlint, materials bool
	palette               uint64
}

func modelSpriteKeyOf(g *drawlist.ModelGeometry) modelSpriteKey {
	return modelSpriteKey{body: g.Cache.Body, revision: g.Cache.Revision, lane: g.Cache.Lane, level: g.SpriteLevel}
}

// beginFrame starts a frame's sprite bookkeeping and drops every sprite when
// the finish switches or the display palette changed.
func (s *modelSpriteStore) beginFrame(metalGlint, materials bool, palette uint64) {
	s.frame++
	if s.draws == nil {
		s.draws = make(map[*drawlist.ModelGeometry]modelSprite)
	} else {
		clear(s.draws)
	}
	s.pending, s.bakes = s.pending[:0], s.bakes[:0]
	if s.metalGlint != metalGlint || s.materials != materials || s.palette != palette {
		s.metalGlint, s.materials, s.palette = metalGlint, materials, palette
		s.dropAll()
	}
}

// dropAll forgets every sprite and empties the pages.
func (s *modelSpriteStore) dropAll() {
	clear(s.entries)
	for i := range s.pages {
		s.pages[i].reset()
	}
}

// modelSpriteOffered reports whether the lane may draw g from a sprite: the
// recorder's offer, and the packet shapes a single region commit draws whole.
func modelSpriteOffered(g *drawlist.ModelGeometry) bool {
	return g.SpriteLevel != 0 && g.Cache.Reusable() && g.Shadow == nil && len(g.Children) == 0 &&
		len(g.LiveFaces) == 0 && len(g.Outline) == 0 && g.Reveal == nil && !g.ReflectWater &&
		g.Waterline == drawlist.ModelWaterlineNone && !g.Digger && !g.Cloaked &&
		g.WreckEmission == ([3]float32{})
}

// spriteDraw decides, for one offered packet before placement, whether this
// frame draws it from its sprite (true) or places it through the lane (false),
// queueing a bake for the latter. A battle light near the subject keeps it on
// the lane and unbaked: its colour pass is lit this frame.
func (r *Renderer) spriteDraw(g *drawlist.ModelGeometry) bool {
	s := &r.modelDirect.sprites
	if s.shader == nil || !modelSpriteOffered(g) {
		return false
	}
	wb := modelWorldBounds(g)
	if wb.Empty() {
		return false
	}
	if lights := r.lighting.near(float32(wb.Min.X+wb.Max.X)*0.5, float32(wb.Min.Y+wb.Max.Y)*0.5, float32(wb.Dx()+wb.Dy())*0.5); lights.count != 0 {
		return false
	}
	if e, ok := s.entries[modelSpriteKeyOf(g)]; ok && e.bw == int32(wb.Dx()) && e.bh == int32(wb.Dy()) {
		s.draws[g] = e
		s.pages[e.page].used = s.frame
		return true
	}
	s.pending = append(s.pending, g)
	return false
}

// planSpriteBakes allocates a sprite for every offered packet the frame placed
// on a page and queues its bake. A packet the pages cannot take this frame is
// offered again on the next.
func (r *Renderer) planSpriteBakes() {
	d := &r.modelDirect
	s := &d.sprites
	for _, g := range s.pending {
		region, ok := d.regions[g]
		if !ok || !region.ok {
			continue
		}
		key := modelSpriteKeyOf(g)
		if _, done := s.entries[key]; done {
			continue
		}
		sp, ok := s.alloc(region.bounds, g.SpriteLevel)
		if !ok {
			continue
		}
		// The page's texture exists before the page goroutine starts: the
		// executing goroutine reads it while the bakes run (bindsSpritePage).
		s.pages[sp.page].ensureImage()
		if s.entries == nil {
			s.entries = make(map[modelSpriteKey]modelSprite)
		}
		s.entries[key] = sp
		s.bakes = append(s.bakes, modelSpriteBake{region: region, sprite: sp})
	}
	r.modelStats.DirectSpriteBakes = len(s.bakes)
}

// alloc reserves a padded sprite for a world box at level on the shelf packer.
// A full set of pages gives up the least recently used page this frame has not
// drawn from: its sprites are forgotten and its shelves start over.
func (s *modelSpriteStore) alloc(bounds image.Rectangle, level uint8) (modelSprite, bool) {
	shift := int32(level)
	w := (int32(bounds.Dx())+(1<<shift)-1)>>shift + 2*modelSpritePad
	h := (int32(bounds.Dy())+(1<<shift)-1)>>shift + 2*modelSpritePad
	sp := modelSprite{w: w, h: h, bw: int32(bounds.Dx()), bh: int32(bounds.Dy()), level: level, ok: true}
	page, x, y, evicted, ok := allocSpritePage(&s.pages, w, h, s.frame)
	if !ok {
		return modelSprite{}, false
	}
	if evicted >= 0 {
		for k, e := range s.entries {
			if e.page == int32(evicted) {
				delete(s.entries, k)
			}
		}
	}
	sp.page, sp.x, sp.y = page, x, y
	return sp, true
}

// allocSpritePage places a w×h rectangle on a set of retained pages, shelf
// packed: on the first page with room, else a new page while there are fewer
// than modelSpriteMaxPages, else the least recently used page not used this
// frame, emptied, whose index it reports as evicted (otherwise -1) so its owner
// forgets what the page held. A page used this frame is never given up.
func allocSpritePage(pages *[]modelSpritePage, w, h int32, frame uint64) (page, x, y int32, evicted int, ok bool) {
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
	if len(ps) < modelSpriteMaxPages {
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

// reset empties a page for reuse. Its texture is cleared as well as its
// shelves: a bake writes only its own rectangle, and the bilinear draw's edge
// tap reaches the texel beside it, which must be transparent rather than an
// evicted sprite's.
func (p *modelSpritePage) reset() {
	p.shelfX, p.shelfY, p.shelfH = 0, 0, 0
	if p.img != nil {
		p.img.Clear()
	}
}

// place puts a w×h rectangle on the page's shelves, at (x, y).
func (p *modelSpritePage) place(w, h int32, x, y *int32) bool {
	if p.shelfX+w > modelSpritePageSize {
		p.shelfY += p.shelfH
		p.shelfX, p.shelfH = 0, 0
	}
	if p.shelfY+h > modelSpritePageSize {
		return false
	}
	*x, *y = p.shelfX, p.shelfY
	p.shelfX += w
	p.shelfH = max(p.shelfH, h)
	return true
}

// ensureImage allocates a page's texture on first use, on the executing
// goroutine during placement (planSpriteBakes).
func (p *modelSpritePage) ensureImage() *ebiten.Image {
	if p.img == nil {
		p.img = ebiten.NewImageWithOptions(image.Rect(0, 0, modelSpritePageSize, modelSpritePageSize), &ebiten.NewImageOptions{Unmanaged: true})
	}
	return p.img
}

// bindsSpritePage reports whether img is a sprite page.
func (s *modelSpriteStore) bindsSpritePage(img *ebiten.Image) bool {
	for i := range s.pages {
		if s.pages[i].img != nil && s.pages[i].img == img {
			return true
		}
	}
	return false
}

// bakeModelSprites averages each queued bake's region of the lane's colour page
// into its sprite, after the page passes have finished the region. It runs on
// the page goroutine with the passes, and the commits that bind a sprite page
// join it first (bindsModelPage). One draw per source page and sprite page.
func (r *Renderer) bakeModelSprites(a *deviceAcct) {
	d := &r.modelDirect
	s := &d.sprites
	if len(s.bakes) == 0 || s.shader == nil {
		return
	}
	s.opts.Blend = ebiten.BlendCopy
	for src := range d.pages {
		for dst := range s.pages {
			s.verts, s.idx = s.verts[:0], s.idx[:0]
			for i := range s.bakes {
				b := &s.bakes[i]
				if int(b.region.page) != src || int(b.sprite.page) != dst {
					continue
				}
				// Sprite texel (i, j) of the padded rectangle averages the
				// k×k doubled texels from (A + k·(i, j)), where A puts the
				// pad's first texel k texels before the region's origin; the
				// region's own texels bound what is read.
				sp, rg := b.sprite, b.region
				k := float32(int32(2) << sp.level)
				ax := float32(rg.x) - k*float32(modelSpritePad) - k*float32(sp.x)
				ay := float32(rg.y) - k*float32(modelSpritePad) - k*float32(sp.y)
				base := uint32(len(s.verts))
				for _, c := range [4][2]int32{{0, 0}, {sp.w, 0}, {sp.w, sp.h}, {0, sp.h}} {
					s.verts = append(s.verts, ebiten.Vertex{
						DstX: float32(sp.x + c[0]), DstY: float32(sp.y + c[1]),
						ColorR: float32(rg.x), ColorG: float32(rg.y),
						ColorB: float32(rg.x) + 2*float32(rg.bounds.Dx()), ColorA: float32(rg.y) + 2*float32(rg.bounds.Dy()),
						Custom0: ax, Custom1: ay, Custom2: k,
					})
				}
				s.idx = append(s.idx, base, base+1, base+2, base, base+2, base+3)
			}
			if len(s.idx) == 0 {
				continue
			}
			img := s.pages[dst].img
			s.opts.Images = [4]*ebiten.Image{d.pages[src].colour}
			a.beginPass(img)
			a.submitted(len(s.verts), len(s.idx))
			img.DrawTrianglesShader32(s.verts, s.idx, s.shader, &s.opts)
		}
	}
}

// commitModelSprite is commitModelDirect for a packet drawn from its sprite:
// one bilinear quad over the padded sprite's world rectangle, in the commit's
// place in record order, that the world transform shrinks to the screen.
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
	if !r.sched.begin(schedOpaque, int(x0), int(y0), int(x1), int(y1), [4]*ebiten.Image{2: s.pages[sp.page].img}) {
		return
	}
	inv := 1 / float32(step)
	sx0 := float32(sp.x) + float32(x0-wx0)*inv
	sy0 := float32(sp.y) + float32(y0-wy0)*inv
	r.sched.quad(schedOpaque,
		float32(x0), float32(y0), float32(x1), float32(y1),
		sx0, sy0, sx0+float32(x1-x0)*inv, sy0+float32(y1-y0)*inv,
		[4]float32{}, [4]float32{0, 0, 0, sceneOpModelSprite})
	r.modelStats.DirectSprites++
}

// modelSpriteBakeShaderSource averages a sprite texel's k×k block of the lane's
// colour page (source 0) into premultiplied colour and coverage. A doubled texel
// is covered when its alpha is set (modelCoveredColorAt); the block's origin is
// Custom0/1 plus k times the texel's place in the page, Custom2 is k, and
// Color bounds the region's own texels.
const modelSpriteBakeShaderSource = `//kage:unit pixels
package main

func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	p := floor(dstPos.xy - imageDstOrigin())
	k := custom.z
	o := custom.xy + p*k
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
