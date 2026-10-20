package main

import (
	"strconv"
	"strings"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/modfetch"
	"github.com/nanolathe-gg/nanolathe/internal/platform/screenkit"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// layoutScreen is a Nanolathe screen drawn on the design canvas.
func layoutScreen(t *testing.T) (*nlScreen, *ebiten.Image) {
	t.Helper()
	oldW, oldH := nlScreenW, nlScreenH
	nlScreenW, nlScreenH = 1560, 900
	t.Cleanup(func() { nlScreenW, nlScreenH = oldW, oldH })
	_, s := settingsRegressionScreen(nil, settings.Defaults())
	s.fonts, s.art, s.ready = screenkit.LoadFonts(), &nlArt{}, true
	s.pageT, s.heroT = 1, 1
	screen := ebiten.NewImage(1560, 900)
	t.Cleanup(screen.Deallocate)
	return s, screen
}

// hotIDs is every region the pointer finds on a grid over r.
func hotIDs(s *nlScreen, r screenkit.Rect) map[string]bool {
	ids := map[string]bool{}
	for y := r.Y + 2; y < r.Y+r.H; y += 6 {
		for x := r.X + 2; x < r.X+r.W; x += 6 {
			s.hits.Update(screenkit.Input{X: x, Y: y}, 0)
			if id := s.hits.Hot(); id != "" {
				ids[id] = true
			}
		}
	}
	return ids
}

func selectPage(s *nlScreen, key string, kind nlKind) (nlPage, int) {
	for pi, p := range s.pages() {
		if p.key != key {
			continue
		}
		s.page = pi
		for ci, c := range p.cards {
			if c.kind == kind {
				s.focus[pi] = ci
				return p, ci
			}
		}
		return p, 0
	}
	panic("no page " + key)
}

// A page with more cards than one row holds shows every card on the screen,
// in two rows of the single row's height, rather than leaving some to scroll
// into view.
func TestNLScreenCarouselFitsEveryCard(t *testing.T) {
	s, screen := layoutScreen(t)
	page, _ := selectPage(s, "mutators", nlRestrictions)
	if float64(len(page.cards))*240 <= 1448 {
		t.Fatalf("the Mutators page has %d cards, which already fit one row", len(page.cards))
	}
	draw := func() {
		s.hits.Begin()
		s.drawCarousel(screen, page, 0, 1)
		s.hits.End()
	}
	draw()
	draw()
	top := float64(s.carouselTop())
	ids := hotIDs(s, screenkit.Rect{W: 1560, H: 900})
	for _, c := range page.cards {
		if !ids[s.ui.id("card-", c.key, -1, -1)] {
			t.Errorf("card %s is not on the screen", c.key)
		}
	}
	if ids["car-right"] || ids["car-left"] {
		t.Error("the cards still scroll")
	}
	// The rows keep to the single row's band.
	if below := hotIDs(s, screenkit.Rect{Y: top + 196, W: 1560, H: 900 - top - 196}); len(below) > 0 {
		t.Errorf("cards reach below the carousel band: %v", below)
	}
}

// The Add-ons list ends above the cards, however many add-ons there are, and
// the wheel reaches its last row.
func TestNLScreenAddonListStopsAboveCards(t *testing.T) {
	s, screen := layoutScreen(t)
	var rows []nlAddonRow
	for i := range 9 {
		e := &modfetch.Entry{ID: "a" + strconv.Itoa(i), Name: "Add-on " + strconv.Itoa(i), Version: "1", Summary: "Units beside Arm and Core."}
		rows = append(rows, nlAddonRow{known: knownAddon{id: e.ID, version: e.Version, name: e.Name, entry: e}})
	}
	s.addonList.fetched, s.addonList.rows = true, rows
	page, idx := selectPage(s, "game", nlAddons)
	draw := func() {
		s.hits.Begin()
		s.drawHero(screen, page, &page.cards[idx], idx, page.cards[idx].get(&s.draft))
		s.hits.End()
	}
	draw()
	list := s.addonScroll.area
	if list.Y+list.H > float64(s.carouselTop())-14 {
		t.Fatalf("the list ends at %.0f, under the lifted cards at %.0f", list.Y+list.H, float64(s.carouselTop())-14)
	}
	if s.addonScroll.shown >= len(rows) {
		t.Fatalf("all %d rows fit; the fixture no longer needs scrolling", len(rows))
	}
	s.updateInput(screenkit.Input{X: list.X + 20, Y: list.Y + 20, WheelY: -float64(len(rows))}, 0)
	draw()
	if !hotIDs(s, s.addonScroll.area)[s.ui.id("addon-get-", "a8", 8, -1)] {
		t.Fatal("the wheel did not bring the last add-on's Get into view")
	}
}

// Every mod in the catalogue can be reached, and a summary never runs under
// its row's button.
func TestNLScreenCatalogScrollsAndFitsSummaries(t *testing.T) {
	s, screen := layoutScreen(t)
	var entries []modfetch.Entry
	for i := range 9 {
		entries = append(entries, modfetch.Entry{ID: "m" + strconv.Itoa(i), Name: "Mod " + strconv.Itoa(i), Version: "1",
			Summary: strings.Repeat("A summary long enough to need a second line and then some more. ", 3)})
	}
	s.dialog = "catalog"
	s.catalog = &nlCatalog{entries: entries}
	draw := func() {
		s.hits.Begin()
		s.drawCatalog(screen)
		s.hits.End()
	}
	draw()
	list := s.catalogScroll.area
	if s.catalogScroll.shown >= len(entries) {
		t.Fatalf("all %d mods fit; the fixture no longer needs scrolling", len(entries))
	}
	// The summary's lines are the ones drawn; each fits left of the button.
	u := s.u()
	// The rows narrow for the scrollbar, which the wheel target includes.
	room := (list.W - 50*u) - 170*u - 32*u
	st := screenkit.Style{Size: 11 * u}
	for _, line := range s.ui.wrap(s.fonts.Body, entries[0].Summary, st, room) {
		if w := s.fonts.Body.Measure(line, st); w > room {
			t.Fatalf("a summary line is %.0f wide in %.0f of room", w, room)
		}
	}
	s.updateInput(screenkit.Input{X: list.X + 20, Y: list.Y + 20, WheelY: -float64(len(entries))}, 0)
	draw()
	if !hotIDs(s, screenkit.Rect{W: 1560, H: 900})[s.ui.id("catalog-get", "", len(entries)-1, -1)] {
		t.Fatal("the wheel did not bring the last mod's Download into view")
	}
}
