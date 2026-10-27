package main

import (
	"os"
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// A search finds a map through any tag, bracket or punctuation in its name:
// every typed word need only occur somewhere in the name's letters and
// digits. These are the user's own examples, all found by "Map Name".
func TestMapSearchMatchesTaggedNames(t *testing.T) {
	for _, tc := range []struct {
		query, name string
		want        bool
	}{
		{"Map Name", "[V] Map name", true},
		{"Map Name", "[Jack's Maps] Map name", true},
		{"Map Name", "Map Name", true},
		{"map name", "[V] Map name", true},
		{"mapname", "[Jack's Maps] Map name", true},
		{"Name Map", "Map Name", true},
		{"map nam", "[V] Map name", true},
		{"  Map   Name  ", "[V] Map name", true},
		{"jack", "[Jack's Maps] Map name", true},
		{"Jack's", "[Jack's Maps] Map name", true},
		{"[V]", "[V] Map name", true},
		{"", "Anything", true},
		{"jack", "[V] Map name", false},
		{"jack", "Map Name", false},
		{"Map Names", "Map Name", false},
		{"Map Nmae", "[V] Map name", false},
		{"Map Name", "Coast to Coast", false},
		{"Map Name x", "Map Name", false},
	} {
		if got := mapSearchMatches(mapSearchWords(tc.query), tc.name); got != tc.want {
			t.Errorf("%q finds %q = %v, want %v", tc.query, tc.name, got, tc.want)
		}
	}
	// The file stem is searched too, and the list keeps its own order.
	names := []string{"acid", "vmapname", "coast", "jmn", "mapname"}
	labels := []string{"Acid Pools", "[V] Map name", "Coast to Coast", "[Jack's Maps] Map name", ""}
	if got := mapSearchRows(names, labels, "Map Name"); !slices.Equal(got, []int{1, 3, 4}) {
		t.Errorf("rows = %v, want [1 3 4]", got)
	}
	if got := mapSearchRows(names, labels, "jmn"); !slices.Equal(got, []int{3}) {
		t.Errorf("stem rows = %v, want [3]", got)
	}
	if got := mapSearchRows(names, labels, " "); len(got) != len(names) {
		t.Errorf("an empty search shows %d of %d maps", len(got), len(names))
	}
}

// mapSearchWindow is a SELMAP-shaped window: a list with its scrollbar, the
// preview, the two text lines and the two buttons, Escape going back.
func mapSearchWindow() *gui.Window {
	w := &gui.Window{Name: "guis/selmap.gui", Rect: gui.Rect{W: 494, H: 420}, Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel, Active: 1},
		{Kind: gui.KindListBox, Name: "MAPNAMES", Active: 1, Assoc: 1, ItemHeight: 16, Rect: gui.Rect{X: 60, Y: 86, W: 235, H: 203}},
		{Kind: gui.KindScrollBar, Name: "SLIDER", Active: 1, Assoc: 1, Rect: gui.Rect{X: 298, Y: 86, W: 16, H: 203}},
		{Kind: gui.KindSurface, Name: "MAPPIC", Active: 1, Rect: gui.Rect{X: 330, Y: 120, W: 140, H: 140}},
		{Kind: gui.KindLabel, Name: "DESCRIPTION", Active: 1, Rect: gui.Rect{X: 60, Y: 300, W: 235, H: 31}},
		{Kind: gui.KindLabel, Name: "SIZE", Active: 1, Rect: gui.Rect{X: 60, Y: 334, W: 235, H: 18}},
		{Kind: gui.KindButton, Name: "LOAD", Active: 1, Rect: gui.Rect{X: 330, Y: 370, W: 96, H: 31}},
		{Kind: gui.KindButton, Name: "PREVMENU", Active: 1, Rect: gui.Rect{X: 60, Y: 370, W: 96, H: 31}},
	}}
	w.Header.EscDefault = "PREVMENU"
	return w
}

// mapSearchOverlaps names the controls the box's frame covers, and the
// window when the frame leaves it.
func mapSearchOverlaps(p *ui.Panel) []string {
	box := p.Index(mapPickerSearchName)
	b := int32(mapPickerSearchBevel)
	r := p.Window.PlacedRect(box)
	frame := gui.Rect{X: r.X - b, Y: r.Y - b, W: r.W + 2*b, H: r.H + 2*b}
	var names []string
	if w := p.Window.Rect; frame.X < w.X || frame.Y < w.Y || frame.X+frame.W > w.X+w.W || frame.Y+frame.H > w.Y+w.H {
		names = append(names, "the window's edge")
	}
	for i, gad := range p.Window.Gadgets {
		if i == 0 || i == box || !p.ActiveAt(i) {
			continue
		}
		o := p.Window.PlacedRect(i)
		if o.X < frame.X+frame.W && frame.X < o.X+o.W && o.Y < frame.Y+frame.H && frame.Y < o.Y+o.H {
			names = append(names, gad.Name)
		}
	}
	return names
}

