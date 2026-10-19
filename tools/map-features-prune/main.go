// map-features-prune prepares a map or dependency root for packaging under
// the engine's map-admission policy: a package may add feature definitions,
// but it may not change one the base install already defines
// (docs/DESIGN_MODS_MUTATORS.md §5.6; maplibrary's preserveBaseFeatures).
//
// The tool asks the engine's own validator which definition collides, removes
// exactly that section from the root's TDFs, and repeats until the root is
// admitted or a refusal the tool cannot fix appears. It prints every removal,
// so the packaging record shows what the policy dropped. Authored bytes are
// otherwise untouched.
//
// Usage: map-features-prune --base DIR --root DIR [--map maps/x.ota]
//
//	[--dep DIR ...]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/maplibrary"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
)

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

var collision = regexp.MustCompile(`changes an existing feature: logical path ([^,]+),`)

func main() {
	base := flag.String("base", "", "base install root")
	root := flag.String("root", "", "package root to prepare")
	mapPath := flag.String("map", "", "catalogue map path (empty for a dependency)")
	var deps stringList
	flag.Var(&deps, "dep", "dependency root mounted under the base install (repeatable)")
	flag.Parse()
	if *base == "" || *root == "" {
		fmt.Fprintln(os.Stderr, "map-features-prune: --base and --root are required")
		os.Exit(2)
	}
	removed, err := prune(*base, *root, *mapPath, deps)
	for _, entry := range removed {
		fmt.Printf("removed [%s] from %s\n", entry[0], entry[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "map-features-prune:", err)
		os.Exit(1)
	}
}

func prune(base, root, mapPath string, deps []string) ([][2]string, error) {
	// The validator reads the package's identity metadata; packaging writes
	// the real one, so a temporary identity-only file stands in for it.
	meta := modlibrary.Metadata{Schema: 1, ID: "prune", Name: "Prune", Version: "1"}
	metaPath := filepath.Join(root, modlibrary.MetadataFile)
	if _, err := os.Stat(metaPath); os.IsNotExist(err) {
		raw, _ := json.Marshal(meta)
		if err := os.WriteFile(metaPath, raw, 0o644); err != nil {
			return nil, err
		}
		defer os.Remove(metaPath)
	}
	required := append(append([]string(nil), deps...), base)
	var removed [][2]string
	for attempt := 0; attempt < 64; attempt++ {
		err := maplibrary.Validator(required, mapPath)(root, meta)
		if err == nil {
			return removed, nil
		}
		match := collision.FindStringSubmatch(err.Error())
		if match == nil {
			return removed, err
		}
		name := match[1]
		touched, err := removeSection(root, name)
		if err != nil {
			return removed, err
		}
		if len(touched) == 0 {
			return removed, fmt.Errorf("no TDF defines [%s]; cannot prune", name)
		}
		removed = append(removed, [2]string{name, strings.Join(touched, ", ")})
	}
	return removed, fmt.Errorf("still colliding after 64 removals")
}

// removeSection deletes every section named name from the root's TDFs,
// returning the files it changed. A file left without sections is removed.
func removeSection(root, name string) ([]string, error) {
	var touched []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.EqualFold(filepath.Ext(path), ".tdf") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(data)
		for {
			next, ok := cutSection(text, name)
			if !ok {
				break
			}
			text = next
		}
		if text == string(data) {
			return nil
		}
		relative, _ := filepath.Rel(root, path)
		if !strings.Contains(text, "[") {
			if err := os.Remove(path); err != nil {
				return err
			}
			touched = append(touched, relative+" (emptied)")
			return nil
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			return err
		}
		touched = append(touched, relative)
		return nil
	})
	sort.Strings(touched)
	return touched, err
}

// cutSection removes the first [name] { ... } block, matching braces.
func cutSection(text, name string) (string, bool) {
	header := regexp.MustCompile(`(?i)\[\s*` + regexp.QuoteMeta(name) + `\s*\]`)
	match := header.FindStringIndex(text)
	if match == nil {
		return text, false
	}
	brace := strings.IndexByte(text[match[1]:], '{')
	if brace < 0 {
		return text, false
	}
	brace += match[1]
	depth := 0
	for i := brace; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end := i + 1
				for end < len(text) && (text[end] == '\r' || text[end] == '\n') {
					end++
				}
				return text[:match[0]] + text[end:], true
			}
		}
	}
	return text, false
}
