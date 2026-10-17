package main

// Official add-ons (docs/DESIGN_MODS_MUTATORS.md §5.7): Cavedog's bonus
// archives, hosted by Nanolathe and mounted with the base install at its
// *.UFO tier, apart from the mod slot, so a player can enable them and still
// select any mod.

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/addonlibrary"
	contentprofiles "github.com/nanolathe-gg/nanolathe/internal/content/profiles"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/replay"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func openAddonLibrary() (*modlibrary.Library, error) {
	root, err := addonlibrary.DefaultRoot()
	if err != nil {
		return nil, err
	}
	return addonlibrary.Open(root)
}

// requestedAddons is the add-on selection a mount applies: --addon (or a
// remount's own) when given, else the saved setting, which captures,
// benchmarks and displayless runs never read.
func requestedAddons(opts Options) []settings.AddonSelection {
	if opts.AddonsSet {
		return opts.Addons
	}
	if opts.ignoresSavedSelection() {
		return nil
	}
	stored, _ := settings.Load()
	return stored.Addons
}

// addonMount is what one mount makes of the add-on selection.
type addonMount struct {
	// mounted are the add-ons this mount holds from the library.
	mounted []addonlibrary.Addon
	// selection is what the mount was asked for, each entry's version
	// resolved, kept so a remount asks for the same.
	selection []settings.AddonSelection
	// off marks a running config that leaves add-ons off.
	off   bool
	notes []string
}

// resolveAddons decides which enabled add-ons a mount holds: none when the
// running config sets content.addons false; each installed one otherwise,
// except one already in the base folder, which the install supplies itself,
// and one that defines a unit or feature name the base or the mod defines at
// another path. A saved add-on that has gone is left out with a notice; one
// named by --addon fails the start, as --mod does (§5.7).
func resolveAddons(opts Options, profile contentprofiles.Profile, baseRoots []string, mod *modlibrary.Mod) (addonMount, error) {
	requested := requestedAddons(opts)
	var out addonMount
	if len(requested) == 0 || runtime.GOOS == "js" || len(baseRoots) == 0 {
		return out, nil
	}
	if !profile.AddonsAllowed() {
		out.off = true
		out.selection = append([]settings.AddonSelection{}, requested...)
		out.notes = append(out.notes, "nanolathe: the running mod leaves the official add-ons off")
		return out, nil
	}
	root, err := addonlibrary.DefaultRoot()
	if err != nil {
		return out, err
	}
	installed, skipped, err := addonlibrary.Installed(root)
	if err != nil {
		if opts.AddonsSet {
			return out, err
		}
		out.notes = append(out.notes, "nanolathe: official add-ons not mounted: "+noticeReason(err))
		return out, nil
	}
	for _, s := range skipped {
		out.notes = append(out.notes, fmt.Sprintf("nanolathe: official add-on not mounted: %s; delete %s", noticeReason(s.Err), s.Dir))
	}
	var candidates []addonlibrary.Addon
	for _, want := range requested {
		addon, ok := installedAddon(installed, want.ID, want.Version)
		if !ok {
			selector := modSelectorOf(want.ID, want.Version)
			if opts.AddonsSet {
				return out, &missingProductError{what: "official add-on is not installed", logical: selector, providers: []string{root}, expected: "an add-on downloaded from the Nanolathe screen"}
			}
			out.notes = append(out.notes, fmt.Sprintf("nanolathe: the enabled official add-on %s is not installed", selector))
			continue
		}
		out.selection = append(out.selection, settings.AddonSelection{ID: addon.ID, Version: addon.Version})
		if _, in := addonlibrary.InFolder(baseRoots[0], addon.Archives); in {
			continue
		}
		candidates = append(candidates, addon)
	}
	if len(candidates) == 0 {
		return out, nil
	}
	others, err := addonOthers(profile, baseRoots, mod, candidates)
	if err != nil {
		return out, err
	}
	defer others.close()
	for _, addon := range candidates {
		names, err := addonlibrary.DefinedNames(addon)
		if err == nil {
			var found string
			if found, err = addonlibrary.Conflict(others.fs, names); err == nil && found != "" {
				err = fmt.Errorf("the installed content already defines %s at another path", found)
			}
		}
		if err != nil {
			out.notes = append(out.notes, fmt.Sprintf("nanolathe: official add-on %s not mounted: %s", addon.Name, noticeReason(err)))
			continue
		}
		out.mounted = append(out.mounted, addon)
	}
	return out, nil
}

// installedAddon finds an installed add-on by id and version; an empty
// version selects the last one the library lists.
func installedAddon(installed []addonlibrary.Addon, id, version string) (addonlibrary.Addon, bool) {
	var found addonlibrary.Addon
	ok := false
	for _, a := range installed {
		if a.ID == id && (version == "" || a.Version == version) {
			found, ok = a, true
		}
	}
	return found, ok
}

