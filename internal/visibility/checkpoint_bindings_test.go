package visibility

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func visibilityBindingsRefusal(t *testing.T, s *Service, c *CheckpointContext, path string) {
	t.Helper()
	if added, err := s.CollectCheckpointReferences(c); added != 0 || err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("collection = %d, %v; want refusal at %s", added, err, path)
	}
	var out bytes.Buffer
	e := checkpoint.NewEncoder(&out)
	if err := s.WriteCheckpoint(e, c); err == nil || !strings.Contains(err.Error(), path) || e.Err() == nil || out.Len() != 0 {
		t.Fatalf("writer = %v, bytes %d; want sticky refusal at %s before bytes", err, out.Len(), path)
	}
}

func registerVisibilityBindings(t *testing.T, s *Service, c *CheckpointContext, a *checkpoint.BindingAuthority) {
	t.Helper()
	if err := c.SetBindings(s, s.terrain, a); err != nil {
		t.Fatal(err)
	}
}

func TestVisibilityBindingsPresenceVectorAndPurity(t *testing.T) {
	s, c := visibilityCheckpointFixture(t)
	*s = Service{}
	a := checkpoint.NewBindingAuthority()
	registerVisibilityBindings(t, s, c, a)
	// The independent empty-Service vector is 137 zero bytes. Each reader
	// occupies its original one-byte slot in Community; proof emits no bytes.
	want := make([]byte, 137)
	if got := visibilityCheckpointBytes(t, s, c); !bytes.Equal(got, want) {
		t.Fatalf("registered absent payload = %x", got)
	}
	s.SetAlliedWithCheckpointBinding(func(PlayerID, PlayerID) bool { panic("capture invoked allied") }, a)
	want[0] = 1
	if got := visibilityCheckpointBytes(t, s, c); !bytes.Equal(got, want) {
		t.Fatalf("allied payload = %x; want %x", got, want)
	}
	s.SetOffMapWithCheckpointBinding(func(uint16) bool { panic("capture invoked off-map") }, a)
	want[2] = 1
	if got := visibilityCheckpointBytes(t, s, c); !bytes.Equal(got, want) {
		t.Fatalf("both reader payload = %x; want %x", got, want)
	}
	s.SetAlliedWithCheckpointBinding(nil, a)
	want[0] = 0
	if got := visibilityCheckpointBytes(t, s, c); !bytes.Equal(got, want) {
		t.Fatalf("off-map payload = %x; want %x", got, want)
	}
	s.SetOffMapWithCheckpointBinding(nil, a)
	want[2] = 0
	if got := visibilityCheckpointBytes(t, s, c); !bytes.Equal(got, want) || s.Community.checkpointAllied != (checkpointReaderProof{}) || s.Community.checkpointOffMap != (checkpointReaderProof{}) {
		t.Fatal("nil canonical installs retained proof or changed the absent vector")
	}

	// A populated fixture also exercises excluded caches, immutable identities
	// and stored grids. Strip only callbacks from detached copies for equality:
	// reflect deliberately treats every nonnil function as unequal.
	s, c = visibilityCheckpointFixture(t)
	registerVisibilityBindings(t, s, c, a)
	s.SetAlliedWithCheckpointBinding(func(PlayerID, PlayerID) bool { panic("capture invoked allied") }, a)
	s.SetOffMapWithCheckpointBinding(func(uint16) bool { panic("capture invoked off-map") }, a)
	s.spokeCache = [][][]step{{{{dx: 7, dz: 8, dist: 9}}}}
	s.fog.ch0, s.fog.in, s.fog.inValid = []byte{1, 2}, []byte{3, 4}, true
	before := cloneVisibilityCheckpointFixture(s)
	before.Community.allied, before.Community.offMap = nil, nil
	contextBefore := *c
	first := visibilityCheckpointBytes(t, s, c)
	for i := 0; i < 2; i++ {
		if !bytes.Equal(first, visibilityCheckpointBytes(t, s, c)) {
			t.Fatal("repeated capture changed bytes")
		}
		after := cloneVisibilityCheckpointFixture(s)
		after.Community.allied, after.Community.offMap = nil, nil
		if !reflect.DeepEqual(before, after) || *c != contextBefore || s.Community.AlliedReader() == nil || s.Community.OffMapReader() == nil {
			t.Fatal("capture changed live state, caches, proof or context")
		}
	}
}

