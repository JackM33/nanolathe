package main

// Drawing for the optional build radial (DESIGN_INTERFACE_HUD_INPUT
// §3.18). The menu is recorded into the same indexed battle HUD list as
// the rest of the chrome, after the world and side page and before any
// modal window, so the Classic and Enhanced executors present the same
// picture. It is laid out on the rail's virtual surface inside the rail's
// chrome region, so the Modern UI scale magnifies it with the sidebar.
//
// The glass is the palette's own fade: every surface is a run of SHD
// shade (or LHT light) spans over whatever the battle drew, so terrain
// shows through darkened rather than painted over. Pictures are the
// authored build-menu gadget art resolved through the sidebar's own lookup
// chain, cropped to discs; the small category pictures are box-filtered
// from the same art through the palette. Colours are Nanolathe decoration
// resolved by nearest match in the loaded palette, like the unit viewer's.
// Recording order keeps every shade and light span ahead of the opaque
// art, so the Enhanced scheduler needs one extra phase, not one per layer.
// Nothing here reads committed state beyond the menu's opening snapshot
// [I6].

import (
	"fmt"
	"math"
	"strconv"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
)

// Fade levels in UIShadeRect's signed terms: SHD rows darken below -17,
// reaching near-black at -32 [03 §4.3.2]; positive levels brighten through
// LHT [03 §4.3.1].
const (
	radialShadeBase     = -21 // the whole footprint, seams included
	radialShadeHub      = -24 // each further fade composes over the base
	radialShadeBand     = -22
	radialShadeEmpty    = -26
	radialShadeHover    = -20
	radialShadeExpanded = -19
	radialShadeTray     = -23
	radialShadePlate    = -25
	radialLightItem     = 3 // the hovered picture, baked through LHT
	radialDimIcon       = -21
)

// radialGrowSizes are the diameters an item passes through as it grows
// from its category's picture to its own; quantized, so each picture keeps
// a handful of cached variants rather than one per frame.
var radialGrowSizes = [...]int{radialCatIcon, 38, 46, 56, radialItemIcon}

// radialPipStep is the angle between page pips on the tray's rim.
const radialPipStep = 6.0

// radialSlotGap is the half-width, in pixels, of the clear seam between
// neighbouring category slots.
const radialSlotGap = 1.0

// radialStyle is the menu's resolved palette bytes.
type radialStyle struct {
	pal       *palette.Tables
	accent    byte // the live highlight
	accentDim byte // its quieter rim
	rim       byte // neutral ring edge
	rimDim    byte // seams and the quieter edges
	outline   byte // picture outline and text shadow
	text      byte // caption text
	textDim   byte
	metal     byte // the side's metal bar colour
	energy    byte // and its energy bar colour
}

// radialDiscKey names one derived disc picture: the authored frame, the
// diameter it is cropped (and box-filtered) to, and the fade baked in.
type radialDiscKey struct {
	src  *formats.GAFFrame
	size int
	tone int
}

// radialArt is the radial's per-HUD cache: the resolved style and the
// derived disc pictures, each built once per authored frame.
type radialArt struct {
	style radialStyle
	discs map[radialDiscKey]*formats.GAFFrame
	// pictures caches each product's resolved gadget frame for one
	// builder; a nil entry records a product without a picture.
	pictureBuilder string
	pictures       map[string]*formats.GAFFrame
}

func (h *retailBattleHUD) radialStyle() *radialStyle {
	a := &h.radial
	if a.style.pal == h.pal && h.pal != nil {
		return &a.style
	}
	pal := h.pal
	// The costs take the resource bars' own authored colours, falling back
	// to a near match when the side authors none in range.
	metal, energy := unitViewerColor(pal, 180, 200, 228), unitViewerColor(pal, 252, 220, 72)
	if h.side != nil {
		if c := h.side.MetalColor; c > 0 && c <= 255 {
			metal = byte(c)
		}
		if c := h.side.EnergyColor; c > 0 && c <= 255 {
			energy = byte(c)
		}
	}
	a.style = radialStyle{
		pal:       pal,
		accent:    unitViewerColor(pal, 120, 236, 196),
		accentDim: unitViewerColor(pal, 52, 120, 100),
		rim:       unitViewerColor(pal, 92, 104, 112),
		rimDim:    unitViewerColor(pal, 58, 66, 72),
		outline:   unitViewerColor(pal, 0, 0, 0),
		text:      unitViewerColor(pal, 236, 240, 232),
		textDim:   unitViewerColor(pal, 150, 158, 160),
		metal:     metal,
		energy:    energy,
	}
	a.discs = nil
	return &a.style
}

