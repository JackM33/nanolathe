package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// spawnChatCommand captures the submission frame's pointer, before TALK's
// input ownership or a later camera movement can change the requested point.
// Nanolathe Modern policy: DESIGN_INTERFACE_HUD_INPUT "Modern spawn command".
func (b *battleSession) spawnChatCommand(words []string) {
	say := func(text string) {
		if ring := b.messageRing(); ring != nil {
			ring.Append(text, 4, 0, 10, b.currentTick())
		}
	}
	if b.sess == nil {
		return
	}
	if b.sess.Gameplay.Normalize() != gameplay.Modern {
		say("+spawn requires Modern gameplay")
		return
	}
	if len(words) != 2 {
		say("Usage: +spawn <unit>, for example +spawn armck")
		return
	}
	b.spawnAtPointer(words[1], "Point at the battlefield before submitting +spawn")
}

// unitNameChatCommand keeps the Modern exact-name shorthand when developer
// access is off (DESIGN_INTERFACE_HUD_INPUT "Modern spawn command"). With
// access, the retail default handler owns patterns and optional player slots
// in both gameplay modes [07 R-CAM-01 §6][07 R-CAM-01 §9].
func (b *battleSession) unitNameChatCommand(words []string) bool {
	if b == nil || b.sess == nil || b.sess.Catalog == nil || len(words) == 0 {
		return false
	}
	if b.developer.authorized {
		return b.developerSpawnChatCommand(words)
	}
	if len(words) != 1 {
		return false
	}
	if b.sess.Gameplay.Normalize() != gameplay.Modern {
		return false
	}
	if def, ok := b.sess.Catalog.Unit(words[0]); !ok || def == nil {
		return false
	}
	b.spawnAtPointer(words[0], "Point at the battlefield before submitting +"+words[0])
	return true
}

func (b *battleSession) spawnAtPointer(unit, offWorld string) {
	if b.cl == nil || b.cam == nil || b.cl.Input() == nil || b.cl.Input().Mouse == nil {
		return
	}
	mouse := b.cl.Input().Mouse
	x, y := int32(mouse.X), int32(mouse.Y)
	if !b.overWorld(x, y) {
		if ring := b.messageRing(); ring != nil {
			ring.Append(offWorld, 4, 0, 10, b.currentTick())
		}
		return
	}
	wx, wy, wz := b.cursorWorld(x, y)
	_ = b.enqueueLocalBattleCommand(session.HumanCommand{
		Kind:  session.HumanSpawn,
		Spawn: session.HumanSpawnCommand{Unit: unit, X: wx, Y: wy, Z: wz},
	})
}

// Capture access and the submission point before TALK closes. Only accepted
// local submissions become commands; online has no developer-spawn payload
// (DESIGN_DEVELOPER_TOOLS §8, DESIGN_MULTIPLAYER §7.4.2).
func (b *battleSession) developerSpawnChatCommand(words []string) bool {
	if b.sess.OnlineCommandContext() || b.cl == nil || b.cam == nil || b.cl.Input() == nil || b.cl.Input().Mouse == nil {
		return true
	}
	mouse := b.cl.Input().Mouse
	x, y := int32(mouse.X), int32(mouse.Y)
	if !b.overWorld(x, y) {
		return true
	}
	wx, wy, wz := b.cursorWorld(x, y)
	_ = b.enqueueLocalBattleCommand(session.HumanCommand{
		Kind: session.HumanDeveloperSpawn,
		DeveloperSpawn: session.HumanDeveloperSpawnCommand{
			Pattern: content.CanonicalKey(words[0]), Owner: uint8(localCommandInt(words, 1)),
			X: wx, Y: wy, Z: wz,
		},
	})
	return true
}
