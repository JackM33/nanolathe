package main

// Fetching a room's content (docs/DESIGN_MULTIPLAYER.md §16.8, Q14): a seat
// that lacks the room's mod or map package downloads it from its verified
// catalogue without asking, through the same download jobs the Mods and More
// maps dialogs use, and joins or resolves the room again once it is
// installed.

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/maplibrary"
	"github.com/nanolathe-gg/nanolathe/internal/modfetch"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// onlineCatalogueTimeout bounds a catalogue lookup; the download itself is
// bounded by the download job's own stall handling.
const onlineCatalogueTimeout = 30 * time.Second

// catalogueArchiveIs reports whether a catalogue archive is the one a room
// names by its SHA-256.
func catalogueArchiveIs(a modfetch.Archive, digest [32]byte) bool {
	return strings.EqualFold(a.SHA256, fmt.Sprintf("%x", digest))
}

// roomPackageFetch is a lobby seat's download of a map package the room
// names and its install lacks: the catalogue lookup, then the shared map
// download job, one package at a time.
type roomPackageFetch struct {
	// want is the entry being fetched; its Name is "" when none is.
	want session.MatchContentEntry
	// catalogue answers the lookup once; nil after it is taken.
	catalogue chan roomCatalogueAnswer
	// found is the catalogue's entry and dependencies, waiting for the
	// shared download job when another download holds it.
	found        *modfetch.Entry
	dependencies []modfetch.Entry
	// started marks the download job begun, at install count installs. The
	// fetch ends once pollMapsFetch has remounted that install
	// (mapInstallsSeen), whichever poll sees it first.
	started  bool
	installs int
	// failed records why a package could not be fetched, by id and
	// version, so the lobby names the reason and does not try again for
	// this room.
	failed map[string]string
}

type roomCatalogueAnswer struct {
	manifest modfetch.Manifest
	err      error
}

func roomFetchKey(e session.MatchContentEntry) string { return e.Name + "@" + e.Version }

// roomEntryListed reports whether entries holds e.
func roomEntryListed(entries []session.MatchContentEntry, e session.MatchContentEntry) bool {
	for _, x := range entries {
		if x == e {
			return true
		}
	}
	return false
}

// startRoomPackageFetch begins fetching the first map package the room
// names and this seat lacks, unless a fetch is running or every missing
// package has failed already. A package installed with the listed identity
// that this install's mount left out is named with the mount's reason,
// since downloading it again would change nothing.
func (g *gameShell) startRoomPackageFetch() {
	r := &g.online.room
	f := &r.fetch
	if f.want.Name != "" {
		return
	}
	for _, e := range r.content.missing {
		if e.Kind != session.MatchContentMapPackage {
			continue
		}
		if _, failed := f.failed[roomFetchKey(e)]; failed {
			continue
		}
		if runtime.GOOS == "js" {
			f.fail(e, "the browser cannot download map packages")
			continue
		}
		if dir, ok := unmountedPackage(g.cs, installedMapPackages(), e); ok {
			reason := "it is installed but could not be mounted"
			if err := g.cs.mapSkipped(dir); err != nil {
				reason += ": " + noticeReason(err)
			}
			f.fail(e, reason)
			continue
		}
		root, err := maplibrary.DefaultRoot()
		if err != nil {
			f.fail(e, noticeReason(err))
			continue
		}
		client := &modfetch.Client{CatalogURL: modfetch.MapCatalogURL(), CacheDir: root}
		answer := make(chan roomCatalogueAnswer, 1)
		f.want, f.catalogue, f.found, f.started = e, answer, nil, false
		onlineWork.Add(1)
		go func() {
			defer onlineWork.Done()
			ctx, cancel := context.WithTimeout(context.Background(), onlineCatalogueTimeout)
			defer cancel()
			result, err := client.FetchManifest(ctx)
			answer <- roomCatalogueAnswer{manifest: result.Manifest, err: err}
		}()
		return
	}
}

// unmountedPackage is the directory of an installed package with e's
// identity that cs's mount did not accept.
func unmountedPackage(cs *contentSet, installed []modlibrary.Mod, e session.MatchContentEntry) (string, bool) {
	for _, m := range installed {
		if m.ID != e.Name || m.Version != e.Version || containsDir(cs.mapRoots, m.Dir) {
			continue
		}
		if got, err := roomPackageEntry(m); err == nil && got.Digest == e.Digest && got.Size == e.Size {
			return m.Dir, true
		}
	}
	return "", false
}

