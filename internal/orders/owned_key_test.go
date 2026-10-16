package orders

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestKeyedRegistrationMatchesReinstalling locks the registration shortcuts:
// skipping a queue whose registration is still confirmed (OwnedRowsCurrent),
// and skipping a row that already holds its keyed installation
// (RegisterOwnedHandler), leave every queue exactly as reinstalling every row
// on every visit did. Two queues receive the same long sequence of
// registrations by three registrants — two of them contending for one row —
// mixed with every other kind of write: a foreign handler, a cleared row, the
// GetBuilt adapter, an externally driven claim, a proofed foreign
// installation, a lone keyed row and a queue replacement. One takes the
// shortcuts, the other installs through the plain setters they replace. After
// every step each row must dispatch to the same handler and carry the same
// proof on both queues.
func TestKeyedRegistrationMatchesReinstalling(t *testing.T) {
	handler := func(code Code) OwnedHandler {
		return func(*units.Unit, *Node, uint32, uint32) (Code, bool) { return code, true }
	}
	owner := &struct{ byte }{}
	source := NewCheckpointHandlerSource(owner, checkpoint.NewBindingAuthority())
	plain := handler(1)
	proofed := NewCheckpointOwnedHandler(handler(2), CheckpointConstructionWake, source)
	foreign := handler(3)
	foreignProof := NewCheckpointOwnedHandler(handler(4), CheckpointAirStandby, source)
	var rows []ID
	for _, name := range []string{"MobileBuild", "BuildingBuild", "GetBuilt", "VTOL_Standby"} {
		id := Lookup(name)
		if id == 0 {
			t.Fatalf("%s is not in the descriptor table", name)
		}
		rows = append(rows, id)
	}
	keyed, reinstalled := &Queue{}, &Queue{}
	ku, ru := &units.Unit{Alive: true}, &units.Unit{Alive: true}
	BindQueue(ku, keyed)
	BindQueue(ru, reinstalled)

	same := func(step int, what string) {
		t.Helper()
		for id := ID(1); int(id) < len(table); id++ {
			a, b := keyed.OwnedHandlerFor(id), reinstalled.OwnedHandlerFor(id)
			if (a == nil) != (b == nil) {
				t.Fatalf("step %d (%s): row %d presence differs", step, what, id)
			}
			if a != nil {
				ac, aok := a(nil, nil, 0, 0)
				bc, bok := b(nil, nil, 0, 0)
				if ac != bc || aok != bok {
					t.Fatalf("step %d (%s): row %d dispatches (%d,%v), reinstalling (%d,%v)", step, what, id, ac, aok, bc, bok)
				}
			}
			var ap, bp CheckpointOwnedHandler
			if int(id) < len(keyed.checkpointOwnedHandlers) {
				ap = keyed.checkpointOwnedHandlers[id]
			}
			if int(id) < len(reinstalled.checkpointOwnedHandlers) {
				bp = reinstalled.checkpointOwnedHandlers[id]
			}
			if ap.kind != bp.kind || ap.source != bp.source || (ap.handler == nil) != (bp.handler == nil) {
				t.Fatalf("step %d (%s): row %d proof differs", step, what, id)
			}
		}
		if (keyed.ownedHandlers == nil) != (reinstalled.ownedHandlers == nil) ||
			(keyed.checkpointOwnedHandlers == nil) != (reinstalled.checkpointOwnedHandlers == nil) {
			t.Fatalf("step %d (%s): handler storage presence differs", step, what)
		}
	}
	seed := uint32(99)
	next := func(n uint32) uint32 {
		seed = seed*1664525 + 1013904223
		return (seed >> 8) % n
	}
	// Three registrants, each installing one value over a fixed row set the
	// way the construction and movement services do: a whole-queue check,
	// then the keyed per-row walk, then the confirmation. The third contends
	// with the first for MobileBuild, as two services fighting over a row.
	type registrant struct {
		rows  []ID
		key   OwnedKey
		apply func(q *Queue, id ID, keyed bool)
	}
	registrants := []registrant{
		{rows[:2], OwnedKey{Handler: &plain}, func(q *Queue, id ID, keyed bool) {
			if keyed {
				q.RegisterOwnedHandler(id, &plain, plain)
			} else {
				q.SetOwnedHandler(id, plain)
			}
		}},
		{rows[2:], OwnedKey{Proof: &proofed}, func(q *Queue, id ID, keyed bool) {
			if keyed {
				q.RegisterOwnedHandlerWithCheckpointBinding(id, &proofed, proofed)
			} else {
				q.SetOwnedHandlerWithCheckpointBinding(id, proofed)
			}
		}},
		{rows[:1], OwnedKey{Proof: &foreignProof}, func(q *Queue, id ID, keyed bool) {
			if keyed {
				q.RegisterOwnedHandlerWithCheckpointBinding(id, &foreignProof, foreignProof)
			} else {
				q.SetOwnedHandlerWithCheckpointBinding(id, foreignProof)
			}
		}},
	}
	skipped, walked := 0, 0
	for step := 0; step < 6000; step++ {
		id := rows[next(uint32(len(rows)))]
		var what string
		switch op := next(24); {
		case op < 16:
			r := registrants[0]
			switch {
			case op >= 15:
				r = registrants[2]
			case op >= 8:
				r = registrants[1]
			}
			what = "registrant"
			if keyed.OwnedRowsCurrent(r.key) {
				skipped++
			} else {
				walked++
				for _, row := range r.rows {
					r.apply(keyed, row, true)
				}
				keyed.MarkOwnedRows(r.key)
			}
			for _, row := range r.rows {
				r.apply(reinstalled, row, false)
			}
		case op == 16 || op == 17:
			what = "foreign"
			keyed.SetOwnedHandler(id, foreign)
			reinstalled.SetOwnedHandler(id, foreign)
		case op == 18:
			what = "cleared"
			keyed.SetOwnedHandler(id, nil)
			reinstalled.SetOwnedHandler(id, nil)
		case op == 19:
			what = "GetBuilt adapter"
			adapt := func(*units.Unit, *Node, uint32, uint32) Code { return 5 }
			keyed.SetGetBuiltHandler(adapt)
			reinstalled.SetGetBuiltHandler(adapt)
		case op == 20:
			what = "externally driven"
			keyed.SetExternallyDrivenHandler(id)
			reinstalled.SetExternallyDrivenHandler(id)
		case op == 21:
			what = "foreign proof"
			keyed.SetOwnedHandlerWithCheckpointBinding(id, foreignProof)
			reinstalled.SetOwnedHandlerWithCheckpointBinding(id, foreignProof)
		case op == 22:
			what = "keyed row alone"
			keyed.RegisterOwnedHandler(id, &plain, plain)
			reinstalled.SetOwnedHandler(id, plain)
		default:
			what = "replacement"
			keyed = &Queue{}
			reinstalled = &Queue{}
			BindQueue(ku, keyed)
			BindQueue(ru, reinstalled)
		}
		same(step, fmt.Sprintf("%s row %d", what, id))
	}
	if skipped < 1000 || walked < 500 {
		t.Fatalf("%d whole-queue skips and %d walks; one of the paths went unexercised", skipped, walked)
	}
}
