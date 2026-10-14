package client

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	presentationrender "github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// previewFace and previewPiece author small fixture models for the composed
// preview tests. Corners are model units; a texture name selects the test
// client's one-texel frame.
type previewFace struct {
	corners [][3]float64
	color   uint8
	texture string
}

type previewPiece struct {
	name      string
	parent    int
	translate [3]float64
	faces     []previewFace
}

func previewFixed(v float64) numeric.Fixed { return numeric.Fixed(int64(v * 65536)) }

func previewModel(name string, pieces []previewPiece) *unitModel {
	m := &compiledmodel.Model{Pieces: make([]compiledmodel.Piece, len(pieces)), Name: name}
	byName := make(map[string]int, len(pieces))
	for i, p := range pieces {
		piece := &m.Pieces[i]
		piece.Name, piece.Parent = p.name, p.parent
		piece.Translate = [3]numeric.Fixed{previewFixed(p.translate[0]), previewFixed(p.translate[1]), previewFixed(p.translate[2])}
		if p.parent < 0 {
			m.Root = i
		} else {
			m.Pieces[p.parent].Children = append(m.Pieces[p.parent].Children, i)
		}
		for _, f := range p.faces {
			var indices []uint16
			for _, c := range f.corners {
				indices = append(indices, uint16(len(piece.Vertices)))
				piece.Vertices = append(piece.Vertices, [3]numeric.Fixed{previewFixed(c[0]), previewFixed(c[1]), previewFixed(c[2])})
			}
			pr := compiledmodel.Primitive{VertexIndices: indices, TextureName: f.texture}
			if f.texture == "" {
				pr.IsColored, pr.ColorIndex = 1, uint32(f.color)
			}
			piece.Primitives = append(piece.Primitives, pr)
		}
		byName[strings.ToLower(p.name)] = i
	}
	return &unitModel{compiled: m, pieceByName: byName}
}

// composeTestRenderer registers a two-piece "factory" with a textured pad
// and a flat door, plus a small "product" with a textured body and a flat
// fin, on an isolated preview renderer.
func composeTestRenderer(t *testing.T) *ModelPreviewRenderer {
	t.Helper()
	c := newTestClient(t)
	p := &palette.Tables{}
	c.SetPalette(p)
	c.texIndex = map[string]texRef{"tex": {kind: texStatic, key: "tex", frame: &formats.GAFFrame{Width: 2, Height: 2, Pixels: []byte{7, 9, 11, 13}}}}
	c.models["factory"] = previewModel("factory", []previewPiece{
		{name: "base", parent: -1, translate: [3]float64{1, .5, -2}, faces: []previewFace{
			{corners: [][3]float64{{-6, 0, -6}, {6, 0, -6}, {6, 0, 6}, {-6, 0, 6}}, color: 40},
			{corners: [][3]float64{{-6, 0, 6}, {6, 0, 6}, {6, 3, 6}, {-6, 3, 6}}, color: 44},
		}},
		{name: "pad", parent: 0, translate: [3]float64{1.25, 1, -.75}, faces: []previewFace{
			{corners: [][3]float64{{-3, 0, -3}, {3, 0, -3}, {3, 0, 3}, {-3, 0, 3}}, texture: "tex"},
		}},
		{name: "door", parent: 0, translate: [3]float64{-4, 1, 0}, faces: []previewFace{
			{corners: [][3]float64{{0, 0, 0}, {1.5, 2.25, 0}, {0, 2.25, .5}}, color: 90},
		}},
	})
	c.models["product"] = previewModel("product", []previewPiece{
		{name: "body", parent: -1, translate: [3]float64{0, .25, 0}, faces: []previewFace{
			{corners: [][3]float64{{-2, 0, -2}, {2, 0, -2}, {2, 2.5, -1}, {-2, 2.5, -1}}, texture: "tex"},
			{corners: [][3]float64{{-2, 2.5, -1}, {2, 2.5, -1}, {2, 2.5, 2}, {-2, 2.5, 2}}, color: 120},
		}},
		{name: "fin", parent: 0, translate: [3]float64{0, 2.5, 1}, faces: []previewFace{
			{corners: [][3]float64{{0, 0, 0}, {0, 1.75, -.5}, {0, 0, -1.5}}, color: 150},
		}},
	})
	return &ModelPreviewRenderer{client: c, palette: p}
}

