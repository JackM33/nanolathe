package path

import (
	"errors"
	"fmt"
	"slices"

	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
)

// WriteCheckpoint writes the scheduler record then goal table 9 and search
// table 10, without collecting or executing behavior (DESIGN_MULTIPLAYER §16.3.6).
func (s *Scheduler) WriteCheckpoint(e *checkpoint.Encoder, c *CheckpointContext) error {
	e.Field("paths")
	if s == nil || c == nil {
		e.Fail(errors.New("missing scheduler or context"))
		return e.Err()
	}
	if field, err := validateCheckpointScheduler(s, c); err != nil {
		e.Field("paths." + field)
		e.Fail(err)
		return e.Err()
	}
	writeSchedulerCheckpoint(e, c, s)
	goals := c.Goals.Values()
	e.Field("paths.goals")
	e.U16(9)
	e.Count(len(goals))
	for i, g := range goals {
		p := fmt.Sprintf("paths.goals[%d]", i)
		e.Field(p)
		if err := checkpointGoal(g); err != nil {
			e.Fail(err)
			return e.Err()
		}
		writeGoalCheckpoint(e, g, p)
	}
	searches := c.Searches.Values()
	e.Field("paths.searches")
	e.U16(10)
	e.Count(len(searches))
	for i, search := range searches {
		p := fmt.Sprintf("paths.searches[%d]", i)
		cfg, child, err := checkpointSearch(search)
		e.Field(p)
		if err != nil {
			e.Fail(err)
			return e.Err()
		}
		a := c.accessors[search]
		if err := validateCheckpointAccessors(cfg, a); err != nil {
			e.Field(p + ".accessors")
			e.Fail(err)
			return e.Err()
		}
		switch v := search.(type) {
		case *Session:
			e.U8(1)
			writeSessionCheckpoint(e, c, v, a, p)
		case *straightenSearch:
			e.U8(2)
			writeSearchCheckpointReference(e, c, child, p+".Search")
			writeConfigCheckpoint(e, c, cfg, a, p+".cfg")
			writeWrapperCheckpoint(e, v.done, v.out, v.probes, v.status, p)
		case *smoothSearch:
			e.U8(3)
			writeSearchCheckpointReference(e, c, child, p+".Search")
			writeConfigCheckpoint(e, c, cfg, a, p+".cfg")
			writeWrapperCheckpoint(e, v.done, v.out, v.probes, v.status, p)
		default:
			e.Fail(errors.New("absent search table record"))
		}
		if e.Err() != nil {
			return e.Err()
		}
	}
	return e.Err()
}

func writeGoalCheckpointReference(e *checkpoint.Encoder, c *CheckpointContext, g Goal, p string) {
	e.Field(p)
	if err := checkpointGoal(g); err != nil {
		e.Fail(err)
		return
	}
	id, ok := c.Goals.Find(g)
	if !ok {
		e.Fail(errors.New("undiscovered goal"))
		return
	}
	e.U16(9)
	e.U32(uint32(id))
}

func writeSearchCheckpointReference(e *checkpoint.Encoder, c *CheckpointContext, s Search, p string) {
	e.Field(p)
	if _, _, err := checkpointSearch(s); err != nil {
		e.Fail(err)
		return
	}
	id, ok := c.Searches.Find(s)
	if !ok {
		e.Fail(errors.New("undiscovered search"))
		return
	}
	e.U16(10)
	e.U32(uint32(id))
}

func writeCheckpointCell(e *checkpoint.Encoder, v Cell, p string) {
	e.Field(p + ".X")
	e.I32(v.X)
	e.Field(p + ".Z")
	e.I32(v.Z)
}

// Rect's nested lexical order is Max, Min, each X then Z.
func writeCheckpointRect(e *checkpoint.Encoder, v Rect, p string) {
	writeCheckpointCell(e, v.Max, p+".Max")
	writeCheckpointCell(e, v.Min, p+".Min")
}

