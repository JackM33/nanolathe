package path

import (
	"errors"
	"fmt"
	"slices"

	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
)

// CheckpointContext owns capture-local path identities and the value operands
// of suspended accessors (DESIGN_MULTIPLAYER §16.3.6). It calls no behavior.
type CheckpointContext struct {
	Goals     checkpoint.References[Goal]
	Searches  checkpoint.References[Search]
	accessors map[Search]CheckpointAccessors // lookup only, never iterated
	// Capture-local expectations, never encoded or exposed by a live owner.
	scheduler        *Scheduler
	bindingAuthority *checkpoint.BindingAuthority
}

func NewCheckpointContext() *CheckpointContext { return &CheckpointContext{} }

// CheckpointAccessor describes one node in the reviewed accessor DAG. Inputs
// refer to preceding nodes by one-based index; movement validates external IDs.
type CheckpointAccessor struct {
	Kind                                  uint8
	Inputs                                []uint32
	Layer, Learned, ClaimCounts, ClaimOwn checkpoint.ObjectID
	Requester                             uint32
	Owner                                 uint8
	Profile                               [8]int32
	Tick, Serial                          uint32
	Width, Height, FootprintX, FootprintZ int32
	Start                                 Cell
	Bounds                                Rect
	Through                               uint8
	Wall                                  uint8
	Row                                   uint8
	Per, Against                          int32
}

type CheckpointAccessors struct {
	Nodes                       []CheckpointAccessor
	Passable, Leg, Cost, Revise uint32
}

// SetAccessors copies reviewed value metadata, never the closure's behavior.
// A repeated registration must agree, including unused zero operands. The
// constructors give each closed wrapper the same callback captures as its
// child, so registration propagates through that chain using direct cfg reads.
// All children are checked before installing any metadata.
func (c *CheckpointContext) SetAccessors(search Search, values CheckpointAccessors) error {
	if c == nil {
		return pathCheckpointError("paths.accessors", errors.New("missing context"))
	}
	if search == nil {
		return pathCheckpointError("paths.accessors", errors.New("absent search"))
	}
	var chain []Search
	for search != nil {
		cfg, child, err := checkpointSearch(search)
		if err != nil {
			return pathCheckpointError("paths.accessors", err)
		}
		if slices.Contains(chain, search) {
			return pathCheckpointError("paths.accessors", errors.New("cyclic search wrapper"))
		}
		if err := validateCheckpointAccessors(cfg, values); err != nil {
			return pathCheckpointError("paths.accessors", err)
		}
		if old, ok := c.accessors[search]; ok && !equalCheckpointAccessors(old, values) {
			return pathCheckpointError("paths.accessors", errors.New("conflicting accessor registration"))
		}
		chain = append(chain, search)
		search = child
	}
	values.Nodes = slices.Clone(values.Nodes)
	for i := range values.Nodes {
		values.Nodes[i].Inputs = slices.Clone(values.Nodes[i].Inputs)
	}
	if c.accessors == nil {
		c.accessors = make(map[Search]CheckpointAccessors)
	}
	for _, s := range chain {
		c.accessors[s] = values
	}
	return nil
}

func equalCheckpointAccessors(a, b CheckpointAccessors) bool {
	if a.Passable != b.Passable || a.Leg != b.Leg || a.Cost != b.Cost || a.Revise != b.Revise || len(a.Nodes) != len(b.Nodes) {
		return false
	}
	for i := range a.Nodes {
		x, y := a.Nodes[i], b.Nodes[i]
		if !slices.Equal(x.Inputs, y.Inputs) {
			return false
		}
		x.Inputs, y.Inputs = nil, nil
		// Compare each scalar without reflection or pointer representations.
		if x.Kind != y.Kind || x.Layer != y.Layer || x.Learned != y.Learned || x.ClaimCounts != y.ClaimCounts || x.ClaimOwn != y.ClaimOwn || x.Requester != y.Requester || x.Owner != y.Owner || x.Profile != y.Profile || x.Tick != y.Tick || x.Serial != y.Serial || x.Width != y.Width || x.Height != y.Height || x.FootprintX != y.FootprintX || x.FootprintZ != y.FootprintZ || x.Start != y.Start || x.Bounds != y.Bounds || x.Through != y.Through || x.Wall != y.Wall || x.Row != y.Row || x.Per != y.Per || x.Against != y.Against {
			return false
		}
	}
	return true
}

