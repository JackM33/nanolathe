package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// The desktop switches to Escalation only from the main menu of a plain base
// install that left a copied Escalation out, with no mod mounted, once.
func TestEscalationAutoWantsOnlyACopiedEscalationOnce(t *testing.T) {
	shell := func() *gameShell {
		return &gameShell{settingsWritable: true, frontend: ui.NewFrontend(modeMenuMain), cs: &contentSet{plainBase: true, baseNotice: "left out"}}
	}
	if !shell().escalationAutoWanted() {
		t.Fatal("a copied Escalation was not switched to")
	}
	for name, change := range map[string]func(*gameShell){
		"done":         func(g *gameShell) { g.escalationAuto = escalationAutoDone },
		"no copy":      func(g *gameShell) { g.cs.baseNotice = "" },
		"mod mounted":  func(g *gameShell) { g.cs.mod = &modlibrary.Mod{ID: "prota"} },
		"manual roots": func(g *gameShell) { g.cs.manualRoots = true },
		"not the menu": func(g *gameShell) { g.frontend = ui.NewFrontend(modeMenuMap) },
		"no settings":  func(g *gameShell) { g.settingsWritable = false },
	} {
		g := shell()
		change(g)
		if g.escalationAutoWanted() {
			t.Fatalf("%s: switched anyway", name)
		}
	}
}
