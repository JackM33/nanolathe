# Browser host

The experimental host compiles the ordinary Go/Ebitengine game and supports a
demo entry point alongside local retail-folder import. The browser host
contract and release boundaries are in
[DESIGN_BROWSER_HOST](../docs/DESIGN_BROWSER_HOST.md).

This is an experimental desktop Chromium build. Safari, Firefox and mobile
have not been accepted. Node 24 and Python 3 run the host checks; the normal Go
toolchain builds Wasm.

Build and preview:

```sh
tools/browser-check
tools/browser-build
tools/browser-serve --demo-root "$HOME/TotalAnnihilationDemo"
```

Open http://127.0.0.1:8766 and choose **Try the demo**. No folder picker is needed.
The loopback server exposes only the original demo archive and readme from the
explicit external directory. The extracted demo contains three campaign missions
and no skirmish maps; the button starts mission zero. To use the menus, choose
Game menus under Graphics and diagnostics before restarting.

**Choose game folder** and folder drop also accept a retail installation or an
extracted demo. Imports remain in the browser; there is no upload. The retail
source keeps full skirmish/campaign menus. **Back to launcher** retires the
runtime so another source can be selected. **Run locally** links to the current
native installation page. Click the game to focus it; losing focus pauses play.

Build a static local artifact with the demo included:

```sh
tools/browser-build --out /tmp/nanolathe-browser-demo --demo-root "$HOME/TotalAnnihilationDemo"
tools/browser-serve --directory /tmp/nanolathe-browser-demo
```

The default artifact is asset-free. Generated output under `build/browser` is
ignored; no original assets are committed. Public demo hosting is the
website's decision and pipeline, recorded in DESIGN_BROWSER_HOST §3 and §6.

## Online play

With an imported retail folder, **MULTI** plays online through the relay's
WebSocket (`relay.nanolathe.gg` by default). To try it against a relay on this
computer, start one from the same checkout and enter its loopback URL in the
browser's **Server** popup:

```sh
go run ./cmd/nanolathe-server --websocket --insecure-loopback --listen 127.0.0.1:39032
# Server: ws://127.0.0.1:39032/relay
```

A native client joins the same rooms through its own MULTI screen with the
same server. Ctrl+V and Command+V paste a room code, and the lobby's **Copy**
writes it to the clipboard. A hidden or backgrounded tab keeps its online
game in step (DESIGN_BROWSER_HOST §4 contract 10).

## Online acceptance — 2026-10-09

Headless desktop Chrome 154 on macOS with an imported retail folder played
three online skirmishes with a native seat (an in-process shell driving the
ordinary MULTI screen, lobby and paced driver) through a loopback
`nanolathe-server --websocket --insecure-loopback` relay at protocol 6:

- native host, browser joiner on Ashap Plateau: the code pasted with
  Command+V, Copy returned it, 4 minutes and 7,245 ticks, the tab hidden for
  44 seconds mid-battle; both seats stopped at tick 7,245 when the native
  seat left;
- native host, browser joiner, the tab hidden before the host pressed Start
  and again for six and a half minutes: the browser entered the battle in the
  background, and the native summary reports 15,256 ticks in 8 minutes
  28 seconds, average and worst 30.0 ticks/s, no stalls;
- browser host on Acid Foursome, the native seat joining with the code Copy
  wrote, the tab hidden for 32 seconds: 2,249 ticks, one opening stall.

Every `+net` capture on both seats showed "Checksums agreed through tick N"
within a second of the current tick, and the relay logged no checksum
failure. Hiding a headless tab needs focus emulation turned off first: with
it on, Chrome keeps a background tab visible. The one stall a seat saw, 0.6–
0.8 s at tick 32, came when a visible browser entered the battle and drew
its first frames.

## Publishing

