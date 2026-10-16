package path

import (
	"bytes"
	"io"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
)

func checkpointAllAccessors() CheckpointAccessors {
	return CheckpointAccessors{
		Nodes: []CheckpointAccessor{
			{Kind: 1, Layer: 1, Requester: 2, Owner: 3, Profile: [8]int32{1, 2, 3, 4, 5, 6, 7, 8}, Tick: 4, FootprintX: 1, FootprintZ: 2},
			{Kind: 2, Inputs: []uint32{1}, Learned: 4, FootprintX: 1, FootprintZ: 2},
			{Kind: 3, Inputs: []uint32{2}, Owner: 3, FootprintX: 1, FootprintZ: 2, Through: 2},
			{Kind: 4, Inputs: []uint32{3}, Layer: 1, FootprintX: 1, FootprintZ: 2},
			{Kind: 5, Inputs: []uint32{4}, Owner: 3, Wall: 1},
			{Kind: 6, Inputs: []uint32{5, 1}, Start: Cell{7, 8}, Bounds: Rect{Min: Cell{6, 6}, Max: Cell{9, 10}}, FootprintX: 1, FootprintZ: 2},
			{Kind: 7, ClaimCounts: 5, ClaimOwn: 6, Owner: 3, Serial: 10, Width: 20, Height: 30, FootprintX: 1, FootprintZ: 2, Per: 4, Against: 5, Row: 2},
			{Kind: 8, Layer: 1, Profile: [8]int32{1, 2, 3, 4, 5, 6, 7, 8}, Requester: 2, Tick: 4},
			{Kind: 9, Layer: 1, Requester: 2, Owner: 3, Profile: [8]int32{1, 2, 3, 4, 5, 6, 7, 8}, Tick: 4, FootprintX: 1, FootprintZ: 2, Bounds: Rect{Min: Cell{-1, 0}, Max: Cell{3, 4}}},
			{Kind: 2, Inputs: []uint32{9}, Learned: 4, FootprintX: 1, FootprintZ: 2},
		},
		Passable: 6, Leg: 10, Cost: 7, Revise: 8,
	}
}

func checkpointAllCallbacks() SearchConfig {
	return SearchConfig{
		PassableValue: func(Cell) uint8 { panic("passability called") },
		LegValue:      func(Cell) uint8 { panic("leg called") },
		CostDir:       func(Cell, uint8) int32 { panic("cost called") },
		Revise:        func() { panic("revision called") },
	}
}

func TestCheckpointAccessorValidationAndCopiedRegistration(t *testing.T) {
	s := &Session{cfg: checkpointAllCallbacks()}
	c := NewCheckpointContext()
	a := checkpointAllAccessors()
	if err := c.SetAccessors(s, a); err != nil {
		t.Fatal(err)
	}
	if err := c.SetAccessors(s, checkpointAllAccessors()); err != nil {
		t.Fatal(err)
	}
	a.Nodes[0].Profile[0] = 99
	a.Nodes[1].Inputs[0] = 99
	if !equalCheckpointAccessors(c.accessors[s], checkpointAllAccessors()) {
		t.Fatal("caller mutated registered descriptors")
	}
	if err := c.SetAccessors(s, a); err == nil {
		t.Fatal("accepted invalid repeated registration")
	}
	b := checkpointAllAccessors()
	b.Nodes[0].Tick++
	if err := c.SetAccessors(s, b); err == nil {
		t.Fatal("accepted conflicting valid registration")
	}
	if _, err := c.Searches.Add(s); err != nil {
		t.Fatal(err)
	}
	collectPathCheckpoint(t, &Scheduler{}, c)
	if err := (&Scheduler{}).WriteCheckpoint(checkpoint.NewEncoder(io.Discard), c); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointAccessorRejectsMalformedDAG(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*CheckpointAccessors)
	}{
		{"unknown kind", func(a *CheckpointAccessors) { a.Nodes[0].Kind = 10 }},
		{"own view irrelevant field", func(a *CheckpointAccessors) { a.Nodes[8].Serial = 4 }},
		{"own view with input", func(a *CheckpointAccessors) { a.Nodes[8].Inputs = []uint32{1} }},
		{"wrong revise kind own view", func(a *CheckpointAccessors) { a.Revise = 9 }},
		{"wrong operand count", func(a *CheckpointAccessors) { a.Nodes[1].Inputs = nil }},
		{"absent operand", func(a *CheckpointAccessors) { a.Nodes[1].Inputs[0] = 0 }},
		{"forward operand", func(a *CheckpointAccessors) { a.Nodes[1].Inputs[0] = 3 }},
		{"self operand", func(a *CheckpointAccessors) { a.Nodes[1].Inputs[0] = 2 }},
		{"non-view operand", func(a *CheckpointAccessors) {
			a.Nodes = append(a.Nodes, CheckpointAccessor{Kind: 2, Inputs: []uint32{7}})
		}},
		{"irrelevant field", func(a *CheckpointAccessors) { a.Nodes[1].Serial = 4 }},
		{"irrelevant profile", func(a *CheckpointAccessors) { a.Nodes[6].Profile[7] = 4 }},
		{"wall selector", func(a *CheckpointAccessors) { a.Nodes[4].Wall = 0 }},
		{"claim row", func(a *CheckpointAccessors) { a.Nodes[6].Row = 1 }},
		{"owner", func(a *CheckpointAccessors) { a.Nodes[0].Owner = 10 }},
		{"learned player", func(a *CheckpointAccessors) { a.Nodes[1].Learned = 11 }},
		{"missing root", func(a *CheckpointAccessors) { a.Passable = 0 }},
		{"outside root", func(a *CheckpointAccessors) { a.Leg = 999 }},
		{"wrong passable kind", func(a *CheckpointAccessors) { a.Passable = 7 }},
		{"wrong leg kind", func(a *CheckpointAccessors) { a.Leg = 8 }},
		{"wrong cost kind", func(a *CheckpointAccessors) { a.Cost = 1 }},
		{"wrong revise kind", func(a *CheckpointAccessors) { a.Revise = 7 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := checkpointAllAccessors()
			tc.mutate(&a)
			if err := NewCheckpointContext().SetAccessors(&Session{cfg: checkpointAllCallbacks()}, a); err == nil {
				t.Fatal("accepted malformed descriptor")
			}
		})
	}
	if err := NewCheckpointContext().SetAccessors(&Session{}, checkpointAllAccessors()); err == nil {
		t.Fatal("accepted roots without callbacks")
	}
	s := &Session{cfg: checkpointAllCallbacks()}
	c := NewCheckpointContext()
	if _, err := c.Searches.Add(s); err != nil {
		t.Fatal(err)
	}
	if _, err := (&Scheduler{}).CollectCheckpointReferences(c); err == nil {
		t.Fatal("accepted undescribed callbacks")
	}
	if err := (&Scheduler{}).WriteCheckpoint(checkpoint.NewEncoder(io.Discard), c); err == nil {
		t.Fatal("writer accepted undescribed callbacks")
	}
}

