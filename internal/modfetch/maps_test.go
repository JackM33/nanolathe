package modfetch

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func mapManifest(t *testing.T) Manifest {
	t.Helper()
	entry := Entry{ID: "map", Name: "Map", Version: "1", Map: "maps/authored.ota", Requires: []string{"features"}, Archive: Archive{URL: "map.zip", Size: int64(len(fixtureArchive)), SHA256: digest(fixtureArchive)}}
	dep := Entry{ID: "features", Name: "Features", Version: "1", Archive: entry.Archive}
	return Manifest{Schema: 1, Maps: []Entry{entry}, Dependencies: []Entry{dep}}
}

func TestMapCatalogTrustDownloadAndOfflineCache(t *testing.T) {
	server := newCatalogServer(t)
	t.Setenv(mapCatalogEnv, server.URL+"/maps/manifest.json")
	client := &Client{CatalogURL: MapCatalogURL(), CacheDir: filepath.Join(t.TempDir(), "maps")}
	m := mapManifest(t)
	raw, _ := json.Marshal(m)
	server.route("/maps/manifest.json", serveJSON(string(raw)))
	server.route("/maps/map.zip", serveBytes(fixtureArchive))
	result, err := client.FetchManifest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Manifest.Maps) != 1 || len(result.Manifest.Dependencies) != 1 || len(result.Manifest.Mods) != 0 {
		t.Fatalf("wrong catalogue: %+v", result.Manifest)
	}
	dst := filepath.Join(t.TempDir(), "map.zip")
	if err := client.Download(context.Background(), result.Manifest.Maps[0], dst, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(dst); err != nil || digest(got) != digest(fixtureArchive) {
		t.Fatalf("download: %v", err)
	}
	server.route("/maps/manifest.json", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	cached, err := client.FetchManifest(context.Background())
	if err != nil || !cached.FromCache || cached.Err == nil || !reflect.DeepEqual(cached.Manifest, result.Manifest) {
		t.Fatalf("cache: %+v %v", cached, err)
	}
	t.Setenv(mapCatalogEnv, server.URL+"/different.json")
	if _, err := client.FetchManifest(context.Background()); !errors.Is(err, ErrOriginRefused) {
		t.Fatalf("nonexact override admitted: %v", err)
	}
}

// Presentation metadata is display-only: it round-trips through the catalogue
// and never changes validation or the stored archive identity (§5.6).
func TestMapManifestCarriesPresentationMetadata(t *testing.T) {
	m := mapManifest(t)
	m.Maps[0].Author = "Quintin Stone"
	m.Maps[0].Players = 8
	m.Maps[0].SizeMap = "12x12"
	m.Maps[0].Year = 1998
	m.Maps[0].Kind = "metal"
	m.Maps[0].AI = "Default"
	m.Maps[0].Legend = []LegendEntry{{Color: "#ffdc00", Label: "metal"}, {Color: "#40c840", Label: "reclaim"}}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse(DefaultMapCatalogURL)
	got, err := parseManifest(raw, base, originOf(base))
	if err != nil {
		t.Fatal(err)
	}
	entry := got.Maps[0]
	if entry.Author != "Quintin Stone" || entry.Players != 8 || entry.SizeMap != "12x12" || entry.Year != 1998 || entry.Kind != "metal" || entry.AI != "Default" {
		t.Fatalf("presentation metadata lost: %+v", entry)
	}
	if entry.Legend == nil || len(entry.Legend) != 2 || entry.Legend[0].Label != "metal" || entry.Legend[0].Color != "#ffdc00" {
		t.Fatalf("legend lost: %+v", entry)
	}
	// A catalogue without them keeps the zero values and still validates; the
	// dependency entry never carries them.
	if got.Dependencies[0].Author != "" || got.Dependencies[0].Players != 0 {
		t.Fatalf("dependency carries presentation metadata: %+v", got.Dependencies[0])
	}
}

func TestMapManifestRefusals(t *testing.T) {
	base, _ := url.Parse(DefaultMapCatalogURL)
	for _, tc := range []struct {
		name   string
		change func(*Manifest)
	}{
		{"unsafe path", func(m *Manifest) { m.Maps[0].Map = "maps/../bad.ota" }},
		{"nested path", func(m *Manifest) { m.Maps[0].Map = "maps/sub/bad.ota" }},
		{"wrong extension", func(m *Manifest) { m.Maps[0].Map = "maps/map.tnt" }},
		{"duplicate map id different version", func(m *Manifest) { e := m.Maps[0]; e.Version = "2"; m.Maps = append(m.Maps, e) }},
		{"duplicate dependency id", func(m *Manifest) { m.Dependencies = append(m.Dependencies, m.Dependencies[0]) }},
		{"id shared across groups", func(m *Manifest) { m.Maps[0].ID = "features" }},
		{"missing dependency", func(m *Manifest) { m.Maps[0].Requires = []string{"missing"} }},
		{"map as dependency", func(m *Manifest) { m.Maps[0].Requires = []string{"map"} }},
		{"dependency has requires", func(m *Manifest) { m.Dependencies[0].Requires = []string{"map"} }},
		{"dependency has map", func(m *Manifest) { m.Dependencies[0].Map = "maps/map.ota" }},
		{"foreign download", func(m *Manifest) { m.Dependencies[0].Archive.URL = "https://elsewhere.example/p.zip" }},
		{"bad digest", func(m *Manifest) { m.Dependencies[0].Archive.SHA256 = "bad" }},
		{"mixed catalogs", func(m *Manifest) { m.Mods = []Entry{m.Dependencies[0]} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := mapManifest(t)
			tc.change(&m)
			raw, _ := json.Marshal(m)
			if _, err := parseManifest(raw, base, originOf(base)); err == nil {
				t.Fatal("accepted invalid manifest")
			}
		})
	}
}

func TestMapCatalogURLDefault(t *testing.T) {
	t.Setenv(mapCatalogEnv, "")
	if MapCatalogURL() != DefaultMapCatalogURL {
		t.Fatal("wrong default")
	}
	t.Setenv(mapCatalogEnv, "http://example.com/maps.json")
	if _, _, err := catalogOrigin(MapCatalogURL()); !errors.Is(err, ErrOriginRefused) {
		t.Fatal("insecure remote map override admitted")
	}
}
