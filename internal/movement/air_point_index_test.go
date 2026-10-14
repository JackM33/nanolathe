package movement

import (
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func TestAirPointIndexExact(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	threats := []airThreat{{x: 0, z: 0, r: 256, w: 13}, {x: -256, z: 256, r: 0, w: 7}, {x: 0, z: 0, r: 256, w: -13}, {x: 512, z: 0, r: 100000, w: 31}, {x: 1 << 60, z: 1 << 60, r: 1 << 60, w: 11}, {x: 2, z: 3, r: -5, w: 17}}
	for i := 0; i < 1000; i++ {
		threats = append(threats, airThreat{x: r.Int63n(12000) - 6000, z: r.Int63n(12000) - 6000, r: r.Int63n(800), w: r.Int63n(100)})
	}
	var idx airPointIndex
	check := func(ts []airThreat) {
		t.Helper()
		idx.rebuild(ts)
		points := [][2]int64{{0, 0}, {256, 0}, {-256, 0}, {-256, 256}, {257, 0}, {-257, 0}, {0, -256}, {1 << 60, 0}, {airPointSafe, airPointSafe}, {-airPointSafe, -airPointSafe}}
		for i := 0; i < 10000; i++ {
			points = append(points, [2]int64{r.Int63n(16000) - 8000, r.Int63n(16000) - 8000})
		}
		for _, p := range points {
			if got, want := idx.rate(p[0], p[1], ts), airPointRate(p[0], p[1], ts); got != want {
				t.Fatalf("point %v got %d want %d", p, got, want)
			}
		}
		for j := 0; j < 16; j++ {
			b := uint16(j * 4096)
			if got, want := airCoverExitIndexed(320, 256, b, ts, 2400, &idx), airCoverExit(320, 256, b, ts, 2400); got != want {
				t.Fatalf("bearing %d got %d want %d", j, got, want)
			}
		}
	}
	check(nil)
	check(threats[:3])
	check(threats)
	// A same-list/version refresh can change radius or weight; rebuild independently.
	threats[0].r = 2048
	threats[0].w = 999
	check(threats)
	check(threats[:1])
	check(nil)
}
func BenchmarkAirPointIndexRebuild(b *testing.B) {
	r := rand.New(rand.NewSource(7))
	ts := make([]airThreat, 1000)
	for i := range ts {
		ts[i] = airThreat{x: r.Int63n(12000), z: r.Int63n(12000), r: 400, w: 10}
	}
	var idx airPointIndex
	idx.rebuild(ts)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx.rebuild(ts)
	}
}

// Keep enough inert contacts to exercise the index, not its sparse fallback.
func paddedAirThreats(ts ...airThreat) []airThreat {
	for len(ts) < airPointMinThreats {
		ts = append(ts, airThreat{x: 100000 + int64(len(ts))*512, r: 1})
	}
	return ts
}

func TestAirPointIndexRefreshWithoutVersionChange(t *testing.T) {
	u := &units.Unit{Alive: true}
	ts := paddedAirThreats(airThreat{r: 10, w: 1})
	for i := range ts {
		ts[i].unit = &units.Unit{Alive: true}
	}
	s := &System{airThreats: []airThreatPicture{{ok: true, version: 7, list: ts}}}
	pic := &s.airThreats[0]
	pic.pointIndex.rebuild(pic.list)
	if !pic.pointIndex.ready || pic.pointIndex.rate(500, 0, pic.list) != 0 {
		t.Fatal("fixture did not exercise indexed lookup")
	}
	pic.list[0].r, pic.list[0].w = 512, 19
	got := s.airThreatsFor(u, airThreatRefresh)
	if got.version != 7 || !got.pointIndex.ready || got.pointIndex.rate(500, 0, got.list) != 19 {
		t.Fatal("refresh must rebuild without changing plan version")
	}
	// Normal expiry removes remembered contacts; a reused picture starts empty.
	got = s.airThreatsFor(u, airThreatMemory)
	if len(got.list) != 0 || got.pointIndex.ready || len(got.pointIndex.entries) != 0 {
		t.Fatal("expired contacts survived refresh")
	}
}

func TestAirPointIndexExceptionalArithmetic(t *testing.T) {
	coords := []int64{math.MinInt64, math.MaxInt64, -airPointSafe - 1, -airPointSafe, -257, -256, -1, 0, 1, 255, 256, 257, airPointSafe, airPointSafe + 1}
	radii := []int64{math.MinInt64, math.MaxInt64, -1, 0, 1, 256, 768, 1024, airPointSafe, airPointSafe + 1}
	var idx airPointIndex
	for _, x := range coords {
		for _, radius := range radii {
			ts := paddedAirThreats(airThreat{x: x, z: -x, r: radius, w: math.MaxInt64}, airThreat{x: x, z: -x, r: radius, w: 23})
			idx.rebuild(ts)
			for _, qx := range coords {
				for _, qz := range coords {
					if got, want := idx.rate(qx, qz, ts), airPointRate(qx, qz, ts); got != want {
						t.Fatalf("circle=(%d,%d,%d) point=(%d,%d): got %d want %d", x, -x, radius, qx, qz, got, want)
					}
				}
			}
		}
	}
}

