package main

// The optional build radial (DESIGN_INTERFACE_HUD_INPUT §3.18): a Nanolathe
// host presentation policy, off unless presentation.buildRadial is on.
// While a builder's command page is selected the middle button opens a
// cursor-centred two-level menu over that builder's published placement
// products; choosing one arms the ordinary placement latch through
// armPlacement, cue included. The menu reads only the committed frame and
// the immutable catalog, submits nothing itself, and leaves the existing
// middle-drag camera pan in place whenever no eligible builder is
// selected. Presentation only [I6].

import (
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// Ring geometry in virtual chrome pixels: the menu is laid out on the rail's
// magnified surface, so it grows with the Modern UI scale like the rest of
// the chrome. The hub is the dead-zone; the category band holds the six
// fixed category slots; beyond it the expanded category's items fan out
// around that category's own direction, so the flick that opened it keeps
// travelling into its middle items.
const (
	radialHubR      = 30  // hub radius, and the dead-zone around the centre
	radialBandInner = 33  // category band, inner edge
	radialBandOuter = 84  // category band, outer edge; beyond it the items
	radialCatIconR  = 58  // category picture centre
	radialCatIcon   = 30  // category picture diameter
	radialItemR     = 122 // item picture centre
	radialItemIcon  = 64  // item picture diameter, the authored gadget size
	radialTrayInner = 88  // the item tray behind the fan
	radialTrayOuter = 160
	// radialExtent is how far the drawn menu reaches from its centre: the
	// tray plus its soft rim. The centre is clamped this far inside the
	// viewport so nothing the menu offers is ever clipped.
	radialExtent = radialTrayOuter + 3
	// A sticky click farther out than this dismisses the menu even in the
	// direction of an item; a held flick has no outer limit, so a fast
	// gesture that overshoots the ring still picks by direction.
	radialDismissR = radialTrayOuter + 28

	radialItemStep     = 36.0 // degrees between neighbouring items on a fan
	radialItemsPerPage = 10   // a full ring at radialItemStep

	// radialPressSlop is how far the opening press must travel before its
	// position counts. A menu shifted away from a viewport edge opens with
	// the pointer already off its centre; until the pointer moves, that
	// offset must neither expand a category nor turn a tap into a pick.
	radialPressSlop = 4
)

// Animation lengths in serviced host frames, so a paused battle still
// animates and a capture is deterministic. Purely cosmetic.
const (
	radialOpenFrames  = 6 // ring scale and fade-in
	radialFanFrames   = 6 // items sliding out of their category
	radialHoverFrames = 4 // hovered item lifting off the ring
)

// buildRadialState is the menu's host state. The zero value is closed.
type buildRadialState struct {
	open    bool
	sticky  bool  // a tap left it open for ordinary clicking
	cx, cy  int32 // ring centre on the virtual chrome surface
	ox, oy  int32 // the opening press, same surface
	scale   int32 // the chrome magnification the menu opened under
	builder pool.Handle
	// builderName and builderPages name the builder's authored build
	// pages, whose GAFs carry the stock products' gadget pictures.
	builderName  string
	builderPages int
	groups       []hud.RadialGroup

	lockedCat  int  // group index the item ring shows, -1 for none
	hoverCat   int  // group index under the pointer, -1 for none
	hoverItem  int  // index into the shown group's current page, -1 for none
	page       int  // current page of the shown group
	movedOut   bool // the opening hold left the dead-zone
	pressMoved bool // the pointer has left the opening press's slop

	// Pointer direction from the centre, for the hub's direction caret.
	pointerDeg  float64
	pointerDist float64

	age      int // serviced frames since opening
	fanAge   int // serviced frames since the shown group or page changed
	hoverAge int // serviced frames since the hovered item changed
}

// radialPointer is one serviced host frame reduced to the menu's terms, in
// the same virtual coordinates as the ring.
type radialPointer struct {
	X, Y           int32
	MiddlePressed  bool
	MiddleReleased bool
	LeftPressed    bool
	RightPressed   bool
	Escape         bool
	Wheel          float32
}

func (s *buildRadialState) openAt(cx, cy, ox, oy int32, builder pool.Handle, groups []hud.RadialGroup) {
	*s = buildRadialState{
		open: true, cx: cx, cy: cy, ox: ox, oy: oy, scale: 1,
		builder: builder, groups: groups,
		lockedCat: -1, hoverCat: -1, hoverItem: -1,
	}
}

func (s *buildRadialState) close() { *s = buildRadialState{} }

// category reports the bucket whose items the ring currently shows: the
// hovered slot while the pointer names one, else the locked expansion.
// In sticky mode a clicked category stays locked — the pointer can cross
// other slots on its way to an item, and the ring must not retarget under
// it — so there the lock outranks the hover.
func (s *buildRadialState) category() hud.RadialCategory {
	if i := s.groupIndex(); i >= 0 {
		return s.groups[i].Category
	}
	return hud.RadialMisc
}

func (s *buildRadialState) groupIndex() int {
	if s.sticky && s.lockedCat >= 0 && s.lockedCat < len(s.groups) {
		return s.lockedCat
	}
	if s.hoverCat >= 0 {
		return s.hoverCat
	}
	if s.lockedCat >= 0 && s.lockedCat < len(s.groups) {
		return s.lockedCat
	}
	return -1
}

func (s *buildRadialState) group() (hud.RadialGroup, bool) {
	if i := s.groupIndex(); i >= 0 {
		return s.groups[i], true
	}
	return hud.RadialGroup{}, false
}

func (s *buildRadialState) pages() int {
	g, ok := s.group()
	if !ok {
		return 0
	}
	return (len(g.Products) + radialItemsPerPage - 1) / radialItemsPerPage
}

func (s *buildRadialState) pageItems() []string {
	g, ok := s.group()
	if !ok {
		return nil
	}
	start := s.page * radialItemsPerPage
	if start < 0 || start >= len(g.Products) {
		return nil
	}
	end := start + radialItemsPerPage
	if end > len(g.Products) {
		end = len(g.Products)
	}
	return g.Products[start:end]
}

// radialAngle is the pointer's direction from the centre as degrees
// clockwise from the top, the layout both rings draw in.
func radialAngle(dx, dy float64) float64 {
	deg := math.Atan2(dy, dx)*180/math.Pi + 90
	if deg < 0 {
		deg += 360
	}
	return deg
}

// radialSector is the nearest of n equal sectors from the top.
func radialSector(deg float64, n int) int {
	if n <= 0 {
		return -1
	}
	step := 360 / float64(n)
	return int(math.Floor((deg+step/2)/step)) % n
}

// radialSlotAngle is a category's fixed direction: each bucket owns a
// 60-degree slot clockwise from the top in bucket order.
func radialSlotAngle(c hud.RadialCategory) float64 {
	return float64(c) * 360 / float64(hud.RadialCategoryCount)
}

// radialItemAngle is item i's direction on a fan of n items centred on the
// given category's slot, neighbours radialItemStep apart in authored order
// clockwise.
func radialItemAngle(c hud.RadialCategory, i, n int) float64 {
	deg := radialSlotAngle(c) + (float64(i)-float64(n-1)/2)*radialItemStep
	return math.Mod(deg+720, 360)
}

// radialItemAt is the fan item nearest the direction, or -1 when the
// direction falls outside a partial fan. A full page of items rings the
// whole circle, so every direction names one.
func radialItemAt(c hud.RadialCategory, n int, deg float64) int {
	if n <= 0 {
		return -1
	}
	diff := math.Mod(deg-radialSlotAngle(c)+540, 360) - 180 // [-180, 180)
	i := int(math.Floor(diff/radialItemStep + float64(n)/2))
	if float64(n)*radialItemStep >= 360 {
		return (i%n + n) % n
	}
	if i < 0 || i >= n {
		return -1
	}
	return i
}

// groupAtAngle maps a direction onto the fixed category slots: each
// category owns its 60-degree slot by bucket order whether or not this
// builder offers it, and an empty slot names no group.
func (s *buildRadialState) groupAtAngle(deg float64) int {
	cat := hud.RadialCategory(radialSector(deg, hud.RadialCategoryCount))
	for i, g := range s.groups {
		if g.Category == cat {
			return i
		}
	}
	return -1
}

// itemAt is the shown page's item in the direction, or -1.
func (s *buildRadialState) itemAt(deg float64) int {
	g, ok := s.group()
	if !ok {
		return -1
	}
	return radialItemAt(g.Category, len(s.pageItems()), deg)
}

// step services one frame and reports the product chosen on it, if any.
// An open menu consumes every frame it is shown on, so no world click,
// selection drag or latch can start underneath it.
func (s *buildRadialState) step(p radialPointer) (string, bool) {
	if !s.open {
		return "", false
	}
	s.age++
	s.fanAge++
	s.hoverAge++
	if p.Escape || p.RightPressed {
		s.close()
		return "", true
	}
	if s.sticky && p.MiddlePressed {
		s.close()
		return "", true
	}
	if !s.pressMoved && (radialAbs(p.X-s.ox) > radialPressSlop || radialAbs(p.Y-s.oy) > radialPressSlop) {
		s.pressMoved = true
	}
	dx := float64(p.X - s.cx)
	dy := float64(p.Y - s.cy)
	dist := math.Hypot(dx, dy)
	deg := radialAngle(dx, dy)
	s.pointerDeg, s.pointerDist = deg, dist

	shownBefore, pageBefore, hoverBefore := s.groupIndex(), s.page, s.hoverItem
	s.hoverItem = -1
	switch {
	case dist < radialHubR, !s.sticky && !s.pressMoved:
		s.hoverCat = -1
	case dist < radialBandOuter:
		s.movedOut = true
		s.hoverCat = s.groupAtAngle(deg)
		// A held flick expands whatever slot it crosses; a sticky menu
		// changes its locked category only on a slot click, below.
		if !s.sticky && s.hoverCat >= 0 && s.hoverCat != s.lockedCat {
			s.lockedCat = s.hoverCat
			s.page = 0
		}
	default:
		s.movedOut = true
		// One fast flick can cross the whole category band between two
		// serviced frames; expand from this frame's direction rather
		// than dropping the gesture.
		if s.lockedCat < 0 {
			if s.lockedCat = s.groupAtAngle(deg); s.lockedCat >= 0 {
				s.page = 0
			}
		}
		s.hoverCat = s.lockedCat
		if !s.sticky || dist <= radialDismissR {
			s.hoverItem = s.itemAt(deg)
		}
	}
	if p.Wheel != 0 {
		if pages := s.pages(); pages > 1 {
			step := 1
			if p.Wheel < 0 {
				step = -1
			}
			s.page = ((s.page+step)%pages + pages) % pages
			s.hoverItem = -1
			if dist >= radialBandOuter && (!s.sticky || dist <= radialDismissR) {
				s.hoverItem = s.itemAt(deg)
			}
		}
	}
	if s.groupIndex() != shownBefore || s.page != pageBefore {
		s.fanAge = 0
	}
	if s.hoverItem != hoverBefore {
		s.hoverAge = 0
	}

	selected := ""
	if s.hoverItem >= 0 {
		if items := s.pageItems(); s.hoverItem < len(items) {
			selected = items[s.hoverItem]
		}
	}
	if !s.sticky {
		if !p.MiddleReleased {
			return "", true
		}
		switch {
		case selected != "":
			s.close()
			return selected, true
		case !s.movedOut, s.groupIndex() >= 0 && dist < radialBandOuter:
			// A tap, or a release still inside the category band: stay
			// open sticky with whatever category is expanded.
			s.sticky = true
			return "", true
		default:
			s.close()
			return "", true
		}
	}
	if p.LeftPressed {
		switch {
		case selected != "":
			s.close()
			return selected, true
		case dist >= radialHubR && dist < radialBandOuter && s.hoverCat >= 0:
			if s.lockedCat != s.hoverCat {
				s.lockedCat = s.hoverCat
				s.page = 0
				s.fanAge = 0
			}
			return "", true
		default:
			// A click on the hub, an empty slot, a gap in the fan or far
			// outside the ring dismisses the sticky menu without a world
			// click.
			s.close()
			return "", true
		}
	}
	return "", true
}

func radialAbs(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// serviceBuildRadial opens the menu on an admitted middle press and
// services it ahead of every world path in handleInput. True means the
// frame belonged to the menu. The middle-drag camera pan keeps every
// press this refuses: radial off, no builder page, or a builder with no
// placement products.
func (b *battleSession) serviceBuildRadial(in *input.State, mouse input.MouseState, mx, my int32) bool {
	if b == nil {
		return false
	}
	if b.hostPreferences().BuildRadial == 0 || b.palettePointerOwned || b.megamapShown() || b.isTalkGUIActive() {
		b.radial.close()
		return false
	}
	frame, ok := b.currentSnapshot()
	if !ok || frame == nil {
		b.radial.close()
		return false
	}
	builder := frame.CommandPage.Builder
	// A selection change to another builder, or a UI scale change under the
	// open ring, closes it rather than retargeting it.
	if b.radial.open && (builder != b.radial.builder || b.radial.scale != b.chromeScale()) {
		b.radial.close()
	}
	vx, vy := b.railPointer(mx, my)
	if !b.radial.open {
		if !mouse.Pressed(input.MouseButtonMiddle) || builder == 0 ||
			!b.overBattleViewport(mx, my) || b.isOverMinimap(mx, my) {
			return false
		}
		groups := hud.RadialGroups(b.cat, hud.AllowedBuildProducts(b.cat, frame))
		if len(groups) == 0 {
			return false
		}
		cx, cy := b.radialCentre(mx, my)
		b.radial.openAt(cx, cy, vx, vy, builder, groups)
		b.radial.scale = b.chromeScale()
		b.radial.builderName, b.radial.builderPages = radialBuilderInfo(b.cat, frame)
		b.playUICue(nil, cueNextBuildMenu)
	}
	before := b.radial.category()
	selected, consumed := b.radial.step(radialPointer{
		X: vx, Y: vy,
		MiddlePressed:  mouse.Pressed(input.MouseButtonMiddle),
		MiddleReleased: mouse.Released(input.MouseButtonMiddle),
		LeftPressed:    mouse.Pressed(input.MouseButtonLeft),
		RightPressed:   mouse.Pressed(input.MouseButtonRight),
		Escape:         battleShortcutKeyboard(in).KeyDown(input.KeyEscape),
		Wheel:          mouse.ScrollY,
	})
	if b.radial.open && b.radial.category() != before {
		b.playUICue(nil, cueNextBuildMenu)
	}
	if selected != "" && b.cat != nil {
		if def, ok := b.cat.Unit(selected); ok && def != nil {
			b.armPlacement(def)
			b.playUICue(nil, cueAddBuild)
		}
	}
	return consumed
}

// radialCentre places the ring for a press at framebuffer (mx, my) and
// returns its centre on the virtual chrome surface. The ring is clamped
// inside the battle viewport the chrome leaves, so a press near an edge
// shifts the whole menu rather than clipping its far side; a viewport too
// small for the full extent centres the ring on it.
func (b *battleSession) radialCentre(mx, my int32) (int32, int32) {
	k := b.chromeScale()
	extent := int32(radialExtent) * k
	left, top, bottom := b.cam.ChromeInset()
	w, h := b.surfaceSize()
	return b.railPointer(radialClampAxis(mx, left+1, w-1, extent),
		radialClampAxis(my, top, h-bottom-1, extent))
}

func radialClampAxis(v, lo, hi, extent int32) int32 {
	if hi-lo < 2*extent {
		return (lo + hi) / 2
	}
	if v < lo+extent {
		return lo + extent
	}
	if v > hi-extent {
		return hi - extent
	}
	return v
}

// radialBuilderInfo names the command-page builder's authored pages from
// the committed frame: its definition name, for the page GAF lookup, and
// its authored page-count byte [07 R-HUD-03 §6].
func radialBuilderInfo(cat *content.Catalog, f *frame.Frame) (string, int) {
	if cat == nil || f == nil || f.CommandPage.Builder == 0 {
		return "", 0
	}
	for _, unit := range f.Units {
		if unit.Slot != f.CommandPage.Builder {
			continue
		}
		pages := 0
		if def, ok := cat.Unit(unit.DefName); ok && def != nil {
			pages = hud.BuilderPageCount(def)
		}
		return unit.DefName, pages
	}
	return "", 0
}

// stageCapture poses an open menu for a headless capture: the group
// expanded, the item hovered with the pointer resting on it, and every
// animation finished. The capture path has no pointer to gesture with.
func (s *buildRadialState) stageCapture(group, item int) {
	if !s.open || group < 0 || group >= len(s.groups) {
		return
	}
	s.sticky = true
	s.movedOut, s.pressMoved = true, true
	s.lockedCat, s.hoverCat = group, group
	s.hoverItem = -1
	if items := s.pageItems(); item >= 0 && item < len(items) {
		s.hoverItem = item
		s.pointerDeg = radialItemAngle(s.groups[group].Category, item, len(items))
		s.pointerDist = radialItemR
	}
	s.age, s.fanAge, s.hoverAge = radialOpenFrames, radialFanFrames, radialHoverFrames
}