// radialDir is the unit direction degFromTop clockwise from the top.
func radialDir(degFromTop float64) (float64, float64) {
	a := degFromTop * math.Pi / 180
	return math.Sin(a), -math.Cos(a)
}

// radialPoint is the ring position at the given clockwise-from-top angle.
func radialPoint(cx, cy int32, degFromTop, r float64) (int, int) {
	sx, sy := radialDir(degFromTop)
	return int(cx) + int(math.Round(r*sx)), int(cy) + int(math.Round(r*sy))
}

// radialHalfPlane is the integer x range on one row that satisfies
// a*x >= b; lo > hi means none.
func radialHalfPlane(a, b float64) (lo, hi int) {
	const eps = 1e-9
	const inf = 1 << 20
	switch {
	case a > eps:
		return int(math.Ceil(b/a - eps)), inf
	case a < -eps:
		return -inf, int(math.Floor(b/a + eps))
	case b <= eps:
		return -inf, inf
	default:
		return inf, -inf
	}
}

// radialSectorSpans calls emit with each row run, as inclusive columns, of
// the annular sector about the pixel (cx, cy): offsets within r0..r1 whose
// direction runs clockwise from a0 to a1, held gap pixels clear of both
// edges. A sweep of a whole turn is the full annulus. The edges are the
// half-planes either side of the sector, intersected for a sweep under
// half a turn and united over it, so every row costs constant work.
func radialSectorSpans(cx, cy int, r0, r1, a0, a1, gap float64, emit func(x0, x1, y int)) {
	sweep := a1 - a0
	if r1 <= 0 || r1 < r0 || sweep <= 0 {
		return
	}
	whole := sweep >= 360
	reflex := sweep > 180
	d0x, d0y := radialDir(a0)
	d1x, d1y := radialDir(a1)
	rows := int(math.Floor(r1))
	for dy := -rows; dy <= rows; dy++ {
		fy := float64(dy)
		o2 := r1*r1 - fy*fy
		if o2 < 0 {
			continue
		}
		xo := int(math.Floor(math.Sqrt(o2)))
		var ring [2][2]int
		nr := 1
		ring[0] = [2]int{-xo, xo}
		if i2 := r0*r0 - fy*fy; i2 > 0 {
			xi := int(math.Ceil(math.Sqrt(i2)))
			if xi > xo {
				continue
			}
			ring[0], ring[1], nr = [2]int{-xo, -xi}, [2]int{xi, xo}, 2
		}
		var wedge [2][2]int
		nw := 1
		wedge[0] = [2]int{-xo, xo}
		if !whole {
			// Clockwise of the a0 edge: cross(d0, p) >= gap; counter-
			// clockwise of the a1 edge: cross(p, d1) >= gap.
			lo0, hi0 := radialHalfPlane(-d0y, gap-d0x*fy)
			lo1, hi1 := radialHalfPlane(d1y, gap+fy*d1x)
			switch {
			case !reflex:
				wedge[0] = [2]int{max(lo0, lo1), min(hi0, hi1)}
			case lo0 > hi0:
				wedge[0] = [2]int{lo1, hi1}
			case lo1 > hi1:
				wedge[0] = [2]int{lo0, hi0}
			case lo1 <= hi0+1 && lo0 <= hi1+1:
				wedge[0] = [2]int{min(lo0, lo1), max(hi0, hi1)}
			default:
				wedge[0], wedge[1], nw = [2]int{lo0, hi0}, [2]int{lo1, hi1}, 2
			}
		}
		for i := 0; i < nr; i++ {
			for j := 0; j < nw; j++ {
				lo, hi := max(ring[i][0], wedge[j][0]), min(ring[i][1], wedge[j][1])
				if lo <= hi {
					emit(cx+lo, cx+hi, cy+dy)
				}
			}
		}
	}
}

