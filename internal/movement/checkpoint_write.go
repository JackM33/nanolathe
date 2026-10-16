package movement

import (
	"errors"
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
)

// WriteCheckpoint writes section7's lexical System record and tables 5–8,11.
// Diagnostic exclusions: collisionHistory/Trace/Limit/Dropped, pathFailures,
// labStats/countRefusals. Per-call scratch: airBaseWalkScratch, firstGroup,
// unreachableGoals, visit/searchCfg, searchSlot, pocketCells/Seen/Stack. tickStarted and
// checkpointSearch must be quiescent. passAlliance/trafficNow are reset before
// the next tick; unknown Rules cannot claim that exclusion and are refused.
// Scheduler and pathProvider are presence edges; their bodies belong to paths.
func (s *System) WriteCheckpoint(e *checkpoint.Encoder, c *CheckpointContext) error {
	e.Field("movement")
	if err := s.checkpointContext(c); err != nil {
		e.Fail(err)
		return e.Err()
	}
	if err := s.validateMovementCheckpoint(c); err != nil {
		e.Fail(err)
		return e.Err()
	}
	m := movementCheckpointRecord{e, c, s, "movement"}
	m.boolean("AirSectors", s.AirSectors != nil)
	m.boolean("Classes", s.Classes != nil)
	writeMovementRows(e, c, s, "movement.Collisions", s.Collisions, writeMovementCollisionState)
	m.field("Community")
	if err := s.Community.WriteCheckpoint(e); err != nil {
		return err
	}
	m.boolean("Damage", s.damage != nil)
	writeMovementProfile(e, c, s, &s.Fallback, "movement.Fallback")
	writeMovementRows(e, c, s, "movement.Flights", s.Flights, writeMovementFlightState)
	m.boolean("Grid", s.Grid != nil)
	if s.Grid != nil {
		writeMovementGrid(e, c, s, s.Grid, "movement.Grid")
	}
	k, err := path.CheckpointKernelKind(s.Kernel)
	if err != nil {
		m.fail("Kernel", err)
		return e.Err()
	}
	m.u8("Kernel", k)
	m.i64("PathPlayers", int64(s.PathPlayers))
	m.i32("PathUnitLimit", s.PathUnitLimit)
	m.pilot("PilotState", s.PilotState)
	m.boolean("ProductFootprint", s.productFootprint != nil)
	writeMovementRows(e, c, s, "movement.Routes", s.Routes, writeMovementRoute)
	rulesKind, err := CheckpointRulesKind(s.Rules)
	e.Fail(err)
	m.u8("Rules", rulesKind)
	m.boolean("Scheduler", s.Scheduler != nil)
	writeMovementRows(e, c, s, "movement.Steers", s.Steers, writeMovementSteerState)
	m.boolean("Terrain", s.Terrain != nil)
	writeMovementRows(e, c, s, "movement.activeOrders", s.activeOrders, writeMovementActiveMove)
	// The concrete registry's pure List method returns its actual stale row.
	// Fixed names need no formatting on success (DESIGN_MULTIPLAYER §16.3.81).
	// fmt's pooled scratch can allocate unpredictably under race instrumentation.
	for owner, field := range [...]string{
		"airBases.lists[0]", "airBases.lists[1]", "airBases.lists[2]", "airBases.lists[3]", "airBases.lists[4]",
		"airBases.lists[5]", "airBases.lists[6]", "airBases.lists[7]", "airBases.lists[8]", "airBases.lists[9]",
	} {
		writeMovementHandles(m, field, s.airBases.List(uint8(owner)))
	}
	m.boolean("airLegHandler", s.airLegHandler != nil)
	writeMovementRows(e, c, s, "movement.arrivalHandles", s.arrivalHandles, writeMovementArrivalHandle)
	writeMovementRows(e, c, s, "movement.clearanceRoutes", s.clearanceRoutes, writeMovementModernClearanceRoute)
	writeMovementHandles(m, "firstRequests", s.firstRequests)
	m.count("jamReleases", len(s.jamReleases))
	for i := range s.jamReleases {
		writeMovementJamRelease(e, c, s, &s.jamReleases[i], fmt.Sprintf("movement.jamReleases[%d]", i))
	}
	m.boolean("layerRegistry", s.layerRegistry != nil)
	if s.layerRegistry != nil {
		writeMovementLayers(e, c, s, s.layerRegistry, "movement.layerRegistry")
	}
	m.boolean("learned", s.learned != nil)
	if s.learned != nil {
		writeMovementLearned(e, c, s, s.learned, "movement.learned")
	}
	m.count("moveGoals", len(s.moveGoals))
	for i, v := range s.moveGoals {
		id, ok := c.moveGoals.Find(v)
		e.FieldIndex("movement.moveGoals", i, "")
		writeMovementReference(e, 11, id, ok)
	}
	m.u64("nextActivation", s.nextActivation)
	m.boolean("pathProvider", s.pathProvider != nil)
	m.count("pendingFiled", len(s.pendingFiled))
	for _, v := range s.pendingFiled {
		e.Bool(v)
	}
	writeMovementHandles(m, "pendingFilings", s.pendingFilings)
	writeMovementHandles(m, "pendingLayers", s.pendingLayers)
	m.i64("pocketLive", int64(s.pocketLive))
	m.count("pockets", len(s.pockets))
	for i := range s.pockets {
		writeMovementPocketCert(e, c, s, &s.pockets[i], fmt.Sprintf("movement.pockets[%d]", i))
	}
	for _, row := range []struct {
		name string
		v    []int
	}{{"prevMoveTier", s.prevMoveTier}, {"prevSFXBand", s.prevSFXBand}} {
		m.count(row.name, len(row.v))
		for _, v := range row.v {
			e.I64(int64(v))
		}
	}
	m.count("profileNames", len(s.profileNames))
	for i, v := range s.profileNames {
		e.FieldIndex("movement.profileNames", i, "")
		e.String(v)
	}
	writeMovementRows(e, c, s, "movement.profiles", s.profiles, writeMovementProfile)
	m.count("recordGoals", len(s.recordGoals))
	for i, row := range s.recordGoals {
		e.FieldIndex("movement.recordGoals", i, "")
		e.Count(len(row))
		for j := range row {
			writeMovementRecordGoal(e, c, s, &row[j], fmt.Sprintf("movement.recordGoals[%d][%d]", i, j))
		}
	}
	writeMovementRows(e, c, s, "movement.repairLandings", s.repairLandings, writeMovementRepairLanding)
	writeMovementRows(e, c, s, "movement.sessions", s.sessions, writeMovementWorkingSet)
	m.u32("tick", s.tick)
	m.count("traffic", len(s.traffic))
	for i := range s.traffic {
		writeMovementTrafficState(e, c, s, &s.traffic[i], fmt.Sprintf("movement.traffic[%d]", i))
	}
	m.count("unreachable", len(s.unreachable))
	for i := range s.unreachable {
		writeMovementUnreachableCert(e, c, s, &s.unreachable[i], fmt.Sprintf("movement.unreachable[%d]", i))
	}
	m.i64("unreachableLive", int64(s.unreachableLive))
	m.i64("workSmooth", s.workSmooth)
	m.i64("workTick", s.workTick)
	m.boolean("world", s.world != nil)
	layers := c.Layers.Values()
	m.field("layers")
	e.U16(5)
	e.Count(len(layers))
	for i, v := range layers {
		writeMovementLayer(e, c, s, v, fmt.Sprintf("movement.layers[%d]", i))
	}
	pilots := c.pilots.Values()
	m.field("pilots")
	e.U16(6)
	e.Count(len(pilots))
	for i, v := range pilots {
		writeMovementPilot(e, c, s, v, fmt.Sprintf("movement.pilots[%d]", i))
	}
	rows := c.claimRows.Values()
	m.field("claimRows")
	e.U16(7)
	e.Count(len(rows))
	for i, v := range rows {
		writeMovementClaimRow(e, v, fmt.Sprintf("movement.claimRows[%d]", i))
	}
	payloads := c.Payloads.Values()
	m.field("payloads")
	e.U16(8)
	e.Count(len(payloads))
	for i, v := range payloads {
		writeMovementPayload(e, c, s, v, fmt.Sprintf("movement.payloads[%d]", i))
	}
	goals := c.moveGoals.Values()
	m.field("groundGoals")
	e.U16(11)
	e.Count(len(goals))
	for i, v := range goals {
		writeMovementMoveGoal(e, c, s, v, fmt.Sprintf("movement.groundGoals[%d]", i))
	}
	return e.Err()
}

func writeMovementHandles(m movementCheckpointRecord, f string, values []pool.Handle) {
	m.count(f, len(values))
	for _, v := range values {
		m.e.U32(uint32(v))
	}
}

// Working set: activation, goal, session, through. Captured accessors are
// lowered into path's own search record, rather than written a second time.
func writeMovementWorkingSet(e *checkpoint.Encoder, c *CheckpointContext, s *System, v *pathWorkingSet, p string) {
	m := movementCheckpointRecord{e, c, s, p}
	m.u64("activation", v.activation)
	m.goal("goal", v.goal)
	a, err := lowerCheckpointAccessors(c, v.checkpoint)
	if err != nil {
		m.fail("session", err)
		return
	}
	if v.session != nil {
		// Existing metadata must agree; SetAccessors can copy capture-local
		// descriptors but cannot register graph identities or mutate a search.
		if err := c.Paths.SetAccessors(v.session, a); err != nil {
			m.fail("session", err)
			return
		}
	} else if len(a.Nodes) != 0 {
		m.fail("session", errors.New("accessors retained without a search"))
		return
	}
	id, ok := c.Paths.Searches.Find(v.session)
	m.ref("session", 10, id, ok)
	m.u8("through", v.through)
}
