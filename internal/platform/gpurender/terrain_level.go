package gpurender

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Strategic terrain levels (docs/DESIGN_GPU_RENDERER.md §16.15). Below the
// strategic cut the tile pass draws one quad per visible tile, and the whole
// map is in view: 65,536 quads for a 256×256-tile map at 0.12×, recorded,
// transformed and submitted every frame to be shrunk to a few pixels each. A
// terrain record carrying a Level is drawn instead as one quad over the map,
// whose fragments look the map up directly: the tile under the texel from an
// image of the tile map, then the texel from the tile set averaged down to
// that level, 2^-Level texels per record pixel.
//
// A level texel never straddles two tiles — a tile is 32 record pixels and a
// level texel 2 to 32 — so the map averaged down is exactly its tiles averaged
// down, and the averaged tile set is the whole of the level's memory: a 16×16,
// 8×8, 4×4, 2×2 and 1×1 copy of every tile, a third of the native tile set's
// texels. Each copy averages the PAL colours of the record pixels it covers,
// so the map at a level is the map supersampled at the size it is seen, where
// the tile pass blends four of the native texels under each pixel. The copies
// are built on the CPU, once per tile set and display palette; the display
// palette is an input, so a gamma change rebuilds them. The build takes a few
// milliseconds for a retail tile set and some tens for the largest, so it runs
// on its own goroutine from the first terrain frame at any factor, and the
// strategic view normally finds it done; a strategic frame that arrives first
// waits for it rather than drawing a level of the wrong colours.
//
// The draw blends the four level texels around each sample point, each
// resolved through its own tile, and clamps at the map's edge as the tile
// atlas's duplicated border does, so the levels tile the plane with no seam.
const terrainLevelMax = 5

// terrainLevelSet is one tile set's levels at one display palette, and the
// build in flight.
type terrainLevelSet struct {
	terrain *world.Terrain
	palette [256][4]byte
	// tileMap is the tile map as an image, the tile index in red (low byte)
	// and green (high byte).
	tileMap *ebiten.Image
	// levels[l] holds every tile averaged to 32>>l texels a side, tile i at
	// cell (i%cols[l], i/cols[l]); the cell after the last tile is the void.
	levels [terrainLevelMax + 1]*ebiten.Image
	cols   [terrainLevelMax + 1]int
	// ready is whether the images hold (terrain, palette); failed marks a
	// tile set this device cannot hold, whose terrain draws tiles.
	ready, failed bool
	// pending is the build of (pendingTerrain, pendingPalette) in flight, nil
	// when there is none. Its channel is buffered, so a build overtaken by a
	// new map or palette finishes into it and is dropped.
	pending        chan terrainLevelBuild
	pendingTerrain *world.Terrain
	pendingPalette [256][4]byte
}

// terrainLevelBuild is one finished build's pixels.
type terrainLevelBuild struct {
	tileMap []byte
	levels  [terrainLevelMax + 1][]byte
	cols    [terrainLevelMax + 1]int
	ok      bool
}

// release hands every image of the set to retire.
func (s *terrainLevelSet) release(retire func(*ebiten.Image)) {
	retire(s.tileMap)
	for _, img := range s.levels {
		retire(img)
	}
}

