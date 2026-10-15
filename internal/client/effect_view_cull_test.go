package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// The view bound drops an effect only when nothing an executor builds from it
// reaches the record extent (effect_view_cull.go). An explosion beside the
// view still lights and distorts its edge, so it stays recorded until its
// anchor lies farther out than its reach; a hidden explosion lights nothing
// and a smoke puff only receives light, so each goes as soon as its own frame
// leaves the view. Every one of them still counts as drawn.
func TestEffectViewBoundKeepsReachingEmitters(t *testing.T) {
	c := stripTestClient(t) // a 64 x 64 view and 2 x 2 art
	c.cam.X, c.cam.Z = 400, 400
	seen := &frame.Frame{Visibility: frame.VisibilityView{Valid: true, W: 32, H: 32, CoverageBytes: true, Visible: make([]uint8, 32*32)}}
	for i := range seen.Visibility.Visible {
		seen.Visibility.Visible[i] = 1
	}
	hidden := &frame.Frame{Visibility: frame.VisibilityView{Valid: true, W: 32, H: 32, CoverageBytes: true, Visible: make([]uint8, 32*32)}}
	reach := int64(drawlist.BlastRingReach) // no blast size, so the ring outreaches the light
	recorded := func() int {
		n := 0
		c.list.VisitSprites(func(drawlist.Sprite) { n++ })
		c.list.Reset()
		return n
	}
	// past is how far beyond the right edge of the view the anchor stands.
	explosion := func(past int64, visibility *frame.Frame) int {
		options := c.effectDrawOptions()
		options.LightingFrame = visibility
		v := frame.EffectView{Kind: frame.KindExplosion.String(), Graphic: "smoke 1", AssetID: "fx", ActiveA: true,
			X: numeric.FixedFromInt(400 + 64 + past), Z: numeric.FixedFromInt(420)}
		if stats := c.DrawEffectViews([]frame.EffectView{v}, options); stats.Sprites != 1 {
			t.Fatalf("explosion %d past the edge counted %d sprites, want 1", past, stats.Sprites)
		}
		return recorded()
	}
	if got := explosion(reach-1, seen); got != 1 {
		t.Fatalf("an explosion whose reach covers the edge recorded %d sprites, want 1", got)
	}
	if got := explosion(reach+1, seen); got != 0 {
		t.Fatalf("an explosion beyond its reach recorded %d sprites, want 0", got)
	}
	if got := explosion(1, hidden); got != 0 {
		t.Fatalf("a hidden explosion outside the view recorded %d sprites, want 0", got)
	}
	puff := func(past int64) int {
		v := frame.StripView{Family: frame.StripFamilySmokePuff, Bank: stripTestBankName, Entry: "smoke 1",
			X: numeric.FixedFromInt(400 + 64 + past), Z: numeric.FixedFromInt(420)}
		if !c.blitStripFrame(v) {
			t.Fatalf("a puff %d past the edge did not count as blitted", past)
		}
		return recorded()
	}
	if got := puff(-1); got != 1 {
		t.Fatalf("a puff overlapping the edge recorded %d sprites, want 1", got)
	}
	if got := puff(0); got != 0 {
		t.Fatalf("a puff wholly outside the view recorded %d sprites, want 0", got)
	}
}
