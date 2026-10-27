package main

// The map picker's search box: a host control over SELMAP's map list that
// narrows which maps the list shows while the player types
// (docs/DESIGN_INTERFACE_HUD_INPUT.md "Map search"). It is presentation
// only. The list still holds the census's maps in their sorted order, and
// the battle's map still changes only through Load or a double click.

import (
	"slices"
	"strings"
	"unicode"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// mapPickerSearchName names the box. It is not the catalogue's MAPSEARCH:
// the catalogue opens over the picker with a box of its own, and the Mods &
// Mutators windows load their own copy of SELMAP, so neither ever holds this
// one, and everything below that finds no such gadget leaves a list alone.
const mapPickerSearchName = "MAPFILTER"

const (
	// mapPickerSearchHeight is one line of the retail font, as the
	// catalogue's box is.
	mapPickerSearchHeight = 18
	// mapPickerSearchBevel is the width of the sunken frame round the box.
	mapPickerSearchBevel = 2
	// mapPickerSearchGap keeps the frame off the first list row.
	mapPickerSearchGap = 2
)

// installMapPickerSearch puts the box at the top of the map list. It takes
// the top of the list's own authored area, so it can never cover the
// window's artwork or another control; the list and its scrollbar give up
// that strip, and the box spans both. It runs before the scrollbar is
// built, so the bar's arrows follow its new top.
func installMapPickerSearch(w *gui.Window) {
	if w == nil {
		return
	}
	i := w.GadgetIndex("MAPNAMES")
	if i < 0 {
		return
	}
	list := &w.Gadgets[i]
	cut := int32(mapPickerSearchHeight + 2*mapPickerSearchBevel + mapPickerSearchGap)
	// A list too short to give up a line keeps its layout and gets no box.
	if list.Rect.H < 3*cut {
		return
	}
	strip := gui.Rect{X: list.Rect.X, Y: list.Rect.Y, W: list.Rect.W}
	list.Rect.Y += cut
	list.Rect.H -= cut
	for j := range w.Gadgets {
		bar := &w.Gadgets[j]
		if bar.Kind != gui.KindScrollBar || bar.Assoc == 0 || bar.Assoc != list.Assoc || bar.Rect.H < bar.Rect.W {
			continue
		}
		if bar.Rect.Y < list.Rect.Y {
			bar.Rect.H -= list.Rect.Y - bar.Rect.Y
			bar.Rect.Y = list.Rect.Y
		}
		if right := bar.Rect.X + bar.Rect.W; bar.Rect.X > strip.X && right > strip.X+strip.W {
			strip.W = right - strip.X
		}
	}
	w.Gadgets = append(w.Gadgets, gui.Gadget{
		Kind: gui.KindTextBox, Name: mapPickerSearchName, SourceName: mapPickerSearchName, Active: 1,
		Attribs: 1, ColorF: 15, MaxChars: 64, Text: mapSearchHint,
		Rect: gui.Rect{X: strip.X + mapPickerSearchBevel, Y: strip.Y + mapPickerSearchBevel, W: strip.W - 2*mapPickerSearchBevel, H: mapPickerSearchHeight},
	})
}

// mapSearchWords splits a search into the words a map must contain: each
// whitespace-separated word in its compact form, so "[V]" looks for "v" and
// "Jack's" for "jacks".
func mapSearchWords(query string) []string {
	var words []string
	for _, field := range strings.Fields(query) {
		if word := mapSearchCompact(field); word != "" {
			words = append(words, word)
		}
	}
	return words
}

// mapSearchCompact is a name's letters and digits, lowercased, with
// everything between them dropped: "[Jack's Maps] Map name" becomes
// "jacksmapsmapname". A tag, a bracket, punctuation or spacing then never
// stands between a search and the name it means.
func mapSearchCompact(name string) string {
	var b strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// mapSearchMatches reports whether every word occurs in the compact form of
// at least one of the names.
func mapSearchMatches(words []string, names ...string) bool {
	compact := make([]string, len(names))
	for i, name := range names {
		compact[i] = mapSearchCompact(name)
	}
	for _, word := range words {
		if !slices.ContainsFunc(compact, func(name string) bool { return strings.Contains(name, word) }) {
			return false
		}
	}
	return true
}

// mapSearchRows returns, in list order, the index of every map the query
// matches by its list label or its file stem; an empty query matches every
// map. Only names are read: like the list itself, the search opens no map.
func mapSearchRows(names, labels []string, query string) []int {
	words := mapSearchWords(query)
	rows := make([]int, 0, len(names))
	for i, name := range names {
		label := ""
		if i < len(labels) {
			label = labels[i]
		}
		if len(words) == 0 || mapSearchMatches(words, label, name) {
			rows = append(rows, i)
		}
	}
	return rows
}

// mapPickerQuery is the search typed in p's box, "" when p has none.
func mapPickerQuery(p *ui.Panel) string {
	if p == nil || p.Window == nil {
		return ""
	}
	index := p.Index(mapPickerSearchName)
	if index < 0 {
		return ""
	}
	query, _ := mapSearchQuery(p.TextAt(index), p.EditorCaptured() && p.EditorIndex() == index)
	return query
}

// mapPickerRows maps each row of p's map list to its index in g.maps. The
// list is rebuilt whenever the query changes (syncMapPickerSearch), so the
// rows worked out here are the ones it shows.
func (g *gameShell) mapPickerRows(p *ui.Panel) []int {
	return mapSearchRows(g.maps, g.mapLabels, mapPickerQuery(p))
}

// mapPickerMap is the index in g.maps of row of p's map list. A row the
// list does not have passes through unchanged, as every row did before the
// search, and the refresh that follows clamps it.
func (g *gameShell) mapPickerMap(p *ui.Panel, row int) int {
	if rows := g.mapPickerRows(p); row >= 0 && row < len(rows) {
		return rows[row]
	}
	return row
}

// mapListLabel is the text of map i's list row: its label, or its file stem
// when it has none.
func (g *gameShell) mapListLabel(i int) string {
	if i < len(g.mapLabels) && g.mapLabels[i] != "" {
		return g.mapLabels[i]
	}
	return g.maps[i]
}

// mapPickerShowsSelection reports whether p's map list shows a selected row.
// A search that matches nothing leaves the list empty, and then no map is
// described, previewed or loaded.
func mapPickerShowsSelection(p *ui.Panel) bool {
	items, selected, _, ok := p.ListValues("MAPNAMES")
	return !ok || (selected >= 0 && selected < len(items))
}

// syncMapPickerSearch follows live typing after each widget pass. The panel
// owns the box's text; this shows the hint in an empty box the player is not
// typing in, and rebuilds the list when the query differs from before, the
// query the list was built from.
func (g *gameShell) syncMapPickerSearch(p *ui.Panel, before string) {
	if p == nil || p.Window == nil {
		return
	}
	index := p.Index(mapPickerSearchName)
	if index < 0 {
		return
	}
	query, box := mapSearchQuery(p.TextAt(index), p.EditorCaptured() && p.EditorIndex() == index)
	if box != p.TextAt(index) {
		p.SetTextAt(index, box)
	}
	if query != before {
		g.refreshMapPanel()
	}
}

// serviceMapPickerSearchKeys gives the box the keys a player expects of a
// search while typing in it. The window's own key pass would send Escape to
// Back and Up or Down to the next control. Here Escape clears the search
// instead (once the box is empty it goes back as before), and Up and Down
// move the highlighted map through the filtered list, so a map can be found
// and chosen from the keyboard. Only the frame's first key is looked at: it
// is the one that pass would take.
func (g *gameShell) serviceMapPickerSearchKeys(p *ui.Panel, in *input.State) {
	if p == nil || p.Window == nil || in == nil {
		return
	}
	index := p.Index(mapPickerSearchName)
	if index < 0 || !p.EditorCaptured() || p.EditorIndex() != index {
		return
	}
	tokens := in.PeekTokens()
	if len(tokens) == 0 || tokens[0].Ctrl {
		return
	}
	token := tokens[0]
	switch {
	case token.Kind == input.TokenEdit && token.Key == input.KeyEscape, token.Kind == input.TokenText && token.Rune == '\x1b':
		if mapPickerQuery(p) == "" {
			return
		}
		p.SetTextAt(index, "")
		g.refreshMapPanel()
	case token.Kind == input.TokenEdit && (token.Key == input.KeyUp || token.Key == input.KeyDown):
		step := 1
		if token.Key == input.KeyUp {
			step = -1
		}
		rows := g.mapPickerRows(p)
		if at := slices.Index(rows, g.mapIdx); at >= 0 {
			g.mapIdx = rows[min(max(at+step, 0), len(rows)-1)]
			g.refreshMapPanel()
		}
	default:
		return
	}
	in.DiscardTokens(1)
}

// clearMapPickerSearch empties p's search, if it has one. A map chosen in
// the catalogue must show in the picker beneath it, whatever was typed there.
func clearMapPickerSearch(p *ui.Panel) {
	if p == nil || p.Window == nil {
		return
	}
	index := p.Index(mapPickerSearchName)
	if index < 0 {
		return
	}
	_, box := mapSearchQuery("", p.EditorCaptured() && p.EditorIndex() == index)
	p.SetTextAt(index, box)
}

// drawMapPickerSearchFrame sinks the box into the list's artwork with the
// bevel the retail fallback gives a pressed button: dark above and left,
// light below and right. It is drawn in the margin round the box, so the
// box's own fill, text and caret are untouched.
func (g *gameShell) drawMapPickerSearchFrame(c *client.Client, p *ui.Panel) {
	if p == nil || p.Window == nil {
		return
	}
	index := p.Index(mapPickerSearchName)
	if index < 0 || !p.ActiveAt(index) {
		return
	}
	r := p.Window.PlacedRect(index)
	b := mapPickerSearchBevel
	x, y, w, h := int(r.X)-b, int(r.Y)-b, int(r.W)+2*b, int(r.H)+2*b
	v := retailButtonVerdict(gui.Gadget{}, 0, 0, 1, 0, false)
	dark, light := g.guiColor(v.top), g.guiColor(v.bot)
	c.UIFillRect(x, y, w, b, dark)
	c.UIFillRect(x, y, b, h, dark)
	c.UIFillRect(x+b, y+h-b, w-b, b, light)
	c.UIFillRect(x+w-b, y+b, b, h-b, light)
}
