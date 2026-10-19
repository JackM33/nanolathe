package main

// Build-button art for the products the package places on builders' menus
// through download entries. Retail draws such a button only from
// anims/<unit>_gadget.GAF, entry <unit>, and leaves it art-less without one
// [07 R-WGT-01 §3]. A release that listed its units in its own copies of the
// base builders' pages drew them from those pages' banks instead, so the
// package carries each placed product's art in the file a download button
// reads.

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
)

// gadgetArt writes anims/<unit>_gadget.gaf for every product the package
// places with a menu or download entry and whose art the output lacks: the
// release's own entry of that name in any of its anims banks, else its
// unitpics picture as a one-frame entry in the game palette, else a picture
// the packager supplies as <art>/<unit>.png — a render of the release's own
// model, for a unit the release gives no picture at all. A product with none
// keeps an art-less button, as retail would draw it.
func (p *packer) gadgetArt(products []string) {
	var need []string
	seen := map[string]bool{}
	for _, product := range products {
		key := strings.ToLower(strings.TrimSpace(product))
		if _, kept := p.kept[key]; !kept || seen[key] {
			continue
		}
		seen[key] = true
		if _, ok := p.files["anims/"+key+"_gadget.gaf"]; ok || p.base.has("anims/"+key+"_gadget.gaf") {
			continue
		}
		need = append(need, key)
	}
	if len(need) == 0 {
		return
	}
	sort.Strings(need)
	wanted := map[string]bool{}
	for _, key := range need {
		wanted[key] = true
	}
	// The release's banks in path order; the first entry of a name wins.
	found := map[string]*formats.GAFEntry{}
	for _, f := range p.rel.list("anims", ".gaf", false) {
		data, ok := p.rel.read(f)
		if !ok {
			continue
		}
		g, err := formats.LoadGAF(data)
		if err != nil {
			continue
		}
		for i := range g.Entries {
			key := strings.ToLower(strings.TrimSpace(g.Entries[i].Name))
			if wanted[key] && found[key] == nil {
				found[key] = &g.Entries[i]
			}
		}
	}
	var game *formats.Palette
	if data, ok := p.base.read("palettes/palette.pal"); ok {
		game, _ = formats.LoadPAL(data)
	}
	extracted, pictured, supplied := 0, 0, 0
	for _, key := range need {
		name := p.keptName[key]
		var frames []formats.GAFWriteFrame
		if entry := found[key]; entry != nil {
			for _, ref := range entry.Frames {
				if f := ref.Frame; f != nil {
					frames = append(frames, formats.GAFWriteFrame{Width: f.Width, Height: f.Height, XOffset: f.XOffset, YOffset: f.YOffset,
						Duration: ref.Value, Pixels: f.Pixels, Transparent: f.Transparent})
				}
			}
			extracted++
		} else if frame, ok := p.pictureFrame(key, game); ok {
			frames = append(frames, frame)
			pictured++
		} else if frame, ok := p.suppliedFrame(key, game); ok {
			frames = append(frames, frame)
			supplied++
		}
		if len(frames) == 0 {
			p.notes = append(p.notes, fmt.Sprintf("%s has no button art in the release; its menu button stays art-less", name))
			continue
		}
		out, err := formats.EncodeGAF([]formats.GAFWriteEntry{{Name: name, Loop: true, Frames: frames}})
		if err != nil {
			p.notes = append(p.notes, fmt.Sprintf("%s button art not written: %v", name, err))
			continue
		}
		p.files["anims/"+key+"_gadget.gaf"] = out
	}
	if extracted+pictured+supplied > 0 {
		p.notes = append(p.notes, fmt.Sprintf("button art written for %d menu products: %d from the release's page banks, %d from its unit pictures, %d from supplied renders of its models", extracted+pictured+supplied, extracted, pictured, supplied))
	}
}

