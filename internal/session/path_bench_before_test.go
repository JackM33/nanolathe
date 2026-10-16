//go:build pathbench && retail

package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/path"
)

// Comparison rule sets for the opt-in path benchmark (docs/PATH_BENCHMARK.md
// "Comparison rule sets"). modern-no-pathfinding is Modern with every Modern
// pathfinding policy switched off; each modern-no-<policy> set switches off one,
// so a run can attribute a change to the policy that makes it. modern-overlap
// is Modern's movement as it stood before its traffic policy replaced the
// overlap policies.

type modernNoPathfinding struct{ movement.ModernRules }

func (*modernNoPathfinding) PathWorkBound(*movement.System) (int32, bool)      { return 0, false }
func (*modernNoPathfinding) PathBatch(*movement.System) (int, int32)           { return 0, 0 }
func (*modernNoPathfinding) PathOpenList(*movement.System) path.OpenList       { return path.OpenListHeap }
func (*modernNoPathfinding) GroupDestinationSlots(*movement.System) bool       { return false }
func (*modernNoPathfinding) UnreachableMoves(*movement.System) (int32, uint32) { return 0, 0 }
func (*modernNoPathfinding) WedgeEscape(*movement.System) bool                 { return false }
func (*modernNoPathfinding) Traffic(*movement.System) movement.Traffic         { return movement.Traffic{} }
func (*modernNoPathfinding) RepathDelay(*movement.System, int, uint32) uint32  { return 60 }

type modernNoBound struct{ movement.ModernRules }

func (*modernNoBound) PathWorkBound(*movement.System) (int32, bool) { return 0, false }

// modernNoBatch keeps the retail single-request scheduler.
type modernNoBatch struct{ movement.ModernRules }

func (*modernNoBatch) PathBatch(*movement.System) (int, int32) { return 0, 0 }

// modernNoBuckets keeps the retail heap as every search's open list.
type modernNoBuckets struct{ movement.ModernRules }

func (*modernNoBuckets) PathOpenList(*movement.System) path.OpenList { return path.OpenListHeap }

type modernNoSlots struct{ movement.ModernRules }

func (*modernNoSlots) GroupDestinationSlots(*movement.System) bool { return false }

type modernNoUnreach struct{ movement.ModernRules }

func (*modernNoUnreach) UnreachableMoves(*movement.System) (int32, uint32) { return 0, 0 }

type modernNoWedge struct{ movement.ModernRules }

func (*modernNoWedge) WedgeEscape(*movement.System) bool { return false }

// modernNoPrompt keeps retail's re-route throttle.
type modernNoPrompt struct{ movement.ModernRules }

func (*modernNoPrompt) RepathDelay(*movement.System, int, uint32) uint32 { return 60 }

// modernTrafficWithout is Modern with one part of its traffic policy switched
// off.
type modernTrafficWithout struct {
	movement.ModernRules
	off func(*movement.Traffic)
}

func (r *modernTrafficWithout) Traffic(s *movement.System) movement.Traffic {
	t := r.ModernRules.Traffic(s)
	r.off(&t)
	return t
}

// pilotsWithout is Modern's pilots with the one at place left out: route
// claims stand first and arrival places second.
func pilotsWithout(t *movement.Traffic, place int) {
	pilots, _ := t.Pilot.(movement.Pilots)
	pilots[place] = nil
	t.Pilot = pilots
}

func init() {
	RegisterRuleSet("modern-no-pathfinding", func() RuleSet {
		return RuleSet{Movement: &modernNoPathfinding{}, Path: path.RetailKernel{}}
	})
	RegisterRuleSet("modern-overlap", func() RuleSet {
		return RuleSet{Movement: &movement.OverlapRules{}, Path: path.StraightenKernel{}}
	})
	RegisterRuleSet("modern-no-bound", func() RuleSet { return RuleSet{Movement: &modernNoBound{}} })
	RegisterRuleSet("modern-no-batch", func() RuleSet { return RuleSet{Movement: &modernNoBatch{}} })
	RegisterRuleSet("modern-no-buckets", func() RuleSet { return RuleSet{Movement: &modernNoBuckets{}} })
	RegisterRuleSet("modern-no-slots", func() RuleSet { return RuleSet{Movement: &modernNoSlots{}} })
	RegisterRuleSet("modern-no-unreach", func() RuleSet { return RuleSet{Movement: &modernNoUnreach{}} })
	RegisterRuleSet("modern-no-wedge", func() RuleSet { return RuleSet{Movement: &modernNoWedge{}} })
	RegisterRuleSet("modern-no-prompt", func() RuleSet { return RuleSet{Movement: &modernNoPrompt{}} })
	RegisterRuleSet("modern-no-straighten", func() RuleSet { return RuleSet{Path: path.RetailKernel{}} })
	RegisterRuleSet("modern-no-smooth", func() RuleSet { return RuleSet{Path: path.StraightenKernel{}} })
	for _, set := range []struct {
		name string
		off  func(*movement.Traffic)
	}{
		{"modern-no-steering", func(t *movement.Traffic) { t.Sidestep = false }},
		{"modern-no-weight", func(t *movement.Traffic) { t.HeuristicScale, t.BusyScale, t.BusyWork = 0, 0, 0 }},
		{"modern-no-through", func(t *movement.Traffic) { t.Through, t.ThroughAfter = 0, 0 }},
		{"modern-no-claims", func(t *movement.Traffic) { pilotsWithout(t, 0) }},
		{"modern-no-places", func(t *movement.Traffic) { pilotsWithout(t, 1) }},
	} {
		RegisterRuleSet(set.name, func() RuleSet {
			return RuleSet{Movement: &modernTrafficWithout{off: set.off}}
		})
	}
}
