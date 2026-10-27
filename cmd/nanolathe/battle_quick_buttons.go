package main

// Quick selection buttons: Deselect, Same type and Con at the top right of
// the battle view, just below the resource strip, for players without a
// keyboard (host presentation policy, DESIGN_INTERFACE_HUD_INPUT "Quick
// selection buttons"). Each runs an existing selection action and emits only
// ordinary typed selection commands [I6]. The faces reuse the strip's own
// PANELBOT metal art.

import (
	"image"
	"image/draw"
	"runtime"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

type quickButton struct {
	label string
	// needsSelection greys the button while nothing is selected.
	needsSelection bool
	action         func(*battleSession)
}

var quickButtons = [...]quickButton{
	{"DESELECT", true, (*battleSession).deselectAll},
	{"SAME TYPE", true, (*battleSession).selectSameTypes},
	{"CON", false, (*battleSession).selectNextIdleConstructor},
}

const (
	quickButtonW, quickButtonH, quickButtonGap = 72, 28, 3
	quickButtonTop                             = hud.ChromeStripHeight + 3
	// quickButtonIncomeClearance keeps the row left of the allied-income
	// toggle and the 205-pixel list that hangs from it (community_income.go).
	quickButtonIncomeClearance = 18 + 205 + 4
)

// quickButtonsHost reports a touch build, the only one that shows the quick
// buttons: a desktop player has Escape, Ctrl+Z and the idle-constructor key
// for the same actions.
var quickButtonsHost = runtime.GOOS == "android"

func (b *battleSession) quickButtonsShown() bool {
	return b != nil && quickButtonsHost && b.hostPreferences().QuickButtons != 0
}

// quickButtonRect is button i's rectangle on the strips' virtual surface.
func (b *battleSession) quickButtonRect(i int) (x, y, w, h int) {
	sw, sh := b.surfaceSize()
	width, _ := b.railRegion().VirtualSize(int(sw), int(sh))
	right := width - quickButtonGap
	if b.hostPreferences().AlliedResources != 0 {
		right = width - quickButtonIncomeClearance
	}
	n := len(quickButtons)
	x = right - (n-i)*quickButtonW - (n-1-i)*quickButtonGap
	return x, quickButtonTop, quickButtonW, quickButtonH
}

func (b *battleSession) quickButtonAt(x, y int32) int {
	for i := range quickButtons {
		bx, by, bw, bh := b.quickButtonRect(i)
		if int(x) >= bx && int(x) < bx+bw && int(y) >= by && int(y) < by+bh {
			return i
		}
	}
	return -1
}

// serviceQuickButtons owns presses that begin on a button: a left release on
// the same button runs its action, and no press there reaches the world.
func (b *battleSession) serviceQuickButtons(mouse input.MouseState) bool {
	if !b.quickButtonsShown() {
		b.quickButtonCapture = 0
		return false
	}
	mx, my := b.railPointer(int32(mouse.X), int32(mouse.Y))
	hit := b.quickButtonAt(mx, my)
	if hit >= 0 && mouse.Pressed(input.MouseButtonLeft) {
		b.quickButtonCapture = hit + 1
		return true
	}
	if b.quickButtonCapture != 0 {
		if mouse.Released(input.MouseButtonLeft) {
			if hit == b.quickButtonCapture-1 {
				quickButtons[hit].action(b)
			}
			b.quickButtonCapture = 0
		}
		return true
	}
	return hit >= 0 && (mouse.Pressed(input.MouseButtonRight) || mouse.Pressed(input.MouseButtonMiddle))
}

// deselectAll is Escape's idle arm for a button: clear the selection, any
// armed order and any armed placement [07 R-CAM-01 §2].
func (b *battleSession) deselectAll() {
	if b.hasSelection() {
		_ = b.enqueueSelectionCommand(session.HumanCommand{Kind: session.HumanSelectionClear})
	}
	b.disarmPlacement()
	b.resetOrderLatch()
}

// selectNextIdleConstructor is the idle-constructor cycle (Ctrl+B under
// community selection): the next idle construction unit is selected and the
// view centres on it. With none idle the selection is left alone and a
// message says so.
func (b *battleSession) selectNextIdleConstructor() {
	f, ok := b.currentSnapshot()
	if !ok {
		return
	}
	if _, found := b.nextCommunityCycleUnit(f, b.communityBuilderCursor, b.communityCycleMask(communityCycleBuilder), communityCycleBuilder); !found {
		b.messageRing().PostSilent("No idle construction units", 0, b.currentTick())
		return
	}
	b.cycleCommunityIdle(communityCycleBuilder)
}

// quickButtonFaceArt, when set, receives the buttons' metal face as an image
// at each battle HUD load, so a host's own overlay controls can match it (the
// Android SHIFT button, docs/ANDROID.md §3).
var quickButtonFaceArt func(face *image.RGBA)

// quickButtonFaceOffset is the face's column in the strip art: a plain
// stretch clear of its bordered ends, grooves and top row.
const quickButtonFaceOffset = 48

func (h *retailBattleHUD) publishQuickButtonFace() {
	if quickButtonFaceArt == nil || h == nil || h.panelBottom == nil {
		return
	}
	art := nlGAFImage(h.panelBottom, h.pal)
	if art == nil || art.Bounds().Dx() < quickButtonFaceOffset+quickButtonW || art.Bounds().Dy() < quickButtonH+2 {
		return
	}
	face := image.NewRGBA(image.Rect(0, 0, quickButtonW, quickButtonH))
	draw.Draw(face, face.Bounds(), art, image.Pt(quickButtonFaceOffset, 2), draw.Src)
	quickButtonFaceArt(face)
}

func (h *retailBattleHUD) drawQuickButtons(c *client.Client, b *battleSession, f *frame.Frame) {
	if h == nil || c == nil || !b.quickButtonsShown() {
		return
	}
	font := h.guiFont
	if font == nil {
		font = h.console
	}
	selected := f != nil && len(f.Selection.Handles) != 0
	outline, light, shadow := h.guiColor(0), h.guiColor(7), h.guiColor(8)
	for i, button := range quickButtons {
		x, y, w, ht := b.quickButtonRect(i)
		down := b.quickButtonCapture == i+1
		if tex := h.panelBottom; tex != nil && int(tex.Width) > w+16 && int(tex.Height) > ht {
			// Each face is the same plain stretch of the strip's metal, inside
			// the art and clear of its bordered ends, grooves and top row.
			offset := min(int(tex.Width)-w-8, quickButtonFaceOffset)
			c.UIBlitClipped(tex, x-offset, y-2, x, y, w, ht)
		} else {
			c.UIFillRect(x, y, w, ht, shadow)
		}
		top, bottom := light, shadow
		if down {
			top, bottom = shadow, light
		}
		c.UIFillRect(x, y, w, 1, outline)
		c.UIFillRect(x, y+ht-1, w, 1, outline)
		c.UIFillRect(x, y, 1, ht, outline)
		c.UIFillRect(x+w-1, y, 1, ht, outline)
		c.UIFillRect(x+1, y+1, w-2, 1, top)
		c.UIFillRect(x+1, y+1, 1, ht-2, top)
		c.UIFillRect(x+1, y+ht-2, w-2, 1, bottom)
		c.UIFillRect(x+w-2, y+1, 1, ht-2, bottom)
		if font == nil {
			continue
		}
		color := h.guiColor(15)
		if button.needsSelection && !selected {
			color = shadow
		}
		tx := x + (w-client.MeasureText(font, button.label))/2
		ty := y + (ht-int(font.Height))/2
		if down {
			tx, ty = tx+1, ty+1
		}
		c.UITextWidth(font, button.label, tx, ty, w-2, color)
	}
}
