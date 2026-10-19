package vfs

import "path/filepath"

// rehomedArchive is an archive authored in the retail directories, presented
// under the directories a content layout renames them to. A content set
// whose layout reads `units` from `unitsE` hides every file under `units`,
// so an archive mounted beside it with its files under `units` would add
// nothing; rehomed, its `units/x.fbi` answers as `unitsE/x.fbi`, beside the
// content set's own, and the layout's view finds it under the retail name
// the loaders ask for. Faction add-ons mount this way under a mod whose
// layout renames directories (docs/DESIGN_MODS_MUTATORS.md §5.8 "With a
// mod"). The table is built once at mount, so a lookup costs what the
// archive's own does.
type rehomedArchive struct {
	archive *Archive
	index   providerIndex
	audit   []EntryInfo
	retail  []EntryInfo
}

// rehomeArchive builds the rehomed view of a, after its mount information
// is set. An entry the archive itself authors under a renamed directory
// keeps that path, and wins over a rehomed one of the same path, so the
// result never depends on map order.
func rehomeArchive(a *Archive, layout Layout) *rehomedArchive {
	r := &rehomedArchive{archive: a, index: newProviderIndex()}
	for name, entry := range a.entries {
		home := layout.home(name)
		if home != name {
			if _, authored := a.entries[home]; authored {
				continue
			}
		}
		r.index.entries[home] = rehomedEntry(entry, home)
	}
	r.audit = rehomeInfos(a.auditEntries(), layout)
	r.retail = rehomeInfos(a.retailEntries(), layout)
	return r
}

func rehomeInfos(infos []EntryInfo, layout Layout) []EntryInfo {
	for i := range infos {
		home := layout.home(infos[i].Path)
		infos[i].Path, infos[i].Source.LogicalPath = home, home
	}
	return infos
}

// home is the folded path a retail-named archive path takes under the
// layout.
func (l Layout) home(name string) string {
	out, err := cleanPath(l.rewrite(name))
	if err != nil {
		return name
	}
	return out
}

func rehomedEntry(entry *providerEntry, home string) *providerEntry {
	info := entry.info
	info.Path, info.Source.LogicalPath = home, home
	out := &providerEntry{info: info, readRange: entry.readRange}
	if open := entry.open; open != nil {
		out.open = func() (File, error) {
			file, err := open()
			if err != nil {
				return nil, err
			}
			return rehomedFile{File: file, info: info}, nil
		}
	}
	return out
}

// rehomedFile reports the path the overlay found it under.
type rehomedFile struct {
	File
	info EntryInfo
}

func (f rehomedFile) Info() EntryInfo { return f.info }

func (r *rehomedArchive) lookup(name string) (*providerEntry, bool) { return r.index.lookup(name) }
func (r *rehomedArchive) children(parent string) []*providerEntry {
	return r.index.children(parent)
}
func (r *rehomedArchive) auditEntries() []EntryInfo { return append([]EntryInfo(nil), r.audit...) }
func (r *rehomedArchive) retailEntries() []EntryInfo {
	return append([]EntryInfo(nil), r.retail...)
}
func (r *rehomedArchive) close() error { return r.archive.close() }

// mountArchiveRehomed is MountArchive with the archive presented under
// layout's directories; an empty layout mounts it as MountArchive does.
func (f *FS) mountArchiveRehomed(filename string, priority int, layout Layout) (*Archive, error) {
	if len(layout.entries) == 0 {
		return f.MountArchive(filename, priority)
	}
	if f.alreadyMounted(filename, filepath.Base(filename)) {
		return nil, nil
	}
	a, err := OpenArchive(filename, ArchiveOptions{})
	if err != nil {
		return nil, err
	}
	f.rememberMount(filename, filepath.Base(filename))
	a.setMountInfo(priority, f.nextOrder)
	f.insertMount(mountedProvider{provider: rehomeArchive(a, layout), priority: priority, order: f.nextOrder})
	return a, nil
}
