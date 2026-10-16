package session

import (
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// TestParallelUnitChannelMatchesSerialPublication locks the unit channel's
// worker build to the inline one (INVARIANTS I1 "Parallel simulation
// exception"). The same committed state is published on the calling goroutine
// alone and then with workers, and every exported field of the two frames —
// not only the unit channel — must agree.
func TestParallelUnitChannelMatchesSerialPublication(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "parallel-fixture"}, MaxDamage: 100}
	const count = publishParallelMinUnits + 3*publishChunkUnits + 5 // a ragged last chunk
	w := newSessionFixtureWorld(count+16, nil)
	handles := make([]pool.Handle, 0, count)
	for i := 0; i < count; i++ {
		h, err := w.Create(def, uint8(i%2), numeric.Fixed(int32(i%32)<<16), 0, numeric.Fixed(int32(i/32)<<16))
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		handles = append(handles, h)
	}
	// Queues in several chunks, including two in one chunk and one in the
	// ragged tail, so the per-chunk queue offsets are exercised.
	for _, i := range []int{0, 1, publishChunkUnits + 3, 2*publishChunkUnits - 1, count - 1} {
		q := orders.QueueForUnit(w.Unit(handles[i]))
		q.Push(orders.Lookup("Move_Ground"), orders.Node{Owner: handles[i], GoalX: numeric.Fixed(int32(i) << 16)})
	}

	s := &Session{Snapshot: frame.NewBuffer(), Units: w, LocalOwner: 0}
	s.publishWorkerLimit = -1
	s.publishSnapshot(1)
	serial := flattenFrame(s.Snapshot.Current())
	if got := len(s.Snapshot.Current().OrderQueues); got != 5 {
		t.Fatalf("serial publication carries %d order queues, want 5", got)
	}

	s.publishWorkerLimit = 0
	s.publishPausedSnapshot(1)
	parallel := flattenFrame(s.Snapshot.Current())
	compareFlatFrames(t, serial, parallel)
}

// flattenFrame lists every exported value of f as "path=value" lines, slices
// element by element, so two frames compare field by field and a difference
// names its field.
func flattenFrame(f *frame.Frame) []string {
	var out []string
	flattenValue(&out, "frame", reflect.ValueOf(f).Elem())
	return out
}

func flattenValue(out *[]string, path string, v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			*out = append(*out, path+"=nil")
			return
		}
		flattenValue(out, path, v.Elem())
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			if !t.Field(i).IsExported() {
				continue
			}
			flattenValue(out, path+"."+t.Field(i).Name, v.Field(i))
		}
	case reflect.Slice, reflect.Array:
		*out = append(*out, fmt.Sprintf("%s.len=%d", path, v.Len()))
		for i := 0; i < v.Len(); i++ {
			flattenValue(out, fmt.Sprintf("%s[%d]", path, i), v.Index(i))
		}
	case reflect.Map:
		keys := make([]string, 0, v.Len())
		for _, k := range v.MapKeys() {
			keys = append(keys, fmt.Sprint(k.Interface()))
		}
		sort.Strings(keys)
		*out = append(*out, fmt.Sprintf("%s.keys=%v", path, keys))
	case reflect.Func, reflect.Chan:
		*out = append(*out, fmt.Sprintf("%s.nil=%v", path, v.IsNil()))
	default:
		*out = append(*out, fmt.Sprintf("%s=%v", path, v.Interface()))
	}
}

func compareFlatFrames(t *testing.T, serial, parallel []string) {
	t.Helper()
	for i := 0; i < len(serial) && i < len(parallel); i++ {
		if serial[i] != parallel[i] {
			t.Fatalf("frames differ at line %d:\nserial   %s\nparallel %s", i, serial[i], parallel[i])
		}
	}
	if len(serial) != len(parallel) {
		t.Fatalf("frames differ in length: serial %d lines, parallel %d", len(serial), len(parallel))
	}
}

// appendOrderQueueView appends src's queue view to dst through the unit
// channel's own writer, for tests that build one view outside a publication.
func appendOrderQueueView(dst []frame.OrderQueueView, src orders.SnapshotQueue, cat *content.Catalog) []frame.OrderQueueView {
	dst = reserveOrderQueueView(dst)
	setOrderQueueView(dst, len(dst)-1, src, cat)
	return dst
}

// unitChannelLive is a test probe of how many live units the unit channel
// last published, so a test can show its scene took the worker path.
func unitChannelLive(s *Session) int { return len(s.unitChannel.live) }
