package path

import (
	"errors"
	"math/bits"
	"slices"
)

// Nanolathe Modern bucket open list (docs/DESIGN_MOVEMENT_PATH.md "Modern
// bucket open list").
//
// The retail open list is a binary heap whose ties fall out of its sifting
// [04 R-PATH-01 §1]. Under the Modern rule set a search's open list is a
// bucket queue instead: one bucket per F value, each a stack of the entries
// pushed with that key, and a bitmap of the buckets that hold any. A pop
// takes the most recent push of the lowest key. A relaxation does not move
// the node's entry; it pushes the node again at its lower key, and the entry
// it leaves behind is stale — its node is closed, or its key is no longer
// the node's F — and is skipped when it comes to the front, without being
// charged as a pop.
//
// Keys need not rise from pop to pop: under a weighted heuristic a neighbour
// can open below the key just popped, and the lowest occupied bucket is then
// found again from there. The buckets address keys 0 to bucketKeySpan-1; a
// key outside them, which no search of a reserved rule set produces, goes to
// a small binary heap that keeps the same order — lowest key first, the most
// recent push first among equal keys — so the order never depends on where
// an entry is stored.

// OpenList names a search's open-list discipline. The zero value is the
// retail heap.
type OpenList uint8

const (
	// OpenListHeap is the retail binary heap and its tie order
	// [04 R-PATH-01 §1].
	OpenListHeap OpenList = iota
	// OpenListBuckets is the Modern bucket queue: lowest F first, the most
	// recent push first among equal F, a relaxation pushed again rather than
	// repositioned.
	OpenListBuckets
)

// bucketKeySpan is the number of keys the buckets address, from zero. It is
// a storage bound, not a rule: a key beyond it is ordered the same way by the
// overflow heap.
const bucketKeySpan = 1 << 18

// bucketEntry is one push: the node and the index of the push before it in
// the same bucket, or -1 at the bottom of the stack.
type bucketEntry struct {
	id   heapID
	next int32
}

// overflowEntry is a push whose key the buckets do not address. seq is its
// push index, which orders equal keys.
type overflowEntry struct {
	f   int32
	seq int32
	id  heapID
}

// bucketQueue is a Modern search's open list.
type bucketQueue struct {
	// entries holds every push of the search in push order; an entry's index
	// is its push sequence.
	entries []bucketEntry
	// heads is the index in entries of each bucket's most recent push. It is
	// meaningful only for a bucket whose bit is set, so it is never cleared.
	heads []int32
	// occupied has one bit per bucket: bit k of word w is key 64w+k. Words
	// past its length are zero within its capacity.
	occupied []uint64
	// low is a word at or below the lowest occupied one; clear sets it before
	// first use.
	low   int
	over  []overflowEntry
	count int
}

// Len returns the number of entries queued, stale ones included.
func (q *bucketQueue) Len() int { return q.count }

// push queues id at key f.
func (q *bucketQueue) push(id NodeID, f int32) {
	seq := int32(len(q.entries))
	if f < 0 || f >= bucketKeySpan {
		q.entries = append(q.entries, bucketEntry{id: heapID(id), next: -1})
		q.overflowPush(overflowEntry{f: f, seq: seq, id: heapID(id)})
		q.count++
		return
	}
	w := int(f) >> 6
	if w >= len(q.occupied) {
		q.grow(w)
	}
	bit := uint64(1) << (uint(f) & 63)
	next := int32(-1)
	if q.occupied[w]&bit != 0 {
		next = q.heads[f]
	}
	q.entries = append(q.entries, bucketEntry{id: heapID(id), next: next})
	q.heads[f] = seq
	q.occupied[w] |= bit
	q.low = min(q.low, w)
	q.count++
}

// grow extends the bucket words to address word w. Words past the length are
// zero within the capacity (clear keeps them so), and heads needs no zeroing.
func (q *bucketQueue) grow(w int) {
	n := min(max(w+1, 2*len(q.occupied), 64), bucketKeySpan>>6)
	if n <= cap(q.occupied) {
		q.occupied = q.occupied[:n]
	} else {
		q.occupied = append(q.occupied[:cap(q.occupied)], make([]uint64, n-cap(q.occupied))...)
	}
	if keys := n << 6; keys <= cap(q.heads) {
		q.heads = q.heads[:keys]
	} else {
		q.heads = append(q.heads[:cap(q.heads)], make([]int32, keys-cap(q.heads))...)
	}
}

// pop removes and returns the entry the next pop takes: the overflow's when
// its key is below every bucket's, else the most recent push of the lowest
// occupied bucket, else the overflow's.
func (q *bucketQueue) pop() (NodeID, int32, bool) {
	if len(q.over) != 0 && q.over[0].f < 0 {
		return q.popOverflow()
	}
	w := q.low
	for w < len(q.occupied) && q.occupied[w] == 0 {
		w++
	}
	q.low = w
	if w < len(q.occupied) {
		k := bits.TrailingZeros64(q.occupied[w])
		f := int32(w<<6 | k)
		e := q.entries[q.heads[f]]
		if e.next >= 0 {
			q.heads[f] = e.next
		} else {
			q.occupied[w] &^= uint64(1) << uint(k)
		}
		q.count--
		return NodeID(e.id), f, true
	}
	if len(q.over) != 0 {
		return q.popOverflow()
	}
	return invalidNodeID, 0, false
}