func (f *roomPackageFetch) fail(e session.MatchContentEntry, reason string) {
	if f.failed == nil {
		f.failed = make(map[string]string)
	}
	f.failed[roomFetchKey(e)] = reason
	f.clear()
}

func (f *roomPackageFetch) clear() {
	f.want, f.catalogue, f.found, f.dependencies, f.started = session.MatchContentEntry{}, nil, nil, nil, false
}

// pollRoomPackageFetch advances the fetch: it drops one the room no longer
// needs, takes the catalogue's answer, starts the download when the shared
// job is free, and ends once pollMapsFetch has installed and remounted it,
// when the room has already been resolved again on the new content
// (refreshOnlineRoomContent); a package that still does not resolve is named
// with its reason. It reports whether the lobby's status may have changed.
func (g *gameShell) pollRoomPackageFetch() bool {
	r := &g.online.room
	f := &r.fetch
	if f.want.Name == "" {
		return false
	}
	if !f.started && !roomEntryListed(r.content.missing, f.want) {
		f.clear() // the room changed map before the download began
		return true
	}
	if f.catalogue != nil {
		select {
		case answer := <-f.catalogue:
			f.catalogue = nil
			if answer.err != nil {
				f.fail(f.want, "the map catalogue is unavailable: "+noticeReason(answer.err))
				return true
			}
			// A shared feature package is a dependency entry, fetched on
			// its own like a map.
			for _, list := range [][]modfetch.Entry{answer.manifest.Maps, answer.manifest.Dependencies} {
				for i := range list {
					m := list[i]
					if f.found == nil && m.ID == f.want.Name && m.Version == f.want.Version && catalogueArchiveIs(m.Archive, f.want.Digest) {
						f.found, f.dependencies = &m, answer.manifest.Dependencies
					}
				}
			}
			if f.found == nil {
				f.fail(f.want, "it is not in the map catalogue")
				return true
			}
		default:
			return false
		}
	}
	if !f.started {
		if mapDownload.view().running {
			return false // another download holds the job; wait for it
		}
		root, err := maplibrary.DefaultRoot()
		if err == nil {
			lib, openErr := maplibrary.Open(root)
			if err = openErr; err == nil {
				f.installs = mapDownload.view().installs
				err = g.startMapDownload(lib, *f.found, f.dependencies)
			}
		}
		if err != nil {
			f.fail(f.want, noticeReason(err))
			return true
		}
		f.started = true
		return true
	}
	v := mapDownload.view()
	switch {
	case v.running:
		return true // the progress moved, or may have
	case v.installs == f.installs:
		reason := strings.TrimPrefix(v.outcome, "Failed: ")
		if reason == "" {
			reason = "the download stopped"
		}
		f.fail(f.want, reason)
		return true
	case mapInstallsSeen < v.installs:
		return false // pollMapsFetch remounts it on its next step
	}
	want := f.want
	f.clear()
	if roomEntryListed(r.content.missing, want) {
		reason := "it installed but could not be mounted"
		if dir, ok := unmountedPackage(g.cs, installedMapPackages(), want); ok {
			if err := g.cs.mapSkipped(dir); err != nil {
				reason += ": " + noticeReason(err)
			}
		}
		f.fail(want, reason)
	}
	return true
}

// roomFetchText is the lobby's line while a map package downloads.
func (g *gameShell) roomFetchText() string {
	f := &g.online.room.fetch
	if f.want.Name == "" {
		return ""
	}
	name := joinNames(roomContentNames([]session.MatchContentEntry{f.want}))
	v := mapDownload.view()
	if !f.started || !v.running || v.total <= 0 {
		return "Getting " + name + " for this game..."
	}
	return fmt.Sprintf("Downloading %s for this game... %d%%", name, v.done*100/v.total)
}

// roomFetchFailures names why missing packages could not be fetched.
func (g *gameShell) roomFetchFailures() string {
	r := &g.online.room
	var reasons []string
	for _, e := range r.content.missing {
		if reason, ok := r.fetch.failed[roomFetchKey(e)]; ok && e.Kind == session.MatchContentMapPackage {
			reasons = append(reasons, fmt.Sprintf("%s could not be downloaded: %s.", joinNames(roomContentNames([]session.MatchContentEntry{e})), reason))
		}
	}
	return strings.Join(reasons, " ")
}

