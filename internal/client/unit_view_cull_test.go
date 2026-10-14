package client

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	presentationrender "github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// cullFixtureModel is a three-piece chain whose pieces sit off their parents'
// origins and whose vertices sit off their own, so a rotation anywhere in the
// chain swings the tip through the whole reach.
func cullFixtureModel() *unitModel {
	m := syntheticModel([]pieceInfo{
		{name: "base", parent: -1, translate: [3]float64{0, 0, 0}},
		{name: "turret", parent: 0, translate: [3]float64{10, 8, -4}},
		{name: "barrel", parent: 1, translate: [3]float64{6, 12, 3}},
	}, []syntheticTri{
		makeTriangle(0, "base", [3][3]float64{{-12, 0, -9}, {12, 0, -9}, {0, 4, 11}}, 10, 0),
		makeTriangle(1, "turret", [3][3]float64{{-5, 0, -5}, {5, 3, -5}, {0, 6, 7}}, 20, 1),
		makeTriangle(2, "barrel", [3][3]float64{{-1, 0, 0}, {1, 0, 0}, {0, 1, 18}}, 30, 2),
	}, 0)
	m.reach = modelReach(m.compiled)
	return m
}

// The reach bounds every posed vertex's distance from the unit's origin: under
// any heading, pitch and bank, any piece rotations, and script translations,
// which unitTranslationReach adds. Too small a reach would cull a unit whose
// edge is on screen, so this is the bound the cull rests on.
func TestModelReachBoundsEveryPose(t *testing.T) {
	c := newPieceFixtureClient(t)
	m := cullFixtureModel()
	rng := rand.New(rand.NewPCG(7, 11))
	fixed := func(v float64) numeric.Fixed { return numeric.Fixed(int64(v * 65536)) }
	for range 500 {
		pieces := make([]frame.PieceView, 3)
		for i := range pieces {
			pieces[i] = frame.PieceView{Index: i,
				RotX: uint16(rng.IntN(65536)), RotY: uint16(rng.IntN(65536)), RotZ: uint16(rng.IntN(65536))}
			if rng.IntN(2) == 0 {
				pieces[i].Tx, pieces[i].Ty, pieces[i].Tz = fixed(rng.Float64()*40-20), fixed(rng.Float64()*40-20), fixed(rng.Float64()*40-20)
			}
		}
		v := frame.UnitView{X: fixed(300), Y: fixed(20), Z: fixed(400), Pieces: pieces}
		draw := presentationrender.BuildUnitDrawInto(m.compiled, c.modelStates(m, pieces),
			uint16(rng.IntN(65536)), uint16(rng.IntN(65536)), uint16(rng.IntN(65536)), v,
			&presentationrender.OrientationCache{}, c.borrowDrawScratch())
		bound := float64(m.reach + unitTranslationReach(pieces))
		for _, p := range draw.Pieces {
			for _, w := range p.WorldVertices {
				dx := float64(w[0].Sub(draw.WorldPos[0]).Raw()) / 65536
				dy := float64(w[1].Sub(draw.WorldPos[1]).Raw()) / 65536
				dz := float64(w[2].Sub(draw.WorldPos[2]).Raw()) / 65536
				if d := math.Sqrt(dx*dx + dy*dy + dz*dz); d > bound {
					t.Fatalf("a posed vertex lies %.2f from the origin, beyond the reach %.0f", d, bound)
				}
			}
		}
	}
}

// The cull skips a unit's model only when its whole box — body, shadow and
// margin — lies outside the record extent; a unit whose reach crosses an edge,
// a model with no known reach, and a unit presenting children are kept.
func TestUnitOutsideView(t *testing.T) {
	c := newTestClient(t)
	c.cam = &camera.Camera{X: 0, Z: 0, ViewW: 640, ViewH: 480, MapW: 8192, MapH: 8192}
	m := cullFixtureModel()
	c.models = map[string]*unitModel{"cull": m, "nobound": {compiled: m.compiled}}
	at := func(x, z int32, model string) (frame.UnitView, int32, int32) {
		u := frame.UnitView{Model: model, X: numeric.Fixed(int64(x) << 16), Z: numeric.Fixed(int64(z) << 16)}
		sx, sy := c.cam.WorldToScreen(u.X, u.Y, u.Z)
		return u, sx - camera.OriginX, sy - camera.OriginY
	}
	outside := func(u frame.UnitView, sx, sy int32) bool { return c.unitOutsideView(&u, sx, sy, 640, 480) }
	near := m.reach + unitViewMargin // within this of an edge the box reaches in

	far, fx, fy := at(4000, 240, "cull")
	if !outside(far, fx, fy) {
		t.Fatal("a unit thousands of pixels right of the view was kept")
	}
	inView, ix, iy := at(320, 240, "cull")
	if outside(inView, ix, iy) {
		t.Fatal("a unit in the middle of the view was culled")
	}
	edge, ex, ey := at(640+near-1, 240, "cull")
	if outside(edge, ex, ey) {
		t.Fatal("a unit whose reach crosses the right edge was culled")
	}
	above, ax, ay := at(320, -near, "cull")
	if outside(above, ax, ay) {
		t.Fatal("a unit whose reach crosses the top edge was culled")
	}
	unbounded, ux, uy := at(4000, 240, "nobound")
	if outside(unbounded, ux, uy) {
		t.Fatal("a model with no known reach was culled")
	}

	// A carrier whose cargo is indexed presents children and is kept.
	carrier, cx, cy := at(4000, 240, "cull")
	carrier.Slot, carrier.Cargo = pool.Handle(1), []pool.Handle{2}
	cargo := frame.UnitView{Model: "cull", Slot: 2, Carrier: 1, X: carrier.X, Z: carrier.Z}
	c.worldBuckets.indexChildren([]frame.UnitView{carrier, cargo})
	if outside(carrier, cx, cy) {
		t.Fatal("a carrier presenting cargo was culled")
	}
}
