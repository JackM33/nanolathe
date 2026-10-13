package session

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// CaptureOnlineCommand converts a resolved local gesture into the play-test's
// closed online vocabulary (DESIGN_MULTIPLAYER §16.4.2). Call while simulation
// is quiescent, after the host resolves selection. It captures current live
// allocations, owns every returned list, and neither enqueues nor applies the
// command. Live foreign actors remain explicit for phase-1 authorization.
// CancelQueuedMove.Sequence must already be the assigned stream position.
func (s *Session) CaptureOnlineCommand(c HumanCommand) (SeatCommand, error) {
	if !s.OnlineCommandContext() {
		return SeatCommand{}, onlineCaptureError("context", "an online command session")
	}
	// Only this capture may supply references, even for an in-package caller
	// reusing a previously captured local command.
	c.refs = localRefs{}
	s.captureLocalRefs(&c)
	var out SeatCommand
	switch c.Kind {
	case HumanEconomyActivity:
		out.Kind = SeatEconomyActivity
		out.EconomyActivity = EconomyActivityPayload(c.EconomyActivity)
	case HumanOrder:
		p := c.Order
		if p.StagedCount != 0 {
			return SeatCommand{}, onlineCaptureError("order.stagedCount", "zero replay-only staging count")
		}
		if p.Code < 1 || p.Code > 14 {
			return SeatCommand{}, onlineCaptureError("order.code", "an order code 1..14")
		}
		position, err := onlineCapturePosition(p.Position)
		if err != nil {
			return SeatCommand{}, err
		}
		out.Kind = SeatOrder
		out.Order = OrderPayload{Actors: c.refs.actors, Code: uint8(p.Code), Target: c.refs.target, Position: position,
			Queued: p.Queued, AssignedPosition: p.AssignedPosition, TrackQueuedMove: p.TrackQueuedMove}
		if len(p.Targets) == 0 {
			out.Order.Actors = sortedUniqueRefs(out.Order.Actors)
		} else {
			out.Order.Targets = make([]CommandTarget, len(p.Targets))
			for i, target := range p.Targets {
				position, err := onlineCapturePosition(target.Position)
				if err != nil {
					return SeatCommand{}, onlineCaptureError(fmt.Sprintf("order.targets[%d].position.interfaceType", i), "an interface type 0 left or 1 right")
				}
				out.Order.Targets[i] = CommandTarget{Target: c.refs.targets[i], Position: position}
			}
		}
	case HumanStop:
		out.Kind, out.Stop = SeatStop, StopPayload{Actors: c.refs.actors}
	case HumanActivation:
		out.Kind = SeatActivation
		out.Activation = ActivationPayload{Unit: c.refs.unit, Activate: c.Activation.Activate, Queued: c.Activation.Queued}
	case HumanMobileBuild:
		p := c.MobileBuild
		out.Kind = SeatMobileBuild
		out.MobileBuild = MobileBuildPayload{Builder: c.refs.unit, Product: content.CanonicalKey(p.Product),
			Position: CommandPoint{X: p.WX, Y: p.WY, Z: p.WZ}, Facing: uint8(p.Facing), Queued: p.Queued, AppendOnly: p.AppendOnly}
	case HumanFactoryBuild:
		count, err := onlineCaptureCount(c.FactoryBuild.Count)
		if err != nil {
			return SeatCommand{}, err
		}
		out.Kind = SeatFactoryBuild
		out.FactoryBuild = FactoryBuildPayload{Builder: c.refs.unit, Product: content.CanonicalKey(c.FactoryBuild.Product), Count: count}
	case HumanCancelProduction:
		out.Kind, out.CancelProduction = SeatCancelProduction, CancelProductionPayload{Unit: c.refs.unit}
	case HumanStockpile:
		count, err := onlineCaptureCount(c.Stockpile.Count)
		if err != nil {
			return SeatCommand{}, err
		}
		out.Kind, out.Stockpile = SeatStockpile, StockpilePayload{Unit: c.refs.unit, Count: count}
	case HumanGroupAssign:
		if c.Group.Group < 1 || c.Group.Group > 9 {
			return SeatCommand{}, onlineCaptureError("groupAssign.group", "a group 1..9")
		}
		out.Kind, out.GroupAssign = SeatGroupAssign, GroupAssignPayload{Group: uint8(c.Group.Group), Members: c.refs.actors}
	case HumanStance:
		if c.Stance.Value < 0 || c.Stance.Value > 2 {
			return SeatCommand{}, onlineCaptureError("stance.value", "a stance value 0..2")
		}
		out.Kind, out.Stance = SeatStance, StancePayload{Actors: c.refs.actors, Fire: c.Stance.Fire, Value: uint8(c.Stance.Value)}
	case HumanCloak:
		out.Kind, out.Cloak = SeatCloak, CloakPayload{Actors: c.refs.actors, Cloak: c.Cloak.Cloak}
	case HumanSelfDestruct:
		out.Kind, out.SelfDestruct = SeatSelfDestruct, SelfDestructPayload{Actors: c.refs.actors, Queued: c.SelfDestruct.Queued}
	case HumanCancelQueuedMove:
		out.Kind, out.CancelQueuedMove = SeatCancelQueuedMove, CancelQueuedMovePayload{Sequence: c.CancelQueuedMove.Sequence, Actors: c.refs.actors}
	case HumanBuilderOptions:
		if c.BuilderOptions.Owner != s.LocalOwner {
			return SeatCommand{}, onlineCaptureError("builderOptions.owner", "the local human owner's options")
		}
		out.Kind = SeatBuilderOptions
		for i := range out.BuilderOptions.Guard {
			out.BuilderOptions.Guard[i] = uint8(c.BuilderOptions.Options.Guard[i])
			out.BuilderOptions.Patrol[i] = uint8(c.BuilderOptions.Options.Patrol[i])
		}
	case HumanGive:
		// `+Give p n metal`: the stamped seat is the source online and the
		// payload names the recipient (§7.1, §7.4).
		p := c.Give
		if p.Player < 0 || p.Player > 9 {
			return SeatCommand{}, onlineCaptureError("give.player", "a recipient 0..9")
		}
		out.Kind, out.Give = SeatGive, GivePayload{Player: uint8(p.Player), Resource: p.Resource, Amount: p.Amount}
	case HumanCommunityKickout:
		ref, err := s.captureKickoutUnit(c.CommunityKickout)
		if err != nil {
			return SeatCommand{}, err
		}
		c.refs.unit = ref
		out.Kind = SeatCommunityKickout
		out.CommunityKickout = CommunityKickoutPayload{Unit: ref,
			Destination: CommandPoint{X: c.CommunityKickout.X, Y: c.CommunityKickout.Y, Z: c.CommunityKickout.Z}}
	case HumanShareMetal:
		out.Kind, out.ShareMetal = SeatShareMetal, ShareMetalPayload{On: c.ShareOption.On}
	case HumanShareEnergy:
		out.Kind, out.ShareEnergy = SeatShareEnergy, ShareEnergyPayload{On: c.ShareOption.On}
	case HumanShareMapping:
		out.Kind, out.ShareMapping = SeatShareMapping, ShareMappingPayload{On: c.ShareOption.On}
	case HumanShareRadar:
		out.Kind, out.ShareRadar = SeatShareRadar, ShareRadarPayload{On: c.ShareOption.On}
	case HumanShareAll:
		out.Kind, out.ShareAll = SeatShareAll, ShareAllPayload(c.ShareAll)
	case HumanSetShareMetal:
		out.Kind, out.SetShareMetal = SeatSetShareMetal, SetShareMetalPayload{Amount: c.ShareThreshold.Amount}
	case HumanSetShareEnergy:
		out.Kind, out.SetShareEnergy = SeatSetShareEnergy, SetShareEnergyPayload{Amount: c.ShareThreshold.Amount}
	case HumanShareGift:
		// The selection as a set in ascending handle order, the order the
		// transfer visits it [05 R-SHARE-01 §5].
		p := c.ShareGift
		out.Kind = SeatShareGift
		out.ShareGift = ShareGiftPayload{Target: p.Target, Metal: p.Metal, Energy: p.Energy,
			Actors: sortedUniqueRefs(c.refs.actors), MapInfo: p.MapInfo}
		if len(out.ShareGift.Actors) == 0 {
			out.ShareGift.Actors = nil
		}
	case HumanDeclareAlliance:
		out.Kind = SeatDeclareAlliance
		out.DeclareAlliance = DeclareAlliancePayload{Target: c.DeclareAlliance.Target, Value: c.DeclareAlliance.Value}
	case HumanSharedVictory:
		out.Kind, out.SharedVictory = SeatSharedVictory, SharedVictoryPayload{On: c.SharedVictory.On}
	default:
		return SeatCommand{}, onlineCaptureError("kind", fmt.Sprintf("a supported play-test command, got %d", c.Kind))
	}
	if err := s.validateOnlineCaptureRefs(c.refs); err != nil {
		return SeatCommand{}, err
	}
	if _, err := EncodeSeatCommand(OnlineCommand, out); err != nil {
		return SeatCommand{}, err
	}
	if why := s.seatSchema(OnlineCommand, &out); why != "" {
		return SeatCommand{}, onlineCaptureError("payload", why)
	}
	// The cheat permission is every receiver's to enforce (§7.2); the sender
	// refuses first what no receiver would admit, such as a negative gift in
	// a room without it.
	if why := s.onlinePermission(&out); why != "" {
		return SeatCommand{}, onlineCaptureError("payload", why)
	}
	return out, nil
}

