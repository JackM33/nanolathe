package features

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Grid reconciliation tracking: what lets the motion phase skip
// syncInstancesToGrid on a tick when nothing it reconciles can have changed
// (DESIGN_ECONOMY_CONSTRUCTION "Grid reconciliation").
//
// The reconciliation reads three things: the instance set, each instance's
// anchor feature word in the terrain's plot, and the definition that word
// binds to. It is idempotent — straight after it runs, running it again
// changes nothing — so it can only have work after one of them changed:
//
//   - the instance set: every write goes through setInstance, deleteInstance
//     or resetInstances, which renew gridRevision;
//   - the plot's feature words: this service's own writers (the footprint
//     clear, the stamp and its dense-pack teardown) renew it, and the one
//     writer outside the service in a composed battle — the resurrection
//     teardown — reports its write through ReleaseRemovedAt;
//   - the terrain, its plot storage or its definition list being replaced or
//     grown, which gridInputs compares directly.
//
// An instance's definition is never reassigned: a different definition is a
// different record. None of this is simulation state; a skipped
// reconciliation is one that would have changed nothing.

// gridInputs identifies the storage the last reconciliation read.
type gridInputs struct {
	terrain  *world.Terrain
	plot     *world.PlotCell
	plotLen  int
	cellW    int32
	defs     **content.FeatureDef
	defsLen  int
	revision uint64
}

func (s *Service) currentGridInputs() gridInputs {
	in := gridInputs{terrain: s.Terrain, revision: s.gridRevision}
	if t := s.Terrain; t != nil {
		in.cellW, in.plotLen, in.defsLen = t.CellW, len(t.Plot), len(t.FeatureDefs)
		if len(t.Plot) != 0 {
			in.plot = &t.Plot[0]
		}
		if len(t.FeatureDefs) != 0 {
			in.defs = &t.FeatureDefs[0]
		}
	}
	return in
}

// noteGridChange records a write that can give the reconciliation work.
func (s *Service) noteGridChange() { s.gridRevision++ }

// SetVerifyGridSync turns on the reconciliation check: every motion phase
// reconciles, and one that the tracking would have skipped panics if it
// changes the instance set. The session sets it with its feature publication
// check (verifyFeatureRevisions); it is for tests and verification builds.
func (s *Service) SetVerifyGridSync(on bool) {
	if s != nil {
		s.verifyGridSync = on
	}
}

// reconcileGrid is the motion phase's reconciliation: it runs
// syncInstancesToGrid unless nothing it reads has changed since it last ran.
// With verifyGridSync set it runs the reconciliation anyway and panics when a
// skipped one would have changed the instance set.
func (s *Service) reconcileGrid() {
	if s == nil {
		return
	}
	current := s.gridSynced && s.syncedGrid == s.currentGridInputs()
	if current && !s.verifyGridSync {
		return
	}
	before := s.gridRevision
	s.syncInstancesToGrid()
	if current && s.gridRevision != before {
		panic("features: grid reconciliation changed the instance set although no grid input changed since it last ran")
	}
	s.syncedGrid, s.gridSynced = s.currentGridInputs(), true
}
