package gpurender

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"image"
	"math"
	"runtime"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// The model lane's placement golden hash (docs/DESIGN_GPU_RENDERER.md §22,
// "Placement workers"). Placement is a pure performance concern: however it
// is organised — one goroutine or a pool, in whatever order the workers claim
// their subjects — its whole output must be byte-identical to the sequential
// lane's. The hash covers that whole output over a deterministic multi-frame
// scenario: the batch (vertices, indices, runs), the parameter bytes, the
// regions, the group merges, the accounting, the retained store and its body
// index in LRU order with their contents, the water reflection batch and the
// page-overflow fallback's triangles.
//
// The scenario takes every path the lane has: keyed subjects primed, captured
// and replayed, turning subjects appended warm, unkeyed and group-composed
// subjects cold, a captured key whose shape changes, live lanes and outline
// rings drawn on the device and walked on the CPU, projected and silhouette
// shadows, attached-unit groups with a carried child's solo image and a
// construction group's merges, a nested child the lane skips, cloaked,
// waterline and Digger subjects, reflecting subjects, texture frames that open
// new texture pages part-way through a lane and one too large for a page, and
// frames large enough to fill both atlas pages and overflow into the fallback
// — one of them a construction group whose child allocation turns the page.
//
// The hash is kept per architecture because arm64 may fuse a float32
// multiply-add that amd64 rounds in two steps. The scenario's float lanes are
// quantized (the battle light, the finish response) or integer-valued, and the
// two agree; either way the hash must not move under a refactor of placement.
//
// 2026-10-14: the lane's off-clip skip (modelDirectClipsAway) added
// ModelStats.DirectClipped, and the digest covers the stats struct's print;
// with the field removed the hash is the previous value, so placement itself
// is unchanged and the scenario culls nothing.
var modelPlaceGolden = map[string]string{
	"arm64": "f6552994f5bc845a3a36cd42cd9ed7b878c2203b873c8b96261c45e2daedac0e",
	"amd64": "f6552994f5bc845a3a36cd42cd9ed7b878c2203b873c8b96261c45e2daedac0e",
}

// modelPlaceFrames is the scenario's frame count.
const modelPlaceFrames = 8

// placeScenario is the deterministic multi-frame placement scenario. Its
// texture frames are built once, because a loaded texture is pointer-stable
// across frames and the body index reads that identity.
type placeScenario struct {
	small, second, third   *formats.GAFFrame
	big1, big2, big3, big4 *formats.GAFFrame
	over                   *formats.GAFFrame
	// duplicate records, on every frame, a second subject under the first
	// keyed subject's body: a frame the parallel placement must refuse.
	duplicate bool
	// flood records, on frames two and six, enough reflecting subjects to
	// fill the reflection batch to its cap part-way through the frame.
	flood bool
}

func newPlaceScenario() *placeScenario {
	return &placeScenario{
		small:  benchCargoTexture,
		second: placeTexture(24, 20, 3),
		third:  placeTexture(9, 30, 5),
		// A page is 2048 texels square; a frame over 1,100 texels takes most of
		// one, so each of these opens a page when it first appears.
		big1: placeTexture(1100, 1100, 7),
		big2: placeTexture(1100, 1100, 11),
		big3: placeTexture(1100, 1100, 13),
		big4: placeTexture(1100, 1100, 17),
		// Wider than any page: bound as a standalone texture.
		over: placeTexture(2050, 3, 19),
	}
}

// placeTexture is a texture frame of w × h texels.
func placeTexture(w, h int, seed byte) *formats.GAFFrame {
	t := &formats.GAFFrame{Width: uint16(w), Height: uint16(h), Pixels: make([]byte, w*h)}
	for i := range t.Pixels {
		t.Pixels[i] = byte(32 + (i*int(seed))%64)
	}
	return t
}

