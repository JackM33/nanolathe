package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// Each row's chosen start persists in the settings file as `start`, omitted
// for automatic, and travels with its row through the start conversion,
// which compacts the open rows out (DESIGN_INTERFACE_HUD_INPUT §2.6 "Start
// positions").
func TestTheSettingsFileKeepsEachRowsStart(t *testing.T) {
	g := &gameShell{setup: newSkirmishMenuConfig("m")}
	blob := settings.Defaults()
	blob.Skirmish.NumPlayers = 3
	blob.Skirmish.Players[0] = settings.Player{Controller: 1, AllyGroup: 5, Metal: 1000, Energy: 1000, Start: 3}
	blob.Skirmish.Players[1] = settings.Player{Controller: 0, AllyGroup: 5, Metal: 1000, Energy: 1000, Start: 1}
	blob.Skirmish.Players[2] = settings.Player{Controller: 2, Side: 1, Color: 2, AllyGroup: 5, Metal: 1000, Energy: 1000}
	g.applySettings(blob)
	back := g.captureSettings()
	for i, want := range []int{3, 1, 0} {
		if got := back.Skirmish.Players[i].Start; got != want {
			t.Fatalf("captured row %d start %d, want %d", i, got, want)
		}
	}
	if data, _ := json.Marshal(back.Skirmish.Players[2]); strings.Contains(string(data), `"start"`) {
		t.Fatalf("an automatic row wrote %s", data)
	}
	cfg := g.skirmishConfigForStart("m")
	if cfg.NumPlayers != 2 || cfg.Players[0].Start != 3 || cfg.Players[1].Start != 0 {
		t.Fatalf("start conversion: %d players, starts %d %d; want the open row's choice dropped with it", cfg.NumPlayers, cfg.Players[0].Start, cfg.Players[1].Start)
	}
	bad := settings.Skirmish{Players: []settings.Player{{Start: session.SkirmishMaxStart + 1}, {Start: -1}}}
	bad.Normalize()
	if bad.Players[0].Start != 0 || bad.Players[1].Start != 0 {
		t.Fatalf("out-of-range starts normalized to %d, %d; want automatic", bad.Players[0].Start, bad.Players[1].Start)
	}
}
