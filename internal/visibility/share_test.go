package visibility

import (
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The mapping-grid merge copies the source's explored bits into the
// destination's, one way, idempotently, and raises neither presentation dirty
// flag [05 R-SHARE-01 §6]. It has no random stream to draw from: the merge's
// only input is the grid.
func TestCopyExploredOneWayIdempotentAndQuiet(t *testing.T) {
	s := newTestService(&world.Terrain{CellW: 16, CellH: 16}, ModeHistoryEnabled|ModeCurrentEnabled)
	s.mode |= ModeFogCacheValid
	// Tile 0: source only. Tile 1: destination only. Tile 2: both. Tile 3: a
	// third player only.
	s.wordMask[0] = cellBit(1)
	s.wordMask[1] = cellBit(2)
	s.wordMask[2] = cellBit(1) | cellBit(2)
	s.wordMask[3] = cellBit(5)
	bytesBefore := slices.Clone(s.byteGrids[2])
	mode, mapping, fog := s.Mode(), s.MappingVersion(), s.FogVersion()

	s.CopyExplored(1, 2)
	want := make([]uint16, len(s.wordMask))
	want[0] = cellBit(1) | cellBit(2)
	want[1] = cellBit(2)
	want[2] = cellBit(1) | cellBit(2)
	want[3] = cellBit(5)
	if !slices.Equal(s.wordMask, want) {
		t.Fatalf("merged grid = %v, want %v", s.wordMask[:4], want[:4])
	}
	s.CopyExplored(1, 2)
	if !slices.Equal(s.wordMask, want) {
		t.Fatal("a repeated merge changed the grid")
	}
	if s.Mode() != mode || s.MappingVersion() != mapping || s.FogVersion() != fog {
		t.Fatal("the merge raised a presentation dirty flag [05 R-SHARE-01 §6]")
	}
	if !slices.Equal(s.byteGrids[2], bytesBefore) {
		t.Fatal("the merge touched a current-sight byte grid")
	}
	// Invalid slots are the no-slot sentinel and change nothing.
	s.CopyExplored(10, 2)
	s.CopyExplored(2, 10)
	if !slices.Equal(s.wordMask, want) {
		t.Fatal("an invalid slot changed the grid")
	}

	// A Mapped battle's grid is all ones; the merge leaves it so.
	mapped := newTestService(&world.Terrain{CellW: 16, CellH: 16}, ModeCurrentEnabled)
	before := slices.Clone(mapped.wordMask)
	mapped.CopyExplored(1, 2)
	if !slices.Equal(mapped.wordMask, before) {
		t.Fatal("the merge changed a Mapped grid")
	}
}

// radarShareFixture is three far-apart units with no sensors: the viewer's,
// a radar sharer's and a third owner's. Pass 1 alone decides their bits.
func radarShareFixture() ([]SensorUnit, *[3]uint32) {
	flags := &[3]uint32{0x10000000, 0x10000700, 0x10000700}
	units := []SensorUnit{
		{ID: 1, AllocationSerial: 11, Owner: 0, Status: &flags[0], X: 8 << 16, Z: 8 << 16, Alive: true},
		{ID: 2, AllocationSerial: 12, Owner: 1, Status: &flags[1], X: 200 << 16, Z: 200 << 16, Alive: true},
		{ID: 3, AllocationSerial: 13, Owner: 2, Status: &flags[2], X: 400 << 16, Z: 400 << 16, Alive: true},
	}
	return units, flags
}

// Pass 1's allied disjunct marks a radar sharer's units friendly for the
// viewer it named, and nobody else's; a zero mask is the ordinary pass
// [03 R-VIS-01 §4] pass 1 [03 R-VIS-01 §7].
func TestRadarSharersMarkOnlyTheSharersUnits(t *testing.T) {
	terrain := &world.Terrain{CellW: 64, CellH: 64}
	shared := newTestService(terrain, ModeHistoryEnabled|ModeCurrentEnabled)
	shared.EnableOwnerPerspectives()
	units, flags := radarShareFixture()
	shared.SensorTickForPerspectiveSharing(0, false, 30, 3, units, 1<<1)
	if got := shared.StatusForPerspective(0, 2, 12, 1, 0); got != FriendlyMask {
		t.Fatalf("sharer's unit for the viewer = %#x, want the friendly pair", got)
	}
	if got := shared.StatusForPerspective(0, 3, 13, 2, 0); got != 0 {
		t.Fatalf("non-sharer's unit for the viewer = %#x, want cleared", got)
	}
	// Another viewer's pass with no sharer for it leaves the sharer foreign.
	shared.SensorTickForPerspectiveSharing(2, false, 60, 3, units, 0)
	if got := shared.StatusForPerspective(2, 2, 12, 1, 0); got != 0 {
		t.Fatalf("sharer's unit for an undeclared viewer = %#x, want cleared", got)
	}
	if *flags != [3]uint32{0x10000000, 0x10000700, 0x10000700} {
		t.Fatal("a banked pass wrote its caller's status words")
	}

	// A zero mask and the unshared entry point are the same pass.
	plain := newTestService(terrain, ModeHistoryEnabled|ModeCurrentEnabled)
	zero := newTestService(terrain, ModeHistoryEnabled|ModeCurrentEnabled)
	plain.EnableOwnerPerspectives()
	zero.EnableOwnerPerspectives()
	plainUnits, _ := radarShareFixture()
	zeroUnits, _ := radarShareFixture()
	plain.SensorTickForPerspective(0, false, 30, 3, plainUnits)
	zero.SensorTickForPerspectiveSharing(0, false, 30, 3, zeroUnits, 0)
	for _, u := range plainUnits {
		if a, b := plain.StatusForPerspective(0, u.ID, u.AllocationSerial, u.Owner, 0), zero.StatusForPerspective(0, u.ID, u.AllocationSerial, u.Owner, 0); a != b {
			t.Fatalf("unit %d: zero mask %#x, plain pass %#x", u.ID, b, a)
		}
	}
	if !slices.Equal(plain.SensorInputs(), zero.SensorInputs()) {
		t.Fatal("a zero mask changed the pass's diagnostic snapshot")
	}
}