// terrainLevel draws a terrain record from its level (§16.15) and reports
// whether it did; a record it cannot serve is drawn as tiles.
func (r *Renderer) terrainLevel(c drawlist.Terrain, dstW, dstH int) bool {
	level := int(c.Level)
	t := c.Terrain
	if level <= 0 || level > terrainLevelMax || !c.Scale.Norm().Native() || r.scene2D == nil {
		return false
	}
	tilesW, tilesH := int(t.CellW/2), int(t.CellH/2)
	set := r.terrainLevelSet(t, tilesW, tilesH, true)
	if set == nil {
		return false
	}
	// The map's record rectangle, clipped to the record extent as each tile
	// is.
	mx0, my0 := -int(c.OriginX), -int(c.OriginY)
	mx1, my1 := mx0+tilesW*terrainTileSize, my0+tilesH*terrainTileSize
	x0, y0 := max(mx0, 0), max(my0, 0)
	x1, y1 := min(mx1, dstW), min(my1, dstH)
	if x0 >= x1 || y0 >= y1 {
		return true
	}
	if !r.sched.begin(schedOpaque, x0, y0, x1, y1, [4]*ebiten.Image{2: set.tileMap, 3: set.levels[level]}) {
		return true
	}
	// The source lane carries level texel coordinates: record pixel x is map
	// pixel x-mx0, which is level texel (x-mx0)/k.
	k := float32(int(1) << level)
	r.sched.quad(schedOpaque,
		float32(x0), float32(y0), float32(x1), float32(y1),
		float32(x0-mx0)/k, float32(y0-my0)/k, float32(x1-mx0)/k, float32(y1-my0)/k,
		[4]float32{float32(int(terrainTileSize) >> level), float32(set.cols[level]), float32(tilesW), float32(tilesH)},
		[4]float32{0, 0, 0, sceneOpTerrainLevel})
	r.modelStats.TerrainLevel = level
	return true
}

// terrainLevelSet returns the levels of t at the current display palette, or
// nil while they are not built. It starts a build when none matches, and
// installs a finished one; need waits for the build, which only a frame that
// draws from a level does.
func (r *Renderer) terrainLevelSet(t *world.Terrain, tilesW, tilesH int, need bool) *terrainLevelSet {
	s := &r.terrainLevels
	pal := r.displayPalette
	if s.terrain == t && s.palette == pal && (s.ready || s.failed) {
		if s.failed {
			return nil
		}
		return s
	}
	if s.pending == nil || s.pendingTerrain != t || s.pendingPalette != pal {
		maxSide := terrainAtlasMaxSide()
		ch := make(chan terrainLevelBuild, 1)
		s.pending, s.pendingTerrain, s.pendingPalette = ch, t, pal
		go func() { ch <- buildTerrainLevels(t, tilesW, tilesH, pal, maxSide) }()
	}
	var b terrainLevelBuild
	if need {
		b = <-s.pending
	} else {
		select {
		case b = <-s.pending:
		default:
			return nil
		}
	}
	s.pending = nil
	r.installTerrainLevels(t, pal, tilesW, tilesH, b)
	if s.failed {
		return nil
	}
	return s
}

// buildTerrainLevels is a build's CPU half; it reads only the tile set, which
// is immutable after load, and its own copy of the palette.
func buildTerrainLevels(t *world.Terrain, tilesW, tilesH int, pal [256][4]byte, maxSide int) terrainLevelBuild {
	if tilesW <= 0 || tilesH <= 0 || tilesW > maxSide || tilesH > maxSide || len(t.TileIndices) < tilesW*tilesH || len(t.TileSet) >= 1<<16 {
		return terrainLevelBuild{}
	}
	b := terrainLevelBuild{tileMap: terrainTileMapPixels(t, tilesW, tilesH)}
	b.levels, b.cols, b.ok = terrainLevelPixels(t.TileSet, &pal, maxSide)
	return b
}

// installTerrainLevels uploads a finished build as the set's images.
func (r *Renderer) installTerrainLevels(t *world.Terrain, pal [256][4]byte, tilesW, tilesH int, b terrainLevelBuild) {
	s := &r.terrainLevels
	if s.terrain != t {
		s.release(func(img *ebiten.Image) {
			if img != nil {
				img.Deallocate()
			}
		})
		s.tileMap, s.levels, s.cols = nil, [terrainLevelMax + 1]*ebiten.Image{}, [terrainLevelMax + 1]int{}
	}
	s.terrain, s.palette, s.ready, s.failed = t, pal, false, !b.ok
	if !b.ok {
		return
	}
	if s.tileMap == nil {
		s.tileMap = newRendererImage(tilesW, tilesH)
		s.tileMap.WritePixels(b.tileMap)
	}
	for l := 1; l <= terrainLevelMax; l++ {
		side := terrainTileSize >> l
		rows := (len(t.TileSet) + 1 + b.cols[l] - 1) / b.cols[l]
		w, h := b.cols[l]*side, rows*side
		if img := s.levels[l]; img == nil || img.Bounds().Dx() != w || img.Bounds().Dy() != h {
			if img != nil {
				img.Deallocate()
			}
			s.levels[l] = newRendererImage(w, h)
		}
		s.levels[l].WritePixels(b.levels[l])
		s.cols[l] = b.cols[l]
	}
	s.ready = true
}

