package visibility

import (
	"bytes"
	"encoding/hex"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Only tiny authored tables enter the real freezer. No retail files or
// manually manufactured content identities are needed by this owner.
func visibilityCheckpointFixture(t *testing.T) (*Service, *CheckpointContext) {
	t.Helper()
	cat := &content.Catalog{
		Sight: &content.SightShapes{Shapes: []content.SightShape{{W: 1, H: 1, Opaque: []bool{true}}}},
		// The declared count deliberately differs from stored sections (docs/SPEC_CONFLICTS.md SC9).
		LOS: &content.LOSTables{NumTables: 1, Tables: []content.LOSTable{
			{TableNum: 1, NumLines: 1, Lines: [][]int32{{1, 0, 1}}},
			{TableNum: 2, NumLines: 0},
		}},
	}
	fs := vfs.New()
	t.Cleanup(func() { fs.Close() })
	sources, err := content.CaptureSimulationSources(fs, "")
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := content.FreezeSimulationInputs(sources, content.SimulationInputRequest{Catalog: cat})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := inputs.CheckpointKeys()
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{
		Community: CommunityState{AlliedJammingIgnored: true, OffMapAircraftMarginTiles: -2},
		W:         2, H: 1, local: 9, mode: Mode(0x8000000f),
		rayTables: cat.LOS, shapes: cat.Sight,
		terrain: &world.Terrain{}, viewerDefeated: true,
		wordMask: []uint16{0x8001, 0x02ff},
		team:     [10]uint16{1, 2, 4, 8, 16, 32, 64, 128, 256, 0xffff},
		footprints: map[ObserverID]footprint{
			256: {cx: 6, cz: -7, heightByte: 8, live: false, owner: 9, quantized: 10, radius: -11, storedByte: 12, storedCX: 13, storedCZ: -14},
			2:   {cx: -1, cz: 2, heightByte: 3, live: true, owner: 4, quantized: -5, radius: 6, storedByte: 7, storedCX: -8, storedCZ: 9},
		},
	}
	for player := range s.byteGrids {
		s.byteGrids[player] = []byte{byte(player), 255 - byte(player)}
	}
	s.wide[0] = []uint64{0, 1 << 40}
	return s, NewCheckpointContext(keys)
}

func visibilityCheckpointBytes(t *testing.T, s *Service, c *CheckpointContext) []byte {
	t.Helper()
	for pass := 0; pass < 2; pass++ {
		if added, err := s.CollectCheckpointReferences(c); added != 0 || err != nil {
			t.Fatalf("collection pass %d: added %d, error %v", pass, added, err)
		}
	}
	var out bytes.Buffer
	if err := s.WriteCheckpoint(checkpoint.NewEncoder(&out), c); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestVisibilityCheckpointByteVector(t *testing.T) {
	s, c := visibilityCheckpointFixture(t)
	// Independent positional vector: little-endian source widths, u32 slice
	// lengths, fixed arrays without lengths, numeric observer-key order, and
	// presence plus admitted definitions. This is the payload, not a section.
	const vector = "000100feffffffffffffff" + // Community
		"010000000002000000" + // H, Rules, W
		"0200000000ff0200000001fe0200000002fd0200000003fc0200000004fb" +
		"0200000005fa0200000006f90200000007f80200000008f70200000009f6" + // ten grids
		"02000000" + // footprints
		"02000000ffffffff02000000030104fbffffff0600000007f8ffffff09000000" +
		"0001000006000000f9ffffff0800090a000000f5ffffff0c0d000000f2ffffff" +
		"0907000080" + // local, complete mode except fog-valid
		"010100000000030000006c6f73" + // ray-table presence, family, ordinal, key
		"0101000000000b0000007369676874736861706573" + // shapes
		"010002000400080010002000400080000001ffff" + // teams
		"0101" + // terrain presence, viewer-defeated
		"0200000000000000000000000000000000010000" + // wide[0], including a count above uint32
		"000000000000000000000000000000000000000000000000000000000000000000000000" + // nine absent rows
		"020000000180ff02" // word-mask count and row-major values
	want, err := hex.DecodeString(vector)
	if err != nil {
		t.Fatal(err)
	}
	got := visibilityCheckpointBytes(t, s, c)
	if !bytes.Equal(got, want) {
		t.Fatalf("payload = %x\nwant      %x", got, want)
	}
}

func TestVisibilityCheckpointRetainsStoredState(t *testing.T) {
	s, c := visibilityCheckpointFixture(t)
	baseline := visibilityCheckpointBytes(t, s, c)
	for _, test := range []struct {
		name string
		edit func(*Service)
	}{
		{"Community.AlliedJammingIgnored", func(v *Service) { v.Community.AlliedJammingIgnored = false }},
		{"Community.OffMapAircraftMarginTiles", func(v *Service) { v.Community.OffMapAircraftMarginTiles++ }},
		{"H", func(v *Service) { v.H++ }},
		{"W", func(v *Service) { v.W++ }},
		{"byteGrids.playerOrder", func(v *Service) { v.byteGrids[0], v.byteGrids[9] = v.byteGrids[9], v.byteGrids[0] }},
		{"byteGrids.rowOrder", func(v *Service) { slices.Reverse(v.byteGrids[4]) }},
		{"local", func(v *Service) { v.local-- }},
		{"mode.history", func(v *Service) { v.mode ^= ModeHistoryEnabled }},
		{"mode.current", func(v *Service) { v.mode ^= ModeCurrentEnabled }},
		{"mode.raster", func(v *Service) { v.mode ^= ModeTerrainRay }},
		{"mode.unclassified", func(v *Service) { v.mode ^= Mode(1 << 31) }},
		{"rayTables.presence", func(v *Service) { v.rayTables = nil }},
		{"shapes.presence", func(v *Service) { v.shapes = nil }},
		{"team", func(v *Service) { v.team[9]-- }},
		{"terrain.presence", func(v *Service) { v.terrain = nil }},
		{"viewerDefeated", func(v *Service) { v.viewerDefeated = false }},
		{"wide.playerOrder", func(v *Service) { v.wide[0], v.wide[9] = v.wide[9], v.wide[0] }},
		{"wide.rowOrder", func(v *Service) { slices.Reverse(v.wide[0]) }},
		{"wide.highBits", func(v *Service) { v.wide[0][1] += 1 << 32 }},
		{"wide.presence", func(v *Service) { v.wide[0] = nil }},
		{"wordMask.rowOrder", func(v *Service) { slices.Reverse(v.wordMask) }},
		{"wordMask.unclassified", func(v *Service) { v.wordMask[0] ^= 0x8000 }},
		{"footprints.key", func(v *Service) { v.footprints[3] = v.footprints[2]; delete(v.footprints, 2) }},
		{"footprints.presence", func(v *Service) { delete(v.footprints, 256) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := cloneVisibilityCheckpointFixture(s)
			test.edit(value)
			if bytes.Equal(visibilityCheckpointBytes(t, value, c), baseline) {
				t.Fatal("retained mutation did not change payload")
			}
		})
	}
	for _, test := range []struct {
		name string
		edit func(*footprint)
	}{
		{"cx", func(f *footprint) { f.cx++ }},
		{"cz", func(f *footprint) { f.cz++ }},
		{"heightByte", func(f *footprint) { f.heightByte++ }},
		{"live", func(f *footprint) { f.live = !f.live }},
		{"owner", func(f *footprint) { f.owner-- }},
		{"quantized", func(f *footprint) { f.quantized++ }},
		{"radius", func(f *footprint) { f.radius++ }},
		{"storedByte", func(f *footprint) { f.storedByte++ }},
		{"storedCX", func(f *footprint) { f.storedCX++ }},
		{"storedCZ", func(f *footprint) { f.storedCZ++ }},
	} {
		t.Run("inactiveFootprint."+test.name, func(t *testing.T) {
			value := cloneVisibilityCheckpointFixture(s)
			f := value.footprints[256] // Inactive records must not lose residuals.
			test.edit(&f)
			value.footprints[256] = f
			if bytes.Equal(visibilityCheckpointBytes(t, value, c), baseline) {
				t.Fatal("inactive footprint mutation did not change payload")
			}
		})
	}
}

func TestVisibilityCheckpointOrderExclusionsAndPurity(t *testing.T) {
	s, c := visibilityCheckpointFixture(t)
	baseline := visibilityCheckpointBytes(t, s, c)
	// Reverse insertion order and replace the terrain pointer. This owner's
	// terrain edge identifies the singleton, not its address; U6 owns alias
	// validation against the world owner (DESIGN_MULTIPLAYER §16.3.11).
	s.footprints = map[ObserverID]footprint{2: s.footprints[2], 256: s.footprints[256]}
	s.terrain = &world.Terrain{CellW: -10, CellH: -20}
	s.mode &^= ModeFogCacheValid
	s.fog = FogCache{
		w: 17, h: 18, ch0: []byte{19}, ch1: []byte{20}, originX: 21, originZ: 22,
		in: []byte{23}, inKey: fogWindowKey{24, 25, 26, 27, 28, 29}, inValid: true,
	}
	s.mappingVersion, s.fogVersion, s.presentationIdentity = 30, 31, 32
	s.rebuildingPresentation = true
	s.spokeCache = [][][]step{{{{dx: 33, dz: 34, dist: 35}}}}
	s.sensorInputs = []SensorInput{{}}
	s.sensorStatusByID = []sensorStatus{{status: 36, valid: true}}
	s.sensorIndex = sensorCandidateIndex{
		enabled: true, forceExhaustive: true, minX: 37, minZ: 38, width: 39, height: 40,
		heads: []int{41}, counts: []int{42}, next: []int{43}, order: []int{44}, candidates: []int{45},
	}
	before := cloneVisibilityCheckpointFixture(s)
	for i := 0; i < 3; i++ {
		if got := visibilityCheckpointBytes(t, s, c); !bytes.Equal(got, baseline) {
			t.Fatal("map insertion order, terrain address or excluded state changed payload")
		}
		if !reflect.DeepEqual(s, before) {
			t.Fatal("capture changed service state or caches")
		}
	}
	// An unbuilt spoke cache must remain unbuilt as well.
	s.spokeCache = nil
	visibilityCheckpointBytes(t, s, c)
	if s.spokeCache != nil {
		t.Fatal("capture populated spoke cache")
	}
}

func TestVisibilityCheckpointAbsence(t *testing.T) {
	s, c := visibilityCheckpointFixture(t)
	*s = Service{}
	got := visibilityCheckpointBytes(t, s, c)
	// Community 11, H/Rules/W 9, ten empty grids 40, empty footprint map 4,
	// local/mode 5, two table presences 2, team 20, terrain/defeat 2,
	// ten empty wide rows 40, words 4.
	if !bytes.Equal(got, make([]byte, 137)) {
		t.Fatalf("empty payload = %x; want 137 zero bytes", got)
	}
	s.footprints = make(map[ObserverID]footprint)
	s.wordMask = []uint16{}
	for i := range s.byteGrids {
		s.byteGrids[i] = []byte{}
		s.wide[i] = []uint64{}
	}
	if !bytes.Equal(visibilityCheckpointBytes(t, s, c), got) {
		t.Fatal("nil and empty sequences have different logical values")
	}
}

// A retail-arithmetic write retires the whole wide row, including counts at
// untouched cells. Equal byte grids and footprints therefore do not imply
// equal future coverage after returning to Modern (DESIGN_MULTIPLAYER §9.1).
func TestVisibilityCheckpointRetainsCoverageAcrossRuleSwitches(t *testing.T) {
	fixture, context := visibilityCheckpointFixture(t)
	build := func() *Service {
		s := New(flatTerrain(16, 0), ModeCurrentEnabled)
		s.Rules = ModernRules{}
		s.SetShapes(fixture.shapes)
		for id := ObserverID(1); id <= 300; id++ {
			s.Refresh(id, Observer{Owner: 0, CX: 3, CZ: 3, Radius: 160})
		}
		s.Refresh(301, Observer{Owner: 0, CX: 6, CZ: 6, Radius: 160})
		return s
	}
	a, b := build(), build()
	b.Rules = StrictRules{}
	b.Refresh(301, Observer{Owner: 0, CX: 7, CZ: 7, Radius: 160})
	b.Refresh(301, Observer{Owner: 0, CX: 6, CZ: 6, Radius: 160})
	b.Rules = ModernRules{}
	if !reflect.DeepEqual(a.byteGrids, b.byteGrids) || !reflect.DeepEqual(a.footprints, b.footprints) {
		t.Fatal("the distant round trip did not preserve bytes and footprints")
	}
	if bytes.Equal(visibilityCheckpointBytes(t, a, context), visibilityCheckpointBytes(t, b, context)) {
		t.Fatal("checkpoint omitted the effective coverage counts")
	}
	a.RetireObserver(1)
	b.RetireObserver(1)
	idx := int(3*a.W + 3)
	if a.ByteGrid(0)[idx] != 255 || b.ByteGrid(0)[idx] != 254 {
		t.Fatal("the fixture no longer exposes different future coverage")
	}
}

type checkpointPanicRules struct{}

func (*checkpointPanicRules) JammerSuppresses(*Service, PlayerID, PlayerID) bool {
	panic("checkpoint invoked rules")
}
func (*checkpointPanicRules) Visible(*Service, PlayerID, Target) bool {
	panic("checkpoint invoked rules")
}
func (*checkpointPanicRules) MainViewRadarDots() bool { panic("checkpoint invoked rules") }
func (*checkpointPanicRules) CoverageCountSaturates() bool {
	panic("checkpoint invoked rules")
}

func TestVisibilityCheckpointRefusesUnattestedBindings(t *testing.T) {
	s, c := visibilityCheckpointFixture(t)
	for _, test := range []struct {
		name string
		path string
		edit func(*Service, **CheckpointContext)
	}{
		{"nil context", "visibility.context", func(_ *Service, c **CheckpointContext) { *c = nil }},
		{"nil keys", "visibility.context", func(_ *Service, c **CheckpointContext) { *c = NewCheckpointContext(nil) }},
		{"custom rules", "Rules", func(v *Service, _ **CheckpointContext) { v.Rules = &checkpointPanicRules{} }},
		{"typed nil rules", "Rules", func(v *Service, _ **CheckpointContext) { v.Rules = (*checkpointPanicRules)(nil) }},
		{"alliance reader", "Community.Allied", func(v *Service, _ **CheckpointContext) {
			v.Community.AlliedJammingIgnored = false // Disabled readers still need attestation.
			v.Community.SetAllied(func(PlayerID, PlayerID) bool { panic("checkpoint invoked alliance") })
		}},
		{"off-map reader", "Community.OffMap", func(v *Service, _ **CheckpointContext) {
			v.Community.SetOffMap(func(uint16) bool { panic("checkpoint invoked off-map reader") })
		}},
		{"foreign shapes", "shapes", func(v *Service, _ **CheckpointContext) { copy := *v.shapes; v.shapes = &copy }},
		{"foreign ray tables", "rayTables", func(v *Service, _ **CheckpointContext) { copy := *v.rayTables; v.rayTables = &copy }},
	} {
		t.Run(test.name, func(t *testing.T) {
			value, context := cloneVisibilityCheckpointFixture(s), c
			test.edit(value, &context)
			added, err := value.CollectCheckpointReferences(context)
			if added != 0 || err == nil || !strings.Contains(err.Error(), test.path) {
				t.Fatalf("collection = %d, %v; want %s refusal", added, err, test.path)
			}
			// A writer must reject independently even if collection was skipped.
			var out bytes.Buffer
			e := checkpoint.NewEncoder(&out)
			if err := value.WriteCheckpoint(e, context); err == nil || !strings.Contains(err.Error(), test.path) || e.Err() == nil {
				t.Fatalf("writer = %v; want sticky %s refusal", err, test.path)
			}
			if out.Len() != 0 {
				t.Fatal("unsupported binding wrote a partial payload")
			}
		})
	}
	var absent *Service
	if _, err := absent.CollectCheckpointReferences(c); err == nil {
		t.Fatal("nil receiver accepted as a present owner")
	}
	var out bytes.Buffer
	if err := absent.WriteCheckpoint(checkpoint.NewEncoder(&out), c); err == nil || out.Len() != 0 {
		t.Fatal("nil receiver wrote a present owner")
	}
}

// Detached mutable stores make the purity assertion catch in-place writes as
// well as replaced slice headers. The content and terrain edges stay shared:
// their pointer identities are unchanged by this read-only test fixture.
func cloneVisibilityCheckpointFixture(s *Service) *Service {
	out := *s
	out.wordMask = slices.Clone(s.wordMask)
	for i := range s.byteGrids {
		out.byteGrids[i] = slices.Clone(s.byteGrids[i])
		out.wide[i] = slices.Clone(s.wide[i])
	}
	out.footprints = maps.Clone(s.footprints)
	out.fog.ch0, out.fog.ch1, out.fog.in = slices.Clone(s.fog.ch0), slices.Clone(s.fog.ch1), slices.Clone(s.fog.in)
	out.spokeCache = slices.Clone(s.spokeCache)
	for i, table := range s.spokeCache {
		out.spokeCache[i] = slices.Clone(table)
		for j, spoke := range table {
			out.spokeCache[i][j] = slices.Clone(spoke)
		}
	}
	out.sensorInputs = slices.Clone(s.sensorInputs)
	out.sensorStatusByID = slices.Clone(s.sensorStatusByID)
	out.sensorIndex.heads = slices.Clone(s.sensorIndex.heads)
	out.sensorIndex.counts = slices.Clone(s.sensorIndex.counts)
	out.sensorIndex.next = slices.Clone(s.sensorIndex.next)
	out.sensorIndex.order = slices.Clone(s.sensorIndex.order)
	out.sensorIndex.candidates = slices.Clone(s.sensorIndex.candidates)
	return &out
}
