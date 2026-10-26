package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// arriveFixture is a flat 20x20 world with one-cell units of owner 0 at the
// given cells, each with a bound queue, under a rule set whose traffic names
// pilot.
func arriveFixture(t *testing.T, pilot ArrivePilot, cells [][2]int32) (*System, *units.World, []pool.Handle, *content.UnitDef) {
	t.Helper()
	profile := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 255, MaxWaterSlope: 255}
	sys := NewSystem(syntheticTerrainForIntegrate(), profile, NewOccupancyGrid())
	sys.Rules = &trafficRules{traffic: Traffic{Pilot: pilot}}
	w := newMovementFixtureWorld(4)
	def := setScratchMovement(&content.UnitDef{
		UnitName: "arrive-test", FootprintX: 1, FootprintZ: 1, BMCode: 1, CanMove: true,
		MaxVelocity: 2 << 16, Acceleration: 1 << 15, BrakeRate: 1 << 15, TurnRate: 0x800,
	}, profile)
	var hs []pool.Handle
	for _, c := range cells {
		h, err := w.Create(def, 0, world.CellToWorld(c[0])+8<<16, 0, world.CellToWorld(c[1])+8<<16)
		if err != nil {
			t.Fatal(err)
		}
		hs = append(hs, h)
	}
	sys.BindWorld(w)
	for _, h := range hs {
		arriveBind(sys, w, h)
	}
	return sys, w, hs, def
}

func arriveBind(sys *System, w *units.World, h pool.Handle) {
	u := w.Unit(h)
	u.Flags |= 1 << units.StandingMoveShift
	sys.EnsureUnit(u)
	orders.QueueForUnit(u).SetBinding(orders.NewQueueBinding(orders.QueueBindingConfig{Lookup: w.Unit, World: &orders.WorldQueryAdapter{}}))
}

// order gives each unit a plain move to cell (x, z) created on tick.
func arriveOrder(w *units.World, hs []pool.Handle, x, z int32, tick uint32) []*orders.Node {
	var ns []*orders.Node
	id := orders.Lookup("Move_Ground")
	for _, h := range hs {
		q := orders.QueueOfUnit(w.Unit(h))
		q.Push(id, orders.NewMoveNode(id, world.CellToWorld(x)+8<<16, world.CellToWorld(z)+8<<16, tick, h, false))
		ns = append(ns, q.Head())
	}
	return ns
}

// Units sent together to one point get a place each before any installs its
// goal: the front unit the far row, no two places shared, no two straight
// lines crossing.
func TestArrivePlacesABlock(t *testing.T) {
	// A two-by-two blob heading east for cell (15, 10); the unit at (5, 9)
	// and the one at (5, 10) are the front.
	sys, w, hs, _ := arriveFixture(t, ArrivePilot{Places: true}, [][2]int32{{4, 9}, {5, 9}, {4, 10}, {5, 10}})
	ns := arriveOrder(w, hs, 15, 10, 7)
	sys.BeginTick(7)
	seen := map[[2]int32]bool{}
	var far, near int32 = -1, 1 << 20
	for i, n := range ns {
		c := [2]int32{world.WorldToCell(n.GoalX), world.WorldToCell(n.GoalZ)}
		if seen[c] {
			t.Fatalf("place %v given twice", c)
		}
		seen[c] = true
		if i == 1 || i == 3 { // the front
			far = max(far, c[0])
		} else {
			near = min(near, c[0])
		}
	}
	if len(seen) != 4 {
		t.Fatalf("%d places for 4 units", len(seen))
	}
	for i := range ns {
		if world.WorldToCell(ns[i].GoalX) < 13 || world.WorldToCell(ns[i].GoalX) > 16 {
			t.Fatalf("place %d at x cell %d, not in a block round the point", i, world.WorldToCell(ns[i].GoalX))
		}
	}
	for i := 1; i < 4; i += 2 {
		for j := 0; j < 4; j += 2 {
			if world.WorldToCell(ns[i].GoalX) <= world.WorldToCell(ns[j].GoalX) {
				t.Fatalf("front unit %d placed at x %d, not beyond rear unit %d at x %d", i, world.WorldToCell(ns[i].GoalX), j, world.WorldToCell(ns[j].GoalX))
			}
		}
	}
	for i := range ns {
		ui := w.Unit(hs[i])
		for j := i + 1; j < len(ns); j++ {
			uj := w.Unit(hs[j])
			if segmentsCross(int64(ui.X>>16), int64(ui.Z>>16), int64(ns[i].GoalX>>16), int64(ns[i].GoalZ>>16),
				int64(uj.X>>16), int64(uj.Z>>16), int64(ns[j].GoalX>>16), int64(ns[j].GoalZ>>16)) {
				t.Fatalf("lines of units %d and %d cross", i, j)
			}
		}
	}
	_ = far
	_ = near
}