// placeFaces authors n faces over a w × h box at scale s: rings of three,
// four or five corners (mostly four), one in seven wound the other way, flat
// or textured from texs in turn, shaded, lit and finished, keys and heights
// rising across the box.
func placeFaces(n int, w, h, s int32, texs []*formats.GAFFrame, seed int) []drawlist.ModelFace {
	out := make([]drawlist.ModelFace, 0, n)
	cols := max(int32(1), (w-6)/6)
	rows := max(int32(1), (h-6)/4)
	for i := 0; i < n; i++ {
		x := (int32(i)%cols)*6 + int32(seed%3)
		y := ((int32(i)/cols)%rows)*4 + int32(seed%2)
		key := int32(10 + (i*7+seed*3)%90)
		tex := texs[(i+seed)%len(texs)]
		tw, th := int32(16), int32(16)
		if tex != nil {
			tw, th = min(int32(tex.Width), 16), min(int32(tex.Height), 16)
		}
		c := func(dx, dy, dk int32, u, v int32, shade uint8) drawlist.ModelVertex {
			return drawlist.ModelVertex{X: (x + dx) * s, Y: (y + dy) * s, Key: key + dk, U: u, V: v, Shade: shade, Height: float32(key+dk) * 0.75}
		}
		var vs []drawlist.ModelVertex
		switch (i + seed) % 7 {
		case 0:
			vs = []drawlist.ModelVertex{c(0, 0, 0, 0, 0, 14), c(5, 1, 2, tw-1, 0, 16), c(2, 4, 3, 0, th-1, 18)}
		case 1:
			vs = []drawlist.ModelVertex{c(1, 0, 0, 0, 0, 15), c(5, 0, 1, tw-1, 0, 15), c(6, 2, 2, tw-1, th/2, 17), c(4, 4, 3, tw/2, th-1, 16), c(0, 3, 1, 0, th-1, 14)}
		case 2:
			// Wound the other way: a back face, culled.
			vs = []drawlist.ModelVertex{c(0, 3, 1, 0, th-1, 16), c(5, 3, 3, tw-1, th-1, 17), c(5, 0, 2, tw-1, 0, 16), c(0, 0, 0, 0, 0, 15)}
		default:
			vs = []drawlist.ModelVertex{c(0, 0, 0, 0, 0, 15), c(5, 0, 2, tw-1, 0, 16), c(5, 3, 3, tw-1, th-1, 17), c(0, 3, 1, 0, th-1, 16)}
		}
		f := drawlist.ModelFace{
			Vertices: vs, Texture: tex, Color: uint8(32 + (i*5+seed)%60), Shaded: (i+seed)%3 != 0,
			Normal:   [3]float32{0.1 * float32((i+seed)%5-2), -0.4 + 0.05*float32(i%4), 0.8},
			Material: uint8((i + seed) % 4),
		}
		if (i+seed)%11 == 5 {
			f.Normal = [3]float32{}
		}
		out = append(out, f)
	}
	return out
}

// placeRings authors outline rings of three, four and five corners over the
// box at scale s: a four-corner ring whose keys the device draws exactly, a
// triangle, and a five-corner ring the CPU always walks.
func placeRings(w, h, s int32, key int32) []drawlist.ModelFace {
	v := func(x, y, k int32) drawlist.ModelVertex { return drawlist.ModelVertex{X: x * s, Y: y * s, Key: k} }
	return []drawlist.ModelFace{
		{Color: 60, Vertices: []drawlist.ModelVertex{v(1, 1, key), v(w-2, 1, key), v(w-2, h-2, key+4), v(1, h-2, key+4)}},
		{Color: 61, Vertices: []drawlist.ModelVertex{v(2, 2, key+1), v(w/2, 1, key+2), v(3, h-3, key+3)}},
		{Color: 62, Vertices: []drawlist.ModelVertex{v(1, 2, key), v(w/2, 1, key+1), v(w-2, 3, key+2), v(w-3, h-2, key+3), v(2, h-3, key+1)}},
	}
}

// placeBodyOpts shapes one authored subject.
type placeBodyOpts struct {
	faces, live int
	w, h        int32
	ax, ay      int32
	texs        []*formats.GAFFrame
	seed        int
	doubled     bool
	outline     bool
	reveal      bool
}

// placeBody authors one subject: its faces, an optional live lane, outline
// rings and reveal, and the doubled lane the recorder builds for the 2× atlas
// (§17.3), whose live faces reach past its box on the negative side.
func placeBody(o placeBodyOpts) *drawlist.ModelGeometry {
	if o.texs == nil {
		o.texs = []*formats.GAFFrame{nil}
	}
	g := &drawlist.ModelGeometry{
		Eligible: true, KeyPlane: true, Scale: 1,
		Width: o.w, Height: o.h, AnchorX: o.ax, AnchorY: o.ay,
		Faces: placeFaces(o.faces, o.w, o.h, 1, o.texs, o.seed),
	}
	if o.live > 0 {
		g.LiveFaces = placeFaces(o.live, o.w, o.h, 1, o.texs, o.seed+1)
	}
	if o.outline {
		g.Outline = placeRings(o.w, o.h, 1, int32(30+o.seed%20))
	}
	if o.reveal {
		g.Reveal = &drawlist.ModelReveal{Line: 60, Floor: 30, Below: -1, Band: 250, Above: -2}
	}
	if o.doubled {
		ss := &drawlist.ModelGeometry{Eligible: true, KeyPlane: true, Scale: 2, Width: 2 * o.w, Height: 2 * o.h,
			Faces: placeFaces(o.faces, o.w, o.h, 2, o.texs, o.seed), Reveal: g.Reveal}
		if o.live > 0 {
			ss.LiveFaces = placeFaces(o.live, o.w, o.h, 2, o.texs, o.seed+1)
			ss.LiveFaces[0].Vertices[0].X -= 3
		}
		if o.outline {
			ss.Outline = placeRings(o.w, o.h, 2, int32(30+o.seed%20))
		}
		g.Supersample = ss
	}
	return g
}

