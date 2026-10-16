package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/version"
)

// fixtureFile is one authored file of a test source tree.
type fixtureFile struct {
	path string
	mode fs.FileMode // 0o644, 0o755, or fs.ModeSymlink with data the target
	data string
}

// fixtureTree is a small module: paths whose byte order differs from a
// directory walk's ("a-b/x" sorts before "a/x"), an executable script, a
// symbolic link and an empty file.
func fixtureTree() []fixtureFile {
	return []fixtureFile{
		{"go.mod", 0o644, "module example.com/fixture\n\ngo 1.27.1\n\nrequire golang.org/x/sys v0.48.0\n"},
		{"go.sum", 0o644, "golang.org/x/sys v0.48.0 h1:sys48=\ngolang.org/x/sys v0.48.0/go.mod h1:sysmod=\nexample.com/a v1.0.0 h1:a=\n"},
		{"a/x.go", 0o644, "package a\n"},
		{"a-b/x.go", 0o644, "package ab\n"},
		{"tools/check", 0o755, "#!/bin/sh\n"},
		{"CLAUDE.md", fs.ModeSymlink, "AGENTS.md"},
		{"AGENTS.md", 0o644, "agents\n"},
		{"internal/version/empty.txt", 0o644, ""},
	}
}

func writeDir(t *testing.T, root string, files []fixtureFile) {
	t.Helper()
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f.path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if f.mode&fs.ModeSymlink != 0 {
			if err := os.Symlink(f.data, p); err != nil {
				t.Skipf("symbolic links unavailable: %v", err)
			}
			continue
		}
		if err := os.WriteFile(p, []byte(f.data), f.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, f.mode); err != nil {
			t.Fatal(err)
		}
	}
}

// writeTarGz writes the files as git archive would: a global header, the
// prefix directory, directories, regular files with 0664 or 0775, links.
func writeTarGz(t *testing.T, target, prefix string, files []fixtureFile) {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(tw.WriteHeader(&tar.Header{Typeflag: tar.TypeXGlobalHeader, Name: "pax_global_header", PAXRecords: map[string]string{"comment": "0123456789abcdef"}}))
	must(tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: prefix, Mode: 0o775}))
	for _, f := range files {
		name := prefix + f.path
		if f.mode&fs.ModeSymlink != 0 {
			must(tw.WriteHeader(&tar.Header{Typeflag: tar.TypeSymlink, Name: name, Linkname: f.data, Mode: 0o777}))
			continue
		}
		mode := int64(0o664)
		if f.mode&0o111 != 0 {
			mode = 0o775
		}
		must(tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: mode, Size: int64(len(f.data))}))
		_, err := tw.Write([]byte(f.data))
		must(err)
	}
	must(tw.Close())
	must(gz.Close())
	must(os.WriteFile(target, b.Bytes(), 0o644))
}

// writeZip writes the files as git archive does in zip form: Unix modes in
// the external attributes, a link's target as its content.
func writeZip(t *testing.T, target, prefix string, files []fixtureFile) {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	dir := &zip.FileHeader{Name: prefix}
	dir.SetMode(fs.ModeDir | 0o775)
	_, err := zw.CreateHeader(dir)
	must(err)
	for _, f := range files {
		h := &zip.FileHeader{Name: prefix + f.path, Method: zip.Deflate}
		switch {
		case f.mode&fs.ModeSymlink != 0:
			h.SetMode(fs.ModeSymlink | 0o777)
		case f.mode&0o111 != 0:
			h.SetMode(0o755)
		default:
			h.SetMode(0o644)
		}
		w, err := zw.CreateHeader(h)
		must(err)
		_, err = w.Write([]byte(f.data))
		must(err)
	}
	must(zw.Close())
	must(os.WriteFile(target, b.Bytes(), 0o644))
}

func digestOf(t *testing.T, source, prefix string) [32]byte {
	t.Helper()
	entries, err := readInventory(source, prefix)
	if err != nil {
		t.Fatal(err)
	}
	return inventoryDigest(entries)
}

