package path

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
)

// These vectors are authored in schema order independently of the writer.
// All literals have explicit wire widths; no expected bytes read the fixture.
func pathCheckpointVector(t *testing.T, values ...any) []byte {
	t.Helper()
	var b bytes.Buffer
	for _, value := range values {
		if err := binary.Write(&b, binary.LittleEndian, value); err != nil {
			t.Fatal(err)
		}
	}
	return b.Bytes()
}

func collectPathCheckpoint(t *testing.T, s *Scheduler, c *CheckpointContext) {
	t.Helper()
	for passes := 0; passes < 10; passes++ {
		added, err := s.CollectCheckpointReferences(c)
		if err != nil {
			t.Fatal(err)
		}
		if added == 0 {
			return
		}
	}
	t.Fatal("discovery did not terminate")
}

func pathCheckpointBytes(t *testing.T, s *Scheduler, c *CheckpointContext) []byte {
	t.Helper()
	collectPathCheckpoint(t, s, c)
	var out bytes.Buffer
	if err := s.WriteCheckpoint(checkpoint.NewEncoder(&out), c); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestCheckpointSchedulerAndGoalVector(t *testing.T) {
	p := PointGoalRestored(Cell{-1, 2}, 3, -4)
	a := AnnulusGoalRestored(Cell{5, -6}, 7, 9, -8, 10)
	r := &rectGoal{rect: Rect{Min: Cell{-11, 12}, Max: Cell{13, -14}}}
	s := &Scheduler{base: -21, baseSet: true, callCount: 22, haveLast: true, playerCount: 3, playerCursor: 2, stepAllowance: -23, unitLimit: 24,
		active: &Request{Activation: 25, Goal: p, Player: 4, Start: Cell{-26, 27}, Unit: 28}, activePlayer: 4, activeScale: 29}
	s.accumulator[0], s.scales[1], s.serviceCount[9] = -30, 31, -32
	c := NewCheckpointContext()
	for _, goal := range []Goal{p, a, r} {
		if _, err := c.Goals.Add(goal); err != nil {
			t.Fatal(err)
		}
	}
	want := pathCheckpointVector(t,
		[10]int32{-30}, uint8(1), uint64(25), uint16(9), uint32(1), uint8(4), int32(-26), int32(27), uint32(28), int64(4), int32(29),
		int32(-21), uint8(1), uint32(22), uint8(1), uint32(0), int64(3), int64(2), uint8(0), uint8(0), [10]int32{0, 31}, uint8(0), [10]int32{0, 0, 0, 0, 0, 0, 0, 0, 0, -32}, int32(-23), int32(24),
		uint16(9), uint32(3),
		uint8(1), int32(-1), int32(2), int32(3), int32(-4),
		uint8(2), int32(5), int32(-6), int32(7), int32(-8), int32(9), int32(10),
		uint8(3), int32(13), int32(-14), int32(-11), int32(12),
		uint16(10), uint32(0))
	if got := pathCheckpointBytes(t, s, c); !bytes.Equal(got, want) {
		t.Fatalf("scheduler/goals vector\ngot  %x\nwant %x", got, want)
	}
}

func TestCheckpointSessionVector(t *testing.T) {
	c := NewCheckpointContext()
	g := PointGoal(Cell{}, 0)
	if _, err := c.Goals.Add(g); err != nil {
		t.Fatal(err)
	}
	s := &Session{
		cfg:  SearchConfig{Bounds: Rect{Min: Cell{-1, 2}, Max: Cell{3, -4}}, FootPrintX: 5, FootPrintZ: 6, Goal: g, HasBounds: true, Scale: 7, Start: Cell{-8, 9}, StartDir: 10},
		done: true, expanded: true, hasTolerance: true, haveNearest: true,
		nearest: Cell{-13, 14}, nearestDist: -15, notified: 16, popped: 17,
		resultPoints: []Point{{18, -19}}, resultStatus: 20, scale: -21, seeded: true, setupSteps: 22, tolerance: -23,
		entries: newMapIndex(),
	}
	s.entries.set(Cell{11, -12}, entry{dir: 3, node: 1, status: 5})
	s.entries.set(Cell{-24, 25}, entry{dir: 6, status: 8})
	s.ns = &NodeStore{index: &s.entries, scale: 26, nodes: []Node{{}, {Cell: Cell{11, -12}, Closed: true, Dir: 3, F: 27, G: -28, H: 29, Open: true, Run: 30, TerrainTerm: 31, hSet: true}}}
	s.heap = Heap{entries: []heapEntry{{id: 1, f: -32}}, positions: []int32{-1, 0}, spent: 1}
	want := pathCheckpointVector(t,
		uint32(0), // descriptor node sequence
		int32(3), int32(-4), int32(-1), int32(2), uint32(0), int32(5), int32(6), uint16(9), uint32(1), uint8(1), uint32(0), uint8(0), uint32(0), uint32(0), int32(7), int32(-8), int32(9), uint8(10),
		uint8(1), uint32(2), int32(11), int32(-12), uint8(3), int32(1), uint8(5), int32(-24), int32(25), uint8(6), int32(0), uint8(8),
		uint8(1), uint8(1), uint8(1), uint32(1), int32(1), int32(-32), int64(1), int32(-13), int32(14), int64(-15), uint32(16),
		uint8(1), uint32(2),
		// Reserved node zero, then allocated node 1 in lexical field order.
		int32(0), int32(0), uint8(0), uint8(0), int32(0), int32(0), int32(0), uint8(0), int64(0), uint16(0), uint16(0), uint8(0),
		int32(11), int32(-12), uint8(1), uint8(3), int32(27), int32(-28), int32(29), uint8(1), int64(0), uint16(30), uint16(31), uint8(1), int32(26),
		int64(17), uint32(1), int32(18), int32(-19), uint32(20), int32(-21), uint8(1), int64(22), int32(-23))
	var out bytes.Buffer
	e := checkpoint.NewEncoder(&out)
	writeSessionCheckpoint(e, c, s, CheckpointAccessors{}, "search")
	if err := e.Err(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), want) {
		t.Fatalf("session vector\ngot  %x\nwant %x", out.Bytes(), want)
	}
}

