package path

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
)

type checkpointBindingProvider struct {
	calls     []string
	installed func(string)
}

func (p *checkpointBindingProvider) PlayerCount() int {
	p.calls = append(p.calls, "players")
	if p.installed != nil {
		p.installed("players")
	}
	return 3
}
func (p *checkpointBindingProvider) UnitLimit() int32 {
	p.calls = append(p.calls, "limit")
	if p.installed != nil {
		p.installed("limit")
	}
	return 42
}
func (*checkpointBindingProvider) Eligible(int) bool { panic("capture called eligibility") }
func (*checkpointBindingProvider) Poll(int) (Request, PollResult) {
	panic("capture polled provider")
}

type checkpointSliceProvider struct {
	*checkpointBindingProvider
	values []int
}

func checkpointBindingSearch(Request, int32, int) WorkResult { panic("capture called search") }
func checkpointBindingPublish(Request, []Point, Status)      { panic("capture called publish") }

func checkpointBoundScheduler(t *testing.T) (*Scheduler, *CheckpointContext, *checkpointBindingProvider, *checkpoint.BindingAuthority) {
	t.Helper()
	a := checkpoint.NewBindingAuthority()
	s := NewSchedulerWithCheckpointBindings(checkpointBindingSearch, checkpointBindingPublish, a)
	p := &checkpointBindingProvider{}
	s.SetCandidateProviderWithCheckpointBinding(p, a)
	c := NewCheckpointContext()
	if err := c.SetSchedulerBindings(s, a); err != nil {
		t.Fatal(err)
	}
	return s, c, p, a
}

func checkpointBindingRefused(t *testing.T, s *Scheduler, c *CheckpointContext, field string) {
	t.Helper()
	if _, err := s.CollectCheckpointReferences(c); err == nil || !strings.Contains(err.Error(), field) {
		t.Fatalf("collection error = %v, want %s", err, field)
	}
	var out bytes.Buffer
	if err := s.WriteCheckpoint(checkpoint.NewEncoder(&out), c); err == nil || !strings.Contains(err.Error(), field) {
		t.Fatalf("write error = %v, want %s", err, field)
	}
	if out.Len() != 0 {
		t.Fatal("unattested scheduler wrote bytes")
	}
}

func TestCheckpointBindingVectorAndReadOnlyCapture(t *testing.T) {
	s, c, p, _ := checkpointBoundScheduler(t)
	p.installed = func(string) { t.Fatal("capture called provider getter") }
	// Independently authored scheduler fields, then the two empty object tables.
	// Binding presence occupies provider, publish and search in lexical order.
	want := pathCheckpointVector(t,
		[10]int32{}, uint8(0), int32(0), uint8(0), uint32(0), uint8(0), uint32(0),
		int64(3), int64(0), uint8(1), uint8(1), [10]int32{}, uint8(1),
		[10]int32{}, int32(1333), int32(42),
		uint16(9), uint32(0), uint16(10), uint32(0))
	for range 2 {
		if got := pathCheckpointBytes(t, s, c); !bytes.Equal(got, want) {
			t.Fatalf("binding vector\ngot  %x\nwant %x", got, want)
		}
	}
	if !slices.Equal(p.calls, []string{"players", "limit"}) {
		t.Fatalf("provider calls = %v", p.calls)
	}
	// A different private authority and owner allocation have no wire identity.
	other, otherContext, _, _ := checkpointBoundScheduler(t)
	if got := pathCheckpointBytes(t, other, otherContext); !bytes.Equal(got, want) {
		t.Fatal("local provenance changed canonical bytes")
	}
}

func TestCheckpointBindingSettersInvalidateOnlyTheirSlot(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*Scheduler, *checkpointBindingProvider)
		bind func(*Scheduler, *checkpointBindingProvider, *checkpoint.BindingAuthority)
	}{
		{"search", func(s *Scheduler, _ *checkpointBindingProvider) { s.SetSearch(checkpointBindingSearch) }, func(s *Scheduler, _ *checkpointBindingProvider, a *checkpoint.BindingAuthority) {
			s.SetSearchWithCheckpointBinding(checkpointBindingSearch, a)
		}},
		{"publish", func(s *Scheduler, _ *checkpointBindingProvider) { s.SetPublish(checkpointBindingPublish) }, func(s *Scheduler, _ *checkpointBindingProvider, a *checkpoint.BindingAuthority) {
			s.SetPublishWithCheckpointBinding(checkpointBindingPublish, a)
		}},
		{"provider", func(s *Scheduler, p *checkpointBindingProvider) { s.SetCandidateProvider(p) }, func(s *Scheduler, p *checkpointBindingProvider, a *checkpoint.BindingAuthority) {
			s.SetCandidateProviderWithCheckpointBinding(p, a)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, c, p, a := checkpointBoundScheduler(t)
			before := pathCheckpointBytes(t, s, c)
			tc.set(s, p) // Supplying the same callback/provider still invalidates.
			checkpointBindingRefused(t, s, c, "paths."+tc.name)
			tc.bind(s, p, checkpoint.NewBindingAuthority())
			checkpointBindingRefused(t, s, c, "paths."+tc.name)
			tc.bind(s, p, nil)
			checkpointBindingRefused(t, s, c, "paths."+tc.name)
			tc.bind(s, p, a)
			if got := pathCheckpointBytes(t, s, c); !bytes.Equal(got, before) {
				t.Fatal("restoring one slot changed bytes or invalidated another slot")
			}
		})
	}
	s, c, p, a := checkpointBoundScheduler(t)
	s.SetSearch(checkpointBindingSearch)
	s.SetPublishWithCheckpointBinding(checkpointBindingPublish, a)
	s.SetCandidateProviderWithCheckpointBinding(p, a)
	checkpointBindingRefused(t, s, c, "paths.search")
}

