package drawlist

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
)

// layerRecorder is the recorder with the feature layer hook.
type layerRecorder struct {
	recorder
	layers []FeatureLayer
}

func (r *layerRecorder) FeatureLayer(c FeatureLayer) {
	r.tags = append(r.tags, "layer")
	r.layers = append(r.layers, c)
}

// A feature layer replays in record order: to an executor with the hook as
// the layer, and to one without as its items' sprites at the layer's position,
// which is what the feature pass would have recorded (§16.14). A clone shares
// the items, which the recorder never writes again.
func TestFeatureLayerReplay(t *testing.T) {
	frame := &formats.GAFFrame{Width: 4, Height: 3}
	layer := FeatureLayer{ID: 2, Revision: 9, Level: 1, X: -30, Y: -40, Bounds: Rect{X: 100, Y: 50, W: 20, H: 10},
		Items: []FeatureLayerItem{
			{Frame: frame, X: 100, Y: 50, Kind: BlitFeatureShadow, Trans: true},
			{Frame: frame, X: 110, Y: 52, Kind: BlitFeatureNormal},
		}}
	var l List
	l.RecordFill(Fill{})
	l.RecordFeatureLayer(layer)
	l.RecordFill(Fill{})

	var plain recorder
	l.Replay(&plain)
	if got := plain.tags; len(got) != 4 || got[1] != "sprite" || got[2] != "sprite" {
		t.Fatalf("an executor without the hook replayed %v, want the items as sprites between the fills", got)
	}
	if want := (Sprite{Frame: frame, X: 80, Y: 12, Kind: BlitFeatureNormal}); plain.lastSprite != want {
		t.Fatalf("the last item replayed as %+v, want %+v", plain.lastSprite, want)
	}

	var hooked layerRecorder
	clone := l.Clone()
	l.Reset()
	clone.Replay(&hooked)
	if got := hooked.tags; len(got) != 3 || got[1] != "layer" {
		t.Fatalf("an executor with the hook replayed %v", got)
	}
	if got := hooked.layers[0]; got.Revision != 9 || &got.Items[0] != &layer.Items[0] {
		t.Fatal("the clone did not carry the layer with its shared items")
	}
	var into List
	clone.CloneInto(&into)
	if len(into.FeatureLayers()) != 1 || &into.FeatureLayers()[0].Items[0] != &layer.Items[0] {
		t.Fatal("CloneInto did not carry the layer")
	}
}
