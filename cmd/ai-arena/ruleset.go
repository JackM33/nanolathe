package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pathlab"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	aikitmod "github.com/nanolathe-gg/nanolathe/mods/aikit"
)

// The arena's research rule set is registered by the arena itself rather than
// by mods/aikit: the game links every set in mods/all.go, and "aikit" — Modern
// rules, full computer income and a think step that runs whatever brain the
// arena installs, the retail step otherwise — is a harness, not a way to play
// (docs/DESIGN_GAMEPLAY_RULES.md §8).
// "aikit-retail-income" is the same harness with the retail income discount
// (-income retail).
func init() {
	session.RegisterRuleSet(aikitmod.ArenaSet, aikitmod.ArenaRuleSet)
	session.RegisterRuleSet(aikitmod.ArenaRetailIncomeSet, aikitmod.ArenaRetailIncomeRuleSet)
	// Other movement under the same harness, for the pathfinding laboratory
	// (docs/PATHFINDING_LAB.md "Whole games"): whole games played by the
	// same brains, for what a short moment cannot show. "aikit" itself plays
	// Modern's movement. "aikit-retail-move" is the harness with retail's
	// movement and route search: what the same brains' games look like under
	// the rules the recorded games were played by. "aikit-<set>" is the
	// harness with the movement and the search of a laboratory rule set:
	// "aikit-lab-overlap" is Modern's movement as it stood before its traffic
	// policy.
	session.RegisterRuleSet("aikit-retail-move", func() session.RuleSet {
		return harness(movement.StrictRules{}, path.RetailKernel{})
	})
	for _, name := range pathlab.RuleSetNames() {
		session.RegisterRuleSet("aikit-"+name, func() session.RuleSet {
			set, ok := session.LookupRuleSet(name)
			if !ok {
				panic("nanolathe: the arena's movement set is not linked: logical path internal/pathlab, providers searched [session rule sets], expected " + name)
			}
			return harness(set.Movement, set.Path)
		})
	}
	// "aikit-community-3.9" and "aikit-strict-3.1" are the harness on the
	// other reserved bases: the same brains and full income under that
	// mode's rules, every seam its base's. A Modern AI player plays in every
	// mode (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI computer player"), and
	// faction balance is rechecked in each (docs/PLAN_FACTION_BALANCE.md §1).
	for _, base := range []gameplay.Mode{gameplay.Community39, gameplay.Strict31} {
		session.RegisterRuleSet("aikit-"+string(base), func() session.RuleSet {
			return session.RuleSet{Base: base, Planner: aikit.HostPlanner{}, ComputerIncome: session.FullComputerIncome{}}
		})
	}
}

// harness is the arena's research set with another movement policy and
// search kernel.
func harness(rules movement.Rules, kernel path.Kernel) session.RuleSet {
	return session.RuleSet{Base: gameplay.Modern, Planner: aikit.HostPlanner{}, ComputerIncome: session.FullComputerIncome{}, Movement: rules, Path: kernel}
}
