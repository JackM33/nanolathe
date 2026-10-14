package gpurender

import (
	"fmt"
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// flatFeatureFrame is a w×h GAF frame of one index, every texel opaque.
func flatFeatureFrame(w, h int, index byte) *formats.GAFFrame {
	px := make([]byte, w*h)
	for i := range px {
		px[i] = index
	}
	return &formats.GAFFrame{Width: uint16(w), Height: uint16(h), Pixels: px}
}

// checkFeatureLayerDevicePixels draws two flat feature sprites — one keyed, one
// ALP-tinted — in a world region at a quarter scale, once as sprites and once as
// a baked feature layer (§16.14). The first frame bakes the layer and draws it
// from its page; a flat frame's interior is the sprites' colour, the keyed one
// exactly and the tinted half-colour to rounding, and a second frame bakes
// nothing.
func checkFeatureLayerDevicePixels() error {
	pal := fixturePalette()
	const w, h = 160, 120
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	items := []drawlist.FeatureLayerItem{
		{Frame: flatFeatureFrame(32, 24, 200), X: 40, Y: 40, Kind: drawlist.BlitFeatureNormal},
		{Frame: flatFeatureFrame(24, 24, 120), X: 100, Y: 40, Kind: drawlist.BlitFeatureShadow, Trans: true},
	}
	build := func(layer bool) *drawlist.List {
		var list drawlist.List
		list.RecordClear()
		list.RecordWorld(drawlist.WorldSpace{Begin: true, Factor: 0.25, Step: camera.ViewScaleNative, RecordW: 4 * w, RecordH: 4 * h})
		list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: 0, Y: 0, W: 4 * w, H: 4 * h}, Index: 7, Style: drawlist.FillSolid})
		if layer {
			list.RecordFeatureLayer(drawlist.FeatureLayer{ID: 3, Revision: 1, Level: 2,
				Bounds: drawlist.Rect{X: 40, Y: 40, W: 84, H: 24}, Items: items})
		} else {
			for _, it := range items {
				list.RecordSprite(it.Sprite(0, 0))
			}
		}
		list.RecordWorld(drawlist.WorldSpace{})
		list.RecordExpand()
		return &list
	}
	read := func(list *drawlist.List) []byte {
		p := make([]byte, w*h*4)
		r.Execute(list, w, h).ReadPixels(p)
		return p
	}
	sprites := read(build(false))
	layer := read(build(true))
	if got := r.modelStats; got.FeatureLayers != 1 || got.FeatureLayerBakes != 1 || got.FeatureLayerItems != 0 {
		return fmt.Errorf("feature layer fixture: first frame drew %d layers, baked %d and drew %d items; want 1, 1 and 0", got.FeatureLayers, got.FeatureLayerBakes, got.FeatureLayerItems)
	}
	read(build(true))
	if got := r.modelStats; got.FeatureLayers != 1 || got.FeatureLayerBakes != 0 {
		return fmt.Errorf("feature layer fixture: second frame drew %d layers and baked %d; want 1 and 0", got.FeatureLayers, got.FeatureLayerBakes)
	}
	// At a quarter, the keyed frame covers (10,10)-(18,16) and the tinted one
	// (25,10)-(31,16); their interiors are a pixel in from the edge.
	for _, box := range []struct {
		x0, y0, x1, y1 int
		tolerance      int
	}{{10, 10, 18, 16, 0}, {25, 10, 31, 16, 2}} {
		for y := box.y0 + 1; y < box.y1-1; y++ {
			for x := box.x0 + 1; x < box.x1-1; x++ {
				i := (y*w + x) * 4
				for ch := 0; ch < 3; ch++ {
					if d := int(layer[i+ch]) - int(sprites[i+ch]); d < -box.tolerance || d > box.tolerance {
						return fmt.Errorf("feature layer fixture: pixel (%d,%d) is %v from the layer, %v from the sprites", x, y, layer[i:i+3], sprites[i:i+3])
					}
				}
			}
		}
	}
	return nil
}

// TestFeatureLayerDevicePixels names the feature layers' device check; the
// hidden loop in TestMain runs it.
func TestFeatureLayerDevicePixels(t *testing.T) {
	if os.Getenv("NANOLATHE_GPU_DEVICE_TEST") != "1" {
		t.Skip("set NANOLATHE_GPU_DEVICE_TEST=1 for real-device feature layer fixtures")
	}
	if deviceFixtureResult != nil {
		t.Fatalf("device fixture loop: %v", deviceFixtureResult)
	}
}