// radialShade fades an annular sector by one signed level.
func radialShade(c *client.Client, pal *palette.Tables, cx, cy int, r0, r1, a0, a1, gap float64, level int) {
	if level == 0 {
		return
	}
	radialSectorSpans(cx, cy, r0, r1, a0, a1, gap, func(x0, x1, y int) {
		c.UIShadeRect(pal, x0, y, x1-x0+1, 1, level)
	})
}

// radialFill paints an annular sector in one palette byte.
func radialFill(c *client.Client, cx, cy int, r0, r1, a0, a1, gap float64, idx byte) {
	radialSectorSpans(cx, cy, r0, r1, a0, a1, gap, func(x0, x1, y int) {
		c.UIFillRect(x0, y, x1-x0+1, 1, idx)
	})
}

// radialArc strokes a one-pixel arc as a polyline of short chords.
func radialArc(c *client.Client, cx, cy int, r, a0, a1 float64, idx byte) {
	sweep := a1 - a0
	if r <= 0 || sweep <= 0 {
		return
	}
	n := max(int(math.Ceil(2*math.Pi*r*sweep/360/5)), 2)
	px, py := radialPoint(int32(cx), int32(cy), a0, r)
	for i := 1; i <= n; i++ {
		x, y := radialPoint(int32(cx), int32(cy), a0+sweep*float64(i)/float64(n), r)
		c.UIWorldLine(int32(px), int32(py), int32(x), int32(y), idx)
		px, py = x, y
	}
}

// radialSpoke strokes a radial segment from r0 to r1 at one direction.
func radialSpoke(c *client.Client, cx, cy int, deg, r0, r1 float64, idx byte) {
	x0, y0 := radialPoint(int32(cx), int32(cy), deg, r0)
	x1, y1 := radialPoint(int32(cx), int32(cy), deg, r1)
	c.UIWorldLine(int32(x0), int32(y0), int32(x1), int32(y1), idx)
}

// radialEase is a cubic ease-out of frame over frames.
func radialEase(frame, frames int) float64 {
	if frames <= 0 || frame >= frames {
		return 1
	}
	if frame <= 0 {
		return 0
	}
	t := 1 - float64(frame)/float64(frames)
	return 1 - t*t*t
}

// radialLevel fades a shade level in with the opening ease.
func radialLevel(level int, e float64) int {
	const identity = -17 // SHD row 15 is near-identity [03 §4.3.2]
	return identity + int(math.Round(float64(level-identity)*e))
}

// radialProductFrame resolves one product's gadget picture in the
// sidebar's order: the builder's authored page GAFs, where the stock
// corpus keeps its build-menu pictures; then a downloaded product's own
// gadget GAF, loaded here because the sidebar registers one only once it
// has built that product's page; then the shared interface GAFs
// [fmt gaf][07 R-HUD-03 §6]. Results, absences included, are cached per
// builder.
func (h *retailBattleHUD) radialProductFrame(product, builder string, pages int) *formats.GAFFrame {
	if h == nil || product == "" {
		return nil
	}
	a := &h.radial
	// Both caches hold one builder's pictures, so a new builder starts
	// them afresh rather than letting a long battle accumulate every
	// picture it has shown.
	if a.pictures == nil || a.pictureBuilder != builder {
		a.pictures, a.pictureBuilder, a.discs = map[string]*formats.GAFFrame{}, builder, nil
	}
	if f, ok := a.pictures[product]; ok {
		return f
	}
	var entry *formats.GAFEntry
	// The page count includes the orders page 0.
	for page := 1; builder != "" && page < pages && entry == nil; page++ {
		if gaf := h.resolvePageArt(commandWindowName("", builder, true, page)); gaf != nil {
			entry, _ = gaf.Find(product)
		}
	}
	if entry == nil {
		if gaf := h.generatedProductGAF(product); gaf != nil {
			entry, _ = gaf.Find(product)
		}
	}
	if entry == nil {
		entry = h.gadgetArtEntry(gui.Gadget{Kind: gui.KindButton, Name: product}, nil)
	}
	var found *formats.GAFFrame
	if entry != nil {
		for _, ref := range entry.Frames {
			if ref.Frame != nil {
				found = ref.Frame
				break
			}
		}
	}
	a.pictures[product] = found
	return found
}

