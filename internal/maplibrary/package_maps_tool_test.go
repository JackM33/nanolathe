package maplibrary

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
)

// tools/package-maps must accept exactly the package ids the engine accepts,
// so a recipe can never publish a catalogue the client refuses as a whole.
func TestPackageMapsToolUsesEngineIDRule(t *testing.T) {
	python := python3(t)
	tool, err := filepath.Abs(filepath.Join("..", "..", "tools", "package-maps"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"good-id", "-leading-hyphen", "under_score", "dotted.id", "Upper", strings.Repeat("a", 64), strings.Repeat("a", 65)} {
		t.Run(id, func(t *testing.T) {
			parsed, _, err := modlibrary.ParseSelector(id + "@1")
			engine := err == nil && parsed == id
			root, out := t.TempDir(), t.TempDir()
			writeFile(t, filepath.Join(root, id), "features/authored.tdf", []byte("[tree]{}"))
			recipe, _ := json.Marshal(map[string]any{
				"schema": 1, "release_base": "https://example.invalid/maps/", "maps": []any{},
				"dependencies": []any{map[string]string{"id": id, "name": "Fixture", "version": "1", "summary": "", "homepage": ""}},
			})
			recipePath := filepath.Join(t.TempDir(), "recipe.json")
			if err := os.WriteFile(recipePath, recipe, 0o644); err != nil {
				t.Fatal(err)
			}
			output, err := exec.Command(python, tool, "--root", root, "--recipe", recipePath, "--out", out).CombinedOutput()
			if tool := err == nil; tool != engine {
				t.Fatalf("package-maps accepted=%v, engine accepted=%v: %s", tool, engine, output)
			}
		})
	}
}

// python3 is the first of python3 and python on PATH that runs Python 3.
// Windows can put a python3 alias on PATH that only offers to install Python,
// so a name that resolves is not yet an interpreter.
func python3(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"python3", "python"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		if exec.Command(path, "-c", "import sys; sys.exit(sys.version_info[0] != 3)").Run() == nil {
			return path
		}
	}
	t.Skip("Python 3 is not installed")
	return ""
}
