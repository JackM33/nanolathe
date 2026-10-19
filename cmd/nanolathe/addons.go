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
	"github.com/nanolathe-gg/nanolathe/internal/content"
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
	// factions are the archives of the mounted faction add-ons, presented
	// under the running content's layout (factionLayout).
	factions map[string]bool
}

// factionLayout is the layout each extra archive mounts under: the running
// content's for a faction add-on's archives, so a mod that renames the
// retail directories reads the faction's files beside its own, and none for
// every other add-on (§5.7, §5.8 "With a mod").
func factionLayout(layout vfs.Layout, factions map[string]bool) func(string) vfs.Layout {
	if len(layout.Names()) == 0 || len(factions) == 0 {
		return nil
	}
	return func(full string) vfs.Layout {
		if factions[full] {
			return layout
		}
		return vfs.Layout{}
	}
}

// factionArchives are the archive paths of the faction add-ons in supplies.
func factionArchives(supplies []addonSupply) map[string]bool {
	out := map[string]bool{}
	for _, s := range supplies {
		if s.faction {
			for _, p := range s.addon.Paths {
				out[p] = true
			}
		}
	}
	return out
}

// resolveAddons decides which enabled add-ons a mount holds: none when the
// running config sets content.addons false; each installed one otherwise,
// except one already in the base folder, which the install supplies itself,
// one that defines a unit or feature name the base or the mod defines at
// another path, one that supplies a logical path or defines a unit or
// feature name an add-on earlier in the selection also does. A saved add-on
// that has
// gone is left out with a notice; one named by --addon fails the start, as
// --mod does (§5.7, §5.8).
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
	// accepted are the add-ons this mount holds so far, with what each
	// supplies, read only when there is another add-on to compare it with.
	var accepted []addonSupply
	// composedSides counts the sides the mount holds so far, read from the
	// base only once a faction add-on needs it.
	composedSides := -1
	for _, addon := range candidates {
		names, err := addonlibrary.DefinedNames(addon)
		if err == nil {
			var found string
			if found, err = addonlibrary.Conflict(others.fs, names); err == nil && found != "" {
				err = fmt.Errorf("the installed content already defines %s at another path", found)
			}
		}
		var paths map[string]string
		if err == nil && len(candidates) > 1 {
			if paths, err = addonlibrary.Paths(addon); err == nil {
				err = addonCollision(accepted, paths, names)
			}
		}
		sides, declared := 0, false
		if err == nil {
			paths, sides, declared, err = factionBaseCollision(others.fs, addon, paths)
		}
		if err == nil && sides > 0 {
			if composedSides < 0 {
				composedSides = baseSideCount(others.fs)
			}
			if composedSides+sides > content.MaxComposedSides {
				err = fmt.Errorf("its %d sides would take the battle past %d sides", sides, content.MaxComposedSides)
			}
		}
		if err != nil {
			out.notes = append(out.notes, fmt.Sprintf("nanolathe: official add-on %s not mounted: %s", addon.Name, noticeReason(err)))
			continue
		}
		accepted = append(accepted, addonSupply{addon: addon, paths: paths, names: names, faction: declared})
		composedSides += sides
	}
	out.mounted = compilingAddons(profile, baseRoots, mod, accepted, &out.notes)
	out.factions = factionArchives(accepted)
	return out, nil
}

// compilingAddons is the accepted add-ons less any faction add-on the
// catalog compiler refuses with the base and the mod: a declaration whose
// names collide in a table the mount does not read, a side missing its font,
// or a reference nothing supplies would otherwise stop every start, the main
// menu's included (§5.8). With no faction add-on accepted nothing is compiled; with all of
// them compiling, one compile is the cost. When the content without the
// faction add-ons does not compile either, the faction add-ons are not to
// blame and all stay, so the start reports the failure itself.
func compilingAddons(profile contentprofiles.Profile, baseRoots []string, mod *modlibrary.Mod, accepted []addonSupply, notes *[]string) []addonlibrary.Addon {
	all := make([]addonlibrary.Addon, 0, len(accepted))
	var kept []addonlibrary.Addon
	factions := false
	for _, s := range accepted {
		all = append(all, s.addon)
		if s.faction {
			factions = true
		} else {
			kept = append(kept, s.addon)
		}
	}
	rehome := factionArchives(accepted)
	if !factions || composeAddons(profile, baseRoots, mod, all, rehome) == nil || composeAddons(profile, baseRoots, mod, kept, rehome) != nil {
		return all
	}
	for _, s := range accepted {
		if !s.faction {
			continue
		}
		trial := append(append([]addonlibrary.Addon(nil), kept...), s.addon)
		if err := composeAddons(profile, baseRoots, mod, trial, rehome); err != nil {
			*notes = append(*notes, fmt.Sprintf("nanolathe: official add-on %s not mounted: the game data does not compile with it: %s", s.addon.Name, noticeReason(err)))
			continue
		}
		kept = trial
	}
	// The selection's order, which the mount keeps (addonExtras).
	ordered := kept[:0:0]
	for _, a := range all {
		for _, k := range kept {
			if k.ID == a.ID && k.Version == a.Version {
				ordered = append(ordered, a)
				break
			}
		}
	}
	return ordered
}

