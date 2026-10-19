package main

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/maplibrary"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Room content tests of DESIGN_MULTIPLAYER §16.8 on authored installs: tiny
// archives written here, never retail bytes. The file names are retail's so
// the product grouping is exercised; the contents are not.

// writeRoomArchive writes an archive holding files to root/name.
func writeRoomArchive(t *testing.T, root, name string, files ...vfs.ArchiveFile) {
	t.Helper()
	var buf bytes.Buffer
	if err := vfs.WriteArchive(&buf, files, vfs.ArchiveWriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, name), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// roomTestInstall mounts root as a base install the way mountContent does,
// with nothing above or below it.
func roomTestInstall(t *testing.T, root string) *contentSet {
	t.Helper()
	fs := vfs.New()
	t.Cleanup(func() { _ = fs.Close() })
	if err := fs.MountGameDirectories([]string{root}); err != nil {
		t.Fatal(err)
	}
	return &contentSet{fs: fs, unmappedMount: fs, baseRoots: []string{root}, limits: content.RetailLimits(), profileLimits: content.RetailLimits()}
}

// roomTestBase writes the archives both test installs share and one loose
// unit file, which a room never mounts.
func roomTestBase(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeRoomArchive(t, root, "totala1.hpi", vfs.ArchiveFile{Path: "units/armcom.fbi", Data: []byte("base commander")},
		vfs.ArchiveFile{Path: "maps/Test Map.ota", Data: []byte("test map")}, vfs.ArchiveFile{Path: "maps/Test Map.tnt", Data: []byte("terrain")})
	writeRoomArchive(t, root, "rev31.gp3", vfs.ArchiveFile{Path: "gamedata/sidedata.tdf", Data: []byte("patch")})
	if err := os.MkdirAll(filepath.Join(root, "units"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "units", "loose.fbi"), []byte("loose"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// The base archives are the mounted ones, by file name with their size and
// SHA-256 in field 16's order; a file the mount rejected is not content.
// Hashes are cached in the data directory and read back on the next listing.
func TestRoomBaseArchivesAreTheMountedOnes(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	root := roomTestBase(t)
	writeRoomArchive(t, root, "AFark.ufo", vfs.ArchiveFile{Path: "units/armfark.fbi", Data: []byte("bonus")})
	if err := os.WriteFile(filepath.Join(root, "broken.hpi"), []byte("not an archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	cs := roomTestInstall(t, root)
	archives, err := baseArchives(cs)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range archives {
		names = append(names, a.entry.Name)
		data, err := os.ReadFile(a.path)
		if err != nil {
			t.Fatal(err)
		}
		if a.entry.Kind != session.MatchContentBaseArchive || a.entry.Size != uint64(len(data)) || a.entry.Digest != sha256.Sum256(data) {
			t.Fatalf("%s: entry %+v does not describe its file", a.entry.Name, a.entry)
		}
	}
	if want := []string{"AFark.ufo", "rev31.gp3", "totala1.hpi"}; !equalStrings(names, want) {
		t.Fatalf("listed %v, want %v", names, want)
	}
	if _, err := os.Stat(archiveDigestCachePath()); err != nil {
		t.Fatalf("no digest cache: %v", err)
	}
	again, err := baseArchives(cs)
	if err != nil || len(again) != len(archives) || again[0].entry != archives[0].entry {
		t.Fatalf("the cached listing differs: %+v, %v", again, err)
	}
}

// The user's 2026-10-16 room: the host's folder lacks a bonus archive the
// guest's holds. The guest resolves the host's content with nothing missing
// and mounts the room without its extra archive and without either install's
// loose files, so both seats compose the same battle.
func TestRoomContentLeavesOutArchivesTheHostLacks(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	hostRoot, guestRoot := roomTestBase(t), roomTestBase(t)
	writeRoomArchive(t, guestRoot, "AFark.ufo", vfs.ArchiveFile{Path: "units/armfark.fbi", Data: []byte("bonus")})
	host, guest := roomTestInstall(t, hostRoot), roomTestInstall(t, guestRoot)
	hostArchives, err := baseArchives(host)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := onlineRoomContent(host, hostArchives, nil, "Test Map")
	if err != nil {
		t.Fatal(err)
	}
	guestArchives, err := baseArchives(guest)
	if err != nil {
		t.Fatal(err)
	}
	plan := resolveRoomContent(guest, guestArchives, nil, nil, entries)
	if len(plan.missing) != 0 || len(plan.different) != 0 || len(plan.archives) != 2 {
		t.Fatalf("plan %+v", plan)
	}
	mounted, view, err := roomContentFS(guest, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer mounted.Close()
	if data, err := view.ReadFileLimit("units/armcom.fbi", 1<<20); err != nil || string(data) != "base commander" {
		t.Fatalf("the room's archive = %q, %v", data, err)
	}
	for _, left := range []string{"units/armfark.fbi", "units/loose.fbi"} {
		if _, err := view.Stat(left); err == nil {
			t.Fatalf("the room mounts %s", left)
		}
	}
	// The guest's own content still has its bonus unit for single-player.
	if _, err := guest.fs.Stat("units/armfark.fbi"); err != nil {
		t.Fatal("the guest's own content lost its archive")
	}
}

// An archive the host lists that this install lacks is missing and named by
// its product; one it holds in other bytes is mounted and named as differing.
func TestRoomContentNamesMissingAndDifferingArchives(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	hostRoot, guestRoot := roomTestBase(t), roomTestBase(t)
	writeRoomArchive(t, hostRoot, "btdata.ccx", vfs.ArchiveFile{Path: "units/bt.fbi", Data: []byte("bt")})
	writeRoomArchive(t, hostRoot, "worlds.hpi", vfs.ArchiveFile{Path: "worlds.txt", Data: []byte("host")})
	writeRoomArchive(t, guestRoot, "WORLDS.HPI", vfs.ArchiveFile{Path: "worlds.txt", Data: []byte("guest")})
	host, guest := roomTestInstall(t, hostRoot), roomTestInstall(t, guestRoot)
	hostArchives, err := baseArchives(host)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := onlineRoomContent(host, hostArchives, nil, "Test Map")
	if err != nil {
		t.Fatal(err)
	}
	guestArchives, err := baseArchives(guest)
	if err != nil {
		t.Fatal(err)
	}
	plan := resolveRoomContent(guest, guestArchives, nil, nil, entries)
	if got := roomMissingText(plan.missing); got != "This game needs Battle Tactics (btdata.ccx), which you don't have." {
		t.Fatalf("missing: %q", got)
	}
	if got := roomDifferentText(plan.different); got != "Your copy of worlds.hpi differs from the host's." {
		t.Fatalf("different: %q", got)
	}
	if plan.archives["worlds.hpi"] != filepath.Join(guestRoot, "WORLDS.HPI") {
		t.Fatalf("the differing copy is not mounted: %v", plan.archives)
	}
}

// An archive of maps alone adds nothing a battle on another map reads, so a
// room lists it only when it holds the room's map; a map only loose files
// supply is refused, since no seat mounts them online.
func TestRoomContentListsMapArchivesForTheirMap(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	root := roomTestBase(t)
	writeRoomArchive(t, root, "Other Maps.ufo", vfs.ArchiveFile{Path: "maps/Other Map.ota", Data: []byte("other")}, vfs.ArchiveFile{Path: "maps/Other Map.tnt", Data: []byte("terrain")})
	writeRoomArchive(t, root, "Featured.ufo", vfs.ArchiveFile{Path: "maps/Featured.ota", Data: []byte("featured")}, vfs.ArchiveFile{Path: "features/rock.tdf", Data: []byte("rock")})
	if err := os.MkdirAll(filepath.Join(root, "maps"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "maps", "Loose Map.ota"), []byte("loose"), 0o600); err != nil {
		t.Fatal(err)
	}
	cs := roomTestInstall(t, root)
	archives, err := baseArchives(cs)
	if err != nil {
		t.Fatal(err)
	}
	names := func(mapName string) []string {
		t.Helper()
		entries, err := onlineRoomContent(cs, archives, nil, mapName)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range entries {
			out = append(out, e.Name)
		}
		return out
	}
	if got, want := names("Test Map"), []string{"Featured.ufo", "rev31.gp3", "totala1.hpi"}; !equalStrings(got, want) {
		t.Fatalf("a room on Test Map lists %v, want %v", got, want)
	}
	if got, want := names("Other Map"), []string{"Featured.ufo", "Other Maps.ufo", "rev31.gp3", "totala1.hpi"}; !equalStrings(got, want) {
		t.Fatalf("a room on Other Map lists %v, want %v", got, want)
	}
	_, err = onlineRoomContent(cs, archives, nil, "Loose Map")
	if err == nil || !strings.Contains(onlineRefusalText(err), "loose files") {
		t.Fatalf("a loose map: %v", err)
	}
}

// Products group their archives; packages and other archives keep their
// own names, joined as a sentence.
func TestRoomContentNamesProducts(t *testing.T) {
	entries := []session.MatchContentEntry{
		{Kind: session.MatchContentBaseArchive, Name: "ccdata.ccx"},
		{Kind: session.MatchContentBaseArchive, Name: "CCMAPS.CCX"},
		{Kind: session.MatchContentBaseArchive, Name: "Example.ufo"},
		{Kind: session.MatchContentBaseArchive, Name: "tactics3.hpi"},
		{Kind: session.MatchContentMapPackage, Name: "gods-of-war-2", Version: "original"},
	}
	got := joinNames(roomContentNames(entries))
	want := "The Core Contingency (ccdata.ccx, CCMAPS.CCX), Battle Tactics (tactics3.hpi), Example.ufo and the downloaded map package gods-of-war-2"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

// A room's limits are the config's own, with the downloaded-map terrain cap
// only when the room lists a map package, whatever this install mounted
// (DESIGN_MODS_MUTATORS §5.6).
func TestRoomLimitsFollowTheRoom(t *testing.T) {
	retail := content.RetailLimits()
	raised := retail
	raised.TNTBytes = max(raised.TNTBytes, maplibrary.MaxTNTBytes)
	cs := &contentSet{limits: raised, profileLimits: retail}
	if got := onlineRoomLimits(cs, []session.MatchContentEntry{{Kind: session.MatchContentBaseArchive, Name: "totala1.hpi"}}); got != retail {
		t.Fatalf("a room without a map package: %+v, want %+v", got, retail)
	}
	if got := onlineRoomLimits(cs, []session.MatchContentEntry{{Kind: session.MatchContentMapPackage, Name: "m", Version: "1"}}); got != raised {
		t.Fatalf("a room with a map package: %+v, want %+v", got, raised)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
