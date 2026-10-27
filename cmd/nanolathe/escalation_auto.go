package main

// A desktop TA folder with TA: Escalation copied over it plays Escalation
// (user-requested 2026-10-27). The copied files themselves stay left out,
// because mounted with the base game they blank the build buttons
// (baseGameRoot, docs/ESCALATION_SUPPORT.md): instead the game switches, once,
// to Nanolathe's Escalation package, first installing it from the mod
// catalogue when the library does not hold it, as the Android launcher does.
// The main menu's status line shows the download. The switch is recorded in
// the settings (escalationAuto), so a player who later picks plain Total
// Annihilation keeps it.

import (
	"context"
	"fmt"
	"runtime"
	"sync"

	"github.com/nanolathe-gg/nanolathe/internal/modfetch"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// escalationModID is the catalogue's and the library's Escalation.
const escalationModID = "escalation"

// escalationAutoDone is the settings value that records the switch.
const escalationAutoDone = 1

// escalationAuto is the process's one attempt: the catalogue read runs on a
// worker, and everything else on the render thread. It outlives a content
// reload, which replaces the shell.
var escalationAuto struct {
	mu       sync.Mutex
	fetching bool
	fetched  bool
	entry    modfetch.Entry
	err      error

	downloading bool
	failed      string // why this run gave up, shown on the main menu
	status      string // the last status line set
}

// escalationAutoWanted reports a desktop main menu over a TA folder holding
// a copied Escalation, with no mod mounted and the switch not yet made.
func (g *gameShell) escalationAutoWanted() bool {
	if g == nil || !g.settingsWritable || runtime.GOOS == "android" || runtime.GOOS == "js" || g.escalationAuto >= escalationAutoDone {
		return false
	}
	if g.cs == nil || !g.cs.plainBase || g.cs.baseNotice == "" || g.cs.mod != nil || g.cs.manualRoots {
		return false
	}
	return g.frontend != nil && g.frontend.Mode == modeMenuMain && g.battle == nil && g.intro == nil && pendingContentReload == nil
}

// serviceEscalationAuto advances the switch once per update.
func (g *gameShell) serviceEscalationAuto() {
	if !g.escalationAutoWanted() {
		return
	}
	a := &escalationAuto
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failed != "" {
		g.escalationAutoStatus(a.failed)
		return
	}
	// A running download holds its mod unmounted until the job ends, even
	// once the library has published it, so nothing switches before then.
	job := modDownload.view()
	if job.running {
		switch {
		case !a.downloading:
			// Another download; wait for it.
		case job.installing:
			g.escalationAutoStatus("Installing TA: Escalation...")
		default:
			text := "Downloading TA: Escalation..."
			if job.total > 0 {
				text = fmt.Sprintf("Downloading TA: Escalation: %d%% (%.0f / %.0f MB)", job.done*100/job.total, float64(job.done)/(1<<20), float64(job.total)/(1<<20))
			}
			g.escalationAutoStatus(text)
		}
		return
	}
	lib, err := openModLibrary()
	if err != nil {
		a.failed = "Could not open the mod library: " + noticeReason(err)
		return
	}
	if mod, ok := installedEscalation(lib); ok {
		g.switchToEscalation(mod)
		return
	}
	if a.downloading {
		// The job ended without an install.
		a.failed = "Could not get TA: Escalation. Try NANOLATHE, Content, Get more mods."
		if job.outcome != "" {
			a.failed = "Could not get TA: Escalation: " + job.outcome
		}
		return
	}
	switch {
	case !a.fetching && !a.fetched:
		a.fetching = true
		g.escalationAutoStatus("Your TA folder has TA: Escalation. Getting it ready...")
		go fetchEscalationEntry(lib.Root)
	case a.fetched && a.err != nil:
		a.failed = "Could not reach the mod catalogue for TA: Escalation: " + noticeReason(a.err)
	case a.fetched:
		if err := g.startCatalogueDownload(lib, a.entry); err != nil {
			a.failed = "Could not get TA: Escalation: " + noticeReason(err)
			return
		}
		a.downloading = true
		g.escalationAutoStatus("Downloading TA: Escalation...")
	}
}

// fetchEscalationEntry reads the catalogue's Escalation entry on a worker.
func fetchEscalationEntry(cacheDir string) {
	client := &modfetch.Client{CatalogURL: modfetch.CatalogURL(), CacheDir: cacheDir}
	fetched, err := client.FetchManifest(context.Background())
	var entry modfetch.Entry
	if err == nil {
		for _, e := range fetched.Manifest.Mods {
			if e.ID == escalationModID {
				entry = e
			}
		}
		if entry.ID == "" {
			err = &missingProductError{what: "the mod catalogue has no TA: Escalation", logical: escalationModID, providers: []string{modfetch.CatalogURL()}, expected: "a catalogue entry"}
		}
	}
	a := &escalationAuto
	a.mu.Lock()
	a.fetching, a.fetched, a.entry, a.err = false, true, entry, err
	a.mu.Unlock()
}

// installedEscalation is an Escalation the library holds: the last version
// in its listing, which is ordered by name.
func installedEscalation(lib *modlibrary.Library) (modlibrary.Mod, bool) {
	mods, err := lib.Installed()
	if err != nil {
		return modlibrary.Mod{}, false
	}
	var found modlibrary.Mod
	ok := false
	for _, m := range mods {
		if m.ID == escalationModID {
			found, ok = m, true
		}
	}
	return found, ok
}

// switchToEscalation asks the window loop to mount mod, recording the switch
// so it is made only once.
func (g *gameShell) switchToEscalation(mod modlibrary.Mod) {
	g.escalationAuto = escalationAutoDone
	g.escalationAutoStatus("Starting TA: Escalation...")
	pendingContentReload = &contentReloadRequest{
		selector: modSelectorOf(mod.ID, mod.Version),
		mod:      settings.ModSelection{ID: mod.ID, Version: mod.Version},
		mutators: g.opts.Mutators,
	}
}

// escalationAutoStatus puts text on the main menu's status line.
func (g *gameShell) escalationAutoStatus(text string) {
	if escalationAuto.status == text && g.cs.modNotice == text {
		return
	}
	escalationAuto.status = text
	g.cs.modNotice = text
	if p := g.activePanel(); p != nil {
		g.refreshMainMenuModStatus(p)
	}
}
