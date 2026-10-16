package features

import (
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

// TestAppendInstancesNearMatchesTheFullList locks the position index: every
// query returns exactly the instances of AppendInstances whose coordinate
// words lie inside the square, in AppendInstances' order — through stamps,
// removals and replacements that rebuild it, with positions off the map and
// on bucket edges, and squares that leave the map.
func TestAppendInstancesNearMatchesTheFullList(t *testing.T) {
	terrain := newEmptyTerrain(70, 50)
	tree := featureDef("tree", 0, 0, 10)
	tree.Filename = "trees"
	big := defP1("big", 3, 2, "big.3do", "")
	defs := []*content.FeatureDef{tree, big}
	terrain.FeatureDefs = append(terrain.FeatureDefs, defs...)
	sim := rng.SimulationFromState(3)
	s := NewService(terrain, &sim, nil, nil)
	seed := uint32(77)
	next := func(n uint32) uint32 {
		seed = seed*1664525 + 1013904223
		return (seed >> 8) % n
	}
	coord := func(cells uint32) numeric.Fixed {
		switch next(6) {
		case 0: // a bucket edge
			return numeric.Fixed(int64(next(cells/16+2)) << 24)
		case 1: // off the map
			return numeric.Fixed(-int64(next(1<<26)) - 1)
		default:
			return numeric.Fixed(int64(next(cells*16+64)) << 16)
		}
	}
	queries, hits := 0, 0
	for step := 0; step < 3000; step++ {
		cx, cz := int(next(70)), int(next(50))
		switch op := next(10); {
		case op < 4:
			pos := [3]numeric.Fixed{coord(70), 0, coord(50)}
			s.stampFeature(cx, cz, defs[next(2)], &pos, nil, neutralFeaturePlacer)
		case op < 6:
			s.spawnFeatureAt(cx, cz, defs[next(2)])
		case op == 6:
			s.RemoveFeatureAt(cx, cz, CauseDead)
		default:
			x, z := coord(70), coord(50)
			radius := int32(next(600)) - 20
			got := s.AppendInstancesNear(nil, x, z, radius)
			var want []*Instance
			if radius >= 0 {
				r := int64(radius) << 16
				for _, inst := range s.Instances() {
					dx, dz := int64(int32(inst.X.Raw()))-int64(int32(x.Raw())), int64(int32(inst.Z.Raw()))-int64(int32(z.Raw()))
					if dx >= -r && dx <= r && dz >= -r && dz <= r {
						want = append(want, inst)
					}
				}
			}
			if !slices.Equal(got, want) {
				t.Fatalf("step %d: query (%d,%d) radius %d returned %d instances, the full list holds %d", step, x.Raw(), z.Raw(), radius, len(got), len(want))
			}
			queries++
			if len(want) > 0 {
				hits++
			}
		}
	}
	if queries < 500 || hits < 200 || len(s.Instances()) < 100 {
		t.Fatalf("%d queries (%d non-empty) over %d instances exercised too little", queries, hits, len(s.Instances()))
	}
}