// keyed gives g a reusable cache key on lane.
func keyed(g *drawlist.ModelGeometry, body, rev uint64, lane drawlist.ModelCacheLane) *drawlist.ModelGeometry {
	g.Cache = drawlist.ModelCacheKey{Body: body, Revision: rev, Lane: lane}
	return g
}

// placeShadow authors a projected shadow for g: a flat, faceted copy of its
// box sheared a few pixels right and down.
func placeShadow(g *drawlist.ModelGeometry, faces, seed int) *drawlist.ModelGeometry {
	return &drawlist.ModelGeometry{
		Eligible: true, KeyPlane: true, Scale: 1,
		Width: g.Width + 6, Height: g.Height/2 + 4, AnchorX: g.AnchorX + 5, AnchorY: g.AnchorY + g.Height/2,
		Faces: placeFaces(faces, g.Width+6, g.Height/2+4, 1, []*formats.GAFFrame{nil}, seed),
	}
}

// frame is scenario frame f's recorded list, and whether its outline rings
// are all walked on the CPU.
func (sc *placeScenario) frame(f int) (drawlist.List, bool) {
	var list drawlist.List
	flat := []*formats.GAFFrame{nil}
	mixed := []*formats.GAFFrame{nil, sc.small, sc.second}
	record := func(g *drawlist.ModelGeometry) { list.RecordModel(drawlist.Model{Geometry: g}) }
	shift := int32(f * 3)

	// Keyed subjects the recorder proves unchanged: primed on the first
	// sighting, captured on the second, replayed after. The second carries a
	// doubled lane, a live lane, an outline and a reveal; the third changes
	// shape at frame four under the same key; the fourth sits under a blue
	// waterline and the fifth is a Digger.
	s1 := keyed(placeBody(placeBodyOpts{faces: 40, w: 48, h: 36, ax: 20 + shift, ay: 30, texs: mixed, seed: 1}), 101, 1, drawlist.ModelCacheLaneBody)
	s1.Shadow = keyed(placeShadow(s1, 12, 2), 101, 1, drawlist.ModelCacheLaneShadow)
	record(s1)
	if sc.duplicate {
		record(keyed(placeBody(placeBodyOpts{faces: 12, w: 30, h: 20, ax: 60, ay: 70, texs: mixed, seed: 81}), 101, 7, drawlist.ModelCacheLaneBody))
	}
	s2 := keyed(placeBody(placeBodyOpts{faces: 30, live: 4, w: 40, h: 30, ax: 90, ay: 40 + shift, texs: mixed, seed: 3, doubled: true, outline: true, reveal: true}), 102, 1, drawlist.ModelCacheLaneBody)
	s2.Shadow = &drawlist.ModelGeometry{Eligible: true, KeyPlane: true, Silhouette: true, SilhouetteClip: 20, Width: 40, Height: 30, AnchorX: 95, AnchorY: 45 + shift, Scale: 1}
	record(s2)
	w3 := int32(30)
	if f >= 4 {
		w3 = 34
	}
	s3 := keyed(placeBody(placeBodyOpts{faces: 20, w: w3, h: 24, ax: 150, ay: 20, texs: []*formats.GAFFrame{sc.third, nil}, seed: 5, doubled: true}), 103, 1, drawlist.ModelCacheLaneBody)
	record(s3)
	s4 := keyed(placeBody(placeBodyOpts{faces: 18, live: 2, w: 28, h: 28, ax: 200, ay: 60, texs: flat, seed: 7, outline: true}), 104, 1, drawlist.ModelCacheLaneBody)
	s4.Waterline, s4.WaterlineKey = drawlist.ModelWaterlineBlue, 40
	record(s4)
	s5 := keyed(placeBody(placeBodyOpts{faces: 16, w: 26, h: 20, ax: 240, ay: 90, texs: mixed, seed: 9}), 105, 1, drawlist.ModelCacheLaneBody)
	s5.Digger, s5.DiggerKey = true, 25
	record(s5)
	if f == 1 {
		// A texture wider than any page, on a subject seen once.
		record(placeBody(placeBodyOpts{faces: 6, w: 30, h: 20, ax: 260, ay: 10, texs: []*formats.GAFFrame{sc.over, sc.small}, seed: 11}))
	}

	// Turning subjects: a new revision every frame, appended warm once their
	// body is held. The second changes a face's texture at frame three, which
	// fails the body's topology proof and appends cold. The first opens a
	// texture page part-way through its lane on its first sighting, so faces
	// after the page change are routed as standalone.
	for i := 0; i < 4; i++ {
		texs := mixed
		if i == 0 {
			texs = []*formats.GAFFrame{sc.small, sc.big1, sc.second, sc.big2, nil}
		}
		if i == 1 && f >= 3 {
			texs = []*formats.GAFFrame{nil, sc.third, sc.second}
		}
		if i == 2 && f >= 4 {
			// A new frame on a later frame: page three opens mid-frame.
			texs = []*formats.GAFFrame{sc.big3, nil, sc.small}
		}
		g := keyed(placeBody(placeBodyOpts{faces: 24 + 4*i, live: i % 2, w: 36, h: 30, ax: 30 + 50*int32(i), ay: 120, texs: texs, seed: 13 + i, doubled: i%2 == 1}), 200+uint64(i), 1, drawlist.ModelCacheLaneBody)
		if i == 0 {
			g.Shadow = keyed(placeShadow(g, 10, 17), 200, 1, drawlist.ModelCacheLaneShadow)
		}
		turnGeometry(g, uint64(f+1), f+1)
		record(g)
	}

	// Subjects with no reusable key: always cold. The second is cloaked.
	for i := 0; i < 3; i++ {
		g := placeBody(placeBodyOpts{faces: 12 + 3*i, live: 1, w: 22, h: 18, ax: 300 + 30*int32(i), ay: 150 + shift, texs: mixed, seed: 21 + i + f, outline: i == 2})
		g.Cloaked = i == 1
		record(g)
	}

	// An ordinary attached-unit group: a keyed carrier and three children —
	// one raised, one lowered that casts a projected shadow (a solo image and
	// a shadow-only command recorded ahead of the carrier), and one with
	// children of its own, which the lane skips.
	carrier := keyed(placeBody(placeBodyOpts{faces: 30, live: 2, w: 60, h: 40, ax: 40, ay: 200, texs: mixed, seed: 31, outline: true}), 300, 1, drawlist.ModelCacheLaneBody)
	// The children carry keys, which a group composition never uses: a child is
	// cold whatever its key says.
	raised := keyed(placeBody(placeBodyOpts{faces: 10, w: 20, h: 16, ax: 50, ay: 205, texs: mixed, seed: 32, outline: true}), 301, 1, drawlist.ModelCacheLaneBody)
	lowered := keyed(placeBody(placeBodyOpts{faces: 10, live: 1, w: 18, h: 14, ax: 70, ay: 215, texs: flat, seed: 33, doubled: true, outline: true}), 302, 1, drawlist.ModelCacheLaneBody)
	lowered.Shadow = placeShadow(lowered, 6, 34)
	nested := placeBody(placeBodyOpts{faces: 4, w: 10, h: 10, ax: 80, ay: 205, seed: 35})
	nested.Children = []drawlist.ModelChild{{Geometry: placeBody(placeBodyOpts{faces: 2, w: 6, h: 6, ax: 82, ay: 207, seed: 36})}}
	if f == 3 {
		raised.ReflectWater, raised.ReflectionSea, raised.WorldHeight = true, 2, 6
	}
	carrier.Children = []drawlist.ModelChild{{Geometry: raised, KeyDelta: 30}, {Geometry: lowered, KeyDelta: -10}, {Geometry: nested}}
	list.RecordModel(drawlist.Model{ShadowOnly: true, Geometry: lowered})
	record(carrier)

	// A construction group: a factory building two products, each with a
	// reveal, so every child finishes in a region of its own and merges.
	factory := placeBody(placeBodyOpts{faces: 26, w: 70, h: 44, ax: 150, ay: 260, texs: mixed, seed: 41})
	factory.Shadow = placeShadow(factory, 8, 42)
	product := placeBody(placeBodyOpts{faces: 14, live: 2, w: 24, h: 20, ax: 170, ay: 270, texs: mixed, seed: 43, doubled: true, outline: true, reveal: true})
	product.Shadow = &drawlist.ModelGeometry{Eligible: true, KeyPlane: true, Silhouette: true, Width: 24, Height: 20, AnchorX: 175, AnchorY: 275, Scale: 1}
	second := placeBody(placeBodyOpts{faces: 8, w: 16, h: 14, ax: 190, ay: 272, texs: flat, seed: 44, reveal: true})
	factory.Children = []drawlist.ModelChild{{Geometry: product, KeyDelta: 12}, {Geometry: second, KeyDelta: 20}}
	record(factory)

	// Reflecting subjects over water on frames two and three: a keyed body,
	// which the replay cannot serve and the warm path can.
	if f == 2 || f == 3 {
		for i := 0; i < 2; i++ {
			g := keyed(placeBody(placeBodyOpts{faces: 20, live: 1, w: 30, h: 24, ax: 400 + 40*int32(i), ay: 300, texs: mixed, seed: 51 + i, doubled: i == 1, outline: i == 0}), 400+uint64(i), uint64(f), drawlist.ModelCacheLaneBody)
			g.ReflectWater, g.ReflectionSea, g.WorldHeight = true, 3, 8
			record(g)
		}
	}

	if sc.flood && (f == 2 || f == 6) {
		for i := 0; i < 220; i++ {
			g := placeBody(placeBodyOpts{faces: 40, w: 40, h: 30, ax: int32(i%20) * 30, ay: 20 + int32(i/20)*40, texs: mixed, seed: 100 + i, doubled: i%3 == 0})
			g.ReflectWater, g.ReflectionSea, g.WorldHeight = true, 3, 8
			record(g)
		}
	}

	// Frames five and six fill both atlas pages. Three subjects a page and a
	// half square take the first page's two shelves; a construction group
	// fits the rest of the first page but its product's region does not, so
	// the group's own allocation turns the page; three more fill the second
	// page and the last two overflow into the fallback.
	if f == 5 || f == 6 {
		for i := 0; i < 3; i++ {
			record(placeBody(placeBodyOpts{faces: 8, w: 1000, h: 1000, ax: -300 + 100*int32(i), ay: -400, texs: []*formats.GAFFrame{nil, sc.big4}, seed: 61 + i}))
		}
		yard := placeBody(placeBodyOpts{faces: 10, w: 1000, h: 900, ax: -200, ay: -300, texs: mixed, seed: 65})
		build := placeBody(placeBodyOpts{faces: 6, w: 100, h: 100, ax: 100, ay: 100, texs: flat, seed: 66, reveal: true})
		yard.Children = []drawlist.ModelChild{{Geometry: build, KeyDelta: 8}}
		yard.Shadow = placeShadow(yard, 4, 67)
		record(yard)
		for i := 0; i < 5; i++ {
			g := placeBody(placeBodyOpts{faces: 8, live: 1, w: 1000, h: 880, ax: -500 + 150*int32(i), ay: -200, texs: mixed, seed: 71 + i})
			if i == 4 {
				// An overflowed carrier draws its cargo in one painter order.
				g.Children = []drawlist.ModelChild{{Geometry: placeBody(placeBodyOpts{faces: 4, w: 20, h: 20, ax: 10, ay: 10, seed: 77}), KeyDelta: 40}}
				g.Cloaked = true
			}
			record(g)
		}
	}
	return list, f == 3
}