// Crossing lines may exchange equally sized places only when each unit can
// stand on the other's ground. Land and amphibious units share a footprint,
// but water admitted for an amphibious unit is not a land unit's place.
func TestArriveMixedProfilesKeepPassablePlaces(t *testing.T) {
	for _, tc := range []struct {
		name   string
		rules  Rules
		places bool
	}{
		{"modern", &ModernRules{}, true},
		{"strict", StrictRules{}, false},
		{"community", CommunityRules{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			terrain := syntheticTerrainForIntegrate()
			terrain.SeaLevel = 30
			for z := int32(0); z < terrain.CellH; z++ {
				for x := int32(0); x < terrain.CellW; x++ {
					height := uint8(40)
					if z == 15 && (x == 14 || x == 15) {
						height = 0
					}
					c := terrain.PlotAt(x, z)
					c.SetHeight(height)
					c.SetMinHeight(height)
					c.SetMaxHeight(height)
				}
			}
			land := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 255, MaxWaterSlope: 255}
			amphibious := land
			amphibious.MaxWaterDepth = 10000
			sys := NewSystem(terrain, land, NewOccupancyGrid())
			sys.Rules = tc.rules
			w := newMovementFixtureWorld(4)
			sys.BindWorld(w)
			var hs []pool.Handle
			for i, c := range [][2]int32{{12, 9}, {9, 13}, {5, 2}, {9, 10}} {
				profile := land
				if i >= 2 {
					profile = amphibious
				}
				def := setScratchMovement(&content.UnitDef{UnitName: "mixed-places", CanMove: true}, profile)
				h, err := w.Create(def, 0, world.CellToWorld(c[0])+8<<16, 0, world.CellToWorld(c[1])+8<<16)
				if err != nil {
					t.Fatal(err)
				}
				hs = append(hs, h)
				arriveBind(sys, w, h)
			}
			ns := arriveOrder(w, hs, 15, 14, 7)
			sys.BeginTick(7)
			seen := map[Cell]bool{}
			for i, n := range ns {
				c := Cell{X: world.WorldToCell(n.GoalX), Z: world.WorldToCell(n.GoalZ)}
				if !sys.ProfileFor(hs[i]).IsPassableFootprint(terrain, c.X, c.Z) {
					t.Fatalf("unit %d given impassable place %v", i, c)
				}
				if tc.places && seen[c] {
					t.Fatalf("place %v given twice", c)
				}
				seen[c] = true
				if !tc.places && c != (Cell{X: 15, Z: 14}) {
					t.Fatalf("retail goal changed to %v", c)
				}
			}
		})
	}
}