func mapSearchClick(g *gameShell, p *ui.Panel, in *input.State, x, y int32) {
	in.Mouse.SetPosition(float32(x), float32(y))
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	g.serviceMenuWidgets(p, in)
	in.Mouse.ResetEdges()
	in.Mouse.SetButton(input.MouseButtonLeft, false)
	g.serviceMenuWidgets(p, in)
	in.Mouse.ResetEdges()
}

func mapSearchKeys(g *gameShell, p *ui.Panel, in *input.State, tokens ...input.Token) {
	for _, token := range tokens {
		in.EnqueueToken(token)
	}
	g.serviceMenuWidgets(p, in)
}

func mapSearchText(text string) []input.Token {
	var tokens []input.Token
	for _, r := range text {
		tokens = append(tokens, input.Token{Kind: input.TokenText, Rune: r})
	}
	return tokens
}

// The picker's search narrows its list as the player types, and every way a
// row becomes a map — a click, Up and Down in the box, Load — reaches the
// map that row shows, not the map at that index of the whole list. Escape
// clears the search before it goes back, and the battle's map changes only
// on Load.
func TestMapPickerSearchFiltersAndChooses(t *testing.T) {
	names := []string{"Acid Pools", "[V] Map name", "Coast to Coast", "[Jack's Maps] Map name", "Map Name", "Mapping Lane"}
	g := &gameShell{
		frontend: ui.NewFrontend(modeMenuSkirmish),
		font:     fixedWidthFont(10),
		assets: &menuAssets{panel: map[shellMode]*retailPanelAssets{
			modeMenuSkirmish: {window: &gui.Window{Name: "guis/skirmish.gui", Rect: gui.Rect{W: 640, H: 480}}},
			modeMenuMap:      {window: mapSearchWindow()},
		}},
		maps:      names,
		mapLabels: slices.Clone(names),
		mapReturn: modeMenuSkirmish,
	}
	g.setup.MapName = names[0]
	g.openMenu(modeMenuMap)
	p := g.activePanel()
	box := p.Index(mapPickerSearchName)
	if box < 0 || p.TextAt(box) != mapSearchHint {
		t.Fatalf("picker has no search box showing its hint")
	}
	if cached := g.assets.panel[modeMenuMap].window; cached.GadgetIndex(mapPickerSearchName) >= 0 || cached.Gadgets[1].Rect != mapSearchWindow().Gadgets[1].Rect {
		t.Fatal("the cached authored window changed; each open must start from it")
	}
	listRect, boxRect := p.Window.PlacedRect(p.Index("MAPNAMES")), p.Window.PlacedRect(box)
	if boxRect.X != listRect.X+mapPickerSearchBevel || boxRect.Y >= listRect.Y || boxRect.W <= listRect.W {
		t.Fatalf("box %+v does not head list %+v and its bar", boxRect, listRect)
	}
	for _, name := range mapSearchOverlaps(p) {
		t.Errorf("the box's frame overlaps %s", name)
	}
	rows := func() []string { items, _, _, _ := p.ListValues("MAPNAMES"); return items }
	selected := func() int { _, s, _, _ := p.ListValues("MAPNAMES"); return s }
	if !slices.Equal(rows(), names) {
		t.Fatalf("unsearched list = %q", rows())
	}

	in := bindingClient(t).Input()
	mapSearchClick(g, p, in, boxRect.X+4, boxRect.Y+4)
	if !p.EditorCaptured() || p.EditorIndex() != box || p.TextAt(box) != "" {
		t.Fatalf("clicking the box: captured %v at %d, text %q", p.EditorCaptured(), p.EditorIndex(), p.TextAt(box))
	}
	mapSearchKeys(g, p, in, mapSearchText("Map Name")...)
	want := []string{"[V] Map name", "[Jack's Maps] Map name", "Map Name"}
	if !slices.Equal(rows(), want) || g.mapIdx != 1 || selected() != 0 {
		t.Fatalf("search rows %q, map %d, row %d", rows(), g.mapIdx, selected())
	}
	mapSearchKeys(g, p, in, input.Token{Kind: input.TokenEdit, Key: input.KeyDown})
	if g.mapIdx != 3 || selected() != 1 || !p.EditorCaptured() {
		t.Fatalf("Down in the box: map %d, row %d, captured %v", g.mapIdx, selected(), p.EditorCaptured())
	}

	escape := input.Token{Kind: input.TokenEdit, Key: input.KeyEscape}
	mapSearchKeys(g, p, in, escape)
	if g.frontend.Mode != modeMenuMap || !slices.Equal(rows(), names) || g.mapIdx != 3 || selected() != 3 {
		t.Fatalf("Escape: mode %v, rows %q, map %d, row %d", g.frontend.Mode, rows(), g.mapIdx, selected())
	}
	mapSearchKeys(g, p, in, mapSearchText("jack zzz")...)
	if len(rows()) != 0 || mapPickerShowsSelection(p) || p.TextOf("DESCRIPTION") != "" {
		t.Fatalf("a search matching nothing shows %q", rows())
	}
	g.activateGadget("LOAD")
	if g.frontend.Mode != modeMenuMap || g.setup.MapName != names[0] {
		t.Fatal("Load with nothing shown chose a map")
	}
	backspace := input.Token{Kind: input.TokenEdit, Key: input.KeyBackspace}
	mapSearchKeys(g, p, in, backspace, backspace, backspace, backspace)
	if !slices.Equal(rows(), []string{"[Jack's Maps] Map name"}) || g.mapIdx != 3 {
		t.Fatalf("jack: rows %q, map %d", rows(), g.mapIdx)
	}

	// A click on the searched list's second row picks the map it shows.
	mapSearchKeys(g, p, in, escape)
	mapSearchKeys(g, p, in, mapSearchText("o")...)
	if !slices.Equal(rows(), []string{"Acid Pools", "Coast to Coast"}) || g.mapIdx != 0 {
		t.Fatalf("o: rows %q, map %d", rows(), g.mapIdx)
	}
	mapSearchClick(g, p, in, listRect.X+10, listRect.Y+2+16+8)
	if g.mapIdx != 2 || selected() != 1 || p.EditorCaptured() {
		t.Fatalf("clicked row 1: map %d, row %d, box captured %v", g.mapIdx, selected(), p.EditorCaptured())
	}
	if g.setup.MapName != names[0] {
		t.Fatal("highlighting a map changed the battle's map")
	}
	// The box's text survives losing focus, and Escape outside it goes back.
	if p.TextAt(box) != "o" {
		t.Fatalf("box text %q after the click", p.TextAt(box))
	}
	g.activateGadget("LOAD")
	if g.setup.MapName != "Coast to Coast" || g.frontend.Mode == modeMenuMap {
		t.Fatalf("Load chose %q", g.setup.MapName)
	}
}

