package orders

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func orderCheckpointContext(t *testing.T, roots ...*units.Unit) *CheckpointContext {
	t.Helper()
	c := NewCheckpointContext(units.NewCheckpointContext(nil))
	for _, u := range roots {
		if _, err := c.Units.Allocations.Add(u); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func collectOrdersCheckpoint(t *testing.T, c *CheckpointContext) {
	t.Helper()
	p := &Pump{}
	for {
		added, err := p.CollectCheckpointReferences(c)
		if err != nil {
			t.Fatal(err)
		}
		if added == 0 {
			return
		}
	}
}

func orderCheckpointBytes(t *testing.T, c *CheckpointContext) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := (&Pump{}).WriteCheckpoint(checkpoint.NewEncoder(&out), c); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func orderCheckpointDigests(t *testing.T, roots ...*units.Unit) checkpoint.Digests {
	t.Helper()
	c := orderCheckpointContext(t, roots...)
	collectOrdersCheckpoint(t, c)
	capture, err := checkpoint.NewCapture(checkpoint.Identity{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for owner := checkpoint.OwnerRuntime; owner <= checkpoint.OwnerComputersScenario; owner++ {
		e, err := capture.Section(owner, owner == checkpoint.OwnerOrders)
		if err != nil {
			t.Fatal(err)
		}
		if owner == checkpoint.OwnerOrders {
			if err := (&Pump{}).WriteCheckpoint(e, c); err != nil {
				t.Fatal(err)
			}
		}
	}
	digests, err := capture.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return digests
}

// Discovery preserves pointer identity even when a retired allocation has the
// same raw handle as a current one. References never resolve through the world.
func TestCheckpointOrderGraphRetainsDetachedAndRetiredObjects(t *testing.T) {
	detached, first, firing, tail, old := &Node{}, &Node{}, &Node{}, &Node{}, &Node{}
	retiredQueue := &Queue{primary: []*Node{old}}
	retired := &units.Unit{Handle: 2, AllocationSerial: 2, Orders: retiredQueue}
	replacement := &units.Unit{Handle: 2, AllocationSerial: 3, Alive: true}
	q := &Queue{primary: []*Node{first, tail, first}, secondary: []*Node{tail}}
	u := &units.Unit{Handle: 1, AllocationSerial: 1, Alive: true, Orders: q}
	q.danger.contacts[0].unit = retired
	q.danger.contacts[1].unit = retired
	q.danger.response, q.danger.resume, q.danger.returnMove = detached, first, detached
	q.firingPosition = firingPositionState{node: firing, owner: u, target: retired}
	c := orderCheckpointContext(t, u, replacement)
	for pass, want := range []int{6, 2, 0} {
		got, err := (&Pump{}).CollectCheckpointReferences(c)
		if err != nil || got != want {
			t.Fatalf("pass %d: added %d, err %v; want %d", pass, got, err, want)
		}
	}
	for i, want := range []*units.Unit{u, replacement, retired} {
		if id, ok := c.Units.Allocations.Find(want); !ok || id != checkpoint.ObjectID(i+1) {
			t.Fatalf("allocation %d: id %d, known %v", i, id, ok)
		}
	}
	for i, want := range []*Node{detached, first, firing, tail, old} {
		if id, ok := c.Nodes.Find(want); !ok || id != checkpoint.ObjectID(i+1) {
			t.Fatalf("node %d: id %d, known %v", i, id, ok)
		}
	}
	// Three allocation roots: the current unqueued allocation remains present
	// with queue ID zero, followed by the retired allocation's own queue.
	wantRoots := checkpointHex(t, `03000000
		0100 01000000 0200 01000000
		0100 02000000 0200 00000000
		0100 03000000 0200 02000000
		0200 02000000`)
	got := orderCheckpointBytes(t, c)
	if !bytes.HasPrefix(got, wantRoots) {
		t.Fatalf("root/table framing = %x; want prefix %x", got[:len(wantRoots)], wantRoots)
	}
}

func TestCheckpointOrderEmptyTablesAndAbsentQueue(t *testing.T) {
	empty := orderCheckpointContext(t)
	want := checkpointHex(t, `00000000 0200 00000000 0300 00000000`)
	if got := orderCheckpointBytes(t, empty); !bytes.Equal(got, want) {
		t.Fatalf("empty orders = %x; want %x", got, want)
	}
	u := &units.Unit{}
	c := orderCheckpointContext(t, u)
	collectOrdersCheckpoint(t, c)
	want = checkpointHex(t, `01000000 0100 01000000 0200 00000000 0200 00000000 0300 00000000`)
	if got := orderCheckpointBytes(t, c); !bytes.Equal(got, want) || u.Orders != nil {
		t.Fatalf("absent queue = %x; want %x, Orders = %v", got, want, u.Orders)
	}
}

// This authored byte vector independently fixes source-field lexical order,
// signed values, Fixed's full int64 width, and the schema's u32 raw handles.
func TestCheckpointOrderNodeWireVector(t *testing.T) {
	n := &Node{
		BuildDefKey: "x", BuildFacing: 2, CachedX: -2, CachedY: 0x1122,
		CaptionPending: true, CreationTick: 0x01020304, Deadline: -1,
		DynamicGate: 0x11121314, Flags: 0x21222324,
		GoalX: 1<<40 + 0x31323334, GoalY: -2, GoalZ: 0x41424344,
		GuardX: -3, GuardY: 0x5152, HumanMoveSequence: 0x6162636465666768,
		ID: 0x71, MoveState: 0x72, Owner: 0x8182,
		Param1: 0x91929394, Param2: 0xa1a2a3a4, Param3: 0xb1b2b3b4,
		PathStatus: 0xc1c2c3c4, Phase: 0xd1, Satisfied: 0xe1e2e3e4,
		StaticGate: 0xf1f2f3f4, Target: 0x1516,
		automaticAttack: true, automaticWork: false,
		crowdedArrival: crowdedArrivalState{active: true, goalX: -4,
			goalZ: 1<<40 + 0x25262728, lastTick: 0x35363738, since: 0x45464748,
			x: -5, z: 0x55565758},
		nextAutomaticTargetTick: 0x65666768,
	}
	want := checkpointHex(t, `
		01000000 78 02 feff 2211 01 04030201 ffffffff 14131211 24232221
		3433323100010000 feffffffffffffff 4443424100000000
		fdff 5251 6867666564636261 71 72 82810000
		94939291 a4a3a2a1 b4b3b2b1 c4c3c2c1 d1 e4e3e2e1 f4f3f2f1 16150000
		01 00 01 fcffffffffffffff 2827262500010000 38373635 48474645
		fbffffff 58575655 68676665`)
	var out bytes.Buffer
	e := checkpoint.NewEncoder(&out)
	writeNodeCheckpoint(e, n, "test.node")
	if err := e.Err(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), want) {
		t.Fatalf("node bytes\n got %x\nwant %x", out.Bytes(), want)
	}
}

func checkpointHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(strings.Join(strings.Fields(value), ""))
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestCheckpointOrderAliasesAndSequence(t *testing.T) {
	makeGraph := func() (*units.Unit, *Queue) {
		a, b := &Node{ID: 1}, &Node{ID: 2}
		q := &Queue{primary: []*Node{a, b}, secondary: []*Node{a}}
		q.danger.response = a
		return &units.Unit{Orders: q}, q
	}
	u, q := makeGraph()
	base := orderCheckpointDigests(t, u)
	separate, _ := makeGraph()
	if got := orderCheckpointDigests(t, separate); got != base {
		t.Fatal("allocation addresses changed the checkpoint")
	}
	assertChanged := func(name string) {
		t.Helper()
		got := orderCheckpointDigests(t, u)
		if got.Full == base.Full || got.Owners[checkpoint.OwnerOrders-1] == base.Owners[checkpoint.OwnerOrders-1] {
			t.Fatalf("%s did not change the full and order digests", name)
		}
		for owner := range got.Owners {
			if owner != int(checkpoint.OwnerOrders-1) && got.Owners[owner] != base.Owners[owner] {
				t.Fatalf("%s changed unrelated owner %d", name, owner+1)
			}
		}
	}
	q.primary[0], q.primary[1] = q.primary[1], q.primary[0]
	assertChanged("primary order")
	q.primary[0], q.primary[1] = q.primary[1], q.primary[0]
	copy := *q.secondary[0]
	q.secondary[0] = &copy
	assertChanged("shared node replaced by equal body")
	q.secondary[0] = q.primary[0]
	q.primary, q.secondary = q.secondary, q.primary
	assertChanged("primary/secondary partition")

	sharedQueue := &Queue{}
	a, b := &units.Unit{Orders: sharedQueue}, &units.Unit{Orders: sharedQueue}
	shared := orderCheckpointDigests(t, a, b)
	b.Orders = &Queue{}
	if got := orderCheckpointDigests(t, a, b); got.Full == shared.Full {
		t.Fatal("shared queue replaced by equal body did not change the checkpoint")
	}
}

func TestCheckpointOrderRetainedMutation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Queue, *Node)
	}{
		{"deadline", func(_ *Queue, n *Node) { n.Deadline = -1 }},
		{"build definition", func(_ *Queue, n *Node) { n.BuildDefKey = "armtest" }},
		{"high fixed bits", func(_ *Queue, n *Node) { n.GoalX = numeric.Fixed(1 << 40) }},
		{"human receipt", func(_ *Queue, n *Node) { n.HumanMoveSequence = 7 }},
		{"automatic attack", func(_ *Queue, n *Node) { n.automaticAttack = true }},
		{"automatic work", func(_ *Queue, n *Node) { n.automaticWork = true }},
		{"next target", func(_ *Queue, n *Node) { n.nextAutomaticTargetTick = 7 }},
		{"crowded inactive residual", func(_ *Queue, n *Node) { n.crowdedArrival.goalZ = 9 }},
		{"pump tick", func(q *Queue, _ *Node) { q.lastPumpTick = 7 }},
		{"danger invalid impact", func(q *Queue, _ *Node) { q.danger.impacts[3].sector = 3 }},
		{"danger failed contact", func(q *Queue, _ *Node) { q.danger.contacts[3].failedUntil = 7 }},
		{"danger contact handle", func(q *Queue, _ *Node) { q.danger.contacts[0].handle = pool.Handle(7) }},
		{"danger anchor", func(q *Queue, _ *Node) { q.danger.anchorY = 9 }},
		{"danger deadline", func(q *Queue, _ *Node) { q.danger.quietUntil = 7 }},
		{"danger opportunity", func(q *Queue, _ *Node) { q.danger.opportunityTarget = 7 }},
		{"firing inactive residual", func(q *Queue, _ *Node) { q.firingPosition.started = 7 }},
		{"firing exhausted", func(q *Queue, _ *Node) { q.firingPosition.exhausted = true }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &Node{}
			q := &Queue{primary: []*Node{n}}
			u := &units.Unit{Orders: q}
			before := orderCheckpointDigests(t, u)
			tt.mutate(q, n)
			after := orderCheckpointDigests(t, u)
			if before.Full == after.Full || before.Owners[checkpoint.OwnerOrders-1] == after.Owners[checkpoint.OwnerOrders-1] {
				t.Fatal("retained mutation did not change the full and order digests")
			}
		})
	}
}

