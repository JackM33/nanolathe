package main

import (
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/platform/ebitenapp"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// Host window policy is independent of the authored logical canvases.
// See DESIGN_PRESENTATION_CLIENT §2.1.
func startupFullscreen(opts Options, saved bool) bool {
	if opts.FullscreenSet {
		return opts.Fullscreen
	}
	return saved
}

func (g *gameShell) windowOptions() ebitenapp.RunOptions {
	return windowOptionsFor(func() *gameShell { return g })
}

// windowOptions is the menu path's adapter: every callback reaches the shell
// the host holds when it runs, so after an in-process content reload the
// window polls the new shell rather than the start-up one
// (docs/DESIGN_MODS_MUTATORS.md §4.4).
func (h *shellHost) windowOptions() ebitenapp.RunOptions {
	return windowOptionsFor(func() *gameShell { return h.shell })
}

// windowOptionsFor builds the platform adapter's options from the shell
// current returns, reading it again inside each callback.
func windowOptionsFor(current func() *gameShell) ebitenapp.RunOptions {
	g := current()
	options := windowRunOptions(g.opts)
	g.fullscreen = startupFullscreen(g.opts, g.fullscreen)
	options.Fullscreen = g.fullscreen
	options.IdleAllowed = func() bool {
		g := current()
		return g.frontend != nil && g.frontend.Mode == modeBattle && g.battle != nil && g.battle.postBattle == nil
	}
	options.IdleService = func(focused bool) time.Duration {
		pumpAudio(time.Now())
		if owner := current().audioOwner; focused && owner != nil {
			serviceMusic(owner)
			if delay := owner.MusicServiceDelay(); delay > 0 {
				return min(time.Second, delay)
			}
		}
		// Reap completed device buffers without stepping or redrawing battle.
		return time.Second
	}
	options.PresentationSettings = func() (ebitenapp.RendererMode, int) {
		g := current()
		return rendererMode(g.opts), g.presentation.FPS
	}
	// The Enhanced effect switches are polled the same way, so an options-page
	// edit previews on the next update (DESIGN_GPU_RENDERER §30).
	options.Effects = func() drawlist.Effects {
		g := current()
		applyEffectStrengths(clPtr, g.presentation)
		return presentationEffects(g.presentation)
	}
	options.ShowFPS = func() bool {
		g := current()
		return g.battle != nil && g.battle.fpsShown() && g.battle.postBattle == nil
	}
	options.RendererChanged = func(mode ebitenapp.RendererMode) { current().rendererChanged(mode) }
	g.commitWindowSize()
	options.WindowSize = func() (int, int) {
		g := current()
		return g.windowSize.W, g.windowSize.H
	}
	options.FullscreenChanged = func(value bool) {
		g := current()
		g.fullscreen = value
		if g.settingsWritable {
			saveFullscreenSetting(value)
		}
	}
	return options
}

// commitWindowSize applies the chosen resolution after the options root closes
// through OK. Keeping pending slider edits out of the adapter prevents a resize
// from moving the pointer relative to its captured widget
// (DESIGN_PRESENTATION_CLIENT §2.1).
func (g *gameShell) commitWindowSize() {
	g.windowSize = retailDisplayMode{g.display.Width, g.display.Height}
}

// desktopDisplayFit is the version of the one-time monitor fit; a desktop
// whose saved fit is older is fitted again, once.
const desktopDisplayFit = 1

// desktopFitWidth caps the fitted battle's width: past it the software
// presentation costs more than the larger view gives.
const desktopFitWidth = 2560

// fitDesktopDisplay gives a desktop still at retail's 4:3 default battle size
// its monitor's proportions once, fullscreen, the first time the window can
// read the monitor (user-requested 2026-10-27; DESIGN_PRESENTATION_CLIENT
// §2.1). A size the player chose is kept, and later choices in the options
// and the Nanolathe screen stand: the fit is recorded and not repeated.
func (g *gameShell) fitDesktopDisplay() {
	if g == nil || !g.settingsWritable || g.displayFitted >= desktopDisplayFit || runtime.GOOS == "android" || runtime.GOOS == "js" {
		return
	}
	pw, ph := ebitenapp.DesktopPixelSize()
	if pw < settings.MinDisplaymodeWidth || ph < settings.MinDisplaymodeHeight {
		return
	}
	if g.fitDisplayTo(pw, ph) && !g.opts.FullscreenSet && !g.fullscreen {
		ebiten.SetFullscreen(true)
	}
	g.saveSettings()
}

// fitDisplayTo records the fit and, when the battle size is still the
// default, takes the monitor's pixel size, scaled down at its own aspect to
// desktopFitWidth. It reports whether the size changed.
func (g *gameShell) fitDisplayTo(pw, ph int) bool {
	g.displayFitted = desktopDisplayFit
	if g.display.Width != settings.DefaultDisplaymodeWidth || g.display.Height != settings.DefaultDisplaymodeHeight {
		return false
	}
	w, h := pw, ph
	if w > desktopFitWidth {
		w, h = desktopFitWidth, (desktopFitWidth*ph+pw/2)/pw
	}
	h = max(h, settings.MinDisplaymodeHeight)
	if w == g.display.Width && h == g.display.Height {
		return false
	}
	g.display.Width, g.display.Height = w, h
	g.commitWindowSize()
	return true
}

// A host fullscreen toggle is independent of the options transaction. Read the
// saved block so it cannot persist pending resolution or other page edits.
func saveFullscreenSetting(value bool) {
	current, err := settings.Load()
	if err == nil {
		current.Fullscreen = value
		err = current.Save()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: %v\n", err)
	}
}
