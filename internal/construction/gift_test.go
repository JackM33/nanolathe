package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// giftFixture builds a world whose catalog has a Commander-category
// definition, an ordinary one and one limited to two records per player.
func giftFixture(t *testing.T, perPlayer int) (*Service, map[string]*content.UnitDef) {
	t.Helper()
	defs := map[string]*content.UnitDef{
		"giftcom":   {UnitName: "giftcom", Category: "COMMANDER ARM", FootprintX: 2, FootprintZ: 2, MaxDamage: 100},
		"giftunit":  {UnitName: "giftunit", Category: "ARM", FootprintX: 2, FootprintZ: 2, MaxDamage: 100},
		"giftlimit": {UnitName: "giftlimit", Category: "ARM", FootprintX: 2, FootprintZ: 2, MaxDamage: 100, LimitEnabled: true, Limit: 2},
		"giftbig":   {UnitName: "giftbig", Category: "ARM", FootprintX: 2, FootprintZ: 2, MaxDamage: 30000},
		// Two stockpiling weapons: slots 0 and 1 enabled, slot 2 not.
		"giftarmed": {UnitName: "giftarmed", Category: "ARM", FootprintX: 2, FootprintZ: 2, MaxDamage: 100,
			Weapon1Def: &content.WeaponDef{Name: "giftrocket", ID: 1, Stockpile: true},
			Weapon2Def: &content.WeaponDef{Name: "giftmissile", ID: 2, Stockpile: true}},
	}
	for name, def := range defs {
		def.CanonicalKey = content.CanonicalKey(name)
	}
	registry, err := content.CompileCategories(defs)
	if err != nil {
		t.Fatal(err)
	}
	units := map[string]*content.UnitDef{}
	for _, def := range defs {
		units[def.CanonicalKey] = def
	}
	cat := &content.Catalog{Units: units, Categories: registry}
	return &Service{World: newConstructionFixtureWorld(perPlayer, cat), Catalog: cat}, defs
}

func giftCreate(t *testing.T, s *Service, def *content.UnitDef, owner uint8) *units.Unit {
	t.Helper()
	h, err := s.World.Create(def, owner, 0, 0, 0)
	if err != nil {
		t.Fatalf("create %s for %d: %v", def.UnitName, owner, err)
	}
	return s.World.Unit(h)
}

// The share screen leaves out an airborne unit, a unit in either transport
// attachment, and a Commander-category unit [05 R-SHARE-01 §5].
func TestGiftEligibleExclusions(t *testing.T) {
	s, defs := giftFixture(t, 10)
	plain := giftCreate(t, s, defs["giftunit"], 1)
	if !s.GiftEligible(plain) {
		t.Fatal("an ordinary grounded unit was excluded")
	}
	for name, edit := range map[string]func(*units.Unit){
		"airborne mirror": func(u *units.Unit) { u.Move.ModeMirror = 2 },
		"carried":         func(u *units.Unit) { u.Attachment.Carrier = 7 },
		"carrying":        func(u *units.Unit) { u.Attachment.Cargo = []pool.Handle{7} },
	} {
		u := giftCreate(t, s, defs["giftunit"], 1)
		edit(u)
		if s.GiftEligible(u) {
			t.Fatalf("%s unit was eligible", name)
		}
	}
	// The mirror's other values, and the live mode, do not exclude.
	parked := giftCreate(t, s, defs["giftunit"], 1)
	parked.Move.ModeMirror, parked.Move.Mode = 0, 2
	if !s.GiftEligible(parked) {
		t.Fatal("a unit whose mirror is not 2 was excluded")
	}
	if s.GiftEligible(giftCreate(t, s, defs["giftcom"], 1)) {
		t.Fatal("a Commander-category unit was eligible")
	}
	// A catalog without the category excludes nothing by category.
	bare := &Service{World: s.World, Catalog: &content.Catalog{}}
	if !bare.GiftEligible(giftCreate(t, s, defs["giftcom"], 1)) {
		t.Fatal("a missing Commander category excluded a unit")
	}
}

