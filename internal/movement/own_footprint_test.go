package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Nanolathe Modern parallel path batches: each search reads only its own
// requester's footprint as open (docs/DESIGN_MOVEMENT_PATH.md "Modern parallel
// path batches", "Own footprint").

// ownFootprintFixture stands two units of one owner a few cells apart, idle
// since they were created, west of a wall their goal lies beyond, and opens
// the tick at which both are stale to the occupant-age gate.
func ownFootprintFixture(t *testing.T) (*System, [2]pool.Handle) {
	t.Helper()
	terrain := layerTerrain(40, 40, 0)
	for z := int32(0); z < 37; z++ {
		setDerived(terrain, 20, z, 0, 200)
	}
	sys := NewSystem(terrain, wiringProfile, NewOccupancyGrid())
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)
	sys.ConfigurePath(1, 20, func(player int) bool { return player == 0 })
	var hs [2]pool.Handle
	for i, z := range []int32{10, 13} {
		x, wz := world.CellToWorld(10), world.CellToWorld(z)
		h, err := w.Create(wiringDef(), 0, x, terrain.HeightAt(x, wz), wz)
		if err != nil {
			t.Fatal(err)
		}
		sys.EnsureUnit(w.Unit(h))
		hs[i] = h
	}
	sys.BeginTick(100)
	return sys, hs
}

func ownFootprintRequest(sys *System, h pool.Handle) path.Request {
	a, _, _, _ := sys.CommittedFootprint(h)
	return path.Request{Unit: h, Player: 0, Start: path.Cell(a), Goal: path.PointGoal(path.Cell{X: 35, Z: 5}, 0)}
}

func ownFootprintView(t *testing.T, sys *System, h pool.Handle) func(path.Cell) uint8 {
	t.Helper()
	ws := handleRow(sys.sessions, int(h))
	if ws == nil || ws.session == nil {
		t.Fatalf("unit %d has no open search", h)
	}
	return ws.session.Config().PassableValue
}

// A search the batch opens reads its own requester's footprint as open and
// every other stale unit's as walled, and the shared layer walls both
// requesters; the retail scheduler's revision still opens its requester's
// footprint in the shared layer, where a later search reads it until the
// request's release [04 R-PATH-01 §14].
func TestBatchSearchReadsOnlyItsOwnFootprintAsOpen(t *testing.T) {
	sys, hs := ownFootprintFixture(t)
	sys.Rules = &ModernRules{}
	for slot, h := range hs {
		if search, _ := sys.pathProvider.OpenBatch(ownFootprintRequest(sys, h), 65536, slot); search == nil {
			t.Fatalf("unit %d's search finished at setup", h)
		}
	}
	a, b := ownFootprintRequest(sys, hs[0]).Start, ownFootprintRequest(sys, hs[1]).Start
	layer := sys.layerRegistry.Existing(sys.classKeyFor(hs[0]))
	if layer.Value(a.X, a.Z) != LayerBlocked || layer.Value(b.X, b.Z) != LayerBlocked {
		t.Fatal("the shared layer reads a stale requester's footprint as open")
	}
	viewA, viewB := ownFootprintView(t, sys, hs[0]), ownFootprintView(t, sys, hs[1])
	if viewA(a) != LayerClear || viewB(b) != LayerClear {
		t.Fatal("a batch search walls its own requester")
	}
	if viewA(b) != LayerBlocked || viewB(a) != LayerBlocked {
		t.Fatal("a batch search reads another stale unit's footprint as open")
	}

	sys, hs = ownFootprintFixture(t)
	sys.Rules = StrictRules{}
	for _, h := range hs {
		if work := sys.searchFunc(ownFootprintRequest(sys, h), 65536, 0); work.Done {
			t.Fatalf("unit %d's search finished at setup", h)
		}
	}
	layer = sys.layerRegistry.Existing(sys.classKeyFor(hs[0]))
	if layer.Value(a.X, a.Z) == LayerBlocked || layer.Value(b.X, b.Z) == LayerBlocked {
		t.Fatal("the retail revision no longer opens its requester's footprint in the shared layer")
	}
	if ownFootprintView(t, sys, hs[1])(a) == LayerBlocked {
		t.Fatal("a retail search no longer reads an unreleased requester's footprint as the shared layer does")
	}
}

// The own reading is the revision pass's restamp of the requester's
// rectangle under a current commit tick, anchor for anchor, with steep and
// blocked ground, other stale units and a larger footprint about it.
func TestOwnReadingMatchesTheRevisionRestamp(t *testing.T) {
	p := Profile{FootPrintX: 2, FootPrintZ: 2, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 30, BadWaterSlope: 15}
	terrain := layerTerrain(24, 24, 0)
	setDerived(terrain, 8, 13, 30, 60) // steep
	setDerived(terrain, 13, 9, 0, 200) // blocked
	grid := NewOccupancyGrid()
	sys := NewSystem(terrain, p, grid)
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)
	def := setScratchMovement(wiringDef(), p)
	var hs []pool.Handle
	for _, c := range [][2]int32{{10, 10}, {12, 12}, {8, 10}, {11, 14}} {
		x, z := world.CellToWorld(c[0])+8<<16, world.CellToWorld(c[1])+8<<16
		h, err := w.Create(def, 0, x, terrain.HeightAt(x, z), z)
		if err != nil {
			t.Fatal(err)
		}
		sys.EnsureUnit(w.Unit(h))
		hs = append(hs, h)
	}
	sys.BeginTick(200)
	reg := sys.ensureLayerRegistry()
	layer := reg.ReviseFor("", p, 0, 200) // every unit stale and walled
	own := sys.ownFootprintFor(hs[0], layer)
	if own == nil {
		t.Fatal("no own reading for a committed requester")
	}
	saved, _ := layer.CommitTick(hs[0])
	layer.NoteCommit(hs[0], 200)
	layer.RestampRect(own.minX, own.minZ, own.maxX, own.maxZ)
	want := map[path.Cell]uint8{}
	opened := 0
	for z := own.minZ; z <= own.maxZ; z++ {
		for x := own.minX; x <= own.maxX; x++ {
			want[path.Cell{X: x, Z: z}] = layer.Value(x, z)
		}
	}
	layer.NoteCommit(hs[0], saved)
	layer.RestampRect(own.minX, own.minZ, own.maxX, own.maxZ)
	for z := own.minZ; z <= own.maxZ; z++ {
		for x := own.minX; x <= own.maxX; x++ {
			c := path.Cell{X: x, Z: z}
			if got := layer.valueOwn(x, z, own); got != want[c] {
				t.Fatalf("anchor %v: own reading %d, restamp under a current tick %d", c, got, want[c])
			}
			if layer.Value(x, z) != want[c] {
				opened++
			}
		}
	}
	if opened == 0 {
		t.Fatal("the requester walls none of its own anchors; the comparison proves little")
	}
	if got := layer.valueOwn(own.maxX+1, own.minZ, own); got != layer.Value(own.maxX+1, own.minZ) {
		t.Fatal("the own reading reaches outside its rectangle")
	}
}