// composeAddons compiles the catalog of the base roots and the mod with
// addons mounted as the start mounts them, factions' archives under the
// layout.
func composeAddons(profile contentprofiles.Profile, baseRoots []string, mod *modlibrary.Mod, addons []addonlibrary.Addon, factions map[string]bool) error {
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
			root.Extra = addonExtras(addons)
			root.ExtraLayout = factionLayout(profile.Layout(), factions)
		}
		roots = append(roots, root)
	}
	if mod != nil {
		roots = append(roots, vfs.GameRoot{Dir: mod.Dir})
	}
	mounted := vfs.New()
	defer mounted.Close()
	if err := mounted.MountGameRoots(roots); err != nil {
		return err
	}
	_, err := content.CompileWithOptions(profile.Layout().Apply(mounted), content.Options{Limits: content.LimitsFromProfile(profile.Limits)})
	return err
}

// addonSupply is an add-on a mount has accepted and what it supplies: its
// logical files and its unit and feature names.
type addonSupply struct {
	addon addonlibrary.Addon
	paths map[string]string
	names addonlibrary.Names
	// faction marks an add-on with a faction declaration, a unit pack's
	// included (§5.8).
	faction bool
}

// addonCollision says why an add-on cannot mount beside the add-ons already
// accepted: one of them supplies a logical path it also supplies, or defines
// a unit or feature name it also defines, so one would replace the other's
// (§5.8 isolation rules 1 and 2). The add-on earlier in the selection keeps
// its own, so the outcome follows the selection's order alone.
func addonCollision(accepted []addonSupply, paths map[string]string, names addonlibrary.Names) error {
	for _, prior := range accepted {
		if shared := addonlibrary.SharedPath(paths, prior.paths); shared != "" {
			return fmt.Errorf("the enabled add-on %s also supplies %s", prior.addon.Name, shared)
		}
		if shared := addonlibrary.SharedName(names, prior.names); shared != "" {
			return fmt.Errorf("the enabled add-on %s already defines %s", prior.addon.Name, shared)
		}
	}
	return nil
}

// factionBaseCollision refuses a faction add-on that supplies a logical path
// the base install or the mod also supplies: a faction never replaces a base
// file (§5.8 isolation rule 1). An add-on without a declaration keeps §5.7's
// name check alone. paths is the add-on's path set when already read; the
// result is it, read here if a faction needed it, the number of sides the
// add-on declares, and whether it has a declaration at all: a unit pack has
// one and no sides.
func factionBaseCollision(others vfs.FSOps, addon addonlibrary.Addon, paths map[string]string) (map[string]string, int, bool, error) {
	factions, err := addonlibrary.Factions(addon)
	if err != nil || len(factions) == 0 {
		return paths, 0, false, err
	}
	sides := 0
	for _, f := range factions {
		sides += len(f.Sides)
	}
	if paths == nil {
		if paths, err = addonlibrary.Paths(addon); err != nil {
			return nil, 0, true, err
		}
	}
	keys := make([]string, 0, len(paths))
	for key := range paths {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if info, err := others.Stat(key); err == nil && !info.IsDir {
			return paths, 0, true, fmt.Errorf("the installed content already supplies %s", paths[key])
		}
	}
	return paths, sides, true, nil
}

// baseSideCount is how many sides the content without add-ons defines;
// zero when they do not compile, which the start reports itself.
func baseSideCount(others vfs.FSOps) int {
	sides, err := content.CompileSides(others)
	if err != nil {
		return 0
	}
	return len(sides)
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