Every push to `main` whose browser, hygiene and simulation CI jobs pass runs
the `browser-publish` job: it packages the asset-free `tools/browser-build`
output as `nanolathe-browser.tar.gz` and `tools/browser-publish` publishes it
with `build.json` and `SHA256SUMS` as the prerelease `browser-<run number>`,
then sends the website repository a `browser-build` dispatch when the
`WEBSITE_DISPATCH_TOKEN` secret is configured (without it the website checks
every six hours). The website's `scripts/fetch-play.py` takes the highest run
number whose three assets are present, verifies the build and publishes it at
https://nanolathe.gg/play/ together with the demo assets it hosts itself.
The repository's releases are immutable: a published release's assets and tag
are locked and its tag name is never reusable, so each run gets its own
release (created as a draft, published once its assets are uploaded) instead
of a rolling tag, and the publish step prunes every other `browser-*` release
and tag except the newest two. Nothing in this repository's releases contains
game content.

The current 45.7 MiB Wasm binary compresses to 11.0 MiB gzip; the launcher
fetches the gzip variant and decompresses it in the page. Demo assets total
19.5 MiB. Wasm and the matching Go runtime have content-hashed filenames. The index
also selects a hashed host directory, pinning scripts, the child page and its
build manifest across restarts. Publish new files before replacing the index;
retain prior hashed files for already-open launchers. Demo
files are SHA-256-verified before mounting and cached for the next visit; cached
bytes are verified again. Serve static host files/manifests with revalidation,
hashed assets with immutable caching, Wasm as application/wasm, and its `.gz`
variant using Content-Encoding: gzip / Vary: Accept-Encoding. Use HTTPS publicly.

Settings and saves are separate for demo and retail. The retail database keeps
existing prototype saves. A Web Lock permits one game at a time per origin, so
several tabs cannot overwrite each other's snapshots. Storage belongs to the
exact address/port; clearing site data removes it. Retail files need selecting
again after a full reload. Demo play can reuse the verified asset cache.

## Diagnostics

Expand Graphics and diagnostics to choose renderer, memory and entry mode.
Changes apply on restart. The 750-unit stress fixture requires full retail
Town & Country assets; it is unavailable for the demo. It reuses the existing
three-army fixture with seed 7, not a new gameplay scenario.

Measure 20 seconds captures one-second host samples. Keep the game focused;
paused samples are part of the export. Export measurements prepares a download
link. Heap capture forces a collection and exposes the allocation profile;
it is disabled during measurement. These are host diagnostics, not isolated
GPU or authoritative tick benchmarks. Wasm capacity is separate from Go heap
and total browser/GPU memory. Lower memory uses the existing sparse terrain
atlas path and GOGC=50; desktop default retains eager packing and GOGC=100.

## Merge-readiness checks — 2026-10-03

The user lifted the prototype's no-tests constraint for this work. The branch
integrates main `b1e3b5ef` and has been reviewed as a complete browser diff.
`tools/browser-check` passes 33 JavaScript and 3 Python regressions. The
asset-free Wasm build passes and contains no archives or test sources. CI now
runs those host checks, executes the existing Wasm numeric kernel vectors
under Node with `GOMAXPROCS=1`, and builds the asset-free artifact; the remote CI job has not been
run from this local worktree.

The integrated `tools/check` and `tools/check-retail --full` gates pass,
including lint/vet, the full retail corpus, extended campaign/save trajectories,
amd64 fingerprint locks and real-device GPU fixtures. The temporary missing
monitor prerequisite was resolved before the successful final runs. Native
Classic/Modern battle benchmarks and documentation citation checks also pass.

Browser acceptance used desktop in-app Chromium with original assets external
to the repository. The first demo mission reached Victory through ordinary
unit orders, and continuation opened the second mission's briefing and battle.
The rebuilt engine also reached the first mission's result after the allocation
fix. Directory picker import starts a retail Comet Catcher skirmish. A second
tab is refused while the first runtime is active. Fullscreen entry and the
visible Exit fullscreen control work. The two content namespaces retain their
separate save lists. A newly written retail save and its sidecar survived a
complete page reload and folder reselection; the Load dialog restored three
units at tick 730; resuming advanced to tick 3965. The previous prototype save
was preserved.

