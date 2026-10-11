package session

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func TestCaptureOnlineCommandValuesAndPurity(t *testing.T) {
	f := newSeatFixture(t, true, false)
	f.s.LocalOwner = 1
	a, b, target := f.ref(f.own1a), f.ref(f.own1b), f.ref(f.own0)
	actors := []pool.Handle{f.own1b, f.own1a}
	refs := []pool.UnitRef{b, a}
	position := orders.ResolvePos{X: -65537, Y: 17, Z: 131075, InterfaceType: 1, HasFeature: true,
		IsWreck: true, FeatureResurrectable: true}
	point := CommandPosition{X: -65537, Y: 17, Z: 131075, InterfaceType: 1, HasFeature: true}
	cases := []struct {
		name string
		in   HumanCommand
		want SeatCommand
	}{
		{"ordinary-order", HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{b.Handle, a.Handle, b.Handle}, Code: 12, Target: target.Handle, Position: position, Queued: true}},
			SeatCommand{Kind: SeatOrder, Order: OrderPayload{Actors: []pool.UnitRef{a, b}, Code: 12, Target: target, Position: point, Queued: true}}},
		{"area-order", HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Handles: actors, Code: 11, Position: position, Queued: true, Targets: []HumanOrderTarget{{Target: target.Handle, Position: position}, {Position: position}, {Target: target.Handle, Position: position}}}},
			SeatCommand{Kind: SeatOrder, Order: OrderPayload{Actors: refs, Code: 11, Position: point, Queued: true, Targets: []CommandTarget{{Target: target, Position: point}, {Position: point}, {Target: target, Position: point}}}}},
		{"stop", HumanCommand{Kind: HumanStop, Stop: HumanStopCommand{Handles: actors}}, SeatCommand{Kind: SeatStop, Stop: StopPayload{Actors: refs}}},
		{"activation", HumanCommand{Kind: HumanActivation, Activation: HumanActivationCommand{Unit: a.Handle, Activate: true, Queued: true}},
			SeatCommand{Kind: SeatActivation, Activation: ActivationPayload{Unit: a, Activate: true, Queued: true}}},
		{"mobile-build", HumanCommand{Kind: HumanMobileBuild, MobileBuild: HumanMobileBuildCommand{Builder: a.Handle, Product: " ScOuT ", WX: 65537, WY: -1, WZ: 327679, Facing: 3, Queued: true, AppendOnly: true}},
			SeatCommand{Kind: SeatMobileBuild, MobileBuild: MobileBuildPayload{Builder: a, Product: "scout", Position: CommandPoint{X: 65537, Y: -1, Z: 327679}, Facing: 3, Queued: true, AppendOnly: true}}},
		{"factory-default", HumanCommand{Kind: HumanFactoryBuild, FactoryBuild: HumanFactoryBuildCommand{Builder: a.Handle, Product: " ScOuT "}},
			SeatCommand{Kind: SeatFactoryBuild, FactoryBuild: FactoryBuildPayload{Builder: a, Product: "scout", Count: 1}}},
		{"cancel-production", HumanCommand{Kind: HumanCancelProduction, CancelProduction: HumanCancelProductionCommand{Unit: a.Handle}},
			SeatCommand{Kind: SeatCancelProduction, CancelProduction: CancelProductionPayload{Unit: a}}},
		{"stockpile-default", HumanCommand{Kind: HumanStockpile, Stockpile: HumanStockpileCommand{Unit: a.Handle}},
			SeatCommand{Kind: SeatStockpile, Stockpile: StockpilePayload{Unit: a, Count: 1}}},
		{"group", HumanCommand{Kind: HumanGroupAssign, Group: HumanGroupCommand{Group: 4, Handles: actors, Preserve: true}},
			SeatCommand{Kind: SeatGroupAssign, GroupAssign: GroupAssignPayload{Group: 4, Members: refs}}},
		{"stance", HumanCommand{Kind: HumanStance, Stance: HumanStanceCommand{Handles: actors, Fire: true, Value: 2}},
			SeatCommand{Kind: SeatStance, Stance: StancePayload{Actors: refs, Fire: true, Value: 2}}},
		{"cloak", HumanCommand{Kind: HumanCloak, Cloak: HumanCloakCommand{Handles: actors, Cloak: true}},
			SeatCommand{Kind: SeatCloak, Cloak: CloakPayload{Actors: refs, Cloak: true}}},
		{"self-destruct", HumanCommand{Kind: HumanSelfDestruct, SelfDestruct: HumanSelfDestructCommand{Handles: actors, Queued: true}},
			SeatCommand{Kind: SeatSelfDestruct, SelfDestruct: SelfDestructPayload{Actors: refs, Queued: true}}},
		{"cancel-move", HumanCommand{Kind: HumanCancelQueuedMove, CancelQueuedMove: HumanCancelQueuedMoveCommand{Handles: actors, Sequence: 9001}},
			SeatCommand{Kind: SeatCancelQueuedMove, CancelQueuedMove: CancelQueuedMovePayload{Actors: refs, Sequence: 9001}}},
		{"builder-options", HumanCommand{Kind: HumanBuilderOptions, BuilderOptions: HumanBuilderOptionsCommand{Owner: 1, Options: orders.BuilderOptions{Guard: [3]orders.GuardHomeOption{2, 0, 1}, Patrol: [3]orders.PatrolWorkOption{1, 2, 0}}}},
			SeatCommand{Kind: SeatBuilderOptions, BuilderOptions: BuilderOptionsPayload{Guard: [3]uint8{2, 0, 1}, Patrol: [3]uint8{1, 2, 0}}}},
	}
	streams, checksum, serial := f.streams(), f.s.UnitStateChecksum(), f.s.Units.LastAllocationSerial()
	f.s.nextHumanSequence = 17
	f.s.seatCommands.lastPosition = 19
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Caller metadata must not become a stamp or a reused reference.
			tc.in.Sequence, tc.in.DueTick = 42, 123
			tc.in.refs = localRefs{captured: true, unit: target, actors: []pool.UnitRef{target}}
			got, err := f.s.CaptureOnlineCommand(tc.in)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("capture = %+v, %v; want %+v", got, err, tc.want)
			}
			encoded, err := EncodeSeatCommand(OnlineCommand, got)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := DecodeSeatCommand(OnlineCommand, encoded)
			if err != nil || !reflect.DeepEqual(decoded, got) {
				t.Fatalf("online round trip = %+v, %v", decoded, err)
			}
		})
	}
	if f.streams() != streams || f.s.UnitStateChecksum() != checksum || f.s.Units.LastAllocationSerial() != serial ||
		f.s.nextHumanSequence != 17 || f.s.seatCommands.lastPosition != 19 || len(f.s.pendingHuman) != 0 || len(f.s.seatCommands.receipts) != 0 {
		t.Fatal("capture changed world, random streams or command queues")
	}
	for _, h := range []pool.Handle{f.own0, f.own1a, f.own1b, f.comp2} {
		if f.queueLen(h) != 0 {
			t.Fatal("capture installed an order")
		}
	}
}

