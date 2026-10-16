package path

import (
	"slices"
	"testing"
)

// Nanolathe Modern bucket open list (docs/DESIGN_MOVEMENT_PATH.md "Modern
// bucket open list").

// bucketModel is the order the bucket queue keeps, written as a sort: lowest
// key first, the most recent push first among equal keys.
type bucketModel struct {
	entries []overflowEntry
	seq     int32
}

func (m *bucketModel) push(id NodeID, f int32) {
	m.entries = append(m.entries, overflowEntry{f: f, seq: m.seq, id: heapID(id)})
	m.seq++
}

func (m *bucketModel) pop() (NodeID, int32, bool) {
	if len(m.entries) == 0 {
		return invalidNodeID, 0, false
	}
	best := 0
	for i := range m.entries {
		if overflowLess(m.entries[i], m.entries[best]) {
			best = i
		}
	}
	e := m.entries[best]
	m.entries = slices.Delete(m.entries, best, best+1)
	return NodeID(e.id), e.f, true
}

// The queue pops in the model's order through pushes below the current
// minimum, equal keys, keys the buckets do not address and reuse after
// clearing, and leaves every bucket word clear when emptied.
func TestBucketQueueOrder(t *testing.T) {
	var q bucketQueue
	q.clear()
	state := uint32(12345)
	next := func(n uint32) uint32 {
		state = state*1664525 + 1013904223
		return (state >> 8) % n
	}
	for round := 0; round < 3; round++ {
		var m bucketModel
		base := int32(next(5000))
		for step := 0; step < 4000; step++ {
			if next(3) != 0 || len(m.entries) == 0 {
				var f int32
				switch next(20) {
				case 0:
					f = -int32(next(50)) - 1 // below every bucket
				case 1:
					f = bucketKeySpan + int32(next(50)) // above every bucket
				default:
					// Around a drifting base, so pushes land below the
					// current minimum as often as above it.
					f = base + int32(next(400)) - 200
					if f < 0 {
						f = -f
					}
				}
				id := NodeID(next(1000) + 1)
				q.push(id, f)
				m.push(id, f)
				continue
			}
			id, f, ok := q.front()
			wantID, wantF, wantOK := m.pop()
			if ok != wantOK || id != wantID || f != wantF {
				t.Fatalf("round %d step %d: front = (%d, %d, %v), want (%d, %d, %v)", round, step, id, f, ok, wantID, wantF, wantOK)
			}
			q.drop(f)
			base += int32(next(7)) - 3
		}
		if q.Len() != len(m.entries) {
			t.Fatalf("round %d: %d queued, model %d", round, q.Len(), len(m.entries))
		}
		q.clear()
		for i, w := range q.occupied[:cap(q.occupied)] {
			if w != 0 {
				t.Fatalf("round %d: word %d left set after clear", round, i)
			}
		}
	}
}

// A relaxation pushes the node again and the old entry is skipped without
// being returned: popLive returns each open node once, at its current key.
func TestBucketQueueSkipsStaleEntries(t *testing.T) {
	var q bucketQueue
	q.clear()
	nodes := []Node{{}, {F: 50, Open: true}, {F: 40, Open: true}, {F: 45, Open: true}}
	q.push(1, 50)
	q.push(2, 40)
	q.push(3, 45)
	nodes[1].F = 30 // relaxed below the current minimum
	q.push(1, 30)
	nodes[3].Closed, nodes[3].Open = true, false
	var got []NodeID
	for {
		id, f, ok := q.popLive(nodes)
		if !ok {
			break
		}
		if f != nodes[id].F {
			t.Fatalf("node %d popped at %d, its F is %d", id, f, nodes[id].F)
		}
		got = append(got, id)
	}
	if want := []NodeID{1, 2}; !slices.Equal(got, want) {
		t.Fatalf("popped %v, want %v", got, want)
	}
	q.push(2, 40)
	nodes[2].Closed = true
	if q.hasLive(nodes) || q.Len() != 0 {
		t.Fatalf("a stale entry counts as a candidate: %d queued", q.Len())
	}
}

