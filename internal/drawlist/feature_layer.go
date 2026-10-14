package drawlist

import "github.com/nanolathe-gg/nanolathe/formats"

// FeatureLayer is one map area's static sprite features in the strategic view
// (docs/DESIGN_GPU_RENDERER.md §16.14): the trees, rocks and other sprite
// features of a fixed square of the map, as the sprite commands the feature
// pass would record for them, retained by the recorder and rebuilt only when
// one of them changes. An executor may bake the items once into an image of the
// area at 2^-Level texels per record pixel and draw that image, keyed on ID and
// Revision; one without the hook replays the items as the sprites they are.
type FeatureLayer struct {
	// ID names the map area; Revision changes whenever its items do.
	ID, Revision uint64
	// Level is the bake's density: 2^-Level texels per record pixel.
	Level uint8
	// X, Y is this frame's record position of the items' origin.
	X, Y int32
	// Bounds is the union of the items' rectangles in their own coordinates.
	Bounds Rect
	// Items are the area's sprite commands in paint order. The recorder never
	// writes a recorded slice again — a change records a new one — so lists and
	// their clones share it.
	Items []FeatureLayerItem
}

// FeatureLayerItem is one feature sprite of a layer: the frame, its top-left in
// the layer's coordinates, the feature blitter it takes and whether that
// blitter is the ALP-tinted one [03 R-RAST-01 §6].
type FeatureLayerItem struct {
	Frame *formats.GAFFrame
	X, Y  int32
	Kind  BlitKind
	Trans bool
}

// Sprite is the item as the sprite command the feature pass records for it,
// placed at the layer's position this frame.
func (it FeatureLayerItem) Sprite(x, y int32) Sprite {
	return Sprite{Frame: it.Frame, X: x + it.X, Y: y + it.Y, Kind: it.Kind, Trans: it.Trans}
}

// FeatureLayerSink is the optional interface an executor implements to draw a
// FeatureLayer as one image. An executor without it is replayed the layer's
// items as Sprite commands, which is exactly what the feature pass would have
// recorded.
type FeatureLayerSink interface {
	FeatureLayer(FeatureLayer)
}

// RecordFeatureLayer appends one feature layer in record order.
func (l *List) RecordFeatureLayer(c FeatureLayer) {
	l.order = append(l.order, tag{familyFeatureLayer, len(l.featureLayers)})
	l.featureLayers = append(l.featureLayers, c)
}

// FeatureLayers returns the recorded feature layers in record order. It is a
// diagnostic and test accessor.
func (l *List) FeatureLayers() []FeatureLayer { return l.featureLayers }

// replayFeatureLayer sends one layer to the executor's hook, or its items as
// sprites to an executor without one.
func replayFeatureLayer(s Sink, hook FeatureLayerSink, c FeatureLayer) {
	if hook != nil {
		hook.FeatureLayer(c)
		return
	}
	for _, it := range c.Items {
		s.Sprite(it.Sprite(c.X, c.Y))
	}
}
