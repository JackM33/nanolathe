package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Nanolathe Modern policy: a held follower waits for its way to change
// (docs/DESIGN_MOVEMENT_PATH.md "Modern prompt re-routing", "Held
// followers").

// heldFollowerFixture is a follower refused by a mobile unit, standing at the
// start of the route a search gave it, and last admitted at tick 110 without
// having changed cells since.
func heldFollowerFixture(t *testing.T) (*System, pool.Handle, pool.Handle) {
	t.Helper()
	terrain := syntheticTerrainForIntegrate()
	sys := NewSystem(terrain, wiringProfile, NewOccupancyGrid())
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)
	var hs [2]pool.Handle
	for i, x := range []int32{5, 6} {
		wx, wz := world.CellToWorld(x), world.CellToWorld(5)
		h, err := w.Create(wiringDef(), 0, wx, terrain.HeightAt(wx, wz), wz)
		if err != nil {
			t.Fatal(err)
		}
		sys.EnsureUnit(w.Unit(h))
		hs[i] = h
	}
	follower, blocker := hs[0], hs[1]
	coll := handleRow(sys.Collisions, follower)
	coll.Blocked, coll.BlockerID, coll.LastStampTick = true, int(blocker), 100
	a := coll.CachedAnchor
	route := handleRow(sys.Routes, follower)
	route.Points[0] = Point{X: (int32(int16(a.X*2)) + int32(wiringProfile.FootPrintX)) * 8, Z: (int32(int16(a.Z*2)) + int32(wiringProfile.FootPrintZ)) * 8}
	route.Points[1] = Point{X: route.Points[0].X + 64, Z: route.Points[0].Z}
	route.Count, route.Active, route.WantsRepath, route.LastRequestTick = 2, true, true, 110
	return sys, follower, blocker
}

// A held follower is admitted again when the unit that refused its step would
// be walled — thirty-one ticks after that unit's last commit — no earlier than
// fifteen ticks and no later than sixty after its last admission; any
// follower that is not held keeps fifteen, and Strict 3.1 and Community 3.9
// keep sixty.
func TestModernHeldFollowerWaitsForTheUnitInItsWay(t *testing.T) {
	m := &ModernRules{}
	for _, tc := range []struct {
		name   string
		change func(s *System, follower, blocker pool.Handle)
		want   uint32
	}{
		{"walled within fifteen", func(s *System, _, b pool.Handle) { handleRow(s.Collisions, b).LastStampTick = 94 }, 15},
		{"walled at sixteen", func(s *System, _, b pool.Handle) { handleRow(s.Collisions, b).LastStampTick = 95 }, 16},
		{"walled at twenty-one", func(s *System, _, b pool.Handle) { handleRow(s.Collisions, b).LastStampTick = 100 }, 21},
		{"walled beyond sixty", func(s *System, _, b pool.Handle) { handleRow(s.Collisions, b).LastStampTick = 150 }, 60},
		{"moved since its admission", func(s *System, f, b pool.Handle) {
			handleRow(s.Collisions, b).LastStampTick = 150
			handleRow(s.Collisions, f).LastStampTick = 111
		}, 15},
		{"refused by the ground", func(s *System, f, b pool.Handle) {
			handleRow(s.Collisions, b).LastStampTick = 150
			handleRow(s.Collisions, f).BlockerID = -1
		}, 15},
		{"refused by a building", func(s *System, _, b pool.Handle) {
			handleRow(s.Collisions, b).LastStampTick = 150
			handleRow(s.Collisions, b).Building = true
		}, 15},
		{"past its first waypoint", func(s *System, f, b pool.Handle) {
			handleRow(s.Collisions, b).LastStampTick = 150
			r := handleRow(s.Routes, f)
			r.Points[0] = r.Points[1]
		}, 15},
		{"not refused", func(s *System, f, b pool.Handle) {
			handleRow(s.Collisions, b).LastStampTick = 150
			handleRow(s.Collisions, f).Blocked = false
		}, 15},
	} {
		s, f, b := heldFollowerFixture(t)
		tc.change(s, f, b)
		if got := m.RepathDelay(s, int(f), 110); got != tc.want {
			t.Fatalf("%s: Modern delay %d, want %d", tc.name, got, tc.want)
		}
		for _, rules := range []Rules{StrictRules{}, CommunityRules{}} {
			if got := rules.RepathDelay(s, int(f), 110); got != 60 {
				t.Fatalf("%s: %T delay %d, want 60", tc.name, rules, got)
			}
		}
	}

	s, f, b := heldFollowerFixture(t)
	handleRow(s.Collisions, b).LastStampTick = 100
	s.Rules = m
	route := handleRow(s.Routes, f)
	if s.repathDue(route, int(f), 130) || !s.repathDue(route, int(f), 131) {
		t.Fatal("held follower: want refused at last+20 and admitted at last+21")
	}
	if allocs := testing.AllocsPerRun(200, func() { learnedRulesSink = s.repathDue(route, int(f), 125) }); allocs != 0 {
		t.Fatalf("the held due test allocates %v", allocs)
	}
	// The answer is read from committed state, so a switch needs nothing:
	// Strict applies retail's sixty from the next poll, and Modern resumes.
	s.Rules = StrictRules{}
	if s.repathDue(route, int(f), 169) || !s.repathDue(route, int(f), 170) {
		t.Fatal("Strict after Modern: want refused at last+59 and admitted at last+60")
	}
	s.Rules = m
	if !s.repathDue(route, int(f), 131) {
		t.Fatal("Modern after Strict: the held follower is not due when the unit in its way would be walled")
	}
}
