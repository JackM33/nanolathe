package visibility

import (
	"bytes"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// fullFogReference derives s's fog from scratch on a separate service holding
// copies of s's grids, local player and mode, for the given window.
func fullFogReference(t *testing.T, s *Service, terrain *world.Terrain, window [4]int32) *FogCache {
	t.Helper()
	ref := New(terrain, s.Mode()&^ModeFogCacheValid)
	copy(ref.wordMask, s.wordMask)
	for p := range s.byteGrids {
		copy(ref.byteGrids[p], s.byteGrids[p])
	}
	ref.local = s.local
	ref.mode &^= ModeFogCacheValid
	ref.fog.inValid = false
	if window == [4]int32{} {
		ref.RebuildFog(0, 0)
	} else {
		ref.RebuildFogWindow(window[0], window[1], window[2], window[3])
	}
	return ref.Fog()
}

// Re-deriving only the cells whose inputs changed must publish the same bytes
// as the full rebuild, tick after tick, while sight footprints move, history
// accumulates, the viewer and the current-coverage mode change, explored
// memory is merged, and the window alternates between the whole map and a
// viewport [03 §3.3]. The revision moves exactly when the bytes do.
//
// Coverage is written through the service's own per-cell writers — the
// increments, decrements and history sets every raster walk performs — so the
// marks the incremental rebuild relies on are the ones production writes, in
// both the retail wrapping and the Modern saturating arithmetic, and with a
// vision team stamping into two players' grids.
func TestIncrementalFogMatchesFullRebuild(t *testing.T) {
	for _, saturates := range []bool{false, true} {
		t.Run(map[bool]string{false: "wrapping", true: "saturating"}[saturates], func(t *testing.T) {
			incrementalFogMatchesFullRebuild(t, saturates)
		})
	}
}

func incrementalFogMatchesFullRebuild(t *testing.T, saturates bool) {
	const cells = 24 // visibility tiles; the terrain grid is twice that
	terrain := &world.Terrain{CellW: cells * 2, CellH: cells * 2}
	s := New(terrain, ModeHistoryEnabled|ModeCurrentEnabled)
	s.SetVisionTeam([]PlayerID{2, 3})
	seed := uint32(99)
	next := func(n int32) int32 {
		seed = seed*1664525 + 1013904223
		return int32((seed >> 8) % uint32(n))
	}
	type mover struct {
		x, z, r int32
		owner   PlayerID
		history bool // writes history only, as a publication under a disabled current-coverage bit does
	}
	movers := []mover{{2, 2, 3, 0, false}, {20, 5, 4, 1, false}, {10, 18, 2, 0, false}, {0, 23, 3, 1, false}, {23, 0, 2, 2, false}, {12, 12, 3, 3, false}, {5, 15, 1, 0, true}, {18, 18, 1, 2, true}}
	type stamp struct {
		idx   int
		owner PlayerID
	}
	var stamped []stamp
	incremental, marked := 0, 0
	var prevWindow [4]int32
	for tick := 0; tick < 400; tick++ {
		// Retire last tick's coverage, then stamp each mover's square for its
		// owner, the way a moved sight raster is retired and republished.
		for _, st := range stamped {
			s.decByteGrid(st.idx, st.owner, saturates)
		}
		stamped = stamped[:0]
		for m := range movers {
			mv := &movers[m]
			mv.x = min(max(mv.x+next(3)-1, -2), cells+1)
			mv.z = min(max(mv.z+next(3)-1, -2), cells+1)
			for z := mv.z - mv.r; z <= mv.z+mv.r; z++ {
				for x := mv.x - mv.r; x <= mv.x+mv.r; x++ {
					if x < 0 || z < 0 || x >= cells || z >= cells {
						continue
					}
					i := int(z*cells + x)
					s.setWordBit(i, mv.owner)
					if mv.history {
						continue
					}
					s.incByteGrid(i, mv.owner, saturates)
					stamped = append(stamped, stamp{i, mv.owner})
				}
			}
		}
		switch {
		case tick%97 == 50:
			s.SetLocal(PlayerID((s.local + 1) % 4))
		case tick%61 == 30:
			s.SetMode(s.Mode() ^ ModeCurrentEnabled)
		case tick%89 == 70:
			s.SetViewingPlayer(PlayerID((s.local + 3) % 4))
		case tick%113 == 90:
			s.CopyExplored(1, s.local)
		}
		var window [4]int32
		if tick%40 >= 30 {
			window = [4]int32{160, 96, 320, 256}
		}
		s.mode &^= ModeFogCacheValid
		takesIncremental := s.fog.inValid && window == prevWindow
		if takesIncremental && s.fogTracked && s.fog.inLocal == s.local && s.fog.inCurrent == s.mode.CurrentEnabled() {
			marked++
		}
		prevWindow = window
		version := s.FogVersion()
		before0 := append([]uint8(nil), s.fog.ch0...)
		before1 := append([]uint8(nil), s.fog.ch1...)
		if window == [4]int32{} {
			s.RebuildFog(0, 0)
		} else {
			s.RebuildFogWindow(window[0], window[1], window[2], window[3])
		}
		if takesIncremental {
			incremental++
		}
		want := fullFogReference(t, s, terrain, window)
		got := s.Fog()
		if got.w != want.w || got.h != want.h || got.originX != want.originX || got.originZ != want.originZ {
			t.Fatalf("tick %d: window %dx%d@%d,%d, full rebuild %dx%d@%d,%d", tick, got.w, got.h, got.originX, got.originZ, want.w, want.h, want.originX, want.originZ)
		}
		if !bytes.Equal(got.ch0, want.ch0) || !bytes.Equal(got.ch1, want.ch1) {
			t.Fatalf("tick %d: incremental fog differs from the full rebuild", tick)
		}
		same := bytes.Equal(before0, got.ch0) && bytes.Equal(before1, got.ch1)
		if takesIncremental && same && s.FogVersion() != version {
			t.Fatalf("tick %d: unchanged fog bytes advanced the revision", tick)
		}
		if !same && s.FogVersion() == version {
			t.Fatalf("tick %d: changed fog bytes kept the revision", tick)
		}
	}
	if incremental == 0 || marked == 0 {
		t.Fatalf("incremental rebuilds %d, of them from marks %d; the comparison proves nothing", incremental, marked)
	}
}

// The marked rebuild reports a change exactly when the comparing one does:
// the two visit the changed cells in the same order, so a byte derived to a
// new value and back within one rebuild advances the revision in both.
func TestMarkedFogRevisionMatchesComparingRebuild(t *testing.T) {
	terrain := &world.Terrain{CellW: 32, CellH: 32}
	build := func() *Service {
		s := New(terrain, ModeHistoryEnabled|ModeCurrentEnabled)
		s.RebuildFog(0, 0)
		return s
	}
	marked, comparing := build(), build()
	for step := 0; step < 50; step++ {
		for _, s := range []*Service{marked, comparing} {
			i := (step * 7) % 256
			s.incByteGrid(i, 0, false)
			s.setWordBit(i, 0)
			if step%3 == 0 {
				s.decByteGrid((i+1)%256, 0, false)
			}
			s.mode &^= ModeFogCacheValid
		}
		comparing.fogTracked = false
		marked.RebuildFog(0, 0)
		comparing.RebuildFog(0, 0)
		if marked.FogVersion() != comparing.FogVersion() || !bytes.Equal(marked.fog.ch0, comparing.fog.ch0) || !bytes.Equal(marked.fog.ch1, comparing.fog.ch1) {
			t.Fatalf("step %d: marked rebuild (version %d) differs from the comparing one (version %d)", step, marked.FogVersion(), comparing.FogVersion())
		}
	}
}

// The bulk writers leave the marks incomplete, so the next rebuild compares
// every cell; the retail restore's direct word-grid install relies on this
// after RebuildAll (WordMask).
func TestBulkVisibilityWritesForceComparingFogRebuild(t *testing.T) {
	terrain := &world.Terrain{CellW: 16, CellH: 16}
	for name, write := range map[string]func(*Service){
		"RebuildAll":       func(s *Service) { s.RebuildAll(nil) },
		"RefreshMode":      func(s *Service) { s.RefreshMode(s.Mode(), true, allSlotsEligible, nil) },
		"RebuildEntry":     func(s *Service) { s.RebuildEntry(allSlotsEligible, nil) },
		"CopyExplored":     func(s *Service) { s.CopyExplored(1, 0) },
		"SetLocal":         func(s *Service) { s.SetLocal(1) },
		"SetViewingPlayer": func(s *Service) { s.SetViewingPlayer(1) },
		"SetMode":          func(s *Service) { s.SetMode(s.Mode() ^ ModeCurrentEnabled) },
	} {
		s := New(terrain, ModeHistoryEnabled|ModeCurrentEnabled)
		s.RebuildFog(0, 0)
		if !s.fogTracked {
			t.Fatalf("%s: a full fog rebuild did not start tracking", name)
		}
		write(s)
		if s.fogTracked {
			t.Fatalf("%s left the fog's cell marks complete", name)
		}
	}
}

// A write that does not come from a rebuild makes the next rebuild a full one.
func TestFogCacheWriteForcesFullRebuild(t *testing.T) {
	s := New(&world.Terrain{CellW: 16, CellH: 16}, ModeHistoryEnabled|ModeCurrentEnabled)
	s.RebuildFog(0, 0)
	s.Fog().SetChannel(3, 3, 9, 9)
	s.mode &^= ModeFogCacheValid
	s.RebuildFog(0, 0)
	if c0, c1 := s.Fog().Channel(3, 3); c0 == 9 || c1 == 9 {
		t.Fatal("a rebuild after a foreign cache write kept the foreign byte")
	}
}