The campaign soak found repeated PCX background decoding on every briefing
draw. Its profile attributed about 2.2 GiB of accumulated allocation to those
loads; the following battle retained 3.4 GiB of Wasm capacity despite only
148 MiB of live heap after collection. Briefings now retain each side's
immutable background until the content source changes. A focused, cached Arm
briefing capture (640×480, Modern, lower memory, 21 samples over 20.9 seconds)
held 59.7 FPS, peaked at 106 MiB Go heap / 191 MiB Wasm capacity and allocated
0.13 MiB/s. This diagnoses the repeated decode, rather than establishing a
same-workload before/after renderer benchmark.

Native performance acceptance used the M3 Pro host, Go 1.27.1 and two runtime
workers. Both renderers used the scene-version-5 Expanded Confluence fixture,
seed 7, normal visibility, 300 pre-window ticks, 60 warmup draws and 180 measured
draws at 30 Hz. Scene metadata and every frame's census match. The captures show
land, naval and airborne combat; eight factories remain active, 201–238 units
move and natural feature fire appears during measurement.

| Native renderer | Mean host draw work | p95 host draw work | Mean cadence | Allocation per frame |
| --- | ---: | ---: | ---: | ---: |
| Classic | 23.70 ms | 26.73 ms | 33.34 ms | 3.59 MB |
| Modern | 12.72 ms | 15.67 ms | 33.34 ms | 0.74 MB |

These are candidate measurements, not a before/after regression comparison.
Host work excludes the intentional pacing sleep; GPU execution and display
scanout timing are unavailable. They establish native play activity and budget,
not equivalent browser performance. Browser captures above remain exploratory.

The maintainer confirmed that a real TotalA folder drag works and that the intro
video plays sound. The batched directory walker and failure behavior also have
automated regressions; the directory picker is independently verified. Battle
sound audibility, multi-hour matches, completion of all three demo missions,
Safari, Firefox and mobile remain follow-ups for the initial experimental
Chromium scope. Public demo distribution still needs the evidence in
DESIGN_BROWSER_HOST §3; public hosting has not occurred. Raw captures and profiles stay outside
the repository.

## Demo acceptance — 2026-10-03

Demo-only loading, verified-cache reload, both renderers, engine restart and
retail-folder source switching were exercised in the in-app Chromium browser.
A demo save and its sidecar survived a complete page reload and were restored
through the in-game Load dialog at tick 1640. The retail source still lists the
previous prototype BROWSE save and settings; neither appears in demo storage.
Restart's lock-release race was reproduced and fixed. The browser-only full
paused composition kept the save dialog complete across successive name edits.
A static demo artifact was built and inspected: only the original archive and
readme were included. Public hosting was not performed. OS folder drag, complete mission progression, long sessions and
other browsers remained unchecked at the prototype stage. Its no-tests
constraint was lifted for the merge-readiness work above.

Exploratory 20-second focused captures, first demo mission, lower memory,
800×600 logical surface, 1280×720 browser viewport, benchmark lock held:

| Renderer | Mean FPS | Mean ticks/s | Peak Go heap | Peak Wasm capacity | Ticks |
|---|---:|---:|---:|---:|---|
| Modern | 60.0 | 30.0 | 160 MiB | 200 MiB | 1907–2511 |
| Classic | 30.0 | 30.0 | 130 MiB | 166 MiB | 2294–2902 |

These windows use different default mission RNG seeds and tick windows; they
establish playable throughput for this small scene, not a same-workload renderer
comparison or a full retail memory budget. The captures preceded the browser-only
paused-composition fallback; running composition is unchanged. The initial Modern
capture also includes two paused samples and averages 27.2 ticks/s; its raw data
is retained with the steady capture rather than presented as focused throughput.
Measurements and screenshots remain outside the repository.

## Initial measurements

Measured in the Codex in-app Chromium browser on this Mac on 2026-10-02,
800×600 logical pixels, Modern gameplay, retail assets, no remaster synthesis.
Each row covers roughly 20 seconds. The host benchmark lock excluded other
Nanolathe benchmarks while sampling.

