package formats

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

func decodedGAFFile(t *testing.T, data []byte) *vfs.FS {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sprites.gaf"), data, 0600); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fs.Close() })
	return fs
}

// The decoded-only product keeps the same authored dispatch, child order,
// aliases and pixels [fmt gaf]. Its only omitted storage is the encoded RLE
// view that feature sprites never read (DESIGN_PRESENTATION_CLIENT §1).
func TestGAFDecodedFilePreservesImagesAndAliasesWithoutDirectCopies(t *testing.T) {
	data, err := EncodeGAF([]GAFWriteEntry{
		{Name: "tree", Frames: []GAFWriteFrame{{Width: 4, Height: 2, XOffset: -2, Duration: 3, Subframes: []GAFWriteFrame{
			{Width: 2, Height: 1, Pixels: []byte{7, 8}},
			{Width: 3, Height: 1, XOffset: 1, Pixels: []byte{9, 5, 6}, Transparent: []bool{false, true, false}, AlternateBlitter: 1},
		}}}},
		{Name: "alias", Frames: []GAFWriteFrame{{Width: 1, Height: 1, Pixels: []byte{4}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// A repeated entry pointer shares its reference table and decoded frames.
	binary.LittleEndian.PutUint32(data[16:20], binary.LittleEndian.Uint32(data[12:16]))
	fs := decodedGAFFile(t, data)
	full, err := LoadGAFFile(fs, "sprites.gaf")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := LoadGAFDecodedFile(fs, "sprites.gaf")
	if err != nil {
		t.Fatal(err)
	}
	if &decoded.Entries[0].Frames[0] != &decoded.Entries[1].Frames[0] {
		t.Fatal("decoded-only bank lost the shared reference table")
	}
	want, got := full.Entries[0].Frames[0].Frame, decoded.Entries[0].Frames[0].Frame
	if GAFFrameBytes(got) >= GAFFrameBytes(want) {
		t.Fatal("decoded-only bank retained the extra RLE storage")
	}
	var removeDirectViews func(*GAFFrame, *GAFFrame)
	removeDirectViews = func(want, got *GAFFrame) {
		if want.Compressed != 0 && len(want.Subframes) == 0 {
			if _, ok := want.DirectRaster(); !ok {
				t.Fatal("ordinary loader lost its encoded RLE view")
			}
			if _, ok := got.DirectRaster(); ok {
				t.Fatal("decoded-only loader retained an encoded RLE view")
			}
		} else if len(got.Subframes) == 0 {
			if raster, ok := got.DirectRaster(); !ok || raster != got {
				t.Fatal("raw leaf stopped sharing its decoded raster")
			}
		}
		want.directRaster = nil
		for i := range want.Subframes {
			removeDirectViews(want.Subframes[i], got.Subframes[i])
		}
	}
	removeDirectViews(want, got)
	if !reflect.DeepEqual(full, decoded) || !reflect.DeepEqual(want.Doubled(), got.Doubled()) {
		t.Fatal("decoded-only loading changed the bank or its detail image")
	}
}

func TestGAFDecodedFileKeepsWholeBankValidation(t *testing.T) {
	data, err := EncodeGAF([]GAFWriteEntry{
		{Name: "rest", Frames: []GAFWriteFrame{{Width: 1, Height: 1, Pixels: []byte{7}}}},
		{Name: "event", Frames: []GAFWriteFrame{{Width: 1, Height: 1, Pixels: []byte{8}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	fs := decodedGAFFile(t, data[:len(data)-1])
	_, fullErr := LoadGAFFile(fs, "sprites.gaf")
	_, decodedErr := LoadGAFDecodedFile(fs, "sprites.gaf")
	if fullErr == nil || decodedErr == nil || fullErr.Error() != decodedErr.Error() {
		t.Fatalf("corrupt bank errors: ordinary=%v decoded-only=%v", fullErr, decodedErr)
	}
}