// A bucket open list is written after the node store, in pop order: the
// lowest key first and the most recent push first among equal keys, keys the
// buckets do not address included and stale entries kept (Nanolathe Modern
// bucket open list).
func TestCheckpointBucketSessionVector(t *testing.T) {
	c := NewCheckpointContext()
	g := PointGoal(Cell{}, 0)
	if _, err := c.Goals.Add(g); err != nil {
		t.Fatal(err)
	}
	s := &Session{cfg: SearchConfig{Goal: g, OpenList: OpenListBuckets}, entries: newMapIndex(), seeded: true}
	s.entries.set(Cell{1, 0}, entry{node: 1, status: 1})
	s.entries.set(Cell{2, 0}, entry{node: 2, status: 1})
	s.ns = &NodeStore{index: &s.entries, nodes: []Node{{}, {Cell: Cell{1, 0}, F: -1, Open: true}, {Cell: Cell{2, 0}, F: 7, Open: true}}}
	s.open.clear()
	s.open.push(2, 7)
	s.open.push(1, 7)
	s.open.push(1, -1)
	want := pathCheckpointVector(t,
		uint32(0),
		int32(0), int32(0), int32(0), int32(0), uint32(0), int32(0), int32(0), uint16(9), uint32(1), uint8(0), uint32(0), uint8(1), uint32(0), uint32(0), int32(0), int32(0), int32(0), uint8(0),
		uint8(0), uint32(2), int32(1), int32(0), uint8(0), int32(1), uint8(1), int32(2), int32(0), uint8(0), int32(2), uint8(1),
		uint8(0), uint8(0), uint8(0), uint32(0), int64(0), int32(0), int32(0), int64(0), uint32(0),
		uint8(1), uint32(3),
		int32(0), int32(0), uint8(0), uint8(0), int32(0), int32(0), int32(0), uint8(0), int64(0), uint16(0), uint16(0), uint8(0),
		int32(1), int32(0), uint8(0), uint8(0), int32(-1), int32(0), int32(0), uint8(1), int64(0), uint16(0), uint16(0), uint8(0),
		int32(2), int32(0), uint8(0), uint8(0), int32(7), int32(0), int32(0), uint8(1), int64(0), uint16(0), uint16(0), uint8(0), int32(0),
		uint32(3), int32(1), int32(-1), int32(1), int32(7), int32(2), int32(7),
		int64(0), uint32(0), uint32(0), int32(0), uint8(1), int64(0), int32(0))
	var out bytes.Buffer
	e := checkpoint.NewEncoder(&out)
	writeSessionCheckpoint(e, c, s, CheckpointAccessors{}, "search")
	if err := e.Err(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), want) {
		t.Fatalf("bucket session vector\ngot  %x\nwant %x", out.Bytes(), want)
	}
	// A heap search may not carry bucket entries, nor a bucket search heap
	// entries.
	s.heap = Heap{entries: []heapEntry{{id: 1, f: -1}}, positions: []int32{-1, 0}}
	e = checkpoint.NewEncoder(io.Discard)
	writeSessionCheckpoint(e, c, s, CheckpointAccessors{}, "search")
	if e.Err() == nil {
		t.Fatal("a bucket search with heap entries was written")
	}
}