func writeCheckpointPoints(e *checkpoint.Encoder, points []Point, p string) {
	e.Field(p)
	e.Count(len(points))
	for i, point := range points {
		writeCheckpointCell(e, Cell(point), fmt.Sprintf("%s[%d]", p, i))
	}
}

// Expanded scheduler fields: accumulator, active, activePlayer, activeScale,
// base, baseSet, callCount, haveLast, playerCount, playerCursor, provider,
// publish, scales, search, serviceCount, stepAllowance, unitLimit. activePlayer
// and activeScale are conditional on active. Request: Activation, Goal,
// Player, Start, Unit. Inactive activeReq storage, workspace capacity/lending,
// sweepPolls and trace fields are excluded (DESIGN_MULTIPLAYER §16.3.5).
func writeSchedulerCheckpoint(e *checkpoint.Encoder, c *CheckpointContext, s *Scheduler) {
	e.Field("paths.accumulator")
	for _, v := range s.accumulator {
		e.I32(v)
	}
	e.Field("paths.active")
	e.Bool(s.active != nil)
	if r := s.active; r != nil {
		e.Field("paths.active.Activation")
		e.U64(r.Activation)
		writeGoalCheckpointReference(e, c, r.Goal, "paths.active.Goal")
		e.Field("paths.active.Player")
		e.U8(r.Player)
		writeCheckpointCell(e, r.Start, "paths.active.Start")
		e.Field("paths.active.Unit")
		e.U32(uint32(r.Unit))
		e.Field("paths.activePlayer")
		e.I64(int64(s.activePlayer))
		e.Field("paths.activeScale")
		e.I32(s.activeScale)
	}
	e.Field("paths.base")
	e.I32(s.base)
	e.Field("paths.baseSet")
	e.Bool(s.baseSet)
	e.Field("paths.callCount")
	e.U32(s.callCount)
	e.Field("paths.haveLast")
	e.Bool(s.haveLast)
	e.Field("paths.playerCount")
	e.I64(int64(s.playerCount))
	e.Field("paths.playerCursor")
	e.I64(int64(s.playerCursor))
	e.Field("paths.provider")
	e.Bool(s.provider != nil)
	e.Field("paths.publish")
	e.Bool(s.publish != nil)
	e.Field("paths.scales")
	for _, v := range s.scales {
		e.I32(v)
	}
	e.Field("paths.search")
	e.Bool(s.search != nil)
	e.Field("paths.serviceCount")
	for _, v := range s.serviceCount {
		e.I32(v)
	}
	e.Field("paths.stepAllowance")
	e.I32(s.stepAllowance)
	e.Field("paths.unitLimit")
	e.I32(s.unitLimit)
}

// Goal fields are center/radius/radiusSq; center/inner/innerSq/outer/outerSq;
// and rect. Independent squared thresholds are not recomputed [08 R-SAVE-02 §10].
func writeGoalCheckpoint(e *checkpoint.Encoder, goal Goal, p string) {
	switch g := goal.(type) {
	case *pointGoal:
		e.U8(1)
		writeCheckpointCell(e, g.center, p+".center")
		e.Field(p + ".radius")
		e.I32(g.radius)
		e.Field(p + ".radiusSq")
		e.I32(g.radiusSq)
	case *annulusGoal:
		e.U8(2)
		writeCheckpointCell(e, g.center, p+".center")
		e.Field(p + ".inner")
		e.I32(g.inner)
		e.Field(p + ".innerSq")
		e.I32(g.innerSq)
		e.Field(p + ".outer")
		e.I32(g.outer)
		e.Field(p + ".outerSq")
		e.I32(g.outerSq)
	case *rectGoal:
		e.U8(3)
		writeCheckpointRect(e, g.rect, p+".rect")
	default:
		e.Fail(errors.New("absent goal table record"))
	}
}

