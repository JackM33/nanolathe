//go:build android

package ebitenapp

import (
	"errors"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/mobile"
)

// runGame hands the game to the Android view, whose render thread drives
// Update and Draw (docs/ANDROID.md §2). ebiten.RunGame does not exist there.
// The view only logs an Update error, so the first one is also returned here
// for the caller to report; a Termination ends the process from the view's
// side and never reaches this call.
func runGame(game ebiten.Game) error {
	errs := make(chan error, 1)
	mobile.SetGame(&mobileGame{Game: game, errs: errs})
	return <-errs
}

// mobileGame forwards the game unchanged apart from copying its first error.
// app implements no optional Ebitengine interfaces, so embedding hides none.
type mobileGame struct {
	ebiten.Game
	errs chan<- error
}

func (g *mobileGame) Update() error {
	err := g.Game.Update()
	if err != nil && !errors.Is(err, ebiten.Termination) {
		select {
		case g.errs <- err:
		default:
		}
	}
	return err
}