// addonOthersFS is the content an add-on is checked against: the base roots
// and the mod, without the add-ons' archives or any of the base folder's of
// the same names, which they would replace.
type addonOthersFS struct {
	fs      vfs.FSOps
	mounted *vfs.FS
}

func (o addonOthersFS) close() { o.mounted.Close() }

func addonOthers(profile contentprofiles.Profile, baseRoots []string, mod *modlibrary.Mod, addons []addonlibrary.Addon) (addonOthersFS, error) {
	replaced := map[string]bool{}
	for _, a := range addons {
		for _, archive := range a.Archives {
			replaced[strings.ToLower(archive.Name)] = true
		}
	}
	roots := make([]vfs.GameRoot, 0, len(baseRoots)+1)
	for i, dir := range baseRoots {
		root := vfs.GameRoot{Dir: dir}
		if i == 0 {
			root.Archives = func(name string) bool { return !replaced[strings.ToLower(name)] }
		}
		roots = append(roots, root)
	}
	if mod != nil {
		roots = append(roots, vfs.GameRoot{Dir: mod.Dir})
	}
	mounted := vfs.New()
	if err := mounted.MountGameRoots(roots); err != nil {
		mounted.Close()
		return addonOthersFS{}, err
	}
	return addonOthersFS{fs: profile.Layout().Apply(mounted), mounted: mounted}, nil
}

// addonExtras are the archive files the mount adds to the base install's
// folder.
func addonExtras(addons []addonlibrary.Addon) []string {
	var paths []string
	for _, a := range addons {
		paths = append(paths, a.Paths...)
	}
	return paths
}

// remountAddons sets a remount's options to the add-ons the running content
// was asked for, so the fresh set holds what the battle ran on.
func (c *contentSet) remountAddons(opts *Options) {
	if c == nil {
		return
	}
	opts.Addons, opts.AddonsSet = append([]settings.AddonSelection{}, c.addonSelection...), true
}

func printNotes(notes []string) {
	for _, note := range notes {
		fmt.Fprintln(os.Stderr, note)
	}
}

// addonIdentity is an add-on a save or a replay recorded: its id, version
// and content identity (addonlibrary.Digest).
type addonIdentity struct {
	id, version string
	digest      [32]byte
}

// addonsSatisfied reports whether cs plays exactly the recorded add-ons:
// each mounted from the library or held by the base folder, and no other
// add-on mounted. known name the archives a folder copy is checked against.
func addonsSatisfied(cs *contentSet, want []addonIdentity, known []knownAddon) bool {
	wanted := make(map[addonIdentity]bool, len(want))
	for _, w := range want {
		wanted[w] = true
	}
	mounted := make(map[addonIdentity]bool, len(cs.addons))
	for _, a := range cs.addons {
		id := addonIdentity{a.ID, a.Version, addonlibrary.Digest(a.Archives)}
		if !wanted[id] {
			return false
		}
		mounted[id] = true
	}
	for _, w := range want {
		if !mounted[w] && !addonInFolder(cs, w, known) {
			return false
		}
	}
	return true
}

func addonInFolder(cs *contentSet, w addonIdentity, known []knownAddon) bool {
	for _, k := range known {
		if k.id == w.id && k.version == w.version && addonlibrary.Digest(k.archives) == w.digest {
			if _, in := addonlibrary.InFolder(cs.root, k.archives); in {
				return true
			}
		}
	}
	return false
}

// addonSelectionFor is the add-on selection that plays exactly want on this
// install: each recorded add-on from the library, except one the base
// folder holds. missing is the first it can find nowhere.
func addonSelectionFor(cs *contentSet, want []addonIdentity, known []knownAddon) (selection []settings.AddonSelection, missing *addonIdentity) {
	selection = []settings.AddonSelection{}
	for i, w := range want {
		if addonInFolder(cs, w, known) {
			continue
		}
		found := false
		for _, k := range known {
			if k.installed != nil && k.id == w.id && k.version == w.version && addonlibrary.Digest(k.archives) == w.digest {
				selection, found = append(selection, settings.AddonSelection{ID: k.id, Version: k.version}), true
				break
			}
		}
		if !found {
			return nil, &want[i]
		}
	}
	return selection, nil
}

// replayAddonsOf is a replay header's list of the add-ons cs mounts, in id
// order (§5.7).
func replayAddonsOf(cs *contentSet) []replay.Addon {
	var out []replay.Addon
	for _, a := range cs.addons {
		out = append(out, replay.Addon{ID: a.ID, Version: a.Version, Size: addonlibrary.Size(a.Archives), Digest: addonlibrary.Digest(a.Archives)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func replayAddonIdentities(addons []replay.Addon) []addonIdentity {
	out := make([]addonIdentity, len(addons))
	for i, a := range addons {
		out[i] = addonIdentity{a.ID, a.Version, a.Digest}
	}
	return out
}
