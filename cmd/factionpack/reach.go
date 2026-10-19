package main

// Reachability: a package keeps only the units a player of it can build.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
)

// buildEdges is the release's build relationships, folded: each builder's
// [CANBUILD] products in its sidedata.tdf and the products its download
// entries add to that builder's menu.
func (p *packer) buildEdges() map[string][]string {
	edges := map[string][]string{}
	add := func(builder, product string) {
		b, u := strings.ToLower(strings.TrimSpace(builder)), strings.ToLower(strings.TrimSpace(product))
		if b != "" && u != "" {
			edges[b] = append(edges[b], u)
		}
	}
	if data, ok := p.rel.read("gamedata/sidedata.tdf"); ok {
		if doc, err := formats.ParseTDF(data); err == nil {
			if canbuild := doc.Root.Section("CANBUILD"); canbuild != nil {
				for _, builder := range canbuild.Sections() {
					for i := 1; ; i++ {
						v, ok := builder.StringValue(fmt.Sprintf("canbuild%d", i), "")
						if !ok {
							break
						}
						add(builder.OriginalName, v)
					}
				}
			}
		}
	}
	for _, f := range p.rel.list("download", ".tdf", false) {
		data, _ := p.rel.read(f)
		doc, err := formats.ParseTDF(data)
		if err != nil {
			continue
		}
		for _, s := range doc.Root.Sections() {
			builder, _ := s.StringValue("UNITMENU", "")
			product, _ := s.StringValue("UNITNAME", "")
			add(builder, product)
		}
	}
	return edges
}

// prune keeps the selected units a player of the package can reach and
// leaves out the rest, each named in the notes. A faction's players start
// from its kept sides' commanders; a unit pack's from the base builders.
// Either then builds what the release's build lists and download entries
// give each reached unit. A unit nothing reaches — a release's unfinished,
// retired or computer-only unit — would otherwise sit in every catalog,
// restriction list and unit viewer without a way to build it.
func (p *packer) prune() error {
	edges := p.buildEdges()
	var queue []string
	reached := map[string]bool{}
	visit := func(key string) {
		if _, kept := p.kept[key]; kept && !reached[key] {
			reached[key] = true
			queue = append(queue, key)
		}
	}
	if p.unitPack() {
		builders := make([]string, 0, len(edges))
		for b := range edges {
			builders = append(builders, b)
		}
		sort.Strings(builders)
		for _, b := range builders {
			if p.baseUnit[b] {
				for _, u := range edges[b] {
					visit(u)
				}
			}
		}
	} else {
		for _, commander := range p.sideCommanders() {
			visit(commander)
		}
	}
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		for _, u := range edges[key] {
			visit(u)
		}
	}
	var dropped []string
	for key := range p.kept {
		if !reached[key] {
			dropped = append(dropped, p.keptName[key])
			delete(p.kept, key)
			delete(p.keptName, key)
		}
	}
	sort.Strings(dropped)
	for _, name := range dropped {
		p.notes = append(p.notes, fmt.Sprintf("unit %s left out: no builder a player of this package can reach offers it", name))
	}
	if len(p.kept) == 0 {
		return fmt.Errorf("no kept unit is reachable from the package's %s", map[bool]string{true: "base builders", false: "commanders"}[p.unitPack()])
	}
	return nil
}

// sideCommanders is the folded commander of each release side record the
// recipe keeps, the first record of a name as sides() keeps it.
func (p *packer) sideCommanders() []string {
	data, ok := p.rel.read("gamedata/sidedata.tdf")
	if !ok {
		return nil
	}
	doc, err := formats.ParseTDF(data)
	if err != nil {
		return nil
	}
	want := map[string]bool{}
	for _, s := range p.r.Sides {
		want[strings.ToLower(s)] = true
	}
	var out []string
	taken := map[string]bool{}
	for i := 0; ; i++ {
		s := doc.Root.Section(fmt.Sprintf("SIDE%d", i))
		if s == nil {
			break
		}
		name, _ := s.StringValue("name", "")
		name = strings.ToLower(strings.TrimSpace(name))
		if !want[name] || taken[name] {
			continue
		}
		taken[name] = true
		commander, _ := s.StringValue("commander", "")
		out = append(out, strings.ToLower(strings.TrimSpace(commander)))
	}
	return out
}