// radialDisc is the authored picture cropped to a disc of the given
// diameter — box-filtered through the palette when smaller than the
// picture — with a fade baked in: a positive tone lifts it through LHT, a
// negative one darkens it through SHD. Each variant is built once and
// kept, so the executors see one stable frame per picture.
func (h *retailBattleHUD) radialDisc(src *formats.GAFFrame, size, tone int) *formats.GAFFrame {
	st := h.radialStyle()
	// A composite frame has no leaf pixels to crop; nil takes the caller's
	// text fallback rather than drawing an empty disc, as below for a
	// frame with no opaque pixel.
	if src == nil || st.pal == nil || size <= 0 || src.Width == 0 || src.Height == 0 ||
		src.SubframeCount != 0 || len(src.Subframes) != 0 {
		return nil
	}
	a := &h.radial
	key := radialDiscKey{src, size, tone}
	if f, ok := a.discs[key]; ok {
		return f
	}
	pal := st.pal
	side := min(int(src.Width), int(src.Height))
	ox, oy := (int(src.Width)-side)/2, (int(src.Height)-side)/2
	out := &formats.GAFFrame{
		Width: uint16(size), Height: uint16(size),
		Pixels: make([]byte, size*size), Transparent: make([]bool, size*size),
	}
	r := float64(size) / 2
	opaqueAny := false
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			i := y*size + x
			fx, fy := float64(x)+0.5-r, float64(y)+0.5-r
			if fx*fx+fy*fy > r*r {
				out.Transparent[i] = true
				continue
			}
			var b byte
			opaque := false
			if size == side {
				b, opaque = src.At(ox+x, oy+y)
			} else {
				b, opaque = radialBoxSample(pal, src, ox+x*side/size, oy+y*side/size,
					ox+max((x+1)*side/size, x*side/size+1), oy+max((y+1)*side/size, y*side/size+1))
			}
			if !opaque {
				out.Transparent[i] = true
				continue
			}
			switch {
			case tone > 0:
				b = pal.LightLookup(tone, b)
			case tone < 0:
				b = pal.ShadeLookup(tone+32, b)
			}
			out.Pixels[i] = b
			opaqueAny = true
		}
	}
	if !opaqueAny {
		// Nothing to show: nil takes the caller's text fallback.
		return nil
	}
	if a.discs == nil {
		a.discs = map[radialDiscKey]*formats.GAFFrame{}
	}
	a.discs[key] = out
	return out
}

// radialBoxSample averages the opaque source pixels of [x0,x1)×[y0,y1) and
// returns the nearest palette entry.
func radialBoxSample(pal *palette.Tables, f *formats.GAFFrame, x0, y0, x1, y1 int) (byte, bool) {
	var r, g, b, n int
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			if v, ok := f.At(x, y); ok {
				c := pal.Base[v]
				r, g, b, n = r+int(c[0]), g+int(c[1]), b+int(c[2]), n+1
			}
		}
	}
	if n == 0 {
		return 0, false
	}
	return unitViewerColor(pal, r/n, g/n, b/n), true
}

// radialShadowText draws FNT text with a one-pixel drop shadow, centred on
// cx.
func radialShadowText(c *client.Client, fnt *formats.FNT, text string, cx, y int, color, shadow byte) {
	if fnt == nil || text == "" {
		return
	}
	x := cx - client.MeasureText(fnt, text)/2
	c.UITextWidth(fnt, text, x+1, y+1, 1<<15, shadow)
	c.UITextWidth(fnt, text, x, y, 1<<15, color)
}

// radialCaption is what the hub says: the hovered product's name and
// cost, else the shown or hovered category and its size.
type radialCaption struct {
	title         string
	metal, energy int
	cost          bool
	detail        string
}

func (h *retailBattleHUD) radialCaptionFor(b *battleSession) radialCaption {
	s := &b.radial
	items := s.pageItems()
	if s.hoverItem >= 0 && s.hoverItem < len(items) && b.cat != nil {
		if def, ok := b.cat.Unit(items[s.hoverItem]); ok && def != nil {
			title := def.Name
			if title == "" {
				title = def.UnitName
			}
			return radialCaption{title: title, cost: true,
				metal: int(def.BuildCostMetal), energy: int(def.BuildCostEnergy)}
		}
	}
	gi := s.groupIndex()
	if s.hoverCat >= 0 {
		gi = s.hoverCat
	}
	if gi >= 0 && gi < len(s.groups) {
		g := s.groups[gi]
		detail := fmt.Sprintf("%d to build", len(g.Products))
		if pages := s.pages(); pages > 1 && gi == s.groupIndex() {
			detail = fmt.Sprintf("page %d of %d  -  wheel", s.page+1, pages)
		}
		return radialCaption{title: g.Category.Label(), detail: detail}
	}
	return radialCaption{title: "Build"}
}

