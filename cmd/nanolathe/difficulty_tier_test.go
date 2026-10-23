package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// A selected save's label follows its recorded entry rules, including a
// switch since entry, a later player override and a missing current rule set.
// The raw bank index and the no-sidecar retail fallback remain unchanged.
func TestSaveDifficultyUsesRecordedRules(t *testing.T) {
	on, off := true, false
	for _, tc := range []struct {
		name, rules, base string
		entry             bool
		player            *bool
		reverse           bool
	}{
		{"Community", string(gameplay.Community39), string(gameplay.Community39), true, nil, true},
		{"Modern", string(gameplay.Modern), string(gameplay.Modern), true, nil, true},
		{"Strict entry", string(gameplay.Strict31), string(gameplay.Strict31), false, nil, false},
		{"switched to Strict", string(gameplay.Strict31), string(gameplay.Strict31), true, nil, true},
		{"switched from Strict", string(gameplay.Community39), string(gameplay.Community39), false, nil, false},
		{"later player override", string(gameplay.Community39), string(gameplay.Community39), true, &off, true},
		{"unknown current set keeps entry", "unavailable-set", string(gameplay.Strict31), true, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "saved.SAV")
			sc := save.Sidecar{Rules: tc.rules, Gameplay: tc.base, Community: save.SidecarCommunity{
				Entry: community.Features{AIDifficultyReversed: tc.entry},
				Sources: save.SidecarCommunitySources{
					Content: []community.Overrides{{AIDifficultyReversed: &on}},
					Player:  community.Overrides{AIDifficultyReversed: tc.player},
				},
			}}
			if err := save.WriteSidecar(path, sc); err != nil {
				t.Fatal(err)
			}
			for index := int32(0); index < 3; index++ {
				want := index
				if tc.reverse {
					want = 2 - index
				}
				if got := saveSummaryDifficulty(path, index); got != want {
					t.Fatalf("index %d displayed as tier %d, want %d", index, got, want)
				}
			}
			if err := save.RemoveSidecar(path); err != nil {
				t.Fatal(err)
			}
			if got := saveSummaryDifficulty(path, 0); got != 0 {
				t.Fatalf("no-sidecar index 0 displayed as tier %d", got)
			}
			if err := os.WriteFile(save.SidecarPath(path), []byte("{broken"), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := retailSummaryPanelFields(save.Summary{Difficulty: saveSummaryDifficulty(path, 0)}, true, nil)["DIFF"]; got != "" {
				t.Fatalf("unreadable sidecar displayed guessed difficulty %q", got)
			}
		})
	}
}

// Selecting another file refreshes the production dialog from that file's
// sidecar, even while the shell and the mounted content select other rules.
func TestSaveDialogDifficultyFollowsSelection(t *testing.T) {
	resetSaveLoadScreenState(t)
	g, dir := retailShellForTest(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	on := true
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Community39} {
		path := session.RetailSavePath(dir, string(mode))
		summary := save.Summary{Description: string(mode), Gametype: 2, Players: 2, Difficulty: 0, BetweenMissions: 1}
		if err := session.WriteRetailContinuationSave(path, summary); err != nil {
			t.Fatal(err)
		}
		sc := save.Sidecar{Rules: string(mode), Gameplay: string(mode), Community: save.SidecarCommunity{
			Entry:   community.Features{AIDifficultyReversed: mode != gameplay.Strict31},
			Sources: save.SidecarCommunitySources{Content: []community.Overrides{{AIDifficultyReversed: &on}}},
		}}
		if err := save.WriteSidecar(path, sc); err != nil {
			t.Fatal(err)
		}
	}
	g.cs.gameplayFeatures = []community.Overrides{{AIDifficultyReversed: &on}}
	if err := g.openSaveLoadScreen(loadScreenMode, saveLoadFromFrontend); err != nil {
		t.Fatal(err)
	}
	defer g.closeSaveLoadScreen()
	var capture *client.Client
	if os.Getenv("NANOLATHE_SAVE_UI_CAPTURE_DIR") != "" {
		var err error
		capture, err = client.New(client.Options{Buffer: &frame.Buffer{}, Width: 640, Height: 480})
		if err != nil {
			t.Fatal(err)
		}
		if err := bindShellContent(g, capture); err != nil {
			t.Fatal(err)
		}
	}
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Community39} {
		g.gameplay = mode
		for i := range saveLoadUI.entries {
			saveLoadUI.Select(i)
			g.refreshSaveLoadPanel()
			entry, _ := saveLoadUI.SelectedEntry()
			want := "Easy"
			if entry.Summary.Description == string(gameplay.Community39) {
				want = "Hard"
			}
			if got := saveLoadPanel.TextOf("DIFF"); got != want || entry.Summary.Difficulty != 0 {
				t.Fatalf("shell %s, save %s: label %q, stored index %d", mode, entry.Summary.Description, got, entry.Summary.Difficulty)
			}
			if capture != nil {
				captureSaveLoadUI(t, capture, "difficulty-"+entry.Summary.Description+"-shell-"+string(mode))
			}
		}
	}
}

// The actual lobby row reads the room's effective feature table even when
// this seat's shell still selects another rule set. Game Options reads the
// running session's table, and neither display rewrites the stored index.
func TestDifficultyLabelsUseTheOwningBattleAndRoom(t *testing.T) {
	useOnlineSessionSeams(t, 4)
	g := onlineTestShell(t)
	g.setup.Difficulty = 0
	g.gameplay = gameplay.Strict31
	fake := newFakeOnlineLobby(0, 0, 1)
	openTestLobby(t, g, fake, onlineTestCatalog())
	g.activateGadget("Player2")
	g.pollOnline()
	r := &g.online.room
	for _, reverse := range []bool{true, false} {
		r.baseReq.Community.AIDifficultyReversed = reverse
		g.refreshOnlineComputerRow(2, 0, 2, []uint8{0, 1, 2})
		want := "Easy"
		if reverse {
			want = "Hard"
		}
		if got := lobbyText(g, lobbyLevel+"2"); got != want || r.settings.computers[0].Difficulty != 0 {
			t.Fatalf("room reverse=%t: label %q, stored index %d", reverse, got, r.settings.computers[0].Difficulty)
		}
		sess := &session.Session{EntryCommunity: community.Features{AIDifficultyReversed: reverse}, Community: community.Features{AIDifficultyReversed: !reverse}}
		sess.Skirmish.Difficulty = 0
		if got := battleGameOptionsRows(&battleSession{sess: sess})[4].Value; got != want || sess.Skirmish.Difficulty != 0 {
			t.Fatalf("battle reverse=%t: label %q, stored index %d", reverse, got, sess.Skirmish.Difficulty)
		}
	}
}