func validateCheckpointAccessors(cfg SearchConfig, a CheckpointAccessors) error {
	for i, n := range a.Nodes {
		var allowed CheckpointAccessor
		allowed.Kind = n.Kind
		inputs := 0
		switch n.Kind {
		case 1:
			allowed.Layer, allowed.Requester, allowed.Owner, allowed.Profile, allowed.Tick = n.Layer, n.Requester, n.Owner, n.Profile, n.Tick
			allowed.FootprintX, allowed.FootprintZ = n.FootprintX, n.FootprintZ
		case 2:
			inputs = 1
			allowed.Learned, allowed.FootprintX, allowed.FootprintZ = n.Learned, n.FootprintX, n.FootprintZ
			if n.Learned > 10 {
				return fmt.Errorf("Nodes[%d].Learned: invalid player reference", i)
			}
		case 3:
			inputs = 1
			allowed.Owner, allowed.FootprintX, allowed.FootprintZ, allowed.Through = n.Owner, n.FootprintX, n.FootprintZ, n.Through
		case 4:
			inputs = 1
			allowed.Layer, allowed.FootprintX, allowed.FootprintZ = n.Layer, n.FootprintX, n.FootprintZ
		case 5:
			inputs = 1
			allowed.Owner, allowed.Wall = n.Owner, n.Wall
			if n.Wall != 1 && n.Wall != 2 {
				return fmt.Errorf("Nodes[%d].Wall: unsupported wall selector", i)
			}
		case 6:
			inputs = 2
			allowed.Start, allowed.Bounds, allowed.FootprintX, allowed.FootprintZ = n.Start, n.Bounds, n.FootprintX, n.FootprintZ
		case 7:
			allowed.ClaimCounts, allowed.ClaimOwn, allowed.Owner, allowed.Serial = n.ClaimCounts, n.ClaimOwn, n.Owner, n.Serial
			allowed.Width, allowed.Height, allowed.FootprintX, allowed.FootprintZ = n.Width, n.Height, n.FootprintX, n.FootprintZ
			allowed.Per, allowed.Against, allowed.Row = n.Per, n.Against, n.Row
			if n.Row != 2 && n.Row != 3 {
				return fmt.Errorf("Nodes[%d].Row: unsupported claim row", i)
			}
		case 8:
			allowed.Layer, allowed.Profile, allowed.Requester, allowed.Tick = n.Layer, n.Profile, n.Requester, n.Tick
		default:
			return fmt.Errorf("Nodes[%d].Kind: unsupported accessor kind %d", i, n.Kind)
		}
		if n.Owner > 9 {
			return fmt.Errorf("Nodes[%d].Owner: invalid player", i)
		}
		if len(n.Inputs) != inputs {
			return fmt.Errorf("Nodes[%d].Inputs: wrong operand count", i)
		}
		for _, input := range n.Inputs {
			if input == 0 || uint64(input) > uint64(i) {
				return fmt.Errorf("Nodes[%d].Inputs: operand must precede node", i)
			}
			if k := a.Nodes[input-1].Kind; k < 1 || k > 6 {
				return fmt.Errorf("Nodes[%d].Inputs: operand is not a view", i)
			}
		}
		allowed.Inputs = n.Inputs
		if !equalCheckpointAccessors(CheckpointAccessors{Nodes: []CheckpointAccessor{n}}, CheckpointAccessors{Nodes: []CheckpointAccessor{allowed}}) {
			return fmt.Errorf("nodes[%d]: irrelevant operand is nonzero", i)
		}
	}
	for _, root := range []struct {
		name    string
		id      uint32
		present bool
		lo, hi  uint8
	}{
		{"Passable", a.Passable, cfg.PassableValue != nil, 1, 6},
		{"Leg", a.Leg, cfg.LegValue != nil, 1, 6},
		{"Cost", a.Cost, cfg.CostDir != nil, 7, 7},
		{"Revise", a.Revise, cfg.Revise != nil, 8, 8},
	} {
		if root.present != (root.id != 0) {
			return fmt.Errorf("%s: callback presence differs from descriptor", root.name)
		}
		if uint64(root.id) > uint64(len(a.Nodes)) {
			return fmt.Errorf("%s: missing accessor node", root.name)
		}
		if root.id != 0 {
			k := a.Nodes[root.id-1].Kind
			if k < root.lo || k > root.hi {
				return fmt.Errorf("%s: incompatible accessor kind", root.name)
			}
		}
	}
	return nil
}

func pathCheckpointError(path string, err error) error {
	return fmt.Errorf("nanolathe: checkpoint owner paths: logical path %s, providers searched [], expected canonical checkpoint: %w", path, err)
}

