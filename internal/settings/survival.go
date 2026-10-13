package settings

// Local Survival records are host data, independent of the settings snapshot
// rewritten by menu preferences (docs/DESIGN_SURVIVAL.md §8.1).

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// SurvivalBest is the outcome of the highest-scoring completed attempt for
// one comparable setup. Equal scores retain the earlier record.
type SurvivalBest struct {
	Score      int64  `json:"score"`
	Waves      int32  `json:"waves"`
	TicksAlive uint32 `json:"ticks_alive"`
}

type survivalBests struct {
	Version int                     `json:"version"`
	Bests   map[string]SurvivalBest `json:"bests"`
}

func survivalBestsPath() (string, error) {
	path, err := Path()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(path), "survival-bests.json"), nil
}

func survivalBestsError(path string, err error) error {
	return fmt.Errorf("nanolathe: Survival records unavailable: logical path %s, providers searched [host settings directory], expected readable version-1 records and an atomic writable destination: %w", path, err)
}

func loadSurvivalBests(path string) (survivalBests, error) {
	book := survivalBests{Version: 1, Bests: make(map[string]SurvivalBest)}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return book, nil
	}
	if err == nil {
		book = survivalBests{}
		err = json.Unmarshal(data, &book)
	}
	if err == nil && (book.Version != 1 || book.Bests == nil) {
		err = errors.New("unsupported or incomplete records")
	}
	if err != nil {
		return survivalBests{}, survivalBestsError(path, err)
	}
	return book, nil
}

// LoadSurvivalBest reads the record without creating a file. Missing records
// are normal; malformed or future files are reported and never replaced.
func LoadSurvivalBest(key string) (SurvivalBest, bool, error) {
	path, err := survivalBestsPath()
	if err != nil {
		return SurvivalBest{}, false, err
	}
	book, err := loadSurvivalBests(path)
	best, ok := book.Bests[key]
	return best, ok, err
}

// StoreSurvivalBest rereads the book at completion, preserves all other setups,
// and atomically replaces it only when this score improves on the stored one.
func StoreSurvivalBest(key string, result SurvivalBest) (best SurvivalBest, improved bool, err error) {
	path, err := survivalBestsPath()
	if err != nil {
		return best, false, err
	}
	book, err := loadSurvivalBests(path)
	if err != nil {
		return best, false, err
	}
	if best, ok := book.Bests[key]; ok && best.Score >= result.Score {
		return best, false, nil
	}
	book.Bests[key] = result
	data, err := json.MarshalIndent(book, "", "  ")
	if err != nil {
		return best, false, survivalBestsError(path, err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return best, false, survivalBestsError(path, err)
	}
	f, err := os.CreateTemp(dir, ".survival-bests-*.json")
	if err != nil {
		return best, false, survivalBestsError(path, err)
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(append(data, '\n'))
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return best, false, survivalBestsError(path, err)
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return best, false, survivalBestsError(path, err)
	}
	return result, true, nil
}