func TestCheckpointOrderExclusionsAndCapturePurity(t *testing.T) {
	n := &Node{ID: 1, GoalX: 17, Flags: 19}
	q := &Queue{primary: []*Node{n}, secondary: []*Node{n}, lastPumpTick: 23}
	u := &units.Unit{Orders: q}
	base := orderCheckpointDigests(t, u)
	n.RetailSubtypeCode, n.RetailSubtype = 5, []byte{1, 2, 3}
	n.RetailSubtypeUnitA, n.RetailSubtypeUnitB = 2, 3
	n.RetailSubtypeWords16, n.RetailSubtypeWords32 = []uint16{7}, []uint32{11}
	q.diagnostics = []string{"test diagnostic"}
	q.secondaryTick = 29
	q.ownedHandlers = make([]OwnedHandler, len(table))
	for range 2 {
		if got := orderCheckpointDigests(t, u); got != base {
			t.Fatal("diagnostics, staging or empty handler storage affected the checkpoint")
		}
	}
	if u.Orders != q || q.primary[0] != n || q.secondary[0] != n || q.lastPumpTick != 23 ||
		n.ID != 1 || n.GoalX != 17 || n.Flags != 19 || n.RetailSubtypeCode != 5 ||
		!bytes.Equal(n.RetailSubtype, []byte{1, 2, 3}) || n.RetailSubtypeUnitA != 2 || n.RetailSubtypeUnitB != 3 ||
		n.RetailSubtypeWords16[0] != 7 || n.RetailSubtypeWords32[0] != 11 ||
		len(q.diagnostics) != 1 || q.diagnostics[0] != "test diagnostic" || q.secondaryTick != 29 || len(q.ownedHandlers) != len(table) {
		t.Fatal("capture mutated queue or node state")
	}
}

