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

// Ring geometry in surface pixels. The dead-zone swallows the press jitter
// around the centre; the category band holds the six fixed category slots;
// beyond it the flick's angle names an item on the expanded ring.
const (
	radialDeadZone     = 26.0
	radialRingInner    = 88.0
	radialRingOuter    = 190.0
	radialCategoryR    = 76
	radialItemR        = 122
	radialItemsPerPage = 10
)

// buildRadialState is the menu's host state. The zero value is closed.
type buildRadialState struct {
	open    bool
	sticky  bool // a tap left it open for ordinary clicking
	cx, cy  int32
	builder pool.Handle
	// builderName and builderPages name the builder's authored build
	// pages, whose GAFs carry the stock products' gadget pictures.
	builderName  string
	builderPages int
	groups       []hud.RadialGroup

	lockedCat int // group index the item ring shows, -1 for none
	hoverCat  int // group index under the pointer, -1 for none
	hoverItem int // index into the locked group's current page, -1 for none
	page      int
	movedOut  bool // the opening hold left the dead-zone
	age       int  // serviced host frames, for the open ease
}

// radialPointer is one serviced host frame reduced to the menu's terms.
type radialPointer struct {
	X, Y           int32
	MiddlePressed  bool
	MiddleReleased bool
	LeftPressed    bool
	RightPressed   bool
	Escape         bool
	Wheel          float32
}

func (s *buildRadialState) openAt(cx, cy int32, builder pool.Handle, groups []hud.RadialGroup) {
	*s = buildRadialState{
		open: true, cx: cx, cy: cy, builder: builder, groups: groups,
		lockedCat: -1, hoverCat: -1, hoverItem: -1,
	}
}

func (s *buildRadialState) close() { *s = buildRadialState{} }

// category reports the bucket whose items the ring currently shows: the
// hovered slot while the pointer names one, else the locked expansion.
// In sticky mode a clicked category stays locked — the pointer usually
// crosses other slots on its way to an item, and the ring must not
// retarget under it — so there the lock outranks the hover.
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

// step services one frame and reports the product chosen on it, if any.
// An open menu consumes every frame it is shown on, so no world click,
// selection drag or latch can start underneath it.
func (s *buildRadialState) step(p radialPointer) (string, bool) {
	if !s.open {
		return "", false
	}
	s.age++
	if p.Escape || p.RightPressed {
		s.close()
		return "", true
	}
	if s.sticky && p.MiddlePressed {
		s.close()
		return "", true
	}
	dx := float64(p.X - s.cx)
	dy := float64(p.Y - s.cy)
	dist := math.Hypot(dx, dy)
	deg := radialAngle(dx, dy)

	s.hoverItem = -1
	switch {
	case dist < radialDeadZone:
		s.hoverCat = -1
	case dist < radialRingInner:
		s.movedOut = true
		s.hoverCat = s.groupAtAngle(deg)
		// A held flick expands whatever slot it crosses; a sticky menu
		// changes its locked category only on a plate click, below.
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
		if dist <= radialRingOuter {
			if items := s.pageItems(); len(items) != 0 {
				s.hoverItem = radialSector(deg, len(items))
			}
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
			if dist >= radialRingInner && dist <= radialRingOuter {
				if items := s.pageItems(); len(items) != 0 {
					s.hoverItem = radialSector(deg, len(items))
				}
			}
		}
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
		case !s.movedOut, s.groupIndex() >= 0 && dist < radialRingInner:
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
		case dist >= radialDeadZone && dist < radialRingInner && s.hoverCat >= 0:
			s.lockedCat = s.hoverCat
			s.page = 0
			return "", true
		default:
			// A click on the hub, an empty slot or far outside the
			// ring dismisses the sticky menu without a world click.
			s.close()
			return "", true
		}
	}
	return "", true
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
	if b.radial.open && builder != b.radial.builder {
		b.radial.close()
	}
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
		b.radial.openAt(cx, cy, builder, groups)
		b.radial.builderName, b.radial.builderPages = radialBuilderInfo(b.cat, frame)
		b.playUICue(nil, cueNextBuildMenu)
	}
	before := b.radial.category()
	selected, consumed := b.radial.step(radialPointer{
		X: mx, Y: my,
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

// radialCentre clamps the ring inside the battle viewport the chrome
// leaves, so a press near an edge shifts the whole menu rather than
// clipping its far side. A viewport too small for the full extent
// centres the ring on it.
func (b *battleSession) radialCentre(mx, my int32) (int32, int32) {
	extent := int32(radialItemR + 34) // ring radius plus a gadget half and rim
	left, top, bottom := b.cam.ChromeInset()
	w, h := b.surfaceSize()
	return radialClampAxis(mx, left+1, w-1, extent),
		radialClampAxis(my, top, h-bottom-1, extent)
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
