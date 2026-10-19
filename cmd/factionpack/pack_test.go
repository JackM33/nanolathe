package main

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
)

func TestTopSectionsSkipCommentsAndNesting(t *testing.T) {
	text := []byte("// [NOT] {\n[A]\n\t{\n\tx=1; /* } [B] */\n\t[SUB]{y=2;}\n\t}\n[B]{z=3;}\n")
	got := topSections(text)
	if len(got) != 2 || got[0].Name != "A" || got[1].Name != "B" {
		t.Fatalf("sections %+v", got)
	}
	if !strings.HasSuffix(sectionText(text, got[0]), "[SUB]{y=2;}\n\t}") {
		t.Fatalf("section A text %q", sectionText(text, got[0]))
	}
}

func TestRewriteValuesTouchesOnlyNamedKeys(t *testing.T) {
	in := "[U]{\n\tweapon1=ARM_LASER;\n\tmyweapon1=KEEP;\n\tCorpse = armdead ;\n}"
	out := rewriteValues(in, map[string]func(string) string{
		"weapon1": func(v string) string { return "tal_" + v },
		"corpse":  func(v string) string { return "tal_" + v },
	})
	if !strings.Contains(out, "weapon1=tal_ARM_LASER;") || !strings.Contains(out, "myweapon1=KEEP;") || !strings.Contains(out, "Corpse = tal_armdead;") {
		t.Fatalf("rewritten %q", out)
	}
}

func TestTaggedNamesFitTheirBound(t *testing.T) {
	p := &packer{tag: "tll"}
	if got := p.tagged("short", 31); got != "tll_short" {
		t.Fatalf("got %q", got)
	}
	long := strings.Repeat("x", 40)
	got := p.tagged(long, 31)
	if len(got) > 31 || !strings.HasPrefix(got, "tll_") || got == p.tagged(long+"y", 31) {
		t.Fatalf("shortened %q (%d bytes) must fit and stay distinct", got, len(got))
	}
}

// A one-object model with one textured quad: the rename appends the new
// name and repoints the primitive, leaving every other byte in place.
func TestRenameModelTexturesRepointsThePrimitive(t *testing.T) {
	model := &formats.ThreeDO{Root: 0, Objects: []formats.ThreeDOObject{{
		Name:       "base",
		Vertices:   []formats.ThreeDOVertex{{}, {X: 1}, {X: 1, Z: 1}, {Z: 1}},
		Primitives: []formats.ThreeDOPrimitive{{VertexIndices: []uint16{0, 1, 2, 3}, TextureName: "metal"}},
		Parent:     -1, FirstChild: -1, NextSibling: -1,
	}}}
	data, err := formats.EncodeThreeDO(model)
	if err != nil {
		t.Fatal(err)
	}
	out, err := renameModelTextures(data, func(name string) string { return "rac_" + name })
	if err != nil {
		t.Fatal(err)
	}
	if !onlyFieldChanged(data, out) {
		t.Fatal("bytes other than the texture field changed")
	}
	got, err := formats.LoadThreeDO(out)
	if err != nil || got.Objects[0].Primitives[0].TextureName != "rac_metal" {
		t.Fatalf("decoded texture %+v err %v", got, err)
	}
}

func onlyFieldChanged(before, after []byte) bool {
	prims, _ := modelTextures(before)
	diff := 0
	for i := range before {
		if before[i] != after[i] {
			inField := false
			for _, p := range prims {
				if i >= p.field && i < p.field+4 {
					inField = true
				}
			}
			if !inField {
				return false
			}
			diff++
		}
	}
	return diff > 0
}

func TestRenameGAFEntriesInPlace(t *testing.T) {
	data, err := formats.EncodeGAF([]formats.GAFWriteEntry{{Name: "metal", Loop: true, Frames: []formats.GAFWriteFrame{{Width: 1, Height: 1, Duration: 10, Pixels: []byte{4}}}}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := renameGAFEntries(data, func(name string) string { return "rac_" + name })
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(data) || binary.LittleEndian.Uint32(out[4:]) != 1 {
		t.Fatal("renaming changed the layout")
	}
	g, err := formats.LoadGAF(out)
	if err != nil || g.Entries[0].Name != "rac_metal" || g.Entries[0].Frames[0].Frame.Pixels[0] != 4 {
		t.Fatalf("decoded %+v err %v", g, err)
	}
	if _, err := renameGAFEntries(data, func(string) string { return strings.Repeat("n", 32) }); err == nil {
		t.Fatal("a 32-byte name must be refused: the field holds 31 and a NUL")
	}
}

// Names fold as the overlay folds them, ASCII only: a release's Latin-1
// byte stays itself, so a copied file keeps the name its references use.
func TestLowerFoldsASCIIOnly(t *testing.T) {
	if got := lower("Objects3d/Ketzer-Gau\xdf.3DO"); got != "objects3d/ketzer-gau\xdf.3do" {
		t.Fatalf("lower = %q", got)
	}
}