// runPlaceFrame places scenario frame f the way Execute does before Replay,
// then draws every subject the atlas could not hold through the fallback, as
// Replay's commits do, and returns the frame's digest.
func runPlaceFrame(r *Renderer, sc *placeScenario, f int) []byte {
	digest, _ := runPlaceFrameSections(r, sc, f)
	return digest
}

// runPlaceFrameSections is runPlaceFrame and the placement digest's sections.
func runPlaceFrameSections(r *Renderer, sc *placeScenario, f int) ([]byte, []placeSection) {
	list, walk := sc.frame(f)
	r.modelDirect.walkOutlines = walk
	r.modelStats = ModelStats{}
	r.modelPrep.reset()
	r.reflections.resetFrame()
	r.placeModelDirectFrame(&list)
	placed, sections := placeDigestSections(r)
	// The fallback builds its triangles in the lane's batch storage, so it
	// runs after the placement digest, as Replay runs after the page passes.
	r.sched.resetFrame(r.w, r.h)
	r.worldW, r.worldH = r.w, r.h
	list.VisitModels(func(m drawlist.Model) {
		if m.ShadowOnly || !r.modelDirectEligible(m.Geometry) {
			return
		}
		if region, ok := r.modelDirect.regions[m.Geometry]; !ok || !region.ok {
			r.drawModelDirectFallback(m.Geometry)
		}
	})
	fallback := fallbackDigest(r)
	return append(placed, fallback...), append(sections, placeSection{name: "fallback", sum: fallback})
}