// bucketSearch runs one request over the batch maze to its end.
func bucketSearch(t *testing.T, g *batchGrid, ws *Workspace, list OpenList, start, goal Cell) ([]Point, Status, int) {
	t.Helper()
	s := NewSession(SearchConfig{Start: start, Goal: PointGoal(goal, 0), Scale: 98304, HasBounds: true,
		Bounds: Rect{Max: Cell{X: g.w - 1, Z: g.h - 1}}, PassableValue: g.passable, Workspace: ws, OpenList: list})
	defer s.Release()
	points, status, done := s.Resume(1 << 30)
	if !done {
		t.Fatal("an unbudgeted search did not finish")
	}
	return append([]Point(nil), points...), status, s.Popped()
}

// A bucket search reaches what the retail heap reaches, a walled goal
// included — both settle on a cell the setup ray made acceptable — and the
// storage it hands back leaves the next search on the same table exactly as
// it would be on a fresh one.
func TestBucketSearchReachesLikeTheHeap(t *testing.T) {
	g := &batchGrid{w: 64, h: 48}
	for _, goal := range []Cell{{X: 60, Z: 45}, {X: 33, Z: 20}} {
		_, heapStatus, _ := bucketSearch(t, g, nil, OpenListHeap, Cell{X: 1, Z: 1}, goal)
		points, status, _ := bucketSearch(t, g, nil, OpenListBuckets, Cell{X: 1, Z: 1}, goal)
		if status != heapStatus || len(points) == 0 {
			t.Fatalf("goal %v: bucket search status %d with %d points, heap status %d", goal, status, len(points), heapStatus)
		}
	}
	walled := &walledGrid{batchGrid: batchGrid{w: 64, h: 48}, around: Cell{X: 40, Z: 30}}
	heapPoints, heapStatus, _ := bucketSearchOn(t, walled, OpenListHeap)
	points, status, _ := bucketSearchOn(t, walled, OpenListBuckets)
	if len(points) == 0 || len(heapPoints) == 0 || status != heapStatus {
		t.Fatalf("walled goal: buckets %d points status %d, heap %d points status %d", len(points), status, len(heapPoints), heapStatus)
	}
	ws := &Workspace{}
	bucketSearch(t, g, ws, OpenListBuckets, Cell{X: 62, Z: 1}, Cell{X: 2, Z: 40})
	reused, _, reusedPops := bucketSearch(t, g, ws, OpenListBuckets, Cell{X: 1, Z: 1}, Cell{X: 60, Z: 45})
	fresh, _, freshPops := bucketSearch(t, g, nil, OpenListBuckets, Cell{X: 1, Z: 1}, Cell{X: 60, Z: 45})
	if !slices.Equal(reused, fresh) || reusedPops != freshPops {
		t.Fatal("a bucket search on a reused table differs from one on a fresh table")
	}
}

// walledGrid is the batch maze with a ring of wall around one cell.
type walledGrid struct {
	batchGrid
	around Cell
}

func (g *walledGrid) passable(c Cell) uint8 {
	if dx, dz := c.X-g.around.X, c.Z-g.around.Z; max(dx, -dx) == 2 && max(dz, -dz) <= 2 || max(dz, -dz) == 2 && max(dx, -dx) <= 2 {
		return 0
	}
	return g.batchGrid.passable(c)
}

func bucketSearchOn(t *testing.T, g *walledGrid, list OpenList) ([]Point, Status, int) {
	t.Helper()
	s := NewSession(SearchConfig{Start: Cell{X: 1, Z: 1}, Goal: PointGoal(g.around, 0), Scale: 98304, HasBounds: true,
		Bounds: Rect{Max: Cell{X: g.w - 1, Z: g.h - 1}}, PassableValue: g.passable, OpenList: list})
	points, status, done := s.Resume(1 << 30)
	if !done {
		t.Fatal("an unbudgeted search did not finish")
	}
	return points, status, s.Popped()
}
