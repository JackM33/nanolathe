// map-verify runs the engine's map-package admission (maplibrary.Validator)
// over a built catalogue: every map package is extracted to a temporary root
// and validated against the base install, an optional selected mod and the
// dependencies the entry declares. It is the pre-publish gate for a curated
// set — the same check the client performs when it installs a package
// (docs/DESIGN_MODS_MUTATORS.md §5.6).
//
//	Usage: map-verify --manifest out/manifest.json --archives out \
//	                  --base "/path/to/Total Annihilation" [--mod path]
package main

import (
	"archive/zip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/maplibrary"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
)

type record struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Map      string   `json:"map"`
	Requires []string `json:"requires"`
	Archive  struct {
		URL string `json:"url"`
	} `json:"archive"`
}

type manifest struct {
	Maps         []record `json:"maps"`
	Dependencies []record `json:"dependencies"`
}

func main() {
	manifestPath := flag.String("manifest", "", "built map catalogue manifest")
	archives := flag.String("archives", "", "directory holding the catalogue ZIPs")
	base := flag.String("base", "", "base install root")
	mod := flag.String("mod", "", "selected mod root (optional)")
	flag.Parse()
	if *manifestPath == "" || *archives == "" || *base == "" {
		fmt.Fprintln(os.Stderr, "map-verify: --manifest, --archives and --base are required")
		os.Exit(2)
	}
	if err := run(*manifestPath, *archives, *base, *mod); err != nil {
		fmt.Fprintln(os.Stderr, "map-verify:", err)
		os.Exit(1)
	}
}

func run(manifestPath, archives, base, mod string) error {
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var doc manifest
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	staging, err := os.MkdirTemp("", "map-verify-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	extract := func(r record) (string, error) {
		dir := filepath.Join(staging, r.ID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		return dir, unzip(filepath.Join(archives, path.Base(r.Archive.URL)), dir)
	}
	deps := map[string]string{}
	failed := 0
	for _, dep := range doc.Dependencies {
		dir, err := extract(dep)
		if err != nil {
			fmt.Printf("FAIL %-24s %v\n", dep.ID, err)
			failed++
			continue
		}
		metaRaw, err := os.ReadFile(filepath.Join(dir, modlibrary.MetadataFile))
		if err != nil {
			fmt.Printf("FAIL %-24s %v\n", dep.ID, err)
			failed++
			continue
		}
		var meta modlibrary.Metadata
		if err := json.Unmarshal(metaRaw, &meta); err != nil {
			fmt.Printf("FAIL %-24s %v\n", dep.ID, err)
			failed++
			continue
		}
		// A dependency is admitted by the same validator with no map path: it
		// must carry support assets only and must not redefine a base feature.
		if err := maplibrary.Validator([]string{base}, "")(dir, meta); err != nil {
			fmt.Printf("FAIL %-24s %v\n", dep.ID, err)
			failed++
			continue
		}
		fmt.Printf("ok   %-24s %s (dependency)\n", dep.ID, dep.Name)
		deps[dep.ID] = dir
	}
	required := []string{base}
	if mod != "" {
		required = append(required, mod)
	}
	for _, entry := range doc.Maps {
		dir, err := extract(entry)
		if err != nil {
			fmt.Printf("FAIL %-24s %v\n", entry.ID, err)
			failed++
			continue
		}
		metaRaw, err := os.ReadFile(filepath.Join(dir, modlibrary.MetadataFile))
		if err != nil {
			fmt.Printf("FAIL %-24s %v\n", entry.ID, err)
			failed++
			continue
		}
		var meta modlibrary.Metadata
		if err := json.Unmarshal(metaRaw, &meta); err != nil {
			fmt.Printf("FAIL %-24s %v\n", entry.ID, err)
			failed++
			continue
		}
		roots := append([]string(nil), required...)
		missing := false
		for _, id := range entry.Requires {
			depDir, ok := deps[id]
			if !ok {
				fmt.Printf("FAIL %-24s undeclared dependency %s\n", entry.ID, id)
				failed++
				missing = true
				break
			}
			roots = append(roots, depDir)
		}
		if missing {
			continue
		}
		if err := maplibrary.Validator(roots, entry.Map)(dir, meta); err != nil {
			fmt.Printf("FAIL %-24s %v\n", entry.ID, err)
			failed++
			continue
		}
		fmt.Printf("ok   %-24s %s\n", entry.ID, entry.Name)
	}
	fmt.Printf("map-verify: %d maps, %d failed\n", len(doc.Maps), failed)
	if failed != 0 {
		return fmt.Errorf("%d map packages refused", failed)
	}
	return nil
}

// unzip extracts a catalogue ZIP, refusing absolute paths and parent
// traversal. Package bytes are written verbatim.
func unzip(archive, dir string) error {
	z, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer z.Close()
	for _, file := range z.File {
		name := file.Name
		if strings.Contains(name, `\`) {
			name = strings.ReplaceAll(name, `\`, "/")
		}
		clean := path.Clean(name)
		if clean == "." || strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
			return fmt.Errorf("%s: unsafe path %s", archive, file.Name)
		}
		target := filepath.Join(dir, filepath.FromSlash(clean))
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		src, err := file.Open()
		if err != nil {
			return err
		}
		dst, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			src.Close()
			return err
		}
		_, copyErr := io.Copy(dst, src)
		src.Close()
		if err := dst.Close(); err != nil && copyErr == nil {
			copyErr = err
		}
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}
