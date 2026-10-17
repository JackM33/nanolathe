//go:build retail

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/addonlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/modfetch"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/replay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// bonusAddon is the official add-on DESIGN_MODS_MUTATORS §5.7 hosts.
var bonusAddon = settings.AddonSelection{ID: "cavedog-bonus-units", Version: "original"}

// linkInstall links every archive of the reference install, except those
// named, into a fresh folder: an install without the reference's loose files.
func linkInstall(t *testing.T, retail string, left ...string) string {
	t.Helper()
	entries, err := os.ReadDir(retail)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, e := range entries {
		if e.IsDir() || !vfs.IsArchiveName(e.Name()) {
			continue
		}
		skip := false
		for _, name := range left {
			skip = skip || strings.EqualFold(e.Name(), name)
		}
		if !skip {
			if err := os.Symlink(filepath.Join(retail, e.Name()), filepath.Join(dir, e.Name())); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

// stageBonusAddon copies the reference install's bonus unit archives into a
// package folder with identity-only metadata, as the hosted zip holds them,
// and returns it with the archives' identities. It skips when the reference
// install lacks any of them.
func stageBonusAddon(t *testing.T, retail string) (string, []addonlibrary.Archive) {
	t.Helper()
	dir := t.TempDir()
	raw, _ := json.Marshal(modlibrary.Metadata{Schema: 1, ID: bonusAddon.ID, Name: "Cavedog bonus units", Version: bonusAddon.Version})
	if err := os.WriteFile(filepath.Join(dir, modlibrary.MetadataFile), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(retail)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		for _, want := range roomBonusUnitArchives {
			if strings.EqualFold(e.Name(), want) {
				names = append(names, e.Name())
			}
		}
	}
	if len(names) != len(roomBonusUnitArchives) {
		t.Skip("the reference install lacks some bonus unit archives")
	}
	paths := make([]string, len(names))
	for i, name := range names {
		data, err := os.ReadFile(filepath.Join(retail, name))
		if err != nil {
			t.Fatal(err)
		}
		paths[i] = filepath.Join(dir, name)
		if err := os.WriteFile(paths[i], data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	installed, _, err := addonlibrary.Installed(t.TempDir())
	if err != nil || len(installed) != 0 {
		t.Fatal(installed, err)
	}
	archives := make([]addonlibrary.Archive, len(names))
	for i := range names {
		_, digest, err := archiveDigest(paths[i])
		if err != nil {
			t.Fatal(err)
		}
		info, _ := os.Stat(paths[i])
		archives[i] = addonlibrary.Archive{Name: names[i], Size: info.Size(), SHA256: digest}
	}
	return dir, archives
}

// installBonusAddon installs the bonus unit add-on into the add-on library
// under the test's data directory.
func installBonusAddon(t *testing.T, retail string) []addonlibrary.Archive {
	t.Helper()
	staged, archives := stageBonusAddon(t, retail)
	lib, err := openAddonLibrary()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.InstallDirectory(staged, modlibrary.InstallOptions{Validate: addonlibrary.Validator(archives)}); err != nil {
		t.Fatal(err)
	}
	return archives
}

// An enabled add-on mounts exactly as its archives would from the install's
// folder: the same providers at the same tier, so the same manifest, and the
// bonus units in the catalog (§5.7). --addon none leaves them out.
func TestAddonMountsAsIfInTheFolderRetail(t *testing.T) {
	retail := testsupport.RetailRoot(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	installBonusAddon(t, retail)
	without := linkInstall(t, retail, roomBonusUnitArchives...)

	open := func(root string, addons ...settings.AddonSelection) *contentSet {
		t.Helper()
		cs, err := openContent(Options{Root: root, Addons: addons, AddonsSet: true})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cs.Close() })
		return cs
	}
	enabled := open(without, bonusAddon)
	whole := open(linkInstall(t, retail))
	none := open(without)
	if len(enabled.addons) != 1 || len(none.addons) != 0 {
		t.Fatalf("mounted add-ons: enabled %d, none %d; notes %v", len(enabled.addons), len(none.addons), enabled.notes)
	}
	got, err := enabled.unmappedMount.ManifestHash()
	if err != nil {
		t.Fatal(err)
	}
	want, err := whole.unmappedMount.ManifestHash()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("manifest with the add-on %s, with the archives in the folder %s", got, want)
	}
	cat, err := enabled.compileCatalog(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, unit := range []string{"armfark", "armflea", "armscab", "cormabm", "cornecro", "corplas"} {
		if _, ok := cat.Unit(unit); !ok {
			t.Errorf("the add-on did not supply %s", unit)
		}
	}
	if _, err := none.unmappedMount.Stat("units/armfark.fbi"); err == nil {
		t.Fatal("--addon none mounted the bonus units")
	}
}

// bonusAddonCatalogue serves a local add-on catalogue offering the bonus
// unit add-on, zipped as the website hosts it from the reference install's
// archives, and points NANOLATHE_ADDON_CATALOG at it.
func bonusAddonCatalogue(t *testing.T, retail string) modfetch.Entry {
	t.Helper()
	staged, archives := stageBonusAddon(t, retail)
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	entries, err := os.ReadDir(staged)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(staged, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		f, err := z.Create(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write(data)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	zipped := buf.Bytes()
	entry := modfetch.Entry{ID: bonusAddon.ID, Name: "Cavedog bonus units", Version: bonusAddon.Version,
		Archive: modfetch.Archive{URL: "/bonus.zip", Size: int64(len(zipped)), SHA256: fmt.Sprintf("%x", sha256.Sum256(zipped))}}
	for _, a := range archives {
		entry.Contents = append(entry.Contents, modfetch.Content{Name: a.Name, Size: a.Size, SHA256: fmt.Sprintf("%x", a.SHA256)})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest.json":
			_ = json.NewEncoder(w).Encode(modfetch.Manifest{Schema: 1, Addons: []modfetch.Entry{entry}})
		case "/bonus.zip":
			_, _ = w.Write(zipped)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { addonDownload.cancelNetwork() })
	t.Setenv("NANOLATHE_ADDON_CATALOG", server.URL+"/manifest.json")
	// A new catalogue for this test: a host may ask it again.
	hostAskedAddonCatalogue.Store(false)
	t.Cleanup(func() { hostAskedAddonCatalogue.Store(false) })
	return entry
}

// A host whose folder holds the bonus unit archives lists them as the
// official add-on, not as base archives; a guest without them downloads the
// add-on from the catalogue in the lobby, enables it and remounts, and its
// battle then mounts the same content as the host's (DESIGN_MULTIPLAYER
// §16.8).
func TestOnlineRoomFetchesItsAddonRetail(t *testing.T) {
	roomFetchesItsAddon(t, "")
}

// A guest whose config leaves add-ons off still fetches the room's add-on,
// and its battle mounts it from the library although its own content does
// not: the lobby resolves the room on the installed copy rather than asking
// for it again.
func TestOnlineRoomFetchesAnAddonItsConfigLeavesOffRetail(t *testing.T) {
	config := filepath.Join(t.TempDir(), "nanolathe-mod.json")
	if err := os.WriteFile(config, []byte(`{"schema":2,"id":"addons-off","name":"Add-ons off","version":"1","content":{"addons":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	roomFetchesItsAddon(t, config)
}

func roomFetchesItsAddon(t *testing.T, guestConfig string) {
	retail := testsupport.RetailRoot(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	entry := bonusAddonCatalogue(t, retail)

	host, err := openContent(Options{Root: linkInstall(t, retail), Seed: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close() })
	archives, err := baseArchives(host)
	if err != nil {
		t.Fatal(err)
	}
	known := onlineKnownAddons(context.Background(), func(known []knownAddon) bool { return hostNeedsAddonCatalogue(archives, known) })
	roomContent, err := onlineRoomContent(host, archives, known, "Ashap Plateau")
	if err != nil {
		t.Fatal(err)
	}
	var listed []session.MatchContentEntry
	for _, e := range roomContent {
		for _, bonus := range roomBonusUnitArchives {
			if e.Kind == session.MatchContentBaseArchive && strings.EqualFold(e.Name, bonus) {
				t.Fatalf("the room lists %s as a base archive", e.Name)
			}
		}
		if e.Kind == session.MatchContentAddon {
			listed = append(listed, e)
		}
	}
	if len(listed) != 1 || listed[0].Name != entry.ID || listed[0].Version != entry.Version {
		t.Fatalf("the room lists add-ons %+v", listed)
	}
	hostCat, err := host.compileCatalog(nil)
	if err != nil {
		t.Fatal(err)
	}
	settings := onlineSettings{rules: session.ModernRuleSetName, mapName: "Ashap Plateau", location: 1, commanderDeath: 1}
	base, err := onlineBaseConfig(host, hostCat, settings, onlineCreationFrozen(host, [2]uint32{3, 5}, content.Mutators{}, content.Restrictions{}, nil, roomContent))
	if err != nil {
		t.Fatal(err)
	}

	cs, err := openContent(Options{Root: linkInstall(t, retail, roomBonusUnitArchives...), Seed: -1, ModConfig: guestConfig})
	if err != nil {
		t.Fatal(err)
	}
	guest := onlineRetailShell(t, cs, Options{Root: cs.root, Roots: cs.roots, Seed: -1})
	t.Cleanup(func() { _ = guest.cs.Close() })
	guestArchives, err := baseArchives(cs)
	if err != nil {
		t.Fatal(err)
	}
	guestCat, err := cs.compileCatalog(nil)
	if err != nil {
		t.Fatal(err)
	}
	guestKnown := onlineKnownAddons(context.Background(), func(known []knownAddon) bool {
		return roomAddonsUnknown(cs, known, base.Request().RoomContent)
	})
	if err := guest.openOnlineScreen(); err != nil {
		t.Fatal(err)
	}
	guest.openOnlineLobby(newFakeOnlineLobby(1, 0, 1), guestCat, base, "wss://relay.example.test/relay", guestArchives, guestKnown)
	r := &guest.online.room
	if len(r.content.missing) != 1 || len(r.content.fetch) != 1 {
		t.Fatalf("missing %+v, fetch %+v", r.content.missing, r.content.fetch)
	}
	deadline := time.Now().Add(30 * time.Second)
	for len(r.content.missing) > 0 || r.addonFetch.want != nil {
		if time.Now().After(deadline) {
			t.Fatalf("never fetched: %q, failed %v", guest.onlineLobbyBlock(), r.addonFetch.failed)
		}
		guest.pollOnline()
		time.Sleep(2 * time.Millisecond)
	}
	mounted := 1
	if guestConfig != "" {
		mounted = 0
	}
	if len(guest.cs.addons) != mounted || len(guest.addonSetting) != 1 || guest.addonSetting[0] != bonusAddon {
		t.Fatalf("mounted %d add-ons, saved %+v", len(guest.cs.addons), guest.addonSetting)
	}
	if _, ok := r.cat.Unit("armfark"); ok != (mounted == 1) {
		t.Fatalf("the guest's lobby catalog has the fetched FARK: %v", ok)
	}
	if block := guest.onlineLobbyBlock(); strings.Contains(block, "needs") || strings.Contains(block, "for this game") {
		t.Fatalf("the lobby still says %q", block)
	}
	// Both seats' battles mount the same providers.
	manifest := func(cs *contentSet, archives []localArchive, known []knownAddon) string {
		t.Helper()
		plan := resolveRoomContent(cs, archives, nil, known, roomContent)
		if len(plan.missing) > 0 {
			t.Fatalf("missing %+v", plan.missing)
		}
		mounted, _, err := roomContentFS(cs, plan)
		if err != nil {
			t.Fatal(err)
		}
		defer mounted.Close()
		hash, err := mounted.ManifestHash()
		if err != nil {
			t.Fatal(err)
		}
		return hash
	}
	guestArchives, err = baseArchives(guest.cs)
	if err != nil {
		t.Fatal(err)
	}
	if h, g := manifest(host, archives, known), manifest(guest.cs, guestArchives, r.known); h != g {
		t.Fatalf("battle content differs: host %s, guest %s", h, g)
	}
}

// A single-player replay records the add-ons its battle mounted, and plays
// back only on content that mounts exactly them (§5.7).
func TestReplayRecordsItsAddonsRetail(t *testing.T) {
	retail := testsupport.RetailRoot(t)
	replayTestSettings(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	installBonusAddon(t, retail)
	without := linkInstall(t, retail, roomBonusUnitArchives...)
	dir := t.TempDir()
	path := filepath.Join(dir, "addons.nlreplay")
	parse := func(args ...string) Options {
		t.Helper()
		var out bytes.Buffer
		opts, err := parseFlags(append([]string{"--root", without, "--renderer", "classic"}, args...), &out)
		if err != nil {
			t.Fatalf("%v: %s", err, &out)
		}
		return opts
	}
	opts := parse("--addon", bonusAddon.ID+"@"+bonusAddon.Version, "--map", "ashap plateau", "--seed", "5", "--shot", filepath.Join(dir, "shot.png"), "--shot-ticks", "60", "--record-replay", path)
	cs := replayTestContent(t, opts)
	previous := clPtr
	defer func() { clPtr = previous }()
	if err := runShot(opts, cs); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := replay.NewReader(data)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Header().Addons; len(got) != 1 || got[0].ID != bonusAddon.ID || got[0].Version != bonusAddon.Version {
		t.Fatalf("recorded add-ons %+v", got)
	}
	if _, err := verifyReplay(data, cs); err != nil {
		t.Fatal(err)
	}
	plain := replayTestContent(t, parse())
	var refused *replayAddonsError
	if _, err := verifyReplay(data, plain); !errors.As(err, &refused) || !errors.Is(err, replay.ErrIncompatible) {
		t.Fatalf("playback without the add-on: %v", err)
	}
}
