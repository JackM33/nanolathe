package features

import (
	"slices"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The position index behind AppendInstancesNear: a query for the instances
// near a point, for a scan that would otherwise read every instance on the map
// to keep the few within reach (the Modern patrol's reclaim pick). Derived
// bookkeeping, never simulation state; it changes no answer, only how many
// records a caller reads to find it.
//
// Instances are bucketed by their world position on a 256-unit grid over the
// map, positions off the map in a separate list. A record's position is set
// before it enters the instance set and never written after, so the buckets
// stay true until the set changes; any change to the set renews gridRevision,
// and the index is rebuilt on the next query after one.

// nearBucketShift turns a 16.16 world coordinate into its bucket: 256 world
// units per bucket.
const nearBucketShift = 24

type nearIndex struct {
	built      bool
	revision   uint64
	terrain    *world.Terrain
	cellW      int32
	cols, rows int64
	start      []int32 // bucket b holds items[start[b]:start[b+1]], row-major
	fill       []int32
	items      []nearItem
	outside    []nearItem
	scratch    []*Instance
	found      []nearItem
}

// nearItem is one indexed instance with its rank in AppendInstances' order,
// so a query can hand its matches back in that order.
type nearItem struct {
	inst *Instance
	rank int32
}

// nearWord is the coordinate word a position test compares: the low 32 bits
// of the 16.16 position, sign-extended. The Modern patrol's distance test
// reads the same word (orders.modernDelta).
func nearWord(v numeric.Fixed) int64 { return int64(int32(v.Raw())) }

func (s *Service) nearIndexCurrent() bool {
	ix := &s.near
	return ix.built && ix.revision == s.gridRevision && ix.terrain == s.Terrain && (s.Terrain == nil || ix.cellW == s.Terrain.CellW)
}

func (s *Service) rebuildNearIndex() {
	ix := &s.near
	ix.built, ix.revision, ix.terrain = true, s.gridRevision, s.Terrain
	ix.cols, ix.rows, ix.cellW = 0, 0, 0
	if t := s.Terrain; t != nil && t.CellW > 0 && t.CellH > 0 {
		ix.cellW = t.CellW
		// A cell is 16 world units, so a 256-unit bucket spans 16 cells.
		ix.cols, ix.rows = (int64(t.CellW)+15)/16, (int64(t.CellH)+15)/16
	}
	ix.scratch = s.AppendInstances(ix.scratch[:0])
	n := ix.cols * ix.rows
	ix.start = slices.Grow(ix.start[:0], int(n)+1)[:n+1]
	clear(ix.start)
	ix.outside = ix.outside[:0]
	bucket := func(inst *Instance) int64 {
		bx, bz := nearWord(inst.X)>>nearBucketShift, nearWord(inst.Z)>>nearBucketShift
		if bx < 0 || bz < 0 || bx >= ix.cols || bz >= ix.rows {
			return -1
		}
		return bz*ix.cols + bx
	}
	for rank, inst := range ix.scratch {
		if b := bucket(inst); b >= 0 {
			ix.start[b+1]++
		} else {
			ix.outside = append(ix.outside, nearItem{inst, int32(rank)})
		}
	}
	for b := int64(1); b <= n; b++ {
		ix.start[b] += ix.start[b-1]
	}
	ix.items = slices.Grow(ix.items[:0], int(ix.start[n]))[:ix.start[n]]
	ix.fill = slices.Grow(ix.fill[:0], int(n))[:n]
	copy(ix.fill, ix.start[:n])
	for rank, inst := range ix.scratch {
		if b := bucket(inst); b >= 0 {
			ix.items[ix.fill[b]] = nearItem{inst, int32(rank)}
			ix.fill[b]++
		}
	}
	clear(ix.scratch)
}

// AppendInstancesNear appends every instance whose position lies within
// radius world units of (x, z) on both axes — the square around the circle a
// distance test of that radius admits — in the order AppendInstances gives
// them. The positions compared are the 32-bit coordinate words, as the
// distance test compares them. A negative radius admits nothing.
func (s *Service) AppendInstancesNear(dst []*Instance, x, z numeric.Fixed, radius int32) []*Instance {
	if s == nil || radius < 0 {
		return dst
	}
	if !s.nearIndexCurrent() {
		s.rebuildNearIndex()
	}
	ix := &s.near
	r := int64(radius) << 16
	cx, cz := nearWord(x), nearWord(z)
	ix.found = ix.found[:0]
	keep := func(item nearItem) {
		dx, dz := nearWord(item.inst.X)-cx, nearWord(item.inst.Z)-cz
		if dx >= -r && dx <= r && dz >= -r && dz <= r {
			ix.found = append(ix.found, item)
		}
	}
	bx0, bx1 := max((cx-r)>>nearBucketShift, 0), min((cx+r)>>nearBucketShift, ix.cols-1)
	bz0, bz1 := max((cz-r)>>nearBucketShift, 0), min((cz+r)>>nearBucketShift, ix.rows-1)
	for bz := bz0; bz <= bz1; bz++ {
		for bx := bx0; bx <= bx1; bx++ {
			b := bz*ix.cols + bx
			for _, item := range ix.items[ix.start[b]:ix.start[b+1]] {
				keep(item)
			}
		}
	}
	for _, item := range ix.outside {
		keep(item)
	}
	slices.SortFunc(ix.found, func(a, b nearItem) int { return int(a.rank) - int(b.rank) })
	for _, item := range ix.found {
		dst = append(dst, item.inst)
	}
	return dst
}
