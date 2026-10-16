package visibility

import (
	"fmt"
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// replayRayTables builds nine LOS tables whose spokes run in eight directions,
// so a terrain-ray walk's visited set depends on the horizon test at every
// step, not just on the radius.
func replayRayTables() *content.LOSTables {
	dirs := [][2]int32{{1, 0}, {1, 1}, {0, 1}, {-1, 1}, {-1, 0}, {-1, -1}, {0, -1}, {1, -2}}
	lt := &content.LOSTables{NumTables: 9}
	for r := int32(1); r <= 9; r++ {
		table := content.LOSTable{TableNum: int(r)}
		for _, d := range dirs {
			line := []int32{r}
			for k := int32(1); k <= r; k++ {
				line = append(line, d[0]*k, d[1]*k)
			}
			table.Lines = append(table.Lines, line)
		}
		table.NumLines = int32(len(table.Lines))
		lt.Tables = append(lt.Tables, table)
	}
	return lt
}

// TestRemovalReplayMatchesWalkingRemoval drives two services through the same
// long sequence of refreshes and retirements. One removes footprints by
// replaying their recorded cells, the other has its records dropped after
// every operation, so every removal walks. Every grid, count and presentation
// word must agree after every operation, under Strict's wrapping bytes and
// Modern's saturating counts, for both rasters, with a shared vision team and
// stacks deep enough to wrap a byte.
func TestRemovalReplayMatchesWalkingRemoval(t *testing.T) {
	for _, rules := range []Rules{StrictRules{}, ModernRules{}} {
		for _, ray := range []bool{false, true} {
			t.Run(fmt.Sprintf("%T/ray=%v", rules, ray), func(t *testing.T) {
				terrain := flatTerrain(256, 0)
				seed := uint32(12345)
				next := func(n uint32) uint32 {
					seed = seed*1103515245 + 12345
					return (seed >> 8) % n
				}
				for z := int32(0); z < 128; z++ {
					for x := int32(0); x < 128; x++ {
						lo := uint8(next(40))
						terrain.SetLOSHeightWord(x, z, lo, lo+uint8(next(20)))
					}
				}
				build := func() *Service {
					mode := ModeHistoryEnabled | ModeCurrentEnabled
					if ray {
						mode |= ModeTerrainRay
					}
					s := New(terrain, mode)
					s.SetShapes(fixtureShapes())
					s.SetRayTables(replayRayTables())
					s.Rules = rules
					s.SetLocal(0)
					s.SetVisionTeam([]PlayerID{0, 2})
					return s
				}
				replay, walk := build(), build()
				compare := func(step int, what string) {
					t.Helper()
					if !slices.EqualFunc(replay.byteGrids[:], walk.byteGrids[:], func(a, b []uint8) bool { return slices.Equal(a, b) }) {
						t.Fatalf("step %d (%s): byte grids differ", step, what)
					}
					if !slices.EqualFunc(replay.wide[:], walk.wide[:], func(a, b []uint64) bool { return slices.Equal(a, b) }) {
						t.Fatalf("step %d (%s): wide counts differ", step, what)
					}
					if !slices.Equal(replay.wordMask, walk.wordMask) || !slices.Equal(replay.fogDirty, walk.fogDirty) ||
						replay.mode != walk.mode || replay.mappingVersion != walk.mappingVersion || replay.fogTracked != walk.fogTracked {
						t.Fatalf("step %d (%s): word mask or presentation state differs", step, what)
					}
				}
				type pos struct{ x, z int32 }
				where := map[ObserverID]pos{}
				radii := []int32{64, 160, 200, 320}
				replayed := 0
				for step := 0; step < 6000; step++ {
					id := ObserverID(1 + next(60))
					switch op := next(100); {
					case op < 8:
						a, b := replay.RetireObserver(id), walk.RetireObserver(id)
						if a != b {
							t.Fatalf("step %d: retire dirty %v, walking %v", step, a, b)
						}
						delete(where, id)
						compare(step, "retire")
					case op < 10 && step%1500 < 60:
						// A stack on one cell: past 255 overlaps Strict's byte
						// wraps and Modern's saturates.
						for k := ObserverID(0); k < 300; k++ {
							ob := Observer{Owner: 0, CX: 60 + int32(next(2)), CZ: 60, HeightByte: uint8(10 + next(30)), Radius: 160}
							replay.Refresh(1000+k, ob)
							walk.Refresh(1000+k, ob)
						}
						walk.dropRemovalRecords()
						compare(step, "stack")
					default:
						p := where[id]
						if next(10) == 0 || p == (pos{}) {
							p = pos{int32(next(140)) - 6, int32(next(140)) - 6}
						} else {
							p.x += int32(next(3)) - 1
							p.z += int32(next(3)) - 1
						}
						where[id] = p
						ob := Observer{Owner: PlayerID(id % 3), CX: p.x, CZ: p.z, HeightByte: uint8(next(48)), Radius: radii[next(4)]}
						if old, ok := replay.footprints[id]; ok && old.live && replay.removalRecords[id].matches(old, ray) {
							replayed++
						}
						replay.Refresh(id, ob)
						walk.Refresh(id, ob)
						walk.dropRemovalRecords()
						compare(step, "refresh")
					}
				}
				for id := ObserverID(1); id <= 60; id++ {
					replay.RetireObserver(id)
					walk.RetireObserver(id)
				}
				for k := ObserverID(0); k < 300; k++ {
					replay.RetireObserver(1000 + k)
					walk.RetireObserver(1000 + k)
				}
				compare(-1, "retire all")
				if replayed < 1000 {
					t.Fatalf("only %d refreshes found a matching removal record; the replay path went unexercised", replayed)
				}
			})
		}
	}
}
