package client

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Strategic feature layers (docs/DESIGN_GPU_RENDERER.md §16.14). Zoomed out,
// the whole map's trees, rocks and other sprite features are in view, and the
// feature pass recorded every one of them as sprite commands on every frame,
// though almost none of them changes. In the strategic view the static sprite
// features are instead grouped by map area — a fixed square of
// featureLayerCells cells — and each area is recorded as one FeatureLayer: the
// same sprite commands, retained in the area's own coordinates and rebuilt only
// when one of the area's features changes, which an executor bakes into one
// image (drawlist.FeatureLayer).
//
// A feature is static when it is a sprite feature resting on its definition's
// first frame: not burning, animating, sinking or running a live event cursor
// (the burn, death and reclaim animations). An area's membership is taken over
// the whole map rather than the frame window, so a scrolling view does not
// change it; the window decides only which areas are recorded. Each member
// passes the feature gate the passes apply — the gate of its own pass, by its
// authored height — so a feature under nodrawundergray joins and leaves its
// area as the view's sight of it changes [03 R-RAST-01 §6].
//
// The layers are painted where the short-feature pass begins, in area row
// order, under every feature drawn live; the live passes skip what a layer
// drew. In the strategic view no unit model is drawn, so tall features lose
// only their interleaving with one another across area edges.
const featureLayerCells = 32

// featureLayerInputs are the inputs of an area's items beyond its features'
// records: the shadow preference, the art the frames resolve from and the
// terrain the anchors read. A change rebuilds every area.
type featureLayerInputs struct {
	shadows bool
	texGen  uint64
	terrain *world.Terrain
}

// featureLayerArea is one map area: its features as last built, the items
// built from them, and this frame's members.
type featureLayerArea struct {
	revision uint64
	views    []frame.FeatureView
	// live marks the views whose sprites a layer cannot carry (a frame with
	// subframes, which emitSprite expands); they are drawn live.
	live    []bool
	items   []drawlist.FeatureLayerItem
	bounds  drawlist.Rect
	members []int32
}

type featureLayerState struct {
	inputs     featureLayerInputs
	cols, rows int32
	areas      []featureLayerArea
	// layered marks, by feature index, the features this frame's layers draw;
	// the passes skip them. active is false on a frame that records none.
	layered []bool
	active  bool
	// serial numbers revisions across areas and maps, so no two builds share
	// one.
	serial uint64
}

// layeredFeature reports whether feature i is drawn by this frame's layers.
func (s *featureLayerState) layeredFeature(i int) bool {
	return s.active && i < len(s.layered) && s.layered[i]
}

// strategicLayersApply reports whether this frame records the strategic view's
// retained layers — the feature layers here and the terrain level (§16.15):
// the modern recording of the strategic view at the native record step.
func (c *Client) strategicLayersApply() bool {
	return c.geometryOnlyModels && c.cam != nil && c.terrain != nil &&
		c.cam.EffectiveScale().Native() && c.strategicView()
}

// layerStaticFeature reports whether f is a static sprite feature
// (featureLayerState).
func layerStaticFeature(f *frame.FeatureView) bool {
	// A named file and sequence is drawFeature's sprite path.
	if f.Filename == "" || f.SeqName == "" {
		return false
	}
	return !f.IsBurning && !f.Animating && !f.IsSinking && !f.RuntimeLive && f.EventSeqName == ""
}

// strategicLayerLevel is the layers' bake density for a live factor: the
// smallest power of two at or above it, so a screen pixel spans one to two
// layer texels. A layer covers the map and its texels are its memory, so it
// takes the coarser of the strategic densities (§16.13 takes twice this).
func strategicLayerLevel(z camera.Zoom) uint8 {
	return strategicDensityLevel(z, strategicSpriteLevelMax)
}

// strategicTerrainLevelMax bounds the terrain's level: at 2^-5 one terrain
// texel is a whole 32-pixel tile's average.
const strategicTerrainLevelMax = 5

// strategicTerrainLevel is the terrain's density for a live factor
// (DESIGN_GPU_RENDERER §16.15), the layers' rule: the smallest power of two at
// or above the factor.
func strategicTerrainLevel(z camera.Zoom) uint8 {
	return strategicDensityLevel(z, strategicTerrainLevelMax)
}

// strategicDensityLevel is the level, at most maxLevel, whose density 2^-level
// is the smallest power of two at or above the strategic factor z.
func strategicDensityLevel(z camera.Zoom, maxLevel uint8) uint8 {
	level := uint8(1)
	for level < maxLevel && z <= camera.ZoomUnit>>(level+1) {
		level++
	}
	return level
}

