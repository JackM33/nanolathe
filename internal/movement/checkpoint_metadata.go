package movement

import (
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// These records describe existing retained callbacks; none is read by gameplay.
// DESIGN_MULTIPLAYER §16.3.5–§16.3.6 owns their checkpoint disposition. Opening
// a search records operands once, alongside the callbacks [04 R-PATH-01 §4].
// Resumption keeps them even when the requester or its current rules change.
// A dropped working set drops its roots [04 R-PATH-01 §6]. No capture operation
// calls a rule, callback, layer revision or search method to discover operands.

// checkpointClaimRow is a comparable identity for one actual backing row in
// table 7. kind is retained as the union tag: 1 own, 2 counts. Exactly one of
// own/counts is the retained row for that tag; the other is nil. The holder is
// attached where its row is allocated, never reconstructed or copied during
// checkpointing. Recounts and serial wrap mutate the same row. Replacement
// allocates a new holder, leaving an old search's row reachable through its
// captured holder (DESIGN_MOVEMENT_PATH "Modern route claims").
// Slice capacity is allocation detail; the complete logical row is retained.
type checkpointClaimRow struct {
	kind   uint8
	own    []uint32
	counts [][8]uint8
}

// checkpointAccessorCapture is one construction-time node before object IDs
// exist. value retains the meaningful scalar operands of its published kind;
// its Inputs and four object-ID fields stay nil/zero until checkpoint lowering.
// inputs retains ordered graph edges, not discarded intermediate choices.
// layer resolves to table 5; learned plus learnedOwner resolves to the canonical
// learned grid's owner+1 operand; counts/own resolve independently to table 7.
// system is the actual hostile/keeps-ground receiver, validated against the
// composed owner; its live world/alliance ports are not frozen here. registry
// and class retain the revision callback's real lookup operands for validation
// against layer. They are checked bindings, not additional serialized objects.
// Scalar disposition by kind (all other value fields remain zero):
//   - class: Requester, Owner, copied Profile, Tick, raw FootprintX/Z;
//   - own class: the class operands plus the inclusive own Bounds;
//   - learned: raw FootprintX/Z, plus learned/learnedOwner above;
//   - through: Owner, raw FootprintX/Z and the selected Through;
//   - static: raw FootprintX/Z, plus layer above;
//   - wall: Owner and Wall (hostile 1, keeps-ground 2), plus system above;
//   - wedge: Start, strict Bounds and raw FootprintX/Z;
//   - claims: Owner, Serial, Width/Height, clamped FootprintX/Z, resolved
//     Per/Against and Row (all 2, slow 3), plus counts/own above;
//   - revision: copied Profile, Requester and Tick, plus layer/registry/class;
//     Requester is zero for a search a Modern path batch opened.
//
// The profile's eight operands are widened exactly to the published int32
// descriptor fields. None of these pointer identities is serialized as an
// address. All other mutable data reachable through them belongs to its
// existing state owner.
type checkpointAccessorCapture struct {
	value  path.CheckpointAccessor
	inputs []*checkpointAccessorCapture

	layer        *ClassLayer
	learned      *LearnedTerrain
	learnedOwner uint8
	counts, own  *checkpointClaimRow

	system   *System
	registry *ClassLayers
	class    string
}

// checkpointAccessorRoots retains only the final callbacks. Replacement drops
// its old root, while wedge keeps the preceding view as an input and the leg
// root stays independent. Nil cost/leg/revise means the callback was absent.
// unsupported is a capture refusal reason, not gameplay state or wire text.
// System.checkpointSearch is scratch only while Pilot.Search lends its cfg;
// pathWorkingSet.checkpoint owns the roots thereafter. The scratch must be nil
// at a checkpoint boundary. Unknown variants continue ordinary gameplay.
type checkpointAccessorRoots struct {
	cost, leg, passable, revise *checkpointAccessorCapture
	unsupported                 string
}

func checkpointProfile(p Profile) [8]int32 {
	return [8]int32{
		int32(p.FootPrintX), int32(p.FootPrintZ), p.MaxWaterDepth, p.MinWaterDepth,
		int32(p.MaxSlope), int32(p.BadSlope), int32(p.MaxWaterSlope), int32(p.BadWaterSlope),
	}
}

func checkpointClassCapture(layer *ClassLayer, p Profile, requester pool.Handle, owner uint8, tick uint32) *checkpointAccessorCapture {
	return &checkpointAccessorCapture{
		value: path.CheckpointAccessor{Kind: 1, Requester: uint32(requester), Owner: owner,
			Profile: checkpointProfile(p), Tick: tick, FootprintX: int32(p.FootPrintX), FootprintZ: int32(p.FootPrintZ)},
		layer: layer,
	}
}

// checkpointOwnClassCapture is the class view of a search a Modern path batch
// opened: the class view's operands, plus the inclusive anchor rectangle
// within which the requester's own occupancy is read as fresh
// (own_footprint.go).
func checkpointOwnClassCapture(class *checkpointAccessorCapture, own *ownFootprint) *checkpointAccessorCapture {
	c := *class
	c.value.Kind = 9
	c.value.Bounds = path.Rect{Min: path.Cell{X: own.minX, Z: own.minZ}, Max: path.Cell{X: own.maxX, Z: own.maxZ}}
	return &c
}

func checkpointLearnedCapture(base *checkpointAccessorCapture, learned *LearnedTerrain, owner uint8, fx, fz int16) *checkpointAccessorCapture {
	return &checkpointAccessorCapture{
		value:  path.CheckpointAccessor{Kind: 2, FootprintX: int32(fx), FootprintZ: int32(fz)},
		inputs: []*checkpointAccessorCapture{base}, learned: learned, learnedOwner: owner,
	}
}

func checkpointThroughCapture(base *checkpointAccessorCapture, owner, through uint8, fx, fz int16) *checkpointAccessorCapture {
	return &checkpointAccessorCapture{
		value:  path.CheckpointAccessor{Kind: 3, Owner: owner, Through: through, FootprintX: int32(fx), FootprintZ: int32(fz)},
		inputs: []*checkpointAccessorCapture{base},
	}
}

func checkpointStaticCapture(base *checkpointAccessorCapture, layer *ClassLayer, fx, fz int16) *checkpointAccessorCapture {
	return &checkpointAccessorCapture{
		value:  path.CheckpointAccessor{Kind: 4, FootprintX: int32(fx), FootprintZ: int32(fz)},
		inputs: []*checkpointAccessorCapture{base}, layer: layer,
	}
}

func checkpointWallCapture(base *checkpointAccessorCapture, s *System, owner, wall uint8) *checkpointAccessorCapture {
	return &checkpointAccessorCapture{
		value:  path.CheckpointAccessor{Kind: 5, Owner: owner, Wall: wall},
		inputs: []*checkpointAccessorCapture{base}, system: s,
	}
}

func checkpointWedgeCapture(base, override *checkpointAccessorCapture, start Cell, fx, fz int32, footX, footZ int16) *checkpointAccessorCapture {
	// These are the strict comparison operands, with the callback's existing
	// int32 arithmetic. They are neither cfg.Bounds nor an inclusive rectangle.
	return &checkpointAccessorCapture{
		value: path.CheckpointAccessor{Kind: 6, Start: path.Cell(start),
			Bounds:     path.Rect{Min: path.Cell{X: start.X - fx, Z: start.Z - fz}, Max: path.Cell{X: start.X + fx, Z: start.Z + fz}},
			FootprintX: int32(footX), FootprintZ: int32(footZ)},
		inputs: []*checkpointAccessorCapture{base, override},
	}
}

// Only the production value pilots have reviewed callback effects. Inspecting
// their types/ordered children calls no pilot; a custom or pointer pilot still
// runs normally but cannot attest the resulting cfg through this metadata.
func checkpointPilotSupported(p Pilot) bool {
	switch p := p.(type) {
	case nil, NoPilot, ClaimsPilot, ArrivePilot:
		return true
	case Pilots:
		for _, child := range p {
			if !checkpointPilotSupported(child) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
