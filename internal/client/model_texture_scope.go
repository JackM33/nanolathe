package client

import (
	"fmt"
	"strings"
	"sync"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// PreviewModelTextureAssets retains decoded immutable art for one mounted
// content set (DESIGN_INTERFACE_HUD_INPUT §3.17). Its maps are private and its
// lazy decodes are serialized; registries only share geometry and pixels.
// Loaded-model identities, texture players and feature cursors remain owned by
// each preview battle [03 R-CRD-005 §1][I6].
type PreviewModelTextureAssets struct {
	fs             *vfs.FS
	teamLogos      string
	primary, logos map[string]texRef
	mu             sync.Mutex
	models         map[string]previewModelResult
	features       map[string]previewFeatureBank
}

type previewModelResult struct {
	model *unitModel
	err   error
}

type previewFeatureBank struct {
	gaf *formats.GAF
	err error
}

// NewPreviewModelTextureAssets decodes the normal full texture namespace once.
// Scope narrows models and feature banks, preserving texture precedence even
// when several banks author an entry with the same name [03 §2.4.1].
func NewPreviewModelTextureAssets(fs *vfs.FS, teamLogos ...string) *PreviewModelTextureAssets {
	r := newModelTextureRegistry(fs, false, teamLogos...)
	return &PreviewModelTextureAssets{
		fs: fs, teamLogos: r.teamLogos, primary: r.primary, logos: r.logos,
		models: map[string]previewModelResult{}, features: map[string]previewFeatureBank{},
	}
}

// NewPreviewModelTextureRegistry prepares only the units the authored fixture
// can create and their projectile/corpse dependencies. Nil unitNames retains
// full preparation; a non-nil empty slice prepares no units. extraWeapons names
// independent scene producers, such as the composed meteor scheduler. Ordinary
// battles continue using NewModelTextureRegistry and its complete load order.
func NewPreviewModelTextureRegistry(assets *PreviewModelTextureAssets, cat *content.Catalog, terrain *world.Terrain, restoreStart int, unitNames []string, extraWeapons ...string) (*ModelTextureRegistry, error) {
	if assets == nil {
		return nil, fmt.Errorf("preview model textures: no mounted assets")
	}
	r := emptyModelTextureRegistry(assets.fs, false, assets.teamLogos)
	r.previewAssets = assets
	r.primary, r.logos = assets.primary, assets.logos
	if unitNames != nil {
		r.previewUnitNames = make([]string, len(unitNames))
		for i, name := range unitNames {
			key := content.CanonicalKey(name)
			if cat != nil {
				if def, ok := cat.Unit(key); !ok || def == nil {
					return nil, fmt.Errorf("preview model textures: unknown unit %q", name)
				}
			}
			r.previewUnitNames[i] = key
		}
		r.previewModels = previewProjectileModels(cat, terrain, r.previewUnitNames, extraWeapons)
	}
	return r.prepareCatalog(cat, terrain, restoreStart)
}

func scopedUnitRecords(cat *content.Catalog, unitNames []string) []*content.UnitDef {
	if cat == nil {
		return nil
	}
	records := cat.UnitRecords()
	if unitNames == nil {
		return records
	}
	want := make(map[string]bool, len(unitNames))
	byDefinition := make(map[*content.UnitDef]bool, len(unitNames))
	for _, name := range unitNames {
		want[content.CanonicalKey(name)] = true
		if def, ok := cat.Unit(name); ok {
			byDefinition[def] = true
		}
	}
	var selected []*content.UnitDef
	for _, def := range records {
		if def == nil {
			continue
		}
		key := def.CanonicalKey
		if key == "" {
			key = content.CanonicalKey(def.UnitName)
		}
		if byDefinition[def] || want[key] {
			selected = append(selected, def)
		}
	}
	return selected
}

func (r *ModelTextureRegistry) unitRecords(cat *content.Catalog) []*content.UnitDef {
	return scopedUnitRecords(cat, r.previewUnitNames)
}

func previewProjectileModels(cat *content.Catalog, terrain *world.Terrain, unitNames, extraWeapons []string) map[string]bool {
	models := map[string]bool{}
	if cat == nil {
		return models
	}
	add := func(def *content.WeaponDef, name string) {
		if def == nil {
			def, _ = cat.WeaponLink(name)
		}
		if def != nil && def.Model != "" {
			models[ckey(def.Model)] = true
		}
	}
	for _, unit := range scopedUnitRecords(cat, unitNames) {
		for _, weapon := range [...]struct {
			def  *content.WeaponDef
			name string
		}{
			{unit.Weapon1Def, unit.Weapon1}, {unit.Weapon2Def, unit.Weapon2}, {unit.Weapon3Def, unit.Weapon3},
			{unit.ExplodeAsDef, unit.ExplodeAs}, {unit.SelfDestructAsDef, unit.SelfDestructAs},
			{unit.TransportedExplodeAsDef, unit.TransportedExplodeAs}, {unit.TransportedSelfDestructAsDef, unit.TransportedSelfDestructAs},
		} {
			add(weapon.def, weapon.name)
		}
	}
	var admitted []*content.FeatureDef
	if terrain != nil {
		admitted = terrain.FeatureDefs
	}
	for _, def := range battleFeatureDefinitionsForUnits(cat, admitted, unitNames) {
		add(nil, def.BurnWeapon)
	}
	for _, name := range extraWeapons {
		add(nil, name)
	}
	return models
}

func (r *ModelTextureRegistry) loadModel(name string) (*unitModel, error) {
	if r.previewAssets == nil {
		return expandModelFromFSStrict(r.fs, name)
	}
	a := r.previewAssets
	path := strings.ReplaceAll(name, `\`, "/")
	if !strings.Contains(strings.ToLower(path), ".3do") {
		path = "objects3d/" + path + ".3do"
	}
	key := strings.ToLower(path)
	a.mu.Lock()
	result, ok := a.models[key]
	if !ok {
		result.model, result.err = expandModelFromFSStrict(a.fs, name)
		a.models[key] = result
	}
	a.mu.Unlock()
	if result.err != nil || result.model == nil {
		return nil, result.err
	}
	// The immutable piece arrays can be shared. A distinct Model wrapper
	// preserves the registry's per-load primitive identity even for two unit
	// definitions or features naming the same file [03 R-CRD-005 §1].
	identity := *result.model.compiled
	return &unitModel{compiled: &identity, pieceByName: result.model.pieceByName, reach: result.model.reach}, nil
}

func (a *PreviewModelTextureAssets) featureBank(key string) previewFeatureBank {
	a.mu.Lock()
	defer a.mu.Unlock()
	bank, ok := a.features[key]
	if !ok {
		bank.gaf, bank.err = formats.LoadGAFFile(a.fs, "anims/"+key+".gaf")
		a.features[key] = bank
	}
	return bank
}
