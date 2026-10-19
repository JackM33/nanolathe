package main

// The conversion: keep the recipe's sides and everything their units reach,
// resolve what the base install already supplies identically from the base,
// and rename everything else into the faction's tag so that no name or path
// can collide with the base game or another faction
// (docs/DESIGN_MODS_MUTATORS.md §5.8).

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
)

type packer struct {
	r        recipe
	artDir   string // packager-supplied <unit>.png renders, or ""
	base     *view
	bonus    *view // the install's own add-on archives alone
	rel      *view
	tag      string
	kept     map[string]tdfSection // folded unit name -> release FBI
	keptName map[string]string     // folded unit name -> authored name
	baseUnit map[string]bool

	relWeapons, baseWeapons   map[string]tdfSection
	relFeatures, baseFeatures map[string]tdfSection
	relMoves, baseMoves       map[string]tdfSection // by folded class Name
	relSounds, baseSounds     map[string]tdfSection
	relTex, baseTex           textureIndex

	files     map[string][]byte // output logical path -> bytes
	fileNames map[string]string // folded release path -> output path, decided
	weapons   map[string]string // folded weapon -> name used ("" missing)
	features  map[string]string // folded feature -> name used
	moves     map[string]string // folded class -> name used
	sounds    map[string]string // folded category -> name used
	textures  map[string]string // folded texture -> name used
	texCopies map[string]bool   // release texture GAF paths to copy
	logoCopy  bool              // the release logo bank is copied
	inFlight  map[string]bool   // definitions being decided: kind|name
	cycled    map[string]bool   // definitions a successor cycle named: kind|name
	out       struct {
		weapons, features, moves, sounds []string // emitted section texts
		menu                             []string // a unit pack's menu entries
	}
	renames []string // "kind old -> new" for the package notes
	notes   []string

	baseKept      int // a unit pack's release units the base defines
	bonusKept     int // a unit pack's release units the install's add-ons define
	bonusUnits    map[string]bool
	menuSeen      map[string]bool // builder|product a unit pack placed
	placed        []string        // products of kept download and menu entries
	damageEntries int
}

// bonusUnit reports a unit the install's own add-on archives define.
func (p *packer) bonusUnit(key string) bool {
	if p.bonusUnits == nil {
		p.bonusUnits = map[string]bool{}
		for _, f := range p.bonus.list("units", ".fbi", false) {
			data, _ := p.bonus.read(f)
			if doc, err := formats.ParseTDF(data); err == nil {
				if ui := doc.Root.Section("UNITINFO"); ui != nil {
					name, _ := ui.StringValue("UnitName", "")
					p.bonusUnits[lower(strings.TrimSpace(name))] = true
				}
			}
		}
	}
	return p.bonusUnits[key]
}

// unitPack reports a recipe of kind "units".
func (p *packer) unitPack() bool { return p.r.Kind == "units" }

// menuEntry records a unit pack's kept product on a base builder's menu, in
// the release's order, once per builder and product; the compiler gives it
// its page and button (§5.8).
func (p *packer) menuEntry(builder, product string) {
	builder, product = strings.TrimSpace(builder), strings.TrimSpace(product)
	key := lower(product)
	if _, kept := p.kept[key]; !kept || builder == "" {
		return
	}
	pair := lower(builder) + "|" + key
	if p.menuSeen == nil {
		p.menuSeen = map[string]bool{}
	}
	if p.menuSeen[pair] {
		return
	}
	p.menuSeen[pair] = true
	p.placed = append(p.placed, product)
	p.out.menu = append(p.out.menu, fmt.Sprintf("[MENUENTRY%d]\r\n\t{\r\n\tUNITMENU=%s;\r\n\tUNITNAME=%s;\r\n\t}", len(p.out.menu)+1, builder, p.keptName[key]))
}