// captureKickoutUnit names the unit a Community kickout gesture drew on: the
// handle's current allocation, which must still be the unit the client's
// published frame showed under that identity, as the local adapter's check
// requires (publishedHumanUnit). Ownership stays the receiver's to judge.
func (s *Session) captureKickoutUnit(c HumanCommunityKickoutCommand) (pool.UnitRef, error) {
	if c.Unit == 0 {
		return pool.UnitRef{}, onlineCaptureError("communityKickout.unit", "a unit")
	}
	if s.publication != nil {
		ids := s.publication.unitIdentities
		if c.InstanceID == 0 || int(c.Unit) >= len(ids) || ids[c.Unit].id != c.InstanceID || ids[c.Unit].unit != s.Units.Unit(c.Unit) {
			return pool.UnitRef{}, onlineCaptureError("communityKickout.instanceID", "the published unit the gesture named")
		}
	}
	return s.captureRef(c.Unit), nil
}

func (s *Session) validateOnlineCaptureRefs(refs localRefs) error {
	live := func(ref pool.UnitRef) bool { return s.Units.LookupReference(ref) != nil }
	for i, ref := range refs.actors {
		if !live(ref) {
			return onlineCaptureError(fmt.Sprintf("actors[%d]", i), "a current live allocation")
		}
	}
	if refs.unit != (pool.UnitRef{}) && !live(refs.unit) {
		return onlineCaptureError("unit", "a current live allocation")
	}
	if refs.target != (pool.UnitRef{}) && !live(refs.target) {
		return onlineCaptureError("order.target", "a current live allocation or an intentional null target")
	}
	for i, ref := range refs.targets {
		if ref != (pool.UnitRef{}) && !live(ref) {
			return onlineCaptureError(fmt.Sprintf("order.targets[%d].target", i), "a current live allocation or an intentional null target")
		}
	}
	return nil
}

func onlineCapturePosition(p orders.ResolvePos) (CommandPosition, error) {
	if p.InterfaceType < 0 || p.InterfaceType > 1 {
		return CommandPosition{}, onlineCaptureError("position.interfaceType", "an interface type 0 left or 1 right")
	}
	// IsWreck and FeatureResurrectable have no authoritative readers or wire
	// fields (§7.4.1). HasFeature is captured intent, not a visibility claim.
	return CommandPosition{X: p.X, Y: p.Y, Z: p.Z, InterfaceType: uint8(p.InterfaceType), HasFeature: p.HasFeature}, nil
}

func onlineCaptureCount(count int) (int32, error) {
	if count == 0 {
		count = 1 // Existing factory and stockpile producer default (§7.4.2).
	}
	if count < -seatOnlineMaxCount || count > seatOnlineMaxCount {
		return 0, onlineCaptureError("count", "a count of -32767..-1 or 1..32767 online")
	}
	return int32(count), nil
}

func onlineCaptureError(path, expected string) error {
	return fmt.Errorf("nanolathe: online command capture failed: logical path %s, providers searched [session], expected %s", path, expected)
}