func TestCheckpointOrderUnsupportedState(t *testing.T) {
	tests := []struct {
		name   string
		field  string
		mutate func(*units.Unit, *Queue, *Node)
	}{
		{"unknown order", "Orders", func(u *units.Unit, _ *Queue, _ *Node) { u.Orders = []int{1} }},
		{"typed nil", "Orders", func(u *units.Unit, _ *Queue, _ *Node) { u.Orders = (*Queue)(nil) }},
		{"detached cleanup", "detachedNode", func(_ *units.Unit, q *Queue, n *Node) { q.detachedNode = n }},
		{"detached successor", "detachedHasSuccessor", func(_ *units.Unit, q *Queue, _ *Node) { q.detachedHasSuccessor = true }},
		{"binding", "binding", func(_ *units.Unit, q *Queue, _ *Node) { q.binding = &QueueBinding{} }},
		{"handler shape", "ownedHandlers", func(_ *units.Unit, q *Queue, _ *Node) { q.ownedHandlers = make([]OwnedHandler, 1) }},
		{"handler callback", "ownedHandlers[1]", func(_ *units.Unit, q *Queue, _ *Node) {
			q.ownedHandlers = make([]OwnedHandler, len(table))
			q.ownedHandlers[1] = func(*units.Unit, *Node, uint32, uint32) (Code, bool) {
				panic("capture must not invoke a handler")
			}
		}},
		{"queued input", "QueuedIssue", func(_ *units.Unit, _ *Queue, n *Node) { n.QueuedIssue = true }},
		{"goal input", "GoalSupplied", func(_ *units.Unit, _ *Queue, n *Node) { n.GoalSupplied = true }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &Node{}
			q := &Queue{primary: []*Node{n}}
			u := &units.Unit{Orders: q}
			c := orderCheckpointContext(t, u)
			collectOrdersCheckpoint(t, c)
			tt.mutate(u, q, n)
			_, err := (&Pump{}).CollectCheckpointReferences(c)
			if err == nil || !strings.Contains(err.Error(), tt.field) {
				t.Fatalf("collector error %v; want field %s", err, tt.field)
			}
			var out bytes.Buffer
			err = (&Pump{}).WriteCheckpoint(checkpoint.NewEncoder(&out), c)
			if err == nil || !strings.Contains(err.Error(), tt.field) {
				t.Fatalf("writer error %v; want field %s", err, tt.field)
			}
		})
	}
}