func TestCheckpointWrapperPropagationDiscoveryAndOwnConfig(t *testing.T) {
	g := PointGoal(Cell{1, 1}, 0)
	// Direct fixture construction permits panic callbacks: capture must not call
	// NewSession, Config, Resume, or any retained accessor to reconstruct state.
	cfg := checkpointAllCallbacks()
	cfg.Goal = g
	base := &Session{cfg: cfg}
	base.cfg.Scale = 65536
	straight := &straightenSearch{Search: base, cfg: cfg, probes: 2, out: []Point{{3, 4}}, done: true, status: 5}
	smooth := &smoothSearch{Search: straight, cfg: cfg, probes: 6, out: []Point{{7, 8}}, done: true, status: 9}
	c := NewCheckpointContext()
	if err := c.SetAccessors(smooth, checkpointAllAccessors()); err != nil {
		t.Fatal(err)
	}
	if len(c.accessors) != 3 {
		t.Fatal("registration did not reach all wrappers")
	}
	if _, err := c.Searches.Add(smooth); err != nil {
		t.Fatal(err)
	}
	q := &Scheduler{}
	first, err := q.CollectCheckpointReferences(c)
	if err != nil || first != 2 {
		t.Fatalf("first collect = (%d, %v), want child + shared goal", first, err)
	}
	second, err := q.CollectCheckpointReferences(c)
	if err != nil || second != 1 {
		t.Fatalf("second collect = (%d, %v), want grandchild", second, err)
	}
	third, err := q.CollectCheckpointReferences(c)
	if err != nil || third != 0 {
		t.Fatalf("third collect = (%d, %v)", third, err)
	}
	if len(c.Goals.Values()) != 1 {
		t.Fatal("shared goal lost identity")
	}
	before := pathCheckpointBytes(t, q, c)
	// Unused output capacity and finishing scratch are not future state.
	straight.out = append(make([]Point, 0, straightenCap), straight.out...)
	smooth.out = append(make([]Point, 0, straightenCap), smooth.out...)
	straight.out[:cap(straight.out)][30] = Point{99, 98}
	smooth.out[:cap(smooth.out)][30] = Point{99, 98}
	smooth.limit, smooth.limited, smooth.legDir = 96, true, 7
	if got := pathCheckpointBytes(t, q, c); !bytes.Equal(got, before) {
		t.Fatal("wrapper scratch changed bytes")
	}
	smooth.cfg.Scale = 42
	if got := pathCheckpointBytes(t, q, c); bytes.Equal(got, before) {
		t.Fatal("writer used promoted Config instead of wrapper cfg")
	}
	// Registration is atomic even when the child disagrees.
	other := NewCheckpointContext()
	changed := checkpointAllAccessors()
	changed.Nodes[0].Tick++
	if err := other.SetAccessors(base, changed); err != nil {
		t.Fatal(err)
	}
	if err := other.SetAccessors(smooth, checkpointAllAccessors()); err == nil {
		t.Fatal("overwrote child descriptor")
	}
	if len(other.accessors) != 1 {
		t.Fatal("conflicting registration partially installed")
	}
}

func TestCheckpointAccessorPresenceRevalidatedAtCapture(t *testing.T) {
	q, c, s := checkpointFixture(t, nil)
	s.cfg.PassableValue = nil
	if _, err := q.CollectCheckpointReferences(c); err == nil {
		t.Fatal("collector accepted changed callback presence")
	}
	if err := q.WriteCheckpoint(checkpoint.NewEncoder(io.Discard), c); err == nil {
		t.Fatal("writer accepted changed callback presence")
	}
}