func TestAirPointIndexLifecycle(t *testing.T) {
	var idx airPointIndex
	ts := paddedAirThreats(airThreat{r: 256, w: 9}, airThreat{r: 100000, w: -9})
	for _, picture := range [][]airThreat{nil, ts, ts[:1], ts, nil, ts} {
		idx.rebuild(picture)
		for _, x := range []int64{-257, -256, 0, 256, 257, 100000} {
			if got, want := idx.rate(x, 0, picture), airPointRate(x, 0, picture); got != want {
				t.Fatalf("transition len=%d x=%d: got %d want %d", len(picture), x, got, want)
			}
		}
		for _, bucket := range idx.buckets {
			ids := idx.entries[bucket.start:bucket.end]
			for i := 1; i < len(ids); i++ {
				if ids[i-1] >= ids[i] {
					t.Fatal("bucket lost source order")
				}
			}
		}
	}
	var copyIndex airPointIndex
	copyIndex.rebuild(ts)
	if !reflect.DeepEqual(idx, copyIndex) {
		t.Fatal("rebuild depends on map hashing")
	}
	for i := range ts {
		ts[i].r = math.MaxInt64
	}
	idx.rebuild(ts)
	if idx.ready {
		t.Fatal("all-global picture should scan directly")
	}
	ts[0] = airThreat{x: 256, r: 1, w: 31}
	idx.rebuild(ts)
	if !idx.ready {
		t.Fatal("index failed to reactivate")
	}
	s := &System{airThreats: []airThreatPicture{{list: ts, pointIndex: idx}, {}}}
	if s.airIndexFor(&units.Unit{}, ts) == nil || s.airIndexFor(&units.Unit{Owner: 1}, ts) != nil || s.airIndexFor(&units.Unit{Owner: 2}, ts) != nil || s.airIndexFor(nil, ts) != nil || s.airIndexFor(&units.Unit{}, append([]airThreat(nil), ts...)) != nil || s.airIndexFor(&units.Unit{}, ts[:1]) != nil {
		t.Fatal("index must belong to the exact owner picture")
	}
}

// Check all private future-affecting planner state, including cached cover exits
// and tie/hysteresis decisions. Session checkpoints intentionally omit pilots.
func TestAirPointIndexPlannerOracle(t *testing.T) {
	s, _, u := wideAirFixture(t)
	r := rand.New(rand.NewSource(41))
	var previous airPilot
	for trial := 0; trial < 60; trial++ {
		ts := make([]airThreat, 64)
		for i := range ts {
			ts[i] = airThreat{x: r.Int63n(4096), z: r.Int63n(1024), r: r.Int63n(600), w: r.Int63n(40)}
		}
		if trial%3 == 0 { // symmetric costs, zero-weight cover and cancellation
			for i := range ts {
				ts[i] = airThreat{x: 1024, z: 512, r: 300, w: int64(i%2)*2 - 1}
			}
		}
		s.airThreats = []airThreatPicture{{version: uint32(trial), list: ts}}
		pic := &s.airThreats[0]
		pic.pointIndex.rebuild(ts)
		initial := airPilot{unit: u, picture: pic.version}
		if trial%2 != 0 {
			initial = previous
			initial.picture = pic.version
		}
		indexed, linear := initial, initial
		s.airPlanRun(u, &indexed, 1024, 512, 180, 400, ts, uint32(100+trial*40))
		flyOut := s.airFlyOut(u, 1024, 512, 2000, ts)
		pic.pointIndex.ready = false
		s.airPlanRun(u, &linear, 1024, 512, 180, 400, ts, uint32(100+trial*40))
		if indexed != linear || flyOut != s.airFlyOut(u, 1024, 512, 2000, ts) {
			t.Fatalf("trial %d planner mismatch: indexed=%+v linear=%+v", trial, indexed, linear)
		}
		previous = indexed
	}
}

var airPointBenchmarkSink int64

func BenchmarkAirPointQuery(b *testing.B) {
	for _, n := range []int{4, 16, 32, 64, 256, 1000} {
		for _, dense := range []bool{false, true} {
			ts := make([]airThreat, n)
			for i := range ts {
				x := int64(i * 512)
				if dense {
					x = 0
				}
				ts[i] = airThreat{x: x, r: 400, w: 10}
			}
			var idx airPointIndex
			idx.rebuild(ts)
			for _, indexed := range []bool{false, true} {
				b.Run(fmt.Sprintf("n%d/dense%t/index%t", n, dense, indexed), func(b *testing.B) {
					b.ReportAllocs()
					var sum int64
					for i := 0; i < b.N; i++ {
						x := int64(i % 128)
						if indexed {
							sum += idx.rate(x, 0, ts)
						} else {
							sum += airPointRate(x, 0, ts)
						}
					}
					airPointBenchmarkSink = sum
				})
			}
		}
	}
}
