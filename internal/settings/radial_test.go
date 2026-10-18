package settings

import (
	"os"
	"path/filepath"
	"testing"
)

// The build radial is opt-in: omission is off, an explicit 1 survives a
// restart through the ordinary write-all transaction, and out-of-range
// values normalize to off rather than enabling it by accident
// (DESIGN_INTERFACE_HUD_INPUT §3.18).
func TestBuildRadialPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	for _, tc := range []struct {
		text string
		want int
	}{
		{`{"version":1,"presentation":{}}`, 0},
		{`{"version":1,"presentation":{"buildRadial":1}}`, 1},
		{`{"version":1,"presentation":{"buildRadial":0}}`, 0},
		{`{"version":1,"presentation":{"buildRadial":-3}}`, 0},
		{`{"version":1,"presentation":{"buildRadial":2}}`, 0},
	} {
		if err := os.WriteFile(path, []byte(tc.text), 0o600); err != nil {
			t.Fatal(err)
		}
		for pass := 0; pass < 2; pass++ {
			s, err := LoadFrom(path)
			if err != nil {
				t.Fatal(err)
			}
			if s.Presentation.BuildRadial != tc.want {
				t.Fatalf("%s pass %d: buildRadial = %d, want %d", tc.text, pass, s.Presentation.BuildRadial, tc.want)
			}
			if err := s.SaveTo(path); err != nil {
				t.Fatal(err)
			}
		}
	}
}
