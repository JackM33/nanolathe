package main

// The map browser's search and filter are pure functions over the catalogue's
// entries so they can be tested without a panel or a network
// (docs/DESIGN_MODS_MUTATORS.md §5.6 "Optional presentation metadata").

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/modfetch"
)

// mapKinds lists the distinct non-empty world-kind labels in the catalogue,
// alphabetically, for the Type cycle's options.
func mapKinds(entries []modfetch.Entry) []string {
	seen := map[string]bool{}
	var kinds []string
	for _, entry := range entries {
		kind := strings.TrimSpace(entry.Kind)
		if kind == "" || seen[strings.ToLower(kind)] {
			continue
		}
		seen[strings.ToLower(kind)] = true
		kinds = append(kinds, kind)
	}
	sort.Slice(kinds, func(i, j int) bool {
		return strings.ToLower(kinds[i]) < strings.ToLower(kinds[j])
	})
	return kinds
}

// mapFilterOrder returns the entry indices the browser shows, in name order.
// A query matches the name, the author or the summary, case insensitively;
// kind is the Type cycle's filter.
func mapFilterOrder(entries []modfetch.Entry, query, kind string) []int {
	query = strings.ToLower(strings.TrimSpace(query))
	kind = strings.ToLower(strings.TrimSpace(kind))
	var order []int
	for i, entry := range entries {
		if kind != "" && !strings.EqualFold(strings.TrimSpace(entry.Kind), kind) {
			continue
		}
		if query != "" {
			haystack := strings.ToLower(entry.Name + "\n" + entry.Author + "\n" + entry.Summary)
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		order = append(order, i)
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := entries[order[i]], entries[order[j]]
		if !strings.EqualFold(a.Name, b.Name) {
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		}
		return a.ID < b.ID
	})
	return order
}

// mapEntryStats is the selected entry's fact line: the author credit, the
// player count, the map's dimensions and the download size. The list row
// already carries the world kind, players and year, so this line stays short
// enough for the retail font.
func mapEntryStats(entry modfetch.Entry) string {
	var parts []string
	if author := strings.TrimSpace(entry.Author); author != "" {
		parts = append(parts, "by "+author)
	}
	if entry.Players > 0 {
		parts = append(parts, fmt.Sprintf("%dp", entry.Players))
	}
	if size := strings.TrimSpace(entry.SizeMap); size != "" {
		parts = append(parts, size)
	}
	if entry.Archive.Size > 0 {
		parts = append(parts, fmt.Sprintf("%.1f MB", float64(entry.Archive.Size)/1e6))
	}
	return strings.Join(parts, " \u00b7 ")
}

// mapEntryKindSuffix labels an uninstalled entry's list row with the world
// kind. The row is one clipped line, so the player count, dimensions and year
// stay in the stats line.
func mapEntryKindSuffix(entry modfetch.Entry) string {
	return strings.TrimSpace(entry.Kind)
}
