package movement

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func movementPathBindingFixture(t *testing.T, w *units.World) (*System, *CheckpointContext, *checkpoint.BindingAuthority) {
	t.Helper()
	a := checkpoint.NewBindingAuthority()
	s := NewSystemWithCheckpointBindings(nil, Profile{}, nil, a)
	// Isolate the approved world edges from BindWorld's unrelated layer ports.
	s.world, s.pathProvider.world = w, w
	c := movementCheckpointContext()
	if err := c.SetPathBindings(s, w, a); err != nil {
		t.Fatal(err)
	}
	return s, c, a
}

func movementPathBindingBytes(t *testing.T, s *System, c *CheckpointContext) []byte {
	t.Helper()
	collectMovementCheckpoint(t, s, c)
	if _, err := s.Scheduler.CollectCheckpointReferences(c.Paths); err != nil {
		t.Fatal(err)
	}
	return movementCheckpointWrite(t, func(e *checkpoint.Encoder) {
		_ = s.WriteCheckpoint(e, c)
		_ = s.WritePathProviderCheckpoint(e, c)
		_ = s.Scheduler.WriteCheckpoint(e, c.Paths)
	})
}

func movementPathBindingRefused(t *testing.T, s *System, c *CheckpointContext) {
	t.Helper()
	if _, err := s.CollectCheckpointReferences(c); err == nil {
		t.Fatal("collection admitted a replaced binding")
	}
	for _, write := range []func(*checkpoint.Encoder, *CheckpointContext) error{s.WriteCheckpoint, s.WritePathProviderCheckpoint} {
		var out bytes.Buffer
		if err := write(checkpoint.NewEncoder(&out), c); err == nil {
			t.Fatal("writer admitted a replaced binding")
		}
	}
}

func TestMovementPathBindingVectorsAndCapturePurity(t *testing.T) {
	w := &units.World{}
	s, c, a := movementPathBindingFixture(t, w)
	s.ConfigurePathWithCheckpointBinding(3, 41, func(int) bool { panic("capture called eligibility") }, a)
	s.Scheduler.SetSearchWithCheckpointBinding(func(path.Request, int32, int) path.WorkResult { panic("capture called search") }, a)
	s.Scheduler.SetPublishWithCheckpointBinding(func(path.Request, []path.Point, path.Status) { panic("capture called publication") }, a)
	p := s.pathProvider
	p.cursor[0], p.cursor[9], p.started[9], p.tick = -7, 10, true, 77
	want := movementCheckpointVector(t,
		uint8(1), [10]int64{-7, 0, 0, 0, 0, 0, 0, 0, 0, 10}, uint8(1), int32(41), int64(3),
		[10]uint32{}, [10]uint8{0, 0, 0, 0, 0, 0, 0, 0, 0, 1}, uint8(1), uint32(77), uint8(1))
	got := movementCheckpointWrite(t, func(e *checkpoint.Encoder) { _ = s.WritePathProviderCheckpoint(e, c) })
	if !bytes.Equal(got, want) {
		t.Fatalf("provider vector\ngot  %x\nwant %x", got, want)
	}
	// The scheduler gets the new topology through its existing provider getter
	// calls, after the provider's players/limit assignments [04 R-PATH-01 §6].
	wantScheduler := movementCheckpointVector(t,
		[10]int32{}, uint8(0), int32(0x18000), uint8(1), uint32(0), uint8(0), uint32(0),
		int64(3), int64(0), uint8(1), uint8(1), [10]int32{}, uint8(1),
		[10]int32{}, int32(1333), int32(41), uint16(9), uint32(0), uint16(10), uint32(0))
	if got := movementCheckpointWrite(t, func(e *checkpoint.Encoder) { _ = s.Scheduler.WriteCheckpoint(e, c.Paths) }); !bytes.Equal(got, wantScheduler) {
		t.Fatalf("scheduler topology\ngot  %x\nwant %x", got, wantScheduler)
	}
	before := movementPathBindingBytes(t, s, c)
	if err := c.SetPathBindings(s, w, a); err != nil {
		t.Fatal(err)
	}
	if after := movementPathBindingBytes(t, s, c); !bytes.Equal(before, after) {
		t.Fatal("capture or identical registration changed state")
	}
}