// newPlaceRenderer is a renderer for the scenario: the finish switches on and
// two battle lights over the field, so the per-face light takes every path.
func newPlaceRenderer(t testing.TB) *Renderer {
	pal := fixturePalette()
	r, err := NewChecked(&pal, 640, 480)
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	r.metalGlint, r.materialsEnabled = true, true
	r.lighting.lights = []battleLight{
		{position: [3]float32{60, 60, 10}, color: [3]float32{1, 0.6, 0.2}, radius: 120},
		{position: [3]float32{200, 220, 4}, color: [3]float32{0.3, 0.5, 1}, radius: 160},
	}
	return r
}

// TestModelPlaceGoldenHash locks the lane's whole placement output over the
// scenario (see modelPlaceGolden). A refactor of placement must keep it.
func TestModelPlaceGoldenHash(t *testing.T) {
	skipAfterDeviceLoop(t)
	r := newPlaceRenderer(t)
	sc := newPlaceScenario()
	sum := sha256.New()
	var cov placeCoverage
	for f := 0; f < modelPlaceFrames; f++ {
		digest := runPlaceFrame(r, sc, f)
		t.Logf("frame %d: %x", f, digest[:8])
		sum.Write(digest)
		cov.add(r, f)
	}
	cov.check(t, r)
	got := hex.EncodeToString(sum.Sum(nil))
	want, ok := modelPlaceGolden[runtime.GOARCH]
	if !ok || want == "" {
		t.Skipf("no golden placement hash for %s; this run's is %s", runtime.GOARCH, got)
	}
	if got != want {
		t.Fatalf("placement hash %s, want %s", got, want)
	}
}