// A downloaded map's X follows the row its map is shown on, in the picker's
// searched list and in the catalogue's filtered one alike.
func TestMapRemoveRowsFollowSearchedRows(t *testing.T) {
	p := ui.NewPanel(&gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel}, {Name: "MAPNAMES", Kind: gui.KindListBox, Active: 1, Attribs: 1, ItemHeight: 16, Rect: gui.Rect{X: 20, Y: 30, W: 100, H: 80}}}})
	p.FillTextListAt(1, []string{"downloaded", "retail"}, nil, 8)
	g := &gameShell{cs: &contentSet{}, frontend: ui.NewFrontend(modeMenuMap)}
	target := &modlibrary.Mod{ID: "downloaded"}
	logical := []string{"maps/retail.ota", "maps/other.ota", "maps/downloaded.ota"}
	g.mapRemovals = mapRemovalRows{panel: p, content: g.cs, logical: logical, targets: []*modlibrary.Mod{nil, nil, target}, rows: []int{2, 0}}
	if _, ok := g.mapRemoveRowRect(p, 1, 0); !ok {
		t.Fatal("the downloaded map's row lacks its X")
	}
	if _, ok := g.mapRemoveRowRect(p, 1, 1); ok {
		t.Fatal("the retail map's row has an X")
	}
	if path, got := g.mapRemovals.at(0); path != "maps/downloaded.ota" || got != target {
		t.Fatalf("row 0 removes %q", path)
	}
}

// TestRetailMapPickerSearch opens the real map picker from the skirmish
// screen, checks the box takes no authored control's place, and searches by
// one word of a map's name. With NANOLATHE_MAP_SEARCH_SHOT set it writes the
// searched picker as a PNG there.
func TestRetailMapPickerSearch(t *testing.T) {
	g, _, cl := retailAssetShell(t)
	if len(g.maps) == 0 {
		t.Skip("no multiplayer maps")
	}
	g.openMenu(modeMenuSkirmish)
	g.activateGadget("SelectMap")
	p := g.activePanel()
	if g.frontend.Mode != modeMenuMap || p == nil {
		t.Skip("map picker unavailable")
	}
	box := p.Index(mapPickerSearchName)
	if box < 0 {
		t.Fatal("picker has no search box")
	}
	r := p.Window.PlacedRect(box)
	for _, name := range mapSearchOverlaps(p) {
		t.Errorf("the box's frame overlaps %s", name)
	}
	query, at := "", -1
	for i := range g.maps {
		for _, word := range mapSearchWords(g.mapListLabel(i)) {
			if len(word) >= 3 && len([]rune(word)) == len(word) {
				query, at = word, i
				break
			}
		}
		if at >= 0 {
			break
		}
	}
	if at < 0 {
		t.Skip("no map label with an ASCII word")
	}
	in := cl.Input()
	mapSearchClick(g, p, in, r.X+4, r.Y+4)
	mapSearchKeys(g, p, in, mapSearchText(query)...)
	items, _, _, _ := p.ListValues("MAPNAMES")
	rows := g.mapPickerRows(p)
	if len(items) == 0 || len(items) != len(rows) || !slices.Contains(rows, at) {
		t.Fatalf("%q shows %d rows, map %d among %v", query, len(items), at, rows)
	}
	for row, i := range rows {
		if items[row] != g.mapListLabel(i) || !mapSearchMatches([]string{query}, g.mapListLabel(i), g.maps[i]) {
			t.Fatalf("%q shows %q for map %q", query, items[row], g.maps[i])
		}
	}
	writeShellShot(t, cl, os.Getenv("NANOLATHE_MAP_SEARCH_SHOT"))
}
