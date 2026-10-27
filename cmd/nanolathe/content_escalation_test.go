package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

// A base install with TA: Escalation copied over it mounts as the plain game:
// Escalation's archives and renamed folders are left out, the main menu says
// so, and the screens that mount the base install again leave them out too.
// A manual root stack mounts exactly what it names
// (docs/ESCALATION_SUPPORT.md, "Unconfigured upstream installations").
func TestBaseInstallLeavesOutCopiedEscalation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	base := t.TempDir()
	write := func(rel string, data []byte) {
		t.Helper()
		path := filepath.Join(base, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	archive := func(name string, files map[string]string) {
		t.Helper()
		var list []vfs.ArchiveFile
		for path, data := range files {
			list = append(list, vfs.ArchiveFile{Path: path, Data: []byte(data)})
		}
		var buf bytes.Buffer
		if err := vfs.WriteArchive(&buf, list, vfs.ArchiveWriteOptions{}); err != nil {
			t.Fatal(err)
		}
		write(name, buf.Bytes())
	}
	archive("totala1.hpi", map[string]string{"gamedata/moveinfo.tdf": "base", "gamedata/sidedata.tdf": "base", "anims/armlab_gadget.gaf": "base"})
	archive("TAESC.gp3", map[string]string{"anims/armlab_gadget.gaf": "escalation"})
	write("unitsE/armcom.fbi", []byte("escalation"))

	cs, err := mountContent(Options{Roots: []string{base}}, []string{base}, modSelection{})
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	if data, err := cs.unmappedMount.ReadFile("anims/armlab_gadget.gaf"); err != nil || string(data) != "base" {
		t.Fatalf("the base art: %q %v", data, err)
	}
	if _, err := cs.fs.Stat("unitse/armcom.fbi"); err == nil {
		t.Fatal("a renamed Escalation folder mounted")
	}
	if !strings.Contains(cs.baseNotice, "Escalation") {
		t.Fatalf("notice %q", cs.baseNotice)
	}
	again := vfs.New()
	defer again.Close()
	if err := again.MountGameRoots(cs.baseGameRoots()); err != nil {
		t.Fatal(err)
	}
	if data, _ := again.ReadFile("anims/armlab_gadget.gaf"); string(data) != "base" {
		t.Fatalf("a screen's own base mount read %q", data)
	}

	// Two explicit roots are a manual stack: mounted as named.
	other := t.TempDir()
	manual, err := mountContent(Options{Roots: []string{other, base}}, []string{other, base}, modSelection{manual: true})
	if err != nil {
		t.Fatal(err)
	}
	defer manual.Close()
	if data, _ := manual.unmappedMount.ReadFile("anims/armlab_gadget.gaf"); string(data) != "escalation" || manual.baseNotice != "" {
		t.Fatalf("a manual stack read %q, notice %q", data, manual.baseNotice)
	}
}