func TestCaptureOnlineCommandDetachedListsAndReferenceReuse(t *testing.T) {
	f := newSeatFixture(t, true, false)
	old := f.ref(f.own1b)
	in := HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{f.own1b, f.own1a}, Code: 11,
		Targets: []HumanOrderTarget{{Target: f.own0, Position: orders.ResolvePos{X: 123}}}}}
	got, err := f.s.CaptureOnlineCommand(in)
	if err != nil {
		t.Fatal(err)
	}
	in.Order.Handles[0], in.Order.Targets[0].Target, in.Order.Targets[0].Position.X = f.own0, 0, 456
	if got.Order.Actors[0] != old || got.Order.Targets[0].Target != f.ref(f.own0) || got.Order.Targets[0].Position.X != 123 {
		t.Fatal("capture retained caller-owned lists")
	}
	stop, err := f.s.CaptureOnlineCommand(HumanCommand{Kind: HumanStop, Stop: HumanStopCommand{Handles: []pool.Handle{old.Handle}}})
	if err != nil {
		t.Fatal(err)
	}
	f.s.Units.FreeImmediate(old.Handle)
	for _, in := range []HumanCommand{
		{Kind: HumanStop, Stop: HumanStopCommand{Handles: []pool.Handle{old.Handle}}},
		{Kind: HumanActivation, Activation: HumanActivationCommand{Unit: old.Handle}},
		{Kind: HumanOrder, Order: HumanOrderCommand{Code: 2, Target: old.Handle}},
		{Kind: HumanOrder, Order: HumanOrderCommand{Code: 11, Targets: []HumanOrderTarget{{Target: old.Handle}}}},
	} {
		if captured, err := f.s.CaptureOnlineCommand(in); err == nil || !reflect.DeepEqual(captured, SeatCommand{}) {
			t.Fatalf("stale reference captured as %+v, %v", captured, err)
		}
	}
	h, err := f.s.Units.Create(f.def, 1, 200<<16, 0, 64<<16)
	if err != nil || h != old.Handle {
		t.Fatalf("slot reuse = %d, %v", h, err)
	}
	expectOutcome(t, f.issue(t, 1, stop), CommandNoOp)
	fresh, err := f.s.CaptureOnlineCommand(HumanCommand{Kind: HumanStop, Stop: HumanStopCommand{Handles: []pool.Handle{h}}})
	if err != nil || fresh.Stop.Actors[0] == old {
		t.Fatalf("fresh capture reused old allocation: %+v, %v", fresh, err)
	}
	expectOutcome(t, f.issue(t, 1, fresh), CommandApplied)
	if f.queueLen(h) != 1 {
		t.Fatal("fresh allocation did not receive its own command")
	}
}