// terrainTileMapPixels is the tile map as RGBA: the tile index's low byte in
// red and high byte in green. An index past the tile set, which the tile pass
// leaves at the cleared void index, names the void cell after the last tile.
func terrainTileMapPixels(t *world.Terrain, tilesW, tilesH int) []byte {
	buf := make([]byte, tilesW*tilesH*4)
	for i := 0; i < tilesW*tilesH; i++ {
		id := t.TileIndices[i]
		if int(id) >= len(t.TileSet) {
			id = uint16(len(t.TileSet))
		}
		buf[i*4], buf[i*4+1], buf[i*4+3] = byte(id), byte(id>>8), 255
	}
	return buf
}

// terrainLevelPixels averages every tile down to each level, in the RGBA
// layout terrainLevelSet holds, from the PAL colour of each tile pixel: a
// level texel is the rounded mean of the 4^level pixels it covers. The cell
// after the last tile is the void, PAL[0]. It reports false when a level's
// cell grid does not fit maxSide.
func terrainLevelPixels(tiles [][terrainTilePixels]byte, pal *[256][4]byte, maxSide int) (levels [terrainLevelMax + 1][]byte, cols [terrainLevelMax + 1]int, ok bool) {
	cells := len(tiles) + 1
	for l := 1; l <= terrainLevelMax; l++ {
		side := terrainTileSize >> l
		c := 1
		for c*c < cells {
			c++
		}
		rows := (cells + c - 1) / c
		if c*side > maxSide || rows*side > maxSide {
			return levels, cols, false
		}
		cols[l] = c
		levels[l] = make([]byte, c*side*rows*side*4)
	}
	// sums holds one tile's per-channel sums at the current level, 16×16 at
	// level one, folded in place into each coarser level.
	var sums [16 * 16][3]uint32
	put := func(l, cell int, sums *[16 * 16][3]uint32) {
		side := terrainTileSize >> l
		w := cols[l] * side
		gx, gy := (cell%cols[l])*side, (cell/cols[l])*side
		n := uint32(1) << (2 * l)
		for y := 0; y < side; y++ {
			for x := 0; x < side; x++ {
				s := &sums[y*16+x]
				p := ((gy+y)*w + gx + x) * 4
				levels[l][p] = byte((s[0] + n/2) / n)
				levels[l][p+1] = byte((s[1] + n/2) / n)
				levels[l][p+2] = byte((s[2] + n/2) / n)
				levels[l][p+3] = 255
			}
		}
	}
	for cell := 0; cell < cells; cell++ {
		for y := 0; y < 16; y++ {
			for x := 0; x < 16; x++ {
				var s [3]uint32
				for j := 0; j < 2; j++ {
					for i := 0; i < 2; i++ {
						idx := byte(0)
						if cell < len(tiles) {
							idx = tiles[cell][(2*y+j)*terrainTileSize+2*x+i]
						}
						e := pal[idx]
						s[0] += uint32(e[0])
						s[1] += uint32(e[1])
						s[2] += uint32(e[2])
					}
				}
				sums[y*16+x] = s
			}
		}
		put(1, cell, &sums)
		for l := 2; l <= terrainLevelMax; l++ {
			side := terrainTileSize >> l
			for y := 0; y < side; y++ {
				for x := 0; x < side; x++ {
					var s [3]uint32
					for _, q := range [4]int{(2*y)*16 + 2*x, (2*y)*16 + 2*x + 1, (2*y+1)*16 + 2*x, (2*y+1)*16 + 2*x + 1} {
						s[0] += sums[q][0]
						s[1] += sums[q][1]
						s[2] += sums[q][2]
					}
					// Row y of the coarser level is written after rows 2y and
					// 2y+1 of the finer one are read, so folding in place
					// never reads a sum it already replaced.
					sums[y*16+x] = s
				}
			}
			put(l, cell, &sums)
		}
	}
	return levels, cols, true
}