// damage is the declaration's base-weapon damage entries: each DAMAGE value
// a release weapon of a base weapon's name gives one of the kept units, as
// the release authored it against its own version of the weapon. They change
// only how the base weapon hits those units (§5.8). An entry the base's own
// table already holds stays the base's.
func (p *packer) damage() []string {
	keys := make([]string, 0, len(p.baseWeapons))
	for key := range p.baseWeapons {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out []string
	for _, key := range keys {
		rel, ok := p.relWeapons[key]
		if !ok {
			continue
		}
		table := rel.section.Section("DAMAGE")
		if table == nil {
			continue
		}
		baseTable := p.baseWeapons[key].section.Section("DAMAGE")
		var lines []string
		for _, item := range table.ResolvedAssignments() {
			unit := lower(strings.TrimSpace(item.Key))
			if _, kept := p.kept[unit]; !kept {
				continue
			}
			if baseTable != nil {
				if _, has := baseTable.FirstValue(item.Key); has {
					p.notes = append(p.notes, fmt.Sprintf("damage of %s against %s left as the base's own entry", p.baseWeapons[key].section.OriginalName, item.Key))
					continue
				}
			}
			lines = append(lines, fmt.Sprintf("\t%s=%d;\r\n", p.keptName[unit], table.IntValue(item.Key, 0)))
		}
		if len(lines) == 0 {
			continue
		}
		p.damageEntries += len(lines)
		out = append(out, "["+strings.TrimSpace(p.baseWeapons[key].section.OriginalName)+"]\r\n\t{\r\n"+strings.Join(lines, "")+"\t}")
	}
	return out
}

// owned reports a name the faction owns without renaming: one starting with
// its tag or with one of its kept units' names, which the mount checks for
// uniqueness (§5.7).
func (p *packer) owned(name string) bool {
	n := lower(name)
	if strings.HasPrefix(n, p.tag) {
		return true
	}
	for unit := range p.kept {
		if strings.HasPrefix(n, unit) {
			return true
		}
	}
	return false
}

// tagged is name moved into the faction's namespace, shortened with a hash
// suffix when it would exceed max bytes.
func (p *packer) tagged(name string, max int) string {
	next := p.tag + "_" + name
	if len(next) <= max {
		return next
	}
	return p.hashTagged(name, max)
}

// hashTagged is name in the faction's namespace with a hash suffix, the form
// tagged shortens to, also used when the plain tagged name is one the
// release itself already gives another definition.
func (p *packer) hashTagged(name string, max int) string {
	sum := sha256.Sum256([]byte(lower(name)))
	keep := max - len(p.tag) - 1 - 7
	if keep < 1 {
		keep = 1
	}
	if keep > len(name) {
		keep = len(name)
	}
	return p.tag + "_" + name[:keep] + "_" + hex.EncodeToString(sum[:])[:6]
}

func (p *packer) noteRename(kind, from, to string) {
	if !strings.EqualFold(from, to) || from != to {
		p.renames = append(p.renames, fmt.Sprintf("%s %s -> %s", kind, from, to))
	}
}

// --- files -------------------------------------------------------------

// file decides one referenced file: the path the output names it by. A file
// the release does not supply is the base's (or missing) and is referenced
// as is; one the release supplies byte-identically to the base is the
// base's; otherwise the release's bytes (after transform) are copied, keeping
// the path when the faction owns the name and the base lacks it, else under
// the tag. transform may rewrite the bytes and reports whether it changed
// any reference.
func (p *packer) file(logical string, max int, transform func([]byte) ([]byte, bool, error)) (string, error) {
	key := lower(logical)
	if out, ok := p.fileNames[key]; ok {
		return out, nil
	}
	rel, relOK := p.rel.read(key)
	if !relOK {
		p.fileNames[key] = logical
		return logical, nil
	}
	data, changed := rel, false
	if transform != nil {
		var err error
		if data, changed, err = transform(rel); err != nil {
			return "", fmt.Errorf("%s: %w", logical, err)
		}
	}
	base, baseOK := p.base.read(key)
	if !changed && sameBytes(rel, relOK, base, baseOK) {
		p.fileNames[key] = logical
		return logical, nil
	}
	dir, file := path.Split(logical)
	ext := path.Ext(file)
	stem := strings.TrimSuffix(file, ext)
	out := logical
	if baseOK || !p.owned(stem) {
		out = dir + p.tagged(stem, max) + ext
		p.noteRename("file", logical, out)
	}
	p.fileNames[key] = out
	p.files[lower(out)] = data
	return out, nil
}

// stemOf returns the name a reference uses for a file it names without
// directory or extension.
func stemOf(logical string) string {
	file := path.Base(logical)
	return strings.TrimSuffix(file, path.Ext(file))
}

// model decides a 3DO: its texture names are resolved first, and a model
// whose textures were renamed is copied with those names repointed.
func (p *packer) model(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	out, err := p.file("objects3d/"+name+".3do", 31, func(data []byte) ([]byte, bool, error) {
		prims, err := modelTextures(data)
		if err != nil {
			return nil, false, err
		}
		changed := false
		for _, prim := range prims {
			next, err := p.texture(prim.texture)
			if err != nil {
				return nil, false, err
			}
			if next != prim.texture {
				changed = true
			}
		}
		if !changed {
			return data, false, nil
		}
		patched, err := renameModelTextures(data, func(t string) string {
			if next, ok := p.textures[lower(t)]; ok {
				return next
			}
			return t
		})
		return patched, true, err
	})
	if err != nil {
		return "", err
	}
	return stemOf(out), nil
}

// texture decides a model texture name. A name the release resolves to the
// same frames as the base is kept; otherwise the release's bank is copied
// (§5.8 "Packaging") and the name moved into the tag unless the faction owns
// it and the base lacks it.
func (p *packer) texture(name string) (string, error) {
	key := lower(name)
	if out, ok := p.textures[key]; ok {
		return out, nil
	}
	relEntry, relLogo, relOK := p.relTex.lookup(key)
	baseEntry, _, baseOK := p.baseTex.lookup(key)
	if !relOK || (baseOK && entriesEqual(relEntry, baseEntry)) {
		p.textures[key] = name
		return name, nil
	}
	if relLogo {
		p.logoCopy = true
	} else {
		p.texCopies[p.relTex.file[key]] = true
	}
	p.textures[key] = p.textureName(name, baseOK)
	return p.textures[key], nil
}

func (p *packer) textureName(name string, baseHas bool) string {
	if !baseHas && p.owned(name) {
		return name
	}
	next := p.tagged(name, gafNameBytes-1)
	p.noteRename("texture", name, next)
	return next
}

// copyTextureBanks writes every release texture bank a kept texture lives
// in. Each entry that is not frame-identical to the base's entry of that
// name is renamed exactly as texture() names it, so a copied bank can never
// shadow a base texture with different frames.
func (p *packer) copyTextureBanks() error {
	banks := make([]string, 0, len(p.texCopies))
	for f := range p.texCopies {
		banks = append(banks, f)
	}
	sort.Strings(banks)
	for _, f := range banks {
		data, _ := p.rel.read(f)
		out, err := p.renameBank(data, false)
		if err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		dest := "textures/" + p.tagged(stemOf(f), 60) + ".gaf"
		p.files[dest] = out
		p.noteRename("file", f, dest)
	}
	if p.logoCopy {
		data, _ := p.rel.read("textures/logos.gaf")
		out, err := p.renameBank(data, true)
		if err != nil {
			return fmt.Errorf("textures/logos.gaf: %w", err)
		}
		p.files["factions/"+lower(p.r.ID)+"/logos.gaf"] = out
	}
	return nil
}

func (p *packer) renameBank(data []byte, logos bool) ([]byte, error) {
	entries := gafEntries(data)
	return renameGAFEntries(data, func(name string) string {
		key := lower(name)
		if out, ok := p.textures[key]; ok {
			return out
		}
		var baseEntry *formats.GAFEntry
		var baseOK bool
		if logos {
			baseEntry, baseOK = p.baseTex.logos[key]
		} else {
			baseEntry, _, baseOK = p.baseTex.lookup(key)
		}
		if baseOK && entriesEqual(entries[key], baseEntry) {
			return name
		}
		next := p.textureName(name, baseOK)
		p.textures[key] = next
		return next
	})
}

// --- definitions ---------------------------------------------------------

// definition decides one named TDF definition (weapon, feature, movement
// class, sound category). rewrite rewrites the release section's references
// and reports whether any changed. A definition the release lacks is the
// base's; one equal to the base's with no changed reference is the base's;
// otherwise the release's section is emitted under the name the faction may
// use. A carried kind is always emitted: from the release's section, or the
// base's when the release has none.
func (p *packer) definition(kind, name string, decided map[string]string, rel, base map[string]tdfSection,
	rewrite func(tdfSection) (string, bool, error), emit func(text, name string)) (string, error) {
	key := lower(strings.TrimSpace(name))
	if key == "" {
		return name, nil
	}
	guard := kind + "|" + key
	if out, ok := decided[key]; ok {
		if p.inFlight[guard] {
			// A successor cycle back to a definition still being decided
			// takes its copy's name, which commits that definition to a copy.
			p.cycled[guard] = true
		}
		return out, nil
	}
	p.inFlight[guard] = true
	defer delete(p.inFlight, guard)
	relSec, relOK := rel[key]
	if !relOK && carried(kind) {
		relSec, relOK = base[key]
	}
	if !relOK {
		decided[key] = name
		return name, nil
	}
	baseSec, baseOK := base[key]
	sameAsBase := !carried(kind) && baseOK && sectionsEqual(relSec.section, baseSec.section)
	out := strings.TrimSpace(name)
	if baseOK || !p.owned(out) {
		out = p.tagged(out, 99)
		if _, taken := rel[lower(out)]; taken {
			out = p.hashTagged(strings.TrimSpace(name), 99)
		}
	}
	// The copy's name is set before the references are visited, so a
	// successor cycle back to this definition resolves to the copy. A
	// definition equal to the base's whose references all stay the base's,
	// and that no cycle named, is the base's after all.
	decided[key] = out
	text, changed, err := rewrite(relSec)
	if err != nil {
		return "", err
	}
	if sameAsBase && !changed && !p.cycled[guard] {
		decided[key] = name
		return name, nil
	}
	decided[key] = out
	if out != strings.TrimSpace(name) {
		p.noteRename(kind, name, out)
	}
	emit(renameSection(text, out), out)
	return out, nil
}

// carried reports the kinds a package always holds its own copy of, even
// where the release's definition equals the base's: the weapons its units
// fire and the features they leave. A mod that rebalances or drops a base
// weapon or wreck then changes no faction unit (§5.8 "With a mod").
func carried(kind string) bool { return kind == "weapon" || kind == "feature" }

func rewriteWith(text string, keys map[string]func(string) string) (string, bool) {
	next := rewriteValues(text, keys)
	return next, next != text
}

// keyFn adapts a decision that may fail into a rewriteValues callback,
// keeping the first error.
func keyFn(errp *error, decide func(string) (string, error)) func(string) string {
	return func(v string) string {
		if *errp != nil || v == "" {
			return v
		}
		out, err := decide(v)
		if err != nil {
			*errp = err
			return v
		}
		return out
	}
}

func (p *packer) animBank(name string) (string, error) {
	if name == "" {
		return name, nil
	}
	out, err := p.file("anims/"+name+".gaf", 60, nil)
	return stemOf(out), err
}

func (p *packer) sound(name string) (string, error) {
	if name == "" {
		return name, nil
	}
	out, err := p.file("sounds/"+name+".wav", 60, nil)
	return stemOf(out), err
}

func (p *packer) weapon(name string) (string, error) {
	return p.definition("weapon", name, p.weapons, p.relWeapons, p.baseWeapons, func(s tdfSection) (string, bool, error) {
		var err error
		text, changed := rewriteWith(s.text, map[string]func(string) string{
			"model":             keyFn(&err, p.model),
			"explosiongaf":      keyFn(&err, p.animBank),
			"waterexplosiongaf": keyFn(&err, p.animBank),
			"lavaexplosiongaf":  keyFn(&err, p.animBank),
			"soundstart":        keyFn(&err, p.sound),
			"soundhit":          keyFn(&err, p.sound),
			"soundwater":        keyFn(&err, p.sound),
		})
		return text, changed, err
	}, func(text, _ string) { p.out.weapons = append(p.out.weapons, text) })
}

func (p *packer) feature(name string) (string, error) {
	return p.definition("feature", name, p.features, p.relFeatures, p.baseFeatures, func(s tdfSection) (string, bool, error) {
		var err error
		text, changed := rewriteWith(s.text, map[string]func(string) string{
			"object":           keyFn(&err, p.model),
			"filename":         keyFn(&err, p.animBank),
			"featuredead":      keyFn(&err, p.feature),
			"featurereclamate": keyFn(&err, p.feature),
			"featureburnt":     keyFn(&err, p.feature),
			"burnweapon":       keyFn(&err, p.weapon),
		})
		return text, changed, err
	}, func(text, _ string) { p.out.features = append(p.out.features, text) })
}

func (p *packer) movement(name string) (string, error) {
	return p.definition("movement class", name, p.moves, p.relMoves, p.baseMoves, func(s tdfSection) (string, bool, error) {
		return s.text, false, nil
	}, func(text, name string) {
		// A class is named by its Name key, not its section.
		text = rewriteValues(text, map[string]func(string) string{"name": func(string) string { return name }})
		p.out.moves = append(p.out.moves, text)
	})
}

func (p *packer) soundCategory(name string) (string, error) {
	return p.definition("sound category", name, p.sounds, p.relSounds, p.baseSounds, func(s tdfSection) (string, bool, error) {
		var err error
		keys := map[string]func(string) string{}
		for _, it := range s.section.Assignments() {
			keys[lower(it.Key)] = keyFn(&err, p.sound)
		}
		text, changed := rewriteWith(s.text, keys)
		return text, changed, err
	}, func(text, _ string) { p.out.sounds = append(p.out.sounds, text) })
}
