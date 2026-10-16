package path

import (
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// The helpers of Nanolathe Modern parallel path batches
// (docs/DESIGN_MOVEMENT_PATH.md "Modern parallel path batches").
//
// A round is a list of searches, each with the heap pops it may take, fixed
// by the simulation goroutine before the round starts. The simulation
// goroutine and up to maxBatchHelpers helper goroutines take the searches one
// at a time in list order until none is left, and the simulation goroutine
// waits until every helper it handed the round to has finished with it. Each
// search resumes on its own storage against inputs nothing writes until the
// round has joined, so what a search computes, and what the round reports,
// is the same whichever goroutine resumed it and however many took part. A
// host with one processor — the browser's, or a process limited to one by
// GOMAXPROCS — runs every search on the simulation goroutine, in list order,
// with the same results.

// maxBatchHelpers bounds the helper goroutines a process starts. It is a cost
// bound, not a rule: a round has at most the batch's searches, and every
// search beyond the helpers runs on a goroutine that has finished one.
const maxBatchHelpers = 15

// batchItem is one search of a round and what its resume reported.
type batchItem struct {
	unit   pool.Handle
	search Search
	budget int
	before int
	points []Point
	status Status
	done   bool
	pops   int
}

// batchRound is one round's work. It is the scheduler's scratch, reused from
// round to round.
type batchRound struct {
	units []pool.Handle
	items []batchItem
	// What the round has handed out so far: of the call's total, and per
	// player of its parked work.
	pool   int32
	parked [10]int32
	next   atomic.Int64
	wg     sync.WaitGroup
	// fault holds the first panic a goroutine recovered while resuming a
	// search of this round; the simulation goroutine raises it again after
	// the join, where a panic on the simulation goroutine would have been.
	faultMu sync.Mutex
	fault   any
	faulted bool
}

func (r *batchRound) reset() {
	r.units = r.units[:0]
	r.items = r.items[:0]
	r.pool, r.parked = 0, [10]int32{}
}

// add appends a search to the round. Its work so far is read now, on the
// simulation goroutine, so the round reports only what it adds.
func (r *batchRound) add(unit pool.Handle, search Search, budget int) {
	r.items = append(r.items, batchItem{unit: unit, search: search, budget: budget, before: search.Popped()})
}

// release drops the round's references to searches and routes, which the
// scheduler has finished with.
func (r *batchRound) release() {
	clear(r.items)
	r.items = r.items[:0]
	r.units = r.units[:0]
}

// work takes the round's searches one at a time until none is left.
func (r *batchRound) work() {
	defer func() {
		if p := recover(); p != nil {
			r.faultMu.Lock()
			if !r.faulted {
				r.fault, r.faulted = p, true
			}
			r.faultMu.Unlock()
		}
	}()
	for {
		i := int(r.next.Add(1) - 1)
		if i >= len(r.items) {
			return
		}
		it := &r.items[i]
		it.points, it.status, it.done = it.search.Resume(it.budget)
		it.pops = it.search.Popped() - it.before
	}
}

// help is a helper goroutine's part of a round.
func (r *batchRound) help() {
	defer r.wg.Done()
	r.work()
}

// run expands every search of the round and returns when all are done.
func (r *batchRound) run() {
	n := len(r.items)
	if n == 0 {
		return
	}
	r.next.Store(0)
	r.fault, r.faulted = nil, false
	if n > 1 && runtime.GOMAXPROCS(0) > 1 {
		if jobs := batchHelpers(); jobs != nil {
			// Hand the round to helpers that are idle now. A helper that is
			// busy — with another session's round — is not waited for: the
			// goroutines that do take part share the searches out between
			// them.
		handout:
			for h := 1; h < n; h++ {
				r.wg.Add(1)
				select {
				case jobs <- r:
				default:
					r.wg.Done()
					break handout
				}
			}
		}
	}
	r.work()
	r.wg.Wait()
	if r.faulted {
		panic(r.fault)
	}
}

var batchPool struct {
	mu   sync.Mutex
	jobs chan *batchRound
}

// batchHelpers starts the helper goroutines at their first use on a host with
// more than one processor and returns the channel that hands them rounds.
func batchHelpers() chan *batchRound {
	batchPool.mu.Lock()
	defer batchPool.mu.Unlock()
	if batchPool.jobs == nil {
		n := min(runtime.GOMAXPROCS(0)-1, maxBatchHelpers)
		if n <= 0 {
			return nil
		}
		batchPool.jobs = make(chan *batchRound)
		for range n {
			go batchHelper(batchPool.jobs)
		}
	}
	return batchPool.jobs
}

// batchHelper is one helper goroutine: it takes part in every round it is
// handed and waits for the next.
func batchHelper(jobs <-chan *batchRound) {
	for r := range jobs {
		r.help()
	}
}
