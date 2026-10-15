package client

// The effect view bound (docs/DESIGN_GPU_RENDERER.md §13.9 "The view bound").
//
// The effect, strip and projectile passes walk every record the simulation
// published, wherever it is on the map, as retail's composer did: each draw
// clipped itself, and a record outside the screen cost a blit that drew
// nothing [03 §1][03 R-COMP-01 §2]. A spread-out battle keeps thousands of
// smoke puffs and hundreds of explosions alive far from the camera, and each
// one still became a recorded command that the executor fetched art for,
// tested against the battle's lights and then clipped away. These tests drop
// a record only when nothing an executor builds from it can reach the record
// extent — its pixels, its water reflection, its glow, its battle light and its
// blast ring — so every skip is one the executor would have clipped, and the
// light budget never held such a source either (its own gather rejects a
// source whose reach misses the same extent). The producers still build the
// whole record first, so what a record carries and every counter they keep are
// unchanged; only its emission is skipped. Nothing here reads simulation state
// [I6].

import (
	"math"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// recordBoxOutside reports whether the record-space box [x0, x1) × [y0, y1)
// lies wholly outside the record extent. An unknown extent culls nothing.
func (c *Client) recordBoxOutside(x0, y0, x1, y1 int32) bool {
	w, h := c.recordExtent()
	if w <= 0 || h <= 0 {
		return false
	}
	return x1 <= 0 || y1 <= 0 || x0 >= int32(w) || y0 >= int32(h)
}

// frameBox is the box a frame covers relative to its anchor. A composite
// draws its ordered leaves at the same anchor by their own offsets, not its
// own canvas (tintedBlitAnchor, emitSprite), so its box is the union of theirs
// and its own.
func frameBox(f *formats.GAFFrame) (x0, y0, x1, y1 int32) {
	x0, y0 = -int32(f.XOffset), -int32(f.YOffset)
	x1, y1 = x0+int32(f.Width), y0+int32(f.Height)
	for _, child := range f.Subframes {
		if child == nil {
			continue
		}
		cx0, cy0, cx1, cy1 := frameBox(child)
		x0, y0, x1, y1 = min(x0, cx0), min(y0, cy0), max(x1, cx1), max(y1, cy1)
	}
	return
}

// effectSpriteOutsideView reports whether nothing of an anchored world effect
// sprite — a keyed effect or projectile frame, or a tinted strip frame — can
// reach the record extent. Its frame covers its box at the anchor; a keyed
// billboard over water is mirrored about its anchor's water-plane projection,
// so its reflection spans the box flipped about the anchor and moved down by
// its height above the sea (water_reflections.go); its glow never leaves its
// own clipped box; and an emitter lights, and an explosion distorts, no
// farther than Sprite.Reach from its anchor.
func (c *Client) effectSpriteOutsideView(sp *drawlist.Sprite) bool {
	f := sp.Frame
	if f == nil || !(sp.Anchored || sp.Kind == drawlist.BlitTinted) {
		return false
	}
	bx0, by0, bx1, by1 := frameBox(f)
	x0, y0, x1, y1 := sp.X+bx0, sp.Y+by0, sp.X+bx1, sp.Y+by1
	if sp.ReflectWater && sp.ReflectionHeight >= 0 && sp.Kind == drawlist.BlitKeyed {
		// A row at y lands at 2·Y + height − y.
		pivot := 2*float64(sp.Y) + float64(sp.ReflectionHeight)
		ry0, ry1 := int32(math.Floor(pivot-float64(y1))), int32(math.Ceil(pivot-float64(y0)))+1
		y0, y1 = min(y0, ry0), max(y1, ry1)
	}
	if r := int32(math.Ceil(float64(sp.Reach()))); r > 0 {
		x0, y0, x1, y1 = min(x0, sp.X-r), min(y0, sp.Y-r), max(x1, sp.X+r+1), max(y1, sp.Y+r+1)
	}
	return c.recordBoxOutside(x0, y0, x1, y1)
}

// lineOutsideView reports whether nothing of a projectile stroke can reach the
// record extent: the stroke itself, its reflection — each endpoint moved down
// by its height above the sea, one pixel either side — and, for an emissive
// stroke, its glow and its light (Line.Reach).
func (c *Client) lineOutsideView(l *drawlist.Line) bool {
	x0, x1 := min(l.X0, l.X1), max(l.X0, l.X1)+1
	y0, y1 := min(l.Y0, l.Y1), max(l.Y0, l.Y1)+1
	if l.ReflectWater {
		d := int32(math.Ceil(float64(max(l.ReflectionHeight0, l.ReflectionHeight1, 0)))) + 2
		y1 += d
		x0, x1 = x0-2, x1+2
	}
	r := int32(math.Ceil(float64(l.Reach())))
	return c.recordBoxOutside(x0-r, y0-r, x1+r, y1+r)
}

// emitViewLine records a projectile stroke unless nothing of it can reach the
// view.
func (c *Client) emitViewLine(l drawlist.Line) {
	if !c.lineOutsideView(&l) {
		c.emitLine(l)
	}
}

// projectileModelOutsideView reports whether nothing of a projectile's model
// present can reach the record extent. A projectile piece is drawn standalone
// at the projectile's own point with its authored translation dropped
// (BuildProjectileModelPiecesInto), so the model's reach bounds every vertex
// of both the piece and its header child, and the unit box applies
// (modelOutsideView). A drawn projectile counts as drawn either way. Nothing
// is culled while a parity trace records every present.
func (c *Client) projectileModelOutsideView(p frame.ProjectileView, m *unitModel) bool {
	if c == nil || c.cam == nil || m == nil || m.reach <= 0 || c.rendererTraceSink != nil {
		return false
	}
	recW, recH := c.recordExtent()
	if recW <= 0 || recH <= 0 {
		return false
	}
	sx, sy := c.modelAnchorAt([3]numeric.Fixed{p.X, p.Y, p.Z})
	return c.modelOutsideView(p.X, p.Y, p.Z, sx, sy, m.reach, recW, recH)
}