// placeCoverage proves the scenario took every path the golden hash is meant
// to lock, so a scenario edit cannot quietly stop covering one.
type placeCoverage struct {
	total               ModelStats
	turned              bool
	texturesAfterFirst  int
	texturesAtFirst     int
	reflectedLastFrame  bool
	reflected           bool
	secondPage          bool
	emptyRuns, runPages int
}

func (c *placeCoverage) add(r *Renderer, f int) {
	s := &r.modelStats
	t := &c.total
	t.DirectRetained += s.DirectRetained
	t.DirectWarm += s.DirectWarm
	t.DirectCaptured += s.DirectCaptured
	t.DirectColdPrimed += s.DirectColdPrimed
	t.DirectColdShape += s.DirectColdShape
	t.DirectColdGroup += s.DirectColdGroup
	t.DirectColdNoKey += s.DirectColdNoKey
	t.DirectColdReflect += s.DirectColdReflect
	t.DirectOverflow += s.DirectOverflow
	t.DirectCargoImages += s.DirectCargoImages
	t.DirectOutlineWalked += s.DirectOutlineWalked
	t.DirectOutlineRings += s.DirectOutlineRings
	t.DirectCulled += s.DirectCulled
	t.LitModelFaces += s.LitModelFaces
	t.MaterialFaces += s.MaterialFaces
	t.DirectShadows += s.DirectShadows
	t.DirectRetainedLive += s.DirectRetainedLive
	t.DirectRetainedOutline += s.DirectRetainedOutline
	t.Skipped += s.Skipped
	c.secondPage = c.secondPage || s.DirectPages == 2
	d := &r.modelDirect
	// A construction group whose child region landed on a later page than
	// the group's own: its allocations turned the page before its faces were
	// appended.
	for _, p := range d.pending {
		g := p.g
		if p.shadow || !modelGroupNeedsMerge(g) || !d.regions[g].ok {
			continue
		}
		for _, child := range g.Children {
			if cr, ok := d.regions[child.Geometry]; ok && cr.ok && cr.page != d.regions[g].page {
				c.turned = true
			}
		}
	}
	for i := range d.runs {
		if d.runs[i].vLen == 0 {
			c.emptyRuns++
		}
		if d.runs[i].page == 1 {
			c.runPages++
		}
	}
	if f == 0 {
		c.texturesAtFirst = len(r.textureAtlas.slots)
	}
	c.texturesAfterFirst = len(r.textureAtlas.slots)
	c.reflectedLastFrame = len(r.reflections.verts) != 0
	c.reflected = c.reflected || c.reflectedLastFrame
}

func (c *placeCoverage) check(t *testing.T, r *Renderer) {
	t.Helper()
	for _, p := range []struct {
		name string
		n    int
	}{
		{"replayed", c.total.DirectRetained}, {"warm", c.total.DirectWarm}, {"captured", c.total.DirectCaptured},
		{"primed", c.total.DirectColdPrimed}, {"shape change", c.total.DirectColdShape}, {"group", c.total.DirectColdGroup},
		{"no key", c.total.DirectColdNoKey}, {"reflecting", c.total.DirectColdReflect}, {"overflow", c.total.DirectOverflow},
		{"cargo image", c.total.DirectCargoImages}, {"walked ring", c.total.DirectOutlineWalked}, {"device ring", c.total.DirectOutlineRings},
		{"culled", c.total.DirectCulled}, {"lit", c.total.LitModelFaces}, {"material", c.total.MaterialFaces},
		{"shadow", c.total.DirectShadows}, {"skipped child", c.total.Skipped},
		{"replay under a live lane", c.total.DirectRetainedLive}, {"replay under an outline", c.total.DirectRetainedOutline},
		{"empty run", c.emptyRuns}, {"run on the second page", c.runPages},
	} {
		if p.n == 0 {
			t.Errorf("the scenario never took the %s path", p.name)
		}
	}
	if !c.secondPage {
		t.Error("no frame used the second atlas page")
	}
	if !c.turned {
		t.Error("no construction group's allocations turned the atlas page")
	}
	if c.texturesAfterFirst <= c.texturesAtFirst {
		t.Error("no texture frame was first resolved after the first frame")
	}
	if !c.reflected {
		t.Error("no frame reflected a model face")
	}
	if c.reflectedLastFrame {
		t.Error("the last frame reflected; the scenario's reflecting frames are two and three")
	}
}

// placeHasher writes the lane's state into a hash in a fixed order, mapping
// every image and texture frame to the order it was first met, so the digest
// depends on identity but not on addresses.
//
// Each named section is also hashed on its own, so a test comparing two
// lanes can say which part of the output differs.
type placeHasher struct {
	h        hash.Hash
	sec      hash.Hash
	sections []placeSection
	images   map[*ebiten.Image]int
	frames   map[*formats.GAFFrame]int
	buf      [8]byte
}