func (q *bucketQueue) popOverflow() (NodeID, int32, bool) {
	e := q.over[0]
	q.overflowPop()
	q.count--
	return NodeID(e.id), e.f, true
}

// front reports the entry the next pop takes without removing it.
func (q *bucketQueue) front() (NodeID, int32, bool) {
	if len(q.over) != 0 && q.over[0].f < 0 {
		return NodeID(q.over[0].id), q.over[0].f, true
	}
	w := q.low
	for w < len(q.occupied) && q.occupied[w] == 0 {
		w++
	}
	q.low = w
	if w < len(q.occupied) {
		f := int32(w<<6 | bits.TrailingZeros64(q.occupied[w]))
		return NodeID(q.entries[q.heads[f]].id), f, true
	}
	if len(q.over) != 0 {
		return NodeID(q.over[0].id), q.over[0].f, true
	}
	return invalidNodeID, 0, false
}

// drop removes the entry front reported, which must be the last call.
func (q *bucketQueue) drop(f int32) {
	q.count--
	if f < 0 || f >= bucketKeySpan {
		q.overflowPop()
		return
	}
	w := int(f) >> 6
	if next := q.entries[q.heads[f]].next; next >= 0 {
		q.heads[f] = next
		return
	}
	q.occupied[w] &^= uint64(1) << (uint(f) & 63)
}

// popLive pops entries until it finds one whose node is still open at that
// key, and returns it. Stale entries are discarded on the way.
func (q *bucketQueue) popLive(nodes []Node) (NodeID, int32, bool) {
	for {
		id, f, ok := q.pop()
		if !ok {
			return invalidNodeID, 0, false
		}
		if n := &nodes[id]; !n.Closed && n.Open && n.F == f {
			return id, f, true
		}
	}
}

// hasLive discards stale entries at the front and reports whether a live one
// remains.
func (q *bucketQueue) hasLive(nodes []Node) bool {
	for {
		id, f, ok := q.front()
		if !ok {
			return false
		}
		if n := &nodes[id]; !n.Closed && n.Open && n.F == f {
			return true
		}
		q.drop(f)
	}
}

// clear empties the queue and keeps its storage. A push sets bits only
// within the words' length, so clearing that length leaves every word of the
// capacity zero again.
func (q *bucketQueue) clear() {
	clear(q.occupied)
	q.occupied = q.occupied[:0]
	q.heads = q.heads[:0]
	q.entries = q.entries[:0]
	q.over = q.over[:0]
	q.low, q.count = bucketKeySpan>>6, 0
}

// overflowLess orders overflow entries: lowest key first, the most recent
// push first among equal keys.
func overflowLess(a, b overflowEntry) bool {
	return a.f < b.f || a.f == b.f && a.seq > b.seq
}

func (q *bucketQueue) overflowPush(e overflowEntry) {
	q.over = append(q.over, e)
	i := len(q.over) - 1
	for i > 0 {
		p := (i - 1) / 2
		if !overflowLess(q.over[i], q.over[p]) {
			break
		}
		q.over[i], q.over[p] = q.over[p], q.over[i]
		i = p
	}
}

func (q *bucketQueue) overflowPop() {
	last := len(q.over) - 1
	q.over[0] = q.over[last]
	q.over = q.over[:last]
	i := 0
	for {
		l := 2*i + 1
		if l >= last {
			return
		}
		c := l
		if r := l + 1; r < last && overflowLess(q.over[r], q.over[l]) {
			c = r
		}
		if !overflowLess(q.over[c], q.over[i]) {
			return
		}
		q.over[i], q.over[c] = q.over[c], q.over[i]
		i = c
	}
}

// order lists the queued entries, stale ones included, in the order pops
// would take them, for the canonical checkpoint. It reports storage that
// cannot have been built by pushes and pops.
func (q *bucketQueue) order() ([]overflowEntry, error) {
	over := slices.Clone(q.over)
	for i, e := range over {
		if e.f >= 0 && e.f < bucketKeySpan {
			return nil, errors.New("overflow entry has a bucket key")
		}
		if i > 0 && overflowLess(e, over[(i-1)/2]) {
			return nil, errors.New("overflow heap order differs")
		}
	}
	slices.SortFunc(over, func(a, b overflowEntry) int {
		if overflowLess(a, b) {
			return -1
		}
		if overflowLess(b, a) {
			return 1
		}
		return 0
	})
	out := make([]overflowEntry, 0, q.count)
	for _, e := range over {
		if e.f < 0 {
			out = append(out, e)
		}
	}
	for w, word := range q.occupied {
		if word != 0 && w < q.low {
			return nil, errors.New("occupied bucket below the lowest word")
		}
		for word != 0 {
			f := int32(w<<6 | bits.TrailingZeros64(word))
			word &= word - 1
			for i := q.heads[f]; i >= 0; {
				if int(i) >= len(q.entries) || len(out) > len(q.entries) {
					return nil, errors.New("bucket stack leaves the pushes")
				}
				e := q.entries[i]
				out = append(out, overflowEntry{f: f, seq: i, id: e.id})
				if e.next >= i {
					return nil, errors.New("bucket stack does not descend")
				}
				i = e.next
			}
		}
	}
	for _, e := range over {
		if e.f >= bucketKeySpan {
			out = append(out, e)
		}
	}
	if len(out) != q.count {
		return nil, errors.New("bucket count differs")
	}
	return out, nil
}