// pictureFrame is unitpics/<unit>.pcx as one opaque frame in the game
// palette: each pixel takes the game index of its colour, the nearest one
// when the picture's palette holds a colour the game's does not.
func (p *packer) pictureFrame(key string, game *formats.Palette) (formats.GAFWriteFrame, bool) {
	data, ok := p.rel.read("unitpics/" + key + ".pcx")
	if !ok || game == nil {
		return formats.GAFWriteFrame{}, false
	}
	pic, err := formats.LoadPCX(data)
	if err != nil || pic.Width == 0 || pic.Height == 0 {
		return formats.GAFWriteFrame{}, false
	}
	var index [256]byte
	for i, c := range pic.Palette {
		index[i] = nearestIndex(game, c)
	}
	pixels := make([]byte, int(pic.Width)*int(pic.Height))
	for y := 0; y < int(pic.Height); y++ {
		for x := 0; x < int(pic.Width); x++ {
			v, _, _ := pic.At(x, y)
			pixels[y*int(pic.Width)+x] = index[v]
		}
	}
	return formats.GAFWriteFrame{Width: pic.Width, Height: pic.Height, Pixels: pixels, Transparent: make([]bool, len(pixels))}, true
}

// nearestIndex is the game palette index of c, exact when present.
func nearestIndex(game *formats.Palette, c color.RGBA) byte {
	best, bestDist := 0, int(^uint(0)>>1)
	for i, g := range game.Colors {
		dr, dg, db := int(g.R)-int(c.R), int(g.G)-int(c.G), int(g.B)-int(c.B)
		if d := dr*dr + dg*dg + db*db; d < bestDist {
			best, bestDist = i, d
			if d == 0 {
				break
			}
		}
	}
	return byte(best)
}

// gadgetSize is a build button picture's side, the size of a unitpics
// picture.
const gadgetSize = 64

// suppliedFrame is <art>/<unit>.png as one opaque 64×64 frame: the
// picture's non-black bounds, centred on a square of black and averaged down
// (or sampled up) to the button, each colour then taking its nearest game
// palette index.
func (p *packer) suppliedFrame(key string, game *formats.Palette) (formats.GAFWriteFrame, bool) {
	if p.artDir == "" || game == nil {
		return formats.GAFWriteFrame{}, false
	}
	f, err := os.Open(filepath.Join(p.artDir, key+".png"))
	if err != nil {
		return formats.GAFWriteFrame{}, false
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		p.notes = append(p.notes, fmt.Sprintf("supplied art for %s not read: %v", key, err))
		return formats.GAFWriteFrame{}, false
	}
	box := image.Rectangle{}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if r, g, bl, _ := img.At(x, y).RGBA(); r|g|bl != 0 {
				box = box.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	if box.Empty() {
		return formats.GAFWriteFrame{}, false
	}
	side := max(box.Dx(), box.Dy()) * 9 / 8 // a margin around the model
	ox, oy := box.Min.X-(side-box.Dx())/2, box.Min.Y-(side-box.Dy())/2
	pixels := make([]byte, gadgetSize*gadgetSize)
	cache := map[color.RGBA]byte{}
	for y := 0; y < gadgetSize; y++ {
		for x := 0; x < gadgetSize; x++ {
			x0, x1 := ox+x*side/gadgetSize, ox+(x+1)*side/gadgetSize
			y0, y1 := oy+y*side/gadgetSize, oy+(y+1)*side/gadgetSize
			x1, y1 = max(x1, x0+1), max(y1, y0+1)
			var sr, sg, sb, n uint32
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					if image.Pt(sx, sy).In(b) {
						r, g, bl, _ := img.At(sx, sy).RGBA()
						sr, sg, sb = sr+r>>8, sg+g>>8, sb+bl>>8
					}
					n++
				}
			}
			c := color.RGBA{uint8(sr / n), uint8(sg / n), uint8(sb / n), 255}
			idx, ok := cache[c]
			if !ok {
				idx = nearestIndex(game, c)
				cache[c] = idx
			}
			pixels[y*gadgetSize+x] = idx
		}
	}
	return formats.GAFWriteFrame{Width: gadgetSize, Height: gadgetSize, Pixels: pixels, Transparent: make([]bool, len(pixels))}, true
}
