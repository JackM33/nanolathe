package path

import (
	"slices"
	"testing"
)

// Finishing a route must preserve the wrapped search's result and keep its
// own result usable after Release: movement releases before publication.
func TestFinishedRouteOwnsItsPoints(t *testing.T) {
	for _, kind := range []string{"straighten", "smooth"} {
		t.Run(kind, func(t *testing.T) {
			route := routeCells(Cell{0, 10}, Cell{5, 15}, Cell{10, 10}, Cell{15, 15}, Cell{20, 10})
			original := slices.Clone(route)
			base := &cannedSearch{route: route}
			cfg := SearchConfig{FootPrintX: 1, FootPrintZ: 1, PassableValue: func(Cell) uint8 { return 3 }}
			var search Search = &straightenSearch{Search: base, cfg: cfg}
			if kind == "smooth" {
				search = &smoothSearch{Search: base, cfg: cfg}
			}
			if _, _, done := search.Resume(100); done {
				t.Fatal("published before wrapped search finished")
			}
			points, status, done := search.Resume(100)
			if !done || len(points) >= len(route) {
				t.Fatalf("finishing pass did not shorten route: %v, done %v", points, done)
			}
			if !slices.Equal(route, original) {
				t.Fatal("finishing changed the wrapped search's result")
			}
			want, pops := slices.Clone(points), search.Popped()
			search.Release()
			clear(route)
			again, againStatus, againDone := search.Resume(100)
			if !slices.Equal(points, want) || !slices.Equal(again, want) || againStatus != status || !againDone || search.Popped() != pops {
				t.Fatal("released result or repeated completion changed")
			}
		})
	}
}

func TestFinishedRouteSurvivesWorkspaceReuse(t *testing.T) {
	for _, kernel := range []struct {
		name string
		k    Kernel
	}{{"straighten", StraightenKernel{}}, {"smooth", SmoothKernel{}}} {
		t.Run(kernel.name, func(t *testing.T) {
			var workspace Workspace
			cfg := SearchConfig{Start: Cell{1, 1}, Goal: PointGoal(Cell{25, 11}, 0), Scale: 65536,
				FootPrintX: 1, FootPrintZ: 1, HasBounds: true, Bounds: Rect{Min: Cell{}, Max: Cell{64, 64}},
				Workspace: &workspace, PassableValue: func(Cell) uint8 { return 3 }}
			finish := func(s Search) []Point {
				t.Helper()
				for i := 0; i < 100; i++ {
					points, _, done := s.Resume(100)
					if done {
						if len(points) < 2 {
							t.Fatalf("search did not produce a route: %v", points)
						}
						return points
					}
				}
				t.Fatal("search did not finish")
				return nil
			}
			first := kernel.k.NewSession(cfg)
			points := finish(first)
			want, pops := slices.Clone(points), first.Popped()
			first.Release()
			cfg.Goal = PointGoal(Cell{11, 25}, 0)
			second := kernel.k.NewSession(cfg)
			finish(second)
			second.Release()
			if !slices.Equal(points, want) || !slices.Equal(finish(first), want) || first.Popped() != pops {
				t.Fatal("another search changed the released route")
			}
		})
	}
}

// Most admission failures never reach a finishing pass. Keep this benchmark
// separate from a completed route so storage costs are visible in both cases.
func BenchmarkSearchFinishingStorage(b *testing.B) {
	for _, kernel := range []struct {
		name string
		k    Kernel
	}{{"retail", RetailKernel{}}, {"straighten", StraightenKernel{}}, {"smooth", SmoothKernel{}}} {
		for _, completed := range []bool{false, true} {
			name := "rejected"
			if completed {
				name = "route"
			}
			b.Run(kernel.name+"/"+name, func(b *testing.B) {
				var workspace Workspace
				cfg := SearchConfig{Start: Cell{1, 1}, Scale: 65536, FootPrintX: 1, FootPrintZ: 1,
					HasBounds: true, Bounds: Rect{Min: Cell{}, Max: Cell{64, 64}}, Workspace: &workspace,
					PassableValue: func(Cell) uint8 { return 3 }}
				if completed {
					cfg.Goal = PointGoal(Cell{25, 11}, 0)
				}
				b.ReportAllocs()
				for b.Loop() {
					s := kernel.k.NewSession(cfg)
					for {
						_, _, done := s.Resume(100)
						if done {
							break
						}
					}
					s.Release()
				}
			})
		}
	}
}
