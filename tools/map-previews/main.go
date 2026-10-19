// map-previews exports the authored minimaps from verified catalogue ZIPs.
// It writes small PNGs and their optional catalogue references; map packages
// themselves remain byte-identical. With --annotate it draws the map's own
// data over the authored picture — metal deposits, reclaimable features,
// water and steep terrain — and records a legend for the browser. See
// DESIGN_MODS_MUTATORS §5.6.
package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

type asset struct {
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func main() {
	manifest := flag.String("manifest", "", "map manifest to update")
	archives := flag.String("archives", "", "directory containing verified catalogue ZIPs")
	root := flag.String("root", "", "reference install supplying the authored palette")
	out := flag.String("out", "", "PNG output directory")
	baseURL := flag.String("url", "https://nanolathe.gg/maps/previews/", "published PNG directory URL")
	annotate := flag.Bool("annotate", false, "draw the map's own data over the picture and record a legend")
	var featuresRoots stringList
	flag.Var(&featuresRoots, "features-root", "extra feature source root, mounted under the base install (repeatable)")
	flag.Parse()
	if err := run(*manifest, *archives, *root, *out, *baseURL, *annotate, featuresRoots); err != nil {
		fmt.Fprintln(os.Stderr, "map-previews:", err)
		os.Exit(1)
	}
}

// stringList is a repeatable string flag.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

// previewLegend names what an annotated picture shows: the coloured dots the
// browser draws beside the preview, and the annotations they stand for.
var previewLegend = []map[string]string{
	{"color": "#ffdc00", "label": "metal"},
	{"color": "#40c840", "label": "reclaim"},
}

func run(manifest, archives, root, out, baseURL string, annotate bool, featuresRoots []string) error {
	if manifest == "" || archives == "" || root == "" || out == "" {
		return fmt.Errorf("--manifest, --archives, --root and --out are required")
	}
	base, err := url.Parse(baseURL)
	if err != nil || base.Scheme != "https" || base.Host == "" || !strings.HasSuffix(base.Path, "/") {
		return fmt.Errorf("--url must be an HTTPS directory")
	}
	raw, err := os.ReadFile(manifest)
	if err != nil {
		return err
	}
	var document map[string]json.RawMessage
	if err = json.Unmarshal(raw, &document); err != nil {
		return err
	}
	var maps []map[string]json.RawMessage
	if err = json.Unmarshal(document["maps"], &maps); err != nil {
		return err
	}
	fs := vfs.New()
	defer fs.Close()
	if err = fs.MountGameDirectory(root); err != nil {
		return err
	}
	tables, err := palette.Load(fs)
	if err != nil {
		return err
	}
	colors := make(color.Palette, 256)
	for i, rgb := range tables.Base {
		colors[i] = color.RGBA{R: rgb[0], G: rgb[1], B: rgb[2], A: 255}
	}
	if err = os.MkdirAll(out, 0755); err != nil {
		return err
	}
	var total int64
	for _, entry := range maps {
		var archive asset
		var logical string
		if err = json.Unmarshal(entry["archive"], &archive); err != nil {
			return err
		}
		if err = json.Unmarshal(entry["map"], &logical); err != nil {
			return err
		}
		u, err := url.Parse(archive.URL)
		if err != nil {
			return err
		}
		filename := path.Base(u.Path)
		payload, err := os.ReadFile(filepath.Join(archives, filename))
		if err != nil {
			return err
		}
		digest := sha256.Sum256(payload)
		if int64(len(payload)) != archive.Size || hex.EncodeToString(digest[:]) != archive.SHA256 {
			return fmt.Errorf("%s: archive identity differs", filename)
		}
		z, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
		if err != nil {
			return err
		}
		terrainPath := strings.TrimSuffix(logical, path.Ext(logical)) + ".tnt"
		file, err := z.Open(terrainPath)
		if err != nil {
			return err
		}
		terrainBytes, readErr := io.ReadAll(io.LimitReader(file, (64<<20)+1))
		file.Close()
		if readErr != nil {
			return readErr
		}
		if len(terrainBytes) > 64<<20 {
			return fmt.Errorf("%s: terrain exceeds map library budget", filename)
		}
		terrain, err := formats.LoadTNT(terrainBytes)
		if err != nil {
			return err
		}
		picture, err := minimap(terrain, colors)
		if err != nil {
			return fmt.Errorf("%s: %w", filename, err)
		}
		if annotate {
			defs, err := featureDefs(root, z, featuresRoots)
			if err != nil {
				return fmt.Errorf("%s: %w", filename, err)
			}
			annotatePicture(picture, terrain, defs, colors)
			entry["legend"], err = json.Marshal(previewLegend)
			if err != nil {
				return err
			}
		}
		var encoded bytes.Buffer
		if err = png.Encode(&encoded, picture); err != nil {
			return err
		}
		if encoded.Len() > 1<<20 {
			return fmt.Errorf("%s: PNG exceeds preview download budget", filename)
		}
		pngName := strings.TrimSuffix(filename, path.Ext(filename)) + ".png"
		if err = os.WriteFile(filepath.Join(out, pngName), encoded.Bytes(), 0644); err != nil {
			return err
		}
		digest = sha256.Sum256(encoded.Bytes())
		preview := asset{URL: base.ResolveReference(&url.URL{Path: pngName}).String(), Size: int64(encoded.Len()), SHA256: hex.EncodeToString(digest[:])}
		entry["preview"], err = json.Marshal(preview)
		if err != nil {
			return err
		}
		total += preview.Size
		fmt.Printf("%s: %dx%d, %d bytes\n", pngName, picture.Rect.Dx(), picture.Rect.Dy(), preview.Size)
	}
	document["maps"], err = json.Marshal(maps)
	if err != nil {
		return err
	}
	raw, err = json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(manifest, append(raw, '\n'), 0644); err != nil {
		return err
	}
	fmt.Printf("%d previews, %d bytes total\n", len(maps), total)
	return nil
}

// featureDefs compiles the feature definitions a map package sees: the base
// install's features, the extra feature source roots (a catalogue dependency
// like the community feature library), plus any features/ tree the package
// itself carries — the same resolution the engine's map admission uses
// (§5.6). An unresolved successor hop anywhere is not fatal here: annotation
// only needs each definition's metal and indestructible flags, so a failed
// compile retries without the package's own tree and then gives up quietly.
func featureDefs(root string, z *zip.Reader, featuresRoots []string) (map[string]*content.FeatureDef, error) {
	tmp, err := os.MkdirTemp("", "map-previews-features-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	wrote := false
	for _, file := range z.File {
		name := strings.ToLower(file.Name)
		if file.FileInfo().IsDir() || !strings.HasPrefix(name, "features/") || !strings.HasSuffix(name, ".tdf") {
			continue
		}
		clean := path.Clean(file.Name)
		if strings.HasPrefix(clean, "..") || strings.Contains(clean, "/../") {
			continue
		}
		target := filepath.Join(tmp, filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, err
		}
		src, err := file.Open()
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(src, 1<<20))
		src.Close()
		if readErr != nil {
			return nil, readErr
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return nil, err
		}
		wrote = true
	}
	compile := func(roots []string) (map[string]*content.FeatureDef, error) {
		fs := vfs.New()
		defer fs.Close()
		if err := fs.MountGameDirectories(roots); err != nil {
			return nil, err
		}
		return content.CompileFeatures(fs)
	}
	roots := append(append([]string(nil), featuresRoots...), root)
	if wrote {
		roots = append([]string{tmp}, roots...)
	}
	if defs, err := compile(roots); err == nil {
		return defs, nil
	}
	if defs, err := compile(append(append([]string(nil), featuresRoots...), root)); err == nil {
		return defs, nil
	}
	return map[string]*content.FeatureDef{}, nil
}

// annotatePicture draws the map's own data over the authored minimap:
// indestructible metal-bearing features as the extractor's deposits (the
// engine's own seed rule [05 R-FEAT-01 §7]) in yellow, and every other placed
// feature as reclaimable ground in green. Colours are matched back into the
// authored palette so the PNG stays indexed and small.
func annotatePicture(picture *image.Paletted, t *formats.TNT, defs map[string]*content.FeatureDef, colors color.Palette) {
	w, h := picture.Rect.Dx(), picture.Rect.Dy()
	cw, ch := int(t.Width), int(t.Height)
	if w <= 0 || h <= 0 || cw <= 0 || ch <= 0 || len(t.Attributes) < cw*ch {
		return
	}
	nearest := nearestPalette(colors)
	for y := 0; y < h; y++ {
		cz := y * ch / h
		for x := 0; x < w; x++ {
			cx := x * cw / w
			i := cz*cw + cx
			r, g, b, _ := colors[picture.ColorIndexAt(x, y)].RGBA()
			rr, gg, bb := uint8(r>>8), uint8(g>>8), uint8(b>>8)
			if f := t.Attributes[i].Feature; f < 0xfffb && int(f) < len(t.FeatureTable) {
				def := defs[content.CanonicalKey(t.FeatureTable[f].Name)]
				if def != nil && def.Metal != 0 && def.Indestructible {
					rr, gg, bb = 255, 220, 0
				} else {
					rr, gg, bb = blend(rr, gg, bb, 64, 200, 64, 0.5)
				}
			}
			picture.SetColorIndex(x, y, nearest(rr, gg, bb))
		}
	}
}

// blend mixes an overlay colour into the source at weight.
func blend(r, g, b, or_, og, ob uint8, weight float64) (uint8, uint8, uint8) {
	mix := func(s, o uint8) uint8 {
		return uint8(float64(s)*(1-weight) + float64(o)*weight + 0.5)
	}
	return mix(r, or_), mix(g, og), mix(b, ob)
}

// nearestPalette maps RGB colours to palette indices, caching each colour.
func nearestPalette(colors color.Palette) func(r, g, b uint8) uint8 {
	cache := map[uint32]uint8{}
	return func(r, g, b uint8) uint8 {
		key := uint32(r)<<16 | uint32(g)<<8 | uint32(b)
		if index, ok := cache[key]; ok {
			return index
		}
		best, distance := uint8(0), 1<<30
		for i, entry := range colors {
			er, eg, eb, _ := entry.RGBA()
			dr := int(r) - int(er>>8)
			dg := int(g) - int(eg>>8)
			db := int(b) - int(eb>>8)
			d := dr*dr + dg*dg + db*db
			if d < distance {
				best, distance = uint8(i), d
			}
		}
		cache[key] = best
		return best
	}
}

func minimap(t *formats.TNT, colors color.Palette) (*image.Paletted, error) {
	w, h := int64(t.MinimapWidth), int64(t.MinimapHeight)
	ew, eh := int64(t.Width)*16-32, int64(t.Height)*16-128
	if !t.MiniMapPresent || w <= 0 || h <= 0 || w > 1024 || h > 1024 || ew <= 0 || eh <= 0 {
		return nil, fmt.Errorf("missing or oversized authored minimap")
	}
	// Keep the source crop used by the front-end chooser [07 R-FE-01 §5].
	// This export omits padding and does not resample or invent terrain art.
	cw, ch := w, h
	if ew < eh {
		cw = ew * w / eh
	} else {
		ch = eh * h / ew
	}
	if cw <= 0 || ch <= 0 {
		return nil, fmt.Errorf("empty minimap crop")
	}
	picture := image.NewPaletted(image.Rect(0, 0, int(cw), int(ch)), colors)
	for y := int64(0); y < ch; y++ {
		copy(picture.Pix[y*cw:(y+1)*cw], t.Minimap[y*w:y*w+cw])
	}
	return picture, nil
}
