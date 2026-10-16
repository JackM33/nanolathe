package movement

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/path"
)

// A system that can never be captured opens its searches without checkpoint
// accessor records, and nothing a search reads depends on them: the same
// requests publish the same routes and count the same work with the records
// and without them.
func TestSearchCaptureRecordsChangeNoRoute(t *testing.T) {
	run := func(capture bool) ([]Route, LabStats, path.SchedulerTraceState, bool) {
		sys, handles := pathBatchFixture(t)
		sys.SetSearchCaptureEnabled(capture)
		recorded := false
		for tick := uint32(1); tick <= 120; tick++ {
			sys.BeginTick(tick)
			if tick%40 == 1 {
				for i, h := range handles {
					start := path.Cell{X: int32(1 + i%3), Z: int32(2 + 3*i)}
					goal := path.Cell{X: int32(156 - i%4), Z: int32(45 - (3*i+int(tick))%40)}
					sys.SubmitMove(h, uint8(i&1), start, goal)
				}
			}
			sys.Scheduler.Tick(tick)
			for _, h := range handles {
				if ws := handleRow(sys.sessions, int(h)); ws != nil && ws.session != nil && ws.checkpoint != nil {
					recorded = true
				}
			}
		}
		var routes []Route
		for _, h := range handles {
			routes = append(routes, *handleRow(sys.Routes, h))
		}
		return routes, sys.LabStats(), sys.Scheduler.TraceState(), recorded
	}
	withRoutes, withLab, withTrace, withRecords := run(true)
	withoutRoutes, withoutLab, withoutTrace, withoutRecords := run(false)
	if !withRecords {
		t.Fatal("a capturable system opened no search with accessor records")
	}
	if withoutRecords {
		t.Fatal("a system that can never be captured built accessor records")
	}
	if !reflect.DeepEqual(withRoutes, withoutRoutes) || withLab != withoutLab || !reflect.DeepEqual(withTrace, withoutTrace) {
		t.Fatal("the accessor records changed what the searches computed")
	}
}
