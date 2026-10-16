package path

import (
	"reflect"
	"runtime"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// Nanolathe Modern parallel path batches (docs/DESIGN_MOVEMENT_PATH.md
// "Modern parallel path batches").

// batchGrid is an authored maze: a lattice of walls with staggered gaps, so
// that searches across it take many pops and differ in length.
type batchGrid struct{ w, h int32 }

func (g batchGrid) passable(c Cell) uint8 {
	if c.X < 0 || c.Z < 0 || c.X >= g.w || c.Z >= g.h {
		return 0
	}
	// A wall on every fourth column, open on one row in eleven, offset by
	// the column so the gaps stagger.
	if c.X%4 == 2 && (c.Z+c.X)%11 != 0 {
		return 0
	}
	return 3
}

// batchProviderFixture serves requests queued per player and opens real
// searches over the maze. It implements both the retail SearchFunc and the
// batch boundary, so one workload can run under either scheduler.
type batchProviderFixture struct {
	grid     batchGrid
	queue    [10][]Request
	eligible [10]bool
	searches int
	pops     int32
	sessions map[pool.Handle]*Session // looked up by unit, never ranged
	created  int
	opens    []pool.Handle
	settles  []pool.Handle
}

func newBatchProviderFixture(searches int, pops int32) *batchProviderFixture {
	p := &batchProviderFixture{grid: batchGrid{w: 64, h: 48}, searches: searches, pops: pops, sessions: map[pool.Handle]*Session{}}
	p.eligible[0], p.eligible[1] = true, true
	return p
}

func (p *batchProviderFixture) PlayerCount() int         { return 2 }
func (p *batchProviderFixture) UnitLimit() int32         { return 50 }
func (p *batchProviderFixture) Eligible(player int) bool { return p.eligible[player] }
func (p *batchProviderFixture) Poll(player int) (Request, PollResult) {
	if q := p.queue[player]; len(q) > 0 {
		p.queue[player] = q[1:]
		return q[0], PollRequest
	}
	return Request{}, PollVisited
}
func (p *batchProviderFixture) PathBatch() (int, int32) { return p.searches, p.pops }

func (p *batchProviderFixture) open(r Request) *Session {
	if s := p.sessions[r.Unit]; s != nil {
		return s
	}
	s := NewSession(SearchConfig{Start: r.Start, Goal: r.Goal, Scale: 65536, HasBounds: true,
		Bounds: Rect{Max: Cell{X: p.grid.w - 1, Z: p.grid.h - 1}}, PassableValue: p.grid.passable})
	p.sessions[r.Unit] = s
	p.created++
	return s
}

// search is the retail boundary over the same sessions.
func (p *batchProviderFixture) search(r Request, _ int32, budget int) WorkResult {
	_, existed := p.sessions[r.Unit]
	s := p.open(r)
	before := s.Popped()
	points, status, done := s.Resume(budget)
	setup := 0
	if !existed {
		setup = s.SetupSteps()
	}
	if done {
		delete(p.sessions, r.Unit)
	}
	return WorkResult{Points: points, Status: status, Done: done, SetupSteps: setup, Pops: s.Popped() - before}
}

func (p *batchProviderFixture) OpenBatch(r Request, _ int32, _ int) (Search, WorkResult) {
	_, open := p.sessions[r.Unit]
	s := p.open(r)
	if !open {
		p.opens = append(p.opens, r.Unit)
	}
	work := WorkResult{}
	if !open {
		work.SetupSteps = s.SetupSteps()
	}
	if s.IsDone() {
		work.Points, work.Status, work.Done = s.resultPoints, s.resultStatus, true
		delete(p.sessions, r.Unit)
		return nil, work
	}
	return s, work
}

func (p *batchProviderFixture) SettleBatch(r Request, _ int32, _ int, slice BatchSlice) WorkResult {
	p.settles = append(p.settles, r.Unit)
	if slice.Done {
		delete(p.sessions, r.Unit)
	}
	return WorkResult{Points: slice.Points, Status: slice.Status, Done: slice.Done, Pops: slice.Pops}
}

// retailOnly hides the batch boundary, as a Strict 3.1 provider does.
type retailOnly struct{ *batchProviderFixture }

func (retailOnly) PathBatch() {}

type batchPublication struct {
	Tick   uint32
	Unit   pool.Handle
	Points []Point
	Status Status
}

// batchWorkload queues requests that cross the maze from both sides, so the
// two players' searches differ in length, and runs ticks calls.
func batchWorkload(t *testing.T, p *batchProviderFixture, provider CandidateProvider, allowance int, ticks uint32) (*Scheduler, []batchPublication) {
	t.Helper()
	var published []batchPublication
	var tick uint32
	s := NewScheduler(p.search, func(r Request, points []Point, status Status) {
		published = append(published, batchPublication{Tick: tick, Unit: r.Unit, Points: append([]Point(nil), points...), Status: status})
	})
	s.SetCandidateProvider(provider)
	s.SetStepAllowance(allowance)
	for i := 0; i < 12; i++ {
		player := i & 1
		start := Cell{X: int32(1 + i), Z: int32(2 + 3*i)}
		goal := PointGoal(Cell{X: int32(60 - 2*i), Z: int32(45 - 3*i)}, 0)
		if player == 1 {
			start, goal = Cell{X: int32(62 - i), Z: int32(1 + 3*i)}, PointGoal(Cell{X: int32(3 + i), Z: int32(40 - 2*i)}, 0)
		}
		p.queue[player] = append(p.queue[player], Request{Unit: pool.Handle(10 + i), Player: uint8(player), Start: start, Goal: goal, Activation: uint64(i + 1)})
	}
	for tick = 1; tick <= ticks; tick++ {
		s.Tick(tick)
	}
	return s, published
}

type batchState struct {
	Accumulator, ServiceCount, Scales [10]int32
	PlayerCursor                      int
	Latched                           []latchedSearch
	Active                            bool
	Trace                             SchedulerTraceState
}

func batchStateOf(s *Scheduler) batchState {
	st := batchState{Accumulator: s.accumulator, ServiceCount: s.serviceCount, Scales: s.scales, PlayerCursor: s.playerCursor,
		Latched: append([]latchedSearch(nil), s.latched...), Active: s.active != nil, Trace: s.TraceState()}
	for i := range st.Latched {
		st.Latched[i].req.Goal = nil
	}
	for i := range st.Trace.Requests {
		st.Trace.Requests[i].Goal = GoalTrace{}
	}
	return st
}

// A provider that answers zero, or does not ask, keeps the retail scheduler:
// the batch boundary is never entered and every publication and charge is
// the retail one [04 R-PATH-01 §6].
func TestPathBatchZeroAnswerKeepsRetailScheduler(t *testing.T) {
	zero := newBatchProviderFixture(0, 0)
	s, zeroPublished := batchWorkload(t, zero, zero, 1333, 120)
	retail := newBatchProviderFixture(0, 0)
	r, retailPublished := batchWorkload(t, retail, retailOnly{retail}, 1333, 120)
	if len(zero.opens) != 0 || len(zero.settles) != 0 {
		t.Fatalf("a zero answer entered the batch boundary: %d opens, %d settles", len(zero.opens), len(zero.settles))
	}
	if len(retailPublished) < 8 {
		t.Fatalf("only %d publications; the comparison proves little", len(retailPublished))
	}
	if !reflect.DeepEqual(zeroPublished, retailPublished) || !reflect.DeepEqual(batchStateOf(s), batchStateOf(r)) {
		t.Fatal("a provider answering zero differs from one without the batch question")
	}
}

// One latched search with no per-search cap is the retail scheduler: the
// same admissions, publications, charges and poll accounting, so the batch
// changes the schedule only through its size and cap.
func TestPathBatchOfOneUncappedMatchesRetail(t *testing.T) {
	retail := newBatchProviderFixture(0, 0)
	r, retailPublished := batchWorkload(t, retail, retailOnly{retail}, 1333, 160)
	one := newBatchProviderFixture(1, 1<<30)
	b, onePublished := batchWorkload(t, one, one, 1333, 160)
	if !reflect.DeepEqual(onePublished, retailPublished) {
		t.Fatalf("publications differ:\nbatch  %d %+v\nretail %d %+v", len(onePublished), onePublished, len(retailPublished), retailPublished)
	}
	if got, want := batchStateOf(b), batchStateOf(r); !reflect.DeepEqual(got.Accumulator, want.Accumulator) || got.ServiceCount != want.ServiceCount || got.PlayerCursor != want.PlayerCursor || !reflect.DeepEqual(got.Trace, want.Trace) {
		t.Fatalf("scheduler state differs:\nbatch  %+v\nretail %+v", got, want)
	}
}

// The batch's results do not depend on how many goroutines expand a round:
// the same workload on one processor and on several publishes the same routes
// on the same ticks and leaves the same scheduler state.
func TestPathBatchSameResultsOnOneAndManyProcessors(t *testing.T) {
	run := func(procs int) ([]batchPublication, batchState) {
		defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(procs))
		p := newBatchProviderFixture(4, 300)
		s, published := batchWorkload(t, p, p, 66650, 60)
		return published, batchStateOf(s)
	}
	serial, serialState := run(1)
	parallel, parallelState := run(max(runtime.NumCPU(), 4))
	if len(serial) < 8 {
		t.Fatalf("only %d publications; the comparison proves little", len(serial))
	}
	if !reflect.DeepEqual(serial, parallel) || !reflect.DeepEqual(serialState, parallelState) {
		t.Fatal("a batch's results depend on the processors that ran it")
	}
}