// Gifts run in ascending handle order whatever order the caller names them
// in, and an earlier replacement counts against the definition limit: the
// new owner holds one of a two-per-player definition, so the first gift
// reaches the limit and the later one is refused and stays with its owner
// [05 R-SHARE-01 §5] [05 R-SHARE-01 §8].
func TestGiftUnitsAscendingWithLimitRefusal(t *testing.T) {
	s, defs := giftFixture(t, 10)
	giftCreate(t, s, defs["giftlimit"], 0)
	first := giftCreate(t, s, defs["giftlimit"], 1)
	second := giftCreate(t, s, defs["giftlimit"], 1)
	com := giftCreate(t, s, defs["giftcom"], 1)
	other := giftCreate(t, s, defs["giftunit"], 2)
	if first.Handle >= second.Handle {
		t.Fatal("fixture handles are not ascending")
	}
	results := s.GiftUnits(1, 0, []pool.Handle{com.Handle, second.Handle, other.Handle, first.Handle, second.Handle}, GiftCopyLocal, 30)
	if len(results) != 4 {
		t.Fatalf("results = %+v, want four distinct handles", results)
	}
	want := []GiftOutcome{GiftTransferred, GiftRefused, GiftExcluded, GiftNotOwned}
	wantUnits := []pool.Handle{first.Handle, second.Handle, com.Handle, other.Handle}
	for i, r := range results {
		if r.Unit != wantUnits[i] || r.Outcome != want[i] {
			t.Fatalf("result %d = %+v, want unit %d outcome %d", i, r, wantUnits[i], want[i])
		}
	}
	repl := s.World.Unit(results[0].Replacement)
	if repl == nil || repl.Owner != 0 || repl.Def != defs["giftlimit"] {
		t.Fatal("the first unit's replacement is not the new owner's")
	}
	if !first.Dying {
		t.Fatal("the transferred record was not killed")
	}
	if second.Dying || second.Owner != 1 || com.Owner != 1 || com.Dying || other.Owner != 2 {
		t.Fatal("a refused, excluded or foreign unit changed")
	}
}

// A full slice refuses the transfer silently, as the allocator does
// [05 R-SHARE-01 §8].
func TestGiftUnitsRefusedByFullSlice(t *testing.T) {
	s, defs := giftFixture(t, 2)
	giftCreate(t, s, defs["giftunit"], 0)
	giftCreate(t, s, defs["giftunit"], 0)
	gift := giftCreate(t, s, defs["giftunit"], 1)
	results := s.GiftUnits(1, 0, []pool.Handle{gift.Handle}, GiftCopyLocal, 30)
	if len(results) != 1 || results[0].Outcome != GiftRefused || gift.Dying || gift.Owner != 1 {
		t.Fatalf("gift into a full slice = %+v", results)
	}
}

// remoteGiftFixture is giftFixture with the ordinary damage receiver bound,
// which the remote branch's kill goes through [06 §9.1].
func remoteGiftFixture(t *testing.T, perPlayer int) (*Service, map[string]*content.UnitDef) {
	t.Helper()
	s, defs := giftFixture(t, perPlayer)
	bindConstructionCombat(s)
	return s, defs
}

