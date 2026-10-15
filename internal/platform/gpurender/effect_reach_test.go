package gpurender

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// TestEmitterReachStaysWithinViewBound locks this executor's light and blast
// ring reaches within the bounds the recorder's view bound trusts
// (drawlist.Sprite.Reach, drawlist.Line.Reach). A reach that outgrew them
// would light or distort the view from an effect the recorder had already
// dropped as unseen, and the light would switch off at the screen edge.
func TestEmitterReachStaysWithinViewBound(t *testing.T) {
	white := [4]byte{255, 255, 255, 255}
	for _, scale := range []float32{0.5, 1, 1.5, 2} {
		for _, kind := range []drawlist.SpriteLightingKind{drawlist.SpriteLightingExplosion, drawlist.SpriteLightingFire, drawlist.SpriteLightingProjectile, drawlist.SpriteLightingSpark} {
			for _, side := range []uint16{1, 4096} {
				r := &Renderer{}
				r.lighting.colors = make(map[*formats.GAFFrame][3]float32)
				r.lighting.recordW, r.lighting.recordH = 1<<20, 1<<20
				r.displayPalette[7] = white
				// Art far larger than any clamp, and an explosion sequence
				// larger still, so each family reaches its widest light.
				sp := drawlist.Sprite{Frame: &formats.GAFFrame{Width: side, Height: side, Pixels: []byte{7}},
					X: 1000, Y: 1000, LightingKind: kind, LightingScale: scale, LightingSize: 1 << 16}
				r.addSpriteLight(sp)
				if len(r.lighting.lights) != 1 {
					t.Fatalf("kind %d scale %v: %d lights gathered", kind, scale, len(r.lighting.lights))
				}
				if got := r.lighting.lights[0].radius; got > sp.Reach() {
					t.Errorf("kind %d scale %v side %d: light radius %v beyond the view bound's %v", kind, scale, side, got, sp.Reach())
				}
			}
		}
		for _, known := range []bool{false, true} {
			for size := float32(0); size <= 4096; size += 7 {
				sp := drawlist.Sprite{LightingKind: drawlist.SpriteLightingExplosion, LightingScale: scale, BlastSize: size}
				for age := float32(0); age < blastTicks; age += 0.25 {
					radius, width, _ := dynamicBlastShape(age, size, scale, 1<<20, 1<<20, known)
					if radius+width > sp.Reach() {
						t.Fatalf("size %v age %v scale %v: blast ring reaches %v beyond the view bound's %v", size, age, scale, radius+width, sp.Reach())
					}
				}
			}
		}
		var list drawlist.List
		line := drawlist.Line{X1: 1 << 16, Index: 7, Emissive: true, LightingScale: scale}
		list.RecordLine(line)
		r := &Renderer{}
		r.lighting.recordW, r.lighting.recordH = 1<<20, 1<<20
		r.displayPalette[7] = white
		r.prepareStrokeLighting(&list)
		if len(r.lighting.lights) != 1 {
			t.Fatalf("scale %v: %d stroke lights gathered", scale, len(r.lighting.lights))
		}
		if got := r.lighting.lights[0].radius; got > line.Reach() {
			t.Errorf("scale %v: stroke light radius %v beyond the view bound's %v", scale, got, line.Reach())
		}
	}
}