func TestCheckpointOrderWriterNeverDiscoversReferences(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*units.Unit, *Queue, *Node)
	}{
		{"queue", func(u *units.Unit, _ *Queue, _ *Node) { u.Orders = &Queue{} }},
		{"primary node", func(_ *units.Unit, q *Queue, _ *Node) { q.primary = []*Node{{}} }},
		{"detached danger node", func(_ *units.Unit, q *Queue, _ *Node) { q.danger.response = &Node{} }},
		{"retired contact", func(_ *units.Unit, q *Queue, _ *Node) { q.danger.contacts[2].unit = &units.Unit{} }},
		{"firing allocation", func(_ *units.Unit, q *Queue, _ *Node) { q.firingPosition.target = &units.Unit{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &Node{}
			q := &Queue{primary: []*Node{n}}
			u := &units.Unit{Orders: q}
			c := orderCheckpointContext(t, u)
			collectOrdersCheckpoint(t, c)
			tt.mutate(u, q, n)
			var out bytes.Buffer
			err := (&Pump{}).WriteCheckpoint(checkpoint.NewEncoder(&out), c)
			if err == nil || !strings.Contains(err.Error(), "undiscovered reference") {
				t.Fatalf("writer error %v; want undiscovered reference", err)
			}
			if len(c.Queues.Values()) != 1 || len(c.Nodes.Values()) != 1 || len(c.Units.Allocations.Values()) != 1 {
				t.Fatal("writer registered a reference")
			}
		})
	}
}