// A unit sent alone to a point a parked friend stands on is given the
// nearest free footprint instead; without places it keeps the point.
func TestArrivePlacesASingleOffAParkedFriend(t *testing.T) {
	for _, tc := range []struct {
		name  string
		pilot ArrivePilot
		moved bool
	}{
		{"places", ArrivePilot{Places: true}, true},
		{"no places", ArrivePilot{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sys, w, hs, _ := arriveFixture(t, tc.pilot, [][2]int32{{3, 10}, {12, 10}})
			ns := arriveOrder(w, hs[:1], 12, 10, 5)
			sys.BeginTick(5)
			gx, gz := world.WorldToCell(ns[0].GoalX), world.WorldToCell(ns[0].GoalZ)
			moved := gx != 12 || gz != 10
			if moved != tc.moved {
				t.Fatalf("goal cell (%d,%d), moved=%v want %v", gx, gz, moved, tc.moved)
			}
			if moved && max(absInt32(gx-12), absInt32(gz-10)) != 1 {
				t.Fatalf("goal cell (%d,%d) is not next to the parked friend", gx, gz)
			}
		})
	}
}

// A unit near its place that a unit has since parked on takes the nearest
// free footprint, through an ordinary goal install.
func TestArriveExchangesATakenPlace(t *testing.T) {
	sys, w, hs, def := arriveFixture(t, ArrivePilot{Places: true, Exchange: true}, [][2]int32{{8, 10}})
	ns := arriveOrder(w, hs, 12, 10, 5)
	sys.BeginTick(5)
	if world.WorldToCell(ns[0].GoalX) != 12 {
		t.Fatalf("free goal moved to x cell %d", world.WorldToCell(ns[0].GoalX))
	}
	ns[0].Phase = 1
	// A friend parks on the place.
	b, err := w.Create(def, 0, world.CellToWorld(12)+8<<16, 0, world.CellToWorld(10)+8<<16)
	if err != nil {
		t.Fatal(err)
	}
	arriveBind(sys, w, b)
	u := w.Unit(hs[0])
	sys.BeginTick(6)
	v := &Visit{Unit: u, Coll: handleRow(sys.Collisions, hs[0]), Profile: sys.ProfileFor(hs[0]), Head: ns[0], Tick: 6}
	ArrivePilot{Places: true, Exchange: true}.Visit(sys, v)
	gx, gz := world.WorldToCell(ns[0].GoalX), world.WorldToCell(ns[0].GoalZ)
	if gx == 12 && gz == 10 {
		t.Fatal("the unit kept a taken place")
	}
	if max(absInt32(gx-12), absInt32(gz-10)) != 1 {
		t.Fatalf("new place (%d,%d) is not the nearest free footprint", gx, gz)
	}
	if x, z, ok := sys.MoveGoalFor(hs[0], ns[0]); !ok || world.WorldToCell(x) != gx || world.WorldToCell(z) != gz {
		t.Fatalf("installed goal (%d,%d) does not match the order's (%d,%d)", world.WorldToCell(x), world.WorldToCell(z), gx, gz)
	}
}

// arriveBenchFixture is a flat 128x128 world holding n one-cell units of
// owner 0 on a lattice two cells apart, each with a bound queue.
func arriveBenchFixture(b *testing.B, pilot ArrivePilot, n int) (*System, *units.World, []pool.Handle) {
	b.Helper()
	ter := &world.Terrain{CellW: 128, CellH: 128, Plot: make([]world.PlotCell, 128*128)}
	for i := range ter.Plot {
		ter.Plot[i].SetFeature(world.PlotFeatureNone)
		ter.Plot[i].SetHeight(10)
		ter.Plot[i].SetMinHeight(10)
		ter.Plot[i].SetMaxHeight(10)
	}
	profile := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 255, MaxWaterSlope: 255}
	sys := NewSystem(ter, profile, NewOccupancyGrid())
	sys.Rules = &trafficRules{traffic: Traffic{Pilot: pilot}}
	w := units.NewSliced(n+8, nil)
	w.SetCOBSource(movementFixtureCOBFS{}, cob.NewCachedLoader())
	def := setScratchMovement(&content.UnitDef{
		UnitName: "arrive-bench", FootprintX: 1, FootprintZ: 1, BMCode: 1, CanMove: true,
		MaxVelocity: 2 << 16, Acceleration: 1 << 15, BrakeRate: 1 << 15, TurnRate: 0x800,
	}, profile)
	var hs []pool.Handle
	for i := 0; i < n; i++ {
		x, z := int32(2+2*(i%60)), int32(2+2*(i/60))
		h, err := w.Create(def, 0, world.CellToWorld(x)+8<<16, 0, world.CellToWorld(z)+8<<16)
		if err != nil {
			b.Fatal(err)
		}
		hs = append(hs, h)
	}
	sys.BindWorld(w)
	for _, h := range hs {
		u := w.Unit(h)
		u.Flags |= 1 << units.StandingMoveShift
		sys.EnsureUnit(u)
		orders.QueueForUnit(u).SetBinding(orders.NewQueueBinding(orders.QueueBindingConfig{Lookup: w.Unit, World: &orders.WorldQueryAdapter{}}))
	}
	return sys, w, hs
}

