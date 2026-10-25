//go:build !android

package ebitenapp

import "github.com/hajimehoshi/ebiten/v2"

// runGame owns the desktop and browser main loop until the window closes.
func runGame(game ebiten.Game) error {
	return ebiten.RunGame(game)
}