func TestCaptureOnlineCommandLeavesOwnershipToReceiver(t *testing.T) {
	f := newSeatFixture(t, true, false)
	in := HumanCommand{Kind: HumanStop, Stop: HumanStopCommand{Handles: []pool.Handle{f.own1a, f.own0}}}
	got, err := f.s.CaptureOnlineCommand(in)
	if err != nil || len(got.Stop.Actors) != 2 {
		t.Fatalf("foreign actor was sender-filtered: %+v, %v", got, err)
	}
	expectOutcome(t, f.issue(t, 1, got), CommandRejected)
	if f.queueLen(f.own1a) != 0 || f.queueLen(f.own0) != 0 {
		t.Fatal("foreign-actor rejection applied a friendly prefix")
	}
	for _, owner := range []uint8{0, 1} {
		f.s.LocalOwner = owner
		c, err := f.s.CaptureOnlineCommand(HumanCommand{Kind: HumanBuilderOptions, BuilderOptions: HumanBuilderOptionsCommand{Owner: owner}})
		if err != nil || c.BuilderOptions.Owner != 0 {
			t.Fatalf("local owner %d was retained in online payload: %+v, %v", owner, c, err)
		}
		if _, err := f.s.CaptureOnlineCommand(HumanCommand{Kind: HumanBuilderOptions, BuilderOptions: HumanBuilderOptionsCommand{Owner: owner ^ 1}}); err == nil {
			t.Fatal("foreign builder-options intent was silently retargeted")
		}
	}
}

func TestCaptureOnlineCommandRefusesUnsupportedAndNarrowing(t *testing.T) {
	f := newSeatFixture(t, true, false)
	for _, s := range []*Session{nil, {}, newSeatFixture(t, false, false).s} {
		if _, err := s.CaptureOnlineCommand(HumanCommand{Kind: HumanStop}); err == nil {
			t.Fatal("capture accepted a non-online session")
		}
	}
	for _, kind := range []HumanCommandKind{0, HumanSelectionReplace, HumanBuildPage, HumanGroupRecall, HumanNoShake, HumanATM,
		HumanSetResource, HumanSetLogo, HumanView, HumanMakeSelectable, HumanVisibility, HumanDoubleShot,
		HumanHalfShot, HumanMeteor, HumanBigBrother, HumanShiftState, HumanSpawn, HumanCommunityOrderDrag,
		HumanDeveloperSpawn, HumanGameplay, 45, 254} {
		if _, err := f.s.CaptureOnlineCommand(HumanCommand{Kind: kind}); err == nil {
			t.Fatalf("unsupported kind %d captured", kind)
		}
	}
	bad := []HumanCommand{
		{Kind: HumanOrder, Order: HumanOrderCommand{Code: 258}},
		{Kind: HumanOrder, Order: HumanOrderCommand{Code: -1}},
		{Kind: HumanOrder, Order: HumanOrderCommand{Code: 2, StagedCount: 1}},
		{Kind: HumanOrder, Order: HumanOrderCommand{Code: 2, Position: orders.ResolvePos{InterfaceType: 256}}},
		{Kind: HumanOrder, Order: HumanOrderCommand{Code: 11, Targets: []HumanOrderTarget{{Position: orders.ResolvePos{InterfaceType: -1}}}}},
		{Kind: HumanOrder, Order: HumanOrderCommand{Code: 2, Position: orders.ResolvePos{X: 1 << 32}}},
		{Kind: HumanOrder, Order: HumanOrderCommand{Code: 2, AssignedPosition: true}},
		{Kind: HumanStop, Stop: HumanStopCommand{Handles: []pool.Handle{f.own0, f.own0}}},
		{Kind: HumanStop, Stop: HumanStopCommand{Handles: []pool.Handle{0}}},
		{Kind: HumanActivation},
		{Kind: HumanMobileBuild, MobileBuild: HumanMobileBuildCommand{Builder: f.own0, Product: "scout", Facing: units.StructureFacing(4)}},
		{Kind: HumanFactoryBuild, FactoryBuild: HumanFactoryBuildCommand{Builder: f.own0, Product: "scout", Count: int(^uint(0) >> 1)}},
		{Kind: HumanFactoryBuild, FactoryBuild: HumanFactoryBuildCommand{Builder: f.own0, Product: ""}},
		{Kind: HumanStockpile, Stockpile: HumanStockpileCommand{Unit: f.own0, Count: -32768}},
		{Kind: HumanGroupAssign, Group: HumanGroupCommand{Group: 257}},
		{Kind: HumanStance, Stance: HumanStanceCommand{Value: 256}},
		{Kind: HumanStance, Stance: HumanStanceCommand{Value: -1}},
		{Kind: HumanCancelQueuedMove},
		{Kind: HumanBuilderOptions, BuilderOptions: HumanBuilderOptionsCommand{Options: orders.BuilderOptions{Guard: [3]orders.GuardHomeOption{3}}}},
		{Kind: HumanGive, Give: HumanGiveCommand{Player: -1, Amount: 1}},
		{Kind: HumanGive, Give: HumanGiveCommand{Player: 1}}, // zero needs the cheat permission (§15 Q8)
		{Kind: HumanCommunityKickout, CommunityKickout: HumanCommunityKickoutCommand{Unit: f.own0, X: 1 << 48}},
		{Kind: HumanShareGift, ShareGift: HumanShareGiftCommand{Handles: []pool.Handle{0}}},
	}
	streams, checksum := f.streams(), f.s.UnitStateChecksum()
	for i, in := range bad {
		if got, err := f.s.CaptureOnlineCommand(in); err == nil || !reflect.DeepEqual(got, SeatCommand{}) {
			t.Fatalf("invalid input %d returned %+v, %v", i, got, err)
		}
	}
	if f.streams() != streams || f.s.UnitStateChecksum() != checksum || len(f.s.pendingHuman) != 0 {
		t.Fatal("refusal mutated simulation")
	}
}

