package client

import (
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// Unit pick margin: a host preference that grows every unit's pick area — the
// projected hull quad, and a strategic icon's square — about its centre by a
// percentage of its size, so a slightly missed click or tap still lands
// (DESIGN_INTERFACE_HUD_INPUT "Unit pick margin"). Zero keeps the exact hull
// contract [07 R-REV-01 §5]; overlapping candidates still resolve by the
// hover score.

// SetUnitPickMargin installs the margin percentage, clamped to 0..100.
func (c *Client) SetUnitPickMargin(percent int) {
	if c != nil {
		c.pickMargin = int32(min(max(percent, 0), 100))
	}
}

// growQuad scales the quad about the mean of its corners by (100+margin)%.
func growQuad(q [4][2]int32, margin int32) [4][2]int32 {
	if margin <= 0 {
		return q
	}
	cx := (q[0][0] + q[1][0] + q[2][0] + q[3][0]) / 4
	cy := (q[0][1] + q[1][1] + q[2][1] + q[3][1]) / 4
	for i := range q {
		q[i][0] = cx + (q[i][0]-cx)*(100+margin)/100
		q[i][1] = cy + (q[i][1]-cy)*(100+margin)/100
	}
	return q
}

// hullReachAdmitsWithMargin widens the reach pre-test by the same factor, so
// a grown hull is never rejected before its polygon test.
func hullReachAdmitsWithMargin(box hullBox, v frame.UnitView, cam *camera.Camera, px, py, margin int32) bool {
	if margin <= 0 || !box.bounded {
		return hullReachAdmits(box, v, cam, px, py)
	}
	r := box.reach
	x0, y0 := cam.WorldToScreen(v.X.Sub(r), v.Y.Add(r), v.Z.Sub(r))
	x1, y1 := cam.WorldToScreen(v.X.Add(r), v.Y.Sub(r), v.Z.Add(r))
	// The grown quad stays within its centre plus (100+margin)% of the
	// reach rectangle, and the centre lies inside it.
	growX, growY := (x1-x0)*margin/100+1, (y1-y0)*margin/100+1
	px, py = px+camera.OriginX, py+camera.OriginY
	return px >= x0-growX && px <= x1+growX && py >= y0-growY && py <= y1+growY
}

// growMarker enlarges a strategic icon's square about its centre; the
// viewport clip is unchanged.
func growMarker(m drawlist.Marker, margin int32) drawlist.Marker {
	if margin > 0 {
		m.Size += m.Size * margin / 100
	}
	return m
}
