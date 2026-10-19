package modfetch

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func addonManifest() Manifest {
	digest := strings.Repeat("A", 64)
	return Manifest{Schema: 1, Addons: []Entry{{
		ID: "bonus", Name: "Bonus units", Version: "original",
		Archive:  Archive{URL: "bonus.zip", Size: 10, SHA256: digest},
		Contents: []Content{{Name: "AFark.ufo", Size: 4, SHA256: digest}, {Name: "corplas.UFO", Size: 6, SHA256: digest}},
	}}}
}

// The add-on catalogue lists each add-on's archives, which a room and the
// already-in-your-install check compare by size and SHA-256 (§5.7).
func TestAddonManifestAdmitsContents(t *testing.T) {
	base, _ := url.Parse(DefaultAddonCatalogURL)
	raw, _ := json.Marshal(addonManifest())
	m, err := parseManifest(raw, base, originOf(base))
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := m.OffersAddon("bonus", "original")
	if !ok || len(entry.Contents) != 2 || entry.Contents[0].SHA256 != strings.Repeat("a", 64) || entry.Archive.URL != "https://nanolathe.gg/addons/bonus.zip" {
		t.Fatalf("entry = %+v, %v", entry, ok)
	}
	if _, ok := m.Offers("bonus", "original"); ok {
		t.Fatal("an add-on was offered as a mod")
	}
}

// Faction add-ons are listed under their own key, which a build without
// faction support ignores; this build offers them as add-ons, and an id and
// version may appear under only one of the two keys (§5.8 "Catalogue").
func TestAddonManifestReadsFactionsApart(t *testing.T) {
	base, _ := url.Parse(DefaultAddonCatalogURL)
	faction := addonManifest().Addons[0]
	faction.ID, faction.Name = "xeno", "Xeno"
	raw, _ := json.Marshal(map[string]any{"schema": 1, "addons": addonManifest().Addons, "factions": []Entry{faction}})
	m, err := parseManifest(raw, base, originOf(base))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.OffersAddon("xeno", "original"); !ok || len(m.Addons) != 2 {
		t.Fatalf("faction entry not offered: %+v", m.Addons)
	}
	var old struct {
		Addons []Entry `json:"addons"`
	}
	if err := json.Unmarshal(raw, &old); err != nil || len(old.Addons) != 1 {
		t.Fatalf("a reader of the add-ons key alone sees %+v (%v)", old.Addons, err)
	}
	faction.ID = "bonus"
	raw, _ = json.Marshal(map[string]any{"schema": 1, "addons": addonManifest().Addons, "factions": []Entry{faction}})
	if _, err := parseManifest(raw, base, originOf(base)); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("an add-on under both keys: err %v", err)
	}
}

func TestAddonManifestRefusals(t *testing.T) {
	base, _ := url.Parse(DefaultAddonCatalogURL)
	for _, tc := range []struct {
		name   string
		change func(*Manifest)
	}{
		{"no contents", func(m *Manifest) { m.Addons[0].Contents = nil }},
		{"nested archive", func(m *Manifest) { m.Addons[0].Contents[0].Name = "units/AFark.ufo" }},
		{"escaping archive", func(m *Manifest) { m.Addons[0].Contents[0].Name = ".." }},
		{"not a UFO", func(m *Manifest) { m.Addons[0].Contents[0].Name = "AFark.hpi" }},
		{"repeated archive", func(m *Manifest) { m.Addons[0].Contents[1].Name = "afark.UFO" }},
		{"empty archive", func(m *Manifest) { m.Addons[0].Contents[0].Size = 0 }},
		{"bad archive digest", func(m *Manifest) { m.Addons[0].Contents[0].SHA256 = "bad" }},
		{"repeated add-on", func(m *Manifest) { m.Addons = append(m.Addons, m.Addons[0]) }},
		{"names a map", func(m *Manifest) { m.Addons[0].Map = "maps/a.ota" }},
		{"mixed with maps", func(m *Manifest) {
			m.Maps = []Entry{{ID: "m", Name: "M", Version: "1", Map: "maps/a.ota", Archive: m.Addons[0].Archive}}
		}},
		{"mixed with mods", func(m *Manifest) { m.Mods = []Entry{{ID: "m", Name: "M", Version: "1", Archive: m.Addons[0].Archive}} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := addonManifest()
			tc.change(&m)
			raw, _ := json.Marshal(m)
			if _, err := parseManifest(raw, base, originOf(base)); err == nil {
				t.Fatal("accepted invalid manifest")
			}
		})
	}
}