// placeSection is one named part of a digest and its own hash.
type placeSection struct {
	name string
	sum  []byte
}

func newPlaceHasher(r *Renderer) *placeHasher {
	return &placeHasher{h: sha256.New(), images: map[*ebiten.Image]int{r.tables.atlas: 0}, frames: map[*formats.GAFFrame]int{}}
}

func (p *placeHasher) write(b []byte) {
	p.h.Write(b)
	if p.sec != nil {
		p.sec.Write(b)
	}
}

// section starts the named part of the digest.
func (p *placeHasher) section(name string) {
	p.closeSection()
	p.h.Write([]byte(name))
	p.sec = sha256.New()
	p.sections = append(p.sections, placeSection{name: name})
}

func (p *placeHasher) closeSection() {
	if p.sec != nil {
		p.sections[len(p.sections)-1].sum = p.sec.Sum(nil)
		p.sec = nil
	}
}

func (p *placeHasher) u64(v uint64) {
	binary.LittleEndian.PutUint64(p.buf[:], v)
	p.write(p.buf[:])
}
func (p *placeHasher) i(v int64)   { p.u64(uint64(v)) }
func (p *placeHasher) f(v float32) { p.u64(uint64(math.Float32bits(v))) }
func (p *placeHasher) b(v bool) {
	if v {
		p.u64(1)
	} else {
		p.u64(0)
	}
}
func (p *placeHasher) img(v *ebiten.Image) {
	if v == nil {
		p.i(-1)
		return
	}
	id, ok := p.images[v]
	if !ok {
		id = len(p.images)
		p.images[v] = id
	}
	p.i(int64(id))
}
func (p *placeHasher) frame(v *formats.GAFFrame) {
	if v == nil {
		p.i(-1)
		return
	}
	id, ok := p.frames[v]
	if !ok {
		id = len(p.frames)
		p.frames[v] = id
	}
	p.i(int64(id))
}
func (p *placeHasher) vertex(v *ebiten.Vertex) {
	for _, f := range [...]float32{v.DstX, v.DstY, v.SrcX, v.SrcY, v.ColorR, v.ColorG, v.ColorB, v.ColorA, v.Custom0, v.Custom1, v.Custom2, v.Custom3} {
		p.f(f)
	}
}
func (p *placeHasher) region(r modelDirectRegion, present bool) {
	p.b(present)
	p.i(int64(r.x))
	p.i(int64(r.y))
	p.i(int64(r.page))
	p.i(int64(r.bounds.Min.X))
	p.i(int64(r.bounds.Min.Y))
	p.i(int64(r.bounds.Max.X))
	p.i(int64(r.bounds.Max.Y))
	p.b(r.ok)
}
func (p *placeHasher) rect(r image.Rectangle) {
	p.i(int64(r.Min.X))
	p.i(int64(r.Min.Y))
	p.i(int64(r.Max.X))
	p.i(int64(r.Max.Y))
}