func TestMovementPathBindingWorldPresenceAndNilEligibility(t *testing.T) {
	s, c, a := movementPathBindingFixture(t, nil)
	collectMovementCheckpoint(t, s, c)
	before := movementCheckpointWrite(t, func(e *checkpoint.Encoder) { _ = s.WriteCheckpoint(e, c) })
	w := &units.World{}
	s.world, s.pathProvider.world = w, w
	c = movementCheckpointContext()
	if err := c.SetPathBindings(s, w, a); err != nil {
		t.Fatal(err)
	}
	collectMovementCheckpoint(t, s, c)
	after := movementCheckpointWrite(t, func(e *checkpoint.Encoder) { _ = s.WriteCheckpoint(e, c) })
	if len(before) != len(after) {
		t.Fatal("world presence changed section length")
	}
	changes := 0
	for i := range before {
		if before[i] != after[i] {
			changes++
			if before[i] != 0 || after[i] != 1 {
				t.Fatal("world presence changed another retained value")
			}
		}
	}
	if changes != 1 {
		t.Fatal("System.world presence is not retained")
	}
	s.ConfigurePathWithCheckpointBinding(1, 1, nil, a)
	if s.pathProvider.checkpointEligibilityAuthority != nil {
		t.Fatal("absent eligibility retained authority")
	}
	want := movementCheckpointVector(t, uint8(1), [10]int64{}, uint8(0), int32(1), int64(1), [10]uint32{}, [10]uint8{}, uint8(1), uint32(0), uint8(1))
	if got := movementCheckpointWrite(t, func(e *checkpoint.Encoder) { _ = s.WritePathProviderCheckpoint(e, c) }); !bytes.Equal(got, want) {
		t.Fatal("nil eligibility did not retain its absent tag")
	}
}

func TestMovementPathBindingReplacementRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*System, *checkpoint.BindingAuthority)
	}{
		{"ordinary configuration", func(s *System, _ *checkpoint.BindingAuthority) { s.ConfigurePath(1, 1, s.pathProvider.eligible) }},
		{"foreign authority", func(s *System, _ *checkpoint.BindingAuthority) {
			s.ConfigurePathWithCheckpointBinding(1, 1, s.pathProvider.eligible, checkpoint.NewBindingAuthority())
		}},
		{"nil authority", func(s *System, _ *checkpoint.BindingAuthority) {
			s.ConfigurePathWithCheckpointBinding(1, 1, s.pathProvider.eligible, nil)
		}},
		{"ordinary provider", func(s *System, _ *checkpoint.BindingAuthority) { s.Scheduler.SetCandidateProvider(s.pathProvider) }},
		{"foreign provider", func(s *System, a *checkpoint.BindingAuthority) {
			s.Scheduler.SetCandidateProviderWithCheckpointBinding(&pathProvider{system: s}, a)
		}},
		{"scheduler alias", func(s *System, a *checkpoint.BindingAuthority) {
			s.Scheduler = NewSystemWithCheckpointBindings(nil, Profile{}, nil, a).Scheduler
		}},
		{"provider alias", func(s *System, _ *checkpoint.BindingAuthority) { copy := *s.pathProvider; s.pathProvider = &copy }},
		{"system world", func(s *System, _ *checkpoint.BindingAuthority) { s.world = &units.World{} }},
		{"provider world", func(s *System, _ *checkpoint.BindingAuthority) { s.pathProvider.world = &units.World{} }},
		{"provider system", func(s *System, _ *checkpoint.BindingAuthority) { s.pathProvider.system = &System{} }},
		{"absent provider owner", func(s *System, _ *checkpoint.BindingAuthority) { s.pathProvider.system = nil }},
		{"absent provider", func(s *System, _ *checkpoint.BindingAuthority) { s.pathProvider = nil }},
		{"absent scheduler", func(s *System, _ *checkpoint.BindingAuthority) { s.Scheduler = nil }},
		{"active eligibility cache", func(s *System, _ *checkpoint.BindingAuthority) { s.pathProvider.eligibleValid = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, c, a := movementPathBindingFixture(t, nil)
			tc.edit(s, a)
			movementPathBindingRefused(t, s, c)
		})
	}
	for _, search := range []bool{false, true} {
		s, c, a := movementPathBindingFixture(t, nil)
		if search {
			s.Scheduler.SetSearch(func(path.Request, int32, int) path.WorkResult { panic("unattested search called") })
		} else {
			s.Scheduler.SetPublish(func(path.Request, []path.Point, path.Status) { panic("unattested publisher called") })
		}
		s.ConfigurePathWithCheckpointBinding(1, 1, s.pathProvider.eligible, a)
		if _, err := s.Scheduler.CollectCheckpointReferences(c.Paths); err == nil {
			t.Fatal("configuration blessed an unrelated callback")
		}
	}
}

func TestMovementPathBindingRegistrationRejectsConflicts(t *testing.T) {
	s, c, a := movementPathBindingFixture(t, nil)
	for _, tc := range []struct {
		c *CheckpointContext
		s *System
		w *units.World
		a *checkpoint.BindingAuthority
	}{
		{nil, s, nil, a}, {&CheckpointContext{}, s, nil, a}, {c, nil, nil, a}, {c, s, nil, nil},
		{c, &System{}, nil, a}, {c, s, &units.World{}, a}, {c, s, nil, checkpoint.NewBindingAuthority()},
	} {
		if err := tc.c.SetPathBindings(tc.s, tc.w, tc.a); err == nil {
			t.Fatal("absent or conflicting registration accepted")
		}
	}
	movementPathBindingBytes(t, s, c)
	old := NewSystem(nil, Profile{}, nil)
	if err := movementCheckpointContext().SetPathBindings(old, nil, a); err == nil {
		t.Fatal("registration blessed ordinary construction")
	}
	c = movementCheckpointContext()
	if err := c.Paths.SetSchedulerBindings(&path.Scheduler{}, a); err != nil {
		t.Fatal(err)
	}
	if err := c.SetPathBindings(s, nil, a); err == nil || c.system != nil || c.pathBindings.authority != nil {
		t.Fatal("conflicting shared path context partially registered movement")
	}
	c = movementCheckpointContext()
	s.pathProvider.world = &units.World{}
	if err := c.SetPathBindings(s, nil, a); err == nil || c.system != nil {
		t.Fatal("registration ignored provider world mismatch")
	}
}

