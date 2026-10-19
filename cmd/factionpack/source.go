package main

// The two views the converter compares: the base install alone, and the
// faction release alone under its own directory layout. A path the release
// supplies is the release's, as it wins over the base when the release is
// mounted as a mod; any other path resolves to the base.

import (
	"bytes"
	"path"
	"sort"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

type view struct {
	fs    vfs.FSOps
	paths map[string]string // folded logical path -> logical path, files only
}

func newView(fs vfs.FSOps, entries []vfs.EntryInfo, layout vfs.Layout) *view {
	v := &view{fs: fs, paths: map[string]string{}}
	restore := map[string]string{}
	for _, row := range layout.Names() {
		restore[lower(row[1])] = lower(row[0])
	}
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		p := lower(e.Path)
		first, rest, ok := strings.Cut(p, "/")
		if r, found := restore[first]; found && ok {
			p = r + "/" + rest
		} else if ok && len(restore) > 0 && layoutHides(layout, first) {
			// A retail directory the layout redirects elsewhere is not read.
			continue
		}
		v.paths[p] = p
	}
	return v
}

// layoutHides reports a retail directory name the layout redirects: under
// the release's own engine its readers look in the target instead.
func layoutHides(layout vfs.Layout, first string) bool {
	for _, row := range layout.Names() {
		if strings.EqualFold(row[0], first) {
			return true
		}
	}
	return false
}

func (v *view) has(p string) bool {
	_, ok := v.paths[lower(p)]
	return ok
}

func (v *view) read(p string) ([]byte, bool) {
	if !v.has(p) {
		return nil, false
	}
	data, err := v.fs.ReadFileLimit(lower(p), 256<<20)
	if err != nil {
		return nil, false
	}
	return data, true
}

// list returns the view's files under dir (recursively when deep) with the
// suffix, sorted.
func (v *view) list(dir, suffix string, deep bool) []string {
	prefix := lower(dir) + "/"
	var out []string
	for p := range v.paths {
		if !strings.HasPrefix(p, prefix) || !strings.HasSuffix(p, suffix) {
			continue
		}
		if !deep && strings.Contains(p[len(prefix):], "/") {
			continue
		}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func sameBytes(a []byte, aok bool, b []byte, bok bool) bool {
	return aok && bok && bytes.Equal(a, b)
}

// tdfSection is one named definition from a TDF family: the file that
// defines it, the raw text, and the parsed section.
type tdfSection struct {
	file    string
	text    string
	section *formats.Section
}

// indexSections reads every top-level section of the files, later
// definitions of a name replacing earlier ones.
func indexSections(v *view, files []string) map[string]tdfSection {
	out := map[string]tdfSection{}
	for _, f := range files {
		data, ok := v.read(f)
		if !ok {
			continue
		}
		doc, err := formats.ParseTDF(data)
		if err != nil {
			continue
		}
		parsed := map[string]*formats.Section{}
		for _, s := range doc.Root.Sections() {
			parsed[lower(strings.TrimSpace(s.OriginalName))] = s
		}
		for _, raw := range topSections(data) {
			key := lower(raw.Name)
			if key == "" || parsed[key] == nil {
				continue
			}
			out[key] = tdfSection{file: f, text: sectionText(data, raw), section: parsed[key]}
		}
	}
	return out
}

// sectionsEqual compares two sections by their resolved assignments and
// subsections, folding key case and trimming values: the definitions the
// compiler would produce from them are the same.
func sectionsEqual(a, b *formats.Section) bool {
	if a == nil || b == nil {
		return a == b
	}
	return canonicalSection(a) == canonicalSection(b)
}

func canonicalSection(s *formats.Section) string {
	var parts []string
	for _, it := range s.ResolvedAssignments() {
		parts = append(parts, lower(it.Key)+"="+strings.TrimSpace(it.Value))
	}
	sort.Strings(parts)
	var subs []string
	for _, sub := range s.Sections() {
		subs = append(subs, lower(sub.OriginalName)+"{"+canonicalSection(sub)+"}")
	}
	sort.Strings(subs)
	return strings.Join(parts, ";") + "|" + strings.Join(subs, "")
}

// gafEntries decodes a GAF and indexes its entries by folded name.
func gafEntries(data []byte) map[string]*formats.GAFEntry {
	g, err := formats.LoadGAF(data)
	if err != nil {
		return nil
	}
	out := map[string]*formats.GAFEntry{}
	for i := range g.Entries {
		out[lower(g.Entries[i].Name)] = &g.Entries[i]
	}
	return out
}

// entriesEqual compares two decoded entries frame by frame.
func entriesEqual(a, b *formats.GAFEntry) bool {
	if a == nil || b == nil || len(a.Frames) != len(b.Frames) {
		return false
	}
	for i := range a.Frames {
		if a.Frames[i].Value != b.Frames[i].Value || !framesEqual(a.Frames[i].Frame, b.Frames[i].Frame) {
			return false
		}
	}
	return true
}

func framesEqual(a, b *formats.GAFFrame) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Width != b.Width || a.Height != b.Height || a.XOffset != b.XOffset || a.YOffset != b.YOffset ||
		!bytes.Equal(a.Pixels, b.Pixels) || len(a.Subframes) != len(b.Subframes) {
		return false
	}
	for i := range a.Subframes {
		if !framesEqual(a.Subframes[i], b.Subframes[i]) {
			return false
		}
	}
	return true
}

// textureIndex is a view's model texture namespace as the renderer builds
// and resolves it: every textures/*.gaf but the logo bank, the lexically later
// file winning a shared name, searched before the logo bank.
type textureIndex struct {
	logos   map[string]*formats.GAFEntry
	primary map[string]*formats.GAFEntry
	file    map[string]string // folded name -> GAF path that wins it in primary
}

func buildTextureIndex(resolve func(string) ([]byte, bool), files []string) textureIndex {
	ix := textureIndex{logos: map[string]*formats.GAFEntry{}, primary: map[string]*formats.GAFEntry{}, file: map[string]string{}}
	for _, f := range files {
		data, ok := resolve(f)
		if !ok {
			continue
		}
		entries := gafEntries(data)
		if path.Base(f) == "logos.gaf" {
			ix.logos = entries
			continue
		}
		for name, e := range entries {
			if len(e.Frames) == 0 || e.Frames[0].Frame == nil {
				continue
			}
			ix.primary[name] = e
			ix.file[name] = f
		}
	}
	return ix
}

// lookup resolves a model texture name; logo reports an entry the logo
// bank supplies.
func (ix textureIndex) lookup(name string) (entry *formats.GAFEntry, logo, ok bool) {
	key := lower(name)
	if e, found := ix.primary[key]; found {
		return e, false, true
	}
	e, found := ix.logos[key]
	return e, found, found
}

// lower folds ASCII letters only, as the overlay folds logical paths
// [02 §2]. A release's Latin-1 names keep their bytes: strings.ToLower
// would turn TA: Chaos Space's Ketzer-Gau\xdf into U+FFFD, and the copy
// would name a file nothing supplies.
func lower(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if b[j] >= 'A' && b[j] <= 'Z' {
					b[j] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}
