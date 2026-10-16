package gpurender

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// glowPacket is one glow-layer quad. The constructors below build it from
// recorded commands; images are opaque bindings used only by submit, so the
// constructors make no GPU call.
type glowPacket struct {
	x, y, sx, sy [4]float32
	color        [4]float32
	custom       [4][4]float32
}

func (q glowPacket) submit(g *glowLayer, images [4]*ebiten.Image) {
	g.quad(images, q.x, q.y, q.sx, q.sy, q.color, q.custom)
}
func glowRect(x0, y0, x1, y1, sx0, sy0, sx1, sy1 float32, color, custom [4]float32) glowPacket {
	return glowPacket{x: [4]float32{x0, x1, x0, x1}, y: [4]float32{y0, y0, y1, y1}, sx: [4]float32{sx0, sx1, sx0, sx1}, sy: [4]float32{sy0, sy0, sy1, sy1}, color: color, custom: [4][4]float32{custom, custom, custom, custom}}
}
func (r *Renderer) glowLinePacket(l drawlist.Line) glowPacket {
	s := &r.sched
	hw := max(float32(glowLineWidth)*0.5*r.noteGlowViewScale(), 1)
	x, y := glowStrokeCorners(s.txx(float32(l.X0)+0.5), s.txy(float32(l.Y0)+0.5), s.txx(float32(l.X1)+0.5), s.txy(float32(l.Y1)+0.5), hw)
	return glowPacket{x: x, y: y, color: [4]float32{float32(l.Index), glowGain * r.weaponGlowScale(), 0, 0}}
}
func (r *Renderer) glowSpritePacket(f *formats.GAFFrame, x, y, cx, cy, cw, ch int, gain, family, atlasX, atlasY float32) (glowPacket, bool) {
	r.noteGlowViewScale()
	minX, minY := max(cx, 0), max(cy, 0)
	maxX, maxY := min(cx+cw, r.clipW()), min(cy+ch, r.clipH())
	x0, y0, x1, y1 := max(0, minX-x), max(0, minY-y), min(int(f.Width), maxX-x), min(int(f.Height), maxY-y)
	if x0 >= x1 || y0 >= y1 {
		return glowPacket{}, false
	}
	s := &r.sched
	return glowRect(s.txx(float32(x+x0)), s.txy(float32(y+y0)), s.txx(float32(x+x1)), s.txy(float32(y+y1)), atlasX+float32(x0), atlasY+float32(y0), atlasX+float32(x1), atlasY+float32(y1), [4]float32{0, gain * glowSpriteGain * glowGain * family, glowThreshold, 0}, [4]float32{0, 0, 0, glowOpKeyed}), true
}
func (r *Renderer) glowFlashPacket(x0, y0, x1, y1 int, sx0, sy0, sx1, sy1 float32) glowPacket {
	r.noteGlowViewScale()
	s := &r.sched
	return glowRect(s.txx(float32(x0)), s.txy(float32(y0)), s.txx(float32(x1)), s.txy(float32(y1)), sx0, sy0, sx1, sy1, [4]float32{0, glowLightGain * glowGain * r.effectGlowScale(), 0, 0}, [4]float32{0, 0, 0, glowOpLight})
}
func (r *Renderer) glowHaloPacket(x0, y0, x1, y1 int, high, lx0, ly0, lx1, ly1, r2 float32) glowPacket {
	r.noteGlowViewScale()
	s := &r.sched
	q := glowRect(s.txx(float32(x0)), s.txy(float32(y0)), s.txx(float32(x1)), s.txy(float32(y1)), 0, 0, 0, 0, [4]float32{high, glowLightGain * glowGain * r.effectGlowScale(), 0, 0}, [4]float32{})
	q.custom = [4][4]float32{{lx0, ly0, r2, glowOpHalo}, {lx1, ly0, r2, glowOpHalo}, {lx0, ly1, r2, glowOpHalo}, {lx1, ly1, r2, glowOpHalo}}
	return q
}
func (r *Renderer) glowNanoPacket(f drawlist.Fill) glowPacket {
	pad := drawlist.NanoGlowPad * nanoScale(f)
	s := &r.sched
	return glowRect(s.txx(max(float32(f.Rect.X)-pad, 0)), s.txy(max(float32(f.Rect.Y)-pad, 0)), s.txx(min(float32(f.Rect.X+f.Rect.W)+pad, float32(r.clipW()))), s.txy(min(float32(f.Rect.Y+f.Rect.H)+pad, float32(r.clipH()))), 0, 0, 0, 0, [4]float32{float32(f.Index), nanoGlowGain * glowGain * r.nanoFamilyScale(), 0, 0}, [4]float32{})
}
