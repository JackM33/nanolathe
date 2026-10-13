//go:build retail

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
)

// Optional captures use a real battle, the authored fonts and the production
// HUD. The benchmark supplies ordinary fighting units and queued construction;
// the report contains their observed ledger writes, never invented values.
func TestRetailEconomyPanelCapture(t *testing.T) {
	dir := os.Getenv("NANOLATHE_ECONOMY_CAPTURE_DIR")
	if dir == "" {
		t.Skip("NANOLATHE_ECONOMY_CAPTURE_DIR is unset")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	opts, err := parseFlags([]string{"--battle-benchmark=/tmp/unused-economy", "--root=" + testsupport.RetailRoot(t)}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := openContent(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	sess, cat, err := newBattleSession(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	scene, err := stageCoastalBenchmark(opts, sess)
	if err != nil {
		t.Fatal(err)
	}
	for tick := int32(1); tick <= 300; tick++ {
		sess.Step(tick)
	}
	cl, err := client.New(client.Options{Buffer: sess.Snapshot, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetModelFS(cs.unmappedMount)
	b, err := composeBattleEntry(sess, cat, cs, cl, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.teardown(cl)
	cl.SetEnhanced(true)
	b.chromeFixed = true // the software capture executor has no affine regions
	b.cam.JumpToBattleViewCenter(scene.X, scene.Z)
	for _, resource := range []economy.Res{economy.Energy, economy.Metal} {
		b.economyPanel = economyPanelState{open: true, resource: resource, viewer: sess.LocalOwner}
		cl.BeginPresentationFrame()
		shareShotPNG(t, filepath.Join(dir, fmt.Sprintf("economy-%d.png", resource)), cl.ComposeFrame())
	}
	if err := sess.EnqueueHumanCommand(session.HumanCommand{Kind: session.HumanEconomyActivity,
		EconomyActivity: session.HumanEconomyActivityCommand{Activity: economy.ActivityConstruction, Pause: true}}); err != nil {
		t.Fatal(err)
	}
	sess.Step(301)
	cl.SetHostStepDue(time.UnixMilli(1000))
	b.economyPanel = economyPanelState{open: true, resource: economy.Energy, viewer: sess.LocalOwner}
	cl.BeginPresentationFrame()
	shareShotPNG(t, filepath.Join(dir, "economy-paused.png"), cl.ComposeFrame())
	b.economyPanel.open = false
	cl.BeginPresentationFrame()
	shareShotPNG(t, filepath.Join(dir, "economy-reminder.png"), cl.ComposeFrame())
	cl.SetHostStepDue(time.UnixMilli(1500))
	cl.BeginPresentationFrame()
	shareShotPNG(t, filepath.Join(dir, "economy-reminder-dim.png"), cl.ComposeFrame())
}