// previewRecordDigest hashes the durable record contents a device consumer
// reads: the ordinary model commands and the precise viewer payload. A
// projected record has no classic planes, so each command is mirrored with
// its classic slot empty; the mirror encodes as the command itself does.
func previewRecordDigest(t *testing.T, record ModelPreviewRecord) string {
	t.Helper()
	type command struct {
		ShadowOnly      bool
		Classic         *struct{}
		Geometry        *drawlist.ModelGeometry
		ShadowOmissions int
	}
	var mirrored []command
	for _, c := range record.List.ModelCommands() {
		if c.Classic != nil {
			t.Fatal("projected record carries classic planes")
		}
		mirrored = append(mirrored, command{ShadowOnly: c.ShadowOnly, Geometry: c.Geometry, ShadowOmissions: c.ShadowOmissions})
	}
	commands, err := json.Marshal(mirrored)
	if err != nil {
		t.Fatal(err)
	}
	if record.Projected == nil {
		t.Fatal("projected record has no precise payload")
	}
	projected, err := json.Marshal(record.Projected.Faces)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(append(append(commands, 0), projected...))
	return hex.EncodeToString(sum[:])
}

func composeParentOptions() ModelPreviewOptions {
	return ModelPreviewOptions{
		Model: "factory", Owner: 3, Width: 160, Height: 120,
		Heading: 9000, Pitch: 3000, Bank: 500, Structure: true, KeyPlane: true,
		PiecePoses: []frame.PieceView{{Name: "pad", RotY: 4096, Ty: 16384}},
	}
}

func composeProjection() ModelPreviewProjection {
	return ModelPreviewProjection{PixelsPerUnit: 7.25, Pivot: [3]numeric.Fixed{16384, -98304, 40000}}
}

// TestModelPreviewProjectedRecordUnchangedWithoutAttachment locks the
// projected record of an isolated model byte for byte. The digest was taken
// before the attachment composition existed; a projected call without an
// attachment must keep producing it.
func TestModelPreviewProjectedRecordUnchangedWithoutAttachment(t *testing.T) {
	r := composeTestRenderer(t)
	record, err := r.RecordProjectedGeometry(composeParentOptions(), composeProjection())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := previewRecordDigest(t, record), "41664beca4dd8fcd754d78335a3b3ee0819ae548fd78a6559eabcadd3d7bd6ad"; got != want {
		t.Fatalf("isolated projected record digest = %s, want %s", got, want)
	}
}

func composeAttachment(t *testing.T, r *ModelPreviewRenderer, remaining float32) *ModelPreviewAttachment {
	t.Helper()
	opts := composeParentOptions()
	placement, err := r.PiecePlacement(opts.Model, opts.PiecePoses, "PAD")
	if err != nil {
		t.Fatal(err)
	}
	return &ModelPreviewAttachment{
		Model: "product", Placement: placement, KeyPlane: true,
		PiecePoses:     []frame.PieceView{{Name: "fin", RotZ: 3000}},
		BuildRemaining: remaining, NanoframeID: 77, NanoframeTick: 1234,
	}
}

func TestModelPreviewAttachmentOnlyOnProjectedEntry(t *testing.T) {
	r := composeTestRenderer(t)
	opts := composeParentOptions()
	opts.Attachment = composeAttachment(t, r, 0)
	if _, err := r.RecordModel(opts); err == nil {
		t.Fatal("ordinary classic preview accepted an attachment")
	}
	if _, err := r.RecordGeometry(opts); err == nil {
		t.Fatal("ordinary geometry preview accepted an attachment")
	}
	for _, bad := range []ModelPreviewAttachment{
		{Model: " "}, {Model: "product", BuildRemaining: -.25}, {Model: "product", BuildRemaining: 1.5},
		{Model: "product", BuildRemaining: float32(math.NaN())}, {Model: "missing"},
	} {
		opts.Attachment = &bad
		if _, err := r.RecordProjectedGeometry(opts, composeProjection()); err == nil {
			t.Fatalf("projected preview accepted attachment %+v", bad)
		}
	}
}