func (h *retailBattleHUD) drawBuildRadial(c *client.Client, b *battleSession) {
	if h == nil || c == nil || b == nil || !b.radial.open {
		return
	}
	s := &b.radial
	st := h.radialStyle()
	if st.pal == nil {
		return
	}
	c.BeginChromeRegion(b.railRegion())
	defer c.EndChromeRegion()

	pal := st.pal
	cx, cy := int(s.cx), int(s.cy)
	open := radialEase(s.age, radialOpenFrames)
	grow := 0.86 + 0.14*open
	hubR := radialHubR * grow
	bandIn, bandOut := radialBandInner*grow, radialBandOuter*grow
	shown := s.groupIndex()
	items := s.pageItems()

	// The expanded category's fan: each item grows out of its category's
	// picture and slides to its place, the tray widening behind it.
	type fanItem struct {
		product string
		deg, r  float64
		size    int
	}
	var fan []fanItem
	var fanCat hud.RadialCategory
	fanE := radialEase(s.fanAge, radialFanFrames) * open
	if shown >= 0 && len(items) != 0 {
		fanCat = s.groups[shown].Category
		slot := radialSlotAngle(fanCat)
		for i, product := range items {
			// A slight stagger, so the fan unfurls from its middle.
			mid := math.Abs(float64(i) - float64(len(items)-1)/2)
			e := radialEase(s.fanAge-int(mid*0.6), radialFanFrames) * open
			final := radialItemAngle(fanCat, i, len(items))
			diff := math.Mod(final-slot+540, 360) - 180
			r := radialCatIconR*grow + (radialItemR-radialCatIconR*grow)*e
			if i == s.hoverItem {
				r += 5 * radialEase(s.hoverAge, radialHoverFrames)
			}
			size := radialGrowSizes[int(math.Round(e*float64(len(radialGrowSizes)-1)))]
			fan = append(fan, fanItem{product, slot + diff*e, r, size})
		}
	}

	// ---- Glass: every shade span first, one blend stream. ----
	// A base fade covers the whole footprint, so the seams between slots
	// read as lighter glass rather than bare battlefield; each surface then
	// fades further over it.
	radialShade(c, pal, cx, cy, 0, bandOut, 0, 360, 0, radialLevel(radialShadeBase, open))
	radialShade(c, pal, cx, cy, 0, hubR, 0, 360, 0, radialLevel(radialShadeHub, open))
	for slot := 0; slot < hud.RadialCategoryCount; slot++ {
		gi := s.groupAtAngle(float64(slot) * 60)
		level := radialShadeBand
		switch {
		case gi < 0:
			level = radialShadeEmpty
		case gi == shown:
			level = radialShadeExpanded
		case gi == s.hoverCat:
			level = radialShadeHover
		}
		a := float64(slot)*60 - 30
		radialShade(c, pal, cx, cy, bandIn, bandOut, a, a+60, radialSlotGap, radialLevel(level, open))
	}
	trayOut := radialTrayInner + (radialTrayOuter-radialTrayInner)*fanE
	trayHalf := float64(len(fan)) * radialItemStep / 2 * fanE
	trayFrom, trayTo := radialSlotAngle(fanCat)-trayHalf, radialSlotAngle(fanCat)+trayHalf
	if 2*trayHalf >= 360 {
		trayFrom, trayTo = 0, 360
	}
	if len(fan) != 0 && trayHalf > 0 {
		// The tray's base starts just past the disc's, so the two cover
		// every pixel between band and tray exactly once.
		radialShade(c, pal, cx, cy, bandOut+1e-6, trayOut, trayFrom, trayTo, 0, radialLevel(radialShadeBase, open))
		radialShade(c, pal, cx, cy, radialTrayInner, trayOut, trayFrom, trayTo, radialSlotGap, radialLevel(radialShadeTray, open))
	}
	caption := h.radialCaptionFor(b)
	title, detail := h.primaryFont, h.guiFont
	if title == nil {
		title = h.guiFont
	}
	if detail == nil {
		detail = title
	}
	plateW, plateH, lineH := 0, 0, 0
	if title != nil {
		lineH = int(title.Height)
		plateW = client.MeasureText(title, caption.title)
		plateH = lineH + 4
		switch {
		case caption.cost:
			plateW = max(plateW, radialCostWidth(title, caption))
			plateH += lineH
		case caption.detail != "":
			plateW = max(plateW, client.MeasureText(detail, caption.detail))
			plateH += int(detail.Height) + 1
		}
		plateW += plateH // the pill's round ends
	}
	px, py := cx-plateW/2, cy-plateH/2
	if plateW > 2*int(hubR)-6 && open >= 1 {
		// A caption wider than the hub sits on its own pill across the
		// band, between the slots on either side.
		radialPillShade(c, pal, px, py, plateW, plateH, radialShadePlate)
	}

	// ---- Rims and accents. ----
	// Fades cannot darken unexplored black ground, so opaque rims and seams
	// carry the menu's shape there; over terrain they read as bevels.
	radialArc(c, cx, cy, hubR, 0, 360, st.rim)
	radialArc(c, cx, cy, bandOut+0.5, 0, 360, st.rimDim)
	for slot := 0; slot < hud.RadialCategoryCount; slot++ {
		radialSpoke(c, cx, cy, float64(slot)*60+30, bandIn, bandOut, st.rimDim)
	}
	if len(fan) != 0 && trayHalf > 0 {
		radialArc(c, cx, cy, radialTrayInner-0.5, trayFrom, trayTo, st.rimDim)
		if trayTo-trayFrom < 360 {
			radialSpoke(c, cx, cy, trayFrom, radialTrayInner, trayOut, st.rimDim)
			radialSpoke(c, cx, cy, trayTo, radialTrayInner, trayOut, st.rimDim)
		}
	}
	for slot := 0; slot < hud.RadialCategoryCount; slot++ {
		gi := s.groupAtAngle(float64(slot) * 60)
		if gi < 0 || (gi != shown && gi != s.hoverCat) {
			continue
		}
		a := float64(slot)*60 - 30
		idx := st.accent
		if gi != shown {
			idx = st.accentDim
		}
		radialFill(c, cx, cy, bandOut-2, bandOut, a, a+60, radialSlotGap, idx)
	}
	if len(fan) != 0 && trayHalf > 0 {
		radialArc(c, cx, cy, trayOut, trayFrom, trayTo, st.accentDim)
		// A category with more than one page carries a pip per page on the
		// tray's rim, in its own direction, beyond the reach of the items.
		if pages := s.pages(); pages > 1 && fanE >= 1 {
			slot := radialSlotAngle(fanCat)
			for p := 0; p < pages; p++ {
				x, y := radialPoint(s.cx, s.cy, slot+(float64(p)-float64(pages-1)/2)*radialPipStep, trayOut)
				idx := st.rim
				if p == s.page {
					idx = st.accent
				}
				radialFill(c, x, y, 0, 2.5, 0, 360, 0, st.outline)
				radialFill(c, x, y, 0, 1.5, 0, 360, 0, idx)
			}
		}
	}
	// The direction caret: where the pointer is aiming, on the hub's rim.
	if s.pointerDist >= radialHubR && open >= 1 {
		radialFill(c, cx, cy, hubR-4, hubR, s.pointerDeg-14, s.pointerDeg+14, 0, st.accent)
	}

	// ---- Category pictures: each slot shows its first product. ----
	for gi, g := range s.groups {
		if len(g.Products) == 0 {
			continue
		}
		pic := h.radialProductFrame(h.radialProductName(b, g.Products[0]), s.builderName, s.builderPages)
		tone := 0
		if shown >= 0 && gi != shown && gi != s.hoverCat {
			tone = radialDimIcon
		}
		disc := h.radialDisc(pic, radialCatIcon, tone)
		x, y := radialPoint(s.cx, s.cy, radialSlotAngle(g.Category), radialCatIconR*grow)
		if disc != nil {
			c.UIBlit(disc, x-radialCatIcon/2, y-radialCatIcon/2)
			edge := st.outline
			if gi == shown {
				edge = st.accent
			}
			radialArc(c, x, y, radialCatIcon/2+0.5, 0, 360, edge)
		} else if detail != nil {
			radialShadowText(c, detail, g.Category.Label(), x, y-int(detail.Height)/2, st.text, st.outline)
		}
	}

	// ---- The fan of items, hovered last so it lies on top. ----
	order := make([]int, 0, len(fan))
	for i := range fan {
		if i != s.hoverItem {
			order = append(order, i)
		}
	}
	if s.hoverItem >= 0 && s.hoverItem < len(fan) {
		order = append(order, s.hoverItem)
	}
	for _, i := range order {
		it := fan[i]
		x, y := radialPoint(s.cx, s.cy, it.deg, it.r)
		hovered := i == s.hoverItem
		tone := 0
		if hovered {
			tone = radialLightItem
		}
		pic := h.radialProductFrame(h.radialProductName(b, it.product), s.builderName, s.builderPages)
		half := it.size / 2
		if disc := h.radialDisc(pic, it.size, tone); disc != nil {
			c.UIBlit(disc, x-half, y-half)
		} else if detail != nil {
			radialShadowText(c, detail, h.radialProductName(b, it.product), x, y-int(detail.Height)/2, st.text, st.outline)
		}
		if hovered {
			radialFill(c, x, y, float64(half), float64(half)+2.5, 0, 360, 0, st.accent)
		} else {
			radialArc(c, x, y, float64(half)+0.5, 0, 360, st.outline)
		}
	}

	// ---- Caption. ----
	if title != nil && open >= 1 {
		ty := py + 2
		radialShadowText(c, title, caption.title, cx, ty, st.text, st.outline)
		switch {
		case caption.cost:
			radialCost(c, title, caption, cx, ty+lineH, st)
		case caption.detail != "":
			radialShadowText(c, detail, caption.detail, cx, ty+lineH+1, st.textDim, st.outline)
		}
	}
}

