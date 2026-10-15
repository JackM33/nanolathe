package gpurender

import (
	"runtime"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// Parallel placement (docs/DESIGN_GPU_RENDERER.md §22, "Placement workers").
//
// A frame whose appends cannot interact is placed in five steps:
//
//  1. a pre-check (placeParallelFrame) proves the frame is one where they
//     cannot: no two keyed packets share a body, the keyed packets fit the
//     body index without evicting one another, and the frame's parameter
//     entries fit the image with room to spare, so no capacity test an
//     append makes can depend on where the append runs;
//  2. a sequential pre-pass allocates every job, in the sequential lane's
//     order, and decides every packet: the ordered store and body index
//     operations, the append path, and every texture the appends will reach,
//     resolved in order into slots the appends read, with the texture page
//     each lane will find;
//  3. the pool fills each job, into the context of the participant that
//     claims it, as soon as the pre-pass has published it, so the fill runs
//     beside the pre-pass — a reflecting face's reflection is recorded rather
//     than appended;
//  4. a sequential layout lays the jobs' output end to end in job order,
//     joining a job's first run to the batch's last exactly where colourRun
//     would have, and sums the accounting;
//  5. the pool copies every job's output into the frame's batch, rebasing the
//     lanes that name a parameter entry and the indices of a joined run,
//     while the placing goroutine appends the recorded reflections in job
//     order, which is the order the sequential lane appends them in.
//
// A frame the pre-check rejects is placed sequentially; a frame whose batch
// turns out larger than colourRun's vertex limit could split is filled again,
// sequentially, from the same decisions. Either way the output is the
// sequential lane's, byte for byte.
//
// The pool is a presentation-side worker set: it reads the recorded list and
// the renderer's read-only state and writes only its own contexts, the store
// entries its jobs own, and disjoint ranges of the frame's batch. It touches
// no simulation state [I6] and no output depends on which worker ran what [I1].

// Pre-check defaults. The lane's tuning (modelPlaceTuning) overrides them for
// the tests that exercise each guard; zero keeps the default.
const (
	// modelPlaceFloor is the job count below which a frame stays on the
	// placing goroutine: the wakes and the joins cost more than the few jobs.
	// It is a timing threshold, not a behavioural one.
	modelPlaceFloor = 32
	// modelPlaceKeyCap bounds the frame's keyed packets. Each touches at most
	// one store entry and one body entry, and touched entries move to the
	// front of their LRU, so with at most this many an insert that evicts
	// always evicts an entry no job of this frame reads or writes.
	modelPlaceKeyCap = modelRetainBodyCap
	// modelPlaceParamBound bounds the frame's parameter slots: every
	// capacity test an append makes passes with two slots to spare (a
	// capture's own test asks for two), wherever its job runs.
	modelPlaceParamBound = modelDirectParamCap - modelQuadSlots
	// modelPlaceVertexLimit bounds a parallel frame's batch: below it no run
	// can reach colourRun's vertex limit, so a run's joins are decided by its
	// images and page alone. A retained segment is at most half the limit
	// (finishCapture), any other append adds at most eight vertices.
	modelPlaceVertexLimit = schedRunVertexLimit / 2
)

// modelPlaceParticipants caps the pool, the placing goroutine included. The
// sequential pre-pass publishes the jobs, so participants beyond what keeps up
// with it only wait, yielding, for jobs that are not there yet. Measured in the
// live window at 1,600 units: six placed as fast as twelve (1.29 against 1.26
// ms at 0.75× zoom, 1.08 against 1.14 ms at 1×) for about a quarter of a core
// less; four fell behind at 0.75× (1.58 ms).
const modelPlaceParticipants = 6

// modelPlaceTuning is the pre-check's thresholds, for tests; zero is the
// default. order, when set, gives the order the participants claim a frame's
// jobs in: a permutation of 0..jobs−1.
type modelPlaceTuning struct {
	floor, keyCap, paramBound, vertexLimit, perHelper int
	order                                             func(jobs int) []int32
}

func tuned(v, def int) int {
	if v != 0 {
		return v
	}
	return def
}

// modelPlaceMode is how the last frame was placed, for tests.
type modelPlaceMode uint8

const (
	modelPlacedSequential modelPlaceMode = iota
	modelPlacedParallel
	modelPlacedRefilled
)

// placeParallelFrame reports whether the frame's pending packets can be
// placed in parallel (step 1), and bounds the frame's jobs and appends. It
// counts conservatively: every child and every shadow a pending subject could
// append, whether or not the atlas holds it.
func (r *Renderer) placeParallelFrame() (jobs, packets int, ok bool) {
	d := &r.modelDirect
	if d.placeKeys == nil {
		d.placeKeys = make(map[modelBodyKey]struct{})
	} else {
		clear(d.placeKeys)
	}
	params := 0
	for _, p := range d.pending {
		g := p.g
		jobs++
		packets++
		if !d.placeKey(g) {
			return 0, 0, false
		}
		if p.shadow {
			continue
		}
		params += modelPlaceParams(g)
		for _, child := range g.Children {
			cg := child.Geometry
			if !mergeableChild(cg) {
				continue
			}
			packets++
			params += modelPlaceParams(cg)
			if cg.Shadow != nil {
				// The solo image.
				packets++
				params += modelPlaceParams(cg)
			}
		}
		if sg := g.Shadow; sg != nil && !sg.Silhouette {
			jobs++
			packets++
			if !d.placeKey(sg) {
				return 0, 0, false
			}
		}
	}
	ok = jobs >= tuned(d.place.floor, modelPlaceFloor) &&
		len(d.placeKeys) <= tuned(d.place.keyCap, modelPlaceKeyCap) &&
		params <= tuned(d.place.paramBound, modelPlaceParamBound)
	return jobs, packets, ok
}

// placeKey admits one top-level packet's key to the frame's set, or reports
// false when another packet of the frame already holds its body.
func (d *modelDirectLane) placeKey(g *drawlist.ModelGeometry) bool {
	if !g.Cache.Reusable() {
		return true
	}
	k := modelBodyKey{g.Cache.Body, g.Cache.Lane}
	if _, dup := d.placeKeys[k]; dup {
		return false
	}
	d.placeKeys[k] = struct{}{}
	return true
}

// modelPlaceParams bounds the parameter slots one non-shadow packet's append
// can take: its verdict entry, two slots for every face of the larger raster
// (each could be a mapped quad), and a key entry per outline ring.
func modelPlaceParams(g *drawlist.ModelGeometry) int {
	faces := len(g.Faces) + len(g.LiveFaces)
	if ss := g.Supersample; ss != nil {
		faces = max(faces, len(ss.Faces)+len(ss.LiveFaces))
	}
	return 1 + modelQuadSlots*faces + len(g.Outline)
}

// placeParallel places the frame's pending packets on the pool (steps 2–5);
// jobs and packets bound the frame's (placeParallelFrame). The participants
// are woken first and fill each job as soon as the pre-pass has decided it,
// so the fill runs beside the pre-pass rather than after it.
func (r *Renderer) placeParallel(jobs, packets int) {
	d := &r.modelDirect
	// Both lists are sized for the whole frame before anyone reads them: the
	// participants read the jobs already published while the pre-pass appends
	// the next, so neither backing array may move.
	d.jobs = slices.Grow(d.jobs[:0], jobs)
	d.packets = slices.Grow(d.packets[:0], packets)
	d.slots.reset()
	d.claimOrder = nil
	// A test's claim order needs the frame's job count, so it publishes the
	// jobs only once they are all decided.
	stream := d.place.order == nil
	pool := r.modelPlacePool()
	pool.start(r, d.jobs[:cap(d.jobs)], d.packets[:cap(d.packets)], jobs/tuned(d.place.perHelper, modelPlaceJobsPerHelper))
	// Step 2: the jobs in the sequential lane's order, allocated and decided,
	// each published to the participants as it is decided.
	for _, p := range d.pending {
		if _, seen := d.regions[p.g]; seen {
			continue
		}
		if p.shadow {
			if job, ok := r.allocShadowJob(p.g); ok {
				r.decideJob(pool, &job, stream)
			}
			continue
		}
		job, ok := r.allocSubjectJob(p.g)
		if !ok {
			continue
		}
		r.decideJob(pool, &job, stream)
		if sg := p.g.Shadow; sg != nil && !sg.Silhouette {
			if job, ok := r.allocShadowJob(sg); ok {
				r.decideJob(pool, &job, stream)
			}
		}
	}
	if !stream {
		d.claimOrder = d.place.order(len(d.jobs))
	}
	pool.published.Store(int64(len(d.jobs)))
	pool.decided.Store(true)
	// Step 3: the placing goroutine fills what is left, then waits for the
	// jobs the others hold.
	r.fillWorker(pool, pool.workers[0])
	for pool.filled.Load() < int64(len(d.jobs)) {
		runtime.Gosched()
	}
	total := 0
	for i := range d.jobs {
		total += int(d.jobs[i].v1 - d.jobs[i].v0)
	}
	if total > tuned(d.place.vertexLimit, modelPlaceVertexLimit) {
		// A batch this large could split a run at colourRun's vertex limit,
		// which depends on everything before it: fill it again in order on
		// the lane's own context, from the same decisions. The captures and
		// body recordings the workers made are redone from the start.
		pool.stage.Store(modelPlaceStageAbort)
		pool.finish()
		lane := r.laneCtx()
		lane.decided = true
		for i := range d.jobs {
			r.fillJob(lane, &d.jobs[i], d.packets)
		}
		lane.decided = false
		lane.flushStats(&r.modelStats)
		d.placeMode = modelPlacedRefilled
		return
	}
	// Step 4: layout.
	var v, ix, q int32
	for i := range d.jobs {
		job := &d.jobs[i]
		c := &pool.workers[job.w].ctx
		job.v, job.ix, job.q = v, ix, q
		job.joined, job.delta = false, 0
		for k := job.r0; k < job.r1; k++ {
			run := c.runs[k]
			if k == job.r0 && len(d.runs) > 0 {
				last := &d.runs[len(d.runs)-1]
				if last.imgs == run.imgs && last.page == run.page && last.seedPhase == run.seedPhase {
					job.joined, job.delta = true, v-last.vOff
					last.vLen += run.vLen
					last.iLen += run.iLen
					continue
				}
			}
			run.vOff += v - job.v0
			run.iOff += ix - job.i0
			d.runs = append(d.runs, run)
		}
		v += job.v1 - job.v0
		ix += job.i1 - job.i0
		q += job.q1 - job.q0
	}
	for _, w := range pool.workers {
		w.ctx.flushStats(&r.modelStats)
	}
	d.verts = slices.Grow(d.verts[:0], int(v))[:v]
	d.idx = slices.Grow(d.idx[:0], int(ix))[:ix]
	d.params.buf = slices.Grow(d.params.buf[:0], int(q)*modelQuadBytes)[:int(q)*modelQuadBytes]
	d.params.count = int(q)
	// Step 5: scatter, the placing goroutine appending the recorded
	// reflections first.
	pool.stage.Store(modelPlaceStageScatter)
	r.replayReflections(pool)
	r.scatterWorker(pool)
	pool.finish()
	d.placeMode = modelPlacedParallel
}

// decideJob decides every packet of a job allocated in the pre-pass, as the
// sequential lane would at that packet's turn, queues the job and, when
// streaming, publishes it to the participants. A packet's verdict entry is
// certain to be made (the parameter bound), so a replayable packet's frame
// alone says whether its faces may map, and every capacity test passes; a
// packet that does not replay takes no path its frame decides.
func (r *Renderer) decideJob(pool *modelPlacePool, job *modelPlaceJob, publish bool) {
	d := &r.modelDirect
	if cap(d.packets) != len(pool.packets) || len(d.jobs) == len(pool.jobs) {
		panic("nanolathe: parallel placement outgrew its bounds: logical path model lane, providers searched [pre-check], expected the frame's job and packet bounds")
	}
	for i := job.p0; i < job.p1; i++ {
		p := &d.packets[i]
		r.decideLane(p)
		noQuads := false
		if p.hit && !p.shadow {
			_, _, fits := modelFrameLanes(p.ox, p.oy)
			noQuads = !fits
		}
		r.decidePath(p, noQuads, 0)
		// Every texture the appends will look up, resolved in the order the
		// sequential lane resolves them, and the texture page each lane finds.
		p.texCached = r.texturePage()
		if p.path != modelPlaceReplay && !p.warm {
			p.cachedSlots = r.resolveTextureSlots(&d.slots, p.raster.Faces)
		}
		p.texLive = r.texturePage()
		p.liveSlots = r.resolveTextureSlots(&d.slots, p.raster.LiveFaces)
	}
	d.jobs = append(d.jobs, *job)
	if publish {
		pool.published.Store(int64(len(d.jobs)))
	}
}

// resolveTextureSlots resolves every face's texture slot, in order, into
// storage taken from the frame's arena, which a later take never moves.
func (r *Renderer) resolveTextureSlots(arena *frameArena[modelTextureSlot], faces []drawlist.ModelFace) []modelTextureSlot {
	slots := arena.take(len(faces))
	for i := range faces {
		slots[i] = r.modelTextureFor(faces[i].Texture)
	}
	return slots
}

// The dispatch's stages after the fill.
const (
	modelPlaceStageFill = iota
	modelPlaceStageScatter
	modelPlaceStageAbort
)

// modelPlaceWorker is one participant: a placement context whose appends
// touch nothing another participant's do, and the outline walk's arena. The
// padding keeps two participants' hot fields off one cache line.
type modelPlaceWorker struct {
	ctx  modelPlaceCtx
	prep modelPrepScratch
	id   int32
	_    [128]byte
}

// modelPlacePool is the persistent placement pool, the pattern of the
// recorder's unit pool (§13.9): n−1 goroutines parked on a one-slot wake
// channel between frames, the placing goroutine the n-th participant, and
// shared cursors over the jobs, because a job's cost varies by two orders of
// magnitude (a replayed shadow against a construction group) and a shared
// cursor balances better than a stripe. Tallest-first order puts the largest
// jobs first. A frame is one dispatch: the participants are woken as the
// pre-pass starts, fill jobs as they are published, then wait — yielding,
// not parking, because the layout between is a few microseconds — for the
// scatter or an abort.
type modelPlacePool struct {
	workers []*modelPlaceWorker
	wake    []chan struct{}
	quit    chan struct{}
	done    sync.WaitGroup
	// closeOnce guards the quit close so a second close is a no-op, and
	// exited is released by each goroutine as it returns, so close returns
	// only once they are gone.
	closeOnce sync.Once
	exited    sync.WaitGroup

	// The dispatch in flight: published jobs, whether the pre-pass is done,
	// the fill and scatter cursors, the jobs filled and the stage after the
	// fill. r, jobs and packets are nil between dispatches, so an idle pool
	// holds no reference to its renderer.
	published, fillCursor, filled, scatterCursor atomic.Int64
	decided                                      atomic.Bool
	stage                                        atomic.Int32
	r                                            *Renderer
	jobs                                         []modelPlaceJob
	packets                                      []modelPlacePacket
}

// newModelPlacePool starts the pool: n participants including the placing
// goroutine.
func newModelPlacePool(n int) *modelPlacePool {
	n = max(n, 1)
	p := &modelPlacePool{quit: make(chan struct{})}
	for i := 0; i < n; i++ {
		w := &modelPlaceWorker{id: int32(i)}
		w.ctx.decided, w.ctx.worker, w.ctx.prep = true, true, &w.prep
		p.workers = append(p.workers, w)
		if i == 0 {
			continue
		}
		// One slot of buffer so the placing goroutine never blocks handing
		// out a wake to a worker that has not yet looped back to its select.
		wake := make(chan struct{}, 1)
		p.wake = append(p.wake, wake)
		p.exited.Add(1)
		go p.serve(w, wake)
	}
	return p
}

func (p *modelPlacePool) serve(w *modelPlaceWorker, wake chan struct{}) {
	defer p.exited.Done()
	for {
		select {
		case <-p.quit:
			return
		case <-wake:
			r := p.r
			r.fillWorker(p, w)
			for {
				stage := p.stage.Load()
				if stage == modelPlaceStageScatter {
					r.scatterWorker(p)
					break
				}
				if stage == modelPlaceStageAbort {
					break
				}
				runtime.Gosched()
			}
			p.done.Done()
		}
	}
}

// modelPlaceJobsPerHelper is the jobs that justify waking one more helper: a
// wake costs the placing goroutine a scheduler round trip, often an OS thread
// wake, so a frame of a few dozen jobs — a settings preview — wakes one
// helper and a crowded battle every participant. The participant that claims
// a job places it; how many participate changes no result.
const modelPlaceJobsPerHelper = 32

// start opens a frame's dispatch over the frame's job and packet storage and
// wakes at most helpers of the participants. Every participant's context is
// emptied here, before the wakes: a participant woken too late to claim a job
// then writes nothing the placing goroutine reads once the jobs are filled.
func (p *modelPlacePool) start(r *Renderer, jobs []modelPlaceJob, packets []modelPlacePacket, helpers int) {
	for _, w := range p.workers {
		w.ctx.resetFrame()
		w.ctx.stats = modelPlaceStats{}
		w.prep.reset()
	}
	p.r, p.jobs, p.packets = r, jobs, packets
	p.published.Store(0)
	p.decided.Store(false)
	p.fillCursor.Store(0)
	p.filled.Store(0)
	p.scatterCursor.Store(0)
	p.stage.Store(modelPlaceStageFill)
	helpers = max(0, min(helpers, len(p.wake)))
	p.done.Add(helpers)
	for _, wake := range p.wake[:helpers] {
		wake <- struct{}{}
	}
}

// finish waits for the participants to leave the dispatch.
func (p *modelPlacePool) finish() {
	p.done.Wait()
	p.r, p.jobs, p.packets = nil, nil, nil
}

// awaitJob waits until job i is published and reports true, or reports false
// once the pre-pass is done without it.
func (p *modelPlacePool) awaitJob(i int64) bool {
	for {
		if i < p.published.Load() {
			return true
		}
		if p.decided.Load() {
			return i < p.published.Load()
		}
		runtime.Gosched()
	}
}

// fillWorker is one participant's fill: the jobs it claims appended into its
// context as the pre-pass publishes them, each job's spans recorded on the
// job.
func (r *Renderer) fillWorker(p *modelPlacePool, w *modelPlaceWorker) {
	d := &r.modelDirect
	c := &w.ctx
	for {
		i := p.fillCursor.Add(1) - 1
		if !p.awaitJob(i) {
			return
		}
		j := int(i)
		if d.claimOrder != nil {
			j = int(d.claimOrder[i])
		}
		job := &p.jobs[j]
		job.w = w.id
		job.v0, job.i0, job.r0, job.q0, job.f0 = int32(len(c.verts)), int32(len(c.idx)), int32(len(c.runs)), int32(c.params.count), int32(len(c.reflected))
		// A job's runs are its own until the layout joins them to the batch.
		c.runBase = len(c.runs)
		r.fillJob(c, job, p.packets)
		job.v1, job.i1, job.r1, job.q1, job.f1 = int32(len(c.verts)), int32(len(c.idx)), int32(len(c.runs)), int32(c.params.count), int32(len(c.reflected))
		p.filled.Add(1)
	}
}

// scatterWorker copies the jobs it claims into their laid-out place in the
// frame's batch. A context's parameter slots are one-based over its own
// entries, so a job's slot s is the frame's s − q0 + q: the lanes that name
// one — a vertex's verdict entry, of either sign, and the entry a mapped
// face or an outline ring reads through its blue lane — take that shift.
// Every slot is an integer far below 2²⁴, so the float sum is exact. A
// joined first run's indices are relative to the run the layout joined it
// to, delta vertices earlier.
func (r *Renderer) scatterWorker(p *modelPlacePool) {
	d := &r.modelDirect
	for {
		i := p.scatterCursor.Add(1) - 1
		if i >= int64(len(d.jobs)) {
			return
		}
		job := &d.jobs[i]
		c := &p.workers[job.w].ctx
		shift := float32(job.q - job.q0)
		dst := d.verts[job.v : job.v+(job.v1-job.v0)]
		for i, v := range c.verts[job.v0:job.v1] {
			if v.Custom2 > 0 {
				v.Custom2 += shift
			} else if v.Custom2 < 0 {
				v.Custom2 -= shift
			}
			mode := int(v.Custom0) % modelDirectSeedStride
			if mode >= modelDirectLive {
				mode -= modelDirectLive
			}
			switch mode {
			case modelDirectQuad, modelDirectQuadShade, modelDirectFlatQuad, modelDirectFlatQuadShade:
				v.ColorB += shift
			case modelDirectOutline:
				if v.ColorB > 0.5 {
					v.ColorB += shift
				}
			}
			dst[i] = v
		}
		idx := d.idx[job.ix : job.ix+(job.i1-job.i0)]
		copy(idx, c.idx[job.i0:job.i1])
		if job.joined {
			delta := uint32(job.delta)
			first := idx[:c.runs[job.r0].iLen]
			for i := range first {
				first[i] += delta
			}
		}
		copy(d.params.buf[int(job.q)*modelQuadBytes:], c.params.buf[int(job.q0)*modelQuadBytes:int(job.q1)*modelQuadBytes])
	}
}

// replayReflections appends every face reflection the jobs recorded, in job
// order and in each job's own order, which is the order the sequential lane
// reflects them in: the reflection batch's runs and its vertex cap depend on
// every reflection before. A recorded quad entry is the job's context's slot
// and takes the job's shift.
func (r *Renderer) replayReflections(p *modelPlacePool) {
	d := &r.modelDirect
	for j := range d.jobs {
		job := &d.jobs[j]
		if job.f0 == job.f1 {
			continue
		}
		c := &p.workers[job.w].ctx
		shift := int(job.q - job.q0)
		for k := job.f0; k < job.f1; k++ {
			m := c.reflected[k]
			if m.quad != 0 {
				m.quad += shift
			}
			r.reflectModelFaceAs(&m)
		}
	}
}

// modelPlacePool returns the renderer's placement pool, starting it on first
// use. The pool is stopped when the renderer is collected: an idle pool holds
// no reference back to it, so a host that drops a renderer, or a test that
// makes many, leaks no goroutine.
func (r *Renderer) modelPlacePool() *modelPlacePool {
	if r.placePool == nil {
		n := r.placeWorkers
		if n <= 0 {
			n = min(runtime.GOMAXPROCS(0), modelPlaceParticipants)
		}
		r.placePool = newModelPlacePool(n)
		runtime.AddCleanup(r, func(p *modelPlacePool) { p.close() }, r.placePool)
	}
	return r.placePool
}

// close stops every worker and returns once each has left its loop. It is
// idempotent and safe on a pool that was never started, and must not run
// beside a dispatch.
func (p *modelPlacePool) close() {
	if p == nil {
		return
	}
	p.closeOnce.Do(func() { close(p.quit) })
	p.exited.Wait()
}
