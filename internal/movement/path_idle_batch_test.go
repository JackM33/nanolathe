package movement

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// singlePollProvider hides the provider's idle-run boundary, so the scheduler
// polls every candidate one at a time.
type singlePollProvider struct{ p *pathProvider }

func (s singlePollProvider) PlayerCount() int                                { return s.p.PlayerCount() }
func (s singlePollProvider) UnitLimit() int32                                { return s.p.UnitLimit() }
func (s singlePollProvider) Eligible(player int) bool                        { return s.p.Eligible(player) }
func (s singlePollProvider) Poll(player int) (path.Request, path.PollResult) { return s.p.Poll(player) }
func (s singlePollProvider) SetPathTick(tick uint32)                         { s.p.SetPathTick(tick) }
func (s singlePollProvider) Cancel(unit pool.Handle) bool                    { return s.p.Cancel(unit) }
func (s singlePollProvider) HasRequest(unit pool.Handle) bool                { return s.p.HasRequest(unit) }
func (s singlePollProvider) PathWorkBound() (int32, bool)                    { return s.p.PathWorkBound() }
func (s singlePollProvider) SweepLen(player int) int                         { return s.p.SweepLen(player) }
func (s singlePollProvider) EndPathTick()                                    { s.p.EndPathTick() }

func idleBatchFixture(t *testing.T, batch bool) (*System, []pool.Handle) {
	t.Helper()
	sys := NewSystem(syntheticTerrainForIntegrate(), wiringProfile, NewOccupancyGrid())
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)
	sys.ConfigurePath(2, 10, func(player int) bool { return player == 0 || player == 1 })
	sys.Scheduler.SetStepAllowance(66650)
	if !batch {
		sys.Scheduler.SetCandidateProvider(singlePollProvider{sys.pathProvider})
	}
	var handles []pool.Handle
	for i := 0; i < 6; i++ {
		owner := uint8(i & 1)
		h, err := w.Create(wiringDef(), owner, world.CellToWorld(int32(1+i*3)), 0, world.CellToWorld(int32(1+i)))
		if err != nil {
			t.Fatal(err)
		}
		sys.EnsureUnit(w.Unit(h))
		handles = append(handles, h)
	}
	return sys, handles
}

// The real provider's idle runs are a cost change only. With the large step
// allowance that makes almost every poll idle, a scheduler that batches them
// must publish the same routes and leave the provider and scheduler in the
// same state as one that polls every candidate [04 R-PATH-01 §6].
func TestPathProviderIdleBatchingMatchesSinglePolls(t *testing.T) {
	run := func(batch bool) (routes []Route, cursors [10]int, trace path.SchedulerTraceState) {
		sys, handles := idleBatchFixture(t, batch)
		for tick := uint32(1); tick <= 200; tick++ {
			sys.BeginTick(tick)
			if tick%23 == 1 {
				for i, h := range handles {
					goal := path.Cell{X: int32(18 - (int(tick)+i*5)%17), Z: int32(2 + (int(tick)/23+i)%15)}
					start := path.Cell{X: int32(1 + i*3), Z: int32(1 + i)}
					sys.SubmitMove(h, uint8(i&1), start, goal)
				}
			}
			sys.Scheduler.Tick(tick)
		}
		for _, h := range handles {
			routes = append(routes, *handleRow(sys.Routes, h))
		}
		return routes, sys.pathProvider.cursor, sys.Scheduler.TraceState()
	}
	singleRoutes, singleCursors, singleTrace := run(false)
	batchRoutes, batchCursors, batchTrace := run(true)
	if !reflect.DeepEqual(batchRoutes, singleRoutes) {
		t.Fatalf("batched routes differ:\nbatched %+v\nsingle  %+v", batchRoutes, singleRoutes)
	}
	if batchCursors != singleCursors {
		t.Fatalf("batched cursors %v, single-poll cursors %v", batchCursors, singleCursors)
	}
	if !reflect.DeepEqual(batchTrace, singleTrace) {
		t.Fatalf("batched scheduler state %+v, single-poll %+v", batchTrace, singleTrace)
	}
	published := 0
	for _, r := range singleRoutes {
		if r.Count > 0 {
			published++
		}
	}
	if published == 0 {
		t.Fatal("no route was published; the comparison proves nothing")
	}
}

// modernSerialPathRules is Modern with the retail single-request scheduler,
// so a search hook sees every admission: a Modern path batch opens its
// searches through the provider instead (DESIGN_MOVEMENT_PATH "Modern parallel
// path batches"), and its admission loop is this one.
type modernSerialPathRules struct{ ModernRules }

func (*modernSerialPathRules) PathBatch(*System) (int, int32) { return 0, 0 }

// Modern's futile-sweep cutoff applies to idle polls too. An idle call must
// leave the same cursors, and therefore the same admission order next call,
// whether those polls were individual or batched.
func TestPathProviderBoundedIdleBatchingMatchesSinglePolls(t *testing.T) {
	run := func(batch bool) ([10]int, []pool.Handle) {
		sys, handles := idleBatchFixture(t, batch)
		sys.Rules = &modernSerialPathRules{}
		sys.BeginTick(100)
		sys.Scheduler.Tick(100)
		cursors := sys.pathProvider.cursor
		var admissions []pool.Handle
		sys.Scheduler.SetSearch(func(r path.Request, _ int32, _ int) path.WorkResult {
			admissions = append(admissions, r.Unit)
			return path.WorkResult{Done: true}
		})
		for i, h := range handles {
			sys.SubmitMove(h, uint8(i&1), path.Cell{X: 1, Z: 1}, path.Cell{X: 18, Z: 18})
		}
		sys.BeginTick(101)
		sys.Scheduler.Tick(101)
		if len(admissions) != len(handles) {
			t.Fatalf("admitted %d of %d requests", len(admissions), len(handles))
		}
		return cursors, admissions
	}
	singleCursors, singleAdmissions := run(false)
	batchCursors, batchAdmissions := run(true)
	if singleCursors != batchCursors || !reflect.DeepEqual(singleAdmissions, batchAdmissions) {
		t.Fatalf("batched cursors %v admissions %v, single-poll cursors %v admissions %v",
			batchCursors, batchAdmissions, singleCursors, singleAdmissions)
	}
}