| Scene | Renderer | Mean FPS | Mean simulation ticks/s | Peak sampled Go heap |
|---|---|---:|---:|---:|
| Menu-started Acid Foursome, 3–5 units | Modern | 59.9 | 30.0 | 652 MiB |
| Town & Country, 756–758 units | Modern | 34.3 | 30.0 | 1,513 MiB |
| Town & Country, 765–757 units | Classic | 16.3 | 30.0 | 1,348 MiB |

Stress windows were ticks 457–1040 (Modern) and 308–896 (Classic). These are
exploratory samples, not a controlled same-tick renderer regression comparison.
Both scenes were inspected visually. Menu navigation, skirmish entry, commander
selection and move commands were exercised manually. Folder picker import was
exercised; the OS drag-and-drop gesture remains a manual check.

The initial 46 MiB Wasm module is uncompressed. Opening the archives took about
0.12 seconds; direct skirmish startup took about 4.6 seconds and stress startup
about 6.2 seconds in these runs, excluding Wasm download/compilation. The menu
preview adds another battle's allocation footprint before entering a skirmish.

## Memory comparison — 2026-10-03

Same Wasm binary, Town & Country stress fixture, seed 7, Modern gameplay,
800×600 logical surface and 1280×720 browser viewport. Sequential captures
held the host benchmark lock. These short windows start within 14 ticks of
one another; they compare similar workloads, not identical per-tick rendering.

| Renderer | Memory preset | Mean FPS | Mean ticks/s | Peak Go heap | Peak Wasm capacity | Tick window |
|---|---|---:|---:|---:|---:|---|
| Modern | Desktop default | 43.9 | 30.0 | 1,416 MiB | 1,470 MiB | 756–1364 |
| Modern | Lower memory | 42.7 | 30.0 | 1,015 MiB | 1,087 MiB | 769–1377 |
| Classic | Desktop default | 28.6 | 30.0 | 1,223 MiB | 1,320 MiB | 770–1379 |
| Classic | Lower memory | 28.5 | 30.0 | 1,098 MiB | 1,170 MiB | 766–1376 |

Each capture contains 21 samples over about 20.3 seconds, with 758–759 units
at its first sample and 757 at its last. Modern's peak heap fell about 28%,
with a 2.7% lower mean frame rate in this pair; this is one exploratory pair,
not a statistically established frame-time regression. Classic's heap fell
about 10% while its frame rate remained about the same. Both held 30 ticks/s.

The stress heap profile identified decoded GAF animation frames and eager
terrain atlases as large allocation groups. Sparse atlases address the latter;
the animation footprint, camera-jump upload costs and long-match memory growth
remain follow-up work. These measurements do not establish a mobile memory
budget or total browser-process memory use. Profiles, JSON and captures remain
outside the repository.

## Frame-rate pass — 2026-10-04

Chrome 154 on the M3 Pro (120 Hz display, ANGLE Metal), visible and focused,
Town & Country stress fixture, seed 7, lower memory, 800×600 logical surface,
1400×727 game view. Each row is a 20-second window from tick ~830 with 757
units, captured through the launcher's own samples plus a rAF-interval
recorder in the game frame. Runs alternated between builds; rows of one
build agree within about 1.5 frames per second. "Before" Modern rows include the
candidate build with its pre-record switched back on.

| Build | Renderer | Mean FPS | Median frame | p90 frame | Allocation | GCs / 20 s |
|---|---|---:|---:|---:|---:|---:|
| Before | Modern | 46.5–47.1 | 24.8–24.9 ms | 32.1–33.3 ms | 7.6–7.8 MiB/s | 1 |
| Frame recorded in its own Draw | Modern | 51.8–53.3 | 16.7 ms | 25.1 ms | 7.6–8.1 MiB/s | 1 |
| + audio frames, Classic body planes | Modern | 54.4 | 16.7 ms | 25.0–25.1 ms | 8.0–8.3 MiB/s | 0–1 |
| Before | Classic | 28.9–29.2 | 16.4–16.6 ms | 32.8–33.3 ms | 45.5–46.0 MiB/s | 3 |
| + audio frames, Classic body planes | Classic | 29.7 | 16.3–16.4 ms | 26.9 ms | 14.2–14.3 MiB/s | 1 |

