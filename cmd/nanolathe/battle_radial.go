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
	// viewport so nothing the menu offers is ever clipped, and a viewport
	// too small for that declines the menu.
	radialExtent = radialTrayOuter + 3
	// A sticky click farther out than this dismisses the menu even in the
	// direction of an item; a held flick has no outer limit, so a fast
	// gesture that overshoots the ring still picks by direction.
	radialDismissR = radialTrayOuter + 28

	radialItemStep     = 36.0 // degrees between neighbouring items on a fan
	radialItemsPerPage = 10   // a full ring at radialItemStep
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
	open bool
	// sticky is open for ordinary clicking: after a tap, or from the start
	// when the ring opened shifted off the press.
	sticky bool
	cx, cy int32 // ring centre on the virtual chrome surface
	scale  int32 // the chrome magnification the menu opened under
	// surfW and surfH are the surface size it opened on; a resize closes
	// it rather than leaving the ring clamped to the old viewport.
	surfW, surfH int32
	builder      pool.Handle
	// builderName and builderPages name the builder's authored build
	// pages, whose GAFs carry the stock products' gadget pictures.
	builderName  string
	builderPages int
	groups       []hud.RadialGroup

	lockedCat int     // group index the item ring shows, -1 for none
	hoverCat  int     // group index under the pointer, -1 for none
	hoverItem int     // index into the shown group's current page, -1 for none
	page      int     // current page of the shown group
	wheel     float32 // banked fraction of a wheel notch
	movedOut  bool    // the opening hold left the dead-zone

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
	Wheel          float32 // zoom-wheel notches, which exclude precise trackpad scrolling
}

func (s *buildRadialState) openAt(cx, cy int32, builder pool.Handle, groups []hud.RadialGroup) {
	*s = buildRadialState{
		open: true, cx: cx, cy: cy, scale: 1,
		builder: builder, groups: groups,
		lockedCat: -1, hoverCat: -1, hoverItem: -1,
	}
}

func (s *buildRadialState) close() { *s = buildRadialState{} }

// groupIndex is the group whose items the ring shows: the locked
// expansion, else the slot under the pointer. A held flick locks every
// slot it crosses, so it follows the pointer. A sticky menu with nothing
// locked previews the slot under the pointer and locks it when the
// pointer reaches the fan; once locked, only a slot click changes it, so
// the ring does not retarget while the pointer crosses other slots on its
// way to an item.
func (s *buildRadialState) groupIndex() int {
	if s.lockedCat >= 0 {
		return s.lockedCat
	}
	return s.hoverCat
}

func (s *buildRadialState) group() (hud.RadialGroup, bool) {
	i := s.groupIndex()
	if i < 0 || i >= len(s.groups) {
		return hud.RadialGroup{}, false
	}
	return s.groups[i], true
}

func (s *buildRadialState) pages() int {
	g, _ := s.group()
	return (len(g.Products) + radialItemsPerPage - 1) / radialItemsPerPage
}