// At zero view orientation the composed attachment is exactly where the
// battle draws a product: the factory's pad origin plus the product's own
// composition at the pad's turns [04 R-FAC-02 §2][04 R-REV-02].
func TestModelPreviewAttachmentPlacesProductLikeBattle(t *testing.T) {
	r := composeTestRenderer(t)
	c := r.client
	for _, heading := range []uint16{0, 9000} {
		opts := composeParentOptions()
		opts.Heading, opts.Pitch, opts.Bank = heading, 0, 0
		opts.Attachment = composeAttachment(t, r, 0)
		record, err := r.RecordProjectedGeometry(opts, composeProjection())
		if err != nil {
			t.Fatal(err)
		}
		got := record.Projected.Attachment
		if got == nil || len(got.Faces) == 0 || got.Reveal != nil || got.Outline != nil {
			t.Fatalf("complete attachment payload = %+v", got)
		}
		if commands := record.List.ModelCommands(); len(commands) != 2 || commands[1].Geometry == nil {
			t.Fatal("attachment's ordinary packet does not follow the parent's")
		}
		// The battle reference: the product as its own unit at the pad's
		// world point, oriented by the factory heading plus the pad's turns.
		factory := c.models["factory"]
		states := slices.Clone(c.modelStates(factory, opts.PiecePoses))
		compiledmodel.FoldRootAngles(states, factory.compiled.Root, heading, 0, 0)
		pad := compiledmodel.Compose(factory.compiled, states, factory.pieceByName["pad"]).Origin
		_, x, z := previewCamera(opts)
		product := c.models["product"]
		a := opts.Attachment
		battle := presentationrender.BuildUnitDrawInto(product.compiled, c.modelStates(product, a.PiecePoses),
			heading+a.Placement.Heading, a.Placement.Pitch, a.Placement.Bank,
			frame.UnitView{X: x + pad[0], Y: pad[1], Z: z + pad[2], BMCode: true}, nil, &presentationrender.DrawScratch{})
		projector := modelPreviewProjector{projection: composeProjection()}
		world := [3]numeric.Fixed{x, 0, z}
		for _, face := range got.Faces {
			for _, position := range face.Positions {
				best := math.Inf(1)
				for _, piece := range battle.Pieces {
					for _, v := range piece.WorldVertices {
						p, _ := projector.project(v, world)
						p.X += float64(opts.Width / 2)
						p.Y += float64(opts.Height / 2)
						best = min(best, math.Hypot(p.X-position.X, p.Y-position.Y)+math.Abs(p.Depth-position.Depth))
					}
				}
				// Heading zero composes the same whole 16.16 sums; a turned
				// factory rounds the turn once rather than per piece.
				limit := 0.0
				if heading != 0 {
					limit = 1e-3
				}
				if best > limit {
					t.Fatalf("heading %d: attachment corner %+v is %g from the battle product", heading, position, best)
				}
			}
		}
	}
}

func TestModelPreviewPiecePlacement(t *testing.T) {
	r := composeTestRenderer(t)
	poses := composeParentOptions().PiecePoses
	root, err := r.PiecePlacement("factory", poses, "base")
	if err != nil || root != (ModelPreviewPlacement{}) {
		t.Fatalf("root placement = %+v, %v; want the root origin with no turn", root, err)
	}
	pad, err := r.PiecePlacement("factory", poses, "pad")
	if err != nil {
		t.Fatal(err)
	}
	// The pad hangs off the root: its root-local origin is its authored
	// translation plus its script offset, and its turn is its own.
	want := ModelPreviewPlacement{Position: [3]numeric.Fixed{previewFixed(1.25), previewFixed(1) + 16384, previewFixed(-.75)}, Heading: 4096}
	if pad != want {
		t.Fatalf("pad placement = %+v, want %+v", pad, want)
	}
	if _, err := r.PiecePlacement("factory", poses, "chimney"); err == nil {
		t.Fatal("placement on a missing piece succeeded")
	}
	if _, err := r.PiecePlacement("missing", nil, "pad"); err == nil {
		t.Fatal("placement on a missing model succeeded")
	}
}

// The reveal is the battle's: remaining 1 erases every key of a body this
// low, a vanishing fraction keeps them all, and zero is complete
// [03 R-P0-19-N].
func TestModelPreviewAttachmentRevealBoundaries(t *testing.T) {
	r := composeTestRenderer(t)
	band, outline := presentationrender.NanoframePulse(77, 1234)
	for _, tt := range []struct {
		remaining float32
		verdict   int16
	}{{1, presentationrender.NanoframeErase}, {1e-6, presentationrender.NanoframeKeep}} {
		opts := composeParentOptions()
		opts.Attachment = composeAttachment(t, r, tt.remaining)
		record, err := r.RecordProjectedGeometry(opts, composeProjection())
		if err != nil {
			t.Fatal(err)
		}
		a := record.Projected.Attachment
		want := presentationrender.BuildNanoframeReveal(tt.remaining, band, outline)
		if a == nil || a.Reveal == nil || *a.Reveal != (drawlist.ModelReveal{Line: want.Line, Floor: want.Floor, Below: want.Below, Band: want.Band, Above: want.Above}) {
			t.Fatalf("remaining %g reveal = %+v, want %+v", tt.remaining, a, want)
		}
		if len(a.Outline) == 0 {
			t.Fatalf("remaining %g has no outline", tt.remaining)
		}
		for _, px := range a.Outline {
			if px.Color != outline {
				t.Fatalf("outline colour %d, want pulse %d", px.Color, outline)
			}
		}
		for _, face := range a.Faces {
			for _, v := range face.Face.Vertices {
				if got := want.Verdict(uint8(v.Key)); got != tt.verdict {
					t.Fatalf("remaining %g key %d verdict %d, want %d", tt.remaining, v.Key, got, tt.verdict)
				}
			}
		}
	}
}