func checkpointGoal(g Goal) error {
	switch v := g.(type) {
	case nil:
		return nil
	case *pointGoal:
		if v != nil {
			return nil
		}
	case *annulusGoal:
		if v != nil {
			return nil
		}
	case *rectGoal:
		if v != nil {
			return nil
		}
	default:
		return fmt.Errorf("unsupported goal %T", g)
	}
	return errors.New("typed-nil goal")
}

// The closed switch reads fields directly, never Search.Config or other ports.
func checkpointSearch(search Search) (SearchConfig, Search, error) {
	switch s := search.(type) {
	case nil:
		return SearchConfig{}, nil, nil
	case *Session:
		if s != nil {
			return s.cfg, nil, nil
		}
	case *straightenSearch:
		if s != nil {
			if s.Search == nil {
				return SearchConfig{}, nil, errors.New("missing wrapped search")
			}
			return s.cfg, s.Search, nil
		}
	case *smoothSearch:
		if s != nil {
			if s.Search == nil {
				return SearchConfig{}, nil, errors.New("missing wrapped search")
			}
			return s.cfg, s.Search, nil
		}
	default:
		return SearchConfig{}, nil, fmt.Errorf("unsupported search %T", search)
	}
	return SearchConfig{}, nil, errors.New("typed-nil search")
}

// CheckpointKernelKind identifies the three stateless, reviewed kernels for
// movement's composition binding (DESIGN_MULTIPLAYER §16.3.5). Nil uses the
// existing retail fallback. Pointer forms hold the same zero-field values;
// typed-nil pointers and custom kernels are unsupported. No search is opened.
func CheckpointKernelKind(kernel Kernel) (uint8, error) {
	switch k := kernel.(type) {
	case nil, RetailKernel:
		return 1, nil
	case StraightenKernel:
		return 2, nil
	case SmoothKernel:
		return 3, nil
	case *RetailKernel:
		if k != nil {
			return 1, nil
		}
	case *StraightenKernel:
		if k != nil {
			return 2, nil
		}
	case *SmoothKernel:
		if k != nil {
			return 3, nil
		}
	default:
		return 0, pathCheckpointError("paths.kernel", fmt.Errorf("unsupported kernel %T", kernel))
	}
	return 0, pathCheckpointError("paths.kernel", errors.New("typed-nil kernel"))
}

func addPathCheckpointReference[T comparable](r *checkpoint.References[T], v T) (int, error) {
	if _, ok := r.Find(v); ok {
		return 0, nil
	}
	_, err := r.Add(v)
	if err != nil {
		return 0, err
	}
	return 1, nil
}

// CollectCheckpointReferences scans roots and objects added by movement.
// Session repeats discovery until no owner adds a reference (§16.3.6).
func (s *Scheduler) CollectCheckpointReferences(c *CheckpointContext) (added int, err error) {
	if s == nil || c == nil {
		return 0, pathCheckpointError("paths", errors.New("missing scheduler or context"))
	}
	if field, err := validateCheckpointScheduler(s, c); err != nil {
		return 0, pathCheckpointError("paths."+field, err)
	}
	addGoal := func(g Goal, field string) error {
		if err := checkpointGoal(g); err != nil {
			return pathCheckpointError(field, err)
		}
		n, err := addPathCheckpointReference(&c.Goals, g)
		added += n
		if err != nil {
			return pathCheckpointError(field, err)
		}
		return nil
	}
	if s.active != nil {
		if err := addGoal(s.active.Goal, "paths.active.Goal"); err != nil {
			return added, err
		}
	}
	for i, m := range s.latched {
		if err := addGoal(m.req.Goal, fmt.Sprintf("paths.latched[%d].req.Goal", i)); err != nil {
			return added, err
		}
	}
	for i, g := range c.Goals.Values() {
		if err := checkpointGoal(g); err != nil {
			return added, pathCheckpointError(fmt.Sprintf("paths.goals[%d]", i), err)
		}
	}
	for i, search := range c.Searches.Values() {
		field := fmt.Sprintf("paths.searches[%d]", i)
		cfg, child, err := checkpointSearch(search)
		if err != nil {
			return added, pathCheckpointError(field, err)
		}
		if err := validateCheckpointAccessors(cfg, c.accessors[search]); err != nil {
			return added, pathCheckpointError(field+".accessors", err)
		}
		// Wrapper Search precedes cfg in lexical order.
		if _, _, err := checkpointSearch(child); err != nil {
			return added, pathCheckpointError(field+".Search", err)
		}
		n, err := addPathCheckpointReference(&c.Searches, child)
		added += n
		if err != nil {
			return added, pathCheckpointError(field+".Search", err)
		}
		if err := addGoal(cfg.Goal, field+".cfg.Goal"); err != nil {
			return added, err
		}
	}
	return added, nil
}

