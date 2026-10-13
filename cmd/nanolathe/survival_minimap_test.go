package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/render"
)

type survivalMinimapTrace struct {
	minimapSurfaceTrace
	batches [][]drawlist.Point
}

func (s *survivalMinimapTrace) Points(p drawlist.Points) {
	s.batches = append(s.batches, append([]drawlist.Point(nil), p.Points...))
}

func TestSurvivalMinimapConsumesCommittedWarning(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 20))
	t.Cleanup(b.cl.Close)
	withMinimap(b)
	h := b.hud
	h.pal = &palette.Tables{}
	h.pal.Logical[14], h.pal.Logical[0] = 73, 41
	layout := camera.LayoutMinimap(640, 320)
	h.radar = render.NewMinimapService(render.MinimapServiceConfig{
		MapW: 1, MapH: 1,
		Picture: &render.RadarSurface{W: int(layout.W), H: int(layout.H), Pitch: int(layout.W), Bits: make([]byte, layout.W*layout.H)},
	})
	cur := b.sess.Snapshot.BeginWrite()
	cur.Visibility = frame.VisibilityView{W: 1, H: 1, Valid: true, WordVisible: []uint16{1}, Visible: []uint8{1}}
	if err := b.sess.Snapshot.Publish(1); err != nil {
		t.Fatal(err)
	}
	b.cl.SetUIStage(scaledMinimapStage{b, cur})
	record := func(status frame.SurvivalStatus) *survivalMinimapTrace {
		cur.Survival = status
		trace := &survivalMinimapTrace{}
		b.cl.RecordModernFrame().Replay(trace)
		if cur.Survival != status {
			t.Fatal("minimap draw mutated committed warning")
		}
		return trace
	}
	warning := frame.SurvivalStatus{Active: true, Phase: frame.SurvivalWarning, ApproachCount: 3,
		Approaches: [3]frame.SurvivalApproach{
			{X: 632, Z: 160, Angle: 0},
			{X: 8, Z: 160, Angle: 32768},
			{X: 320, Z: 8, Angle: 49152},
		}}
	// No director is installed in this fixture. Both online and offline HUDs
	// must draw only the supplied committed warning, never query live planning.
	for _, scale := range []int32{1, 2} {
		h.chromeScale = scale
		baseline := record(frame.SurvivalStatus{})
		marked := record(warning)
		if len(marked.surfaces) != 1 || len(marked.batches) != len(baseline.batches)+3 {
			t.Fatalf("scale %d: minimap has %d pictures and %d marker batches, want one picture and three approaches", scale, len(marked.surfaces), len(marked.batches)-len(baseline.batches))
		}
		_, dst, _ := b.minimapLayout()
		for _, batch := range marked.batches[len(baseline.batches):] {
			fill, outline := false, false
			for _, point := range batch {
				fill, outline = fill || point.Index == 73, outline || point.Index == 41
				cx, cy, _ := layout.DisplayToCanvas(point.X, point.Y, dst.X1, dst.Y1, dst.X2-dst.X1+1, dst.Y2-dst.Y1+1)
				if !layout.HitTest(cx, cy) {
					t.Fatalf("marker wrote into a letterbox at %d,%d", point.X, point.Y)
				}
			}
			if !fill || !outline {
				t.Fatal("marker did not resolve both colours through the logical palette")
			}
		}
		for _, phase := range []uint8{frame.SurvivalGrace, frame.SurvivalActive, frame.SurvivalDowntime} {
			stale := warning
			stale.Phase = phase
			if got := record(stale); len(got.batches) != len(baseline.batches) {
				t.Fatalf("phase %d painted a stale warning", phase)
			}
		}
		for _, absent := range []frame.SurvivalStatus{
			{Phase: frame.SurvivalWarning, ApproachCount: warning.ApproachCount, Approaches: warning.Approaches},
			{Active: true, Phase: frame.SurvivalWarning},
		} {
			if got := record(absent); len(got.batches) != len(baseline.batches) {
				t.Fatal("inactive or unplanned warning painted a marker")
			}
		}
	}
	h.radar = nil
	if got := record(warning); len(got.batches) != 0 || len(got.surfaces) != 0 {
		t.Fatal("warning painted without a radar picture")
	}
	h.minimapAnchorOK = false
	if got := record(warning); len(got.batches) != 0 || len(got.surfaces) != 0 {
		t.Fatal("warning painted without a minimap rectangle")
	}
}