func TestCheckpointBindingRegistrationIsExactAndNonMutating(t *testing.T) {
	s, c, _, a := checkpointBoundScheduler(t)
	for range 2 {
		if err := c.SetSchedulerBindings(s, a); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		c *CheckpointContext
		s *Scheduler
		a *checkpoint.BindingAuthority
	}{
		{nil, s, a}, {c, nil, a}, {c, s, nil},
		{c, &Scheduler{}, a}, {c, s, checkpoint.NewBindingAuthority()},
	} {
		if err := tc.c.SetSchedulerBindings(tc.s, tc.a); err == nil {
			t.Fatal("accepted absent or conflicting registration")
		}
	}
	pathCheckpointBytes(t, s, c) // Failed registrations left the expectation intact.
	checkpointBindingRefused(t, &Scheduler{}, c, "paths.bindings")
	checkpointBindingRefused(t, s, NewCheckpointContext(), "paths.search")
	old := NewScheduler(checkpointBindingSearch, checkpointBindingPublish)
	oldContext := NewCheckpointContext()
	if err := oldContext.SetSchedulerBindings(old, a); err != nil {
		t.Fatal(err)
	}
	checkpointBindingRefused(t, old, oldContext, "paths.search")
}

func TestCheckpointNilBindingsRemainAbsent(t *testing.T) {
	s, c, _, a := checkpointBoundScheduler(t)
	s.SetSearchWithCheckpointBinding(nil, a)
	s.SetPublishWithCheckpointBinding(nil, a)
	s.SetCandidateProviderWithCheckpointBinding(nil, a)
	if s.checkpointSearchAuthority != nil || s.checkpointPublishAuthority != nil || s.checkpointProviderAuthority != nil {
		t.Fatal("nil binding retained authority")
	}
	want := pathCheckpointVector(t,
		[10]int32{}, uint8(0), int32(0), uint8(0), uint32(0), uint8(0), uint32(0),
		int64(3), int64(0), uint8(0), uint8(0), [10]int32{}, uint8(0),
		[10]int32{}, int32(1333), int32(42),
		uint16(9), uint32(0), uint16(10), uint32(0))
	if got := pathCheckpointBytes(t, s, c); !bytes.Equal(got, want) {
		t.Fatal("removing callbacks changed residual fields or absent framing")
	}
	if got := pathCheckpointBytes(t, s, NewCheckpointContext()); !bytes.Equal(got, want) {
		t.Fatal("absent fixture required registration")
	}
	empty := NewSchedulerWithCheckpointBindings(nil, nil, a)
	if empty.checkpointSearchAuthority != nil || empty.checkpointPublishAuthority != nil {
		t.Fatal("constructor stamped absent callbacks")
	}
}

func TestCheckpointProviderInstallationKeepsOrderAndClearsBeforeCalls(t *testing.T) {
	for _, canonical := range []bool{false, true} {
		s, _, _, a := checkpointBoundScheduler(t)
		s.playerCount, s.unitLimit = 8, 99
		p := &checkpointBindingProvider{}
		p.installed = func(call string) {
			if s.provider != p || s.checkpointProviderAuthority != nil {
				t.Fatal("provider callback saw stale binding or authority")
			}
			wantPlayers := 8
			if call == "limit" {
				wantPlayers = 3
			}
			if s.playerCount != wantPlayers || s.unitLimit != 99 {
				t.Fatal("provider getter order or assignment order changed")
			}
		}
		if canonical {
			s.SetCandidateProviderWithCheckpointBinding(p, a)
		} else {
			s.SetCandidateProvider(p)
		}
		if !slices.Equal(p.calls, []string{"players", "limit"}) || s.playerCount != 3 || s.unitLimit != 42 {
			t.Fatalf("installation = %v, %d, %d", p.calls, s.playerCount, s.unitLimit)
		}
		if got := CheckpointProviderMatches(s, p, a); got != canonical {
			t.Fatalf("provider match = %v, want %v", got, canonical)
		}
	}
}

func TestCheckpointProviderAliasIsTypedAndNeverCalled(t *testing.T) {
	s, _, p, a := checkpointBoundScheduler(t)
	p.installed = func(string) { t.Fatal("alias check called getter") }
	if !CheckpointProviderMatches(s, p, a) { // Type arguments must infer normally.
		t.Fatal("canonical provider did not match")
	}
	if CheckpointProviderMatches(nil, p, a) || CheckpointProviderMatches(s, (*checkpointBindingProvider)(nil), a) ||
		CheckpointProviderMatches(s, &checkpointBindingProvider{}, a) || CheckpointProviderMatches(s, p, nil) ||
		CheckpointProviderMatches(s, p, checkpoint.NewBindingAuthority()) {
		t.Fatal("accepted absent, foreign or wrong provider binding")
	}
	foreign := checkpointSliceProvider{checkpointBindingProvider: &checkpointBindingProvider{}, values: []int{1}}
	s.SetCandidateProviderWithCheckpointBinding(foreign, a)
	foreign.installed = func(string) { t.Fatal("alias check called foreign getter") }
	if CheckpointProviderMatches(s, p, a) {
		t.Fatal("noncomparable foreign provider matched")
	}
	s.SetCandidateProviderWithCheckpointBinding(nil, a)
	if CheckpointProviderMatches(s, p, a) {
		t.Fatal("absent provider matched")
	}
}