func TestCheckpointAccessorVector(t *testing.T) {
	a := CheckpointAccessor{Kind: 7, ClaimCounts: 11, ClaimOwn: 12, Owner: 2, Serial: 13, Width: 14, Height: 15, FootprintX: 16, FootprintZ: 17, Per: -18, Against: 19, Row: 3}
	want := pathCheckpointVector(t,
		int32(19), [4]int32{}, uint32(11), uint32(12), int32(16), int32(17), int32(15), uint32(0), uint8(7), uint32(0), uint32(0), uint8(2), int32(-18), [8]int32{}, uint32(0), uint8(3), uint32(13), [2]int32{}, uint8(0), uint32(0), uint8(0), int32(14))
	var out bytes.Buffer
	e := checkpoint.NewEncoder(&out)
	writeAccessorCheckpoint(e, a, "accessor")
	if err := e.Err(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), want) {
		t.Fatalf("accessor vector\ngot  %x\nwant %x", out.Bytes(), want)
	}
}

// The own class view of a Modern path batch search writes the class view's
// operands and its inclusive Bounds in the fixed slots, Max before Min.
func TestCheckpointOwnClassAccessorVector(t *testing.T) {
	a := CheckpointAccessor{Kind: 9, Layer: 5, Requester: 6, Owner: 2, Profile: [8]int32{1, 2, 3, 4, 5, 6, 7, 8}, Tick: 7, FootprintX: 2, FootprintZ: 3,
		Bounds: Rect{Min: Cell{-1, 4}, Max: Cell{3, 9}}}
	want := pathCheckpointVector(t,
		int32(0), [4]int32{3, 9, -1, 4}, uint32(0), uint32(0), int32(2), int32(3), int32(0), uint32(0), uint8(9), uint32(5), uint32(0), uint8(2), int32(0), [8]int32{1, 2, 3, 4, 5, 6, 7, 8}, uint32(6), uint8(0), uint32(0), [2]int32{}, uint8(0), uint32(7), uint8(0), int32(0))
	var out bytes.Buffer
	e := checkpoint.NewEncoder(&out)
	writeAccessorCheckpoint(e, a, "accessor")
	if err := e.Err(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), want) {
		t.Fatalf("own class accessor vector\ngot  %x\nwant %x", out.Bytes(), want)
	}
	if err := validateCheckpointAccessors(SearchConfig{PassableValue: func(Cell) uint8 { panic("passability called") }}, CheckpointAccessors{Nodes: []CheckpointAccessor{a}, Passable: 1}); err != nil {
		t.Fatalf("own class view refused as a passability root: %v", err)
	}
}

