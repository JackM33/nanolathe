package gpurender

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// The lane spends no region, no page faces and no commit on a subject whose
// every committed image lies wholly outside the frame's world clip: the
// commits test the same rectangle, so those pixels were never drawn
// (modelDirectClipsAway). A shadow that is still in frame keeps its subject
// placed — the commit order is the shadow's, but the body's raster is what a
// recorded shadow packet draws from — and a reflecting subject is never
// skipped, because its reflection is committed from geometry of its own.
func TestModelDirectClipsAway(t *testing.T) {
	r := newPlaceRenderer(t)
	r.worldW, r.worldH = 640, 480
	var list drawlist.List
	record := func(g *drawlist.ModelGeometry) { list.RecordModel(drawlist.Model{Geometry: g}) }

	off := placeBody(placeBodyOpts{faces: 4, w: 30, h: 20, ax: -1000, ay: 100, seed: 1})
	on := placeBody(placeBodyOpts{faces: 4, w: 30, h: 20, ax: 100, ay: 100, seed: 2})
	shadowInFrame := placeBody(placeBodyOpts{faces: 4, w: 30, h: 20, ax: -34, ay: 100, seed: 3})
	shadowInFrame.Shadow = placeShadow(shadowInFrame, 2, 4)
	reflecting := placeBody(placeBodyOpts{faces: 4, w: 30, h: 20, ax: -1000, ay: 100, seed: 5})
	reflecting.ReflectWater = true
	submerged := placeBody(placeBodyOpts{faces: 4, w: 30, h: 20, ax: -1000, ay: 100, seed: 9})
	submerged.Waterline = drawlist.ModelWaterlineBlue
	airborne := placeBody(placeBodyOpts{faces: 4, w: 30, h: 20, ax: -1000, ay: 100, seed: 10})
	airborne.AircraftShadowHeight = 200
	record(off)
	record(on)
	record(shadowInFrame)
	record(reflecting)
	record(submerged)
	record(airborne)
	// One shadow-only command whose shadow clips away and one whose does not.
	awayOnly := placeBody(placeBodyOpts{faces: 4, w: 30, h: 20, ax: 100, ay: 100, seed: 6})
	awayOnly.Shadow = &drawlist.ModelGeometry{Eligible: true, KeyPlane: true, Width: 30, Height: 20, AnchorX: -1000, AnchorY: 100, Scale: 1}
	nearOnly := placeBody(placeBodyOpts{faces: 4, w: 30, h: 20, ax: 100, ay: 100, seed: 7})
	nearOnly.Shadow = &drawlist.ModelGeometry{Eligible: true, KeyPlane: true, Width: 30, Height: 20, AnchorX: 200, AnchorY: 100, Scale: 1}
	list.RecordModel(drawlist.Model{ShadowOnly: true, Geometry: awayOnly})
	list.RecordModel(drawlist.Model{ShadowOnly: true, Geometry: nearOnly})

	r.modelStats = ModelStats{}
	r.placeModelDirectFrame(&list)

	if r.modelStats.DirectClipped != 2 {
		t.Fatalf("clipped %d subjects, want the off-clip body and the off-clip shadow-only command", r.modelStats.DirectClipped)
	}
	if _, ok := r.modelDirect.regions[off]; ok {
		t.Fatal("the off-clip body took a region")
	}
	if _, ok := r.modelDirect.regions[awayOnly.Shadow]; ok {
		t.Fatal("the off-clip shadow-only command took a region")
	}
	for _, g := range []*drawlist.ModelGeometry{on, shadowInFrame, reflecting, submerged, airborne, nearOnly.Shadow} {
		if _, ok := r.modelDirect.regions[g]; !ok {
			t.Fatal("a subject whose commit is in frame took no region")
		}
	}

	// Below a rest factor the frame's record extent is wider than the
	// framebuffer and it is the rectangle the commits clip against: a subject
	// outside the framebuffer but inside the record extent is still placed.
	var zoomed drawlist.List
	zoomed.RecordWorld(drawlist.WorldSpace{Begin: true, Zoom: camera.ZoomUnit / 2, Step: camera.ViewScaleNative, RecordW: 1280, RecordH: 960})
	wide := placeBody(placeBodyOpts{faces: 4, w: 30, h: 20, ax: 1000, ay: 100, seed: 8})
	zoomed.RecordModel(drawlist.Model{Geometry: wide})
	r.modelStats = ModelStats{}
	r.placeModelDirectFrame(&zoomed)
	if r.modelStats.DirectClipped != 0 {
		t.Fatal("a subject inside the record extent but outside the framebuffer was clipped")
	}
	if _, ok := r.modelDirect.regions[wide]; !ok {
		t.Fatal("a subject inside the record extent took no region")
	}
}
