package main

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
)

// battleWindowContext carries the startup-selected caption table and the
// process-lifetime builder state into battle entry, including direct --map and
// screenshot entry where no frontend shell exists [07 R-WGT-01 §3][§11].
type battleWindowContext struct {
	content          *contentSet
	shell            *gameShell
	preclearDisabled *bool
}

func newBattleWindowContext(content *contentSet, shell *gameShell) *battleWindowContext {
	if shell != nil {
		return &battleWindowContext{content: content, shell: shell, preclearDisabled: &shell.quickKeyPreclearDisabled}
	}
	disabled := false
	return &battleWindowContext{content: content, preclearDisabled: &disabled}
}

func (c *battleWindowContext) captions() gui.CaptionTranslator {
	if c != nil && c.content != nil {
		return c.content.translations
	}
	return nil
}

func (c *battleWindowContext) completeTransition() {
	if c != nil && c.preclearDisabled != nil {
		*c.preclearDisabled = true
	}
}

func (c *battleWindowContext) install(window *gui.Window, page, common *formats.GAF) {
	if c == nil || window == nil {
		return
	}
	if c.shell != nil {
		// A faction side's support bank (factionSupportGAF) stands in for the
		// shell's shared one while its windows are built.
		saved := c.shell.battleCommon
		c.shell.battleCommon = common
		c.shell.installRetailWindowButtonArt(window, page)
		c.shell.battleCommon = saved
		return
	}
	// Direct battle entry has the same content and startup state but no menu
	// shell. Supply the builder's resource context without manufacturing a
	// frontend or bypassing its process preclear [07 R-WGT-01 §3].
	disabled := false
	if c.preclearDisabled != nil {
		disabled = *c.preclearDisabled
	}
	(&gameShell{
		cs:                       c.content,
		assets:                   &menuAssets{common: common},
		quickKeyPreclearDisabled: disabled,
	}).installRetailWindowButtonArt(window, page)
}
