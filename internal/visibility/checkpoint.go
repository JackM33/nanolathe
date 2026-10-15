package visibility

import (
	"fmt"
	"slices"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// CheckpointContext carries this capture's admitted immutable identities.
// Visibility owns no object-reference tables (DESIGN_MULTIPLAYER §16.3.11).
type CheckpointContext struct {
	Keys *content.CheckpointKeys

	bindingService   *Service
	bindingTerrain   *world.Terrain
	bindingAuthority *checkpoint.BindingAuthority
}

// NewCheckpointContext binds admitted keys without reading or refreshing state.
func NewCheckpointContext(keys *content.CheckpointKeys) *CheckpointContext {
	return &CheckpointContext{Keys: keys}
}

// CollectCheckpointReferences validates the immutable edges and composition
// boundary. Visibility adds no graph objects (DESIGN_MULTIPLAYER §16.3.11).
func (s *Service) CollectCheckpointReferences(c *CheckpointContext) (int, error) {
	_, _, err := s.checkpointBindings(c)
	return 0, err
}

// WriteCheckpoint emits only the visibility payload. The expanded Service
// field order is Community, H, Rules, W, byteGrids, footprints, local, mode,
// rayTables, shapes, team, terrain, viewerDefeated, wide, wordMask. Community order
// is Allied, AlliedJammingIgnored, OffMap, OffMapAircraftMarginTiles. Rules use
// the closed stateless tag; readers use absent 0 or verified-present 1.
// The terrain edge is singleton presence. A registered capture validates its
// exact terrain identity, without writing terrain data or pointer identity
// (DESIGN_MULTIPLAYER §16.3.49).
//
// The mode excludes only ModeFogCacheValid. Fog, spoke and sensor caches,
// sensor diagnostics, presentation identities/versions and rebuild flags are
// excluded, never rebuilt here (DESIGN_MULTIPLAYER §16.3.5–§16.3.6, §16.3.11).
// Wide coverage rows are retained even under Strict: a later switch can
// consume them, and the byte grids cannot recover a saturated count.
func (s *Service) WriteCheckpoint(e *checkpoint.Encoder, c *CheckpointContext) error {
	e.Field("visibility.Service")
	rayTables, shapes, err := s.checkpointBindings(c)
	if err != nil {
		e.Fail(err)
		return e.Err()
	}
	e.Field("visibility.Service.Community.Allied")
	e.Bool(s.Community.allied != nil)
	e.Field("visibility.Service.Community.AlliedJammingIgnored")
	e.Bool(s.Community.AlliedJammingIgnored)
	e.Field("visibility.Service.Community.OffMap")
	e.Bool(s.Community.offMap != nil)
	e.Field("visibility.Service.Community.OffMapAircraftMarginTiles")
	e.I64(int64(s.Community.OffMapAircraftMarginTiles))
	e.Field("visibility.Service.H")
	e.I32(s.H)
	e.Field("visibility.Service.Rules")
	rulesKind, err := CheckpointRulesKind(s.Rules)
	e.Fail(err)
	e.U8(rulesKind)
	e.Field("visibility.Service.W")
	e.I32(s.W)
	e.Field("visibility.Service.byteGrids")
	// The ten-player array has no count; each stored row-major grid does.
	for player, grid := range s.byteGrids {
		e.Field(fmt.Sprintf("visibility.Service.byteGrids[%d]", player))
		e.Bytes(grid)
	}
	e.Field("visibility.Service.footprints")
	e.Count(len(s.footprints))
	for _, id := range s.checkpointObserverIDs() {
		path := fmt.Sprintf("visibility.Service.footprints[%d]", id)
		e.Field(path)
		e.U32(uint32(id))
		writeCheckpointFootprint(e, s.footprints[id], path)
	}
	e.Field("visibility.Service.local")
	e.U8(uint8(s.local))
	e.Field("visibility.Service.mode")
	e.U32(uint32(s.mode &^ ModeFogCacheValid))
	e.Field("visibility.Service.rayTables")
	e.Bool(s.rayTables != nil)
	if s.rayTables != nil {
		e.Definition(rayTables)
	}
	e.Field("visibility.Service.shapes")
	e.Bool(s.shapes != nil)
	if s.shapes != nil {
		e.Definition(shapes)
	}
	e.Field("visibility.Service.team")
	for _, bits := range s.team {
		e.U16(bits)
	}
	e.Field("visibility.Service.terrain")
	e.Bool(s.terrain != nil)
	e.Field("visibility.Service.viewerDefeated")
	e.Bool(s.viewerDefeated)
	e.Field("visibility.Service.wide")
	for player, row := range s.wide {
		e.Field(fmt.Sprintf("visibility.Service.wide[%d]", player))
		e.Count(len(row))
		for _, count := range row {
			e.U64(count)
		}
	}
	e.Field("visibility.Service.wordMask")
	e.Count(len(s.wordMask))
	for _, bits := range s.wordMask {
		e.U16(bits)
	}
	return e.Err()
}

// Footprint order is cx, cz, heightByte, live, owner, quantized, radius,
// storedByte, storedCX, storedCZ. Inactive footprints retain the same fields:
// a mode change can reinterpret the saved record [03 R-VIS-01 §1][03 R-VIS-01 §2].
func writeCheckpointFootprint(e *checkpoint.Encoder, f footprint, path string) {
	e.Field(path + ".cx")
	e.I32(f.cx)
	e.Field(path + ".cz")
	e.I32(f.cz)
	e.Field(path + ".heightByte")
	e.U8(f.heightByte)
	e.Field(path + ".live")
	e.Bool(f.live)
	e.Field(path + ".owner")
	e.U8(uint8(f.owner))
	e.Field(path + ".quantized")
	e.I32(f.quantized)
	e.Field(path + ".radius")
	e.I32(f.radius)
	e.Field(path + ".storedByte")
	e.U8(f.storedByte)
	e.Field(path + ".storedCX")
	e.I32(f.storedCX)
	e.Field(path + ".storedCZ")
	e.I32(f.storedCZ)
}

func (s *Service) checkpointObserverIDs() []ObserverID {
	ids := make([]ObserverID, 0, len(s.footprints))
	for id := range s.footprints {
		ids = append(ids, id)
	}
	// The map is read only in ascending numeric key order [I1].
	slices.Sort(ids)
	return ids
}

func (s *Service) checkpointBindings(c *CheckpointContext) (rayTables, shapes checkpoint.Definition, err error) {
	if s == nil {
		return rayTables, shapes, visibilityCheckpointError("visibility.Service", "a present service")
	}
	if c == nil || c.Keys == nil {
		return rayTables, shapes, visibilityCheckpointError("visibility.context", "admitted content keys")
	}
	if err := s.validateCheckpointOwnerBindings(c); err != nil {
		return rayTables, shapes, err
	}
	if _, err := CheckpointRulesKind(s.Rules); err != nil {
		return rayTables, shapes, err
	}
	if s.rayTables != nil {
		rayTables, err = c.Keys.LOSTables(s.rayTables)
		if err != nil {
			return rayTables, shapes, fmt.Errorf("%w: %w", visibilityCheckpointError("visibility.Service.rayTables", "the admitted ray-table object"), err)
		}
	}
	if s.shapes != nil {
		shapes, err = c.Keys.SightShapes(s.shapes)
		if err != nil {
			return rayTables, shapes, fmt.Errorf("%w: %w", visibilityCheckpointError("visibility.Service.shapes", "the admitted sprite-mask object"), err)
		}
	}
	return rayTables, shapes, nil
}

func visibilityCheckpointError(path, expected string) error {
	return fmt.Errorf("nanolathe: visibility checkpoint failed: logical path %s, providers searched [frozen simulation inputs], expected %s", path, expected)
}