// refreshOnlineRoomContent follows a remount of this seat's content — a map
// package the lobby fetched, or one the host installed from the map picker:
// the lobby's catalog is compiled again from the new content, and the room
// is adopted and resolved again on it.
func (g *gameShell) refreshOnlineRoomContent() {
	r := &g.online.room
	if cat, err := g.cs.compileCatalog(nil); err == nil {
		r.cat = cat
	}
	// An add-on the lobby fetched is in the library now, whether or not the
	// remount mounted it; the room resolves against that (§16.8).
	r.known = refreshKnownAddons(r.known, r.baseReq.RoomContent)
	g.adoptOnlineBase(r.base)
}

// onlineModFetch is a join waiting for the room's mod to download.
type onlineModFetch struct {
	room     onlineRoom
	entry    modfetch.Entry
	installs int
}

// startOnlineModFetch looks the room's mod up in the mod catalogue (Q14) and
// downloads it; the join continues once it is installed (pollOnlineModFetch).
func (g *gameShell) startOnlineModFetch(room onlineRoom, want session.MatchMod) {
	s := g.online
	name := onlineModName(want)
	lib, err := openModLibrary()
	if err != nil {
		g.onlineIdleStatus("This game needs the mod " + name + ", but the mod library is unavailable.")
		return
	}
	client := &modfetch.Client{CatalogURL: modfetch.CatalogURL(), CacheDir: lib.Root}
	s.phase, s.status = onlineFetching, "Looking for "+name+" in the mod catalogue..."
	s.run(func(ctx context.Context) onlineJobResult {
		ctx, cancel := context.WithTimeout(ctx, onlineCatalogueTimeout)
		defer cancel()
		result, err := client.FetchManifest(ctx)
		if err != nil {
			return onlineJobResult{err: fmt.Errorf("nanolathe: the mod catalogue is unavailable: logical path %s, providers searched [mod catalogue], expected the catalogue: %w", want.ID, err)}
		}
		for i := range result.Manifest.Mods {
			e := result.Manifest.Mods[i]
			if e.ID == want.ID && e.Version == want.Version && catalogueArchiveIs(e.Archive, want.Archive) {
				return onlineJobResult{described: &room, modEntry: &e}
			}
		}
		return onlineJobResult{refusal: "This game needs the mod " + name + ", which is not in the mod catalogue."}
	})
	g.refreshOnlinePanel()
}

// beginOnlineModDownload downloads the catalogue's copy of the room's mod
// through the shared mod download job.
func (g *gameShell) beginOnlineModDownload(room onlineRoom, entry modfetch.Entry) {
	s := g.online
	lib, err := openModLibrary()
	if err == nil {
		installs := modDownload.view().installs
		if err = g.startCatalogueDownload(lib, entry); err == nil {
			s.modFetch = &onlineModFetch{room: room, entry: entry, installs: installs}
			s.phase, s.status = onlineFetching, "Downloading "+entry.Name+" "+entry.Version+" for this game..."
			g.refreshOnlinePanel()
			return
		}
	}
	g.onlineIdleStatus("This game needs the mod " + entry.Name + ", which could not be downloaded: " + noticeReason(err))
}

// pollOnlineModFetch follows the mod download and, once the mod is
// installed, joins again, which now mounts it.
func (g *gameShell) pollOnlineModFetch() {
	s := g.online
	if s == nil || s.phase != onlineFetching || s.modFetch == nil {
		return
	}
	f := s.modFetch
	v := modDownload.view()
	if v.running {
		status := "Downloading " + f.entry.Name + " " + f.entry.Version + " for this game..."
		if v.total > 0 {
			status = fmt.Sprintf("Downloading %s %s for this game... %d%%", f.entry.Name, f.entry.Version, v.done*100/v.total)
		}
		if status != s.status {
			s.status = status
			g.refreshOnlinePanel()
		}
		return
	}
	s.modFetch = nil
	if v.installs == f.installs {
		g.onlineIdleStatus("This game needs the mod " + f.entry.Name + ", which could not be downloaded: " + strings.TrimPrefix(v.outcome, "Failed: "))
		return
	}
	s.phase, s.status = onlineConnecting, f.entry.Name+" "+f.entry.Version+" is installed. Joining..."
	g.adoptOnlineRoom(f.room, false)
}

// cancelOnlineModFetch stops a mod download the join started.
func (s *onlineScreen) cancelOnlineModFetch() {
	if s.modFetch != nil {
		modDownload.cancelNetwork()
		s.modFetch = nil
	}
}
