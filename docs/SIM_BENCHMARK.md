# Simulation-cost benchmark

The displayless companion to [the live battle benchmark](BATTLE_BENCHMARK.md).
That one measures a window and a renderer presenting a battle; this one
measures authoritative ticks with no window, renderer, audio device or
presentation consumer. It reports elapsed tick time and process CPU consumption
across the measured window, with optional thread CPU time per tick. Use it when
the change is to the simulation: movement, pathfinding, COB, weapons, damage,
economy, construction,
the feature lifecycle, visibility, AI, or the publication boundary.

```
tools/sim-bench /tmp/sim-baseline --seed 7
```

The first positional value after `--sim-benchmark` is the output directory and
it must not already exist, the same convention the windowed benchmark uses, so
one comparison can never mix two runs' artifacts. Runs serialize with each
other and with the windowed benchmark through the shared benchmark lock
(`internal/platform/benchlock`); wait time is outside every measurement.

`tools/sim-bench` defaults to a quick sample: the same 1,200 warm-up ticks,
then 300 measured ticks with profiles disabled. That is 1,500 total ticks
instead of 4,200 (64% fewer steps), measuring combat onset rather than the full
sustained battle. Inspect its census; a shorter window is not evidence about
late combat. `tools/sim-bench --full OUT` retains the 3,000-tick profiled window.
Explicit options after OUT override the quick defaults. The native command's
defaults remain unchanged.

The wrapper defaults to two Go runtime workers and two build jobs (override
`GOMAXPROCS` / `NANOLATHE_TEST_P`). Compilation takes the benchmark lock; the
native benchmark acquires it again for setup and measurement. Verification
gates do not take it and may run beside a benchmark.
Record and match the runtime worker setting when comparing. These limits reduce
contention; they do not guarantee quiet-machine timings.

Build the binary once and run it directly when comparing; use `tools/host-run`
for compilation so it cannot overlap another benchmark:

```
GOMAXPROCS=2 tools/host-run go build -p 2 -o /tmp/nanolathe-headless ./cmd/nanolathe-headless
GOMAXPROCS=2 /tmp/nanolathe-headless --sim-benchmark=/tmp/sim-a --seed=7
GOMAXPROCS=2 /tmp/nanolathe-headless --sim-benchmark=/tmp/sim-b --seed=7
```

## Checkpoint cost comparison