// AppendCheckpointSummary appends only the scheduler words listed in
// DESIGN_MULTIPLAYER §16.3.7. Movement appends its per-handle working searches.
// It neither collects the graph nor calls the provider/search/publisher.
func (s *Scheduler) AppendCheckpointSummary(out *checkpoint.Summary) error {
	if s == nil || out == nil {
		return pathCheckpointError("paths.summary", errors.New("missing scheduler or summary"))
	}
	out.Word(uint64(int64(s.base)))
	checkpointSummaryBool(out, s.baseSet)
	for _, v := range s.scales {
		out.Word(uint64(int64(v)))
	}
	out.Word(uint64(s.callCount))
	out.Word(uint64(int64(s.playerCursor)))
	out.Word(uint64(int64(s.stepAllowance)))
	for _, v := range s.serviceCount {
		out.Word(uint64(int64(v)))
	}
	for _, v := range s.accumulator {
		out.Word(uint64(int64(v)))
	}
	checkpointSummaryBool(out, s.active != nil)
	if r := s.active; r != nil {
		out.Word(uint64(r.Player))
		out.Word(uint64(r.Unit))
		out.Word(uint64(int64(r.Start.X)))
		out.Word(uint64(int64(r.Start.Z)))
		out.Word(r.Activation)
	}
	// Nanolathe Modern parallel path batches: each latched search's request
	// words, as the active request's, then its slot.
	out.Word(uint64(len(s.latched)))
	for _, m := range s.latched {
		r := m.req
		out.Word(uint64(r.Player))
		out.Word(uint64(r.Unit))
		out.Word(uint64(int64(r.Start.X)))
		out.Word(uint64(int64(r.Start.Z)))
		out.Word(r.Activation)
		out.Word(uint64(int64(m.slot)))
	}
	return nil
}

// AppendSearchCheckpointSummary appends the working-search words from
// DESIGN_MULTIPLAYER §16.3.7: presence, charged popped work, setup steps,
// expanded, logical node count, heap count. Wrapper probes contribute to
// charged work exactly as Popped does, but no Search method is invoked.
// Unsupported chains fail before appending any word. Full graph validation
// and accessor attestation belong to the full checkpoint collector/writer.
func AppendSearchCheckpointSummary(search Search, out *checkpoint.Summary) error {
	if out == nil {
		return pathCheckpointError("paths.search.summary", errors.New("missing summary"))
	}
	if search == nil {
		out.Word(0)
		return nil
	}
	// The production chain has at most two wrappers. Keep that normal walk
	// on the stack; longer authored chains still receive cycle validation.
	var local [2]Search
	seen := local[:0]
	probes := 0
	for {
		var child Search
		switch s := search.(type) {
		case *Session:
			if s == nil {
				return pathCheckpointError("paths.search.summary", errors.New("typed-nil search"))
			}
			nodes := 0
			if s.ns != nil {
				nodes = max(0, len(s.ns.nodes)-1)
			}
			out.Word(1)
			out.Word(uint64(int64(s.popped + probes)))
			out.Word(uint64(int64(s.setupSteps)))
			checkpointSummaryBool(out, s.expanded)
			out.Word(uint64(nodes))
			out.Word(uint64(len(s.heap.entries)))
			return nil
		case *straightenSearch:
			if s == nil {
				return pathCheckpointError("paths.search.summary", errors.New("typed-nil search"))
			}
			child = s.Search
			probes += s.probes
		case *smoothSearch:
			if s == nil {
				return pathCheckpointError("paths.search.summary", errors.New("typed-nil search"))
			}
			child = s.Search
			probes += s.probes
		default:
			return pathCheckpointError("paths.search.summary", fmt.Errorf("unsupported search %T", search))
		}
		if child == nil {
			return pathCheckpointError("paths.search.summary", errors.New("missing wrapped search"))
		}
		if slices.Contains(seen, search) {
			return pathCheckpointError("paths.search.summary", errors.New("cyclic search wrapper"))
		}
		seen = append(seen, search)
		search = child
	}
}

func checkpointSummaryBool(out *checkpoint.Summary, v bool) {
	if v {
		out.Word(1)
	} else {
		out.Word(0)
	}
}