// placeDigestSections is the digest of everything placement produced this frame.
//
// It also returns the hash of each section, which a failing comparison names.
func placeDigestSections(r *Renderer) ([]byte, []placeSection) {
	d := &r.modelDirect
	p := newPlaceHasher(r)
	section := p.section

	section("batch")
	p.i(int64(len(d.verts)))
	for i := range d.verts {
		p.vertex(&d.verts[i])
	}
	p.i(int64(len(d.idx)))
	for _, v := range d.idx {
		p.i(int64(v))
	}
	p.i(int64(len(d.runs)))
	for _, run := range d.runs {
		p.img(run.imgs[0])
		p.img(run.imgs[1])
		p.i(int64(run.page))
		p.i(int64(run.vOff))
		p.i(int64(run.vLen))
		p.i(int64(run.iOff))
		p.i(int64(run.iLen))
	}

	section("params")
	p.i(int64(d.params.count))
	p.write(d.params.buf[:d.params.count*modelQuadBytes])

	section("regions")
	for i := range d.pages {
		p.i(int64(d.pages[i].usedRows))
	}
	p.i(int64(d.page))
	p.i(int64(len(d.pending)))
	for _, pend := range d.pending {
		p.b(pend.shadow)
		region, ok := d.regions[pend.g]
		p.region(region, ok)
		if pend.shadow {
			continue
		}
		for _, child := range pend.g.Children {
			region, ok := d.regions[child.Geometry]
			p.region(region, ok)
			solo, ok := d.solo[child.Geometry]
			p.region(solo, ok)
		}
		if sg := pend.g.Shadow; sg != nil {
			region, ok := d.regions[sg]
			p.region(region, ok)
		}
	}
	p.i(int64(len(d.regions)))
	p.i(int64(len(d.solo)))

	section("merges")
	p.i(int64(len(d.groups.merges)))
	for _, m := range d.groups.merges {
		p.region(m.parent, true)
		p.region(m.child, true)
		p.b(m.key)
	}

	section("stats")
	p.write([]byte(fmt.Sprintf("%+v", r.modelStats)))

	section("store")
	s := &d.retain
	p.i(int64(s.count))
	for e := s.head; e != nil; e = e.next {
		p.u64(e.key.Body)
		p.u64(e.key.Revision)
		p.i(int64(e.key.HalfX))
		p.i(int64(e.key.HalfY))
		p.i(int64(e.key.Lane))
		p.b(e.primed)
		p.b(e.captured)
		if !e.captured {
			continue
		}
		p.i(int64(e.width))
		p.i(int64(e.height))
		p.i(int64(e.originX))
		p.i(int64(e.originY))
		p.i(int64(e.faces))
		p.b(e.hasSupersample)
		p.b(e.doubled)
		p.rect(e.slot)
		p.i(int64(len(e.segs)))
		for _, seg := range e.segs {
			p.img(seg.img)
			p.i(int64(seg.f0))
			p.i(int64(seg.f1))
			p.i(int64(seg.verts))
		}
		p.i(int64(len(e.verts)))
		for i := range e.verts {
			p.vertex(&e.verts[i])
		}
		p.i(int64(len(e.recs)))
		for _, rec := range e.recs {
			p.i(int64(rec.n))
			p.i(int64(rec.quad))
			p.f(rec.cx)
			p.f(rec.cy)
			p.f(rec.h)
			p.f(rec.normal[0])
			p.f(rec.normal[1])
			p.f(rec.normal[2])
		}
		p.i(int64(len(e.params)))
		p.write(e.params)
		p.i(int64(e.quads))
		p.i(int64(e.culled))
		p.i(int64(e.material))
	}
	p.i(int64(s.bodyCount))
	for b := s.bodyHead; b != nil; b = b.next {
		p.u64(b.key.body)
		p.i(int64(b.key.lane))
		p.b(b.doubled)
		p.img(b.page)
		p.b(b.ready)
		p.i(int64(len(b.faces)))
		for _, bf := range b.faces {
			p.i(int64(bf.index))
			p.i(int64(bf.n))
			p.frame(bf.texture)
			p.img(bf.img)
			p.i(int64(bf.tex.x))
			p.i(int64(bf.tex.y))
			p.f(bf.tex.quadA)
			p.f(bf.tex.linA)
			p.f(bf.tex.linB)
		}
	}

	section("reflections")
	rf := &r.reflections
	p.i(int64(len(rf.verts)))
	for i := range rf.verts {
		p.vertex(&rf.verts[i])
	}
	p.i(int64(len(rf.indices)))
	for _, v := range rf.indices {
		p.i(int64(v))
	}
	p.i(int64(len(rf.runs)))
	for _, run := range rf.runs {
		p.i(int64(run.page))
		p.i(int64(run.scenePage))
		p.i(int64(run.occlusionPage))
		p.i(int64(run.first))
		p.i(int64(run.count))
		p.i(int64(run.firstIndex))
		p.i(int64(run.indexCount))
	}
	p.b(rf.active != nil)
	p.closeSection()
	return p.h.Sum(nil), p.sections
}

// fallbackDigest is the digest of the page-overflow fallback's triangles as
// the scheduler compiled them, and of the accounting they added.
func fallbackDigest(r *Renderer) []byte {
	p := newPlaceHasher(r)
	p.write([]byte(fmt.Sprintf("%+v", r.modelStats)))
	sc := &r.sched
	p.i(int64(sc.nphase))
	for i := 0; i < sc.nphase; i++ {
		for class := range sc.phases[i].batch {
			b := &sc.phases[i].batch[class]
			p.i(int64(len(b.runs)))
			for _, run := range b.runs {
				for _, img := range run.imgs {
					p.img(img)
				}
				p.i(int64(run.vOff))
				p.i(int64(run.vLen))
				p.i(int64(run.iOff))
				p.i(int64(run.iLen))
			}
			p.i(int64(len(b.verts)))
			for j := range b.verts {
				p.vertex(&b.verts[j])
			}
			p.i(int64(len(b.idx)))
			for _, v := range b.idx {
				p.i(int64(v))
			}
		}
	}
	return p.h.Sum(nil)
}

// appendPacket appends one packet into a region on the lane's own context,
// decided inline, the way the sequential lane appends each packet of a job:
// the unit the retain, group and outline tests drive. The solo pass and the
// construction group region come from the context, as the job's packet
// would carry them.
func (r *Renderer) appendPacket(g *drawlist.ModelGeometry, region modelDirectRegion, shadow bool, keyDelta int32, group *drawlist.ModelGeometry) {
	c := r.laneCtx()
	c.runPage = region.page
	p := modelPlacePacket{g: g, region: region, shadow: shadow, keyDelta: keyDelta, group: group, solo: c.soloPass, groupReflection: c.groupReflection}
	r.appendPlaced(c, &p)
	c.flushStats(&r.modelStats)
}
