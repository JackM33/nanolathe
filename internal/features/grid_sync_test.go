package features

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestGridReconciliationSkipsOnlyUnchangedTicks locks the tracking contract of
// grid_sync.go: a motion phase with nothing new to reconcile does no work, a
// reported grid write is reconciled on the next one, and the verification
// switch catches a write that went unreported.
func TestGridReconciliationSkipsOnlyUnchangedTicks(t *testing.T) {
	s := restingForestService(t, 4, 0)
	s.TickMotion(1) // the first motion phase always reconciles
	s.Terrain.Plot[0].SetFeature(world.PlotFeatureNone)
	s.TickMotion(2)
	if s.InstanceAt(0, 0) == nil {
		t.Fatal("a motion phase with no reported change reconciled anyway; the skip is not in effect")
	}
	s.noteGridChange()
	s.TickMotion(3)
	if s.InstanceAt(0, 0) != nil {
		t.Fatal("a reported grid write was not reconciled on the next motion phase")
	}
	s.Terrain.Plot[1].SetFeature(world.PlotFeatureNone)
	s.SetVerifyGridSync(true)
	defer func() {
		if recover() == nil {
			t.Fatal("verification did not catch an unreported grid write")
		}
	}()
	s.TickMotion(4)
}

// TestGridReconciliationSkipNeverMissesAServiceWrite drives the service's own
// writers — stamps over occupied ground (the dense-pack teardown), removals
// with every successor cause, death and reclaim transitions, ignitions that
// burn out into successors and spread, damage, the reclaim payout, and the
// resurrection teardown reported through ReleaseRemovedAt — with verification
// on. Every motion phase reconciles, and any phase the tracking would have
// skipped must find nothing to change.
func TestGridReconciliationSkipNeverMissesAServiceWrite(t *testing.T) {
	tree := spreadTree("tree")
	tree.Reclaimable = true
	tree.SeqNameDie = "die"
	ash := featureDef("ash", 1, 0, 10)
	ash.Filename = "ash"
	tree.FeatureBurntDef = ash
	tree.FeatureDeadDef = ash
	big := defP1("big", 2, 2, "", "trees")
	big.Flamable, big.SeqNameBurn, big.SpreadChance, big.SparkTime = true, "burn", 50, 60
	big.FeatureBurntDef = ash
	heap := defP1("heap", 2, 1, "heap.3do", "")
	heap.Reclaimable = true
	wreck := defP1("wreck", 1, 1, "wreck.3do", "")
	wreck.Reclaimable, wreck.Flamable, wreck.SeqNameBurn = true, true, "burn"
	wreck.FeatureDeadDef, wreck.FeatureReclamateDef, wreck.FeatureBurntDef = heap, ash, heap
	defs := []*content.FeatureDef{tree, big, wreck, heap, ash}
	s := spreadService(t, 11, defs...)
	stubSequences(s, []int32{3, 3}, []int32{2, 2}, []int32{2})
	s.SetVerifyGridSync(true)
	seed := uint32(2024)
	next := func(n uint32) uint32 {
		seed = seed*1664525 + 1013904223
		return (seed >> 8) % n
	}
	w, h := uint32(s.Terrain.CellW), uint32(s.Terrain.CellH)
	for tick := uint32(1); tick <= 3000; tick++ {
		cx, cz := int(next(w)), int(next(h))
		switch op := next(16); {
		case op < 5:
			s.spawnFeatureAt(cx, cz, defs[next(uint32(len(defs)))])
		case op == 5:
			s.RemoveFeatureAt(cx, cz, Cause(next(3)))
		case op == 6:
			s.Ignite(cx, cz, 1, int32(next(20)))
		case op == 7:
			s.DamageFeature(cx, cz, int32(next(30)))
		case op == 8:
			s.ReclaimAt(cx, cz)
		case op == 9:
			if inst := s.InstanceAt(cx, cz); inst != nil {
				s.transitionFeatureAt(cx, cz, inst.Def, next(2) == 0)
			}
		case op == 10:
			// The resurrection teardown: an owner outside the service clears
			// the footprint, then reports it.
			if inst := s.InstanceAt(cx, cz); inst != nil {
				clearFeatureRect(s.Terrain, cx, cz, inst.Def)
				s.ReleaseRemovedAt(cx, cz)
			}
		}
		tickFeature(s, tick)
	}
	if len(s.Instances()) == 0 {
		t.Fatal("the scenario ended with an empty map; it exercised too little")
	}
}