// One source has one inventory whichever form carries it: an extracted
// directory, the tar.gz a Unix installer verifies and the zip a Windows
// installer verifies.
func TestOneSourceOneInventory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a Windows directory keeps neither executable bits nor links")
	}
	tmp := t.TempDir()
	files := fixtureTree()
	root := filepath.Join(tmp, "dir")
	writeDir(t, root, files)
	writeTarGz(t, filepath.Join(tmp, "src.tar.gz"), "fixture-rev/", files)
	writeZip(t, filepath.Join(tmp, "src.zip"), "fixture-rev/", files)
	d := digestOf(t, root, "")
	if digestOf(t, filepath.Join(tmp, "src.tar.gz"), "fixture-rev/") != d {
		t.Fatal("the tar.gz inventory differs from the directory's")
	}
	if digestOf(t, filepath.Join(tmp, "src.zip"), "fixture-rev/") != d {
		t.Fatal("the zip inventory differs from the directory's")
	}
	entries, _ := readInventory(root, "")
	var paths []string
	for _, e := range entries {
		paths = append(paths, e.path)
	}
	if got := strings.Join(paths, " "); got != "AGENTS.md CLAUDE.md a-b/x.go a/x.go go.mod go.sum internal/version/empty.txt tools/check" {
		t.Fatalf("inventory order %q", got)
	}
	// The stamp is the one file the inventory leaves out; every other file,
	// whatever it is, changes it.
	writeDir(t, root, []fixtureFile{{version.StampFile, 0o644, "package version\n"}})
	if digestOf(t, root, "") != d {
		t.Fatal("the generated stamp entered the inventory")
	}
	for _, c := range []struct {
		name string
		edit func(dir string) error
	}{
		{"an untracked file", func(dir string) error { return os.WriteFile(filepath.Join(dir, "notes.txt"), nil, 0o644) }},
		{"a changed byte", func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "a", "x.go"), []byte("package b\n"), 0o644)
		}},
		{"an executable bit", func(dir string) error { return os.Chmod(filepath.Join(dir, "a", "x.go"), 0o755) }},
		{"a link target", func(dir string) error {
			if err := os.Remove(filepath.Join(dir, "CLAUDE.md")); err != nil {
				return err
			}
			return os.Symlink("README.md", filepath.Join(dir, "CLAUDE.md"))
		}},
		{"a link made a file", func(dir string) error {
			if err := os.Remove(filepath.Join(dir, "CLAUDE.md")); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("AGENTS.md"), 0o644)
		}},
	} {
		dir := filepath.Join(tmp, strings.ReplaceAll(c.name, " ", "-"))
		writeDir(t, dir, files)
		if err := c.edit(dir); err != nil {
			t.Fatal(err)
		}
		if digestOf(t, dir, "") == d {
			t.Errorf("%s: same inventory", c.name)
		}
	}
}

// What the inventory refuses rather than filters: version-control
// metadata, entries outside the archive prefix, special entries and a path
// listed twice.
func TestInventoryRefusals(t *testing.T) {
	tmp := t.TempDir()
	files := fixtureTree()
	checkout := filepath.Join(tmp, "checkout")
	writeDir(t, checkout, append(files, fixtureFile{".git/HEAD", 0o644, "ref: refs/heads/main\n"}))
	if _, err := readInventory(checkout, ""); err == nil {
		t.Error("a checkout with .git was inventoried")
	}
	archive := filepath.Join(tmp, "src.tar.gz")
	writeTarGz(t, archive, "fixture-rev/", files)
	if _, err := readInventory(archive, "other-rev/"); err == nil {
		t.Error("an archive was read under the wrong prefix")
	}
	if _, err := readInventory(archive, ""); err == nil {
		t.Error("an archive was read without its prefix")
	}
	twice := filepath.Join(tmp, "twice.zip")
	writeZip(t, twice, "fixture-rev/", append(files, fixtureFile{"go.mod", 0o644, "module other\n"}))
	if _, err := readInventory(twice, "fixture-rev/"); err == nil {
		t.Error("a path listed twice was inventoried")
	}
	// A hard link is not a file the inventory can name.
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeLink, Name: "fixture-rev/go.sum", Linkname: "fixture-rev/go.mod"})
	_ = tw.Close()
	_ = gz.Close()
	hard := filepath.Join(tmp, "hard.tar.gz")
	if err := os.WriteFile(hard, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readInventory(hard, "fixture-rev/"); err == nil {
		t.Error("a hard link was inventoried")
	}
	for _, p := range []string{"", "/abs", "a//b", "a/./b", "a/../b", "\xff", "a\x00b"} {
		if checkPath(p) == nil {
			t.Errorf("path %q admitted", p)
		}
	}
}