// Attachment keys are the product's own heights above its origin plus the
// bias, independent of the view orientation and the parent's frame
// [03 R-COMP-01 §3].
func TestModelPreviewAttachmentKeysAreProductLocal(t *testing.T) {
	r := composeTestRenderer(t)
	keys := func(heading, pitch uint16) []int32 {
		opts := composeParentOptions()
		opts.Heading, opts.Pitch = heading, pitch
		opts.Attachment = composeAttachment(t, r, .5)
		record, err := r.RecordProjectedGeometry(opts, composeProjection())
		if err != nil {
			t.Fatal(err)
		}
		var out []int32
		for _, face := range record.Projected.Attachment.Faces {
			for _, v := range face.Face.Vertices {
				out = append(out, v.Key)
			}
		}
		return out
	}
	a, b := keys(0, 0), keys(20000, 9000)
	if !slices.Equal(a, b) || len(a) == 0 {
		t.Fatalf("view orientation changed attachment keys: %v vs %v", a, b)
	}
	// Body base corners sit at the product root's 0.25 offset: whole height 0.
	if !slices.Contains(a, presentationrender.NanoframeHeightBias) || !slices.Contains(a, presentationrender.NanoframeHeightBias+2) {
		t.Fatalf("attachment keys %v lack the product-local base and top", a)
	}
}

// A piece origin projects exactly where the record draws a corner authored
// at that origin, for the parent and the attachment alike.
func TestModelPreviewPiecePointsMatchDrawnVertices(t *testing.T) {
	r := composeTestRenderer(t)
	opts := composeParentOptions()
	opts.Attachment = composeAttachment(t, r, .4)
	record, err := r.RecordProjectedGeometry(opts, composeProjection())
	if err != nil {
		t.Fatal(err)
	}
	points, err := r.ProjectedPieces(opts, composeProjection(), []ModelPreviewPiece{{Name: "door"}, {Attachment: true, Name: "FIN"}})
	if err != nil {
		t.Fatal(err)
	}
	// door's only face and fin's only face each start at their piece origin.
	first := func(faces []drawlist.ModelPreviewFace, color uint8) drawlist.ModelPreviewPosition {
		for _, f := range faces {
			if f.Face.Color == color && f.Face.Texture == nil && len(f.Positions) == 3 {
				return f.Positions[0]
			}
		}
		t.Fatalf("no triangle of colour %d", color)
		return drawlist.ModelPreviewPosition{}
	}
	if got := first(record.Projected.Faces, 90); got != points[0] {
		t.Fatalf("door origin %+v, drawn corner %+v", points[0], got)
	}
	if got := first(record.Projected.Attachment.Faces, 150); got != points[1] {
		t.Fatalf("fin origin %+v, drawn corner %+v", points[1], got)
	}
	if _, err := r.ProjectedPieces(composeParentOptions(), composeProjection(), []ModelPreviewPiece{{Attachment: true, Name: "fin"}}); err == nil {
		t.Fatal("attachment piece projected without an attachment")
	}
}

// The outline is the battle's row-extreme rule sampled at pixel centres
// (viewer policy, DESIGN_GPU_RENDERER §22.5): the first covered column and
// the first uncovered one on each covered row, and nothing for a back face.
func TestModelPreviewOutlineRowExtremes(t *testing.T) {
	xs, ys := []float64{2.2, 8.6, 8.6, 2.2}, []float64{3.4, 3.4, 7.9, 7.9}
	ds := []float64{1.1, 4.3, 4.3, 1.1}
	got := appendPreviewOutlineRing(nil, xs, ys, ds, 0xa3)
	var cells [][2]int32
	for _, px := range got {
		cells = append(cells, [2]int32{px.X, px.Y})
		if px.Color != 0xa3 {
			t.Fatal("outline lost its colour")
		}
		// Depth runs 0.5 per column on this face, clamped to its span.
		for k, dx := range []float64{0, 1, 1, 0} {
			want := min(max(1.1+0.5*(float64(px.X)+dx-2.2), 1.1), 4.3)
			if math.Abs(px.Depth[k]-want) > 1e-12 {
				t.Fatalf("pixel %v corner %d depth %g, want %g", cells[len(cells)-1], k, px.Depth[k], want)
			}
		}
	}
	var want [][2]int32
	for y := int32(3); y <= 7; y++ {
		want = append(want, [2]int32{2, y}, [2]int32{9, y})
	}
	if !slices.Equal(cells, want) {
		t.Fatalf("outline pixels %v, want %v", cells, want)
	}
	slices.Reverse(xs)
	slices.Reverse(ys)
	if back := appendPreviewOutlineRing(nil, xs, ys, ds, 0xa3); len(back) != 0 {
		t.Fatalf("back-facing ring drew %d outline pixels", len(back))
	}
}
