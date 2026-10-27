package install

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The files an upstream TA: Escalation installer copies into a base install,
// and nothing of the base game's own (docs/ESCALATION_SUPPORT.md).
func TestUpstreamEscalationFiles(t *testing.T) {
	for name, want := range map[string]bool{
		"TAESC.gp3": true, "taesc.gp3": true, "TXESC.ufo": true, "T2ESC.ufo": true, "T4ESC1.ufo": true, "t5esc.UFO": true,
		"totala1.hpi": false, "rev31.gp3": false, "ccdata.ccx": false, "TADEMO.ufo": false, "taesc.ufo": false, "T2ESC.hpi": false,
	} {
		if got := UpstreamEscalationArchive(name); got != want {
			t.Errorf("archive %q: %v, want %v", name, got, want)
		}
	}
	for name, want := range map[string]bool{"unitsE": true, "GUIE": true, "aE": true, "units": false, "guis": false, "maps": false} {
		if got := UpstreamEscalationFolder(name); got != want {
			t.Errorf("folder %q: %v, want %v", name, got, want)
		}
	}
	dir := t.TempDir()
	for _, f := range []string{"totala1.hpi", "TAESC.gp3", "T3ESC.ufo"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{"unitsE", "maps"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0700); err != nil {
			t.Fatal(err)
		}
	}
	got := UpstreamEscalationFiles(dir)
	slices.Sort(got)
	if !slices.Equal(got, []string{"T3ESC.ufo", "TAESC.gp3", "unitsE"}) {
		t.Fatalf("found %v", got)
	}
}