// Admission opens requests in poll order, a round settles its searches in
// admission order and publishes the finished ones in that order, and a search
// that reaches the call's per-search pops stays latched, is charged exactly
// what it expanded, and continues on the next call.
func TestPathBatchAdmissionSettleAndPublicationOrder(t *testing.T) {
	p := newBatchProviderFixture(3, 120)
	var published []pool.Handle
	s := NewScheduler(p.search, func(r Request, _ []Point, _ Status) { published = append(published, r.Unit) })
	s.SetCandidateProvider(p)
	s.SetStepAllowance(66650)
	long := PointGoal(Cell{X: 60, Z: 45}, 0)
	p.queue[0] = []Request{{Unit: 1, Start: Cell{X: 1, Z: 1}, Goal: long}, {Unit: 3, Start: Cell{X: 5, Z: 5}, Goal: PointGoal(Cell{X: 6, Z: 6}, 0)}}
	p.queue[1] = []Request{{Unit: 2, Player: 1, Start: Cell{X: 1, Z: 30}, Goal: PointGoal(Cell{X: 3, Z: 30}, 0)}}
	s.Tick(1)
	// Players alternate in poll order; the batch has three places.
	if want := []pool.Handle{1, 2, 3}; !reflect.DeepEqual(p.opens[:3], want) {
		t.Fatalf("admission order %v, want %v", p.opens, want)
	}
	if want := []pool.Handle{1, 2, 3}; !reflect.DeepEqual(p.settles[:3], want) {
		t.Fatalf("first round settled %v, want admission order %v", p.settles[:3], want)
	}
	// The two short searches finish in the first round, in admission order;
	// the long one is cut at the call's per-search pops and stays latched.
	if want := []pool.Handle{2, 3}; !reflect.DeepEqual(published, want) {
		t.Fatalf("published %v, want %v", published, want)
	}
	if len(s.latched) != 1 || s.latched[0].req.Unit != 1 || s.latched[0].spent != 120 {
		t.Fatalf("latched %+v, want unit 1 cut at 120 pops", s.latched)
	}
	if got := p.sessions[1].Popped(); got != 120 {
		t.Fatalf("the long search expanded %d pops in its first call, want the cap", got)
	}
	if !s.HasRequest(1) || s.CurrentRequest(1) == nil || len(s.TraceState().Requests) != 1 {
		t.Fatal("a latched search is not reported as the unit's request")
	}
	for tick := uint32(2); len(s.latched) != 0; tick++ {
		if tick > 200 {
			t.Fatal("the long search never finished")
		}
		s.Tick(tick)
	}
	if published[len(published)-1] != 1 {
		t.Fatalf("published %v, want the long search last", published)
	}
}

