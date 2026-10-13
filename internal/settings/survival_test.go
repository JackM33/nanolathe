package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSurvivalBestsPersistOnlyImprovementsAndKeepOtherSetups(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvPath, filepath.Join(dir, "settings.json"))
	if _, ok, err := LoadSurvivalBest("a"); ok || err != nil {
		t.Fatalf("absent record: %v, %v", ok, err)
	}
	first := SurvivalBest{Score: 100, Waves: 2, TicksAlive: 900}
	if got, improved, err := StoreSurvivalBest("a", first); got != first || !improved || err != nil {
		t.Fatalf("first: %+v, %v, %v", got, improved, err)
	}
	for _, score := range []int64{50, 100} {
		if got, improved, err := StoreSurvivalBest("a", SurvivalBest{Score: score, Waves: 9}); got != first || improved || err != nil {
			t.Fatalf("worse/tie: %+v, %v, %v", got, improved, err)
		}
	}
	if _, _, err := StoreSurvivalBest("b", SurvivalBest{Score: 20}); err != nil {
		t.Fatal(err)
	}
	better := SurvivalBest{Score: 101, Waves: 3, TicksAlive: 1000}
	if got, improved, err := StoreSurvivalBest("a", better); got != better || !improved || err != nil {
		t.Fatalf("better: %+v, %v, %v", got, improved, err)
	}
	// A settings save must not clobber the separate records book.
	if err := Defaults().Save(); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]SurvivalBest{"a": better, "b": {Score: 20}} {
		if got, ok, err := LoadSurvivalBest(key); got != want || !ok || err != nil {
			t.Fatalf("reload %s: %+v, %v, %v", key, got, ok, err)
		}
	}
	if files, err := filepath.Glob(filepath.Join(dir, ".survival-bests-*")); err != nil || len(files) != 0 {
		t.Fatalf("temporary files left: %v, %v", files, err)
	}
}

func TestSurvivalBestsPreserveUnreadableOrFutureRecords(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvPath, filepath.Join(dir, "settings.json"))
	path := filepath.Join(dir, "survival-bests.json")
	for _, original := range []string{`{`, `null`, `{}`, `{"version":1}`, `{"bests":{}}`, `{"version":2,"bests":{}}`, `{"version":1,"bests":null}`} {
		if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LoadSurvivalBest("a"); err == nil {
			t.Fatal("invalid book read successfully")
		}
		if _, _, err := StoreSurvivalBest("a", SurvivalBest{Score: 10}); err == nil {
			t.Fatal("invalid book replaced")
		}
		if data, err := os.ReadFile(path); err != nil || string(data) != original {
			t.Fatalf("original lost: %q, %v", data, err)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := StoreSurvivalBest("a", SurvivalBest{Score: 10}); err == nil {
		t.Fatal("directory accepted as book")
	}
}