// recordFeatureLayers decides this frame's layers and records them; it runs
// where the short-feature pass begins.
func (c *Client) recordFeatureLayers(cur *frame.Frame) {
	s := &c.featureLayers
	s.active = false
	if cur == nil || !c.strategicLayersApply() {
		return
	}
	s.active = true
	in := featureLayerInputs{shadows: c.featureShadows, texGen: c.texGen, terrain: c.terrain}
	cols := (int32(c.terrain.CellW) + featureLayerCells - 1) / featureLayerCells
	rows := (int32(c.terrain.CellH) + featureLayerCells - 1) / featureLayerCells
	if in != s.inputs || cols != s.cols || rows != s.rows {
		s.inputs, s.cols, s.rows = in, cols, rows
		s.areas = make([]featureLayerArea, int(cols*rows))
	}
	for i := range s.areas {
		s.areas[i].members = s.areas[i].members[:0]
	}
	if cap(s.layered) < len(cur.Features) {
		s.layered = make([]bool, len(cur.Features))
	}
	s.layered = s.layered[:len(cur.Features)]
	clear(s.layered)
	for i := range cur.Features {
		f := &cur.Features[i]
		if !layerStaticFeature(f) || f.CX < 0 || f.CZ < 0 {
			continue
		}
		col, row := f.CX/featureLayerCells, f.CZ/featureLayerCells
		if col >= cols || row >= rows {
			continue
		}
		visible := featureVisibleForFrame(cur, *f)
		if f.Height >= 10 {
			visible = tallFeatureVisibleForFrame(cur, *f)
		}
		if !visible {
			continue
		}
		a := &s.areas[row*cols+col]
		a.members = append(a.members, int32(i))
	}
	win := c.worldWindow()
	level := strategicLayerLevel(c.liveZoom())
	for ai := range s.areas {
		a := &s.areas[ai]
		if !a.matches(cur.Features) {
			c.buildFeatureLayer(a, cur.Features)
		}
		for k, i := range a.members {
			s.layered[i] = !a.live[k]
		}
		if len(a.items) == 0 {
			continue
		}
		row, col := int32(ai)/cols, int32(ai)%cols
		if row*featureLayerCells >= win.firstRow+win.rowCount || (row+1)*featureLayerCells <= win.firstRow ||
			col*featureLayerCells >= win.firstCol+win.colCount || (col+1)*featureLayerCells <= win.firstCol {
			continue
		}
		// An item is placed in the coordinates of a camera at the world
		// origin; at the native step a feature's record position is that less
		// the camera's own (featureScreenPos), so the layer's origin is too.
		c.list.RecordFeatureLayer(drawlist.FeatureLayer{
			ID: uint64(ai), Revision: a.revision, Level: level,
			X: -c.cam.X, Y: -c.cam.Z, Bounds: a.bounds, Items: a.items,
		})
	}
}

// matches reports whether this frame's members are the features the area was
// last built from, record for record.
func (a *featureLayerArea) matches(features []frame.FeatureView) bool {
	if len(a.members) != len(a.views) {
		return false
	}
	for k, i := range a.members {
		if features[i] != a.views[k] {
			return false
		}
	}
	return true
}

// buildFeatureLayer rebuilds an area from this frame's members: the sprite
// commands drawFeature would record for each, shadow then body, in the
// coordinates of a camera at the world origin. A new item slice is made rather
// than the old one written, because lists already recorded share it.
func (c *Client) buildFeatureLayer(a *featureLayerArea, features []frame.FeatureView) {
	s := &c.featureLayers
	s.serial++
	a.revision = s.serial
	a.views = a.views[:0]
	a.live = a.live[:0]
	var items []drawlist.FeatureLayerItem
	var bounds drawlist.Rect
	for _, i := range a.members {
		f := features[i]
		a.views = append(a.views, f)
		sx, sy := c.featureWorldRecordPos(f)
		var shadowFrame *formats.GAFFrame
		if c.featureShadows {
			shadowFrame = c.viewFrame(c.featureFrameFor(f, true))
		}
		normalFrame := c.viewFrame(c.featureFrameFor(f, false))
		if shadowFrame != nil && len(shadowFrame.Subframes) != 0 || normalFrame != nil && len(normalFrame.Subframes) != 0 {
			a.live = append(a.live, true)
			continue
		}
		a.live = append(a.live, false)
		for _, part := range [2]struct {
			frame *formats.GAFFrame
			kind  drawlist.BlitKind
			trans bool
		}{{shadowFrame, drawlist.BlitFeatureShadow, f.ShadTrans}, {normalFrame, drawlist.BlitFeatureNormal, f.AnimTrans}} {
			if part.frame == nil {
				continue
			}
			it := drawlist.FeatureLayerItem{
				Frame: part.frame, Kind: part.kind, Trans: part.trans,
				X: sx - int32(part.frame.XOffset), Y: sy - int32(part.frame.YOffset),
			}
			items = append(items, it)
			bounds = unionLayerRect(bounds, drawlist.Rect{X: it.X, Y: it.Y, W: int32(part.frame.Width), H: int32(part.frame.Height)})
		}
	}
	a.items, a.bounds = items, bounds
}

// featureWorldRecordPos is featureScreenPos for a camera at the world origin at
// the native step.
func (c *Client) featureWorldRecordPos(f frame.FeatureView) (int32, int32) {
	y := c.featureAnchorY(f)
	wx := int32(int64(f.X) >> 16)
	wy := int32(int64(y) >> 16)
	wz := int32(int64(f.Z) >> 16)
	return wx, wz - (wy >> 1)
}

// unionLayerRect is the smallest rectangle holding both; an empty one holds
// nothing.
func unionLayerRect(a, b drawlist.Rect) drawlist.Rect {
	if b.W <= 0 || b.H <= 0 {
		return a
	}
	if a.W <= 0 || a.H <= 0 {
		return b
	}
	x0, y0 := min(a.X, b.X), min(a.Y, b.Y)
	x1, y1 := max(a.X+a.W, b.X+b.W), max(a.Y+a.H, b.Y+b.H)
	return drawlist.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}