// A cancelled latched search is dropped without publication, and a request
// latched by the retail scheduler before a switch to a batching one carries
// over without being set up again; after a switch back the retail scheduler
// serves what the batch had latched, in admission order, before it polls.
func TestPathBatchCancelAndSwitches(t *testing.T) {
	p := newBatchProviderFixture(3, 50)
	var published []pool.Handle
	s := NewScheduler(p.search, func(r Request, _ []Point, _ Status) { published = append(published, r.Unit) })
	s.SetCandidateProvider(p)
	s.SetStepAllowance(66650)
	far := func(x int32) Goal { return PointGoal(Cell{X: 60, Z: 40 + x}, 0) }
	p.queue[0] = []Request{{Unit: 1, Start: Cell{X: 1, Z: 1}, Goal: far(0)}, {Unit: 3, Start: Cell{X: 1, Z: 9}, Goal: far(2)}}
	p.queue[1] = []Request{{Unit: 2, Player: 1, Start: Cell{X: 1, Z: 5}, Goal: far(1)}}
	s.Tick(1)
	if len(s.latched) != 3 {
		t.Fatalf("latched %d searches, want 3", len(s.latched))
	}
	if !s.Cancel(2) || s.HasRequest(2) || len(s.latched) != 2 {
		t.Fatal("cancel left the latched search")
	}
	// Switch to the retail scheduler: unit 1, then unit 3, without polling.
	p.searches = 0
	created := p.created
	for tick := uint32(2); len(s.latched) != 0 || s.active != nil; tick++ {
		if tick > 300 {
			t.Fatal("the retail scheduler never finished the batch's searches")
		}
		s.Tick(tick)
	}
	if p.created != created {
		t.Fatal("the retail scheduler set a carried-over search up again")
	}
	if want := []pool.Handle{1, 3}; !reflect.DeepEqual(published, want) {
		t.Fatalf("published %v, want %v", published, want)
	}
	// Back to a batch: the retail scheduler's active search becomes the
	// first latched one.
	p.queue[0] = []Request{{Unit: 4, Start: Cell{X: 1, Z: 1}, Goal: far(3)}}
	p.searches = 0
	s.SetStepAllowance(150) // too little for the search to finish in one call
	s.accumulator = [10]int32{}
	s.Tick(400)
	if s.active == nil || s.active.Unit != 4 {
		t.Fatal("the retail scheduler did not latch the request")
	}
	p.searches = 3
	s.Tick(401)
	if s.active != nil || len(s.latched) == 0 || s.latched[0].req.Unit != 4 {
		t.Fatalf("the active request did not become the first latched search: %+v", s.latched)
	}
}
