// Package labrules is the pathfinding laboratory's list of movement policies
// (docs/PATHFINDING_LAB.md): the baseline every measurement was made
// against, the policy Modern adopted, and between them the rungs of the
// ladder that led from one to the other, each as it was measured.
//
// A set is a movement policy and a search kernel. It is not a rule set: the
// laboratory's replay (internal/pathlab), the AI arena and the path
// benchmark each register the list under their own registry entries, and
// the game links none of them. The package imports nothing above the
// movement system, so that the session's own benchmark tests can read it.
package labrules

import (
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/path"
)

// Set is one named movement policy with the kernel its routes are searched
// by.
type Set struct {
	Name     string
	Movement movement.Rules
	Kernel   path.Kernel
}

// Baseline names Modern's movement as it stood until 2026-09-29:
// movement.OverlapRules and the straightening kernel. It is what "modern"
// named in every measurement made before the adoption. Adopted names the
// set that answers as Modern does now.
const (
	Baseline = "lab-overlap"
	Adopted  = "next-v4"
)

// The kernels: the baseline's, which a set that names none keeps, and
// Modern's.
var (
	straighten path.Kernel = path.StraightenKernel{}
	smooth     path.Kernel = path.SmoothKernel{}
)

// rules is the baseline with its overlap policies kept or removed, one
// traffic policy and one re-route throttle. It carries its answers, which a
// reserved rule set may not (docs/DESIGN_GAMEPLAY_RULES.md §3); a laboratory
// set is never saved.
type rules struct {
	movement.OverlapRules
	// overlap keeps the baseline's three overlap policies; without it no
	// unit enters a cell another holds.
	overlap bool
	// last, when positive, is a jam release as the last resort: a mover
	// friends have held for that many ticks running passes through them for
	// three seconds.
	last    uint16
	traffic movement.Traffic
	// repath, when positive, is the re-request throttle in ticks; zero keeps
	// the baseline's staggered sixty.
	repath uint32
	// modernRepath answers Modern's own throttle instead, held followers
	// included (docs/DESIGN_MOVEMENT_PATH.md "Modern prompt re-routing"), so
	// that the adopted set keeps answering as Modern does.
	modernRepath bool
}

// modern answers the adopted set's re-request throttle.
var modern movement.ModernRules

func (r *rules) AlliedPassThrough(s *movement.System) bool {
	return r.overlap && r.OverlapRules.AlliedPassThrough(s)
}

func (r *rules) JamRelease(s *movement.System) (uint16, uint32) {
	switch {
	case r.last > 0:
		return r.last, 90
	case r.overlap:
		return r.OverlapRules.JamRelease(s)
	}
	return 0, 0
}

func (r *rules) PocketRelease(s *movement.System) (int32, uint32) {
	if r.overlap {
		return r.OverlapRules.PocketRelease(s)
	}
	return 0, 0
}

func (r *rules) Traffic(*movement.System) movement.Traffic { return r.traffic }

func (r *rules) RepathDelay(s *movement.System, slot int, last uint32) uint32 {
	if r.modernRepath {
		return modern.RepathDelay(s, slot, last)
	}
	if r.repath > 0 {
		return r.repath
	}
	return r.OverlapRules.RepathDelay(s, slot, last)
}