// The per-tick scan with 1,500 units under way and no new order.
func BenchmarkArriveScan1500(b *testing.B) {
	sys, w, hs := arriveBenchFixture(b, ArrivePilot{Places: true, Exchange: true}, 1500)
	ns := arriveOrder(w, hs, 100, 100, 1)
	sys.BeginTick(1)
	for _, n := range ns {
		n.Phase = 1
	}
	pilot := ArrivePilot{Places: true, Exchange: true}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pilot.BeginTick(sys, uint32(10+i))
	}
}

// Placing a block of 64 units sent to one point.
func BenchmarkArrivePlaceBlock64(b *testing.B) {
	sys, w, hs := arriveBenchFixture(b, ArrivePilot{Places: true}, 64)
	pilot := ArrivePilot{Places: true}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tick := uint32(10 + 2*i)
		for _, h := range hs {
			orders.QueueOfUnit(w.Unit(h)).PurgeUnprotected()
		}
		arriveOrder(w, hs, 100, 60, tick)
		pilot.BeginTick(sys, tick)
	}
}

// The per-visit exchange check of 1,500 movers near their places.
func BenchmarkArriveSteer1500(b *testing.B) {
	pilot := ArrivePilot{Places: true, Exchange: true}
	sys, w, hs := arriveBenchFixture(b, pilot, 1500)
	var ns []*orders.Node
	for _, h := range hs {
		u := w.Unit(h)
		ns = append(ns, arriveOrder(w, []pool.Handle{h}, world.WorldToCell(u.X)+3, world.WorldToCell(u.Z), 1)...)
	}
	sys.BeginTick(1)
	visits := make([]Visit, len(hs))
	for i, h := range hs {
		ns[i].Phase = 1
		visits[i] = Visit{Unit: w.Unit(h), Coll: handleRow(sys.Collisions, h), Profile: sys.ProfileFor(h), Head: ns[i]}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := range visits {
			visits[j].Tick = uint32(20 + i)
			pilot.Visit(sys, &visits[j])
		}
	}
}

