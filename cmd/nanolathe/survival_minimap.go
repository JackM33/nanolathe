package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
)

// drawSurvivalMinimap marks every announced approach for the warning's entire
// countdown. The committed frame owns these points in offline and online
// Survival alike; fog does not hide a public announcement. This is Nanolathe
// presentation policy (DESIGN_SURVIVAL §9), independent of gameplay mode.
func (h *retailBattleHUD) drawSurvivalMinimap(c *client.Client, status frame.SurvivalStatus, layout camera.Minimap, dst hud.Rect, playW, playH int32) {
	if h == nil || c == nil || !status.Active || status.Phase != frame.SurvivalWarning || playW <= 0 || playH <= 0 {
		return
	}
	for _, entry := range status.Approaches[:min(int(status.ApproachCount), len(status.Approaches))] {
		x, y := layout.WorldToRadar(entry.X, entry.Z, playW, playH)
		c.DrawMinimapEntryMarker(dst, layout, x, y, entry.Angle, h.paletteIndex(14), h.paletteIndex(0))
	}
}
