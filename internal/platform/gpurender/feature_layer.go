package gpurender

import (
	"fmt"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// Strategic feature layers (docs/DESIGN_GPU_RENDERER.md §16.14). A FeatureLayer
// is one map area's static sprite features, retained by the recorder and keyed
// by its ID and revision. The executor bakes the area's items once into an
// image at the layer's level, 2^-Level texels per record pixel, on the same
// kind of retained page the model sprites use (model_sprite.go), and from
// then on draws the area as one bilinear quad in its place in record order
// (sceneOpModelSprite). A layer it has not baked — over this frame's budget,
// or one the pages cannot hold — is drawn as its items, exactly as the feature
// pass would have drawn them.
//
// The bake reproduces the feature blitters' two primitives over a transparent
// image, each item in paint order under source-over: the keyed copy, which
// writes PAL[index] where the frame is opaque, and the ALP tint of a
// translucent shadow or body, whose half-colour composite is the premultiplied
// half of PAL[index] [03 R-RAST-01 §6][03 §5.3.1]. A layer texel averages the
// k×k record pixels it covers, so a layer is its features supersampled at the
// size they are seen. The palette tables are an input: a change of table atlas
// or of the display palette drops every layer.
const (
	// featureLayerBakeBudget bounds the layers baked in one frame, so the view
	// that first reaches the strategic cut bakes over a few frames rather than
	// in one.
	featureLayerBakeBudget = 64
	// featureLayerPad is each layer's transparent border, for the bilinear
	// draw's edge.
	featureLayerPad = 1
	// featureLayerMaxPages bounds the layers' pages at 64 MiB.
	featureLayerMaxPages = 4
)

type featureLayerKey struct {
	id, revision uint64
	level        uint8
}

// featureLayerEntry is one baked layer: its padded rectangle on its page.
type featureLayerEntry struct {
	page       int32
	x, y, w, h int32
}

type featureLayerStore struct {
	pages   []modelSpritePage
	entries map[featureLayerKey]featureLayerEntry
	// frame is the submission frame the budget and page use are counted for.
	frame uint64
	baked int
	// tables and palette are the table atlas and display palette generation
	// the layers were baked against.
	tables    *ebiten.Image
	palette   uint64
	shader    *ebiten.Shader
	shaderErr error
	opts      ebiten.DrawTrianglesShaderOptions
	verts     []ebiten.Vertex
	idx       []uint32
}

// beginFrame starts the store's count for a submission frame, and drops every
// layer when the table atlas or the display palette changed.
func (s *featureLayerStore) beginFrame(frame uint64, tables *ebiten.Image, palette uint64) {
	if s.frame != frame {
		s.frame, s.baked = frame, 0
	}
	if s.tables != tables || s.palette != palette {
		s.tables, s.palette = tables, palette
		clear(s.entries)
		for i := range s.pages {
			s.pages[i].reset()
		}
	}
}

// FeatureLayer draws one strategic feature layer (drawlist.FeatureLayerSink).
func (r *Renderer) FeatureLayer(c drawlist.FeatureLayer) {
	if r == nil || r.surfaces[0] == nil {
		return
	}
	s := &r.layers
	if s.shader == nil && s.shaderErr == nil {
		s.shader, s.shaderErr = compileShader(featureLayerBakeShaderSource())
	}
	if s.shader == nil || r.scene2D == nil || r.tables.atlas == nil || c.Level == 0 || c.Bounds.W <= 0 || c.Bounds.H <= 0 {
		r.featureLayerItems(c)
		return
	}
	s.beginFrame(r.submissionFrame, r.tables.atlas, r.paletteGen)
	key := featureLayerKey{id: c.ID, revision: c.Revision, level: c.Level}
	e, ok := s.entries[key]
	if !ok {
		if s.baked >= featureLayerBakeBudget {
			r.featureLayerItems(c)
			return
		}
		if e, ok = r.bakeFeatureLayer(c); !ok {
			r.featureLayerItems(c)
			return
		}
		s.baked++
		if s.entries == nil {
			s.entries = make(map[featureLayerKey]featureLayerEntry)
		}
		s.entries[key] = e
	}
	s.pages[e.page].used = s.frame
	r.commitFeatureLayer(c, e)
	r.modelStats.FeatureLayers++
}

// featureLayerItems draws a layer as its items: the sprites the feature pass
// would have recorded.
func (r *Renderer) featureLayerItems(c drawlist.FeatureLayer) {
	for _, it := range c.Items {
		r.Sprite(it.Sprite(c.X, c.Y))
	}
	r.modelStats.FeatureLayerItems += len(c.Items)
}

// bakeFeatureLayer allocates a layer's padded image and draws its items into
// it. An item whose frame has no scene atlas placement is left out, as the
// sprite path would draw nothing for it.
func (r *Renderer) bakeFeatureLayer(c drawlist.FeatureLayer) (featureLayerEntry, bool) {
	s := &r.layers
	k := int32(1) << c.Level
	w := (c.Bounds.W+k-1)/k + 2*featureLayerPad
	h := (c.Bounds.H+k-1)/k + 2*featureLayerPad
	page, x, y, evicted, ok := allocSpritePage(&s.pages, w, h, s.frame, featureLayerMaxPages)
	if !ok {
		return featureLayerEntry{}, false
	}
	if evicted >= 0 {
		for key, e := range s.entries {
			if e.page == int32(evicted) {
				delete(s.entries, key)
			}
		}
	}
	e := featureLayerEntry{page: page, x: x, y: y, w: w, h: h}
	img := s.pages[page].ensureImage()
	// The page texel of the layer's local texel (0,0), and the art coordinate
	// of a page texel p's first record pixel: (p − origin)·k + bounds − item.
	ox, oy := x+featureLayerPad, y+featureLayerPad
	s.opts.Blend = ebiten.BlendSourceOver
	s.verts, s.idx = s.verts[:0], s.idx[:0]
	var run *ebiten.Image
	flush := func() {
		if len(s.idx) == 0 {
			return
		}
		s.opts.Images = [4]*ebiten.Image{run, r.tables.atlas}
		img.DrawTrianglesShader32(s.verts, s.idx, s.shader, &s.opts)
		s.verts, s.idx = s.verts[:0], s.idx[:0]
	}
	for _, it := range c.Items {
		f := it.Frame
		if f == nil || f.Width <= 0 || f.Height <= 0 {
			continue
		}
		se := r.sceneFrameFor(f)
		src := r.scene.pageImage(se)
		if !se.ok || src == nil {
			continue
		}
		if src != run {
			flush()
			run = src
		}
		lx0, ly0 := it.X-c.Bounds.X, it.Y-c.Bounds.Y
		tx0, ty0 := ox+floorDiv32(lx0, k), oy+floorDiv32(ly0, k)
		tx1, ty1 := ox+ceilDiv32(lx0+int32(f.Width), k), oy+ceilDiv32(ly0+int32(f.Height), k)
		ax := float32(c.Bounds.X-it.X) - float32(ox*k)
		ay := float32(c.Bounds.Y-it.Y) - float32(oy*k)
		trans := float32(0)
		if it.Trans {
			trans = 1
		}
		base := uint32(len(s.verts))
		for _, p := range [4][2]int32{{tx0, ty0}, {tx1, ty0}, {tx1, ty1}, {tx0, ty1}} {
			s.verts = append(s.verts, ebiten.Vertex{
				DstX: float32(p[0]), DstY: float32(p[1]),
				ColorR: float32(se.x), ColorG: float32(se.y), ColorB: float32(f.Width), ColorA: float32(f.Height),
				Custom0: ax, Custom1: ay, Custom2: float32(k), Custom3: trans,
			})
		}
		s.idx = append(s.idx, base, base+1, base+2, base, base+2, base+3)
	}
	flush()
	r.modelStats.FeatureLayerBakes++
	return e, true
}

// commitFeatureLayer draws a baked layer: one quad over its padded world
// rectangle, sampled bilinearly from its page.
func (r *Renderer) commitFeatureLayer(c drawlist.FeatureLayer, e featureLayerEntry) {
	k := int32(1) << c.Level
	wx0 := c.X + c.Bounds.X - featureLayerPad*k
	wy0 := c.Y + c.Bounds.Y - featureLayerPad*k
	wx1, wy1 := wx0+e.w*k, wy0+e.h*k
	x0, y0 := max(wx0, 0), max(wy0, 0)
	x1, y1 := min(wx1, int32(r.clipW())), min(wy1, int32(r.clipH()))
	if x0 >= x1 || y0 >= y1 {
		return
	}
	if !r.sched.begin(schedOpaque, int(x0), int(y0), int(x1), int(y1), [4]*ebiten.Image{2: r.layers.pages[e.page].img}) {
		return
	}
	inv := 1 / float32(k)
	sx0 := float32(e.x) + float32(x0-wx0)*inv
	sy0 := float32(e.y) + float32(y0-wy0)*inv
	r.sched.quad(schedOpaque,
		float32(x0), float32(y0), float32(x1), float32(y1),
		sx0, sy0, sx0+float32(x1-x0)*inv, sy0+float32(y1-y0)*inv,
		[4]float32{}, [4]float32{0, 0, 0, sceneOpModelSprite})
}

func floorDiv32(a, b int32) int32 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

func ceilDiv32(a, b int32) int32 { return -floorDiv32(-a, b) }

// featureLayerBakeShaderSource averages, for one layer texel, the k×k record
// pixels it covers of one item's frame (source 0, the scene atlas page: index
// in red, opacity in green), resolving each opaque one through PAL (source 1,
// the table atlas). Color is the frame's atlas origin and size, Custom0/1 the
// art coordinate of page texel (0,0)'s first record pixel, Custom2 k, Custom3
// the ALP tint.
func featureLayerBakeShaderSource() string {
	return `//kage:unit pixels
package main

const palRow = ` + fmt.Sprint(tableRowPAL) + `.0

func palAt(idx float) vec3 {
	return imageSrc1AtFromSrc0Pos(imageSrc0Origin()+vec2(idx+0.5, palRow+0.5)).rgb
}

func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	k := custom.z
	a0 := custom.xy + floor(dstPos.xy-imageDstOrigin())*k
	sum := vec3(0.0)
	cover := 0.0
	for j := 0; j < 16; j++ {
		if float(j) < k {
			for i := 0; i < 16; i++ {
				if float(i) < k {
					a := a0 + vec2(float(i), float(j))
					if a.x >= 0.0 && a.y >= 0.0 && a.x < color.b && a.y < color.a {
						t := imageSrc0At(imageSrc0Origin() + color.rg + a + vec2(0.5))
						if t.g >= 0.5 {
							sum += palAt(floor(t.r*255.0 + 0.5))
							cover += 1.0
						}
					}
				}
			}
		}
	}
	c := vec4(sum, cover) / (k * k)
	if custom.w > 0.5 {
		c *= 0.5
	}
	return c
}
`
}
