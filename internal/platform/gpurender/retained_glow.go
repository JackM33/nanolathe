package gpurender

import (
	"fmt"
	"strconv"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// The production GPU and retained CPU paths share these quad constructors.
// Images are opaque bindings used only by submit; constructors make no GPU call.
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

// RetainedGlowAtlas supplies prepared atlas TEXEL endpoints. Frame texels are
// unpremultiplied displayed RGB plus coverage alpha, independently of tinted
// command half-alpha. Flash texels contain their original row byte in red and
// coverage alpha. Callbacks must not mutate an already-uploaded atlas.
type RetainedGlowAtlas interface {
	Frame(*formats.GAFFrame) ([4]float32, error)
	Flash(drawlist.Flash) ([4]float32, error)
}

// RetainedGlowVertex is 48 bytes. PositionUV.xy is framebuffer pixels, zw atlas
// TEXELS (halo offsets for op3). Color is solid RGB/gain; Params is op,
// threshold, radius-squared, halo high lane. Six vertices form each quad.
type RetainedGlowVertex struct{ PositionUV, Color, Params [4]float32 }

// RetainedGlowParameters is 48 bytes, shared with native NMGlowParameters.
// Kernel weights use production's six-decimal shader literals, not retuning.
type RetainedGlowParameters struct {
	Weights [8]float32
	Blur    [4]float32
}
type RetainedGlowSettings struct {
	On            bool
	Strength      int
	Effects       drawlist.Effects
	Families      [3]int
	Width, Height int
	Palette       [256][4]byte
}
type RetainedGlow struct {
	cpu      Renderer
	settings RetainedGlowSettings
	vertices []RetainedGlowVertex
}

var retainedGlowWeights = func() (out [8]float32) {
	for i, v := range glowWeights() {
		rounded, _ := strconv.ParseFloat(fmt.Sprintf("%.6f", v), 32)
		out[i] = float32(rounded)
	}
	return
}()

func NewRetainedGlow(settings RetainedGlowSettings) *RetainedGlow {
	g := &RetainedGlow{}
	g.SetSettings(settings)
	return g
}
func (g *RetainedGlow) SetSettings(s RetainedGlowSettings) {
	g.settings = s
	g.cpu.SetEffects(s.Effects)
	g.cpu.SetGlow(s.On)
	g.cpu.SetGlowStrength(s.Strength)
	g.cpu.SetGlowFamilies(s.Families[0], s.Families[1], s.Families[2])
	g.cpu.displayPalette = s.Palette
	g.cpu.w, g.cpu.h = s.Width, s.Height
}

// Begin starts a source-only frame. World is the production recording transform;
// it reaches source geometry once, and its view scale controls both kernels.
func (g *RetainedGlow) Begin(w drawlist.WorldSpace) {
	g.vertices = g.vertices[:0]
	r := &g.cpu
	r.water.record.Scale = w.Step
	r.worldW, r.worldH = max(r.w, int(w.RecordW)), max(r.h, int(w.RecordH))
	r.sched.setWorld(int32(w.Zoom), int32(camera.ZoomOf(w.Step)))
	k := r.sched.worldScale
	if w.Factor > 0 {
		k = w.Factor / (float32(camera.ZoomOf(w.Step)) / float32(camera.ZoomUnit))
	}
	r.sched.setWorldTransform(k, w.OffsetX, w.OffsetY)
	r.glow.viewScale = 0
}
func (g *RetainedGlow) append(q glowPacket) {
	for _, i := range [6]int{0, 1, 2, 1, 2, 3} {
		v := RetainedGlowVertex{PositionUV: [4]float32{q.x[i], q.y[i], q.sx[i], q.sy[i]}, Color: [4]float32{0, 0, 0, q.color[1]}, Params: [4]float32{q.custom[i][3], q.color[2], q.custom[i][2], q.color[0]}}
		if int(q.custom[i][3]) == glowOpSolid {
			c := g.cpu.displayPalette[byte(q.color[0])]
			for j := 0; j < 3; j++ {
				v.Color[j] = float32(c[j]) / 255
			}
		}
		if int(q.custom[i][3]) == glowOpHalo {
			v.PositionUV[2], v.PositionUV[3] = q.custom[i][0], q.custom[i][1]
		}
		g.vertices = append(g.vertices, v)
	}
}

// Append uses replayed sprite LEAVES, never complete Art light metadata, so
// composite keyed/half-alpha decomposition remains the producer's. No cursor,
// private CRT, replay or GPU operation runs. Dynamic discs sample the completed
// world composite at native Encode, after effects and before fog/chrome.
func (g *RetainedGlow) Append(sprites []drawlist.Sprite, lines []drawlist.Line, nano []drawlist.Fill, flashes []drawlist.Flash, halos []drawlist.Halo, atlas RetainedGlowAtlas) error {
	r := &g.cpu
	if !g.settings.On || r.glow.strength <= 0 {
		return nil
	}
	for _, l := range lines {
		if l.Emissive && r.weaponGlowScale() > 0 {
			g.append(r.glowLinePacket(l))
		}
	}
	for _, f := range nano {
		if f.Nano && f.Style == drawlist.FillSolid && !f.NanoSubmerged && r.nanoInView(f) && r.nanoFamilyScale() > 0 {
			g.append(r.glowNanoPacket(f))
		}
	}
	for _, sp := range sprites {
		if !sp.Emissive || sp.Frame == nil || r.spriteGlowScale(sp.LightingKind) <= 0 {
			continue
		}
		gain := float32(1)
		if sp.Kind == drawlist.BlitTinted {
			gain = 0.5
		} else if sp.Kind != drawlist.BlitKeyed || !sp.Anchored {
			continue
		}
		if atlas == nil {
			return fmt.Errorf("retained glow: emissive art requires prepared atlas")
		}
		uv, err := atlas.Frame(sp.Frame)
		if err != nil {
			return err
		}
		cx, cy, cw, ch := 0, 0, r.clipW(), r.clipH()
		if sp.HasClip {
			cx, cy, cw, ch = int(sp.Clip.X), int(sp.Clip.Y), int(sp.Clip.W), int(sp.Clip.H)
		}
		q, ok := r.glowSpritePacket(sp.Frame, int(sp.X)-int(sp.Frame.XOffset), int(sp.Y)-int(sp.Frame.YOffset), cx, cy, cw, ch, gain, r.spriteGlowScale(sp.LightingKind), uv[0], uv[1])
		if ok {
			g.append(q)
		}
	}
	if r.effectGlowScale() <= 0 {
		return nil
	}
	for _, f := range flashes {
		x0, x1 := f.X+f.Scale.Project(-f.Offset), f.X+f.Scale.Project(f.Side-f.Offset)
		y0, y1 := f.Y+f.Scale.Project(-f.Offset), f.Y+f.Scale.Project(f.Side-f.Offset)
		if x0 >= x1 || y0 >= y1 {
			continue
		}
		a, b, c, d := clipLitDisc(int(x0), int(y0), int(x1), int(y1), f.Clip, r.clipW(), r.clipH())
		if a >= c || b >= d {
			continue
		}
		if atlas == nil {
			return fmt.Errorf("retained glow: flash requires prepared atlas")
		}
		uv, err := atlas.Flash(f)
		if err != nil {
			return err
		}
		kx, ky := float32(f.Side)/float32(x1-x0), float32(f.Side)/float32(y1-y0)
		g.append(r.glowFlashPacket(a, b, c, d, uv[0]+float32(a-int(x0))*kx, uv[1]+float32(b-int(y0))*ky, uv[0]+float32(c-int(x0))*kx, uv[1]+float32(d-int(y0))*ky))
	}
	for _, h := range halos {
		if h.Radius <= 0 {
			continue
		}
		a, b, c, d := clipLitDisc(int(h.X-h.Radius), int(h.Y-h.Radius), int(h.X+h.Radius)+1, int(h.Y+h.Radius)+1, h.Clip, r.clipW(), r.clipH())
		if a >= c || b >= d {
			continue
		}
		_, high := rowScaleLanes(lightScale(clampLHTRow(int(h.Row))))
		if high > 0 {
			g.append(r.glowHaloPacket(a, b, c, d, high, float32(a-int(h.X)), float32(b-int(h.Y)), float32(c-int(h.X)), float32(d-int(h.Y)), float32(h.Radius)*float32(h.Radius)))
		}
	}
	return nil
}

// Frame borrows upload storage until Begin. Native emits no passes when empty.
func (g *RetainedGlow) Frame() ([]RetainedGlowVertex, RetainedGlowParameters) {
	p := RetainedGlowParameters{Weights: retainedGlowWeights}
	n, f := g.cpu.glow.octaveWeights()
	p.Blur = [4]float32{glowBlurStep(g.cpu.glow.viewScale), glowFarKernelSigma(g.cpu.glow.viewScale), n, f}
	return g.vertices, p
}