func checkpointFixture(t *testing.T, ws *Workspace) (*Scheduler, *CheckpointContext, *Session) {
	t.Helper()
	cfg := SearchConfig{Goal: PointGoal(Cell{10, 10}, 0), PassableValue: func(Cell) uint8 { return 3 }, HasBounds: true, Bounds: Rect{Max: Cell{12, 12}}, Workspace: ws}
	s := NewSession(cfg)
	s.Resume(2)
	c := NewCheckpointContext()
	if err := c.SetAccessors(s, CheckpointAccessors{Nodes: []CheckpointAccessor{{Kind: 1, Layer: 1}}, Passable: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Searches.Add(s); err != nil {
		t.Fatal(err)
	}
	return &Scheduler{}, c, s
}

func pathCheckpointDigests(t *testing.T, s *Scheduler, c *CheckpointContext) checkpoint.Digests {
	t.Helper()
	collectPathCheckpoint(t, s, c)
	capture, err := checkpoint.NewCapture(checkpoint.Identity{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for id := checkpoint.Owner(1); id <= checkpoint.OwnerCount; id++ {
		e, err := capture.Section(id, id == 8)
		if err != nil {
			t.Fatal(err)
		}
		if id == 8 {
			if err := s.WriteCheckpoint(e, c); err != nil {
				t.Fatal(err)
			}
		}
	}
	d, err := capture.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestCheckpointRetainedFieldsChangeOwnerAndFullDigests(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Scheduler, *CheckpointContext, *Session)
	}{
		{"scheduler accumulator", func(q *Scheduler, _ *CheckpointContext, _ *Session) { q.accumulator[9]++ }},
		{"scheduler quantum", func(q *Scheduler, _ *CheckpointContext, _ *Session) { q.scales[8]++ }},
		{"scheduler service count", func(q *Scheduler, _ *CheckpointContext, _ *Session) { q.serviceCount[7]++ }},
		{"scheduler base presence", func(q *Scheduler, _ *CheckpointContext, _ *Session) { q.baseSet = true }},
		{"scheduler last presence", func(q *Scheduler, _ *CheckpointContext, _ *Session) { q.haveLast = true }},
		{"goal threshold", func(_ *Scheduler, _ *CheckpointContext, s *Session) { s.cfg.Goal.(*pointGoal).radiusSq++ }},
		{"config stored scale", func(_ *Scheduler, _ *CheckpointContext, s *Session) { s.cfg.Scale++ }},
		{"session stored scale", func(_ *Scheduler, _ *CheckpointContext, s *Session) { s.scale++ }},
		{"node store scale", func(_ *Scheduler, _ *CheckpointContext, s *Session) { s.ns.scale++ }},
		{"bounds despite presence", func(_ *Scheduler, _ *CheckpointContext, s *Session) { s.cfg.Bounds.Max.X++ }},
		{"nearest threshold", func(_ *Scheduler, _ *CheckpointContext, s *Session) { s.nearestDist++ }},
		{"tolerance", func(_ *Scheduler, _ *CheckpointContext, s *Session) { s.tolerance++ }},
		{"notification", func(_ *Scheduler, _ *CheckpointContext, s *Session) { s.notified++ }},
		{"popped", func(_ *Scheduler, _ *CheckpointContext, s *Session) { s.popped++ }},
		{"setup", func(_ *Scheduler, _ *CheckpointContext, s *Session) { s.setupSteps++ }},
		{"ray-only entry", func(_ *Scheduler, _ *CheckpointContext, s *Session) {
			s.entries.set(Cell{-50, 50}, entry{dir: 3, status: 8})
		}},
		{"terminal-only entry", func(_ *Scheduler, _ *CheckpointContext, s *Session) {
			s.entries.set(Cell{-50, 50}, entry{dir: 255, status: 4})
		}},
		{"status-zero direction", func(_ *Scheduler, _ *CheckpointContext, s *Session) { s.entries.set(Cell{-50, 50}, entry{dir: 3}) }},
		{"node terrain", func(_ *Scheduler, _ *CheckpointContext, s *Session) { s.ns.nodes[1].TerrainTerm++ }},
		{"node run", func(_ *Scheduler, _ *CheckpointContext, s *Session) { s.ns.nodes[1].Run++ }},
		{"node heuristic latch", func(_ *Scheduler, _ *CheckpointContext, s *Session) { s.ns.nodes[1].hSet = !s.ns.nodes[1].hSet }},
		{"heap key", func(_ *Scheduler, _ *CheckpointContext, s *Session) { s.heap.entries[0].f++ }},
		{"stored result", func(_ *Scheduler, _ *CheckpointContext, s *Session) { s.resultPoints = []Point{{1, 2}} }},
		{"captured tick", func(_ *Scheduler, c *CheckpointContext, s *Session) {
			a := c.accessors[s]
			a.Nodes[0].Tick++
			c.accessors[s] = a
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, c, s := checkpointFixture(t, nil)
			before := pathCheckpointDigests(t, q, c)
			tc.mutate(q, c, s)
			after := pathCheckpointDigests(t, q, c)
			if before.Full == after.Full || before.Owners[7] == after.Owners[7] {
				t.Fatal("retained field did not change paths and full digest")
			}
			for i := range before.Owners {
				if i != 7 && before.Owners[i] != after.Owners[i] {
					t.Fatalf("changed unrelated owner %d", i+1)
				}
			}
		})
	}
}

func TestCheckpointStorageOrderExclusionsAndPurity(t *testing.T) {
	q1, c1, s1 := checkpointFixture(t, nil)
	q2, c2, s2 := checkpointFixture(t, &Workspace{})
	// Entries without nodes exist both inside dense storage and in overflow.
	for _, s := range []*Session{s1, s2} {
		s.entries.set(Cell{-100, 50}, entry{status: 4, dir: 255})
		s.entries.set(Cell{100, -50}, entry{status: 8, dir: 2})
	}
	before := pathCheckpointBytes(t, q1, c1)
	if got := pathCheckpointBytes(t, q2, c2); !bytes.Equal(got, before) {
		t.Fatal("sparse/dense output differs")
	}
	q2.activeReq = Request{Activation: 999, Goal: PointGoal(Cell{99, 99}, 99)}
	q2.activePlayer, q2.activeScale = 3, 999
	q2.traceEnabled, q2.traceDropped, q2.traceLimit = true, true, 20
	q2.traces = []Trace{{Tick: 99}}
	q2.sweepPolls[1] = 99
	s2.fan = Fan{Len: 8}
	s2.entries.ws.gen += 12 // index generation, not allocator current generation, governs reads
	s2.entries.ws.lent = false
	for i, slot := range s2.entries.ws.slots {
		if slot.gen != s2.entries.gen {
			s2.entries.ws.slots[i] = cellSlot{gen: s2.entries.gen + 30, e: entry{status: 8, dir: 2}}
			break
		}
	}
	s2.heap.positions = append(s2.heap.positions, -1, -1)
	// Callbacks would fail the test if collection or serialization invoked them.
	s2.cfg.PassableValue = func(Cell) uint8 { t.Fatal("capture called passability"); return 0 }
	if got := pathCheckpointBytes(t, q2, c2); !bytes.Equal(got, before) {
		t.Fatal("excluded scratch changed output")
	}
	if got := pathCheckpointBytes(t, q2, c2); !bytes.Equal(got, before) {
		t.Fatal("repeated capture changed state")
	}
	// Reinsert sparse keys in reverse order; the writer must sort before reads.
	entries, err := checkpointEntries(&s1.entries)
	if err != nil {
		t.Fatal(err)
	}
	s1.entries.m = make(map[Cell]entry)
	for i := len(entries) - 1; i >= 0; i-- {
		s1.entries.m[entries[i].cell] = entries[i].value
	}
	s1.entries.m[Cell{500, 500}] = entry{}
	if got := pathCheckpointBytes(t, q1, c1); !bytes.Equal(got, before) {
		t.Fatal("map insertion order or zero entry changed bytes")
	}
}

func TestCheckpointHeapArrayOrderAndNodeAllocationOrder(t *testing.T) {
	q, c, s := checkpointFixture(t, nil)
	before := pathCheckpointBytes(t, q, c)
	if len(s.heap.entries) < 2 {
		t.Fatal("fixture needs heap siblings")
	}
	s.heap.entries[0], s.heap.entries[1] = s.heap.entries[1], s.heap.entries[0]
	s.heap.positions[s.heap.entries[0].id], s.heap.positions[s.heap.entries[1].id] = 0, 1
	if got := pathCheckpointBytes(t, q, c); bytes.Equal(got, before) {
		t.Fatal("heap array order was normalized")
	}
	// Allocation order is an identity, independent of geometric key order.
	before = pathCheckpointBytes(t, q, c)
	s.ns.nodes[1].G++
	s.ns.nodes[2].G--
	if got := pathCheckpointBytes(t, q, c); bytes.Equal(got, before) {
		t.Fatal("allocated node records were collapsed")
	}
}

type checkpointUnknownGoal struct{}

func (*checkpointUnknownGoal) Enumerate([]Cell) []Cell  { panic("unknown goal executed") }
func (*checkpointUnknownGoal) StartSatisfied(Cell) bool { panic("unknown goal executed") }
func (*checkpointUnknownGoal) H(Cell) int32             { panic("unknown goal executed") }

type checkpointUnknownSearch struct{ Search }

type checkpointUnknownKernel struct{}

func (*checkpointUnknownKernel) NewSession(SearchConfig) Search { panic("kernel executed") }

func TestCheckpointKernelKindsAreClosedAndReadOnly(t *testing.T) {
	for _, tc := range []struct {
		kernel Kernel
		kind   uint8
	}{
		{nil, 1}, {RetailKernel{}, 1}, {&RetailKernel{}, 1},
		{StraightenKernel{}, 2}, {&StraightenKernel{}, 2}, {SmoothKernel{}, 3}, {&SmoothKernel{}, 3},
	} {
		kind, err := CheckpointKernelKind(tc.kernel)
		if err != nil || kind != tc.kind {
			t.Fatalf("kernel %T = (%d,%v), want %d", tc.kernel, kind, err, tc.kind)
		}
	}
	for _, kernel := range []Kernel{(*RetailKernel)(nil), (*StraightenKernel)(nil), (*SmoothKernel)(nil), &checkpointUnknownKernel{}} {
		if kind, err := CheckpointKernelKind(kernel); err == nil || kind != 0 {
			t.Fatalf("accepted kernel %T", kernel)
		}
	}
}

func TestCheckpointRefusesUnknownAndTypedNilValuesWithoutCalls(t *testing.T) {
	for _, g := range []Goal{&checkpointUnknownGoal{}, (*pointGoal)(nil), (*annulusGoal)(nil), (*rectGoal)(nil)} {
		q := &Scheduler{active: &Request{Goal: g}}
		c := NewCheckpointContext()
		if _, err := q.CollectCheckpointReferences(c); err == nil {
			t.Fatalf("accepted goal %T", g)
		}
		if err := q.WriteCheckpoint(checkpoint.NewEncoder(io.Discard), c); err == nil {
			t.Fatalf("writer accepted goal %T", g)
		}
	}
	for _, s := range []Search{&checkpointUnknownSearch{}, (*Session)(nil), (*straightenSearch)(nil), (*smoothSearch)(nil)} {
		c := NewCheckpointContext()
		if err := c.SetAccessors(s, CheckpointAccessors{}); err == nil {
			t.Fatalf("registered search %T", s)
		}
		if _, err := c.Searches.Add(s); err != nil {
			t.Fatal(err)
		}
		if _, err := (&Scheduler{}).CollectCheckpointReferences(c); err == nil {
			t.Fatalf("collected search %T", s)
		}
		if err := (&Scheduler{}).WriteCheckpoint(checkpoint.NewEncoder(io.Discard), c); err == nil {
			t.Fatalf("wrote search %T", s)
		}
	}
}

type checkpointUnknownProvider struct{}

func (*checkpointUnknownProvider) PlayerCount() int               { panic("provider executed") }
func (*checkpointUnknownProvider) UnitLimit() int32               { panic("provider executed") }
func (*checkpointUnknownProvider) Eligible(int) bool              { panic("provider executed") }
func (*checkpointUnknownProvider) Poll(int) (Request, PollResult) { panic("provider executed") }

func TestCheckpointRefusesUnattestedBindings(t *testing.T) {
	for _, q := range []*Scheduler{
		{search: func(Request, int32, int) WorkResult { panic("search called") }},
		{publish: func(Request, []Point, Status) { panic("publish called") }},
		{provider: &checkpointUnknownProvider{}},
		{provider: (*checkpointUnknownProvider)(nil)},
	} {
		c := NewCheckpointContext()
		if _, err := q.CollectCheckpointReferences(c); err == nil || !strings.Contains(err.Error(), "unattested") {
			t.Fatalf("collector error = %v", err)
		}
		if err := q.WriteCheckpoint(checkpoint.NewEncoder(io.Discard), c); err == nil {
			t.Fatal("writer accepted binding")
		}
	}
}

func TestCheckpointRefusesUndiscoveredAndInvalidDerivedState(t *testing.T) {
	if err := (&Scheduler{active: &Request{Goal: PointGoal(Cell{}, 0)}}).WriteCheckpoint(checkpoint.NewEncoder(io.Discard), NewCheckpointContext()); err == nil {
		t.Fatal("writer discovered goal")
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Session)
	}{
		{"index alias", func(s *Session) { other := newMapIndex(); s.ns.index = &other }},
		{"node back-reference", func(s *Session) { s.entries.set(s.ns.nodes[1].Cell, entry{}) }},
		{"entry node", func(s *Session) { s.entries.set(Cell{-1, -1}, entry{node: 999}) }},
		{"node parent", func(s *Session) { s.ns.nodes[1].Parent = 999 }},
		{"heap position", func(s *Session) { s.heap.positions[s.heap.entries[0].id] = 999 }},
		{"heap duplicate", func(s *Session) { s.heap.entries = append(s.heap.entries, s.heap.entries[0]) }},
		{"spent node", func(s *Session) { s.heap.spent = 999 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, c, s := checkpointFixture(t, nil)
			collectPathCheckpoint(t, q, c)
			tc.mutate(s)
			if err := q.WriteCheckpoint(checkpoint.NewEncoder(io.Discard), c); err == nil {
				t.Fatal("accepted invalid derived state")
			}
		})
	}
}

type checkpointBrokenWriter struct{ err error }

func (w checkpointBrokenWriter) Write([]byte) (int, error) { return 0, w.err }

func TestCheckpointWriterFailureIsSticky(t *testing.T) {
	q, c, _ := checkpointFixture(t, nil)
	collectPathCheckpoint(t, q, c)
	failed := errors.New("authored sink failure")
	e := checkpoint.NewEncoder(checkpointBrokenWriter{failed})
	if err := q.WriteCheckpoint(e, c); !errors.Is(err, failed) {
		t.Fatalf("lost sink error: %v", err)
	}
	if err := q.WriteCheckpoint(checkpoint.NewEncoder(checkpointBrokenWriter{}), c); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write error: %v", err)
	}
}

func TestCheckpointSchedulerSummaryVectorAndBlindSpot(t *testing.T) {
	q := &Scheduler{base: -1, baseSet: true, callCount: 2, playerCursor: 3, stepAllowance: 4, active: &Request{Player: 5, Unit: 6, Start: Cell{-7, 8}, Activation: 9}}
	q.scales[0], q.serviceCount[0], q.accumulator[0] = 10, -11, 12
	var out checkpoint.Summary
	if err := q.AppendCheckpointSummary(&out); err != nil {
		t.Fatal(err)
	}
	words, sum := out.Result()
	// 42 words, the last the empty Modern batch's count. Independently
	// indexed nonzero terms, with signed extension.
	want := uint64(0)
	for _, v := range []struct {
		index uint64
		value int64
	}{{1, -1}, {2, 1}, {3, 10}, {13, 2}, {14, 3}, {15, 4}, {16, -11}, {26, 12}, {36, 1}, {37, 5}, {38, 6}, {39, -7}, {40, 8}, {41, 9}} {
		want += v.index * uint64(v.value)
	}
	if words != 42 || sum != want {
		t.Fatalf("summary = (%d,%d), want (42,%d)", words, sum, want)
	}
	q.active.Goal = PointGoal(Cell{99, 99}, 99)
	q.activeScale++ // deliberately unlisted in the cheap summary
	q.search = func(Request, int32, int) WorkResult { panic("summary called search") }
	q.publish = func(Request, []Point, Status) { panic("summary called publisher") }
	q.provider = &checkpointUnknownProvider{}
	var changed checkpoint.Summary
	if err := q.AppendCheckpointSummary(&changed); err != nil {
		t.Fatal(err)
	}
	if w, s := changed.Result(); w != words || s != sum {
		t.Fatal("summary changed for an excluded field")
	}
}

func TestCheckpointSearchSummaryVectorsAndBlindSpots(t *testing.T) {
	base := &Session{
		cfg: checkpointAllCallbacks(), popped: 7, setupSteps: -8, expanded: true,
		ns:   &NodeStore{nodes: []Node{{}, {}, {}, {}}},
		heap: Heap{entries: []heapEntry{{id: 1}, {id: 2}}},
	}
	straight := &straightenSearch{Search: base, probes: 11, cfg: checkpointAllCallbacks()}
	smooth := &smoothSearch{Search: straight, probes: 13, cfg: checkpointAllCallbacks()}
	for _, tc := range []struct {
		name   string
		search Search
		popped int64
	}{
		{"retail", base, 7},
		{"straight", straight, 18},
		{"smooth", smooth, 31},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out checkpoint.Summary
			out.Word(4) // Words continue from the scheduler's existing accumulator.
			if err := AppendSearchCheckpointSummary(tc.search, &out); err != nil {
				t.Fatal(err)
			}
			want := uint64(4 + 2 + 5 + 6*3 + 7*2)
			setup := int64(-8)
			want += 3*uint64(tc.popped) + 4*uint64(setup)
			if words, sum := out.Result(); words != 7 || sum != want {
				t.Fatalf("summary = (%d,%d), want (7,%d)", words, sum, want)
			}
		})
	}
	var before checkpoint.Summary
	if err := AppendSearchCheckpointSummary(smooth, &before); err != nil {
		t.Fatal(err)
	}
	base.ns.nodes[1].F = 99
	base.heap.entries[0].f = 100
	base.tolerance = 101
	smooth.out = []Point{{102, 103}}
	var after checkpoint.Summary
	if err := AppendSearchCheckpointSummary(smooth, &after); err != nil {
		t.Fatal(err)
	}
	bw, bs := before.Result()
	aw, as := after.Result()
	if aw != bw || as != bs {
		t.Fatal("unselected search state changed summary")
	}
	for _, search := range []Search{nil, &Session{}, &Session{ns: &NodeStore{}}, &Session{ns: &NodeStore{nodes: []Node{{}}}}} {
		var out checkpoint.Summary
		if err := AppendSearchCheckpointSummary(search, &out); err != nil {
			t.Fatal(err)
		}
		words, sum := out.Result()
		if search == nil {
			if words != 1 || sum != 0 {
				t.Fatalf("absent summary = (%d,%d)", words, sum)
			}
		} else if words != 6 || sum != 1 {
			t.Fatalf("empty search summary = (%d,%d)", words, sum)
		}
	}
	// A bucket open list's entry count, stale entries included, stands where
	// the heap's does (Nanolathe Modern bucket open list).
	buckets := &Session{cfg: checkpointAllCallbacks(), popped: 7, setupSteps: -8, expanded: true, ns: &NodeStore{nodes: []Node{{}, {}, {}, {}}}}
	buckets.cfg.OpenList = OpenListBuckets
	buckets.open.clear()
	buckets.open.push(1, 5)
	buckets.open.push(1, 3)
	var heapSummary, bucketSummary checkpoint.Summary
	if err := AppendSearchCheckpointSummary(base, &heapSummary); err != nil {
		t.Fatal(err)
	}
	if err := AppendSearchCheckpointSummary(buckets, &bucketSummary); err != nil {
		t.Fatal(err)
	}
	hw, hs := heapSummary.Result()
	bw2, bs2 := bucketSummary.Result()
	if hw != bw2 || hs != bs2 {
		t.Fatalf("bucket summary = (%d,%d), want the two-entry heap's (%d,%d)", bw2, bs2, hw, hs)
	}
}

func TestCheckpointSearchSummaryRefusalsAreAtomic(t *testing.T) {
	cycle := &smoothSearch{}
	cycle.Search = cycle
	a, b := &straightenSearch{}, &smoothSearch{}
	a.Search, b.Search = b, a
	for _, search := range []Search{
		&checkpointUnknownSearch{}, (*Session)(nil), (*straightenSearch)(nil), (*smoothSearch)(nil),
		&straightenSearch{}, &smoothSearch{Search: &checkpointUnknownSearch{}},
		&smoothSearch{Search: (*Session)(nil)}, cycle, a,
	} {
		var out checkpoint.Summary
		out.Word(99)
		if err := AppendSearchCheckpointSummary(search, &out); err == nil {
			t.Fatalf("accepted search %T", search)
		}
		if words, sum := out.Result(); words != 1 || sum != 99 {
			t.Fatalf("failed summary wrote (%d,%d)", words, sum)
		}
	}
	if err := AppendSearchCheckpointSummary(nil, nil); err == nil {
		t.Fatal("accepted absent accumulator")
	}
}
