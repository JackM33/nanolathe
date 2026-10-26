package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Selection, footer hover and contextual orders share the modern icon picker;
// the client's normal-zoom branch retains the retail hull contract (§18.4).
func (b *battleSession) pickPresentedUnit(f *frame.Frame, x, y int32, viewer uint8) (pool.Handle, frame.UnitView, bool) {
	if b.cl != nil && b.cl.StrategicIconsActive() {
		return b.cl.PickPresentedUnit(f, x, y, viewer)
	}
	return client.PickSnapshotUnitWithMargin(f, x, y, b.cam, viewer, b.hostPreferences().UnitPickMargin)
}

// pickRadarAttackTarget is the separate, attack-only adapter for anonymous
// main-view dots (DESIGN_INTERFACE_HUD_INPUT "Modern radar dots"). Identified
// picking, footer hover and unit-info continue to use pickPresentedUnit.
func (b *battleSession) pickRadarAttackTarget(x, y int32, latch input.Latch) (pool.Handle, *units.Unit) {
	if b == nil || b.cl == nil || (latch != input.LatchNormal && latch != input.LatchAttack) || b.classifyPointer(x, y) != battlePointerViewport || b.megamapOwnsPointer(x, y) {
		return 0, nil
	}
	f, ok := b.currentSnapshot()
	if !ok {
		return 0, nil
	}
	h := b.cl.PickRadarDot(f, x, y, f.ViewingPlayer)
	v, found := snapshotUnitByHandle(f, h)
	if h == 0 || !found || int(f.Selection.LocalPlayer) >= len(f.Players) || v.Owner == f.Selection.LocalPlayer || (int(v.Owner) < len(f.Players) && f.Players[f.Selection.LocalPlayer].Allies[v.Owner]) {
		return 0, nil
	}
	// This short-lived copy is used only by the existing attack admission.
	// No metadata from it reaches a footer, tooltip, selection or info panel.
	return h, b.snapshotUnitCopy(v)
}
