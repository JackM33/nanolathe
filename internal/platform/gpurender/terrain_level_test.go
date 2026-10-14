package gpurender

import (
	"fmt"
	"math/rand"
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// terrainLevelTexel is the reference for one level texel: the rounded mean of
// the PAL colours of the 2^level × 2^level tile pixels it covers.
func terrainLevelTexel(tile *[terrainTilePixels]byte, pal *[256][4]byte, level, x, y int) [3]byte {
	k := 1 << level
	var s [3]int
	for j := 0; j < k; j++ {
		for i := 0; i < k; i++ {
			e := pal[tile[(y*k+j)*terrainTileSize+x*k+i]]
			s[0], s[1], s[2] = s[0]+int(e[0]), s[1]+int(e[1]), s[2]+int(e[2])
		}
	}
	n := k * k
	return [3]byte{byte((s[0] + n/2) / n), byte((s[1] + n/2) / n), byte((s[2] + n/2) / n)}
}

// levelTexelAt reads cell texel (x, y) of a level built by terrainLevelPixels.
func levelTexelAt(levels *[terrainLevelMax + 1][]byte, cols *[terrainLevelMax + 1]int, level, cell, x, y int) [3]byte {
	side := terrainTileSize >> level
	w := cols[level] * side
	p := (((cell/cols[level])*side+y)*w + (cell%cols[level])*side + x) * 4
	b := levels[level]
	return [3]byte{b[p], b[p+1], b[p+2]}
}

// Every level texel of every tile is the rounded mean of the pixels it covers,
// though the builder folds each level from the one before in place
// (§16.15); the cell after the last tile is the void, PAL[0].
func TestTerrainLevelsAverageTheirPixels(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	var pal [256][4]byte
	for i := range pal {
		pal[i] = [4]byte{byte(rng.Intn(256)), byte(rng.Intn(256)), byte(rng.Intn(256)), 0}
	}
	tiles := make([][terrainTilePixels]byte, 5)
	for i := range tiles {
		for p := range tiles[i] {
			tiles[i][p] = byte(rng.Intn(256))
		}
	}
	levels, cols, ok := terrainLevelPixels(tiles, &pal, 4096)
	if !ok {
		t.Fatal("five tiles did not fit a 4096 page")
	}
	for l := 1; l <= terrainLevelMax; l++ {
		side := terrainTileSize >> l
		for cell := range tiles {
			for y := 0; y < side; y++ {
				for x := 0; x < side; x++ {
					if got, want := levelTexelAt(&levels, &cols, l, cell, x, y), terrainLevelTexel(&tiles[cell], &pal, l, x, y); got != want {
						t.Fatalf("level %d tile %d texel (%d,%d) = %v, want %v", l, cell, x, y, got, want)
					}
				}
			}
		}
		if got, want := levelTexelAt(&levels, &cols, l, len(tiles), 0, 0), [3]byte{pal[0][0], pal[0][1], pal[0][2]}; got != want {
			t.Fatalf("level %d void cell = %v, want PAL[0] %v", l, got, want)
		}
	}
	if _, _, ok := terrainLevelPixels(tiles, &pal, 16); ok {
		t.Fatal("a level grid wider than the device's maximum was accepted")
	}
}

// terrainLevelFixtureMap is a 4×3-tile map of three distinct tiles and one
// index past the tile set.
func terrainLevelFixtureMap() *world.Terrain {
	tiles := make([][terrainTilePixels]byte, 3)
	for p := range tiles[0] {
		x, y := p%terrainTileSize, p/terrainTileSize
		tiles[0][p] = byte(16 + (x*7+y*3)%64)
		tiles[1][p] = byte(200 - (x^y)%40)
		if (x+y)%2 == 0 {
			tiles[2][p] = 40
		} else {
			tiles[2][p] = 160
		}
	}
	return &world.Terrain{CellW: 8, CellH: 6, TileSet: tiles,
		TileIndices: []uint16{0, 1, 2, 0, 2, 2, 1, 9, 1, 0, 0, 2}}
}

// checkTerrainLevelDevicePixels draws the fixture map in a world region at a
// quarter scale from level two (§16.15), after a frame of tiles that starts
// the build. At exactly the level's density each pixel centre is a level
// texel's centre, so the bilinear draw returns that texel: the mean of the
// 4×4 tile pixels under it, through PAL. A display palette change rebuilds
// the level.
func checkTerrainLevelDevicePixels() error {
	pal := fixturePalette()
	const w, h = 32, 24
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	m := terrainLevelFixtureMap()
	build := func(level uint8) *drawlist.List {
		var list drawlist.List
		list.RecordClear()
		list.RecordWorld(drawlist.WorldSpace{Begin: true, Factor: 0.25, Step: camera.ViewScaleNative, RecordW: 4 * w, RecordH: 4 * h})
		list.RecordTerrain(drawlist.Terrain{Terrain: m, DstW: 4 * w, DstH: 4 * h, Level: level})
		list.RecordWorld(drawlist.WorldSpace{})
		list.RecordExpand()
		return &list
	}
	check := func(display *[256][4]byte, what string) error {
		p := make([]byte, w*h*4)
		r.Execute(build(2), w, h).ReadPixels(p)
		if r.modelStats.TerrainLevel != 2 {
			return fmt.Errorf("terrain level fixture (%s): drew from level %d, want 2", what, r.modelStats.TerrainLevel)
		}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				id := int(m.TileIndices[(y/8)*4+x/8])
				var want [3]byte
				if id < len(m.TileSet) {
					want = terrainLevelTexel(&m.TileSet[id], display, 2, x%8, y%8)
				} else {
					want = [3]byte{display[0][0], display[0][1], display[0][2]}
				}
				i := (y*w + x) * 4
				for ch := 0; ch < 3; ch++ {
					if d := int(p[i+ch]) - int(want[ch]); d < -1 || d > 1 {
						return fmt.Errorf("terrain level fixture (%s): pixel (%d,%d) is %v, want %v", what, x, y, p[i:i+3], want)
					}
				}
			}
		}
		return nil
	}
	r.Execute(build(0), w, h)
	if r.modelStats.TerrainLevel != 0 || r.terrainLevels.pending == nil {
		return fmt.Errorf("terrain level fixture: a frame of tiles drew from level %d and started no build", r.modelStats.TerrainLevel)
	}
	if err := check(&pal.Base, "grey ramp"); err != nil {
		return err
	}
	var inverted [256][4]byte
	for i := range inverted {
		inverted[i] = [4]byte{byte(255 - i), byte(i / 2), byte(i), 255}
	}
	r.SetDisplayPalette(inverted)
	return check(&inverted, "after a display palette change")
}

// TestTerrainLevelDevicePixels names the terrain levels' device check; the
// hidden loop in TestMain runs it.
func TestTerrainLevelDevicePixels(t *testing.T) {
	if os.Getenv("NANOLATHE_GPU_DEVICE_TEST") != "1" {
		t.Skip("set NANOLATHE_GPU_DEVICE_TEST=1 for real-device terrain level fixtures")
	}
	if deviceFixtureResult != nil {
		t.Fatalf("device fixture loop: %v", deviceFixtureResult)
	}
}
