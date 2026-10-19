package main

// Faction add-ons in the skirmish menu and the battle interface
// (docs/DESIGN_MODS_MUTATORS.md §5.8).

import (
	"path"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// spliceFactionSideButtons gives the skirmish menu's side button a frame for
// every faction side. The authored SIDEx art holds one frame per base side,
// at its ordinal, then the button's own frames; a faction declaration's
// skirmish.gaf holds its sides' frames in its sides' order. The faction
// frames go after the base sides' so frame n stays side n's. A faction side
// without art repeats the frame after the base sides', so the button still
// draws.
func spliceFactionSideButtons(fs vfs.FSOps, art *formats.GAF) {
	sides, err := content.CompileSides(fs)
	if err != nil {
		return
	}
	var entry *formats.GAFEntry
	for i := range art.Entries {
		if strings.EqualFold(art.Entries[i].Name, "SIDEx") {
			entry = &art.Entries[i]
		}
	}
	if entry == nil {
		return
	}
	baseSides := 0
	for _, s := range sides {
		if !strings.HasPrefix(s.Provenance.LogicalPath, content.FactionDir+"/") {
			baseSides++
		}
	}
	if baseSides == len(sides) || baseSides > len(entry.Frames) {
		return
	}
	banks := map[string][]formats.GAFFrameRef{}
	used := map[string]int{}
	frames := append([]formats.GAFFrameRef(nil), entry.Frames[:baseSides]...)
	for _, s := range sides[baseSides:] {
		dir := path.Dir(s.Provenance.LogicalPath)
		bank, ok := banks[dir]
		if !ok {
			if g, err := formats.LoadGAFFile(fs, dir+"/skirmish.gaf"); err == nil {
				for i := range g.Entries {
					if strings.EqualFold(g.Entries[i].Name, "SIDEx") {
						bank = g.Entries[i].Frames
					}
				}
			}
			banks[dir] = bank
		}
		k := used[dir]
		used[dir]++
		switch {
		case k < len(bank):
			frames = append(frames, bank[k])
		case baseSides < len(entry.Frames):
			frames = append(frames, entry.Frames[baseSides])
		default:
			frames = append(frames, entry.Frames[len(entry.Frames)-1])
		}
	}
	frames = append(frames, entry.Frames[baseSides:]...)
	entry.Frames = frames
	entry.FrameCount = uint16(len(frames))
}

// factionSupportGAF is the shared support bank a faction side's interface
// draws from: the declaration's commongui.gaf, which a faction release
// authored as its replacement of anims/commongui.gaf, layered over the shared
// bank for that side's player alone. Its entries come first and the shared
// bank's other entries follow, so no other side's interface changes. A base
// side, or a faction without the bank, keeps the shared bank.
func factionSupportGAF(fs vfs.FSOps, side *content.SideDef, common *formats.GAF) *formats.GAF {
	if side == nil || !strings.HasPrefix(side.Provenance.LogicalPath, content.FactionDir+"/") {
		return common
	}
	own, err := formats.LoadGAFFile(fs, path.Dir(side.Provenance.LogicalPath)+"/commongui.gaf")
	if err != nil || own == nil {
		return common
	}
	if common == nil {
		return own
	}
	merged := *own
	merged.Entries = append([]formats.GAFEntry(nil), own.Entries...)
	for _, e := range common.Entries {
		if _, ok := own.Find(e.Name); !ok {
			merged.Entries = append(merged.Entries, e)
		}
	}
	merged.EntryCount = uint32(len(merged.Entries))
	return &merged
}
