package client

// The unit view cull (docs/DESIGN_GPU_RENDERER.md §13.9 "The view bound").
//
// The bucket build admits units by plot row alone, as retail's painter does
// [03 R-RAST-01 §7]: retail clipped each finished draw, and a unit outside
// the screen cost it a blit that drew nothing. Here a unit's model present is
// the recorder's largest per-unit cost — piece transforms, projection and
// packet building, and the classic composer's raster — so a battle spread
// across the map spent most of that work on units whose every pixel the
// executor then clipped away. The cull keeps the row admission and the bucket
// order exactly as they are and skips only the model present of a unit whose
// conservative screen box lies wholly outside the record extent. Every pixel
// it skips is one the executor would have clipped, so it changes no picture,
// and it reads nothing the simulation owns [I6].

import (
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// unitViewMargin is the record pixels, at the native scale, added around a
// unit's box for what reaches past its projected geometry: the doubled and
// smooth projections' half pixels, the underwater refraction's displacement,
// an aircraft's soft-shadow disc (at most about twelve pixels at the highest
// softness), and the reflection's edge fattening.
const unitViewMargin = 32

// modelReach is the farthest any vertex of m can lie from the model origin,
// in whole world pixels rounded up, whatever its pieces' rotations: a piece's
// vertices lie within its farthest vertex of its own origin, and its origin
// within the summed lengths of its own and its ancestors' offsets of the model
// origin. Script translations move a piece beyond its offset and are added per
// unit (unitTranslationReach). Zero means no bound is known.
func modelReach(m *compiledmodel.Model) int32 {
	if m == nil || len(m.Pieces) == 0 {
		return 0
	}
	length := func(v [3]numeric.Fixed) float64 {
		x, y, z := float64(v[0].Raw())/65536, float64(v[1].Raw())/65536, float64(v[2].Raw())/65536
		return math.Sqrt(x*x + y*y + z*z)
	}
	reach := 0.0
	for i := range m.Pieces {
		p := &m.Pieces[i]
		far := 0.0
		for _, v := range p.Vertices {
			far = max(far, length(v))
		}
		// Walk the parent chain; a malformed link that would loop is cut at
		// the piece count rather than trusted.
		chain := 0.0
		for q, steps := i, 0; q >= 0 && q < len(m.Pieces) && steps <= len(m.Pieces); q, steps = m.Pieces[q].Parent, steps+1 {
			chain += length(m.Pieces[q].Translate)
		}
		reach = max(reach, chain+far)
	}
	return int32(math.Ceil(reach)) + 1
}

// unitTranslationReach bounds how far a unit's script translations move any
// vertex: each translation moves its piece's subtree by at most its own
// length, which the per-axis sum bounds.
func unitTranslationReach(pieces []frame.PieceView) int32 {
	var sum int64
	for _, p := range pieces {
		for _, t := range [3]numeric.Fixed{p.Tx, p.Ty, p.Tz} {
			if t != 0 {
				sum += (abs64(int64(t.Raw())) >> 16) + 1
			}
		}
	}
	return int32(min(sum, math.MaxInt32/4))
}

// unitOutsideView reports whether nothing of a unit's model present can reach
// the record extent: its body, its shadow, its water reflection. (sx, sy) is
// the unit's model anchor as the bucket build projected it. The box is the
// model reach projected through the same shears the body and shadow
// projections apply — a vertex's screen offset from its anchor is at most its
// reach horizontally and one and a half reaches vertically for the body (the
// height shear halves), one and a quarter both ways for the shadow (whose
// anchor is the ground under the unit) — and a reflection extends the body
// down by its height above the sea [R-REN-03D §2][R-REN-03D §3].
//
// A unit is never culled when nothing bounds it: a model with no known reach,
// the arriving commander (drawn above its position while it drops, with heat
// light of its own), and a unit presenting children — cargo hanging off its
// pieces, or a factory's occupants (indexFactoryOccupants) — whose reaches are
// their own. Nor is any unit while a parity trace records every present.
func (c *Client) unitOutsideView(u *frame.UnitView, sx, sy int32, recW, recH int) bool {
	if c == nil || c.cam == nil || recW <= 0 || recH <= 0 || c.rendererTraceSink != nil {
		return false
	}
	if c.arrivalMatches(*u) || c.worldBuckets.firstChild(u.Slot) >= 0 {
		return false
	}
	m := c.modelForUnit(*u)
	if m == nil || m.reach <= 0 {
		return false
	}
	return c.modelOutsideView(u.X, u.Y, u.Z, sx, sy, m.reach+unitTranslationReach(u.Pieces), recW, recH)
}

// modelOutsideView is unitOutsideView's box for any model present: a body
// standing at (x, y, z), anchored at (sx, sy), none of whose vertices lies
// farther than reach world pixels from its origin.
func (c *Client) modelOutsideView(x, y, z numeric.Fixed, sx, sy, reach int32, recW, recH int) bool {
	s := c.viewScale()
	r := s.Px(reach)
	margin := s.Px(unitViewMargin)
	// The body.
	bodyY := r + (r+1)/2
	x0, x1 := sx-r, sx+r
	y0, y1 := sy-bodyY, sy+bodyY
	if c.reflectionWaterAt(x, z) {
		// A reflected vertex lands its height above the sea below its own
		// place (water_reflections.go), and no vertex is higher than the
		// body's own height above the sea plus its reach.
		above := int32(max(0, int64(y-c.seaLevel())>>16)) + 1
		y1 += s.Px(above + reach)
	}
	// The shadow, at the ground under the body (shadowAnchorAt).
	gx, gy := c.cam.WorldToScreen(x, c.groundHeightUnder(x, z), z)
	gx += s.Px(shadowXOffset) - camera.OriginX
	gy -= camera.OriginY
	shadow := r + (r+3)/4
	x0, x1 = min(x0, gx-shadow), max(x1, gx+shadow)
	y0, y1 = min(y0, gy-shadow), max(y1, gy+shadow)
	x0, y0, x1, y1 = x0-margin, y0-margin, x1+margin, y1+margin
	return x1 <= 0 || y1 <= 0 || x0 >= int32(recW) || y0 >= int32(recH)
}
