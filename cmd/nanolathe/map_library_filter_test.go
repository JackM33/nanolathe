package main

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/modfetch"
)

func filterFixture() []modfetch.Entry {
	size := func(mb int64) modfetch.Archive { return modfetch.Archive{Size: mb << 20} }
	return []modfetch.Entry{
		{ID: "sherwood", Name: "Sherwood", Author: "Kikoz", Kind: "green", Year: 1998, Players: 4,
			Summary: "The classic forest map", Archive: size(2), AI: "Default"},
		{ID: "rorshock", Name: "Ror Shock", Author: "Gnome", Kind: "metal", Year: 2002, Players: 10,
			Summary: "Metal chokepoint", Archive: size(5)},
		{ID: "iceberg", Name: "Ice Berg", Author: "Kikoz", Kind: "ice", Year: 1997, Players: 2,
			Summary: "Cold island", Archive: size(1)},
		{ID: "solar", Name: "Solar System", Author: "", Kind: "space", Year: 2010, Players: 8,
			Summary: "Wacky space world", Archive: size(8), AI: "yes"},
	}
}

func TestMapFilterOrderSearchAndKind(t *testing.T) {
	entries := filterFixture()
	names := func(order []int) string {
		var out []string
		for _, index := range order {
			out = append(out, entries[index].Name)
		}
		return strings.Join(out, ",")
	}
	if got := names(mapFilterOrder(entries, "", "")); got != "Ice Berg,Ror Shock,Sherwood,Solar System" {
		t.Fatalf("name order = %s", got)
	}
	if got := names(mapFilterOrder(entries, "sher", "")); got != "Sherwood" {
		t.Fatalf("query by name = %s", got)
	}
	if got := names(mapFilterOrder(entries, "kikoz", "")); got != "Ice Berg,Sherwood" {
		t.Fatalf("query by author = %s", got)
	}
	if got := names(mapFilterOrder(entries, "CHOKEPOINT", "")); got != "Ror Shock" {
		t.Fatalf("query by summary, case-insensitive = %s", got)
	}
	if got := names(mapFilterOrder(entries, "", "space")); got != "Solar System" {
		t.Fatalf("kind filter = %s", got)
	}
	if got := names(mapFilterOrder(entries, "zzz", "")); got != "" {
		t.Fatalf("no match = %s", got)
	}
	// Equal names fall back to the id, stably.
	tied := []modfetch.Entry{{ID: "b", Name: "Same"}, {ID: "a", Name: "Same"}}
	order := mapFilterOrder(tied, "", "")
	if len(order) != 2 || tied[order[0]].ID != "a" || tied[order[1]].ID != "b" {
		t.Fatalf("tie order = %v", order)
	}
}

func TestMapKindsDedupeAndOrder(t *testing.T) {
	entries := filterFixture()
	entries = append(entries, modfetch.Entry{ID: "dup", Name: "Dup", Kind: "Green"}, modfetch.Entry{ID: "none", Name: "None"})
	kinds := mapKinds(entries)
	if strings.Join(kinds, ",") != "green,ice,metal,space" {
		t.Fatalf("kinds = %v", kinds)
	}
}

func TestMapEntryStatsAndSuffix(t *testing.T) {
	entries := filterFixture()
	if got := mapEntryStats(entries[0]); got != "by Kikoz \u00b7 4p \u00b7 2.1 MB" {
		t.Fatalf("stats = %q", got)
	}
	if got := mapEntryStats(modfetch.Entry{ID: "bare"}); got != "" {
		t.Fatalf("bare stats = %q", got)
	}
	if got := mapEntryKindSuffix(entries[0]); got != "green" {
		t.Fatalf("suffix = %q", got)
	}
	if got := mapEntryKindSuffix(modfetch.Entry{ID: "bare"}); got != "" {
		t.Fatalf("bare suffix = %q", got)
	}
}

func TestMapSearchQueryPlaceholderPolicy(t *testing.T) {
	cases := []struct {
		text     string
		captured bool
		query    string
		box      string
	}{
		{mapSearchHint, false, "", mapSearchHint},
		{mapSearchHint, true, "", ""},
		{"", false, "", mapSearchHint},
		{"", true, "", ""},
		{"sher", false, "sher", "sher"},
		{"sher", true, "sher", "sher"},
	}
	for _, tc := range cases {
		query, box := mapSearchQuery(tc.text, tc.captured)
		if query != tc.query || box != tc.box {
			t.Fatalf("mapSearchQuery(%q, %v) = %q, %q; want %q, %q", tc.text, tc.captured, query, box, tc.query, tc.box)
		}
	}
}

// The browser's controls sit in the window's free bands: the search box above
// the list, the cycle buttons between the preview and Load.
func TestBuildMapsPreviewWindowBrowserControls(t *testing.T) {
	w := &gui.Window{Gadgets: []gui.Gadget{
		{Name: "DESCRIPTION", Kind: gui.KindLabel, Active: 1, Rect: gui.Rect{X: 60, Y: 300, W: 235, H: 31}},
		{Name: "LOAD", Kind: gui.KindButton, Active: 1, Rect: gui.Rect{X: 357, Y: 325, W: 96, H: 20}},
		{Name: "PREVMENU", Kind: gui.KindButton, Active: 1, Rect: gui.Rect{X: 356, Y: 360, W: 96, H: 20}},
		{Name: "STATUS", Kind: gui.KindLabel, Active: 1, Rect: gui.Rect{X: 352, Y: 150, W: 116, H: 48}},
		{Name: "MAPNAMES", Kind: gui.KindListBox, Active: 1, Rect: gui.Rect{X: 60, Y: 86, W: 230, H: 193}},
	}}
	buildMapsPreviewWindow(w)
	if w.GadgetIndex("MAPSEARCH") < 0 || w.GadgetIndex("MAPTYPE") < 0 {
		t.Fatal("browser controls missing")
	}
	gadget := func(name string) gui.Rect { return w.Gadgets[w.GadgetIndex(name)].Rect }
	list := gadget("MAPNAMES")
	search := gadget("MAPSEARCH")
	if search.Y+search.H > list.Y {
		t.Fatalf("search box overlaps the list: %+v vs %+v", search, list)
	}
	if w.Gadgets[w.GadgetIndex("MAPSEARCH")].Kind != gui.KindTextBox {
		t.Fatal("search control is not a text box")
	}
	load, back := gadget("LOAD"), gadget("PREVMENU")
	rect := gadget("MAPTYPE")
	if rect.X < 352 || rect.X+rect.W > 460 || rect.Y < 266 || rect.Y+rect.H > 325 {
		t.Fatalf("MAPTYPE is outside the free column: %+v", rect)
	}
	if overlaps(rect, load) || overlaps(rect, back) {
		t.Fatalf("MAPTYPE overlaps an action button: %+v", rect)
	}
}

func overlaps(a, b gui.Rect) bool {
	return a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H
}
