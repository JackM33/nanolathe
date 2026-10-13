package session

import (
	"reflect"
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/survival"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The minimap receives precisely the public warning geometry, without exposing
// terrain-adjusted spawn sites or consuming another planning draw (§6.7, §9).
func TestSurvivalWarningFrameApproaches(t *testing.T) {
	cat := minimalCatalogForStrict()
	s := &Session{Units: units.NewSliced(2, cat), World: &world.Terrain{CellW: 96, CellH: 48}}
	s.Survival = &survivalState{attacker: 1, centreX: 35, centreZ: 19,
		phase: survivalWarning, wave: 4, phaseEnd: 100,
		tuning: survival.DefaultTuning(survival.PaceNormal)}
	sim, crt := *s.SimRNG(), *s.CrtRNG()
	for direction := uint16(0); direction < 8; direction++ {
		angles := [3]uint16{direction * 8192, direction*8192 + 21845, direction*8192 + 43690}
		plan := survival.Wave{Groups: []survival.Group{
			{Angle: angles[0], Domain: survival.Ground, Picks: []int{0}},
			{Angle: angles[1], Domain: survival.Air, Picks: []int{1}},
			{Angle: angles[2], Domain: survival.Naval, Picks: []int{2}},
		}}
		s.Survival.plan = plan
		before := *s.Survival
		before.plan.Groups = slices.Clone(plan.Groups)
		for i := range before.plan.Groups {
			before.plan.Groups[i].Picks = slices.Clone(plan.Groups[i].Picks)
		}
		got := s.survivalFrameStatus(70)
		if !got.Active || got.Phase != frame.SurvivalWarning || got.Wave != 4 || got.SecondsLeft != 1 || got.ApproachCount != 3 {
			t.Fatalf("warning frame = %+v", got)
		}
		for i, angle := range angles {
			x, z := s.survivalEntryCell(angle)
			want := frame.SurvivalApproach{X: x*16 + 8, Z: z*16 + 8, Angle: angle}
			if got.Approaches[i] != want {
				t.Fatalf("direction %d group %d = %+v, want announced entry %+v", direction, i, got.Approaches[i], want)
			}
		}
		if *s.SimRNG() != sim || *s.CrtRNG() != crt || !reflect.DeepEqual(*s.Survival, before) {
			t.Fatal("publishing warning geometry changed a random stream or director state")
		}
		// Replanning the director cannot alter an already committed frame, nor
		// can a presentation copy mutate the director or another status value.
		saved := got
		s.Survival.plan.Groups[0].Angle++
		if got != saved {
			t.Fatal("warning frame aliases director geometry")
		}
		got.Approaches[0].X++
		if s.survivalFrameStatus(70).Approaches[0].Angle != angles[0]+1 || got == saved {
			t.Fatal("warning geometry was not copied independently")
		}
	}
}

func TestSurvivalFrameClearsUnannouncedApproaches(t *testing.T) {
	cat := minimalCatalogForStrict()
	s := &Session{Units: units.NewSliced(2, cat), World: &world.Terrain{CellW: 32, CellH: 32}}
	s.Survival = &survivalState{attacker: 1, centreX: 16, centreZ: 16,
		tuning: survival.DefaultTuning(survival.PaceNormal),
		plan:   survival.Wave{Groups: []survival.Group{{Angle: 0}}}}
	for _, phase := range []survivalPhase{survivalGrace, survivalActive, survivalDowntime} {
		s.Survival.phase = phase
		got := s.survivalFrameStatus(1)
		if got.ApproachCount != 0 || got.Approaches != [3]frame.SurvivalApproach{} {
			t.Fatalf("phase %d retained an announced direction: %+v", phase, got)
		}
	}
	s.Survival.phase, s.Survival.plan.Groups = survivalWarning, nil
	if got := s.survivalFrameStatus(1); got.ApproachCount != 0 || got.Approaches != [3]frame.SurvivalApproach{} {
		t.Fatalf("unplanned warning invented a direction: %+v", got)
	}
	s.Survival = nil
	if got := s.survivalFrameStatus(1); got != (frame.SurvivalStatus{}) {
		t.Fatalf("ordinary battle published Survival status: %+v", got)
	}
}