// The remote rule runs the giving machine's branch — fields captured, the
// selected bit cleared, the fixed kind-4 kill through the ordinary damage
// receiver — and only then the receiving arm, which builds a fresh finished
// record from the captured fields and the mirror's operational byte
// [05 R-WORK-01 §15].
func TestGiftUnitsRemoteOrderOfOperations(t *testing.T) {
	s, defs := remoteGiftFixture(t, 10)
	victim := giftCreate(t, s, defs["giftarmed"], 1)
	victim.Health, victim.Remaining, victim.Kills = 37, 0, 9
	victim.Move.Bank, victim.Move.Heading, victim.Move.Pitch = 111, 222, 333
	victim.Flags |= units.SelectedStatus | (uint32(3) << units.StandingMoveShift)
	victim.Activated = true
	victim.Slots[0].Ammo, victim.Slots[1].Ammo, victim.Slots[2].Ammo = 5, 7, 11
	var latchedAtCreation, created bool
	s.World.SetCreateHook(func(_ pool.Handle, u *units.Unit) {
		if u.Owner == 0 {
			created, latchedAtCreation = true, victim.Dying
		}
	})

	results := s.GiftUnits(1, 0, []pool.Handle{victim.Handle}, GiftCopyRemote, 40)
	if len(results) != 1 || results[0].Outcome != GiftTransferred {
		t.Fatalf("remote gift = %+v", results)
	}
	if !created || !latchedAtCreation {
		t.Fatal("the receiving arm ran before the giver's kill")
	}
	// The giving side: the departing copy stays the giver's, latched by a
	// cause-4 packet that went through the damage receiver (its flash).
	if !victim.Dying || victim.Owner != 1 || victim.LastDamageCause != uint8(combat.CauseCapture) || uint8(victim.BlinkSuppress) != 240 {
		t.Fatalf("departing copy: dying %t owner %d cause %d flash %d", victim.Dying, victim.Owner, victim.LastDamageCause, uint8(victim.BlinkSuppress))
	}
	if victim.Flags&units.SelectedStatus != 0 {
		t.Fatal("the departing copy kept its selected bit")
	}
	// The receiving side: health read before the kill, the rest from the
	// packet, defaults elsewhere.
	repl := s.World.Unit(results[0].Replacement)
	if repl == nil || repl.Owner != 0 || repl.Def != victim.Def {
		t.Fatal("no replacement of the new owner")
	}
	if repl.Health != 37 || repl.Remaining != 0 || repl.Kills != 0 {
		t.Fatalf("replacement health/remaining/kills = %d/%v/%d, want 37/0/0", repl.Health, repl.Remaining, repl.Kills)
	}
	if repl.Move.Bank != 111 || repl.Move.Heading != 222 || repl.Move.Pitch != 333 {
		t.Fatal("orientation triple not carried")
	}
	if got := (repl.Flags >> units.StandingMoveShift) & units.StandingFieldMask; got != 0 {
		t.Fatalf("standing-move field = %d, want cleared", got)
	}
	// Slot 2 is disabled on the replacement, so its byte is not written.
	if repl.Slots[0].Ammo != 5 || repl.Slots[1].Ammo != 7 || repl.Slots[2].Ammo != 0 {
		t.Fatalf("stockpile = %d/%d/%d, want 5/7/0", repl.Slots[0].Ammo, repl.Slots[1].Ammo, repl.Slots[2].Ammo)
	}
	if !repl.Activated {
		t.Fatal("the mirror's operational byte was not replayed")
	}
}

// The packet carries the remaining-build fraction truncated to a whole
// number: a partly built unit arrives finished, an untouched nanoframe
// arrives unbuilt [05 R-WORK-01 §15].
func TestGiftUnitsRemoteFractionTruncation(t *testing.T) {
	s, defs := remoteGiftFixture(t, 10)
	partly := giftCreate(t, s, defs["giftunit"], 1)
	partly.Remaining, partly.Health = 0.5, 40
	untouched := giftCreate(t, s, defs["giftunit"], 1)
	untouched.Remaining, untouched.Health = 1, 0
	results := s.GiftUnits(1, 0, []pool.Handle{partly.Handle, untouched.Handle}, GiftCopyRemote, 40)
	if len(results) != 2 || results[0].Outcome != GiftTransferred || results[1].Outcome != GiftTransferred {
		t.Fatalf("remote gifts = %+v", results)
	}
	a, b := s.World.Unit(results[0].Replacement), s.World.Unit(results[1].Replacement)
	if a.Remaining != 0 || a.Health != 40 {
		t.Fatalf("partly built arrived remaining %v health %d, want finished at 40", a.Remaining, a.Health)
	}
	if b.Remaining != 1 || b.Health != 0 {
		t.Fatalf("untouched frame arrived remaining %v health %d, want unbuilt at 0", b.Remaining, b.Health)
	}
	if results[0].Replacement >= results[1].Replacement {
		t.Fatal("replacements were not allocated in the giver's order")
	}
}

