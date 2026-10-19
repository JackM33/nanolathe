package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Nanolathe Modern policy: DESIGN_WEAPONS_PROJECTILES §2.3.2 "No room". A
// laser tower cannot step aside. Behind a Stumpy wreck its beam passes the
// wreck between samples, so it now fires and hits a parked Weasel instead of
// waiting. Behind a vehicle plant's wreck the beam would stop in the wreck,
// and a laser's blast never reaches the wreck's centre, so it still holds;
// Strict fires into that wreck as retail does. A lava spire cannot be
// destroyed and holds it too.
func TestModernNoRoomTowerFiresPastWreck(t *testing.T) {
	f := loadRetailFixture(t)
	stumpy := f.cat.Features[content.CanonicalKey("armstump_dead")]
	plant := f.cat.Features[content.CanonicalKey("armvp_dead")]
	spire := f.cat.Features[content.CanonicalKey("LavaSpire006C")]
	if stumpy == nil || plant == nil || spire == nil {
		t.Skip("retail fixture features armstump_dead, armvp_dead and LavaSpire006C are absent")
	}
	for _, tc := range []struct {
		name    string
		mode    gameplay.Mode
		feature *content.FeatureDef
		fires   bool
		hits    bool
	}{
		{"stumpy wreck", gameplay.Modern, stumpy, true, true},
		{"plant wreck", gameplay.Modern, plant, false, false},
		{"strict plant wreck", gameplay.Strict31, plant, true, false},
		{"spire", gameplay.Modern, spire, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := f.session(t)
			s.SetGameplay(tc.mode)
			stepRetail(s, 2)
			// Keep the map's starting commanders out of this isolated fight.
			for player := 0; player < 2; player++ {
				s.Units.ForEachPlayerSliceLive(player, func(u *units.Unit) {
					u.Flags &^= units.StandingFieldMask << units.StandingFireShift
					for i := 0; i < combat.NumSlots; i++ {
						u.SlotAt(i).Flags &^= units.SlotFlagEnabled
					}
				})
			}
			for _, ai := range s.AI {
				if ai != nil {
					for i := range ai.Deadlines {
						ai.Deadlines[i] = ^uint32(0)
					}
				}
			}
			tower := placeCompleteRetailUnit(t, s, "ARMLLT", 0, numeric.FixedFromInt(600), numeric.FixedFromInt(600))
			weasel := placeCompleteRetailUnit(t, s, "CORFAV", 1, numeric.FixedFromInt(600), numeric.FixedFromInt(800))
			weasel.Flags &^= units.StandingFieldMask<<units.StandingMoveShift | units.StandingFieldMask<<units.StandingFireShift
			weasel.SlotAt(0).Flags &^= units.SlotFlagEnabled
			eligible, observers := visibilityModeRefreshInputs(s, 3)
			s.Vis.RefreshMode(3, true, eligible, observers)
			// Anchor the footprint so it covers the line of fire, x = 600.
			cx, cz := int32(600/16)-tc.feature.FootprintX/2, int32(680/16)
			s.Features.PlaceAt(int(cx), int(cz), tc.feature)
			if def, ok := s.World.FeatureDefAt(s.World.PlotAt(cx, cz).Feature()); !ok || def != tc.feature || s.World.PlotAt(600/16, cz).IsEmpty() {
				t.Fatal("feature does not cover the line of fire")
			}
			health, hit := weasel.Health, false
			shots := map[[2]uint32]bool{}
			for i := 0; i < 900 && !hit; i++ {
				s.Step(s.Clock.ScaledAnchor + 1)
				hit = weasel.Health < health
				s.Combat.ForEachAliveInEntrySpan(func(h pool.Handle, p *combat.Projectile) {
					if p.Shooter == tower.Handle {
						shots[[2]uint32{uint32(h), p.CreationTick}] = true
					}
				})
			}
			t.Logf("shots=%d hit=%v", len(shots), hit)
			if (len(shots) > 0) != tc.fires || hit != tc.hits {
				t.Fatalf("fired=%v hit=%v, want fired=%v hit=%v", len(shots) > 0, hit, tc.fires, tc.hits)
			}
		})
	}
}
