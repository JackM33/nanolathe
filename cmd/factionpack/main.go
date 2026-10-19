// Command factionpack converts a third-party faction release into a faction
// add-on: one archive holding only the kept sides' units and what they reach,
// under names the faction owns, with its sides, movement classes, sound
// categories, weapons and base-weapon damage entries declared under
// factions/<id>/ for the catalog compiler to compose beside the base game's
// (docs/DESIGN_MODS_MUTATORS.md §5.8). A recipe of kind "units" converts the
// units a release adds to the base sides into a unit pack instead: no sides
// of its own, and its units placed on the base builders' menus. It is a
// packaging tool, not shipped with the game.
//
//	factionpack -recipe factionconfigs/mynn-2003-01-17.json \
//	  -base ~/TotalAnnihilation -source ./mynn-release -out ./out
package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// recipe is one release's conversion, authored in the repository as
// factionconfigs/<id>-<version>.json.
type recipe struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	Summary  string `json:"summary"`
	Homepage string `json:"homepage,omitempty"`
	Tag      string `json:"tag"`
	// Kind is "units" for a unit pack: the release's units of UnitSides that
	// the base does not define, with no side records. Empty is a faction.
	Kind   string            `json:"kind,omitempty"`
	Sides  []string          `json:"sides,omitempty"`
	Layout map[string]string `json:"layout,omitempty"`
	Source struct {
		File   string `json:"file"`
		SHA256 string `json:"sha256"`
		// Root is the release's folder inside the source, when its files
		// sit under one rather than at the top; readmes are named from it.
		Root string `json:"root,omitempty"`
	} `json:"source"`
	// UnitSides are the unit `side` values to keep, when they are more than
	// the side records' names (a release may tag some of its units with a
	// variant spelling). Empty keeps the units of Sides.
	UnitSides []string `json:"unitSides,omitempty"`
	// Exclude names release archives to leave unmounted, such as a repair
	// overlay the composition makes unnecessary.
	Exclude []string `json:"exclude,omitempty"`
	// OmitUnits names release units to leave out, such as units the release
	// made for its computer players alone; what only they reach goes too,
	// and builders' lists drop them as they drop any missing product.
	OmitUnits []string `json:"omitUnits,omitempty"`
	// Readmes are the release's own documents, by path in the release, copied
	// to the package root as RELEASE-<name> so its authors' credits travel
	// with the add-on; ".htm" becomes ".html", a root document the add-on
	// library accepts.
	Readmes []string `json:"readmes,omitempty"`
	Notes   []string `json:"notes,omitempty"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "verify" {
		if err := runVerify(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "factionpack verify:", err)
			os.Exit(1)
		}
		return
	}
	recipePath := flag.String("recipe", "", "faction recipe JSON")
	base := flag.String("base", "", "base Total Annihilation install")
	source := flag.String("source", "", "extracted faction release (a directory) or the release zip")
	out := flag.String("out", "", "output directory; the add-on is written to <out>/<id>-<version>/ and <out>/<id>-<version>.zip")
	art := flag.String("art", "", "directory of <unit>.png renders of the release's own models, for placed units the release gives no button art or picture")
	flag.Parse()
	if *recipePath == "" || *base == "" || *source == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*recipePath, *base, *source, *out, *art); err != nil {
		fmt.Fprintln(os.Stderr, "factionpack:", err)
		os.Exit(1)
	}
}

func readRecipe(name string) (recipe, error) {
	var r recipe
	data, err := os.ReadFile(name)
	if err != nil {
		return r, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return r, fmt.Errorf("%s: %w", name, err)
	}
	if r.ID == "" || r.Version == "" || r.Name == "" || len(r.Tag) < 2 {
		return r, fmt.Errorf("%s: id, name, version and a tag of two or more characters are required", name)
	}
	switch r.Kind {
	case "":
		if len(r.Sides) == 0 {
			return r, fmt.Errorf("%s: a faction recipe names its sides", name)
		}
	case "units":
		if len(r.Sides) != 0 || len(r.UnitSides) == 0 {
			return r, fmt.Errorf("%s: a unit pack names the unit sides to keep and no sides of its own", name)
		}
	default:
		return r, fmt.Errorf("%s: unknown kind %q", name, r.Kind)
	}
	if lower(r.ID) != r.ID || strings.ContainsAny(r.ID, "/\\ ") {
		return r, fmt.Errorf("%s: id must be lowercase with no separators", name)
	}
	r.Tag = lower(r.Tag)
	return r, nil
}

func run(recipePath, baseDir, source, outDir, artDir string) error {
	r, err := readRecipe(recipePath)
	if err != nil {
		return err
	}
	pinned := false
	if info, err := os.Stat(source); err == nil && !info.IsDir() {
		if err := checkSource(source, r.Source.SHA256); err != nil {
			return err
		}
		pinned = r.Source.SHA256 != ""
		dir, err := os.MkdirTemp("", "factionpack-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		if err := extract(source, dir); err != nil {
			return err
		}
		source = dir
	}
	if r.Source.Root != "" {
		source = filepath.Join(source, filepath.FromSlash(r.Source.Root))
	}
	files, notes, err := convert(r, baseDir, source, artDir)
	if err != nil {
		return err
	}
	if !pinned {
		notes = append([]string{"Source: a directory or an unpinned file; its bytes were not checked against the recipe's SHA-256."}, notes...)
	}
	docs := map[string][]byte{}
	for _, name := range r.Readmes {
		data, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(name)))
		if err != nil {
			return fmt.Errorf("readme %s: %w", name, err)
		}
		out := "RELEASE-" + path.Base(name)
		if strings.EqualFold(path.Ext(out), ".htm") {
			out += "l"
		}
		docs[out] = data
	}
	return write(r, files, notes, docs, outDir)
}

func checkSource(file, want string) error {
	if want == "" {
		return nil
	}
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != lower(want) {
		return fmt.Errorf("%s: SHA-256 %s, the recipe pins %s", file, got, want)
	}
	return nil
}

func extract(zipPath, dir string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		name := filepath.FromSlash(f.Name)
		if strings.Contains(f.Name, "..") || filepath.IsAbs(name) {
			return fmt.Errorf("%s: unsafe entry %q", zipPath, f.Name)
		}
		dest := filepath.Join(dir, name)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		w, err := os.Create(dest)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(w, rc)
		rc.Close()
		if cerr := w.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func mountView(roots []vfs.GameRoot, layout vfs.Layout) (*view, func(), error) {
	fs := vfs.New()
	if err := fs.MountGameRoots(roots); err != nil {
		fs.Close()
		return nil, nil, err
	}
	return newView(layout.Apply(fs), fs.Entries(), layout), func() { fs.Close() }, nil
}

// addonArchive reports a *.UFO archive in an install's folder: an add-on
// such as Cavedog's bonus units, which an install may lack (§5.7).
func addonArchive(name string) bool { return strings.EqualFold(filepath.Ext(name), ".ufo") }

// convert produces the add-on archive's files and the package notes.
func convert(r recipe, baseDir, source, artDir string) (map[string][]byte, []string, error) {
	layout := vfs.NewLayout(r.Layout)
	// The base is the install without its add-on archives, which a player's
	// install may lack: anything the release reaches only through them is the
	// release's, copied under the faction's tag, so the add-on never depends
	// on another (§5.8). The release is read as its authors saw it: over the
	// install's add-on archives.
	base, closeBase, err := mountView([]vfs.GameRoot{{Dir: baseDir, Archives: func(name string) bool { return !addonArchive(name) }}}, vfs.Layout{})
	if err != nil {
		return nil, nil, fmt.Errorf("base: %w", err)
	}
	defer closeBase()
	skip := map[string]bool{}
	for _, name := range r.Exclude {
		skip[lower(name)] = true
	}
	// The install's own add-on archives alone: a unit pack leaves their
	// units, such as Cavedog's bonus units, to the official add-ons (§5.7).
	bonus, closeBonus, err := mountView([]vfs.GameRoot{{Dir: baseDir, Archives: addonArchive, NoLoose: true}}, vfs.Layout{})
	if err != nil {
		return nil, nil, fmt.Errorf("base add-ons: %w", err)
	}
	defer closeBonus()
	rel, closeRel, err := mountView([]vfs.GameRoot{
		{Dir: baseDir, Archives: addonArchive, NoLoose: true},
		{Dir: source, Archives: func(name string) bool { return !skip[lower(name)] }},
	}, layout)
	if err != nil {
		return nil, nil, fmt.Errorf("release: %w", err)
	}
	defer closeRel()

	p := &packer{
		r: r, artDir: artDir, base: base, bonus: bonus, rel: rel, tag: r.Tag,
		kept: map[string]tdfSection{}, keptName: map[string]string{}, baseUnit: map[string]bool{},
		files: map[string][]byte{}, fileNames: map[string]string{},
		weapons: map[string]string{}, features: map[string]string{}, moves: map[string]string{},
		sounds: map[string]string{}, textures: map[string]string{}, texCopies: map[string]bool{},
		inFlight: map[string]bool{}, cycled: map[string]bool{},
	}
	p.relWeapons = indexSections(rel, rel.list("weapons", ".tdf", false))
	p.baseWeapons = indexSections(base, base.list("weapons", ".tdf", false))
	p.relFeatures = indexSections(rel, rel.list("features", ".tdf", true))
	p.baseFeatures = indexSections(base, base.list("features", ".tdf", true))
	p.relMoves, p.baseMoves = moveClasses(rel, base)
	p.relSounds = indexSections(rel, []string{"gamedata/sound.tdf"})
	p.baseSounds = indexSections(base, []string{"gamedata/sound.tdf"})
	if !rel.has("gamedata/sound.tdf") {
		p.relSounds = map[string]tdfSection{}
	}
	p.baseTex = buildTextureIndex(base.read, base.list("textures", ".gaf", false))
	// The release's view of textures: its banks and the base banks it does
	// not replace, in path order, as the renderer would index them under the
	// release mounted as a mod.
	union := map[string]bool{}
	for _, f := range append(base.list("textures", ".gaf", false), rel.list("textures", ".gaf", false)...) {
		union[f] = true
	}
	var texFiles []string
	for f := range union {
		texFiles = append(texFiles, f)
	}
	sort.Strings(texFiles)
	p.relTex = buildTextureIndex(func(f string) ([]byte, bool) {
		if data, ok := rel.read(f); ok {
			return data, true
		}
		return nil, false // a base bank is not the release's; lookup falls through
	}, texFiles)

	if err := p.selectUnits(); err != nil {
		return nil, nil, err
	}
	if err := p.prune(); err != nil {
		return nil, nil, err
	}
	sides, err := p.sides()
	if err != nil {
		return nil, nil, err
	}
	if err := p.units(); err != nil {
		return nil, nil, err
	}
	if err := p.unitFiles(sides); err != nil {
		return nil, nil, err
	}
	if err := p.downloads(); err != nil {
		return nil, nil, err
	}
	p.gadgetArt(p.placed)
	if err := p.copyTextureBanks(); err != nil {
		return nil, nil, err
	}
	dir := "factions/" + lower(r.ID) + "/"
	p.files[dir+"sidedata.tdf"] = []byte(sides.text)
	if !p.unitPack() {
		if art, ok := p.sideButtons(sides); ok {
			p.files[dir+"skirmish.gaf"] = art
		}
	}
	// A release that replaced the shared interface support bank gets it back
	// for its own sides' interface only (§5.8); a unit pack has none.
	if rel, ok := p.rel.read("anims/commongui.gaf"); ok && !p.unitPack() {
		if base, baseOK := p.base.read("anims/commongui.gaf"); !sameBytes(rel, ok, base, baseOK) {
			p.files[dir+"commongui.gaf"] = rel
		}
	}
	emit := func(name string, sections []string) {
		if len(sections) > 0 {
			p.files[dir+name] = []byte(strings.Join(sections, "\r\n\r\n") + "\r\n")
		}
	}
	emit("weapons.tdf", p.out.weapons)
	emit("sound.tdf", p.out.sounds)
	emit("damage.tdf", p.damage())
	emit("download.tdf", p.out.menu)
	var classes []string
	for i, text := range p.out.moves {
		classes = append(classes, renameSection(text, fmt.Sprintf("CLASS%d", i)))
	}
	emit("moveinfo.tdf", classes)
	if len(p.out.features) > 0 {
		p.files["features/"+lower(r.ID)+"/"+lower(r.ID)+".tdf"] = []byte(strings.Join(p.out.features, "\r\n\r\n") + "\r\n")
	}
	notes := p.packageNotes(sides)
	p.files[dir+"package.txt"] = []byte(strings.Join(notes, "\r\n") + "\r\n")
	return p.files, notes, nil
}

// moveClasses indexes each view's movement classes by name, as the compiler
// resolves a name: the first in CLASS0..CLASS31 order. A release that
// supplies no moveinfo.tdf uses the base classes.
func moveClasses(rel, base *view) (map[string]tdfSection, map[string]tdfSection) {
	byName := func(v *view) map[string]tdfSection {
		sections := indexSections(v, []string{"gamedata/moveinfo.tdf"})
		out := map[string]tdfSection{}
		for i := 0; i < 32; i++ {
			s, ok := sections[fmt.Sprintf("class%d", i)]
			if !ok {
				continue
			}
			name, ok := s.section.StringValue("Name", "")
			key := lower(strings.TrimSpace(name))
			if !ok || key == "" {
				continue
			}
			if _, seen := out[key]; !seen {
				out[key] = s
			}
		}
		return out
	}
	if !rel.has("gamedata/moveinfo.tdf") {
		return map[string]tdfSection{}, byName(base)
	}
	return byName(rel), byName(base)
}

// selectUnits keeps the release units whose side is one of the recipe's.
func (p *packer) selectUnits() error {
	want := map[string]bool{}
	sides := p.r.UnitSides
	if len(sides) == 0 {
		sides = p.r.Sides
	}
	for _, s := range sides {
		want[lower(s)] = true
	}
	omit := map[string]bool{}
	for _, name := range p.r.OmitUnits {
		omit[lower(strings.TrimSpace(name))] = true
	}
	for _, f := range p.base.list("units", ".fbi", false) {
		if data, ok := p.base.read(f); ok {
			if doc, err := formats.ParseTDF(data); err == nil {
				if ui := doc.Root.Section("UNITINFO"); ui != nil {
					name, _ := ui.StringValue("UnitName", "")
					p.baseUnit[lower(strings.TrimSpace(name))] = true
				}
			}
		}
	}
	for _, f := range p.rel.list("units", ".fbi", false) {
		data, _ := p.rel.read(f)
		doc, err := formats.ParseTDF(data)
		if err != nil {
			continue
		}
		ui := doc.Root.Section("UNITINFO")
		if ui == nil {
			continue
		}
		side, _ := ui.StringValue("side", "")
		if !want[lower(strings.TrimSpace(side))] {
			continue
		}
		name, _ := ui.StringValue("UnitName", "")
		name = strings.TrimSpace(name)
		key := lower(name)
		if key == "" {
			continue
		}
		if omit[key] {
			p.notes = append(p.notes, fmt.Sprintf("unit %s left out as the recipe asks", name))
			continue
		}
		if p.unitPack() && p.bonusUnit(key) {
			p.bonusKept++
			continue
		}
		if p.baseUnit[key] {
			if p.unitPack() {
				p.baseKept++ // the base's own unit, which the pack leaves as the base defines it
				continue
			}
			return fmt.Errorf("kept unit %s is also a base unit; a faction cannot replace one", name)
		}
		if lower(stemOf(f)) != lower(name) {
			p.notes = append(p.notes, fmt.Sprintf("unit %s was authored in %s; written as units/%s.fbi, the file the compiler reads", name, f, key))
		}
		p.kept[key] = tdfSection{file: f, text: string(data), section: ui}
		p.keptName[key] = name
	}
	if p.unitPack() && p.baseKept > 0 {
		p.notes = append(p.notes, fmt.Sprintf("%d release units of the kept sides are base units; the pack leaves them as the base defines them", p.baseKept))
	}
	if p.bonusKept > 0 {
		p.notes = append(p.notes, fmt.Sprintf("%d release units of the kept sides are the install's add-on units; the pack leaves them to the official add-ons", p.bonusKept))
	}
	if len(p.kept) == 0 {
		return errors.New("no release unit names one of the recipe's sides")
	}
	return nil
}

// units writes each kept unit with its references resolved.
func (p *packer) units() error {
	names := make([]string, 0, len(p.kept))
	for k := range p.kept {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, key := range names {
		u := p.kept[key]
		var err error
		objectName, hasObject := u.section.StringValue("Objectname", "")
		if !hasObject || strings.TrimSpace(objectName) == "" {
			if _, err := p.model(p.keptName[key]); err != nil {
				return err
			}
		}
		text := rewriteValues(u.text, map[string]func(string) string{
			"weapon1":        keyFn(&err, p.weapon),
			"weapon2":        keyFn(&err, p.weapon),
			"weapon3":        keyFn(&err, p.weapon),
			"explodeas":      keyFn(&err, p.weapon),
			"selfdestructas": keyFn(&err, p.weapon),
			"corpse":         keyFn(&err, p.feature),
			"movementclass":  keyFn(&err, p.movement),
			"soundcategory":  keyFn(&err, p.soundCategory),
			"objectname":     keyFn(&err, p.model),
		})
		if err != nil {
			return fmt.Errorf("unit %s: %w", p.keptName[key], err)
		}
		p.files["units/"+key+".fbi"] = []byte(text)
	}
	return nil
}

type sideOutput struct {
	text     string
	ordinals []int // each kept side's ordinal in the release
	names    []string
	prefixes []string
	baseSide map[string]bool // base nameprefixes
}

// sides writes the declaration's sidedata: the kept SIDE records renumbered
// from zero, and the kept builders' [CANBUILD] pages with every product that
// would not exist dropped.
func (p *packer) sides() (sideOutput, error) {
	out := sideOutput{baseSide: map[string]bool{}}
	baseData, _ := p.base.read("gamedata/sidedata.tdf")
	if doc, err := formats.ParseTDF(baseData); err == nil {
		for i := 0; ; i++ {
			s := doc.Root.Section(fmt.Sprintf("SIDE%d", i))
			if s == nil {
				break
			}
			prefix, _ := s.StringValue("nameprefix", "")
			out.baseSide[lower(strings.TrimSpace(prefix))] = true
		}
	}
	data, ok := p.rel.read("gamedata/sidedata.tdf")
	if !ok {
		return out, errors.New("the release supplies no gamedata/sidedata.tdf")
	}
	doc, err := formats.ParseTDF(data)
	if err != nil {
		return out, err
	}
	raw := map[string]rawSection{}
	for _, s := range topSections(data) {
		raw[lower(s.Name)] = s
	}
	want := map[string]bool{}
	for _, s := range p.r.Sides {
		want[lower(s)] = true
	}
	var b strings.Builder
	n := 0
	taken := map[string]bool{}
	for i := 0; !p.unitPack(); i++ {
		name := fmt.Sprintf("SIDE%d", i)
		s := doc.Root.Section(name)
		if s == nil {
			break
		}
		sideName, _ := s.StringValue("name", "")
		if !want[lower(strings.TrimSpace(sideName))] {
			continue
		}
		if taken[lower(strings.TrimSpace(sideName))] {
			p.notes = append(p.notes, fmt.Sprintf("%s (%s) left out: an earlier side record has that name", name, sideName))
			continue
		}
		commander, _ := s.StringValue("commander", "")
		if _, kept := p.kept[lower(strings.TrimSpace(commander))]; !kept {
			p.notes = append(p.notes, fmt.Sprintf("side %s left out: its commander %s is not a kept unit", sideName, commander))
			continue
		}
		var ferr error
		text := rewriteValues(sectionText(data, raw[lower(name)]), map[string]func(string) string{
			"intgaf": keyFn(&ferr, p.animBank),
			"font": keyFn(&ferr, func(v string) (string, error) {
				out, err := p.file("fonts/"+v+".fnt", 60, nil)
				return stemOf(out), err
			}),
			"fontgui": keyFn(&ferr, func(v string) (string, error) {
				out, err := p.file("fonts/"+v+".fnt", 60, nil)
				return stemOf(out), err
			}),
		})
		if ferr != nil {
			return out, ferr
		}
		b.WriteString(renameSection(text, fmt.Sprintf("SIDE%d", n)))
		b.WriteString("\r\n\r\n")
		n++
		taken[lower(strings.TrimSpace(sideName))] = true
		out.ordinals = append(out.ordinals, i)
		out.names = append(out.names, strings.TrimSpace(sideName))
		prefix, _ := s.StringValue("nameprefix", "")
		out.prefixes = append(out.prefixes, strings.TrimSpace(prefix))
	}
	if n == 0 && !p.unitPack() {
		return out, errors.New("no kept side has a kept commander")
	}
	b.WriteString("[CANBUILD]\r\n\t{\r\n")
	if canbuild := doc.Root.Section("CANBUILD"); canbuild != nil {
		for _, builder := range canbuild.Sections() {
			name := strings.TrimSpace(builder.OriginalName)
			if _, kept := p.kept[lower(name)]; !kept {
				// A unit pack's units the release added to a base builder's
				// list go on that builder's menu (§5.8).
				if p.unitPack() && p.baseUnit[lower(name)] {
					for i := 1; ; i++ {
						v, ok := builder.StringValue(fmt.Sprintf("canbuild%d", i), "")
						if !ok {
							break
						}
						p.menuEntry(name, v)
					}
				}
				continue
			}
			var products []string
			for i := 1; ; i++ {
				v, ok := builder.StringValue(fmt.Sprintf("canbuild%d", i), "")
				if !ok {
					break
				}
				if p.available(v) {
					products = append(products, strings.TrimSpace(v))
				}
			}
			b.WriteString("\t[" + name + "]\r\n\t\t{\r\n")
			b.WriteString(numberedList("canbuild", products, "\t\t"))
			b.WriteString("\t\t}\r\n")
		}
	}
	b.WriteString("\t}\r\n")
	out.text = b.String()
	return out, nil
}

// available reports a product a kept builder may still build: a kept unit
// or a base unit.
func (p *packer) available(name string) bool {
	key := lower(strings.TrimSpace(name))
	if key == "" {
		return false
	}
	_, kept := p.kept[key]
	return kept || p.baseUnit[key]
}

// unitFiles copies the files named after kept units — scripts, pictures,
// build pages and their art — and the kept sides' own interface windows and
// art, named by a side prefix the base sides do not use.
func (p *packer) unitFiles(sides sideOutput) error {
	var prefixes []string
	for _, prefix := range sides.prefixes {
		key := lower(prefix)
		if key != "" && !sides.baseSide[key] {
			prefixes = append(prefixes, key)
		}
	}
	units := make([]string, 0, len(p.kept))
	for k := range p.kept {
		units = append(units, k)
	}
	for _, dir := range []string{"scripts", "unitpics", "anims", "guis"} {
		for _, f := range p.rel.list(dir, "", false) {
			stem := lower(stemOf(f))
			match := false
			for _, u := range units {
				if strings.HasPrefix(stem, u) {
					match = true
					break
				}
			}
			if !match && (dir == "guis" || dir == "anims") {
				for _, prefix := range prefixes {
					if strings.HasPrefix(stem, prefix) {
						match = true
						break
					}
				}
			}
			if !match {
				continue
			}
			if p.base.has(f) {
				if base, _ := p.base.read(f); base != nil {
					if rel, _ := p.rel.read(f); bytes.Equal(rel, base) {
						continue
					}
				}
				p.notes = append(p.notes, fmt.Sprintf("%s left out: the base install supplies that path", f))
				continue
			}
			data, _ := p.rel.read(f)
			p.files[f] = data
		}
	}
	return nil
}

// downloads keeps the download-menu entries that add a product to a kept
// builder, each file renamed into the tag unless the faction owns it. A
// faction's entry that would add to a base builder is left out: it would
// change the base game. A unit pack's entry adding one of its units to a
// base builder becomes a menu entry the compiler places (§5.8).
func (p *packer) downloads() error {
	for _, f := range p.rel.list("download", ".tdf", false) {
		data, _ := p.rel.read(f)
		doc, err := formats.ParseTDF(data)
		if err != nil {
			continue
		}
		parsed := map[string]*formats.Section{}
		for _, s := range doc.Root.Sections() {
			parsed[lower(strings.TrimSpace(s.OriginalName))] = s
		}
		var kept []string
		dropped := 0
		for _, raw := range topSections(data) {
			s := parsed[lower(raw.Name)]
			if s == nil {
				continue
			}
			builder, _ := s.StringValue("UNITMENU", "")
			product, _ := s.StringValue("UNITNAME", "")
			if _, ok := p.kept[lower(strings.TrimSpace(builder))]; !ok || !p.available(product) {
				if p.unitPack() && p.baseUnit[lower(strings.TrimSpace(builder))] {
					p.menuEntry(builder, product)
				}
				dropped++
				continue
			}
			kept = append(kept, sectionText(data, raw))
			p.placed = append(p.placed, product)
		}
		if len(kept) == 0 {
			continue
		}
		out := f
		if p.base.has(f) || !p.owned(stemOf(f)) {
			out = "download/" + p.tagged(stemOf(f), 60) + ".tdf"
			p.noteRename("file", f, out)
		}
		p.files[out] = []byte(strings.Join(kept, "\r\n\r\n") + "\r\n")
	}
	return nil
}

func (p *packer) packageNotes(sides sideOutput) []string {
	notes := []string{
		fmt.Sprintf("%s %s — Nanolathe faction add-on (docs/DESIGN_MODS_MUTATORS.md §5.8)", p.r.Name, p.r.Version),
		"Converted by cmd/factionpack from factionconfigs/" + p.r.ID + "-" + p.r.Version + ".json.",
	}
	if p.r.Source.File != "" {
		notes = append(notes, fmt.Sprintf("Source: %s, SHA-256 %s", p.r.Source.File, p.r.Source.SHA256))
	}
	if p.unitPack() {
		notes[0] = fmt.Sprintf("%s %s — Nanolathe unit pack (docs/DESIGN_MODS_MUTATORS.md §5.8)", p.r.Name, p.r.Version)
		notes = append(notes, fmt.Sprintf("Units for the existing sides: %d. Tag: %s. Menu entries on base builders: %d.", len(p.kept), p.tag, len(p.out.menu)))
	} else {
		notes = append(notes, fmt.Sprintf("Sides: %s. Tag: %s. Units: %d.", strings.Join(sides.names, ", "), p.tag, len(p.kept)))
	}
	notes = append(notes, fmt.Sprintf("Damage entries on base weapons for its own units: %d.", p.damageEntries))
	notes = append(notes, p.r.Notes...)
	notes = append(notes, "", "Conversion notes:")
	notes = append(notes, p.notes...)
	sort.Strings(p.renames)
	notes = append(notes, "", fmt.Sprintf("Renames (%d):", len(p.renames)))
	notes = append(notes, p.renames...)
	return notes
}

// write emits <out>/<id>-<version>/ with the identity-only metadata, the
// archive, the notes and the release's documents, and the same files as a
// zip beside it.
func write(r recipe, files map[string][]byte, notes []string, docs map[string][]byte, outDir string) error {
	name := r.ID + "-" + r.Version
	dir := filepath.Join(outDir, name)
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("%s exists; choose an empty output directory", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	list := make([]vfs.ArchiveFile, 0, len(paths))
	for _, p := range paths {
		list = append(list, vfs.ArchiveFile{Path: p, Data: files[p]})
	}
	var archive bytes.Buffer
	if err := vfs.WriteArchive(&archive, list, vfs.ArchiveWriteOptions{Compress: true}); err != nil {
		return err
	}
	meta, err := json.MarshalIndent(map[string]any{
		"schema": 1, "id": r.ID, "name": r.Name, "version": r.Version, "summary": r.Summary,
	}, "", "  ")
	if err != nil {
		return err
	}
	if r.Homepage != "" {
		var m map[string]any
		_ = json.Unmarshal(meta, &m)
		m["homepage"] = r.Homepage
		meta, _ = json.MarshalIndent(m, "", "  ")
	}
	prefix := "faction-"
	if r.Kind == "units" {
		prefix = "units-"
	}
	ufoName := prefix + r.ID + ".ufo"
	contents := map[string][]byte{
		"nanolathe-mod.json":    append(meta, '\n'),
		ufoName:                 archive.Bytes(),
		"FACTION-NANOLATHE.txt": []byte(strings.Join(notes, "\n") + "\n"),
	}
	order := []string{"nanolathe-mod.json", ufoName, "FACTION-NANOLATHE.txt"}
	docNames := make([]string, 0, len(docs))
	for name, data := range docs {
		contents[name] = data
		docNames = append(docNames, name)
	}
	sort.Strings(docNames)
	order = append(order, docNames...)
	for _, f := range order {
		if err := os.WriteFile(filepath.Join(dir, f), contents[f], 0o644); err != nil {
			return err
		}
	}
	zf, err := os.Create(filepath.Join(outDir, name+".zip"))
	if err != nil {
		return err
	}
	zw := zip.NewWriter(zf)
	for _, f := range order {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f, Method: zip.Store})
		if err != nil {
			return err
		}
		if _, err := w.Write(contents[f]); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if err := zf.Close(); err != nil {
		return err
	}
	sum := sha256.Sum256(archive.Bytes())
	fmt.Printf("%s: %d files, archive %d bytes, SHA-256 %s\n", path.Join(outDir, name), len(files), archive.Len(), hex.EncodeToString(sum[:]))
	return nil
}

// sideButtons cuts the kept sides' skirmish side-button frames out of the
// release's SIDEx art, where frame n is side n's, into a declaration bank
// whose SIDEx holds one frame per kept side in kept order; the skirmish
// menu splices them after the base sides' frames (§5.8). A release whose art
// lacks a kept side's frame gives none.
func (p *packer) sideButtons(sides sideOutput) ([]byte, bool) {
	data, ok := p.rel.read("anims/skirmish.gaf")
	if !ok {
		return nil, false
	}
	entry := gafEntries(data)["sidex"]
	if entry == nil {
		return nil, false
	}
	var frames []formats.GAFWriteFrame
	for _, ordinal := range sides.ordinals {
		if ordinal >= len(entry.Frames) || entry.Frames[ordinal].Frame == nil {
			p.notes = append(p.notes, fmt.Sprintf("the release's SIDEx art has no frame for side ordinal %d", ordinal))
			return nil, false
		}
		f := entry.Frames[ordinal].Frame
		frames = append(frames, formats.GAFWriteFrame{
			Width: f.Width, Height: f.Height, XOffset: f.XOffset, YOffset: f.YOffset,
			Duration: entry.Frames[ordinal].Value, Pixels: f.Pixels, Transparent: f.Transparent,
		})
	}
	out, err := formats.EncodeGAF([]formats.GAFWriteEntry{{Name: "SIDEx", Loop: true, Frames: frames}})
	if err != nil {
		p.notes = append(p.notes, "side-button art not written: "+err.Error())
		return nil, false
	}
	return out, true
}
