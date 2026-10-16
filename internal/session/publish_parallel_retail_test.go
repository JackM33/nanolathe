//go:build retail

package session

import "testing"

// TestParallelPublicationMatchesSerialRetail publishes a busy retail battle —
// moving armies with routes, factory queues, nanoframes and animated scripts —
// once on the calling goroutine and once with the unit channel's workers, and
// requires the two committed frames to agree in every exported field
// (INVARIANTS I1 "Parallel simulation exception"). Both are republications of
// the same committed tick, so nothing but the build differs.
//
// Every publication of the battle also runs the feature revision check: each
// feature is read and compared, and the run fails if one changed its view
// while its publication revision did not (feature_publication.go).
func TestParallelPublicationMatchesSerialRetail(t *testing.T) {
	s := checkpointCostBusyBattle(t)
	t.Cleanup(s.closeAIControllers)
	s.verifyFeatureRevisions = true
	for range 300 {
		s.ExecuteStep(StepPlan{run: true, ticks: 1})
	}
	if s.State != StateBattle {
		t.Fatalf("busy battle left the battle state at tick %d", s.Clock.GlobalTick)
	}
	tick := s.Clock.GlobalTick
	s.publishWorkerLimit = -1
	s.publishPausedSnapshot(tick)
	serial := flattenFrame(s.Snapshot.Current())
	s.publishWorkerLimit = 0
	s.publishPausedSnapshot(tick)
	parallel := flattenFrame(s.Snapshot.Current())
	if live := unitChannelLive(s); live < publishParallelMinUnits {
		t.Fatalf("busy battle has %d live units, below the %d the worker build needs", live, publishParallelMinUnits)
	}
	f := s.Snapshot.Current()
	routes := 0
	for _, q := range f.OrderQueues {
		for _, o := range q.Primary {
			if len(o.Route) != 0 {
				routes++
			}
		}
	}
	if len(f.OrderQueues) == 0 || routes == 0 {
		t.Fatalf("busy battle published %d order queues and %d routes; the comparison needs both", len(f.OrderQueues), routes)
	}
	compareFlatFrames(t, serial, parallel)
}