// cfg is preceded by the accessor node sequence. Its expanded fields are
// Bounds, CostDir, FootPrintX, FootPrintZ, Goal, HasBounds, LegValue,
// PassableValue, Revise, Scale, Start, StartDir. Callback slots contain the
// descriptor root u32, not a function identity. Workspace is excluded.
func writeConfigCheckpoint(e *checkpoint.Encoder, c *CheckpointContext, cfg SearchConfig, a CheckpointAccessors, p string) {
	e.Field(p + ".accessors.Nodes")
	e.Count(len(a.Nodes))
	for i, n := range a.Nodes {
		writeAccessorCheckpoint(e, n, fmt.Sprintf("%s.accessors.Nodes[%d]", p, i))
	}
	writeCheckpointRect(e, cfg.Bounds, p+".Bounds")
	e.Field(p + ".CostDir")
	e.U32(a.Cost)
	e.Field(p + ".FootPrintX")
	e.I32(cfg.FootPrintX)
	e.Field(p + ".FootPrintZ")
	e.I32(cfg.FootPrintZ)
	writeGoalCheckpointReference(e, c, cfg.Goal, p+".Goal")
	e.Field(p + ".HasBounds")
	e.Bool(cfg.HasBounds)
	e.Field(p + ".LegValue")
	e.U32(a.Leg)
	e.Field(p + ".PassableValue")
	e.U32(a.Passable)
	e.Field(p + ".Revise")
	e.U32(a.Revise)
	e.Field(p + ".Scale")
	e.I32(cfg.Scale)
	writeCheckpointCell(e, cfg.Start, p+".Start")
	e.Field(p + ".StartDir")
	e.U8(cfg.StartDir)
}

// All operands have fixed slots: Against, Bounds, ClaimCounts, ClaimOwn,
// FootprintX, FootprintZ, Height, Inputs, Kind, Layer, Learned, Owner, Per,
// Profile, Requester, Row, Serial, Start, Through, Tick, Wall, Width.
func writeAccessorCheckpoint(e *checkpoint.Encoder, a CheckpointAccessor, p string) {
	e.Field(p + ".Against")
	e.I32(a.Against)
	writeCheckpointRect(e, a.Bounds, p+".Bounds")
	e.Field(p + ".ClaimCounts")
	e.U32(uint32(a.ClaimCounts))
	e.Field(p + ".ClaimOwn")
	e.U32(uint32(a.ClaimOwn))
	e.Field(p + ".FootprintX")
	e.I32(a.FootprintX)
	e.Field(p + ".FootprintZ")
	e.I32(a.FootprintZ)
	e.Field(p + ".Height")
	e.I32(a.Height)
	e.Field(p + ".Inputs")
	e.Count(len(a.Inputs))
	for _, input := range a.Inputs {
		e.U32(input)
	}
	e.Field(p + ".Kind")
	e.U8(a.Kind)
	e.Field(p + ".Layer")
	e.U32(uint32(a.Layer))
	e.Field(p + ".Learned")
	e.U32(uint32(a.Learned))
	e.Field(p + ".Owner")
	e.U8(a.Owner)
	e.Field(p + ".Per")
	e.I32(a.Per)
	e.Field(p + ".Profile")
	for _, v := range a.Profile {
		e.I32(v)
	}
	e.Field(p + ".Requester")
	e.U32(a.Requester)
	e.Field(p + ".Row")
	e.U8(a.Row)
	e.Field(p + ".Serial")
	e.U32(a.Serial)
	writeCheckpointCell(e, a.Start, p+".Start")
	e.Field(p + ".Through")
	e.U8(a.Through)
	e.Field(p + ".Tick")
	e.U32(a.Tick)
	e.Field(p + ".Wall")
	e.U8(a.Wall)
	e.Field(p + ".Width")
	e.I32(a.Width)
}

// Wrappers retain Search, cfg, done, out, probes, status. The synchronous
// finishing locals and smooth's limit/limited/legDir are synchronous scratch.
func writeWrapperCheckpoint(e *checkpoint.Encoder, done bool, out []Point, probes int, status Status, p string) {
	e.Field(p + ".done")
	e.Bool(done)
	writeCheckpointPoints(e, out, p+".out")
	e.Field(p + ".probes")
	e.I64(int64(probes))
	e.Field(p + ".status")
	e.U32(uint32(status))
}