Chrome CPU profiles showed the page thread saturated (under 1% idle) in Modern:
about 30% recording, 24% simulation, 20% model placement and submission and,
before the change, 6% mixing audio, which native builds do on another thread.
The skipped pre-records had missed their predicted fraction on 22% of frames.
Classic now has about 4% idle time at its 30 FPS presentation rate.

Every Go collection paused the page for one long task: `GODEBUG=gctrace=1` showed
224 and 176 ms of mark wall time with about 900 MB live. With the map catalog no
longer holding parsed OTA documents the same marks took 109 and 117 ms with about
840 MB live. The demo mission keeps about 220 MB in the heap and allocates about
0.3 MiB/s, so it collects every few minutes.

Native battle benchmark (version 2, scene 5, seed 7, two runtime workers),
alternating base and candidate. Censuses match. The first two Classic
candidates predate the OTA change; the third Classic pair and both Modern pairs
include every change.

| Renderer | Build | Mean DrawWork | Allocation per frame | Heap after GC | Live heap objects |
|---|---|---:|---:|---:|---:|
| Classic | Before | 24.64 / 24.72 / 27.23 ms | 3.59 MB | 484 MiB | 1.56 M |
| Classic | Candidate | 23.69 / 23.90 / 26.45 ms | 1.01 MB | 485 / 485 / 408 MiB | 1.56 / 1.56 / 0.47 M |
| Modern | Before | 12.23 / 11.66 ms | 0.75–0.78 MB | 554–561 MiB | 1.60 M |
| Modern | Candidate | 11.89 / 11.76 ms | 0.74 MB | 482–490 MiB | 0.52 M |

The third Classic pair ran while the host was busier; it is compared only with
its own partner. The audio reader's benchmark fell from 202 to 90 µs per second
of panned PCM natively and from 918 to 429 µs under Node's Wasm. Profiles,
traces and captures remain outside the repository.

## Feature-animation storage — 2026-10-15

Feature sprites now load the same validated, fully decoded banks without the
extra encoded RLE views used by direct-storage consumers. A setup-only inventory
of the Town & Country fixture found 98.2 MiB of these unused byte planes, plus
their frame headers. All feature entries and event pixels remain prepared
before drawing. Model/projectile textures, visibility masks and on-demand
effect loading retain their existing direct views.

Two alternating captures per build used headless desktop Chromium 155 on the
macOS arm64 host, Go 1.27.1, Modern rendering/gameplay, sparse terrain and
GOGC=50, 800×600 logical pixels and a 1400×900 browser viewport. The Town &
Country fixture had three 250-unit armies and seed 7. The minimap camera was
moved to the fight before each window; capture images were inspected. Each
focused window lasted 35 seconds, beginning around tick 750 (the exported
one-second samples span ticks 781–1840 across the four runs).

| Browser measurement | Before | Decoded-only feature banks |
| --- | ---: | ---: |
| Mean composed FPS | 59.73–59.81 | 59.41–59.60 |
| Mean simulation ticks/s | 30.00 | 30.00–30.01 |
| Go heap after a post-window forced collection | 730–789 MiB | 621–623 MiB |
| Peak sampled Wasm capacity | 990–1,043 MiB | 835–845 MiB |
| Mean host Draw work | 7.80–8.31 ms | 7.54–7.74 ms |

GC tracing in the second pair measured 123 ms of marking in the post-window
forced collection before, and 89 ms after. This is one diagnostic pair, outside
the measured frame window. The short browser runs establish the memory
reduction and near-cap throughput; they do not establish a sustained FPS gain
or eliminate long collection tasks. Collection timing also changes which
temporary buffers survive, so total heap differences exceed the omitted byte
planes in some runs. Go heap and Wasm capacity exclude browser/GPU memory.