func TestVisibilityBindingsIndependentReplacementAndWrongProof(t *testing.T) {
	for _, slot := range []struct {
		name      string
		path      string
		canonical func(*Service, *checkpoint.BindingAuthority)
		reinstall func(*Service)
		clear     func(*Service)
		proof     func(*Service) checkpointReaderProof
		invoke    func(*Service) bool
	}{
		{"allied", "Community.Allied", func(s *Service, a *checkpoint.BindingAuthority) {
			s.SetAlliedWithCheckpointBinding(func(viewer, other PlayerID) bool { return viewer == 2 && other == 7 }, a)
		}, func(s *Service) { s.Community.SetAllied(s.Community.AlliedReader()) },
			func(s *Service) { s.Community.SetAllied(nil) },
			func(s *Service) checkpointReaderProof { return s.Community.checkpointAllied },
			func(s *Service) bool { return s.Community.AlliedReader()(2, 7) }},
		{"off-map", "Community.OffMap", func(s *Service, a *checkpoint.BindingAuthority) {
			s.SetOffMapWithCheckpointBinding(func(id uint16) bool { return id == 0xffff }, a)
		}, func(s *Service) { s.Community.SetOffMap(s.Community.OffMapReader()) },
			func(s *Service) { s.Community.SetOffMap(nil) },
			func(s *Service) checkpointReaderProof { return s.Community.checkpointOffMap },
			func(s *Service) bool { return s.Community.OffMapReader()(0xffff) }},
	} {
		t.Run(slot.name, func(t *testing.T) {
			s, c := visibilityCheckpointFixture(t)
			a := checkpoint.NewBindingAuthority()
			registerVisibilityBindings(t, s, c, a)
			// Both features are disabled; retained readers still require proof.
			s.Community.AlliedJammingIgnored, s.Community.OffMapAircraftMarginTiles = false, 0
			slot.canonical(s, a)
			baseline := visibilityCheckpointBytes(t, s, c)
			projection := s.Community
			slot.reinstall(s)
			visibilityBindingsRefusal(t, s, c, slot.path)
			if slot.proof(s) != (checkpointReaderProof{}) || !slot.invoke(s) {
				t.Fatal("ordinary same-function reinstall retained proof or changed callback behavior")
			}
			s.Community = projection // Complete projection restoration on same owner is valid.
			if !bytes.Equal(visibilityCheckpointBytes(t, s, c), baseline) {
				t.Fatal("same-owner projection restore changed bytes")
			}
			slot.canonical(s, nil)
			visibilityBindingsRefusal(t, s, c, slot.path)
			if slot.proof(s) != (checkpointReaderProof{}) || !slot.invoke(s) {
				t.Fatal("missing authority gated installation or kept old proof")
			}
			slot.canonical(s, checkpoint.NewBindingAuthority())
			visibilityBindingsRefusal(t, s, c, slot.path)
			if !slot.invoke(s) {
				t.Fatal("foreign authority changed callback behavior")
			}
			slot.canonical(s, a)
			foreignContext := NewCheckpointContext(c.Keys)
			registerVisibilityBindings(t, s, foreignContext, checkpoint.NewBindingAuthority())
			visibilityBindingsRefusal(t, s, foreignContext, slot.path)
			unregistered := NewCheckpointContext(c.Keys)
			visibilityBindingsRefusal(t, s, unregistered, slot.path)
			// Copying the whole Service or only its projection does not transfer
			// ownership. Getters and ordinary setters cannot transfer proof either.
			copied := *s
			copiedContext := NewCheckpointContext(c.Keys)
			registerVisibilityBindings(t, &copied, copiedContext, a)
			visibilityBindingsRefusal(t, &copied, copiedContext, slot.path)
			other := &Service{Community: s.Community, terrain: s.terrain}
			otherContext := NewCheckpointContext(c.Keys)
			registerVisibilityBindings(t, other, otherContext, a)
			visibilityBindingsRefusal(t, other, otherContext, slot.path)
			slot.reinstall(other)
			visibilityBindingsRefusal(t, other, otherContext, slot.path)
			visibilityCheckpointBytes(t, s, c)
			slot.clear(s)
			if slot.proof(s) != (checkpointReaderProof{}) {
				t.Fatal("nil ordinary installation kept proof")
			}
			visibilityCheckpointBytes(t, s, c)
		})
	}
}