type checkpointCellEntry struct {
	cell  Cell
	value entry
}

func checkpointCellLess(a, b Cell) int {
	if a.Z < b.Z {
		return -1
	}
	if a.Z > b.Z {
		return 1
	}
	if a.X < b.X {
		return -1
	}
	if a.X > b.X {
		return 1
	}
	return 0
}

// Map traversal gathers keys only; sorting precedes every value read. Zero
// entries have the exact absent-cell answer and need no logical record.
func checkpointMapEntries(m map[Cell]entry) []checkpointCellEntry {
	keys := make([]Cell, 0, len(m))
	for cell := range m {
		keys = append(keys, cell)
	}
	slices.SortFunc(keys, checkpointCellLess)
	entries := make([]checkpointCellEntry, 0, len(keys))
	for _, cell := range keys {
		if v := m[cell]; v != (entry{}) {
			entries = append(entries, checkpointCellEntry{cell, v})
		}
	}
	return entries
}

func checkpointEntries(ix *cellIndex) ([]checkpointCellEntry, error) {
	if ix.ws == nil {
		return checkpointMapEntries(ix.m), nil
	}
	ws := ix.ws
	if ws.w <= 0 || ws.h <= 0 || int64(ws.w)*int64(ws.h) != int64(len(ws.slots)) {
		return nil, errors.New("invalid workspace dimensions")
	}
	entries := make([]checkpointCellEntry, 0)
	for i, slot := range ws.slots {
		if slot.gen != ix.gen || slot.e == (entry{}) {
			continue
		}
		cell := Cell{X: ws.origin.X + int32(i%int(ws.w)), Z: ws.origin.Z + int32(i/int(ws.w))}
		entries = append(entries, checkpointCellEntry{cell, slot.e})
	}
	for _, v := range checkpointMapEntries(ix.overflow) {
		// Dense storage shadows any overflow key inside its rectangle.
		if _, inside := ws.slot(v.cell); !inside {
			entries = append(entries, v)
		}
	}
	slices.SortFunc(entries, func(a, b checkpointCellEntry) int { return checkpointCellLess(a.cell, b.cell) })
	return entries, nil
}

func validateCheckpointSearchStorage(s *Session, entries []checkpointCellEntry) error {
	nodes := []Node(nil)
	if s.ns != nil {
		if s.ns.index != &s.entries {
			return errors.New("node index does not alias search entries")
		}
		nodes = s.ns.nodes
	}
	for _, v := range entries {
		id := v.value.id()
		if id < 0 || id != 0 && int(id) >= len(nodes) {
			return errors.New("entry node is outside allocation order")
		}
		if id != 0 && nodes[id].Cell != v.cell {
			return errors.New("entry node has a different cell")
		}
	}
	for i := 1; i < len(nodes); i++ {
		n := nodes[i]
		if n.Parent < 0 || int(n.Parent) >= len(nodes) {
			return errors.New("node parent is outside allocation order")
		}
		if s.entries.get(n.Cell).id() != NodeID(i) {
			return errors.New("node index back-reference differs")
		}
	}
	positions := make([]int32, len(s.heap.positions))
	for i := range positions {
		positions[i] = absentPosition
	}
	for i, h := range s.heap.entries {
		if h.id <= 0 || int(h.id) >= len(nodes) || int(h.id) >= len(positions) {
			return errors.New("heap node is outside allocation order or position table")
		}
		if positions[h.id] != absentPosition {
			return errors.New("duplicate heap node")
		}
		positions[h.id] = int32(i)
	}
	if !slices.Equal(positions, s.heap.positions) {
		return errors.New("heap position back-reference differs")
	}
	if s.heap.spent < 0 || s.heap.spent != 0 && (int(s.heap.spent) >= len(positions) || positions[s.heap.spent] == absentPosition) {
		return errors.New("spent node is absent from heap")
	}
	return nil
}