// The giver writes the stockpile bytes only when its slot 0 is enabled; the
// receiver writes each slot the replacement has enabled, so a victim with
// slot 0 disabled hands over zero bytes [05 R-WORK-01 §15].
func TestGiftUnitsRemoteStockpileGating(t *testing.T) {
	s, defs := remoteGiftFixture(t, 10)
	victim := giftCreate(t, s, defs["giftarmed"], 1)
	victim.Slots[0].Ammo, victim.Slots[1].Ammo = 5, 7
	victim.Slots[0].Flags &^= units.SlotFlagEnabled
	results := s.GiftUnits(1, 0, []pool.Handle{victim.Handle}, GiftCopyRemote, 40)
	repl := s.World.Unit(results[0].Replacement)
	if repl == nil || !repl.Slots[0].IsEnabled() || !repl.Slots[1].IsEnabled() {
		t.Fatal("fixture replacement lacks its enabled slots")
	}
	if repl.Slots[0].Ammo != 0 || repl.Slots[1].Ammo != 0 {
		t.Fatalf("stockpile = %d/%d, want zero bytes from a slot-0-disabled giver", repl.Slots[0].Ammo, repl.Slots[1].Ammo)
	}
}

// A refusal on the receiving side creates nothing, and the departing unit
// has already taken its kill, so the unit is lost [05 R-WORK-01 §15].
func TestGiftUnitsRemoteRefusalStillKills(t *testing.T) {
	s, defs := remoteGiftFixture(t, 2)
	giftCreate(t, s, defs["giftunit"], 0)
	giftCreate(t, s, defs["giftunit"], 0)
	victim := giftCreate(t, s, defs["giftunit"], 1)
	results := s.GiftUnits(1, 0, []pool.Handle{victim.Handle}, GiftCopyRemote, 40)
	if len(results) != 1 || results[0].Outcome != GiftLost || results[0].Replacement != 0 || !victim.Dying {
		t.Fatalf("remote gift into a full slice = %+v, departing dying %t", results, victim.Dying)
	}
	if got := s.World.CreatedCountForPlayer(0); got != 2 {
		t.Fatalf("receiver created %d units, want its 2", got)
	}
}

// Defender veterancy scales the fixed kill, so a tier-five copy holding more
// health than 24,000 survives as the giver's while the replacement exists —
// retail behaviour, not prevented [05 R-WORK-01 §15] [06 §9.2].
func TestGiftUnitsRemoteVeteranCopySurvives(t *testing.T) {
	s, defs := remoteGiftFixture(t, 10)
	victim := giftCreate(t, s, defs["giftbig"], 1)
	victim.Health, victim.Kills = 30000, 25
	results := s.GiftUnits(1, 0, []pool.Handle{victim.Handle}, GiftCopyRemote, 40)
	if len(results) != 1 || results[0].Outcome != GiftTransferred {
		t.Fatalf("remote gift = %+v", results)
	}
	if victim.Dying || victim.Owner != 1 || victim.Health != 6000 {
		t.Fatalf("veteran copy: dying %t owner %d health %d, want alive giver's at 6000", victim.Dying, victim.Owner, victim.Health)
	}
	if repl := s.World.Unit(results[0].Replacement); repl == nil || repl.Health != 30000 {
		t.Fatal("the replacement did not take the pre-kill health")
	}
}

// The entry gate refuses a latched unit, and a new owner whose record is not
// live sends and kills nothing [05 R-WORK-01 §15].
func TestGiftUnitsRemoteRefusals(t *testing.T) {
	s, defs := remoteGiftFixture(t, 10)
	s.Economy = &economy.Service{}
	s.Economy.Players[1].Exists = true
	victim := giftCreate(t, s, defs["giftunit"], 1)
	victim.Flags |= units.SelectedStatus
	results := s.GiftUnits(1, 0, []pool.Handle{victim.Handle}, GiftCopyRemote, 40)
	if len(results) != 1 || results[0].Outcome != GiftRefused || victim.Dying || victim.BlinkSuppress != 0 || victim.Flags&units.SelectedStatus == 0 {
		t.Fatalf("gift to an absent record = %+v; the unit changed", results)
	}
	s.Economy.Players[0].Exists = true
	victim.Dying = true
	results = s.GiftUnits(1, 0, []pool.Handle{victim.Handle}, GiftCopyRemote, 40)
	if len(results) != 1 || results[0].Outcome != GiftRefused || s.World.CreatedCountForPlayer(0) != 0 {
		t.Fatalf("gift of a latched unit = %+v", results)
	}
}