// All lists every set, in the order the laboratory's documents take them.
func All() []Set {
	var sets []Set
	add := func(name string, r rules, kernel path.Kernel) {
		sets = append(sets, Set{Name: name, Movement: &r, Kernel: kernel})
	}
	with := func(t movement.Traffic, f func(*movement.Traffic)) movement.Traffic {
		f(&t)
		return t
	}

	// The baseline, and what its parts are worth.
	sets = append(sets, Set{Name: Baseline, Movement: &movement.OverlapRules{}, Kernel: straighten})
	add("lab-no-overlap", rules{}, straighten)
	add("lab-smooth", rules{}, smooth)
	add("lab-modern-smooth", rules{overlap: true}, smooth)
	add("lab-modern-smooth-s", rules{overlap: true, traffic: movement.Traffic{LegsThroughMovers: true}}, smooth)
	add("lab-w15", rules{traffic: movement.Traffic{HeuristicScale: 0x18000}}, straighten)
	add("lab-w30", rules{traffic: movement.Traffic{HeuristicScale: 0x30000}}, straighten)

	// Steering over the floor, rule by rule.
	steer := movement.Traffic{Sidestep: true, PassBehind: true, NoBrake: true}
	add("lab-steer-only", rules{traffic: movement.Traffic{Sidestep: true}}, straighten)
	add("lab-steer-behind", rules{traffic: movement.Traffic{Sidestep: true, PassBehind: true}}, straighten)
	add("lab-steer-plain", rules{traffic: with(steer, func(t *movement.Traffic) { t.NoOvertake = true })}, straighten)
	add("next-steer", rules{traffic: steer}, straighten)
	add("next-steer-smooth", rules{traffic: with(steer, func(t *movement.Traffic) { t.LegsThroughMovers = true })}, smooth)

	// The base: steering, smoothed routes whose legs may cross friends on
	// the move, one heuristic weight and a re-request throttle of fifteen
	// ticks. Every set from here on has the smoothing kernel.
	base := with(steer, func(t *movement.Traffic) { t.LegsThroughMovers, t.HeuristicScale = true, 0x18000 })
	next := func(name string, t movement.Traffic) { add(name, rules{traffic: t, repath: 15}, smooth) }
	beside := func(p movement.Pilot) movement.Traffic {
		return with(base, func(t *movement.Traffic) { t.Pilot = p })
	}
	next("next", base)
	add("next-r60", rules{traffic: base}, smooth)
	next("next-w0", with(base, func(t *movement.Traffic) { t.HeuristicScale = 0 }))

	// Places and claims, each alone and together.
	places := movement.ArrivePilot{Places: true}
	arrive := movement.ArrivePilot{Places: true, Exchange: true}
	claims := movement.ClaimsPilot{Rank: true, Per: 4, Oncoming: 24}
	next("next-places", beside(places))
	next("next-arrive", beside(arrive))
	next("next-claims", beside(claims))
	next("next-spread", beside(movement.ClaimsPilot{Rank: true}))
	next("next-spread-flat", beside(movement.ClaimsPilot{}))
	next("next-spread-4", beside(movement.ClaimsPilot{Rank: true, Per: 4}))
	next("next-spread-16", beside(movement.ClaimsPilot{Rank: true, Per: 16}))
	next("next-spread-arrive", beside(movement.Pilots{movement.ClaimsPilot{Rank: true}, arrive}))
	next("next-c-opp16", beside(movement.Pilots{movement.ClaimsPilot{Rank: true, Oncoming: 16}, arrive}))
	next("next-c-opp32", beside(movement.Pilots{movement.ClaimsPilot{Rank: true, Oncoming: 32}, arrive}))
	all := beside(movement.Pilots{claims, arrive})
	next("next-all", all)

	// What the search work buys: the heuristic's weight, the weight while
	// route searching is busy, and the throttle.
	weighed := func(scale int32) movement.Traffic {
		return with(all, func(t *movement.Traffic) { t.HeuristicScale = scale })
	}
	busy := func(scale, work int32) movement.Traffic {
		return with(all, func(t *movement.Traffic) { t.BusyScale, t.BusyWork = scale, work })
	}
	idle := func(scale int32) movement.Traffic {
		return with(busy(0x30000, 3000), func(t *movement.Traffic) { t.HeuristicScale = scale })
	}
	next("next-all-w0", weighed(0))
	next("next-all-w20", weighed(0x20000))
	next("next-all-w30", weighed(0x30000))
	next("next-all-busy", busy(0x30000, 6000))
	next("next-all-busy3k", busy(0x30000, 3000))
	next("next-all-busy12k", busy(0x30000, 12000))
	next("next-all-idle125", idle(0x14000))
	next("next-all-idle100", idle(0x10000))
	add("next-all-r30", rules{traffic: all, repath: 30}, smooth)
	add("next-all-w30-r30", rules{traffic: weighed(0x30000), repath: 30}, smooth)

	// Settling at the edge: after how long, and held by what.
	settle := func(ticks uint32, parked bool) movement.Traffic {
		return with(idle(0x18000), func(t *movement.Traffic) {
			t.Pilot = movement.Pilots{claims, movement.ArrivePilot{Places: true, Exchange: true, Stuck: ticks, StuckParked: parked}}
		})
	}
	next("next-all-stuck20", settle(20, false))
	next("next-all-stuck45", settle(45, false))
	next("next-all-stuck90", settle(90, false))
	next("next-all-walled10", settle(10, true))
	next("next-all-walled30", settle(30, true))
	next("next-v2", settle(20, true))

	// Steering that turns less: the second candidate.
	calm := func(t movement.Traffic) movement.Traffic {
		return with(t, func(t *movement.Traffic) { t.ClearOnly, t.NoOvertake = true, true })
	}
	next("next-calm-clear", with(idle(0x18000), func(t *movement.Traffic) { t.ClearOnly = true }))
	next("next-calm-noover", with(idle(0x18000), func(t *movement.Traffic) { t.NoOvertake = true }))
	next("next-calm-nobehind", with(calm(idle(0x18000)), func(t *movement.Traffic) { t.PassBehind = false }))
	next("next-v2-calm", calm(settle(20, true)))

	// What whole games asked for: settling short of a sealed goal, a route
	// through friends, steering that looks to the next turn, and a refused
	// mover keeping the way round it chose. The third candidate, its parts,
	// and the policy Modern adopted.
	third := func(sealed uint32, through uint8, after uint32) movement.Traffic {
		return with(idle(0x18000), func(t *movement.Traffic) {
			t.Pilot = movement.Pilots{claims, movement.ArrivePilot{Places: true, Exchange: true, Stuck: 20, StuckParked: true, Sealed: sealed}}
			t.Through, t.ThroughAfter = through, after
		})
	}
	to := func(t movement.Traffic) movement.Traffic {
		return with(t, func(t *movement.Traffic) { t.ToWaypoint = true })
	}
	adopted := with(to(calm(third(150, 2, 150))), func(t *movement.Traffic) { t.KeepRound = true })
	next("next-v3-sealed", calm(third(150, 0, 0)))
	next("next-v3-through-quiet", calm(third(0, 2, 0)))
	next("next-v3-through1-quiet", calm(third(0, 1, 0)))
	next("next-v3", calm(third(150, 2, 0)))
	next("next-v3-after90", calm(third(150, 2, 90)))
	next("next-v3-after150", calm(third(150, 2, 150)))
	next("next-v3-after300", calm(third(150, 2, 300)))
	next("next-v3-eager", third(150, 2, 0))
	next("next-v3-eager-to", to(third(150, 2, 150)))
	next("next-v3-to", to(calm(third(150, 2, 150))))
	// Measured with the flat fifteen; it now answers Modern's throttle, which
	// added held followers after the adoption.
	add(Adopted, rules{traffic: adopted, repath: 15, modernRepath: true}, smooth)

	// A jam release as the last resort, which was measured and left out.
	add("next-all-last150", rules{traffic: all, repath: 15, last: 150}, smooth)
	add("next-v3-last150", rules{traffic: to(calm(third(150, 2, 150))), repath: 15, last: 150}, smooth)
	add("next-v3-last300", rules{traffic: to(calm(third(150, 2, 150))), repath: 15, last: 300}, smooth)
	add("next-v4-last150", rules{traffic: adopted, repath: 15, last: 150}, smooth)

	// The parts of the adopted policy as they would have played had each
	// landed alone: the better routes with the overlap kept, and steering
	// without the rules for arriving.
	routes := movement.Traffic{LegsThroughMovers: true, HeuristicScale: 0x18000, BusyScale: 0x30000, BusyWork: 3000}
	add("step1", rules{overlap: true, traffic: routes, repath: 15}, smooth)
	add("step1-r60", rules{overlap: true, traffic: routes}, smooth)
	add("step1-w0", rules{overlap: true, traffic: movement.Traffic{LegsThroughMovers: true}, repath: 15}, smooth)
	add("step2", rules{traffic: with(routes, func(t *movement.Traffic) {
		t.Sidestep, t.PassBehind, t.NoBrake = true, true, true
		t.ClearOnly, t.NoOvertake, t.ToWaypoint, t.KeepRound = true, true, true, true
	}), repath: 15}, smooth)
	return sets
}