func TestCaptureOnlineCommandCountEdgesAndEmptyGroups(t *testing.T) {
	f := newSeatFixture(t, true, false)
	for _, n := range []int{-32768, -32767, -5, 0, 5, 32767, 32768} {
		factory, factoryErr := f.s.CaptureOnlineCommand(HumanCommand{Kind: HumanFactoryBuild, FactoryBuild: HumanFactoryBuildCommand{Builder: f.own0, Product: "scout", Count: n}})
		stockpile, stockpileErr := f.s.CaptureOnlineCommand(HumanCommand{Kind: HumanStockpile, Stockpile: HumanStockpileCommand{Unit: f.own0, Count: n}})
		if n < -32767 || n > 32767 {
			if factoryErr == nil || stockpileErr == nil {
				t.Fatalf("out-of-range count %d accepted", n)
			}
			continue
		}
		want := int32(n)
		if want == 0 {
			want = 1
		}
		if factoryErr != nil || stockpileErr != nil || factory.FactoryBuild.Count != want || stockpile.Stockpile.Count != want {
			t.Fatalf("count %d: factory=%+v/%v stockpile=%+v/%v", n, factory, factoryErr, stockpile, stockpileErr)
		}
	}
	if got, err := f.s.CaptureOnlineCommand(HumanCommand{Kind: HumanGroupAssign, Group: HumanGroupCommand{Group: 2}}); err != nil || len(got.GroupAssign.Members) != 0 {
		t.Fatalf("empty replacement group = %+v, %v", got, err)
	}
	if got, err := f.s.CaptureOnlineCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Code: 2}}); err != nil || len(got.Order.Actors) != 0 {
		t.Fatalf("empty order gained implicit selection: %+v, %v", got, err)
	}
}

func TestCaptureOnlineCommandRefusesExcessiveAreaWork(t *testing.T) {
	f := newSeatFixture(t, true, false)
	in := HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{f.own0}, Code: 11, Targets: make([]HumanOrderTarget, 10000)}}
	if _, err := f.s.CaptureOnlineCommand(in); err != nil {
		t.Fatalf("exact area entry bound refused: %v", err)
	}
	in.Order.Targets = append(in.Order.Targets, HumanOrderTarget{})
	if _, err := f.s.CaptureOnlineCommand(in); err == nil {
		t.Fatal("area work beyond the online bound accepted")
	}
	f.s.seatCommands.online.unitLimit = 1
	if _, err := f.s.CaptureOnlineCommand(HumanCommand{Kind: HumanStop, Stop: HumanStopCommand{Handles: []pool.Handle{f.own1a, f.own1b}}}); err == nil {
		t.Fatal("configured actor limit was not applied")
	}
}
