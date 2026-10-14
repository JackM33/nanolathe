package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The strategic view offers the terrain at the smallest power-of-two density
// at or above the factor (DESIGN_GPU_RENDERER §16.15), only to the modern
// recording and only below the strategic cut; everywhere else the record is
// the tile pass's.
func TestStrategicTerrainLevel(t *testing.T) {
	c := zoomRecorderClient(t)
	c.SetTerrain(&world.Terrain{CellW: 8, CellH: 8, TileIndices: make([]uint16, 16), TileSet: make([][1024]byte, 1)})
	c.cam.Scale = camera.ViewScaleNative
	recorded := func(z camera.Zoom, modern bool) uint8 {
		t.Helper()
		c.cam.Zoom, c.geometryOnlyModels = z, modern
		c.list.Reset()
		c.drawTerrainPrep()
		var capture scaleCapture
		c.list.Replay(&capture)
		if len(capture.terrain) != 1 {
			t.Fatalf("terrain commands = %d", len(capture.terrain))
		}
		return capture.terrain[0].Level
	}
	for _, tc := range []struct {
		z      camera.Zoom
		modern bool
		want   uint8
	}{
		{camera.ZoomUnit, true, 0},
		{camera.ZoomUnit * 5 / 8, true, 0},
		{camera.ZoomUnit / 2, true, 1},
		{camera.ZoomUnit * 3 / 8, true, 1},
		{camera.ZoomUnit / 4, true, 2},
		{camera.ZoomUnit * 3 / 25, true, 3},
		{camera.ZoomUnit / 16, true, 4},
		{camera.ZoomUnit / 32, true, 5},
		{camera.ZoomUnit / 64, true, 5},
		{camera.ZoomUnit / 4, false, 0},
	} {
		if got := recorded(tc.z, tc.modern); got != tc.want {
			t.Errorf("zoom %d/%d modern=%v: terrain level %d, want %d", tc.z, camera.ZoomUnit, tc.modern, got, tc.want)
		}
	}
}
