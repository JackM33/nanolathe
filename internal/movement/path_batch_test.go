package movement

import (
	"reflect"
	"runtime"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Nanolathe Modern parallel path batches (docs/DESIGN_MOVEMENT_PATH.md
// "Modern parallel path batches").

// Strict 3.1 and Community 3.9 keep the retail single-request scheduler, as
// does a system with no rules bound; Modern latches its fixed batch. The
// answer is a pure constant and allocates nothing.
func TestPathBatchAnswers(t *testing.T) {
	for _, tc := range []struct {
		name     string
		rules    Rules
		searches int
		pops     int32
	}{
		{"unbound", nil, 0, 0},
		{"strict", StrictRules{}, 0, 0},
		{"community", CommunityRules{}, 0, 0},
		{"modern", &ModernRules{}, modernPathBatch, modernPathBatchPops},
	} {
		sys := NewSystem(syntheticTerrainForIntegrate(), wiringProfile, NewOccupancyGrid())
		sys.Rules = tc.rules
		searches, pops := sys.pathProvider.PathBatch()
		if searches != tc.searches || pops != tc.pops {
			t.Fatalf("%s: PathBatch = (%d, %d), want (%d, %d)", tc.name, searches, pops, tc.searches, tc.pops)
		}
		if allocs := testing.AllocsPerRun(100, func() { sys.pathProvider.PathBatch() }); allocs != 0 {
			t.Fatalf("%s: PathBatch allocates %.0f", tc.name, allocs)
		}
	}
}

// pathBatchFixture is a long walled field with a dozen units of two owners at
// one end and their goals at the other, under Modern movement: each search
// takes more than a call's per-search pops, so searches stay latched across
// calls and expand side by side.
func pathBatchFixture(t *testing.T) (*System, []pool.Handle) {
	t.Helper()
	terrain := layerTerrain(160, 48, 0)
	for x := int32(6); x < 160; x += 8 {
		for z := int32(0); z < 48; z++ {
			if (z+x)%13 != 0 {
				setDerived(terrain, x, z, 0, 200)
			}
		}
	}
	sys := NewSystem(terrain, wiringProfile, NewOccupancyGrid())
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)
	sys.ConfigurePath(2, 20, func(player int) bool { return player == 0 || player == 1 })
	sys.Scheduler.SetStepAllowance(66650)
	sys.Rules = &ModernRules{}
	var handles []pool.Handle
	for i := 0; i < 12; i++ {
		x, z := world.CellToWorld(int32(1+i%3)), world.CellToWorld(int32(2+3*i))
		h, err := w.Create(wiringDef(), uint8(i&1), x, terrain.HeightAt(x, z), z)
		if err != nil {
			t.Fatal(err)
		}
		sys.EnsureUnit(w.Unit(h))
		handles = append(handles, h)
	}
	return sys, handles
}

// A Modern batch publishes the same routes, counts the same search work and
// leaves the same scheduler state whether its rounds run on one processor or
// several: what a round computes is fixed before it starts.
func TestModernPathBatchSameRoutesOnOneAndManyProcessors(t *testing.T) {
	run := func(procs int) (routes []Route, lab LabStats, trace path.SchedulerTraceState, widest int) {
		defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(procs))
		sys, handles := pathBatchFixture(t)
		for tick := uint32(1); tick <= 160; tick++ {
			sys.BeginTick(tick)
			if tick%40 == 1 {
				for i, h := range handles {
					start := path.Cell{X: int32(1 + i%3), Z: int32(2 + 3*i)}
					goal := path.Cell{X: int32(156 - i%4), Z: int32(45 - (3*i+int(tick))%40)}
					sys.SubmitMove(h, uint8(i&1), start, goal)
				}
			}
			sys.Scheduler.Tick(tick)
			widest = max(widest, len(sys.Scheduler.TraceState().Requests))
		}
		for _, h := range handles {
			routes = append(routes, *handleRow(sys.Routes, h))
		}
		return routes, sys.LabStats(), sys.Scheduler.TraceState(), widest
	}
	serialRoutes, serialLab, serialTrace, widest := run(1)
	parallelRoutes, parallelLab, parallelTrace, _ := run(max(runtime.NumCPU(), 4))
	if widest < 2 {
		t.Fatalf("at most %d searches were latched at once; the batch was never exercised", widest)
	}
	if serialLab.Searches < 12 {
		t.Fatalf("only %d searches finished; the comparison proves little", serialLab.Searches)
	}
	if !reflect.DeepEqual(serialRoutes, parallelRoutes) || serialLab != parallelLab || !reflect.DeepEqual(serialTrace, parallelTrace) {
		t.Fatal("a Modern batch's routes depend on the processors that ran it")
	}
}