func (s *buildRadialState) pageItems() []string {
	g, ok := s.group()
	start := s.page * radialItemsPerPage
	if !ok || start >= len(g.Products) {
		return nil
	}
	return g.Products[start:min(start+radialItemsPerPage, len(g.Products))]
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
	if p.RightPressed || (s.sticky && p.MiddlePressed) {
		s.close()
		return "", true
	}
	dx := float64(p.X - s.cx)
	dy := float64(p.Y - s.cy)
	dist := math.Hypot(dx, dy)
	deg := radialAngle(dx, dy)
	s.pointerDeg, s.pointerDist = deg, dist

	shownBefore, pageBefore, hoverBefore := s.groupIndex(), s.page, s.hoverItem
	switch {
	case dist < radialHubR:
		s.hoverCat = -1
	case dist < radialBandOuter:
		s.movedOut = true
		s.hoverCat = s.groupAtAngle(deg)
		// A held flick expands whatever slot it crosses; a sticky menu
		// changes its locked category only on a slot click, below.
		if !s.sticky && s.hoverCat >= 0 {
			s.lockedCat = s.hoverCat
		}
	default:
		s.movedOut = true
		// A sticky preview locks the category it was showing. One fast
		// flick can cross the whole band between two serviced frames, so
		// with nothing shown, expand from this frame's direction rather
		// than dropping the gesture.
		if s.lockedCat < 0 {
			if s.lockedCat = shownBefore; s.lockedCat < 0 {
				s.lockedCat = s.groupAtAngle(deg)
			}
		}
		s.hoverCat = s.lockedCat
	}
	if s.groupIndex() != shownBefore {
		s.page = 0
	}
	// One page per whole notch; fractions bank, as the megamap's wheel does.
	if p.Wheel != 0 {
		if (p.Wheel < 0) != (s.wheel < 0) {
			s.wheel = 0
		}
		s.wheel += p.Wheel
		if s.wheel >= 1 || s.wheel <= -1 {
			step := 1
			if s.wheel < 0 {
				step = -1
			}
			s.wheel = 0
			if pages := s.pages(); pages > 1 {
				s.page = ((s.page+step)%pages + pages) % pages
			}
		}
	}
	s.hoverItem = -1
	if dist >= radialBandOuter && (!s.sticky || dist <= radialDismissR) {
		s.hoverItem = s.itemAt(deg)
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
		case !s.movedOut, dist < radialHubR, s.groupIndex() >= 0 && dist < radialBandOuter:
			// A tap, a release back on the hub, or one still inside the
			// category band: stay open sticky with whatever is expanded.
			s.sticky = true
		default:
			s.close()
		}
		return "", true
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
		default:
			// A click on the hub, an empty slot, a gap in the fan or far
			// outside the ring dismisses the sticky menu without a world
			// click.
			s.close()
		}
	}
	return "", true
}

// openBuildRadial opens the menu for a press at framebuffer (mx, my) over
// the committed command page's builder, or reports false when that builder
// offers no placement product.
func (b *battleSession) openBuildRadial(f *frame.Frame, mx, my int32) bool {
	groups := hud.RadialGroups(b.cat, hud.AllowedBuildProducts(b.cat, f))
	if len(groups) == 0 {
		return false
	}
	cx, cy, fits := b.radialCentre(mx, my)
	if !fits {
		return false
	}
	b.radial.openAt(cx, cy, f.CommandPage.Builder, groups)
	b.radial.scale = b.chromeScale()
	b.radial.surfW, b.radial.surfH = b.surfaceSize()
	// The builder's name and page count select the page GAFs that carry
	// the stock products' gadget pictures.
	if v, ok := snapshotUnitByHandle(f, f.CommandPage.Builder); ok {
		b.radial.builderName = v.DefName
	}
	b.radial.builderPages = int(f.CommandPage.PageCount)
	// Near an edge the ring shifts to stay in view, which can leave the
	// unmoved pointer over a slot or the fan; a hold could then choose
	// without a flick, so a ring shifted past its hub opens for clicking.
	vx, vy := b.railPointer(mx, my)
	b.radial.sticky = math.Hypot(float64(cx-vx), float64(cy-vy)) >= radialHubR
	// The menu takes the pointer, so a gesture already in flight ends here,
	// as it does when the battle menu or the megamap opens, and the economy
	// panel, which paints above it, closes.
	state := &b.battleState().Input
	state.DragActive, state.HUDCaptured = false, false
	b.modernDrag, b.resourceClick = nil, nil
	b.economyPanel = economyPanelState{}
	return true
}