// radialCost lays out the cost line: a metal swatch and amount, then an
// energy swatch and amount, each in the economy's own colour.
func radialCost(c *client.Client, fnt *formats.FNT, caption radialCaption, cx, y int, st *radialStyle) {
	m, e := strconv.Itoa(caption.metal), strconv.Itoa(caption.energy)
	x := cx - radialCostWidth(fnt, caption)/2
	h := int(fnt.Height)
	for _, part := range [2]struct {
		text  string
		color byte
	}{{m, st.metal}, {e, st.energy}} {
		c.UIFillRect(x, y+2, radialSwatch+2, h-3, st.outline)
		c.UIFillRect(x+1, y+3, radialSwatch, h-5, part.color)
		x += radialSwatch + 5
		c.UITextWidth(fnt, part.text, x+1, y+1, 1<<15, st.outline)
		c.UITextWidth(fnt, part.text, x, y, 1<<15, part.color)
		x += client.MeasureText(fnt, part.text) + radialCostGap
	}
}

// radialCostWidth is the cost line's width as radialCost lays it out.
func radialCostWidth(fnt *formats.FNT, caption radialCaption) int {
	m, e := strconv.Itoa(caption.metal), strconv.Itoa(caption.energy)
	return 2*(radialSwatch+5) + client.MeasureText(fnt, m) + radialCostGap + client.MeasureText(fnt, e)
}

const (
	radialSwatch  = 3  // cost swatch width
	radialCostGap = 10 // between the metal and energy readouts
)

// radialPillShade fades a rectangle whose short ends are half-discs.
func radialPillShade(c *client.Client, pal *palette.Tables, x, y, w, h, level int) {
	if w <= 0 || h <= 0 {
		return
	}
	r := float64(h) / 2
	for row := 0; row < h; row++ {
		dy := float64(row) + 0.5 - r
		inset := int(math.Round(r - math.Sqrt(max(r*r-dy*dy, 0))))
		if w-2*inset > 0 {
			c.UIShadeRect(pal, x+inset, y+row, w-2*inset, 1, level)
		}
	}
}

// radialProductName is the catalog's unit name for a product, the key the
// gadget art is authored under.
func (h *retailBattleHUD) radialProductName(b *battleSession, product string) string {
	if b != nil && b.cat != nil {
		if def, ok := b.cat.Unit(product); ok && def != nil && def.UnitName != "" {
			return def.UnitName
		}
	}
	return product
}
