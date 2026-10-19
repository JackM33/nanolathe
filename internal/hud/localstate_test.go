package hud

import (
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

func ref(h pool.Handle, serial uint64) pool.UnitRef { return pool.UnitRef{Handle: h, Serial: serial} }

func facts(tick uint32, unready []pool.UnitRef, ready ...pool.UnitRef) frame.InterfaceFacts {
	return frame.InterfaceFacts{Valid: true, Tick: tick, Unready: unready, Ready: ready}
}

// A toggle flips each listed reference in order, so a repeat cancels itself,
// as the bit toggle it replaces did [07 §9]. Entries are keyed by allocation
// reference: the unit created in a selected unit's slot is not selected
// (DESIGN_MULTIPLAYER §16.2 M2-C6).
func TestLocalSelectionGesturesAndReferenceKeys(t *testing.T) {
	l := NewLocalInterface()
	a, b, reused := ref(3, 10), ref(5, 11), ref(3, 12)
	l.ReplaceSelection([]pool.UnitRef{b, a, b})
	if got := l.SelectedRefs(); !slices.Equal(got, []pool.UnitRef{a, b}) {
		t.Fatalf("replace = %v, want the set in slot order", got)
	}
	if l.Selected(reused) {
		t.Fatal("a reused slot inherited the selection")
	}
	epoch := l.Epoch()
	l.ToggleSelection([]pool.UnitRef{a, a, b})
	if got := l.SelectedRefs(); !slices.Equal(got, []pool.UnitRef{a}) || l.Epoch() == epoch {
		t.Fatalf("toggle = %v, want the repeated reference unchanged and b removed", got)
	}
	l.ClearSelection()
	if l.SelectionCount() != 0 {
		t.Fatal("clear left a selection")
	}
	l.ReplaceSelection([]pool.UnitRef{a, b})
	l.MarkVisited(a)
	l.Prune(func(r pool.UnitRef) bool { return r != a })
	if l.Selected(a) || l.Visited(a) || !l.Selected(b) {
		t.Fatal("prune did not forget the dead reference alone")
	}
}

// The readiness clear is the sweep's step 7: a selected unit unready at its
// own visit leaves the selection whatever it is at the end of the tick
// [04 R-MOV-03 §1]; invalid facts — a republication's — change nothing.
func TestLocalSelectionReadinessClear(t *testing.T) {
	l := NewLocalInterface()
	a, b := ref(1, 1), ref(2, 2)
	l.ReplaceSelection([]pool.UnitRef{a, b})
	l.Advance(frame.InterfaceFacts{Unready: []pool.UnitRef{a}})
	if !l.Selected(a) {
		t.Fatal("invalid facts cleared a selection")
	}
	l.Advance(facts(1, []pool.UnitRef{a}, b))
	if l.Selected(a) || !l.Selected(b) {
		t.Fatalf("selection after an unready visit = %v", l.SelectedRefs())
	}
}

// BigBrother's sweep-tail lifecycle [07 R-CAM-01 §12]: held Shift pauses the
// countdown; a cycle with a selected ready unit clears every selection and
// visit and selects the ready unit after it, wrapping to the first; a cycle
// with none selects the first ready unit without the clear; disabling keeps
// the counter and cancels follow once; the signed decrement through zero
// notifies even with no ready unit.
func TestLocalBigBrotherLifecycle(t *testing.T) {
	l := NewLocalInterface()
	first, unready, last := ref(1, 1), ref(2, 2), ref(3, 3)
	ready := []pool.UnitRef{first, last} // unready is skipped by the walk
	l.ReplaceSelection([]pool.UnitRef{first})
	l.MarkVisited(first)

	l.ToggleBigBrother()
	l.SetShiftHeld(true)
	if ev := l.Advance(facts(1, nil, ready...)); ev.Cycle || l.bb.countdown != 1 {
		t.Fatal("held Shift did not pause the first cycle")
	}
	l.SetShiftHeld(false)
	ev := l.Advance(facts(2, nil, ready...))
	if !ev.Cycle || !ev.ResetVisited || ev.CancelFollow || l.bb.countdown != bigBrotherPeriod {
		t.Fatalf("first cycle notices %+v countdown %d", ev, l.bb.countdown)
	}
	if !slices.Equal(l.SelectedRefs(), []pool.UnitRef{last}) || l.Visited(first) {
		t.Fatalf("first cycle selected %v; want the next ready unit and no visits", l.SelectedRefs())
	}
	if ev := l.Advance(facts(3, nil, ready...)); ev.Cycle || l.bb.countdown != bigBrotherPeriod-1 {
		t.Fatal("an ordinary tick did not decrement without cycling")
	}
	l.bb.countdown = 1
	l.Advance(facts(4, nil, ready...))
	if !slices.Equal(l.SelectedRefs(), []pool.UnitRef{first}) {
		t.Fatalf("end of the slice selected %v, want the wrap to the first ready unit", l.SelectedRefs())
	}

	l.ToggleBigBrother()
	if ev := l.TakeEvents(); !ev.CancelFollow || l.bb.countdown != bigBrotherPeriod {
		t.Fatal("disable did not keep the counter and cancel follow")
	}
	if ev := l.Advance(facts(5, nil, ready...)); ev.CancelFollow || ev.Cycle || l.bb.countdown != bigBrotherPeriod {
		t.Fatal("a disabled tick changed the counter or repeated a notice")
	}

	l.ReplaceSelection([]pool.UnitRef{unready})
	l.MarkVisited(last)
	l.ToggleBigBrother()
	ev = l.Advance(facts(6, nil, ready...))
	if !ev.Cycle || ev.ResetVisited || !l.Selected(first) || !l.Selected(unready) || !l.Visited(last) {
		t.Fatalf("no selected ready unit must select the first without the clear: %+v %v", ev, l.SelectedRefs())
	}
	l.bb.countdown = 0
	if ev := l.Advance(facts(7, nil)); !ev.Cycle || ev.ResetVisited || l.bb.countdown != bigBrotherPeriod {
		t.Fatal("the signed decrement through zero must notify even with no ready units")
	}
}

// A unit that turns unready at its visit and ready again by the sweep tail
// (it is in the tail's ready list) still left the selection first, so the
// tail walk finds it unselected (M2-C6).
func TestLocalReadinessClearPrecedesTheTailWalk(t *testing.T) {
	l := NewLocalInterface()
	a, b, c := ref(1, 1), ref(2, 2), ref(3, 3)
	l.ReplaceSelection([]pool.UnitRef{b})
	l.ToggleBigBrother()
	l.Advance(facts(1, []pool.UnitRef{b}, a, b, c))
	if !slices.Equal(l.SelectedRefs(), []pool.UnitRef{a}) {
		t.Fatalf("selection %v: the cleared unit must not anchor the cycle", l.SelectedRefs())
	}
}

// The page field falls back to the published status word's — creation's seed
// or a load's value — and local input moves it through the retail identity
// guard, page-count clamp and encoding [07 §9][07 R-HUD-03 §6].
func TestLocalBuildPageGuardAndClamp(t *testing.T) {
	l := NewLocalInterface()
	builder := ref(4, 9)
	seed := EncodePageBits(0x20, 1)
	if got := DecodePage(l.PageFlags(builder, seed)); got != 1 {
		t.Fatalf("seeded page %d, want 1", got)
	}
	if l.SetBuildPage(builder, seed, 0, 2, 3) {
		t.Fatal("a zero definition id passed the identity guard")
	}
	if !l.SetBuildPage(builder, seed, 7, 2, 3) || DecodePage(l.PageFlags(builder, seed)) != 2 {
		t.Fatal("valid page move did not apply")
	}
	l.SetBuildPage(builder, seed, 7, 0, 3)
	if flags := l.PageFlags(builder, seed); DecodePage(flags) != 0 || RememberedPage(flags) != 2 {
		t.Fatalf("orders page flags %#x: want page 0 remembering 2", flags)
	}
	l.SetBuildPage(builder, seed, 7, 9, 3)
	if got := DecodePage(l.PageFlags(builder, seed)); got != 2 {
		t.Fatalf("page-count clamp got %d, want 2", got)
	}
	if got := DecodePage(l.PageFlags(ref(4, 10), seed)); got != 1 {
		t.Fatal("a reused slot inherited the build page")
	}
}

// Recall changes only the local selection; an assignment sent but not yet
// published is overlaid on the published group numbers, so assign-then-recall
// in one input batch recalls the new membership, as phase 1 applied both in
// order [07 §9].
func TestLocalGroupRecallSeesPendingAssignment(t *testing.T) {
	l := NewLocalInterface()
	a, b, c := ref(1, 1), ref(2, 2), ref(3, 3)
	units := []GroupUnit{{Ref: a, DefID: 1, Group: 4}, {Ref: b, DefID: 1}, {Ref: c, DefID: 1}}
	l.RecallGroup(units, 4, false, content.CategoryMask{})
	if !slices.Equal(l.SelectedRefs(), []pool.UnitRef{a}) {
		t.Fatalf("recall = %v, want the published member", l.SelectedRefs())
	}
	l.NoteGroupAssign(4, []pool.UnitRef{b, c}, 9)
	l.RecallGroup(units, 4, false, content.CategoryMask{})
	if !slices.Equal(l.SelectedRefs(), []pool.UnitRef{b, c}) {
		t.Fatalf("recall after a pending assignment = %v, want its membership", l.SelectedRefs())
	}
	l.ObserveTick(9)
	l.RecallGroup(units, 4, true, content.CategoryMask{})
	if !slices.Equal(l.SelectedRefs(), []pool.UnitRef{a, b, c}) {
		t.Fatalf("preserving recall after the assignment's tick = %v, want a toggled in", l.SelectedRefs())
	}
}

// Recall with no CTRL_F filter supplied recalls every member, whatever its
// definition id: the key flag on an armed member must not turn absent filter
// state into a filter that drops them, including the IDs past 511 that a
// content profile or faction add-ons give a widened catalog
// (docs/DESIGN_MODS_MUTATORS.md §5.8).
func TestLocalGroupRecallWithoutFilterKeepsWideDefinitionIDs(t *testing.T) {
	l := NewLocalInterface()
	a, b := ref(1, 1), ref(2, 2)
	units := []GroupUnit{{Ref: a, DefID: 7, Group: 3, Flags: CtrlFFlag}, {Ref: b, DefID: 600, Group: 3, Flags: CtrlFFlag}}
	l.RecallGroup(units, 3, false, content.CategoryMask{})
	if !slices.Equal(l.SelectedRefs(), []pool.UnitRef{a, b}) {
		t.Fatalf("recall = %v, want both members", l.SelectedRefs())
	}
}

// Composition writes the local selection onto a committed frame: only the
// local player's matching allocation, the selected bit, the local page field,
// the contacts' selection words and range gate, and a logo override. A frame
// already composed at the epoch is left alone.
func TestComposeSelectionOntoTheFrame(t *testing.T) {
	l := NewLocalInterface()
	f := &frame.Frame{
		Selection: frame.SelectionView{LocalPlayer: 1},
		Units: []frame.UnitView{
			{Slot: 2, AllocationSerial: 5, Owner: 1, Flags: EncodePageBits(0, 1), OwnerColorKnown: true, OwnerColor: 3},
			{Slot: 3, AllocationSerial: 6, Owner: 0, OwnerColorKnown: true},
			{Slot: 4, AllocationSerial: 8, Owner: 1},
		},
		Radar: frame.RadarView{Contacts: []frame.RadarContactView{
			{Kind: frame.RadarContactUnit, Handle: 2, Owner: 1, RangeEligible: true, PaletteKnown: true},
			{Kind: frame.RadarContactUnit, Handle: 3, Owner: 0, RangeEligible: true},
			{Kind: frame.RadarContactUnit, Handle: 4, Owner: 1, RangeEligible: false},
		}},
	}
	f.Players[1].Present = true
	// Slot 4's selected reference is an older allocation of that slot.
	l.ReplaceSelection([]pool.UnitRef{ref(2, 5), ref(3, 6), ref(4, 7)})
	l.SetBuildPage(ref(2, 5), f.Units[0].Flags, 1, 2, 3)
	l.SetLogoOverride(1, 9)
	if !l.ComposeSelection(f) || l.ComposeSelection(f) {
		t.Fatal("composition is not keyed by epoch")
	}
	if !slices.Equal(f.Selection.Handles, []pool.Handle{2}) || f.Selection.Primary != 2 || f.Selection.Count != 1 || f.Selection.Composed != l.Epoch() {
		t.Fatalf("composed selection %+v", f.Selection)
	}
	if f.Units[0].Flags&SelectionFlag == 0 || f.Units[1].Flags&SelectionFlag != 0 || f.Units[2].Flags&SelectionFlag != 0 {
		t.Fatal("selected bit composed onto the wrong units")
	}
	if DecodePage(f.Units[0].Flags) != 2 {
		t.Fatalf("composed page %d, want the local page 2", DecodePage(f.Units[0].Flags))
	}
	c := f.Radar.Contacts
	if !c[0].Selected || c[0].Status&SelectionFlag == 0 || !c[0].RangeStatus || c[1].Selected || c[1].RangeStatus || c[2].Selected {
		t.Fatalf("contacts %+v", c)
	}
	if f.Players[1].Logo != 9 || f.Units[0].OwnerColor != 9 || c[0].Palette != 9 || f.Units[1].OwnerColor != 0 {
		t.Fatal("the logo override was not composed onto the overridden player alone")
	}
	l.ClearSelection()
	l.ComposeSelection(f)
	if f.Units[0].Flags&SelectionFlag != 0 || c[0].RangeStatus || f.Selection.Count != 0 {
		t.Fatal("recomposition kept a stale selection")
	}
}

// The command page is the single selected unit's, whatever it is, with the
// bound rule's complete membership, and the page's products are copied out of
// the catalog, so a later catalog change does not reach the frame
// [07 §9][07 R-HUD-03 §6][I6]. A mixed selection has aggregate command state
// and no page.
func TestComposeCommandPageFromTheLocalSelection(t *testing.T) {
	builder := &content.UnitDef{UnitName: "builder", Builder: true, BuildPageCount: 3, MobileStandOrders: true}
	builder.CanonicalKey = "builder"
	worker := &content.UnitDef{UnitName: "worker", MobileStandOrders: true}
	worker.CanonicalKey = "worker"
	menu := &content.BuildMenuPage{Buttons: []string{"a", "b", "c", "d", "e", "f", "g"}}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"builder": builder, "worker": worker},
		BuildMenus: map[string]*content.BuildMenuPage{"builder": menu}}
	products := func(key string) []string { return menu.Buttons }
	f := &frame.Frame{Units: []frame.UnitView{
		{Slot: 1, AllocationSerial: 1, DefName: "builder", Flags: EncodePageBits(1<<18, 2), StockpileRounds: 4},
		{Slot: 2, AllocationSerial: 2, DefName: "worker", Flags: 2 << 18},
	}}
	l := NewLocalInterface()
	l.ReplaceSelection([]pool.UnitRef{ref(1, 1)})
	l.ComposeSelection(f)
	ComposeCommandPage(&f.CommandPage, f, cat, products)
	page := f.CommandPage
	if page.Builder != 1 || page.Page != 2 || page.PageCount != 3 || page.Stockpile != 4 || page.MoveStance != 1 ||
		!slices.Equal(page.ProductKeys, []string{"g"}) || len(page.AllowedProducts) != 7 {
		t.Fatalf("command page %+v", page)
	}
	menu.Buttons[6] = "changed"
	if page.ProductKeys[0] != "g" || page.AllowedProducts[6] != "g" {
		t.Fatal("the composed page aliases catalog storage")
	}
	l.ReplaceSelection([]pool.UnitRef{ref(1, 1), ref(2, 2)})
	l.ComposeSelection(f)
	ComposeCommandPage(&f.CommandPage, f, cat, products)
	if f.CommandPage.Builder != 0 || f.CommandPage.MoveStance != 3 {
		t.Fatalf("mixed selection page %+v, want no builder and the disagreeing stance", f.CommandPage)
	}
}
