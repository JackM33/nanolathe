// Package profiles describes a mounted content set's load-time facts: which
// directories hold each authored family, how large its tables and files may
// be, and which front-end art it replaces.
//
// A content profile is load-time data, not a gameplay rule set. It is chosen
// before the catalog compiles and, by itself, never changes what a tick does:
// the catalog keeps asking for `units/`, `weapons/` and the rest, and the
// profile's directory table answers with whatever the content set actually
// ships. That is sanctioned content policy, documented in
// docs/DESIGN_CONTENT_VFS.md §5 "Content profiles" and listed in
// docs/INVARIANTS.md I11.
//
// The engine carries no mod's profile. The base game's is Retail; a mod's is
// the `content` section of its own nanolathe-mod.json, which the mod library
// reads through Parse (docs/DESIGN_MODS_MUTATORS.md §4.2). Rule declarations
// in the same file — the gameplay minimum and the Community feature table —
// are not part of a profile and never pass through this package.
package profiles

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

// RetailName is the name every content set without a Nanolathe config
// reports: the base game, and a mod mounted as plain content.
const RetailName = "retail"

// The controls presets the running content may recommend, spelled as the
// keyboard profiles are (docs/DESIGN_MODS_MUTATORS.md §4.3).
const (
	ControlsCommunity = "community"
	ControlsRetail    = "retail"
	ControlsZero      = "zero"
)

// Limits records the table sizes and read caps a content set needs. The
// catalog compile reads all four through content.LimitsFromProfile, which
// keeps the retail value for any count left zero.
//
// Units and Weapons are definition-table sizes; TNTBytes and LOSBytes are the
// largest map and LOS table a loader may read. The per-player unit limit and
// the pathfinding step allowance a content set expects are Community feature
// values (`rules.communityFeatures.unitLimit` and `pathStepAllowance`), not
// content facts.
type Limits struct {
	Units    int   `json:"units,omitempty"`
	Weapons  int   `json:"weapons,omitempty"`
	TNTBytes int64 `json:"tnt_bytes,omitempty"`
	LOSBytes int64 `json:"los_bytes,omitempty"`
}

// Presentation carries optional mod-authored UI defaults, never simulation rules.
// Omitted placement_weapon_ranges keeps the Modern placement guide enabled.
type Presentation struct {
	// These logical resource paths are host presentation inputs. Empty fields
	// retain the retail assets; they never change the catalog or a rule set.
	MainMenuBackground     string `json:"main_menu_background,omitempty"`
	SinglePlayerBackground string `json:"single_player_background,omitempty"`
	LoadingBackground      string `json:"loading_background,omitempty"`
	TeamLogos              string `json:"team_logos,omitempty"`
	ShowRanges             bool   `json:"show_ranges,omitempty"`
	PlacementWeaponRanges  *bool  `json:"placement_weapon_ranges,omitempty"`
	// BuildMenuPageSize recommends a fixed capacity for adaptive build pages,
	// applied when neither the player nor mounted mod supplies a choice
	// (DESIGN_INTERFACE_HUD_INPUT §3.3 "Build page lock").
	BuildMenuPageSize int `json:"build_menu_page_size,omitempty"`
	// MainMenuVersion is the text the main menu writes into its `DebugString`
	// version label in place of the executable's `v3.1` literal, for a
	// package whose engine replaces that literal. Empty keeps `v3.1`.
	MainMenuVersion string `json:"main_menu_version,omitempty"`
}

// Profile is one content set's load-time description.
type Profile struct {
	// Name is what the reports carry as `content_profile`: RetailName, or the
	// id of the mod config the profile was read from. It is set by the
	// reader, never authored.
	Name string `json:"-"`
	// Detect lists logical directories the content set is known to ship. It
	// selects nothing: an install check refuses a package whose mounted
	// overlay lacks one (MissingMarkers), so a config paired with the wrong
	// content is caught before it is installed.
	Detect []string `json:"detect,omitempty"`
	// Directories maps the retail directory the loaders ask for to the
	// directory this content set ships. Keys are the retail names in lower
	// case; values are spelled as the content set spells them, though every
	// lookup is case-insensitive anyway [02 §2].
	Directories  map[string]string `json:"layout,omitempty"`
	Limits       Limits            `json:"limits,omitzero"`
	Presentation Presentation      `json:"presentation,omitzero"`
	// Addons set to false keeps every official add-on unmounted while this
	// content runs; omitted, they mount (docs/DESIGN_MODS_MUTATORS.md §4.2,
	// §5.7).
	Addons *bool `json:"addons,omitempty"`
}

// AddonsAllowed reports whether official add-ons mount with this content.
func (p Profile) AddonsAllowed() bool { return p.Addons == nil || *p.Addons }

// Retail is the base game's profile: no directory table, the retail limits
// (content.RetailLimits, which a zero Limits selects) and the retail
// front-end art. A mod without a Nanolathe config mounts under it too.
func Retail() Profile { return Profile{Name: RetailName} }

// Layout returns the first-segment redirection this profile applies. The
// retail profile's layout is empty, and an empty layout wraps nothing.
func (p Profile) Layout() vfs.Layout { return vfs.NewLayout(p.Directories) }

// MissingMarkers lists the Detect directories the mounted overlay does not
// resolve as directories, in the profile's order. An empty result means the
// content matches every marker the profile names.
func (p Profile) MissingMarkers(mounted vfs.FSOps) []string {
	var missing []string
	for _, marker := range p.Detect {
		if mounted == nil {
			missing = append(missing, marker)
			continue
		}
		if info, err := mounted.Stat(marker); err != nil || !info.IsDir {
			missing = append(missing, marker)
		}
	}
	return missing
}

// Parse decodes one `content` section and rejects a shape no loader could
// use. Unknown fields are refused so a typo in a hand-written config is
// reported instead of silently ignored. name becomes the profile's Name and
// origin names the document in a diagnostic.
func Parse(data []byte, name, origin string) (Profile, error) {
	fail := func(what, expected string) error {
		return fmt.Errorf("nanolathe: %s: logical path %s, providers searched [%s], expected %s", what, origin, origin, expected)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var profile Profile
	if err := decoder.Decode(&profile); err != nil {
		return Profile{}, fail("reading the content section failed: "+err.Error(), "a content section of detect, layout, limits and presentation")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Profile{}, fail("the content section has trailing data", "exactly one JSON object")
	}
	profile.Name = name
	if profile.Presentation.BuildMenuPageSize < 0 {
		return Profile{}, fail(fmt.Sprintf("content presentation build_menu_page_size %d is negative", profile.Presentation.BuildMenuPageSize), "a positive number of products per build page or omitted")
	}
	if profile.Limits.Units < 0 || profile.Limits.Weapons < 0 || profile.Limits.TNTBytes < 0 || profile.Limits.LOSBytes < 0 {
		return Profile{}, fail("a content limit is negative", "non-negative limits, or omitted for the retail value")
	}
	for _, marker := range profile.Detect {
		if strings.TrimSpace(marker) == "" || strings.ContainsAny(marker, "/\\") {
			return Profile{}, fail(fmt.Sprintf("content detect marker %q is not a single directory", marker), "top-level directory names without separators")
		}
	}
	for retail, target := range profile.Directories {
		if strings.TrimSpace(retail) == "" || strings.TrimSpace(target) == "" {
			return Profile{}, fail("a content layout row is empty", "a retail directory name and the directory this content set ships")
		}
		if strings.ContainsAny(retail, "/\\") || strings.ContainsAny(target, "/\\") {
			return Profile{}, fail("a content layout row is not a single directory", "two top-level directory names without separators")
		}
	}
	return profile, nil
}
