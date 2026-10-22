package main

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/mapgen"
	"github.com/nanolathe-gg/nanolathe/internal/maplibrary"
)

// The generator panel's background work (docs/DESIGN_MAP_GENERATOR.md §10):
// listing the content's worlds, indexing the chosen world's tiles once, and
// generating the map the panel's settings name. A newer request cancels the
// older one, and an older result can never replace a newer request's.

// mapgenMinMaps is how many archive maps a world needs to be offered: fewer
// give the painter too little art to choose from.
const mapgenMinMaps = 3

// mapgenResult is a finished map and what it was made from.
type mapgenResult struct {
	m       *mapgen.Map
	world   string // the planet as its maps spell it
	class   string // the ground class the measurements were made with
	sources []mapgenSourceID
}

// mapgenJob is what the generator's goroutines share with the panel.
type mapgenJob struct {
	mu sync.Mutex
	// wg counts the goroutines reading the content; join waits for them
	// before the host may unmount it.
	wg sync.WaitGroup
	// The worlds of the content they were listed for.
	cs          *contentSet
	worlds      []mapgenWorldInfo
	worldsReady bool
	worldsErr   error
	// The last world's generator and its sources, kept while the panel is
	// open: indexing a world's tiles is most of a first generation.
	genWorld   string
	gen        *mapgen.Generator
	genClass   string
	genSources []mapgenSourceID
	dry        map[string]bool // worlds known to have no water art
	// serial names the latest request; a finished older one is dropped.
	serial      int
	running     bool
	stage       string
	done, total int
	cancel      context.CancelFunc
	result      *mapgenResult
	err         error
}

// bind lists the worlds of cs in the background, unless they are listed
// already; it forgets everything made from other content.
func (j *mapgenJob) bind(cs *contentSet) {
	j.mu.Lock()
	if j.cs == cs {
		j.mu.Unlock()
		return
	}
	j.cs, j.worlds, j.worldsReady, j.worldsErr = cs, nil, false, nil
	j.gen, j.genWorld, j.genClass, j.genSources, j.result, j.dry = nil, "", "", nil, nil, map[string]bool{}
	if cs == nil {
		j.mu.Unlock()
		return
	}
	j.wg.Add(1)
	j.mu.Unlock()
	go func() {
		defer j.wg.Done()
		worlds, err := mapgenWorlds(cs.fs)
		worlds = slices.DeleteFunc(worlds, func(w mapgenWorldInfo) bool { return len(w.Maps) < mapgenMinMaps })
		j.mu.Lock()
		defer j.mu.Unlock()
		if j.cs == cs {
			j.worlds, j.worldsErr, j.worldsReady = worlds, err, true
		}
	}()
}

// listed is the content's worlds, and whether they are listed yet.
func (j *mapgenJob) listed() ([]mapgenWorldInfo, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.worlds, j.worldsReady, j.worldsErr
}

// waiting reports a request made before the worlds were listed, which can
// start now.
func (j *mapgenJob) waiting() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.running && j.cancel == nil && j.worldsReady
}

// isDry reports a world known to have no water art.
func (j *mapgenJob) isDry(world string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.dry[world]
}

// start generates the map p names on world, cancelling any running request.
// Before the worlds are listed it only records that a request waits.
func (j *mapgenJob) start(world string, p mapgen.Params) {
	j.mu.Lock()
	if j.cancel != nil {
		j.cancel()
	}
	j.serial++
	serial := j.serial
	j.running, j.stage, j.done, j.total, j.err, j.cancel = true, "Listing the worlds…", 0, 0, nil, nil
	cs, worlds, ready, listErr := j.cs, j.worlds, j.worldsReady, j.worldsErr
	if !ready {
		j.mu.Unlock()
		return
	}
	i := slices.IndexFunc(worlds, func(w mapgenWorldInfo) bool { return content.CanonicalKey(w.Planet) == world })
	if listErr != nil || i < 0 {
		j.running, j.err = false, listErr
		if listErr == nil {
			j.err = errors.New("No world has enough maps to take tiles from")
		}
		j.mu.Unlock()
		return
	}
	info := worlds[i]
	if j.dry[world] {
		p.Water = 0
	}
	ctx, cancel := context.WithCancel(context.Background())
	j.cancel = cancel
	gen, class, sources := j.gen, j.genClass, j.genSources
	if j.genWorld != world {
		gen, class, sources = nil, "", nil
	}
	j.wg.Add(1)
	j.mu.Unlock()

	limit := cs.limits.TNTBytes
	if limit <= 0 {
		limit = maplibrary.MaxTNTBytes
	}
	go func() {
		defer j.wg.Done()
		defer cancel()
		report := func(stage string, done, total int) bool {
			j.mu.Lock()
			defer j.mu.Unlock()
			if j.serial != serial {
				return false
			}
			j.stage, j.done, j.total = stage, done, total
			return true
		}
		fail := func(err error) {
			j.mu.Lock()
			defer j.mu.Unlock()
			if j.serial == serial {
				j.running, j.err, j.result = false, err, nil
			}
		}
		if gen == nil {
			if !report("Reading the world's maps…", 0, 0) {
				return
			}
			w, ids, err := mapgenWorld(cs.fs, info, limit, nil)
			if err == nil {
				gen, err = mapgen.NewGenerator(w)
			}
			if err != nil {
				fail(err)
				return
			}
			gen.Workers, class, sources = mapgenWorkers(), w.Classes[0].Name, ids
			j.mu.Lock()
			j.dry[world] = !gen.Wet()
			current := j.serial == serial
			if current {
				// Only the latest request keeps its world: an older one
				// that was indexing another world leaves it alone.
				j.gen, j.genWorld, j.genClass, j.genSources = gen, world, class, sources
			}
			j.mu.Unlock()
			if !current {
				return
			}
			if !gen.Wet() {
				p.Water = 0
			}
		}
		m, err := gen.Generate(ctx, p, func(done, total int) { report("Generating…", done, total) })
		j.mu.Lock()
		defer j.mu.Unlock()
		if j.serial != serial {
			return
		}
		// A failed request clears the picture too: what is shown is always
		// what Use and Save take, made from the settings beside it.
		j.running, j.err, j.result = false, err, nil
		if err == nil {
			j.result = &mapgenResult{m: m, world: info.Planet, class: class, sources: sources}
		}
	}()
}

// state is the latest finished map, and whether a newer one is being made.
func (j *mapgenJob) state() (r *mapgenResult, running bool, stage string, done, total int, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.result, j.running, j.stage, j.done, j.total, j.err
}

// stop cancels the running request and waits for every goroutine reading
// the content, then lets the indexed world go: it is the bulk of the
// generator's memory, and the next panel indexes it again.
func (j *mapgenJob) stop() {
	j.mu.Lock()
	if j.cancel != nil {
		j.cancel()
	}
	j.serial++
	j.running = false
	j.mu.Unlock()
	j.wg.Wait()
	j.mu.Lock()
	j.gen, j.genWorld, j.genClass, j.genSources = nil, "", "", nil
	j.mu.Unlock()
}
