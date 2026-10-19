//go:build retail

package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/maplibrary"
	"github.com/nanolathe-gg/nanolathe/internal/modfetch"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
)

// An authored two-cell terrain keeps the integration fixture independent of
// retail map bytes. Retail assets supply only the menu being exercised.
func mapDownloadFixtureTNT() []byte {
	b := make([]byte, 64+2+32+1024)
	word := func(offset int, v uint32) { binary.LittleEndian.PutUint32(b[offset:], v) }
	word(0, 0x1020)
	word(4, 2)
	word(8, 2)
	word(12, 64)
	word(16, 66)
	word(20, 98)
	word(24, 1)
	for i := 0; i < 4; i++ {
		b[66+i*8+2] = 0xff
	}
	return b
}

func mapDownloadFixtureArchive(t *testing.T, id string, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	meta := fmt.Sprintf(`{"schema":1,"id":%q,"name":%q,"version":"1"}`, id, id)
	f, err := z.Create(modlibrary.MetadataFile)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte(meta))
	for name, data := range files {
		f, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write(data)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestMapCatalogueDownloadSelectAndOffline(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dep := mapDownloadFixtureArchive(t, "map-trees", map[string][]byte{"features/map-trees/trees.tdf": []byte("// authored support fixture\n")})
	terrain := mapDownloadFixtureArchive(t, "fixture-islands", map[string][]byte{
		"maps/Fixture Islands.ota": []byte(`[GlobalHeader]{missionname=Fixture Islands;missiondescription=An authored download fixture.;SCHEMACOUNT=1;[Schema 0]{Type=Network 1;}}`),
		"maps/Fixture Islands.tnt": mapDownloadFixtureTNT(),
	})
	archive := func(name string, b []byte) modfetch.Archive {
		return modfetch.Archive{URL: "/" + name, Size: int64(len(b)), SHA256: fmt.Sprintf("%x", sha256.Sum256(b))}
	}
	entry := modfetch.Entry{ID: "fixture-islands", Name: "Fixture Islands", Version: "1", Map: "maps/Fixture Islands.ota", Requires: []string{"map-trees"}, Summary: "An authored community map fixture.", Archive: archive("islands.zip", terrain),
		Author: "Fixture Author", Players: 4, SizeMap: "16x16", Year: 2001, Kind: "green",
		Legend: []modfetch.LegendEntry{{Color: "#ffdc00", Label: "metal"}, {Color: "#40c840", Label: "reclaim"}}}
	var preview bytes.Buffer
	previewImage := image.NewRGBA(image.Rect(0, 0, 32, 24))
	for y := 0; y < 24; y++ {
		for x := 0; x < 32; x++ {
			previewImage.SetRGBA(x, y, color.RGBA{R: uint8(x * 8), G: uint8(y * 10), B: 90, A: 255})
		}
	}
	if err := png.Encode(&preview, previewImage); err != nil {
		t.Fatal(err)
	}
	previewArchive := archive("preview.png", preview.Bytes())
	entry.Preview = &previewArchive
	dependency := modfetch.Entry{ID: "map-trees", Name: "Map trees", Version: "1", Archive: archive("trees.zip", dep)}
	var requests []string
	var requestsMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest.json":
			_ = json.NewEncoder(w).Encode(modfetch.Manifest{Schema: 1, Maps: []modfetch.Entry{entry}, Dependencies: []modfetch.Entry{dependency}})
		case "/preview.png":
			_, _ = w.Write(preview.Bytes())
		case "/trees.zip":
			requestsMu.Lock()
			requests = append(requests, "trees")
			requestsMu.Unlock()
			_, _ = w.Write(dep)
		case "/slow.zip":
			w.Header().Set("Content-Length", fmt.Sprint(len(terrain)))
			_, _ = w.Write(terrain[:len(terrain)/2])
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case "/islands.zip":
			requestsMu.Lock()
			requests = append(requests, "map")
			requestsMu.Unlock()
			_, _ = w.Write(terrain)
		default:
			if captureManifest := os.Getenv("NANOLATHE_MAPS_CAPTURE_MANIFEST"); captureManifest != "" && strings.HasPrefix(r.URL.Path, "/release-preview/") {
				http.ServeFile(w, r, filepath.Join(filepath.Dir(captureManifest), "previews", filepath.Base(r.URL.Path)))
				return
			}
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	defer mapDownload.cancelNetwork()
	t.Setenv("NANOLATHE_MAP_CATALOG", server.URL+"/manifest.json")
	g, _ := retailShellForTest(t)
	g.settingsWritable = false
	t.Cleanup(func() {
		if mapsFetchUI != nil {
			g.closeMapsFetch()
		}
		g.releaseAudio()
		g.cs.Close()
	})
	shot := survivalMenuShooter(t, g)
	g.openSurvivalMenu()
	before := g.setup
	g.activateGadget("SelectMap")
	if g.activePanel().Index("GETMAPS") < 0 {
		t.Fatal("Survival map chooser lacks catalogue button")
	}
	shot("map-chooser")
	g.activateGadget("GETMAPS")
	if mapsFetchUI == nil {
		t.Fatal("catalogue did not open")
	}
	await := func(done func() bool) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for !done() {
			if time.Now().After(deadline) {
				t.Fatal("catalogue job timeout")
			}
			time.Sleep(time.Millisecond)
		}
		g.pollMapsFetch()
	}
	await(func() bool { mapsFetchUI.mu.Lock(); defer mapsFetchUI.mu.Unlock(); return len(mapsFetchUI.entries) > 0 })
	await(func() bool { mapsFetchUI.mu.Lock(); defer mapsFetchUI.mu.Unlock(); return mapsFetchUI.preview != nil })
	requestsMu.Lock()
	if len(requests) != 0 {
		t.Fatal("preview fetched a map archive")
	}
	requestsMu.Unlock()
	shot("map-catalogue")
	if captureManifest := os.Getenv("NANOLATHE_MAPS_CAPTURE_MANIFEST"); captureManifest != "" {
		raw, err := os.ReadFile(captureManifest)
		if err != nil {
			t.Fatal(err)
		}
		var manifest modfetch.Manifest
		if err := json.Unmarshal(raw, &manifest); err != nil {
			t.Fatal(err)
		}
		for i := range manifest.Maps {
			if preview := manifest.Maps[i].Preview; preview != nil {
				preview.URL = server.URL + "/release-preview/" + filepath.Base(preview.URL)
			}
		}
		mapsFetchUI.mu.Lock()
		mapsFetchUI.entries = manifest.Maps
		mapsFetchUI.status = fmt.Sprintf("%d community maps", len(manifest.Maps))
		mapsFetchUI.mu.Unlock()
		g.refreshMapsFetch()
		await(func() bool { mapsFetchUI.mu.Lock(); defer mapsFetchUI.mu.Unlock(); return mapsFetchUI.preview != nil })
		shot("map-catalogue-release")
		g.commitMapsListSelection("MAPNAMES", len(manifest.Maps)-1)
		await(func() bool { mapsFetchUI.mu.Lock(); defer mapsFetchUI.mu.Unlock(); return mapsFetchUI.preview != nil })
		shot("map-catalogue-release-scrolled")
		mapsFetchUI.mu.Lock()
		mapsFetchUI.entries = []modfetch.Entry{entry}
		mapsFetchUI.entries[0].Archive.URL = server.URL + "/islands.zip"
		mapsFetchUI.selected = 0
		mapsFetchUI.mu.Unlock()
		g.refreshMapsFetch()
	}
	// The same Cancel action stops an actual in-flight HTTP transfer.
	slow := entry
	slow.ID = "slow-map"
	slow.Requires = nil
	slow.Archive.URL = server.URL + "/slow.zip"
	if err := g.startMapDownload(mapsFetchUI.lib, slow, nil); err != nil {
		t.Fatal(err)
	}
	await(func() bool { return mapDownload.view().done > 0 })
	shot("map-downloading")
	g.activateGadget("LOAD")
	await(func() bool { return !mapDownload.view().running })
	if mapDownload.view().outcome != "Download cancelled" {
		t.Fatal(mapDownload.view().outcome)
	}
	shot("map-cancelled")
	// A bad archive leaves both the installed library and the setup intact.
	bad := entry
	bad.ID = "broken-map"
	bad.Archive.URL = server.URL + "/islands.zip"
	bad.Archive.SHA256 = strings.Repeat("0", 64)
	bad.Requires = nil
	if err := g.startMapDownload(mapsFetchUI.lib, bad, nil); err != nil {
		t.Fatal(err)
	}
	await(func() bool { return !mapDownload.view().running })
	if !strings.HasPrefix(mapDownload.view().outcome, "Failed:") || !reflect.DeepEqual(g.setup, before) {
		t.Fatal("bad archive changed selection or did not report failure")
	}
	installed, err := mapsFetchUI.lib.Installed()
	if err != nil || len(installed) != 0 {
		t.Fatalf("bad archive published: %v, %v", installed, err)
	}
	shot("map-download-failed")
	requestsMu.Lock()
	requests = nil
	requestsMu.Unlock()
	g.activateGadget("LOAD")
	await(func() bool { return !mapDownload.view().running })
	if strings.HasPrefix(mapDownload.view().outcome, "Failed:") {
		t.Fatal(mapDownload.view().outcome)
	}
	requestsMu.Lock()
	gotRequests := append([]string(nil), requests...)
	requestsMu.Unlock()
	if !reflect.DeepEqual(gotRequests, []string{"trees", "map"}) {
		t.Fatalf("downloads %v", gotRequests)
	}
	if !reflect.DeepEqual(g.setup, before) || !g.survivalMenu {
		t.Fatal("download changed Survival setup")
	}
	if mapsFetchPanel.TextOf("LOAD") != "Select" {
		t.Fatalf("installed row action: %s", mapsFetchPanel.TextOf("LOAD"))
	}
	shot("map-installed")
	// A scrolled row's X must not select it, even when another row is selected.
	mapsFetchUI.mu.Lock()
	originalEntries := append([]modfetch.Entry(nil), mapsFetchUI.entries...)
	var scrolledEntries []modfetch.Entry
	for i := range 19 {
		other := entry
		other.ID, other.Map, other.Name, other.Preview = fmt.Sprintf("uninstalled-%d", i), fmt.Sprintf("maps/Other%d.ota", i), fmt.Sprintf("Uninstalled fixture %02d", i+1), nil
		scrolledEntries = append(scrolledEntries, other)
	}
	scrolledEntries = append(scrolledEntries, originalEntries[0])
	mapsFetchUI.entries = scrolledEntries
	mapsFetchUI.selected = 18
	mapsFetchUI.mu.Unlock()
	g.refreshMapsFetch()
	mapsFetchPanel.SetListTopAt(mapsFetchPanel.Index("MAPNAMES"), 19, mapsFetchPanel.ListMaxTopAt(mapsFetchPanel.Index("MAPNAMES")))
	shot("map-catalogue-row-x-scrolled")
	clickMapRemoveRow(t, g, 19)
	if pendingMapRemoval == nil || pendingMapRemoval.logical != entry.Map || mapsFetchUI.selected != 18 {
		t.Fatal("catalogue X changed selection or targeted another row")
	}
	modal := g.frontend.Panels.Modal()
	g.frontend.Panels.CloseModal()
	g.finishMapRemovalConfirmation(modal, "OK")
	mapsFetchUI.mu.Lock()
	mapsFetchUI.entries = originalEntries
	mapsFetchUI.mu.Unlock()
	g.commitMapsListSelection("MAPNAMES", 0)

	g.activateGadget("LOAD")
	if mapsFetchUI != nil || !strings.EqualFold(g.maps[g.mapIdx], "Fixture Islands") {
		t.Fatal("installed map did not return to preview")
	}
	if !reflect.DeepEqual(g.setup, before) {
		t.Fatal("preview committed selection before Load")
	}
	shot("map-selected")
	g.activateGadget("LOAD")
	if g.setup.MapName != "Fixture Islands" || !g.survivalMenu {
		t.Fatal("ordinary Load did not select downloaded Survival map")
	}
	server.Close()
	g.activateGadget("SelectMap")
	g.activateGadget("GETMAPS")
	if mapsFetchUI == nil {
		t.Fatal("catalogue did not open")
	}
	await(func() bool {
		mapsFetchUI.mu.Lock()
		defer mapsFetchUI.mu.Unlock()
		return strings.HasPrefix(mapsFetchUI.status, "Offline:")
	})
	await(func() bool { mapsFetchUI.mu.Lock(); defer mapsFetchUI.mu.Unlock(); return mapsFetchUI.preview != nil })
	shot("map-offline")
	g.activateGadget("LOAD")
	if mapsFetchUI != nil {
		t.Fatal("offline installed selection failed")
	}
	g.closeSurvivalMenu()
	g.openMenu(modeMenuSkirmish)
	g.activateGadget("SelectMap")
	if g.activePanel().Index("GETMAPS") < 0 {
		t.Fatal("Skirmish chooser lacks map catalogue")
	}
	shot("skirmish-map-chooser")
	// A fresh ordinary desktop mount also discovers the download.
	opts := g.opts
	opts.Roots = g.cs.baseRoots
	opts.Root = opts.Roots[0]
	fresh, err := openContent(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if _, err := fresh.fs.Stat(entry.Map); err != nil {
		t.Fatal(err)
	}
	fresh.Close()
	if _, ok := g.mapRemoveRowRect(g.activePanel(), g.activePanel().Index("MAPNAMES"), g.mapIdx); !ok {
		t.Fatal("downloaded selection lacks X")
	}
	beforeRemoval := g.setup
	targetRow := slices.Index(g.maps, "Fixture Islands")
	g.mapIdx = 0
	if targetRow == 0 {
		g.mapIdx = 1
	}
	g.refreshMapPanel()
	p := g.activePanel()
	p.SetListTopAt(p.Index("MAPNAMES"), targetRow, p.ListMaxTopAt(p.Index("MAPNAMES")))
	selectedBefore := g.mapIdx
	shot("map-row-x-scrolled")
	clickMapRemoveRow(t, g, targetRow)
	if g.mapIdx != selectedBefore || pendingMapRemoval == nil || pendingMapRemoval.logical != entry.Map {
		t.Fatal("picker X selected or loaded the clicked row")
	}
	modal = g.frontend.Panels.Modal()
	if modal == nil {
		t.Fatal("delete lacks confirmation")
	}
	shot("map-delete-confirmation")
	g.frontend.Panels.CloseModal()
	g.finishMapRemovalConfirmation(modal, "OK")
	if _, err := g.cs.fs.Stat(entry.Map); err != nil {
		t.Fatal("cancel deleted map")
	}
	clickMapRemoveRow(t, g, targetRow)
	modal = g.frontend.Panels.Modal()
	g.frontend.Panels.CloseModal()
	g.finishMapRemovalConfirmation(modal, "DELETEMAP")
	if _, err := g.cs.fs.Stat(entry.Map); err == nil {
		t.Fatal("deleted map still mounted")
	}
	if g.setup.MapName == "Fixture Islands" {
		t.Fatal("setup still selects removed map")
	}
	beforeRemoval.MapName = g.setup.MapName
	if !reflect.DeepEqual(g.setup, beforeRemoval) {
		t.Fatal("delete changed setup")
	}
	root, _ := maplibrary.DefaultRoot()
	remaining, err := (&modlibrary.Library{Root: root}).Installed()
	if err != nil || len(remaining) != 1 || remaining[0].ID != dependency.ID {
		t.Fatalf("shared dependency was removed: %v, %v", remaining, err)
	}
	if _, ok := g.mapRemoveRowRect(g.activePanel(), g.activePanel().Index("MAPNAMES"), g.mapIdx); ok {
		t.Fatal("retail replacement exposes X")
	}
	shot("map-deleted")
}

func clickMapRemoveRow(t *testing.T, g *gameShell, row int) {
	t.Helper()
	p := g.activePanel()
	r, ok := g.mapRemoveRowRect(p, p.Index("MAPNAMES"), row)
	if !ok {
		t.Fatalf("row %d has no visible X", row)
	}
	in := &input.State{Mouse: &input.MouseState{}, Kbd: &input.KeyboardState{}}
	in.Mouse.SetPosition(float32(r.X+r.W/2), float32(r.Y+r.H/2))
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	g.serviceMenuWidgets(p, in)
	in.Mouse.ResetEdges()
	in.Mouse.SetButton(input.MouseButtonLeft, false)
	in.EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyEnter})
	g.serviceMenuWidgets(p, in)
	if in.PendingTokens() != 0 {
		t.Fatal("row click leaked a keyboard token into confirmation")
	}
}
