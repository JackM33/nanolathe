package main

import (
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

const talkCommandHistoryLimit = 64

type battleChatState struct {
	active      bool
	ownsFrame   bool
	lastCommand string
	// mode is the persistent recipient-mode byte an online line is sent by
	// (battle_online_chat.go); nothing in this build changes it from all.
	mode chatRecipientMode

	// Battle-local host input policy; see DESIGN_INTERFACE_HUD_INPUT §3.9.
	commandHistory   []string
	historyIndex     int
	historyDraft     string
	historyRecalling bool
}

func talkOpenToken(in *input.State) bool {
	if in == nil {
		return false
	}
	tokens := in.PeekTokens()
	return len(tokens) != 0 && tokens[0].Kind == input.TokenEdit && tokens[0].Key == input.KeyEnter
}

func (b *battleSession) openTalk(in *input.State, cl *client.Client) bool {
	if b == nil || b.hud == nil || b.chat.active || battleSessionKind(b) == 3 {
		return false
	}
	// The opener returns at once for a watcher, so a watcher can neither chat
	// nor type a + line [07 §5 "Chat"]. Online, a seat whose result is final
	// is defeated and watching, so it counts as one.
	if b.localChatWatcher() {
		return false
	}
	// TODO(question): online this opens the single-player TALK.GUI form with
	// SENDTO hidden. Retail's multiplayer opener configures SENDTO instead of
	// hiding it, and opens the expanded TALK2.GUI form, with SENDTYPE and the
	// recipient rows, when a persistent expansion-flag byte has bit 0x01 set
	// [07 §5 "Chat"]. Unknown: what sets that flag, how the recipient rows are
	// painted, what SENDTO's word-flag bit 0x0100 does, and the recipient-mode
	// byte's initial value. A trace of the flag's writers, the row painter,
	// the bit's readers and the mode byte's initialisation would settle them.
	b.hud.openTalkWindow()
	p := b.hud.talkPanel
	if p == nil {
		return false
	}
	index := p.Index("TALK")
	if index < 0 {
		return false
	}
	// Opening a generic window flushes the old producer ring before the new
	// focused editor receives input [07 R-WGT-01 §1][07 §5 "Chat"].
	if in != nil {
		in.DiscardTokens(in.PendingTokens())
	}
	p.SetTextAt(index, "")
	if !p.FocusEditor(index) {
		return false
	}
	b.chat.active, b.chat.ownsFrame = true, true
	b.chat.resetHistoryRecall()
	if b.dragScrollActive {
		b.endDragScroll(cl)
	}
	b.playUICue(cl, "SmallButton")
	return true
}

func (b *battleSession) closeTalk() {
	if b == nil || b.hud == nil || b.hud.talkPanel == nil {
		return
	}
	b.hud.talkPanel.SetText("TALK", "")
	b.hud.talkPanel.ResetPress()
	b.chat.active = false
	b.chat.resetHistoryRecall()
	b.developer.quickkeysDisabled = false
}

func (b *battleSession) talkMeasure(index int, text string) int {
	if b == nil || b.hud == nil || b.hud.talkWin == nil || index < 0 || index >= len(b.hud.talkWin.Gadgets) {
		return len(text)
	}
	gad := b.hud.talkWin.Gadgets[index]
	if b.hud.modalFont != nil {
		return retailGAFTextWidth(b.hud.modalFont, text)
	}
	font := b.hud.talkWin.Font(b.hud.fs, gad.FontNumber)
	if font == nil {
		font = b.hud.guiFont
	}
	if font == nil {
		return len(text)
	}
	return client.MeasureText(font, text)
}

func (b *battleSession) serviceTalk(in *input.State) {
	if b == nil || b.hud == nil || !b.chat.active || b.hud.talkPanel == nil || in == nil {
		return
	}
	b.chat.ownsFrame = true
	// Enter commits and Escape empties then fires, so both close the line
	// [07 R-WGT-01 §6]; keypad Enter reaches here as Enter (ebitenapp).
	frame := pointerFrame(in, in.PeekTokens(), false)
	// A right-button press anywhere cancels the line exactly as Escape does:
	// the editor is served one Escape in place of this frame's records, so it
	// empties the text, frees its capture and fires, and nothing is committed.
	// Supported inference from manual observation of retail by a long-time
	// ProTA maintainer, not a trace [07 §5 "Chat"]. The press is consumed with
	// the rest of this frame, so it neither cancels an armed order nor clears
	// the selection.
	cancel := talkRightPress(frame)
	if cancel {
		frame.Tokens = []input.Token{{Kind: input.TokenEdit, Key: input.KeyEscape}}
		frame.PointerEvents = nil
	}
	frame.DisableQuickKeys = b.developer.quickkeysDisabled
	result := b.serviceTalkHistory(frame)
	if result.Fired && result.FiredIndex == b.hud.talkPanel.Index("TALK") {
		text := b.hud.talkPanel.TextOf("TALK")
		if text != "" && !cancel {
			b.commitLocalChat(text)
		}
		b.closeTalk()
	} else if cancel {
		// The editor had already lost its capture, so the Escape did not
		// reach it; the cancel still closes the line without committing.
		b.closeTalk()
	}
	// The dialog owns the complete input frame, including records after its
	// closing Enter/Escape. None may reach a battle child opened later.
	in.DiscardTokens(in.PendingTokens())
}

// serviceTalkHistory intercepts history arrows only in the captured TALK
// editor. Each prefix ends with its arrow, so an earlier Enter stays inert as
// it does in the ordinary ordered editor batch [07 R-WGT-01 §6]. Pointer
// service runs once, after the remaining editor tokens, through the same panel.
func (b *battleSession) serviceTalkHistory(frame ui.WidgetFrame) ui.ServiceResult {
	p := b.hud.talkPanel
	index := p.Index("TALK")
	measure := func(text string) int { return b.talkMeasure(index, text) }
	for len(b.chat.commandHistory) != 0 && p.EditorIndex() == index && p.EditorCaptured() {
		arrow := -1
		for i, token := range frame.Tokens {
			if token.Kind == input.TokenEdit && (token.Key == input.KeyUp || token.Key == input.KeyDown) {
				arrow = i
				break
			}
		}
		if arrow < 0 {
			break
		}
		result := p.ApplyEditorTokens(frame.Tokens[:arrow+1], measure)
		if result.Action.Kind == ui.ActionActivate {
			return ui.ServiceResult{Fired: true, FiredIndex: result.Action.Index}
		}
		b.recallTalkCommand(index, frame.Tokens[arrow].Key, measure)
		frame.Tokens = frame.Tokens[arrow+1:]
	}
	return p.ServiceFrame(frame, ui.WidgetHooks{Measure: b.talkMeasure})
}

func (c *battleChatState) resetHistoryRecall() {
	c.historyIndex = len(c.commandHistory)
	c.historyDraft = ""
	c.historyRecalling = false
}

func (c *battleChatState) rememberCommand(text string) {
	command := strings.TrimLeft(text, " ")
	if command == "" || command[0] != '+' {
		return
	}
	if len(c.commandHistory) != 0 && c.commandHistory[len(c.commandHistory)-1] == text {
		return
	}
	if len(c.commandHistory) == talkCommandHistoryLimit {
		copy(c.commandHistory, c.commandHistory[1:])
		c.commandHistory[len(c.commandHistory)-1] = text
	} else {
		c.commandHistory = append(c.commandHistory, text)
	}
}

func (b *battleSession) recallTalkCommand(index int, key input.Key, measure func(string) int) {
	c, p := &b.chat, b.hud.talkPanel
	if key == input.KeyUp {
		if !c.historyRecalling {
			c.historyDraft = p.TextAt(index)
			c.historyIndex = len(c.commandHistory)
			c.historyRecalling = true
		}
		if c.historyIndex == 0 {
			return
		}
		c.historyIndex--
	} else {
		if !c.historyRecalling {
			return
		}
		c.historyIndex++
		if c.historyIndex == len(c.commandHistory) {
			// The draft already passed the editor's admission (including paste).
			// Restore it exactly, with the caret at the end, and end recall.
			p.SetTextAt(index, c.historyDraft)
			c.resetHistoryRecall()
			return
		}
	}
	// Reuse ordinary typing admission for the current authored byte limit and
	// font width [07 R-WGT-01 §6][07 R-WGT-01 §12], then leave the caret at
	// the end. This host policy does not extend the editor or execute the line.
	p.SetTextAt(index, "")
	tokens := make([]input.Token, 0, len(c.commandHistory[c.historyIndex]))
	for _, r := range c.commandHistory[c.historyIndex] {
		tokens = append(tokens, input.Token{Kind: input.TokenText, Rune: r})
	}
	p.ApplyEditorTokens(tokens, measure)
}

// talkRightPress reports a right-button down, single or double, in the frame
// the TALK dialog owns.
func talkRightPress(frame ui.WidgetFrame) bool {
	for _, event := range frame.PointerEvents {
		if event.Kind == input.RightDown || event.Kind == input.RightDoubleClick {
			return true
		}
	}
	return false
}

// commitLocalChat runs a committed TALK line: a + line is dispatched on this
// machine first, then every line, + lines included, goes through the chat
// sender, to everyone when the dispatched entry was a cheat
// [07 R-CAM-01 §6][07 §5 "Chat"].
func (b *battleSession) commitLocalChat(text string) {
	if b == nil || b.sess == nil || b.sess.Econ == nil || int(b.sess.LocalOwner) >= len(b.sess.Econ.Players) {
		return
	}
	b.chat.rememberCommand(text)
	cheat := b.dispatchLocalCommand(text)
	_ = b.sendChat(text, true, cheat)
}

// postLocalChat is the chat composer's local post: the registered local
// player name without a fallback, `<name> text`, routing class 4, source unit
// 0 and the no-speaker sentinel, so the line draws no logo and plays no
// MessageArrived cue [07 §5 "Chat"][07 R-FE-02 §12][07 R-HUD-03 §14.3].
func (b *battleSession) postLocalChat(text string) {
	if b == nil || b.sess == nil || b.sess.Econ == nil || int(b.sess.LocalOwner) >= len(b.sess.Econ.Players) {
		return
	}
	name := b.sess.Econ.Players[b.sess.LocalOwner].Name
	if ring := b.messageRing(); ring != nil {
		ring.Append("<"+name+"> "+text, chatClassLocal, pool.Handle(0), chatNoSpeaker, b.currentTick())
	}
}

// talkOwnedInput preserves pointer position for the presentation pass while
// removing every world-command edge, held button and keyboard state. The
// controller still advances the simulation budget on the frame [07 §3].
func talkOwnedInput(in *input.State, delta float64) input.Sample {
	sample := input.Sample{Elapsed: delta}
	if in != nil && in.Mouse != nil {
		sample.MouseX, sample.MouseY = int32(in.Mouse.X), int32(in.Mouse.Y)
	}
	return sample
}

func (h *retailBattleHUD) drawTalk(c *client.Client, b *battleSession) {
	if h == nil || b == nil || !b.chat.active || h.talkPanel == nil {
		return
	}
	h.drawGUIWindowState(c, h.talkWin, nil, "", h.talkPanel, nil)
}