Native acceptance used the version-2, scene-5 Expanded Confluence benchmark,
seed 7, 1920×1080, two runtime workers, 300 pre-window ticks, 60 warmup draws
and 180 measured draws at 30 Hz, with remaster synthesis disabled. Every
per-frame census and all start/end simulation snapshots match between builds;
both renderers' final PNGs are byte-identical and were visually inspected.

| Native renderer | Mean host Draw work, before → after | p95, before → after |
| --- | ---: | ---: |
| Classic | 23.42 → 24.02 ms | 29.42 → 30.15 ms |
| Modern | 13.09 → 13.47 ms | 16.53 → 16.81 ms |

These single native pairs show similar steady work rather than a measured
native speedup. The change removes loading allocation and retained storage;
it adds no work to frame composition. Raw profiles, JSON and captures remain
outside the repository.

## Camera gestures

In Enhanced rendering, two-finger trackpad scrolling pans the camera and
pinch/spread zooms using the existing Smooth/Steps preference. On touchscreens,
dragging two fingers pans and changing their distance zooms. Gesture controls
respect the existing battle viewport, UI ownership and focus gates. Single
touches do not issue unit commands; this is camera support, not full mobile
playability.

Browsers expose wheel units rather than device identity. Pixel-mode wheel
events pan, including mouse wheels reported in pixels; line/page wheel events
keep wheel zoom. Ctrl-wheel follows the pinch path and suppresses browser page
zoom over the canvas. Browser scrolling may include inertia because DOM events
do not expose macOS momentum phases. Safari/Firefox and mobile performance
acceptance remain open (DESIGN_BROWSER_HOST §6).

Verified on 2026-10-04 in the in-app Chromium browser with the original demo:
pixel scrolling moved the battle view on both axes; Ctrl-wheel spread reached
detail zoom and pinch returned to the native lock while launcher page scale
stayed fixed. Camera/GUI ownership, touch geometry, idle-poll pointer retention,
batched pinch anchors and cancellation pass the authored regressions. Physical
touchscreen acceptance remains unverified. Captures are outside the repository.

## Persistent browser files

Settings now live at `/settings/settings.json` and saves at `/saves`, using the
engine's existing `--save-dir` host override. Their bytes and directories are
stored in IndexedDB at this browser origin. Files are committed on close or
fsync; renames replace records in one transaction. Browser storage errors are
returned to Go as filesystem errors. The imported `/game` tree is read-only
through every mutation path; `/tmp` stays session-local.

The **Browser settings and saved games** panel lists committed files and offers
individual downloads, including the save's Nanolathe sidecar. Keep the sidecar
beside its `.SAV` if moving a save to the desktop. Use the in-game menus for save
and load. A skirmish save, a full page reload, importing the folder again, and
loading the saved tick were exercised manually. A changed damage-bar preference
also survived the reload. Browser storage belongs to the exact address/port;
clearing site data removes it. Retail files still need selecting after a full
page reload, and importing desktop saves/mods has not been implemented.

## Prototype boundaries

- Retail files never go to the server. The browser retains local File/Blob
  handles, serves range reads through a Node-style Go filesystem bridge, and
  caps its small-file/range read cache at 64 MiB. Existing VFS precedence and
  archive decompression remain in Go.
- Go's Wasm runtime uses one CPU thread. Existing goroutines do not provide
  native multicore execution here. Audio uses Ebitengine's browser backend.
- Settings and single-player saves survive restart/reload in this browser.
  Persistent installation access, mods, mobile browsers and long-match stability
  have not been validated. Saves still use the engine's existing retail codec;
  browser persistence adds no new simulation/save semantics.
- The browser benchmark lock stub exists only to let the command package
  compile. Browser telemetry stays outside the authoritative tick.
- Generated browser output is ignored. The build script creates content-hashed
  Wasm and its matching Go runtime; no original assets or new dependencies are
  committed.