// SkipIdle moves the cursor exactly as the same number of idle polls does,
// including the first visit's start and the wrap at the slice's end.
func TestPathProviderSkipIdleMatchesPolls(t *testing.T) {
	for _, n := range []int32{1, 2, 7, 63, 64, 65, 1000, 22216} {
		polled, _ := idleBatchFixture(t, true)
		skipped, _ := idleBatchFixture(t, true)
		for i := int32(0); i < n; i++ {
			if _, result := polled.pathProvider.Poll(1); result == path.PollRequest {
				t.Fatal("an idle fixture admitted a request")
			}
		}
		if run := skipped.pathProvider.IdleRun(1, n); run != n {
			t.Fatalf("IdleRun(%d) = %d with nothing staged", n, run)
		}
		skipped.pathProvider.SkipIdle(1, n)
		if polled.pathProvider.cursor != skipped.pathProvider.cursor || polled.pathProvider.started != skipped.pathProvider.started {
			t.Fatalf("n=%d: skipped cursor %v, polled %v", n, skipped.pathProvider.cursor, polled.pathProvider.cursor)
		}
	}
}

// A staged slot ends the idle run exactly where the polls would reach it, on
// either side of the slice's wrap.
func TestPathProviderIdleRunStopsAtStagedSlot(t *testing.T) {
	for _, lead := range []int32{0, 1, 5, 300} {
		sys, handles := idleBatchFixture(t, true)
		p := sys.pathProvider
		h := handles[3] // owned by player 1
		p.Submit(path.Request{Unit: h, Player: 1})
		p.SkipIdle(1, lead)
		run := p.IdleRun(1, 1<<20)
		polls := int32(0)
		for {
			c := p.cursor[1]
			if !p.started[1] {
				start, _, _ := p.world.SliceForPlayer(1)
				c = start
			}
			start, end, _ := p.world.SliceForPlayer(1)
			if nextSlot(c, start, end) == int(h) {
				break
			}
			p.SkipIdle(1, 1)
			polls++
		}
		if run != polls {
			t.Fatalf("lead %d: IdleRun = %d, the staged slot is %d polls away", lead, run, polls)
		}
	}
}

// The constructor's cursor is already on the first allocatable slot. These
// expectations distinguish that seed from a cursor preceding the slice, even
// if Poll and its batching optimization accidentally share the same error
// [04 R-PATH-01 §6].
func TestPathProviderInitialCursorBeforeAdvance(t *testing.T) {
	for _, length := range []int{1, 4} {
		for _, stagedOffset := range []int{0, length - 1} {
			s := NewSystem(syntheticTerrainForIntegrate(), wiringProfile, NewOccupancyGrid())
			w := newMovementFixtureWorld(length)
			s.BindWorld(w)
			start, _, _ := w.SliceForPlayer(0)
			p := s.pathProvider
			p.Submit(path.Request{Unit: pool.Handle(start + stagedOffset), Player: 0})
			wantIdle := int32(stagedOffset - 1)
			if stagedOffset == 0 {
				wantIdle = int32(length - 1)
			}
			if got := p.IdleRun(0, 100); got != wantIdle {
				t.Fatalf("length=%d offset=%d initial idle run=%d, want %d", length, stagedOffset, got, wantIdle)
			}
			p.SkipIdle(0, wantIdle)
			if _, result := p.Poll(0); result != path.PollNoUnit || p.cursor[0] != start+stagedOffset {
				t.Fatalf("length=%d offset=%d selected cursor=%d result=%d", length, stagedOffset, p.cursor[0], result)
			}
		}
	}
}

// HasRequest and Cancel answer from the staged index first; across resubmits
// to another player, cancels and drops they answer exactly what the players'
// maps hold.
func TestPathProviderRequestQueriesFollowTheMaps(t *testing.T) {
	sys, handles := idleBatchFixture(t, true)
	p := sys.pathProvider
	inMaps := func(h pool.Handle) bool {
		for player := range p.requests {
			if _, ok := p.requests[player][h]; ok {
				return true
			}
		}
		return false
	}
	check := func(step string) {
		t.Helper()
		for _, h := range handles {
			if got, want := p.HasRequest(h), inMaps(h); got != want {
				t.Fatalf("%s: HasRequest(%d) = %v, maps hold %v", step, h, got, want)
			}
		}
	}
	check("empty")
	p.Submit(path.Request{Unit: handles[1], Player: 0})
	p.Submit(path.Request{Unit: handles[3], Player: 1})
	check("submitted")
	p.Submit(path.Request{Unit: handles[1], Player: 1})
	check("resubmitted to another player")
	if p.Cancel(handles[2]) {
		t.Fatal("cancelled a unit with no request")
	}
	if !p.Cancel(handles[1]) || p.Cancel(handles[1]) {
		t.Fatal("cancel did not remove exactly one request")
	}
	check("cancelled")
	p.dropRequest(1, handles[3])
	check("dropped")
}
