package main

// --install-addon (docs/DESIGN_MODS_MUTATORS.md §5.7, §5.8): an add-on
// package from this machine — a faction add-on that factionpack prepared,
// for one — installed into the add-on library from the command line, as
// --install-mod installs a mod (§4.5).

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/addonlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
)

// runInstallAddon is --install-addon: it installs a zip or a folder through
// the add-on library's atomic install, with the local gate — identity-only
// metadata and the add-on payload audit, with no catalogue contents list —
// and prints what it installed. A version already installed is refused, as a
// manual mod install's is. Like --install-mod it selects nothing: --addon or
// the Add-ons card enables it.
func runInstallAddon(opts Options, out io.Writer) error {
	lib, err := openAddonLibrary()
	if err != nil {
		return err
	}
	installed, err := installLocalAddon(lib, opts.InstallAddon)
	if err != nil {
		return err
	}
	selector := modSelectorOf(installed.ID, installed.Version)
	if _, err := fmt.Fprintf(out, "installed add-on %s (%s) in %s\n", selector, installed.Name, installed.Dir); err != nil {
		return err
	}
	if addon, ok := libraryAddon(lib.Root, installed.ID, installed.Version); ok {
		factions, _ := addonlibrary.Factions(addon)
		for _, f := range factions {
			line := fmt.Sprintf("faction %s: sides %s; %s\n", f.ID, strings.Join(f.Sides, ", "), unitCountText(f.Units))
			if f.UnitPack() {
				line = fmt.Sprintf("unit pack %s: %s for the existing sides\n", f.ID, unitCountText(f.Units))
			}
			if _, err := io.WriteString(out, line); err != nil {
				return err
			}
		}
	}
	_, err = fmt.Fprintf(out, "enable it with --addon %s or on the Nanolathe screen's Add-ons card\n", selector)
	return err
}

// installLocalAddon installs one local package, a folder or a zip.
func installLocalAddon(lib *modlibrary.Library, path string) (modlibrary.Mod, error) {
	name := filepath.Base(filepath.Clean(path))
	info, err := os.Stat(path)
	if err != nil {
		return modlibrary.Mod{}, &missingProductError{what: "add-on package is not readable", logical: path, providers: []string{name}, expected: "a .zip file or a folder"}
	}
	options := modlibrary.InstallOptions{Source: "local:" + name, Validate: addonlibrary.LocalValidator()}
	if info.IsDir() {
		return lib.InstallDirectory(path, options)
	}
	return lib.InstallArchive(path, options)
}

// libraryAddon is the add-on library's listing of one installed version.
func libraryAddon(root, id, version string) (addonlibrary.Addon, bool) {
	addons, _, err := addonlibrary.Installed(root)
	if err != nil {
		return addonlibrary.Addon{}, false
	}
	return installedAddon(addons, id, version)
}

// unitCountText counts units in words.
func unitCountText(n int) string {
	if n == 1 {
		return "1 unit"
	}
	return fmt.Sprintf("%d units", n)
}
