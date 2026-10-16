package orders

import (
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The per-queue handler registration seam.
//
// Retail compiles a handler into every static descriptor, so this file
// describes Nanolathe's build, not retail [04 §3.1]. Some rows' bodies belong
// to a package internal/orders cannot import — the factory and mobile-build
// lifecycle and the unit-reclaim machine in internal/construction, the air
// executors in internal/movement — and the dependency runs the other way. The
// owning subsystem therefore registers the row's handler on the queue, and the
// pump dispatches that registration exactly as it dispatches a descriptor
// handler: it applies the ordinary gate test, clears the record's dynamic gate,
// calls the handler, and applies the result code [04 §3.3].
//
// The pump stays the sole dispatcher, which is the property the registration
// exists to preserve. It supersedes a `Driver` enum on the descriptor whose
// `DriverExternalMachine` value made the pump special-case six rows by table
// lookup; the routing fact is now the owner's own statement, made on the queue
// it owns, and the pump reads one seam instead of two.

// OwnedHandler is the shape a subsystem registers for an order row whose body
// that subsystem, not this package's descriptor table, owns. Its first three
// arguments are the descriptor Handler's, and the satisfied set is an argument
// for the same reason: `GetBuilt`'s phase-2 body reads it to choose its arm —
// the `0x8000` arm holds, the bit-0 arm decays [04 R-ORD-01 §11].
//
// The boolean reports whether the registration advanced the record on this
// visit:
//
//   - `(code, true)` — it ran the row's body, and the pump applies `code`
//     through the ordinary result-code epilogue [04 §3.3].
//   - `(0, false)` — the registering subsystem advances this record from its
//     own per-unit step instead, reading and writing the record's phase,
//     dynamic gate and deadline as its state machine. The pump then writes
//     none of those fields and ends the pass.
//
// The second form is not a courtesy. A result code applied to such a record
// overwrites its owner's own deadline, and a build record parked for 30 to 44
// ticks mid-build is precisely the "the plant will not build another" stall of
// PLAN 17 §0 row 3 (internal/construction gates its own step on
// `node.Deadline >= 0 && tick < node.Deadline`). Such a record is also not
// diagnosed: an owned record is not a missing handler.
type OwnedHandler func(u *units.Unit, n *Node, satisfied uint32, tick uint32) (Code, bool)

// SetOwnedHandler registers handler as the owner of order row id on this queue,
// replacing any previous registration. A zero or out-of-range id is the reject
// sentinel and registers nothing; a nil handler clears the row.
//
// Registration is per queue and carries no process-wide dispatch state
// [P0-I16].
// An owning subsystem installs it wherever it binds a queue, which is the same
// place it installs the session binding.
func (q *Queue) SetOwnedHandler(id ID, handler OwnedHandler) {
	if q == nil || int(id) <= 0 || int(id) >= len(table) {
		return
	}
	// Reinstalling even the same function transfers no provenance (§16.3.62).
	if int(id) < len(q.checkpointOwnedHandlers) {
		q.checkpointOwnedHandlers[id] = CheckpointOwnedHandler{}
	}
	// Whatever installed the row before, this write is not a keyed
	// registration's (RegisterOwnedHandler sets the key after calling here),
	// and it ends every registration's confirmation (OwnedRowsCurrent).
	if int(id) < len(q.ownedKeys) {
		q.ownedKeys[id] = OwnedKey{}
	}
	q.ownedEpoch++
	if q.ownedHandlers == nil {
		if handler == nil {
			return
		}
		q.ownedHandlers = make([]OwnedHandler, len(table))
	}
	q.ownedHandlers[id] = handler
}

// OwnedKey names one installation by the registrant field it installs from:
// Handler for an ordinary installation, Proof for one carrying its checkpoint
// proof, never both. A registrant names an installation only by a field that
// holds its value for the registrant's lifetime once filled, so one key always
// means one handler (and proof) value.
type OwnedKey struct {
	Handler *OwnedHandler
	Proof   *CheckpointOwnedHandler
}

// RegisterOwnedHandler is SetOwnedHandler for a registrant that installs the
// same row on every visit — the construction and movement services, from each
// unit's step. key is the field handler was read from (OwnedKey). When the row
// was last installed by this call with the same key, and nothing has written
// it since, it already holds exactly what this call would write, so the call
// changes nothing. Otherwise it installs through SetOwnedHandler and records
// the key.
//
// The key is per row, so a different installation on one row — a test's
// handler, SetGetBuiltHandler, a row cleared to nil — sends only that row
// back through the setter on the next visit, and a queue built by
// replacement carries no keys at all. The keys are derived bookkeeping: no
// dispatch, checkpoint or comparison reads them.
func (q *Queue) RegisterOwnedHandler(id ID, key *OwnedHandler, handler OwnedHandler) {
	k := OwnedKey{Handler: key}
	if q.ownedKeyCurrent(id, k) {
		return
	}
	q.SetOwnedHandler(id, handler)
	q.setOwnedKey(id, k, handler != nil)
}

// RegisterOwnedHandlerWithCheckpointBinding is RegisterOwnedHandler for an
// installation carrying its checkpoint proof: key is the field the proof was
// read from, and the proof is installed through
// SetOwnedHandlerWithCheckpointBinding.
func (q *Queue) RegisterOwnedHandlerWithCheckpointBinding(id ID, key *CheckpointOwnedHandler, handler CheckpointOwnedHandler) {
	k := OwnedKey{Proof: key}
	if q.ownedKeyCurrent(id, k) {
		return
	}
	q.SetOwnedHandlerWithCheckpointBinding(id, handler)
	q.setOwnedKey(id, k, handler.handler != nil)
}

// OwnedRowsCurrent reports whether the registration named by key confirmed
// every one of its rows on this queue (MarkOwnedRows) and no owned row has
// been written since. Then each of those rows still holds exactly key's
// installation, so a registrant may skip its per-row walk: this is the check
// a unit's step makes on the queue it holds, and it reads the queue's own
// words rather than one row slot per registered row.
func (q *Queue) OwnedRowsCurrent(key OwnedKey) bool {
	if q == nil || key == (OwnedKey{}) {
		return false
	}
	for i := range q.ownedStamps {
		if q.ownedStamps[i].key == key {
			return q.ownedStamps[i].epoch == q.ownedEpoch
		}
	}
	return false
}

// MarkOwnedRows records that every row of the registration named by key holds
// key's installation now — the registrant has just registered each of them
// through RegisterOwnedHandler or its checkpoint form. The record holds until
// the next write to any owned row. A queue keeps three records, one for each
// registration a composed battle makes; a further key replaces one in turn,
// which only costs that registrant its per-row walk on a later visit.
func (q *Queue) MarkOwnedRows(key OwnedKey) {
	if q == nil || key == (OwnedKey{}) {
		return
	}
	for i := range q.ownedStamps {
		if q.ownedStamps[i].key == key {
			q.ownedStamps[i].epoch = q.ownedEpoch
			return
		}
	}
	q.ownedStamps[q.ownedStampNext] = ownedStamp{key: key, epoch: q.ownedEpoch}
	q.ownedStampNext = (q.ownedStampNext + 1) % uint8(len(q.ownedStamps))
}

// ownedStamp is one MarkOwnedRows record: the registration's key and the
// owned-row write count at which it was confirmed whole.
type ownedStamp struct {
	key   OwnedKey
	epoch uint64
}

// ownedKeyCurrent reports whether row id was last installed under key.
func (q *Queue) ownedKeyCurrent(id ID, key OwnedKey) bool {
	return q != nil && key != (OwnedKey{}) && int(id) > 0 && int(id) < len(q.ownedKeys) && q.ownedKeys[id] == key
}

// setOwnedKey records key as row id's installation. A nil handler stores no
// key: a cleared row is cheap to clear again and keeps the queue unallocated.
func (q *Queue) setOwnedKey(id ID, key OwnedKey, installed bool) {
	if q == nil || key == (OwnedKey{}) || !installed || int(id) <= 0 || int(id) >= len(table) {
		return
	}
	if q.ownedKeys == nil {
		q.ownedKeys = make([]OwnedKey, len(table))
	}
	q.ownedKeys[id] = key
}

// OwnedHandlerFor returns the handler registered for row id on this queue, or
// nil when the row has none.
func (q *Queue) OwnedHandlerFor(id ID) OwnedHandler {
	if q == nil || q.ownedHandlers == nil || int(id) <= 0 || int(id) >= len(q.ownedHandlers) {
		return nil
	}
	return q.ownedHandlers[id]
}

// getBuiltID is the `GetBuilt` identity, resolved once by buildTable. It cannot
// be a package-level initializer: the table is filled by init, not by a var
// initializer, so a Lookup at variable-initialization time would answer 0.
var getBuiltID ID

// SetGetBuiltHandler binds the construction-owned GetBuilt lifecycle to this
// queue. The queue pump remains the sole dispatcher and therefore preserves
// BeCarried/GetBuilt composition timing [04 R-FAC-02 §4].
//
// It is the named form of SetOwnedHandler for the one row whose owner always
// advances the record from inside the pump visit, so its registration reports
// `true` unconditionally and the pump applies the code it returns.
func (q *Queue) SetGetBuiltHandler(handler func(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code) {
	if q == nil {
		return
	}
	if handler == nil {
		q.SetOwnedHandler(getBuiltID, nil)
		return
	}
	q.SetOwnedHandler(getBuiltID, func(u *units.Unit, n *Node, satisfied uint32, tick uint32) (Code, bool) {
		return handler(u, n, satisfied, tick), true
	})
}

// SetExternallyDrivenHandler is the registration for a row the owning subsystem
// advances from its own per-unit step rather than from the pump. It is a
// convenience over SetOwnedHandler with a handler that always reports it did
// not advance the record; see OwnedHandler's second form for what the pump then
// does, and does not do.
//
// The registration is what makes the claim: a row nobody registers has no owner
// and is dispatched, or diagnosed, on its descriptor alone. When a subsystem
// later moves such a machine into the pump it replaces this call with a
// SetOwnedHandler carrying a real body, and nothing in this package changes.
func (q *Queue) SetExternallyDrivenHandler(id ID) {
	q.SetOwnedHandler(id, externallyDriven)
}

// externallyDriven is the shared body of every "my own per-unit step advances
// this record" registration. It writes nothing, which is the whole contract.
func externallyDriven(*units.Unit, *Node, uint32, uint32) (Code, bool) { return 0, false }

// ApproachWakeGate is the dynamic gate a ground work row's approach phase arms:
// `0xE0`, the three movement outcomes of [04 R-ORD-01 §0] — `0x20` the follower
// reached the goal, `0x40` an empty route was published away from it ("cannot
// get there"), `0x80` a goal object was released. [05 R-WORK-01 §13] states it
// for `MobileBuild` by name: phase 0 arms `0xE0`, so phase 1 is dispatched on
// those three bits and on nothing else.
//
// The gate is the WHOLE delivery mechanism, and the owning subsystem arms it on
// its own record. The pump's step 3 then computes `(record.satisfied |
// unit.pending) & record.gate`, clears the delivered bits out of both
// accumulating words and hands the set to the registered handler as its
// `satisfied` argument [04 §3.3] — the same argument `GetBuilt`'s phase-2 body
// reads. A handler that reports it did not advance the record (OwnedHandler's
// second form) still receives that argument, so a state machine stepped from
// its owner's per-unit pass gets its wake here and nowhere else.
//
// Retired with WU-19-225: a `DeliverApproachWake` helper stood below, repeating
// the pump's own step for a caller because the mobile-build approach parked on
// a private wake bit with a one-tick deadline instead of on retail's `0xE0`.
// The approach arms `0xE0` now, so the detour has no reason to exist.
const ApproachWakeGate uint32 = 0x20 | 0x40 | 0x80