`--sim-benchmark-checkpoints` opts into the full checkpoint and tick-summary
capture path ([DESIGN_MULTIPLAYER §16.3.79](DESIGN_MULTIPLAYER.md#16379-u7-measurement-composition)).
It uses the M2 single-seat admitted constructor with the same map, seeds,
resources, rules and 250-unit scene, stages the armies before any tick, publishes
the opening and enables capture once. The default remains the ordinary benchmark
entry. Compare matching Modern runs with the flag off and on:

```sh
GOMAXPROCS=2 /tmp/nanolathe-headless --sim-benchmark=/tmp/checkpoints-off \
  --root="$HOME/TotalAnnihilation" --gameplay=modern --seed=7 \
  --benchmark-profiles=false --phase-timing=false --thread-cpu-timing=false
GOMAXPROCS=2 /tmp/nanolathe-headless --sim-benchmark=/tmp/checkpoints-on \
  --root="$HOME/TotalAnnihilation" --gameplay=modern --seed=7 \
  --benchmark-profiles=false --phase-timing=false --thread-cpu-timing=false \
  --sim-benchmark-checkpoints
```

The fixture's admission room uses content profile `retail`, participant identity
1 for the passive human, player scales 1024..2048 without full-map, spectator and
replay scales 256..2048 with full-map, policy revision/scheduling/pacing/drop/
audience 1, 90000 ms cumulative grace and zero audience delays. These are fixed
benchmark inputs, not lobby defaults. Admission failures are reported. Strict's
one-computer-per-human limit refuses this three-computer scene. Enlarged
checkpoint scenes also refuse for now: their commander relocation replaces a
movement binding through the ordinary setup API, which invalidates its proof.
Ordinary enlarged benchmarks retain their existing behavior.

Every enabled step checks the value-only capture result; any diagnostic failure
fails the run. The report includes `checkpoints` and, when enabled,
`checkpoint_records` and `checkpoint_ticks` (the retained history lengths),
`checkpoint_tick` (the latest retained full capture's tick; omitted at entry
zero), and `checkpoint_digest` (its full SHA-256 in hexadecimal). History is
copied once after the measured window. The normal timing and allocation series
include the capture cadence; entry composition and the opening digest remain
outside the window. Cumulative death census reads the world's exact primary
hook-dispatch counter without replacing the admitted callback.

Correctness tests compare partial fingerprints, RNG totals and census between
enabled and disabled scenes and require runtime checkpoint evidence. They make
no timing claim. Acceptance budgets and measured results belong to M3-C9 after
exploration; a partial fingerprint alone is not full checkpoint evidence.

## The scene

Scene version 1, on **Town & Country** (540×540 cells, 8640×8640 world units).
Of the stock maps large enough for three 250-unit armies, this is the one whose
flattest three-army triangle scores a perfect placement heuristic *and* carries
a full feature table — about six thousand plots, two thirds of them flammable —
so the armies march on traversable ground while feature obstruction, wreckage
and fire are exercised at the same time. A map with a bare feature table
measures a materially cheaper publication.

Four setup slots. Retail's skirmish lobby refuses a setup with no human
("There must be at least one player and one computer opponent",
`[08 "Skirmish configuration"]`), so slot 0 is a human placeholder in an ally
group of its own that owns nothing but the commander battle entry stamps for it
and issues no command for the whole run. It is also the local/viewing owner.
Slots 1..3 are the three measured computer armies, each in its own ally group,
so all three are mutually hostile. Commander death is set to *continues*, so
losing a computer commander does not eliminate its remaining army. The human placeholder
still loses if its sole unit dies.

`ApplyDefaults` treats a zero `Side` as an absent value and installs `slot & 1`,
so a slot cannot ask for side 0 explicitly: the measured armies are CORE, ARM,
CORE. Both sides are exercised.

Each army defaults to 250 units — 50 buildings and 200 mobiles — plus the
commander:

| Role | ARM | CORE | Per team |
|---|---|---|---|
| solar collector | armsolar | corsolar | 14 |
| wind generator | armwin | corwin | 6 |
| metal maker | armmakr | cormakr | 6 |
| kbot lab | armlab | corlab | 2 |
| vehicle plant | armvp | corvp | 1 |
| aircraft plant | armap | corap | 1 |
| defence tower | armllt | corllt | 12 |
| radar | armrad | corrad | 4 |
| metal storage | armmstor | cormstor | 2 |
| energy storage | armestor | corestor | 2 |
| medium tank | armstump | corraid | 28 |
| assault tank | armflash | corlevlr | 28 |
| scout tank | armfav | corfav | 24 |
| light kbot | armpw | corak | 28 |
| rocket kbot | armrock | corstorm | 24 |
| artillery kbot | armham | corthud | 24 |
| fighter | armfig | corveng | 14 |
| bomber | armthund | corshad | 12 |
| scout aircraft | armpeep | corfink | 6 |
| construction kbot | armck | corck | 6 |
| construction vehicle | armcv | corcv | 6 |

`--sim-benchmark-army-size=334` places 334 units per computer army, plus
its commander: 1,006 initial units including the idle human commander. The
accepted range is 250..1000, and the army plus its commander must fit the
**resolved** per-player unit limit. Larger Strict fixtures therefore need a
larger `--unit-limit`; Community and Modern follow their resolved feature table
unless `--unit-limit` is given, which is layered over it.
All sizes retain the fifty buildings. Mobile roles scale in the table order:
for each cumulative default count, take `count × (army size − 50) / 200` with
integer truncation, then subtract the preceding quota. This assigns every
mobile, including rounding remainders. The default reproduces scene version 1
exactly; compare only matching `scene.army_size`, `scene.mobile_roster` and
`scene.passive_human`.

Enlarged fixtures also relocate the existing passive human commander before
any tick. The default map start can put that sole human unit in the enlarged
battle's path, ending the session while the computer armies are still active.
The fixture scans land on a 128-unit lattice, 256 units inside the map edges,
and chooses the valid free commander footprint furthest from its nearest
opponent, including the other original commanders. Ties keep the first point
in Z/X order; placement fails if no point is at least 2300 world units from
every opponent. The ordinary movement placement API commits the position,
recorded in `scene.passive_human`. It draws no RNG, adds no units and grants no
immunity: all ordinary damage and result rules still apply. The 250-unit default
retains its original commander position and fingerprints.

The larger fixtures retain the default placement heuristic, pitches, columns,
and orders; extra mobiles deepen the existing blocks. At large sizes those
blocks can overlap terrain obstacles or other blocks beyond the area sampled
by the default flatness heuristic. Review the census for the resulting workload
rather than assuming it has the default scene's travel and combat timing.

Geometry: the fixture picks the battle centre by a deterministic flatness
search over the terrain (no random draw, no map iteration), then lays each
army's mobiles in a block 1050 world units from that centre at 120° spacing and
its buildings 2300 units out behind them. Each army's four factories carry a
twenty-deep production queue; each mobile builder (twelve at the default size)
carries three ordinary site-anchored build orders behind its own block. Every other mobile
gets a `Move_Ground` order onto the battle centre. Like the windowed
benchmark's, this fixture places its units directly; it is not a normal
skirmish opening.

The scripted moves only guarantee that the armies meet. The AI managers then
take over on their own schedule: the classifier assigns armed ground units to
the regroup records every thirty eligible manager entries, the wave task merges
regroup into wave and broadcasts attack orders every three hundred ticks, and
by roughly tick one thousand each army carries about 160 attack orders it
issued itself. The census records both, so a run says which is driving it.

Each computer army starts with 32000 metal and 32000 energy so production and
the AI's own construction tasks proceed through the window instead of stalling
on an empty ledger. The passive human starts with 1000 of each resource.
The configured unit setting is 400. Strict uses that setting; Community and
Modern use the resolved feature table
([DESIGN_COMMUNITY_PATCH §3](DESIGN_COMMUNITY_PATCH.md#3-the-feature-table)),
whose mainline unit limit is 1500. The benchmark never reads the saved
`unitLimit`. The effective limit sizes the unit pool at
`limit × 10 + 1` records and is recorded in the scene. Use the same effective
limit when comparing revisions; `--unit-limit=400` (or
`--gameplay-feature=unitLimit=400`) selects the 400-unit layout under
Community or Modern.

## The window

One `Step` call per authoritative tick, so the once-per-pump executor tail runs
on the same cadence as the tick it follows. Warm-up is 1200 unmeasured ticks —
the AI's first classification, construction, resource and wave deadlines, and
the march that brings the armies into contact around tick one thousand. The
native/full measured window is then 3000 ticks (100 s of game time) from battle onset
through sustained engagement, while all three armies remain active.
`--warmup-ticks` and `--benchmark-ticks` move both.

CPU profiling starts after the warm-up, so `cpu.pprof` covers the measured
window plus its immediate host bookkeeping. `alloc-base.pprof` is written
before that window and `alloc.pprof` after it closes. Each allocation profile
first forces a completed garbage collection so the sampled allocation records
include recent allocations even when no automatic collection ran during the
window. These forced collections are outside wall timing, process/thread CPU
measurements, CPU profiling and the `MemStats` delta. Profiling therefore starts
from a freshly collected heap; match the `profiles` setting across comparisons.
The allocation profiles include host census and profiler allocations as well
as the simulation, and are sampled estimates rather than exact `MemStats`
counters. Read their difference:

```
go tool pprof -base OUT/alloc-base.pprof OUT/alloc.pprof
go tool pprof -top -cum OUT/cpu.pprof
```

`--benchmark-profiles=false` skips all three profiles and those forced
collections. Process CPU measurements and `MemStats` deltas still run.

The process CPU clock covers the entire measured loop, including concurrent GC,
profiler workers, interior census samples and timing bookkeeping. Its
`ms_per_tick` is the process CPU delta divided by the number of completed ticks.
Thread CPU sampling is disabled by default: its clock reads can materially
perturb the workload, particularly on Darwin. `--thread-cpu-timing=true` enables
it for separate diagnostic runs. That clock brackets each tick on one pinned
OS thread. It includes the host clocks and synchronous GC assists on that
thread, excludes time when that thread is descheduled, and excludes concurrent
GC or profiler work on other threads. It also excludes between-tick census work.
The default process-only run neither pins the thread nor samples a per-tick CPU
clock. Use process CPU with thread sampling and profiling disabled for primary
comparisons; optional thread distributions measure a different host workload.
CPU clocks are supported on Darwin and Linux. Disabled thread timing, other
hosts and failed clock reads report `available: false` with a reason and omit
the numeric measurement; a failed thread sample discards the whole thread series.

## Per-phase attribution

`Session.PhaseObserver` is a host-side boundary notification called once as each
of the twelve phases completes, carrying the phase name and the tick label. The
session never reads a clock; the **host** stamps the time
`[I6]`. The observer draws no random number and writes no authoritative field,
so a run with it installed produces the same tick sequence as one without —
matching partial fingerprints provide regression evidence for that contract.
`--phase-timing=false` leaves it nil, and the nil test is the whole cost when unset.

Everything after the last phase boundary — the sharing tail, result evaluation,
publication and the once-per-pump executor tail — is attributed to a final row
named `tail-sharing-result-publication`.

Wall-clock reads, and two additional thread CPU reads when enabled, add
measurement overhead against a tick measured in milliseconds. Match the
phase-timing and thread CPU settings across the runs being compared.

## Reading `sim-bench.json`

- `runtime` — Go version, platform, CPU count and the source revision. A binary
  built inside a git worktree carries no VCS stamp of its own, so `tools/sim-bench`
  passes `NANOLATHE_BENCH_REVISION`; set it yourself when running a prebuilt
  binary directly, or the field is empty.
- `scene` — version, map, terrain size, the chosen battle centre and its
  flatness score, army size and scaled mobile roster, and each army's placement
  and counts.
- `initial_fingerprint` / `warm_fingerprint` / `final_fingerprint` — the
  versioned partial state fingerprint before fixture armies and relocation are
  applied, at window open and at window close, plus the two RNG draw totals.
  Two runs of one binary at one seed must agree on all three; if they do not, the run is not comparable and
  the divergence is the finding.
- `ms_per_tick_p50/p95/p99/max`, `ticks_per_second`, `tick_ms` — the window's
  timings, and the whole per-tick series for a distribution or a stall hunt.
- `process_cpu` / `thread_cpu` — availability, CPU milliseconds and CPU
  milliseconds per tick. Process CPU includes every thread across the window;
  thread CPU sums only the individual tick intervals on the pinned host thread.
  `tick_thread_cpu_ms` parallels `tick_ms` when thread CPU is available.
  `thread_cpu_timing` records whether the diagnostic sampling was requested;
  otherwise `thread_cpu.unavailable_reason` is `disabled`.
- `phases` — millseconds per tick and share of the window per phase.
- `gc` — collections, pause total, and allocated bytes and objects, every one a
  **delta between two `MemStats` reads** taken as the window opens and closes.
  The heap figures are instantaneous gauges. `gc_cpu_fraction_lifetime` is
  the exception its name declares: the runtime keeps that fraction over the
  whole process and it cannot be differenced, so it is not the window's GC
  share — read that from `runtime.gcBgMarkWorker` in `cpu.pprof`.
- `tick_restamps` / `restamps` — a per-tick series parallel to `tick_ms`
  counting end-to-end movement class-layer rebuilds
  (`movement.(*System).ClassLayerFullStamps`, a plain counter the host samples
  between ticks; it allocates nothing, reads no clock and the simulation never
  sees it), and a summary relating them to the slow ticks. A full rebuild
  reclassifies every attribute cell of a layer, so it is the first thing to
  check when the p95 leaves the p50 behind: the summary says how many ticks
  over the threshold carried one and whether any rebuild landed in a fast tick.
- `census` — samples at window open, five interior points and window close.
  Each carries live units, in-flight projectiles, active effects, fragments,
  strip objects, feature count and how many are burning, published events,
  build progressions and cumulative deaths; and per army the live count split
  into mobiles and buildings, how many are moving, how many hold a path
  request, nanoframes under construction, order-queue depth split into attack,
  move and build intents, factory production and queue depth, kills, losses,
  both stocks, and the AI manager's ten task-group member counts and its task
  deadlines.

The census is the evidence that the run measured what it claims. A window whose
armies are idle, whose teams never met, or whose factories are all starved is
not this benchmark's workload, and its timings mean something else.

## Comparing runs

The [September 2026 speed experiment](SIM_SPEED_EXPERIMENT.md) records a
1,006-unit comparison across all three reserved modes, including loaded-host
CPU limitations and allocation measurements.

Compare the same scene version, army size and roster, map, seed, effective unit
limit, runtime worker count, window, profiling, phase-timing and thread CPU
sampling settings, from binaries built before the runs were queued. Prefer a quiet machine. When other
work is running, alternate the two binaries under similar load and report both
process CPU per tick alongside wall time. Compare thread CPU distributions only
between runs that both opted into that additional instrumentation.
CPU clocks remove scheduling waits, but contention for caches, memory bandwidth
and CPU frequency still affects the work; they do not make a loaded machine
identical to a quiet one. Report the median, p95 and max together with the
census, never a median alone: this scene's cost falls as units die, so a run that
killed more units looks faster.

A change that alters `final_fingerprint` altered the simulation, not only its
speed. That is a behaviour change and needs justifying as one, separately from
any timing it produced.

This is an opt-in development and regression probe, not a CI threshold. Keep
baseline outputs outside the repository.

## Go 1.27.1 upgrade comparison (2026-09-29)

Go 1.25.0 and Go 1.27.1 ran the same engine source and reference assets on
Darwin/arm64 (Apple M3 Pro), with twelve logical CPUs and `GOMAXPROCS=2`.
Both binaries were built with `-trimpath`, without extra compiler experiments or runtime tuning.
The simulation source was revision `020d2100`; later integrated settings and
renderer changes were included in the separate
[live renderer comparison](BATTLE_BENCHMARK.md#go-1271-upgrade-comparison-2026-09-29).
No gameplay implementation or engine dependency version changed for this upgrade.

Each run used the default 250-unit armies, seed 7, the mode's default unit
limit, 1,200 warm-up ticks and 3,000 measured ticks. Profiles, phase timing and
thread CPU sampling were disabled. Prebuilt binaries alternated old/new,
then new/old, through the ordinary benchmark lock. Modern had three runs per
compiler; Strict and Community had eight each after early host variability
prompted additional samples. All samples are retained. Outputs are outside
the repository in `/private/tmp/nanolathe-go127-perf/`, under
`sim-MODE-go125-N` and `sim-MODE-go127-N`.

Values below are medians across runs, including the median of each run's
maximum tick time. Each pair reads **Go 1.25.0 → Go 1.27.1**; timings are ms.

| Mode | Process CPU/tick | Wall p50 | Wall p95 | Run maximum | Allocated bytes/tick |
|---|---:|---:|---:|---:|---:|
| Modern | 1.877 → 1.812 | 1.744 → 1.665 | 2.765 → 2.705 | 8.934 → 8.740 | 86,428 → 86,428 |
| Strict 3.1 | 1.210 → 1.101 | 1.144 → 1.041 | 1.577 → 1.498 | 4.555 → 4.594 | 19,871 → 19,871 |
| Community 3.9 | 1.591 → 1.447 | 1.433 → 1.290 | 2.407 → 2.299 | 9.323 → 9.310 | 16,721 → 16,721 |

The median within-pair CPU changes were −3.3%, −5.7% and −5.9%, respectively.
Host drift affects the medians across runs, so these observations do not establish
a universal speedup. The small Strict maximum difference did not accompany a
median or p95 slowdown; isolated maxima remained noisy. No sustained regression
was observed, and allocation rates were unchanged within measurement noise.

Every run within a mode had identical scene metadata, catalog hash, all three
partial fingerprints, both RNG draw counts and all seven census samples.
The measured windows began with about 750 live units, active combat, factory
construction and more than 6,200 features. Equality of the partial fingerprint
alone is not a whole-state equivalence claim; the census and ordinary contract
gates remain necessary.

To reproduce a sample with each prebuilt compiler-specific binary:

```sh
GOMAXPROCS=2 /tmp/nanolathe-headless --sim-benchmark=/tmp/go-comparison \
  --root="$HOME/TotalAnnihilation" --gameplay=modern --seed=7 \
  --warmup-ticks=1200 --benchmark-ticks=3000 --benchmark-profiles=false \
  --phase-timing=false --thread-cpu-timing=false
```

## Path and firing-preview storage comparison (2026-10-14)

Revision `92bbe87df` reduces temporary storage in two simulation paths. Route
straightening and smoothing copy only an eligible completed route, instead of
reserving maximum-size arrays in every search. The combat service reuses a
candidate firing-position frame, saving and restoring it across nested rule
callbacks. Both changes preserve existing decisions, work budgets and RNG use.
The storage contracts are in
[DESIGN_MOVEMENT_PATH §2](DESIGN_MOVEMENT_PATH.md#2-packages-and-key-types) and
[DESIGN_UNITS_ORDERS_COB](DESIGN_UNITS_ORDERS_COB.md#modern-firing-positions).

One short native pair compared `5c8b02934` then `92bbe87df`, using Go 1.27.1 on
Darwin/arm64, twelve logical CPUs and `GOMAXPROCS=2`. Both binaries used the same
build settings. The Modern scene used the default three 250-unit armies, seed 7,
1,200 warm-up ticks and 300 measured ticks. Profiles, phase timing and thread CPU
sampling were disabled. Other verification gates ran on the host. Each command
had a 55-second deadline including lock wait; full sweeps were stopped at the
maintainer's request to release the shared host. Raw reports are outside the
repository in `/private/tmp/nanolathe-tick-quick-before/` and
`/private/tmp/nanolathe-tick-quick-after/`.

| Metric | Before | After |
|---|---:|---:|
| Process CPU/tick (ms) | 4.911 | 4.648 |
| Wall mean (ms) | 7.973 | 10.069 |
| Wall p50 (ms) | 5.376 | 6.282 |
| Wall p95 (ms) | 14.081 | 30.146 |
| Wall maximum (ms) | 157.543 | 134.914 |
| Allocated bytes/tick | 132,709 | 108,069 |
| Allocated objects/tick | 972.65 | 995.11 |

Allocated bytes fell **18.6%**. Object count rose **2.3%**, as completed routes
now allocate independent small point arrays. CPU time fell **5.4%** in this one
pair, while wall latency was mixed and mostly worse. This loaded-host sample
establishes the allocation reduction for this scene; it does not establish a
repeatable tick-latency improvement, sustained-combat performance or a browser
speedup.

The pair matched scene metadata, catalog hash, all three partial fingerprints,
both RNG draw counts, every census sample and tick restamp. It began with 748
live units, 84 projectiles and 6,211 features, and ended with 719 live units,
36 projectiles and 6,223 features. All twelve factories were building at both
ends. The final partial fingerprint was `partial-v1:c48d1840a767b6ce`; partial
fingerprints alone are not whole-state equivalence evidence.

Before cancellation, 522 of the path sweep's 543 case/mode/size combinations
completed against the path-only revision `9e57c7510`; all matched baseline
inputs and outcomes exactly. The combined revision passed `tools/check`, native
retail tests and amd64 fingerprint locks. Focused path and firing-position
contracts also passed under js/wasm. The remaining path cases, the remainder of
the whole-tree retail gate (stopped during js/wasm fingerprint locks), repeated
sustained comparisons and browser performance measurements are deferred.
After integrating main, focused short tests passed for path, movement, combat,
orders and documentation; the deferred whole-tree gates were not restarted.