// Expanded Session: cfg, done, entries, expanded, hasTolerance, haveNearest,
// heap, nearest, nearestDist, notified, ns, popped, resultPoints, resultStatus,
// scale, seeded, setupSteps, tolerance. Entries are (Z,X)-sorted logical keys
// then dir/node/status; heap is entries (id,f) in actual array order then spent.
// NodeStore has presence, nodes (including reserved index zero), scale.
// Node: Cell, Closed, Dir, F, G, H, Open, Parent, Run, TerrainTerm, hSet.
// Fan, storage generations/capacity and derived index/positions are excluded.
func writeSessionCheckpoint(e *checkpoint.Encoder, c *CheckpointContext, s *Session, a CheckpointAccessors, p string) {
	entries, err := checkpointEntries(&s.entries)
	e.Field(p + ".entries")
	if err != nil {
		e.Fail(err)
		return
	}
	if err := validateCheckpointSearchStorage(s, entries); err != nil {
		e.Fail(err)
		return
	}
	writeConfigCheckpoint(e, c, s.cfg, a, p+".cfg")
	e.Field(p + ".done")
	e.Bool(s.done)
	e.Field(p + ".entries")
	e.Count(len(entries))
	for i, v := range entries {
		q := fmt.Sprintf("%s.entries[%d]", p, i)
		writeCheckpointCell(e, v.cell, q+".cell")
		e.Field(q + ".dir")
		e.U8(v.value.dir)
		e.Field(q + ".node")
		e.I32(v.value.node)
		e.Field(q + ".status")
		e.U8(v.value.status)
	}
	e.Field(p + ".expanded")
	e.Bool(s.expanded)
	e.Field(p + ".hasTolerance")
	e.Bool(s.hasTolerance)
	e.Field(p + ".haveNearest")
	e.Bool(s.haveNearest)
	e.Field(p + ".heap.entries")
	e.Count(len(s.heap.entries))
	for i, h := range s.heap.entries {
		q := fmt.Sprintf("%s.heap.entries[%d]", p, i)
		e.Field(q + ".id")
		e.I32(h.id)
		e.Field(q + ".f")
		e.I32(h.f)
	}
	e.Field(p + ".heap.spent")
	e.I64(int64(s.heap.spent))
	writeCheckpointCell(e, s.nearest, p+".nearest")
	e.Field(p + ".nearestDist")
	e.I64(s.nearestDist)
	e.Field(p + ".notified")
	e.U32(uint32(s.notified))
	e.Field(p + ".ns")
	e.Bool(s.ns != nil)
	if s.ns != nil {
		e.Field(p + ".ns.nodes")
		e.Count(len(s.ns.nodes))
		for i, n := range s.ns.nodes {
			q := fmt.Sprintf("%s.ns.nodes[%d]", p, i)
			writeCheckpointCell(e, n.Cell, q+".Cell")
			e.Field(q + ".Closed")
			e.Bool(n.Closed)
			e.Field(q + ".Dir")
			e.U8(n.Dir)
			e.Field(q + ".F")
			e.I32(n.F)
			e.Field(q + ".G")
			e.I32(n.G)
			e.Field(q + ".H")
			e.I32(n.H)
			e.Field(q + ".Open")
			e.Bool(n.Open)
			e.Field(q + ".Parent")
			e.I64(int64(n.Parent))
			e.Field(q + ".Run")
			e.U16(n.Run)
			e.Field(q + ".TerrainTerm")
			e.U16(n.TerrainTerm)
			e.Field(q + ".hSet")
			e.Bool(n.hSet)
		}
		e.Field(p + ".ns.scale")
		e.I32(s.ns.scale)
	}
	e.Field(p + ".popped")
	e.I64(int64(s.popped))
	writeCheckpointPoints(e, s.resultPoints, p+".resultPoints")
	e.Field(p + ".resultStatus")
	e.U32(uint32(s.resultStatus))
	e.Field(p + ".scale")
	e.I32(s.scale)
	e.Field(p + ".seeded")
	e.Bool(s.seeded)
	e.Field(p + ".setupSteps")
	e.I64(int64(s.setupSteps))
	e.Field(p + ".tolerance")
	e.I32(s.tolerance)
}
