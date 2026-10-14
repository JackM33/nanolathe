package movement

import "github.com/nanolathe-gg/nanolathe/internal/units"

const (
	airPointCellShift  = 8 // 256 world units; a lookup choice, not a policy radius
	airPointMaxCells   = 64
	airPointMinThreats = 32
	// With both points in this domain, the sum of squared deltas is at most
	// 2^59. Outside it we retain the original predicate's int64 arithmetic.
	airPointSafe = int64(1 << 28)
)

type airPointCell struct{ x, z int64 }
type airPointBucket struct{ start, end int }

// airPointIndex is derived from one owner's remembered threat picture. It
// conservatively indexes circle bounds, never live unit positions. Maps are
// queried, not iterated; each bucket and the overflow list retain picture order.
// A single entry arena bounds retained storage by the largest indexed picture,
// rather than retaining a separate high-water capacity for every bucket.
type airPointIndex struct {
	cells   map[airPointCell]int
	buckets []airPointBucket
	entries []int
	global  []int
	ready   bool
}

func airPointSafeCoord(v int64) bool { return v >= -airPointSafe && v <= airPointSafe }

// bounds rejects exceptional arithmetic and circles whose indexing cost would
// exceed the per-threat bound. Rejected threats are always tested globally.
func airPointBounds(t *airThreat) (x0, x1, z0, z1 int64, ok bool) {
	if !airPointSafeCoord(t.x) || !airPointSafeCoord(t.z) || t.r < 0 || t.r > airPointSafe {
		return
	}
	x0, x1 = (t.x-t.r)>>airPointCellShift, (t.x+t.r)>>airPointCellShift
	z0, z1 = (t.z-t.r)>>airPointCellShift, (t.z+t.r)>>airPointCellShift
	ok = (x1-x0+1)*(z1-z0+1) <= airPointMaxCells
	return
}

func (p *airPointIndex) rebuild(ts []airThreat) {
	clear(p.cells)
	p.buckets = p.buckets[:0]
	p.global = p.global[:0]
	p.entries = p.entries[:0]
	p.ready = false
	// A short linear scan avoids hash lookup and construction overhead for
	// sparse pictures. This changes no query result or planner cadence.
	if len(ts) < airPointMinThreats {
		return
	}
	if p.cells == nil {
		p.cells = make(map[airPointCell]int)
	}
	for i := range ts {
		x0, x1, z0, z1, ok := airPointBounds(&ts[i])
		if !ok {
			p.global = append(p.global, i)
			continue
		}
		for z := z0; z <= z1; z++ {
			for x := x0; x <= x1; x++ {
				key := airPointCell{x, z}
				b, exists := p.cells[key]
				if !exists {
					b = len(p.buckets)
					p.cells[key] = b
					p.buckets = append(p.buckets, airPointBucket{})
				}
				p.buckets[b].end++
			}
		}
	}
	if len(p.global) == len(ts) {
		return
	}
	total := 0
	for i := range p.buckets {
		n := p.buckets[i].end
		p.buckets[i] = airPointBucket{start: total, end: total}
		total += n
	}
	if cap(p.entries) < total {
		p.entries = make([]int, total)
	} else {
		p.entries = p.entries[:total]
	}
	// Fill buckets in source order, independently of map hashing and capacity.
	for i := range ts {
		x0, x1, z0, z1, ok := airPointBounds(&ts[i])
		if !ok {
			continue
		}
		for z := z0; z <= z1; z++ {
			for x := x0; x <= x1; x++ {
				b := &p.buckets[p.cells[airPointCell{x, z}]]
				p.entries[b.end] = i
				b.end++
			}
		}
	}
	p.ready = true
}

func (p *airPointIndex) rate(x, z int64, ts []airThreat) int64 {
	if p == nil || !p.ready || !airPointSafeCoord(x) || !airPointSafeCoord(z) {
		return airPointRate(x, z, ts)
	}
	var local []int
	if b, ok := p.cells[airPointCell{x >> airPointCellShift, z >> airPointCellShift}]; ok {
		v := p.buckets[b]
		local = p.entries[v.start:v.end]
	}
	if len(local)+len(p.global) == len(ts) {
		return airPointRate(x, z, ts)
	}
	var sum int64
	for a, b := 0, 0; a < len(local) || b < len(p.global); {
		var i int
		if b == len(p.global) || (a < len(local) && local[a] < p.global[b]) {
			i = local[a]
			a++
		} else {
			i = p.global[b]
			b++
		}
		t := &ts[i]
		dx, dz := t.x-x, t.z-z
		if dx*dx+dz*dz <= t.r*t.r {
			sum += t.w
		}
	}
	return sum
}

func (s *System) airIndexFor(u *units.Unit, ts []airThreat) *airPointIndex {
	if u == nil || int(u.Owner) >= len(s.airThreats) {
		return nil
	}
	p := &s.airThreats[u.Owner]
	if !p.pointIndex.ready || len(ts) != len(p.list) || (len(ts) > 0 && &ts[0] != &p.list[0]) {
		return nil
	}
	return &p.pointIndex
}

func airCoverExitIndexed(tx, tz int64, b uint16, ts []airThreat, limit int64, p *airPointIndex) int64 {
	if p == nil || !p.ready {
		return airCoverExit(tx, tz, b, ts, limit)
	}
	for d := int64(0); d <= limit; d += 32 {
		x, z := airAlong(tx, tz, b, d)
		if p.rate(x, z, ts) == 0 {
			return d
		}
	}
	return limit
}