// serviceBuildRadial opens the menu on an admitted middle press and
// services it ahead of every other pointer consumer in handleInput. True
// means the frame belonged to the menu. The middle-drag camera pan keeps
// every press this refuses: radial off, no builder page, a builder with no
// placement products, a replay (which takes no orders), a camera drag in
// progress, a viewport too small for the menu, or a press over the minimap
// or the economy panel.
func (b *battleSession) serviceBuildRadial(mouse input.MouseState, mx, my int32) bool {
	if b == nil {
		return false
	}
	if b.hostPreferences().BuildRadial == 0 || b.palettePointerOwned || b.megamapShown() || b.isTalkGUIActive() {
		b.radial.close()
		return false
	}
	f, ok := b.currentSnapshot()
	if !ok || f == nil {
		b.radial.close()
		return false
	}
	// A selection change to another builder, or a UI scale or surface size
	// change under the open ring, closes it rather than retargeting it.
	w, h := b.surfaceSize()
	if b.radial.open && (f.CommandPage.Builder != b.radial.builder || b.radial.scale != b.chromeScale() ||
		b.radial.surfW != w || b.radial.surfH != h) {
		b.radial.close()
	}
	opened := false
	if !b.radial.open {
		if !mouse.Pressed(input.MouseButtonMiddle) || f.CommandPage.Builder == 0 || b.replayPlayback() != nil ||
			b.minimapCameraCaptured || b.dragScrollActive ||
			!b.overBattleViewport(mx, my) || b.isOverMinimap(mx, my) || b.economyPanelCovers(mx, my) ||
			!b.openBuildRadial(f, mx, my) {
			return false
		}
		opened = true
	}
	group, page := b.radial.groupIndex(), b.radial.page
	vx, vy := b.railPointer(mx, my)
	selected, consumed := b.radial.step(radialPointer{
		X: vx, Y: vy,
		// The opening press is not the toggle that closes a sticky menu.
		MiddlePressed:  mouse.Pressed(input.MouseButtonMiddle) && !opened,
		MiddleReleased: mouse.Released(input.MouseButtonMiddle),
		LeftPressed:    mouse.Pressed(input.MouseButtonLeft),
		RightPressed:   mouse.Pressed(input.MouseButtonRight),
		Wheel:          mouse.ZoomScrollY,
	})
	// One cue for each frame that changes the shown items: the opening, a
	// new category or a new page. A preview returning to none is silent.
	shown := b.radial.groupIndex()
	if b.radial.open && (opened || shown != group && shown >= 0 || b.radial.page != page) {
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
// shifts the whole menu rather than clipping its far side; fits is false
// when the viewport cannot hold the whole menu at the current UI scale.
func (b *battleSession) radialCentre(mx, my int32) (cx, cy int32, fits bool) {
	extent := int32(radialExtent) * b.chromeScale()
	left, top, bottom := b.cam.ChromeInset()
	w, h := b.surfaceSize()
	x, fitX := radialClampAxis(mx, left+1, w-1, extent)
	y, fitY := radialClampAxis(my, top, h-bottom-1, extent)
	cx, cy = b.railPointer(x, y)
	return cx, cy, fitX && fitY
}

func radialClampAxis(v, lo, hi, extent int32) (int32, bool) {
	if hi-lo < 2*extent {
		return v, false
	}
	return max(lo+extent, min(v, hi-extent)), true
}

// stageCapture poses an open menu for a headless capture: sticky, every
// animation finished, and — unless group is -1, the bare category ring —
// the group expanded with the item hovered and the pointer resting on it.
// The capture path has no pointer to gesture with.
func (s *buildRadialState) stageCapture(group, item int) {
	if !s.open || group >= len(s.groups) {
		return
	}
	s.sticky, s.movedOut = true, true
	s.age, s.fanAge, s.hoverAge = radialOpenFrames, radialFanFrames, radialHoverFrames
	if group < 0 {
		return
	}
	s.lockedCat, s.hoverCat = group, group
	s.hoverItem = -1
	if items := s.pageItems(); item >= 0 && item < len(items) {
		s.hoverItem = item
		s.pointerDeg = radialItemAngle(s.groups[group].Category, item, len(items))
		s.pointerDist = radialItemR
	}
}