func TestMovementPathConfigurationPreservesGuardsAndCursor(t *testing.T) {
	s, c, a := movementPathBindingFixture(t, nil)
	p := s.pathProvider
	p.cursor[1], p.started[1], p.tick = 37, true, 91
	p.eligibleNow[1] = true
	for _, args := range [][2]int{{-1, 1}, {11, 1}, {1, 0}, {1, -1}} {
		s.ConfigurePath(args[0], int32(args[1]), nil)
		s.ConfigurePathWithCheckpointBinding(args[0], int32(args[1]), nil, checkpoint.NewBindingAuthority())
	}
	scheduler := s.Scheduler
	s.Scheduler = nil
	s.ConfigurePath(2, 3, nil)
	s.Scheduler = scheduler
	s.pathProvider = nil
	s.ConfigurePath(2, 3, nil)
	s.pathProvider = p
	var absent *System
	absent.ConfigurePath(1, 1, nil)
	absent.ConfigurePathWithCheckpointBinding(1, 1, nil, a)
	if s.PathPlayers != 1 || s.PathUnitLimit != 1 || !p.checkpointEligibilityAuthority.Matches(a) || !path.CheckpointProviderMatches(s.Scheduler, p, a) {
		t.Fatal("invalid configuration changed bindings or topology")
	}
	before := movementPathBindingBytes(t, s, c)
	eligible := func(int) bool { panic("configuration invoked eligibility") }
	s.ConfigurePath(3, 41, eligible)
	if p.checkpointEligibilityAuthority != nil || path.CheckpointProviderMatches(s.Scheduler, p, a) {
		t.Fatal("ordinary configuration kept authority")
	}
	s.ConfigurePathWithCheckpointBinding(1, 1, eligible, a)
	if p.cursor[1] != 37 || !p.started[1] || p.tick != 91 || !p.eligibleNow[1] {
		t.Fatal("configuration reset persistent cursor or eligibility scratch")
	}
	if after := movementPathBindingBytes(t, s, c); !bytes.Equal(before, after) {
		t.Fatal("canonical reinstall changed retained state")
	}
}

func TestMovementPathConstructorAndUnrelatedRefusals(t *testing.T) {
	var old, admitted *System
	for i := 0; i < 2; i++ {
		terrain, grid := syntheticTerrainForIntegrate(), NewOccupancyGrid()
		if i == 0 {
			old = NewSystem(terrain, wiringProfile, grid)
		} else {
			admitted = NewSystemWithCheckpointBindings(terrain, wiringProfile, grid, checkpoint.NewBindingAuthority())
		}
		if grid.plot != terrain || terrain.ClassRestamp() == nil || terrain.Movers() == nil || grid.planeW != terrain.CellW || grid.planeH != terrain.CellH {
			t.Fatal("constructor lost terrain/grid wiring")
		}
	}
	if !reflect.DeepEqual(old.Scheduler.TraceState(), admitted.Scheduler.TraceState()) || old.Fallback != admitted.Fallback || len(old.Routes) != len(admitted.Routes) || len(old.Flights) != len(admitted.Flights) {
		t.Fatal("admitted construction changed initial simulation values")
	}
	for player := -1; player <= 10; player++ {
		if old.pathProvider.Eligible(player) != (player == 0) || admitted.pathProvider.Eligible(player) != (player == 0) {
			t.Fatal("constructor changed default eligibility")
		}
	}
	for _, mutate := range []func(*System){
		func(s *System) { s.Terrain = &world.Terrain{} },
		func(s *System) { s.Classes = map[string]*content.MovementClass{} },
		func(s *System) { s.Rules = checkpointUnknownRules{} },
		func(s *System) {
			s.SetProductFootprint(func(uint32) (int32, int32, bool) { panic("capture called footprint") })
		},
		func(s *System) {
			s.layerRegistry = &ClassLayers{mapping: func(int32, int32) (uint16, bool) { panic("capture called mapping") }}
		},
	} {
		s, c, _ := movementPathBindingFixture(t, nil)
		mutate(s)
		if _, err := s.CollectCheckpointReferences(c); err == nil {
			t.Fatal("path registration blessed an unrelated movement binding")
		}
	}
}