func TestVisibilityBindingsSlotsAndScalarProjection(t *testing.T) {
	s, c := visibilityCheckpointFixture(t)
	a := checkpoint.NewBindingAuthority()
	registerVisibilityBindings(t, s, c, a)
	allied := func(PlayerID, PlayerID) bool { panic("capture called allied") }
	offMap := func(uint16) bool { panic("capture called off-map") }
	s.SetAlliedWithCheckpointBinding(allied, a)
	s.SetOffMapWithCheckpointBinding(offMap, a)
	alliedProof, offMapProof := s.Community.checkpointAllied, s.Community.checkpointOffMap
	s.Community.SetAllied(allied)
	if s.Community.checkpointOffMap != offMapProof {
		t.Fatal("allied replacement invalidated off-map proof")
	}
	s.SetOffMapWithCheckpointBinding(offMap, a)
	visibilityBindingsRefusal(t, s, c, "Community.Allied")
	s.SetAlliedWithCheckpointBinding(allied, a)
	s.Community.SetOffMap(offMap)
	if s.Community.checkpointAllied != alliedProof {
		t.Fatal("off-map replacement invalidated allied proof")
	}
	s.SetAlliedWithCheckpointBinding(allied, a)
	visibilityBindingsRefusal(t, s, c, "Community.OffMap")
	s.SetOffMapWithCheckpointBinding(offMap, a)
	baseline := visibilityCheckpointBytes(t, s, c)
	s.Community.AlliedJammingIgnored = !s.Community.AlliedJammingIgnored
	s.Community.OffMapAircraftMarginTiles++
	if s.Community.checkpointAllied != alliedProof || s.Community.checkpointOffMap != offMapProof || bytes.Equal(visibilityCheckpointBytes(t, s, c), baseline) {
		t.Fatal("scalar projection erased proof or lost retained scalar bytes")
	}
	// Reader admission cannot bless rules or immutable content from elsewhere.
	s.Rules = &checkpointPanicRules{}
	visibilityBindingsRefusal(t, s, c, "Rules")
	s.Rules = nil
	shapes := s.shapes
	shapeCopy := *shapes
	s.shapes = &shapeCopy
	visibilityBindingsRefusal(t, s, c, "shapes")
	s.shapes = shapes
	rayCopy := *s.rayTables
	s.rayTables = &rayCopy
	visibilityBindingsRefusal(t, s, c, "rayTables")
}

func TestVisibilityBindingsRegistrationAndTerrain(t *testing.T) {
	s, c := visibilityCheckpointFixture(t)
	a := checkpoint.NewBindingAuthority()
	before := *c
	for _, err := range []error{(*CheckpointContext)(nil).SetBindings(s, s.terrain, a), c.SetBindings(nil, s.terrain, a), c.SetBindings(s, s.terrain, nil)} {
		if err == nil || *c != before {
			t.Fatal("missing registration inputs accepted or changed context")
		}
	}
	registerVisibilityBindings(t, s, c, a)
	before = *c
	registerVisibilityBindings(t, s, c, a)
	terrain := s.terrain
	terrainCopy := *terrain
	for _, err := range []error{
		c.SetBindings(&Service{}, terrain, a),
		c.SetBindings(s, &terrainCopy, a),
		c.SetBindings(s, nil, a),
		c.SetBindings(s, terrain, checkpoint.NewBindingAuthority()),
	} {
		if err == nil || *c != before {
			t.Fatal("conflicting registration accepted or changed context")
		}
	}
	// Registration alone never stamps a reader installed through an ordinary API.
	s.Community.SetOffMap(func(uint16) bool { panic("capture invoked reader") })
	visibilityBindingsRefusal(t, s, c, "Community.OffMap")
	s.Community.SetOffMap(nil)
	baseline := visibilityCheckpointBytes(t, s, c)
	visibilityBindingsRefusal(t, &Service{terrain: terrain}, c, "visibility.Service")
	for _, replacement := range []*world.Terrain{&terrainCopy, nil} {
		s.terrain = replacement
		visibilityBindingsRefusal(t, s, c, "visibility.Service.terrain")
	}
	s.terrain = terrain
	if !bytes.Equal(visibilityCheckpointBytes(t, s, c), baseline) {
		t.Fatal("restoring the admitted terrain changed bytes")
	}
	// Unregistered no-reader fixtures still encode terrain presence alone.
	unregistered := NewCheckpointContext(c.Keys)
	s.terrain = &terrainCopy
	if !bytes.Equal(visibilityCheckpointBytes(t, s, unregistered), baseline) {
		t.Fatal("unregistered fixture encoded terrain identity")
	}
	*s = Service{}
	nilTerrainContext := NewCheckpointContext(c.Keys)
	registerVisibilityBindings(t, s, nilTerrainContext, a)
	if !bytes.Equal(visibilityCheckpointBytes(t, s, nilTerrainContext), make([]byte, 137)) {
		t.Fatal("registered nil terrain changed absent vector")
	}
	s.terrain = terrain
	visibilityBindingsRefusal(t, s, nilTerrainContext, "visibility.Service.terrain")
	presentTerrainContext := NewCheckpointContext(c.Keys)
	registerVisibilityBindings(t, s, presentTerrainContext, a)
	want := make([]byte, 137)
	want[91] = 1 // terrain's original singleton-presence slot
	if got := visibilityCheckpointBytes(t, s, presentTerrainContext); !bytes.Equal(got, want) {
		t.Fatalf("terrain presence payload = %x; want %x", got, want)
	}
}