// A unit near a place that is free, which has come no nearer for Stuck ticks
// and whose last step a unit at rest refused, takes the footprint it stands
// on for its place. A friend on the move that refused the step does not make
// it settle, and neither does the wait before it has run out.
func TestArriveSettlesAtTheEdge(t *testing.T) {
	pilot := ArrivePilot{Places: true, Exchange: true, Stuck: 20, StuckParked: true}
	for _, tc := range []struct {
		name    string
		moving  bool
		tick    uint32
		settles bool
	}{
		{"held by a unit at rest", false, 26, true},
		{"before the wait has run out", false, 25, false},
		{"held by a friend on the move", true, 26, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sys, w, hs, _ := arriveFixture(t, pilot, [][2]int32{{8, 10}, {9, 10}})
			mover, friend := hs[0], hs[1]
			ns := arriveOrder(w, hs[:1], 12, 10, 5)
			sys.BeginTick(5)
			ns[0].Phase = 1
			coll := handleRow(sys.Collisions, mover)
			coll.Blocked, coll.BlockerID = true, int(friend)
			if tc.moving {
				handleRow(sys.Collisions, friend).Speed = 1 << 16
			}
			route := handleRow(sys.Routes, mover)
			route.PublishAtRevision([]Point{{X: 8*16 + 8, Z: 10*16 + 8}, {X: 12*16 + 8, Z: 10*16 + 8}}, sys.staticObstacleRevision())
			visit := func(tick uint32) {
				sys.BeginTick(tick)
				v := &Visit{Unit: w.Unit(mover), Coll: coll, Profile: sys.ProfileFor(mover), Route: route, Head: ns[0], Tick: tick}
				pilot.Visit(sys, v)
			}
			visit(6)
			if world.WorldToCell(ns[0].GoalX) != 12 {
				t.Fatalf("the unit settled on its first visit, at x cell %d", world.WorldToCell(ns[0].GoalX))
			}
			visit(tc.tick)
			gx, gz := world.WorldToCell(ns[0].GoalX), world.WorldToCell(ns[0].GoalZ)
			if settled := gx == 8 && gz == 10; settled != tc.settles {
				t.Fatalf("place (%d,%d): settled=%v, want %v", gx, gz, settled, tc.settles)
			}
			if !tc.settles && gx != 12 {
				t.Fatalf("the place moved to (%d,%d)", gx, gz)
			}
		})
	}
}

// A unit that has stood for Sealed ticks far from a place the ground closes
// off settles where it stands; one that has a way, however long, does not,
// and neither does any unit without the setting.
func TestArriveSettlesShortOfASealedGoal(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pilot   ArrivePilot
		gap     bool
		settles bool
	}{
		{"the ground closes the goal off", ArrivePilot{Places: true, Exchange: true, Sealed: 150}, false, true},
		{"a gap in the wall", ArrivePilot{Places: true, Exchange: true, Sealed: 150}, true, false},
		{"no setting", ArrivePilot{Places: true, Exchange: true}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sys, w, hs, _ := arriveFixture(t, tc.pilot, [][2]int32{{2, 10}})
			// A wall from edge to edge between the unit and its goal.
			for z := int32(0); z < sys.Terrain.CellH; z++ {
				if tc.gap && z == 3 {
					continue
				}
				sys.Terrain.PlotAt(10, z).SetFeature(world.PlotFeatureVoid)
			}
			ns := arriveOrder(w, hs, 16, 10, 5)
			sys.BeginTick(5)
			ns[0].Phase = 1
			u := w.Unit(hs[0])
			settled := uint32(0)
			for tick := uint32(6); tick <= 400 && settled == 0; tick++ {
				v := &Visit{Unit: u, Coll: handleRow(sys.Collisions, hs[0]), Profile: sys.ProfileFor(hs[0]), Head: ns[0], Tick: tick}
				tc.pilot.Visit(sys, v)
				if world.WorldToCell(ns[0].GoalX) == 2 && world.WorldToCell(ns[0].GoalZ) == 10 {
					settled = tick
				}
			}
			if (settled != 0) != tc.settles {
				t.Fatalf("settled at tick %d, want settles=%v", settled, tc.settles)
			}
			if !tc.settles {
				return
			}
			if settled < 6+150 {
				t.Fatalf("settled at tick %d, before it had stood %d ticks", settled, 150)
			}
			if x, z, ok := sys.MoveGoalFor(hs[0], ns[0]); !ok || world.WorldToCell(x) != 2 || world.WorldToCell(z) != 10 {
				t.Fatalf("installed goal (%d,%d) is not where the unit stands", world.WorldToCell(x), world.WorldToCell(z))
			}
		})
	}
}