// The generator writes a stamp that decodes to the manifest it describes,
// prints that manifest's digest, writes the same stamp again for the same
// inputs, and refuses what a stamp cannot describe.
func TestStampGeneration(t *testing.T) {
	tmp := t.TempDir()
	files := fixtureTree()
	root := filepath.Join(tmp, "src")
	writeDir(t, root, files)
	archive := filepath.Join(tmp, "src.tar.gz")
	writeTarGz(t, archive, "fixture-rev/", files)
	sum := strings.Repeat("ab", 32)
	args := []string{"-source", archive, "-prefix", "fixture-rev/", "-write", root, "-cgo", "0",
		"-variant", "linux/amd64/v1=" + sum, "-variant", "darwin/arm64/v8.0=" + sum,
		"-arg", "-mod=readonly", "-arg=-trimpath", "-arg", "-buildvcs=false", "-arg=-ldflags=-s -w", "-arg", "./cmd/fixture"}
	digest, err := run(args, "go1.27.1")
	if err != nil {
		t.Fatal(err)
	}
	stampPath := filepath.Join(root, filepath.FromSlash(version.StampFile))
	source, err := os.ReadFile(stampPath)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeStampFile(t, source)
	if d, err := m.Digest(); err != nil || strings.ToLower(digest) != hexDigest(d) {
		t.Fatalf("printed digest %s, stamp digest %x, %v", digest, d, err)
	}
	if m.GoVersion != "go1.27.1" || m.CGOEnabled || len(m.Variants) != 2 || m.Variants[0].GOOS != "darwin" ||
		strings.Join(m.BuildArgs, " ") != "-mod=readonly -trimpath -buildvcs=false -ldflags=-s -w ./cmd/fixture" {
		t.Fatalf("stamped manifest %+v", m)
	}
	if len(m.Modules) != 2 || m.Modules[0].Path != "example.com/a" || m.Modules[1].Sum != "h1:sys48=" {
		t.Fatalf("stamped modules %+v", m.Modules)
	}
	// A Windows directory keeps no executable bits, so its inventory cannot
	// match the archive's there; the rest of the contract still holds.
	if runtime.GOOS != "windows" {
		entries, _ := readInventory(root, "")
		if m.SourceTree != inventoryDigest(entries) {
			t.Fatal("the stamp's source tree is not the inventory's, stamp excluded")
		}
	}
	// The same inputs stamp the same bytes; the stamp now present under
	// -write changes nothing.
	again, err := run(args, "go1.27.1")
	if err != nil || again != digest {
		t.Fatalf("a second run printed %s, %v", again, err)
	}
	if second, _ := os.ReadFile(stampPath); !bytes.Equal(second, source) {
		t.Fatal("a second run wrote another stamp")
	}
	for _, c := range []struct {
		name string
		args []string
	}{
		{"no cgo", []string{"-source", root, "-write", root, "-variant", "linux/amd64/v1=" + sum}},
		{"no variant", []string{"-source", root, "-write", root, "-cgo", "0"}},
		{"bad variant", []string{"-source", root, "-write", root, "-cgo", "0", "-variant", "linux/amd64=" + sum}},
		{"short toolchain digest", []string{"-source", root, "-write", root, "-cgo", "0", "-variant", "linux/amd64/v1=abcd"}},
		{"output path", []string{"-source", root, "-write", root, "-cgo", "0", "-variant", "linux/amd64/v1=" + sum, "-arg", "-o", "-arg", "x"}},
		{"tags as an argument", []string{"-source", root, "-write", root, "-cgo", "0", "-variant", "linux/amd64/v1=" + sum, "-arg=-tags=retail"}},
		{"a trailing list", []string{"-source", root, "-write", root, "-cgo", "0", "-variant", "linux/amd64/v1=" + sum, "--", "-trimpath"}},
		{"prefix for a directory", []string{"-source", root, "-prefix", "x/", "-write", root, "-cgo", "0", "-variant", "linux/amd64/v1=" + sum}},
	} {
		if _, err := run(c.args, "go1.27.1"); err == nil {
			t.Errorf("%s: stamped", c.name)
		}
	}
	replaced := filepath.Join(tmp, "replaced")
	writeDir(t, replaced, append(files[1:], fixtureFile{"go.mod", 0o644, "module x\n\nreplace golang.org/x/sys => ../sys\n"}))
	if _, err := run([]string{"-source", replaced, "-write", replaced, "-cgo", "0", "-variant", "linux/amd64/v1=" + sum}, "go1.27.1"); err == nil {
		t.Error("a module with a replace directive was stamped")
	}
}

// decodeStampFile reads the generated file's one string literal.
func decodeStampFile(t *testing.T, source []byte) version.BuildManifest {
	t.Helper()
	text := string(source)
	if !strings.HasPrefix(text, "// Code generated by internal/version/stampgen. DO NOT EDIT.\n") {
		t.Fatal("the stamp is not marked generated")
	}
	start := strings.Index(text, "stamp = ")
	end := strings.LastIndex(text, " }")
	payload, err := strconv.Unquote(text[start+len("stamp = ") : end])
	if err != nil {
		t.Fatal(err)
	}
	m, err := version.DecodeBuildManifest([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func hexDigest(d [32]byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 64)
	for _, b := range d {
		out = append(out, digits[b>>4], digits[b&15])
	}
	return string(out)
}

// The module's own go.sum is in the form the generator reads, and the
// module has no replace directive.
func TestTheModuleCanBeStamped(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	goSum, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	modules, err := modulesFromSum(goSum)
	if err != nil || len(modules) == 0 {
		t.Fatalf("go.sum: %d modules, %v", len(modules), err)
	}
	goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if err := refuseReplacements(goMod); err != nil {
		t.Fatal(err)
	}
}
