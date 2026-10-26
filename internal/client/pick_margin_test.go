package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// The margin grows the drawn hull about its centre: misses just outside the
// exact rectangle land, the exact contract holds at zero, and a click well
// clear of the grown hull still misses.
func TestUnitPickMarginGrowsTheHull(t *testing.T) {
	cam := &camera.Camera{ViewW: 640, ViewH: 480, MapW: 4096, MapH: 4096}
	m := hullTestModel()
	src := UnitHullModelFunc(func(string) *compiledmodel.Model { return m })
	f := &frame.Frame{Units: []frame.UnitView{hullTestView()}}
	near := [][2]int32{{117, 177}, {91, 200}, {104, 213}, {104, 175}}
	for _, p := range near {
		if _, _, ok := PickSnapshotUnitWithMargin(f, p[0], p[1], cam, 0, 0, src); ok {
			t.Fatalf("zero margin admitted %v outside the exact hull", p)
		}
		if h, _, ok := PickSnapshotUnitWithMargin(f, p[0], p[1], cam, 0, 25, src); !ok || h != 1 {
			t.Fatalf("a 25%% margin missed %v just outside the hull", p)
		}
	}
	for _, p := range [][2]int32{{122, 194}, {86, 194}, {104, 219}, {104, 169}} {
		if _, _, ok := PickSnapshotUnitWithMargin(f, p[0], p[1], cam, 0, 25, src); ok {
			t.Fatalf("a 25%% margin admitted %v clear of the grown hull", p)
		}
	}
	// A unit whose exact hull holds the pointer beats a neighbour whose grown
	// hull also reaches it, whatever their hover scores.
	beside := hullTestView()
	beside.Slot = 2
	beside.X = beside.X.Add(numericFromInt(20))
	f.Units = append(f.Units, beside)
	bx, by := hoverHullCentre(t, m, beside, cam)
	if h, _, ok := PickSnapshotUnitWithMargin(f, bx, by, cam, 0, 100, src); !ok || h != 2 {
		t.Fatalf("a click inside unit 2's exact hull picked %d", h)
	}
}

func numericFromInt(n int64) numeric.Fixed { return numeric.FixedFromInt(n) }

func hoverHullCentre(t *testing.T, m *compiledmodel.Model, v frame.UnitView, cam *camera.Camera) (int32, int32) {
	t.Helper()
	q, ok := hoverHullCorners(m, v, cam)
	if !ok {
		t.Fatal("no hull")
	}
	return (q[0][0] + q[1][0] + q[2][0] + q[3][0]) / 4, (q[0][1] + q[1][1] + q[2][1] + q[3][1]) / 4
}
