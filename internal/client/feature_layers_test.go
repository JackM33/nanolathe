package client

import (
	"cmp"
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// featureLayerFixture is a modern recording client in the strategic view over a
// 128-cell map, with the zoom fixture's sprite art.
func featureLayerFixture(t *testing.T) (*Client, *frame.Frame) {
	t.Helper()
	c := zoomRecorderClient(t)
	installZoomFeatureArt(c)
	c.geometryOnlyModels = true
	c.terrain = &world.Terrain{CellW: 128, CellH: 128}
	c.cam.Zoom, c.cam.Scale = camera.ZoomUnit/4, camera.ViewScaleNative
	feature := func(cx, cz int32, tall bool) frame.FeatureView {
		f := frame.FeatureView{CX: cx, CZ: cz, X: numeric.FixedFromInt(int64(16*cx + 5)), Z: numeric.FixedFromInt(int64(16*cz + 7)),
			Y: numeric.FixedFromInt(12), Filename: "zoom-fixture", SeqName: "body"}
		if tall {
			f.Height = 20
		}
		return f
	}
	cur := &frame.Frame{Features: []frame.FeatureView{
		feature(3, 4, false), feature(9, 2, true), feature(40, 5, false), feature(6, 6, false),
	}}
	cur.Features[3].IsBurning = true
	return c, cur
}

// recordFeaturePasses records both feature passes of one frame.
func recordFeaturePasses(c *Client, cur *frame.Frame) {
	c.list.Reset()
	c.drawFeaturePass(cur, true)
	c.drawWorldPass(cur, true)
}

// sortedSprites is the sprites' drawn fields — frame, placement, blitter and
// tint — in a fixed order. A static feature's body carries a lighting scale the
// executor reads only for a lit sprite, which it is not.
func sortedSprites(s []drawlist.Sprite) []drawlist.Sprite {
	out := make([]drawlist.Sprite, len(s))
	for i, sp := range s {
		out[i] = drawlist.Sprite{Frame: sp.Frame, X: sp.X, Y: sp.Y, Kind: sp.Kind, Trans: sp.Trans}
	}
	slices.SortFunc(out, func(a, b drawlist.Sprite) int {
		return cmp.Or(cmp.Compare(a.X, b.X), cmp.Compare(a.Y, b.Y), cmp.Compare(a.Kind, b.Kind))
	})
	return out
}

// The strategic view records its static sprite features as feature layers
// (DESIGN_GPU_RENDERER §16.14): the layers' items, at the layer's position, are
// exactly the sprites the feature passes would have recorded, a burning
// feature is still recorded live, an unchanged area keeps its revision and its
// items across frames and scrolling, and a changed feature rebuilds its own
// area alone.
func TestStrategicFeatureLayers(t *testing.T) {
	c, cur := featureLayerFixture(t)
	c.geometryOnlyModels = false // the live passes, for reference
	recordFeaturePasses(c, cur)
	want := sortedSprites(recordedFeatureSprites(&c.list))
	if len(want) != 4 || len(c.list.FeatureLayers()) != 0 {
		t.Fatalf("the live passes recorded %d sprites and %d layers; the fixture proves nothing", len(want), len(c.list.FeatureLayers()))
	}
	c.geometryOnlyModels = true
	recordFeaturePasses(c, cur)
	layers := c.list.FeatureLayers()
	if len(layers) != 2 {
		t.Fatalf("recorded %d feature layers, want one per area holding a static feature", len(layers))
	}
	if got := sortedSprites(recordedFeatureSprites(&c.list)); !slices.Equal(got, want) {
		t.Fatalf("the layers' items as sprites are %+v, want the live passes' %+v", got, want)
	}
	var direct int
	c.list.VisitSprites(func(drawlist.Sprite) { direct++ })
	if direct != 1 {
		t.Fatalf("recorded %d sprites outside the layers, want the burning feature's one", direct)
	}

	first := slices.Clone(layers)
	c.cam.X, c.cam.Z = c.cam.X+37, c.cam.Z+11
	recordFeaturePasses(c, cur)
	for i, l := range c.list.FeatureLayers() {
		if l.Revision != first[i].Revision || &l.Items[0] != &first[i].Items[0] {
			t.Fatal("an unchanged area was rebuilt by a frame that only scrolled")
		}
		if l.X != -c.cam.X || l.Y != -c.cam.Z {
			t.Fatalf("layer at (%d,%d), want the world origin's record position (%d,%d)", l.X, l.Y, -c.cam.X, -c.cam.Z)
		}
	}

	cur.Features[2].X += numeric.FixedFromInt(16)
	recordFeaturePasses(c, cur)
	again := c.list.FeatureLayers()
	if again[0].Revision != first[0].Revision || again[1].Revision == first[1].Revision {
		t.Fatal("a moved feature did not rebuild its own area alone")
	}

	c.cam.Zoom = camera.ZoomUnit
	recordFeaturePasses(c, cur)
	if n := len(c.list.FeatureLayers()); n != 0 {
		t.Fatalf("the ordinary view recorded %d feature layers", n)
	}
}
