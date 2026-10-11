package main

// The share commands of an online battle (DESIGN_MULTIPLAYER §7.1):
// `+ShareMetal`, `+ShareEnergy`, `+ShareMapping`, `+ShareRadar`, `+ShareAll`,
// `+SetShareMetal n`, `+SetShareEnergy n` and `+Give p n metal|energy`
// [07 R-CAM-01 §6]. Retail's handlers flip or store the local player's
// option on the typing machine; here each becomes the issuing seat's command,
// carrying the value the press produces from the committed frame the client
// shows, so every replica applies the same value at the same tick. The line
// is still sent as chat afterwards (commitLocalChat).

import (
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// onlineShareCommand runs one share command for the local seat. It reports
// false when this battle cannot issue one: a replay playback, a battle with no
// committed sharing standing, or a seat that may not share (a watcher cannot
// type a `+` line in retail [07 §5 "Chat"]).
func (b *battleSession) onlineShareCommand(words []string) bool {
	if b == nil || b.multiplayer == nil || b.playback != nil {
		return false
	}
	cur, ok := b.currentSnapshot()
	if !ok || cur == nil || !cur.Sharing.Online || !cur.Sharing.MayShare {
		return false
	}
	me := cur.Players[cur.Sharing.Seat]
	switch name := strings.ToLower(words[0]); name {
	case "sharemetal":
		b.shareToggle(session.HumanShareMetal, "Metal", !me.ShareMetal)
	case "shareenergy":
		b.shareToggle(session.HumanShareEnergy, "Energy", !me.ShareEnergy)
	case "sharemapping":
		b.shareToggle(session.HumanShareMapping, "Mapping", !me.ShareMapping)
	case "shareradar":
		b.shareToggle(session.HumanShareRadar, "Radar", !me.ShareRadar)
	case "shareall":
		// The four toggles in sequence, each flipping its own option
		// [07 R-CAM-01 §6]; one command carries all four new values.
		all := session.HumanShareAllCommand{Metal: !me.ShareMetal, Energy: !me.ShareEnergy, Mapping: !me.ShareMapping, Radar: !me.ShareRadar}
		if b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanShareAll, ShareAll: all}) == nil {
			b.postShareToggle("Metal", all.Metal)
			b.postShareToggle("Energy", all.Energy)
			b.postShareToggle("Mapping", all.Mapping)
			b.postShareToggle("Radar", all.Radar)
		}
	case "setsharemetal", "setshareenergy":
		// The typed integer; the receiver stores it, or the storage capacity
		// when it is above that. The reply, `OK.  Will share metal if above
		// %d` or its energy twin, names the parsed integer
		// [07 R-CAM-01 §6][05 R-SHARE-01 §3].
		amount := int32(localCommandInt(words, 1))
		kind, resource := session.HumanSetShareMetal, "metal"
		if name == "setshareenergy" {
			kind, resource = session.HumanSetShareEnergy, "energy"
		}
		if b.enqueueHumanCommand(session.HumanCommand{Kind: kind, ShareThreshold: session.HumanShareThresholdCommand{Amount: amount}}) == nil {
			b.postShareReply(fmt.Sprintf("OK.  Will share %s if above %d", resource, amount))
		}
	case "give":
		// `Give p n metal|energy` from the issuing seat to slot p, the typed
		// integer as single precision, word 3 compared case-insensitively
		// [07 R-CAM-01 §6]. Online a positive amount within the producer's
		// range is ordinary sharing; the receiver refuses any other amount
		// unless the room allows cheats (§7.1 Q8).
		if len(words) < 4 {
			return true
		}
		var resource economy.Res
		switch strings.ToLower(words[3]) {
		case "metal":
			resource = economy.Metal
		case "energy":
			resource = economy.Energy
		default:
			return true
		}
		_ = b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanGive, Give: session.HumanGiveCommand{
			Player: int(uint8(localCommandInt(words, 1))), Resource: resource, Amount: float32(localCommandInt(words, 2)),
		}})
	default:
		return false
	}
	return true
}

// shareToggle sends one share toggle with the option's new value and posts
// its status line.
func (b *battleSession) shareToggle(kind session.HumanCommandKind, option string, on bool) {
	if b.enqueueHumanCommand(session.HumanCommand{Kind: kind, ShareOption: session.HumanShareOptionCommand{On: on}}) == nil {
		b.postShareToggle(option, on)
	}
}

// postShareToggle posts `Toggled Share<option> to: ON` or `OFF`
// [07 R-CAM-01 §6].
func (b *battleSession) postShareToggle(option string, on bool) {
	state := "OFF"
	if on {
		state = "ON"
	}
	b.postShareReply("Toggled Share" + option + " to: " + state)
}

// postShareReply posts a share command's reply to the message column with
// retail's routing class 2, source unit 0 and speaker sentinel 10: no logo,
// no `MessageArrived`, and hidden when `screenchat` is 0
// [07 R-CAM-01 §6][07 R-HUD-03 §14.3].
func (b *battleSession) postShareReply(text string) {
	if ring := b.messageRing(); ring != nil {
		ring.Append(text, 2, 0, 10, b.currentTick())
	}
}