// A unit that moves on does not settle: the clock starts again whenever it
// leaves the ground it stood on.
func TestArriveSealedWaitsForAUnitThatMoves(t *testing.T) {
	pilot := ArrivePilot{Places: true, Exchange: true, Sealed: 150}
	sys, w, hs, _ := arriveFixture(t, pilot, [][2]int32{{2, 10}})
	for z := int32(0); z < sys.Terrain.CellH; z++ {
		sys.Terrain.PlotAt(12, z).SetFeature(world.PlotFeatureVoid)
	}
	ns := arriveOrder(w, hs, 16, 10, 5)
	sys.BeginTick(5)
	ns[0].Phase = 1
	u := w.Unit(hs[0])
	for tick := uint32(6); tick <= 240; tick++ {
		// A cell every thirty ticks: slower than any unit, and moving.
		u.X = world.CellToWorld(2) + 8<<16 + numeric.Fixed(int64(tick)*16<<16/30)
		v := &Visit{Unit: u, Coll: handleRow(sys.Collisions, hs[0]), Profile: sys.ProfileFor(hs[0]), Head: ns[0], Tick: tick}
		pilot.Visit(sys, v)
	}
	if world.WorldToCell(ns[0].GoalX) != 16 {
		t.Fatalf("a unit on the move settled: goal x cell %d", world.WorldToCell(ns[0].GoalX))
	}
}

// A move that waited behind another order is the pilot's as soon as it
// heads the queue: its goal is its place, and what the pilot does for a unit
// near or far from its place it does for this one.
func TestArriveAdoptsAMoveThatWaited(t *testing.T) {
	pilot := ArrivePilot{Places: true, Exchange: true, Sealed: 150}
	sys, w, hs, _ := arriveFixture(t, pilot, [][2]int32{{2, 10}})
	first := arriveOrder(w, hs, 4, 10, 5)[0]
	q := orders.QueueOfUnit(w.Unit(hs[0]))
	id := orders.Lookup("Move_Ground")
	q.Push(id, orders.NewMoveNode(id, world.CellToWorld(16)+8<<16, world.CellToWorld(10)+8<<16, 5, hs[0], false))
	sys.BeginTick(5)
	st := pilot.state(sys)
	if st.row(hs[0]).node != first {
		t.Fatal("the first move was not given a place")
	}
	// The first move ends a hundred ticks later; the second heads the queue.
	q.RemoveHead()
	second := q.Head()
	if second == nil || second == first {
		t.Fatal("the fixture's queue did not advance")
	}
	sys.BeginTick(105)
	row := st.row(hs[0])
	if row.node != second {
		t.Fatal("the move that waited was not adopted")
	}
	if row.place != (Cell{X: 16, Z: 10}) || world.WorldToCell(second.GoalX) != 16 {
		t.Fatalf("its place is %v and its goal cell %d, not the goal it was given", row.place, world.WorldToCell(second.GoalX))
	}
}

// The arrival root is the floor square root: the Newton descent from the
// value itself, which it replaced, over small values, values about every
// square and a spread of large ones.
func TestArriveRootIsTheFloorRoot(t *testing.T) {
	fromValue := func(v int64) int64 {
		if v <= 0 {
			return 0
		}
		x := v
		y := (x + 1) / 2
		for y < x {
			x = y
			y = (x + v/x) / 2
		}
		return x
	}
	check := func(v int64) {
		if got, want := isqrtInt64Arrive(v), fromValue(v); got != want {
			t.Fatalf("root of %d = %d, want %d", v, got, want)
		}
	}
	for v := int64(-3); v < 1<<16; v++ {
		check(v)
	}
	for r := int64(1); r < 1<<31; r = r*3/2 + 1 {
		check(r*r - 1)
		check(r * r)
		check(r*r + 1)
	}
	seed := uint64(7)
	for i := 0; i < 100000; i++ {
		seed = seed*6364136223846793005 + 1442695040888963407
		check(int64(seed >> (1 + seed%40)))
	}
}
