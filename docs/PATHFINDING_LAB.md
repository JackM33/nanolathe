# Pathfinding laboratory

The laboratory measures ground movement against **recorded games**. It reads
multiplayer recordings, finds where units were held up, cuts those moments out
as *snippets*, replays each snippet in Nanolathe under any registered rule
set, and scores the recording and every replay with one scorer. It exists to
answer one question about a movement policy: *would the games people actually
played have gone better with it?*

It is an opt-in research tool. Recorded-game experiments stay outside CI;
small authored contract tests check the harness. No rule set it
registers is reachable from the game, and nothing it measures is evidence of
retail executable behaviour: the recordings are observations of retail and
patched engines, and the replays are Nanolathe.

Modern's ground movement was chosen with it:
[Modern traffic](DESIGN_MOVEMENT_PATH.md#modern-traffic), adopted on
2026-09-29, is the laboratory's fourth candidate, and that design section
carries the measurements it was adopted on. What Modern was before is the
laboratory's **baseline**, the rule set `lab-overlap`
([The baseline](#the-baseline)).

The opt-in [path benchmark](PATH_BENCHMARK.md) remains the tool for authored
scenes and for tick cost. The two disagree in places, and the difference is
informative: see [Reading the two together](#reading-the-two-together).

## Data and privacy

Recordings are `.tad` files and their patched-engine variants
[fmt tad](../research/formats/tad.md). They hold player names, chat and
network addresses. **Recordings, extracts, snippets and replay logs are local
research data: never commit them**, and never quote a player name or a chat
line in a report, a commit or a test. Tests use authored fixtures only.
Everything the laboratory writes belongs outside the repository.

A recording is telemetry, not an input log. The part the laboratory reads is
the unit-sync stream [fmt tad §7]:

- a ground unit's movement entry carries its blocked bit and the first three
  points of the route it holds;
- a scheduled status carries its exact position, heading and speed, once every
  `maxUnits` ticks.

Two properties of that stream make reconstruction possible
[04 R-PATH-01 §8][04 R-MOV-01 §3]:

- A published route holds cell anchors, which lie on a 16-unit lattice offset
  by half the footprint. A two-point entry whose points are *off* that lattice
  is the goal installer's straight-line fallback — the unit's exact position
  and the goal point — so it marks **an order being activated**, and gives a
  position fix and the goal for free.
- A route whose first point equals the previous route's second point is a
  waypoint being consumed: the unit was within five world units of it on that
  tick.

Reconstructed positions were checked against the scheduled statuses: the
median error is 0.9 world units for a unit at rest, about 4.5 moving and about
10 while held; nine in ten are within one cell.

## Tools

| Tool | What it does |
|---|---|
| `tools/tad-extract -moves` | Decodes recordings into movement extracts: per unit, the entries, poses, and damage and fire buckets. |
| `tools/path-lab mine` | Builds per-unit timelines, orders (*episodes*), blocked spans with their likely blocker, and group orders (*cohorts*); writes tallies and records. |
| `tools/path-lab pick` | Chooses snippets from the mined records, by class, on maps that have been exported. |
| `tools/path-lab snippet` | Cuts one snippet by hand. |
| `cmd/nanolathe-pathlab map` | Exports a map's passability grid, and optionally its picture. |
| `cmd/nanolathe-pathlab replay` | Replays snippets under rule sets and writes logs in the extract's shape. |
| `tools/path-lab score` | Scores the recording and every replay of a snippet. |
| `tools/path-lab-render` | Draws tracks, contact sheets, videos and an interactive player. |

`internal/pathlab` holds the replay; it is the only part that links the
engine. `tools/path-lab` reads and writes files and links nothing.

## Classification

A *blocked span* is a run of ticks during which a moving unit's blocked bit is
set. The miner names each span by the unit most likely to have refused the
step — the nearest unit within reach, preferring what lies ahead:

| Class | The blocker is |
|---|---|
| `same-way` | a friendly mover heading within 60° of the unit |
| `head-on` | a friendly mover heading at least 120° away |
| `crossing` | a friendly mover in between |
| `parked-friend` | a friendly mobile unit at rest |
| `structure` | a building or a frame under construction |
| `other-player` | any unit of another player |
| `static` | nothing near: ground, a feature, or the map edge |

A same-way span is followed from blocker to blocker to **what held the front
of the queue** (`root`): the first block in the chain whose blocker is not a
held friend going the same way, or `free-leader` when the unit ahead was
moving freely.

Other records carry what the classes cannot: the delay from an order to its
first route, how long a group took to settle at its destination, the length
travelled against the shortest path over the map's static grid, and the free
half-width of the ground where a unit was held.

## Snippets and replays

A snippet is a window of one recording: the units and structures in a region,
the orders given in the window, and the group commands those orders came
from. `subject` units are the ones scored; the rest are traffic.

A replay composes a battle on the snippet's map, stages every unit where the
recording had it, and issues the recorded orders at their ticks. The details
that turned out to matter, each found by looking at rendered tracks:

- **Group orders go through the command boundary**
  (`Session.StageGroupMove`), so the formation arithmetic and the Modern
  destination slots run as they do for a player. The recorded selection size
  is not in the stream; the replay passes the smallest count consistent with
  the recorded goals.
- **A snippet is staged thirty ticks early**, with each moving unit given the
  order it was already carrying. A unit staged standing is a wall to every
  search, which a unit caught mid-move was not.
- **A battle is warmed up for 64 ticks** before the first order: a fresh
  unit's zero request stamp holds its first request for sixty.
- **Combat is off**: units hold fire, the computer player is passive, and the
  whole map is visible. A snippet measures movement only.
- **Recorded lifetimes keep their identity.** An ID may be reused on the
  same tick its former unit dies. Structures still stage before mobile
  units, but a death names the exact snippet unit that died, even if its
  replacement has already taken the ID. Later orders name the current ID
  owner. The output keeps each lifetime's original ID and birth tick.

`pathlab.TestReplayDeathKeepsRecordedInstanceIdentity` exercises a mobile
unit replaced by a structure on its death tick, with distinct and reused
IDs, and a reused ID whose new mobile unit receives the later order. The
fixture is authored; its retail map and definitions are loaded only in the
opt-in retail test tier.

Crowds are chaotic: a one-unit nudge changes who arrives when. Every rule set
is therefore replayed under several *jitters* — the same snippet with staged
positions perturbed by a few world units — and rule sets are compared by
**paired differences per snippet**, with standard errors.

## Scores

Per subject, against its recorded goal:

| Score | Meaning |
|---|---|
| `trip` | Ticks from the order until the unit has **come to rest for good at its goal**; the span's length when it never does. This is the score a rule set is judged by. |
| `rested` | Whether it came to rest at its goal at all. |
| `near` | Ticks from the order until the unit first comes within 96 world units of its goal. Kept because earlier results were stated in it. |
| `blocked` | Ticks spent with the blocked bit set. |
| `detour` | Length travelled over the static shortest path. |
| `turns`, `stops`, `away` | How the trip looked, a replay only: turning per distance, stops made while holding a route, and the share of the distance moved that led away from the goal. |
| `overlap` | Unit-ticks during which two units' footprints shared a cell, the part of it spent at rest, and the units left overlapping when the window closed. Replays only. |

*At its goal* allows for company: a crowd cannot stand on one point. A unit
sent with `n − 1` others to the same place at the same time is at its goal
within 96 world units plus its footprint times the square root of `n`, the
half-width of the block `n` such units stand in.

*Its goal* is the goal its own engine gave it. A group command gives every
member a goal of its own, and a rule set that places a group differently
sends a unit somewhere other than the recording did: it has arrived when it
rests there. A replay logs the goal of each unit's head order whenever it
changes, and the scorer judges the replay against the last one in the scored
span when that lies within the block the order's whole group stands in round
the recorded goal — 96 world units plus twice the footprint times the square
root of the group's size. A goal farther off is not this order's, and the
recorded goal is used.

The scores misled this project three times, and the reasons are worth
keeping:

- It stops watching six cells from the goal. A rule that sent parked friends
  aside scored seven ticks *better* on `near` while a group that had just
  arrived went on shuffling for a hundred ticks more; on `trip` it is 160 ticks
  worse.
- Its arrival share is partly an artefact of the radius: two in five of the
  units it counts as never arriving ended within twenty cells of their goal,
  parked at the edge of their group.
- Judged against the recorded goal, a quarter of a group of 104 never
  arrived under any rule set. They were standing on the places their replay
  had given them, up to 570 world units from the places the game had.

Read the tail as well as the mean — the median and the ninetieth percentile
of `trip` — since a player notices the stragglers.

Snippets chosen *because* something went wrong in them are selection-biased:
any replay tends to do better than the recording, by regression to the mean.
The classes `group-random`, `group-large`, `trip-random` and `army` are drawn
by lottery without regard to recorded arrival share, arrival time, or
distance actually travelled. Their populations differ:

- `group-random` includes groups of at least six sent at least 300 world
  units; `group-large` raises the size minimum to 32. These are calm
  recording-comparison samples: no member died during its recorded trip,
  and the existing combat measure, summed damage plus shots, is at most
  three per member.
- `trip-random` includes solo move episodes with a known goal at least 800
  world units from their start, no combat and no recorded death. It does
  not require a measured static shortest path: the miner measures those
  only for arrivals, which would select on success.
- `army` includes groups of at least 32 sent at least 300 world units,
  including those under fire or taking losses. Combat-free replays of these
  samples compare rule sets only; they do not reproduce the recording's
  combat conditions.

All four classes use the configured `-max-ticks` window, starting one tick
before the command and shortened only by the recording's end when known.
Zero arrivals and arrivals beyond the window do not remove a candidate or
change its lottery weight. Deliberately selected hard-case groups retain
their existing filters, including 60% recorded arrival, and their window
ending sixty ticks after the last recorded arrival. Blocked and detour
hard-case selection is unchanged.

Mine lottery corpora with `mine -all`: the default miner writes only notable
episodes, so picking from those extracts cannot recover the omitted solo
trips. Regenerate snippets to use the corrected selection; existing snippets
and historical measurements are unchanged. Comparisons *between rule sets*
remain paired on the same snippets.

`TestRandomGroupsDoNotSelectOnArrival`,
`TestRandomGroupsKeepCalmAndArmyBoundaries` and
`TestRandomTripsDoNotRequireArrivalMeasurements` in `tools/path-lab` lock
the lottery populations and windows with authored records.

**Tune on one set, report on another.** A policy's settings are chosen on the
tuning set; the numbers quoted for it come from a held-out set drawn by
lottery that no setting was chosen on.

**Look at it.** No score says whether movement looks right. A fixed viewing
set — moments drawn by lottery from the held-out snippets, and a few named
hard cases — is rendered for every candidate, recorded game beside replays.

## Running it

```sh
# 1. Decode. Extracts go outside the repository.
go run ./tools/tad-extract -moves -out ~/lab/moves ~/ta-demos

# 2. Export the maps the recordings were played on.
go run ./cmd/nanolathe-pathlab map -all -out ~/lab/maps

# 3. Mine and pick.
go run ./tools/path-lab mine -all -moves ~/lab/moves -maps ~/lab/maps -out ~/lab/mined
go run ./tools/path-lab pick -mined ~/lab/mined -out ~/lab/snippets

# 4. Replay and score one snippet under three rule sets, three jitters each.
go run ./cmd/nanolathe-pathlab replay -rules modern,lab-overlap,strict-3.1 -jitters 3 \
    -out ~/lab/logs ~/lab/snippets/<snippet>.json
go run ./tools/path-lab score -snippet ~/lab/snippets/<snippet>.json \
    ~/lab/moves/<recording>.json.gz ~/lab/logs/<snippet>__*.moves.json.gz

# 5. Look at it.
go run ./tools/path-lab-render tracks ...
```

## The baseline

`lab-overlap` is Modern's movement as it stood until 2026-09-29:
`movement.OverlapRules` — allied pass-through, jam release, pocket release
and the staggered re-route throttle, with no traffic policy — and the
straightening kernel. It is what "Modern" names in every measurement made
before that date, this document's and the design documents' included. Every
other laboratory rule set derives from it, so each keeps the meaning it was
measured under. `internal/pathlab.TestTheBaselineIsTheOverlapRules` holds it.

## Traffic policies

`movement.Rules.Traffic` answers a `movement.Traffic` value, asked once a
tick. The zero value is retail's behaviour and is what Strict 3.1, Community
3.9 and the baseline answer. Modern answers the policy it adopted
([Modern traffic](DESIGN_MOVEMENT_PATH.md#modern-traffic)). The laboratory's
sets (`internal/pathlab/labrules`, one list that the replay, the AI arena and
the path benchmark each register) answer other values of the same switches
and numbers: the rungs of the ladder that led from the baseline to the
adopted policy, each as it was measured, and the variants its tuning was
chosen among. None of those is a Modern policy: adopting one means moving it
behind `ModernRules`, documenting it in
[DESIGN_MOVEMENT_PATH](DESIGN_MOVEMENT_PATH.md) as a Nanolathe Modern policy,
and locking both the Modern contract and the Strict bypass with tests
([DESIGN_GAMEPLAY_RULES §9](DESIGN_GAMEPLAY_RULES.md)).

No policy lets a unit enter a held cell. Each one changes what a unit *wants*;
the commit validator is untouched. The design document describes the
policies exactly; the table is the laboratory's summary.

| Policy | What it does |
|---|---|
| Sidestep | A mover looks three cells ahead and, when its footprint could not stand there, wants the nearest heading to either side along which it could. Oncoming friends are passed on the right by both; a crossing friend is passed behind; a same-way friend making way is followed unless it is markedly slower. With `ToWaypoint` it looks no farther than the point its route turns at: a mover that sees the wall beyond a corner before it has reached the corner turns away, comes round and turns away again. With `KeepRound` a mover whose last step was refused keeps the way round it chose on the visits its own way looks clear too: a unit turning where it stands, whose probes start from a position that creeps, otherwise turns a few degrees towards one and then the other and reaches neither. A waypoint the mover is beside and past is taken from it only while it is steering: the first turn of a new route is the way round what holds the unit. |
| Heuristic weight | One weight for every search in place of the load tiers [04 R-PATH-01 §10]; with `BusyScale`, a second and heavier weight while route searching is busy, which is the load tiers the other way round. |
| Route smoothing (`path.SmoothKernel`) | After the search, runs of turns are replaced by straight legs wherever the footprint can stand along the whole leg with a margin. A leg is judged at the anchors a mover holds along it: the commit adds half a cell before it divides [04 R-COLL-01 §1], and a leg judged without the half cell is judged half a cell off. |
| Route claims (`movement.ClaimsPilot`) | Every ground mover claims the ground its route crosses over the next 768 world units, with the way it will cross it, counted afresh every four ticks. A search pays a little for a claim a friend holds along the step's way and six times as much for one against it, and smoothing takes no leg across ground dearer than the stretch it replaces. A slow-turning unit pays only for the claims of other slow-turning units: vehicles keep the direct way, walkers go round. |
| Arrival places (`movement.ArrivePilot`) | Units sent to one exact point on one tick as separate orders are given places in a block round it, and two units near their goals that would cross exchange them. A move first seen after it has begun — the computer player's, or one that waited behind another order — keeps its goal for its place. |
| Settling (`ArrivePilot.Stuck`, `StuckParked`) | A unit near its place that has made no way toward it for twenty ticks, and is kept from it by something that will not move of itself, settles where it stands. |
| Settling short of a sealed goal (`ArrivePilot.Sealed`) | A unit on a plain move that has stood for five seconds far from its place, and whose goal the ground closes off from where it stands, settles there. It is the answer Modern unreachable moves gives the first units to reach the ground nearest such a goal, given to the ones those keep from it. |
| Through (`Traffic.Through`) | A request whose search found nothing, from a unit whose searches have found nothing for five seconds, is searched again with friendly units read as absent: first the ones with somewhere to go, then all. Other players' units, structures, features and ground stay walls, and the commit refuses a held cell as always: the unit follows its route as far as its friends let it. |

**Measured and dropped.** What was built, measured and not chosen is no longer
in the engine: asking an idle friend to stand aside and backing off, making
way by rank, following and keeping off a leader's heels, local detours,
searches that look through friendly movers, the prompt re-order, steering
that waits, damps, looks farther or steers round units only, and six whole
approaches built as prototypes — reciprocal velocity avoidance, lanes along a
shared spine, flow fields, line of sight to the goal, an optimal route search
and cooperative unjamming. The research report records what each measured and
why it was dropped; the branch `research/path-redesign` and main's history
from `9d4fa3e8` to `86c36b6d` keep the code, and the laboratory's tools can
be built from any of those to repeat a measurement. The lesson that recurred:
never slow or hold a unit to avoid a conflict that is only predicted.

## What was adopted

`next-v4` is what Modern adopted, and answers as `movement.ModernRules` does,
value for value (`internal/pathlab.TestTheAdoptedSetIsModern`): a replay
under one is a replay under the other. It is the baseline without
allied pass-through, jam release and pocket release; Sidestep with
pass-behind and no braking, keeping its heading when it has no way round,
following a friend going its way however slow, looking no farther than
the point its route turns at and keeping the way round it chose while its
steps are refused; route smoothing with legs
through movers; a heuristic weight of 1.5, and 3 while route searching is
busy; a re-request throttle of fifteen ticks (measured so; Modern has since
let a held follower wait for the unit in its way, see
[prompt re-routing](DESIGN_MOVEMENT_PATH.md#modern-prompt-re-routing), and
`next-v4` answers that too); arrival places and exchange;
settling, near a place and short of a sealed goal; route claims with rank,
four along and twenty-four against; and Through after five seconds.

Two of its parts were measured as they would have stood alone, and each is a
rule set of its own. `step1` is the baseline with every overlap policy kept
and the routes alone: smoothing with legs through movers, the two weights and
the throttle of fifteen ticks. `step2` retires the overlap policies for the
steering and adds nothing else. The first holds no more units in whole games
than the baseline does. The second measures well on recorded moments and
locks up in whole games, on maps of narrow passes worse than retail's
movement: what keeps a crowd from holding itself is settling short of a
sealed goal and Through. Steering and the rules for arriving were therefore
adopted together.

A diagnostic capture taken in a game (Ctrl+Shift+F11) becomes a scenario
with the laboratory's capture script, kept with the research data: the
units where they stood and the selected ones ordered as one group to where
they were going.

## Whole games

A recorded moment is half a minute long. What only a long game shows — a
crowd that holds itself for minutes — is looked for in whole games the
computer plays against itself: `cmd/ai-arena` plays the Modern brain on both
sides under the research harness, whose movement is Modern's, or with the
movement and search of any laboratory rule set (`-rules aikit-<set>`, as
`aikit-lab-overlap` for the baseline), and samples, for every ground unit,
whether it is **held**: it
has stood within 48 world units of one point for ten seconds while the goal
of its order lies more than 320 away. A held unit is counted **open** when
the ground connects where it stands with its goal, for its kind and owner and
with every mobile unit absent. The others have been sent where they cannot
go, which no way of moving mends, and the brain sends many: on a map whose
halves only some kinds of unit can cross between, most held units are of the
kinds that cannot.

`-jam` adds a picture of the last tick to the result: every ground unit with
its order, route, heading and what refused its last step; for each held unit
what a route search from where it stands answers under three readings of the
ground (its own, friends absent, every mobile unit absent); and the ground
itself as one held unit's search reads it. `movement.LabRouteViews`,
`LabRing`, `LabGround` and `LabReachOf` are the diagnostics behind it; none of
them is called from a tick.

What the pictures showed of the policy without Sealed and Through: three
held units in four stood on orders to goals the ground sealed off — wrecks
of the first battles close a narrow pass — behind the first rows of units
that had reached the nearest ground; their searches were rejected at setup,
the friends in front of them being walls [04 R-PATH-01 §14]
[04 R-PATH-01 §4]. Modern empties such a crowd by letting its units through
each other to that ground, where Modern unreachable moves finishes their
orders. `-watch` showed units circling the point their route turned at for
minutes, steering round the ground beyond it, which `ToWaypoint` ends; and
the pictures showed units standing on moves the arrival pilot had never
taken up, because they had begun before it saw them: the computer player's,
and moves that waited in a queue. It takes them up now.

`-rules aikit-retail-move` plays the same harness with retail's movement and
route search in place of Modern's, for what the same brains' games hold under
the rules the recorded games were played by. It is a reference for the
census and not a way to play.

What holds the units of a crowd at a closed pass differs by policy in one
more way. Under the baseline no unit on an attack order is held at the end of
such games: its units walk through each other until they are in range and
fire from one spot. Without overlap the rows behind the first stand out of
range.
Two movers that refuse each other are several times as common at the end of
those games without overlap, and a rule that settled them by rank — the one
longer on its way asks the other aside once they have refused each other for
half a second — was measured and dropped: it
brings every unit of the benchmark's aligned funnel through and changes
nothing in whole games, for a few ticks a trip on recorded ones.

## Cost

A policy is measured for what it costs a tick as well as for what it buys a
trip: the [simulation-cost benchmark](SIM_BENCHMARK.md), which plays the rule
sets the game links, by alternating runs of two builds when two policies are
compared; and the search work and steering probes the replay logs count for
every rule set (`searches`, `pops`, `probes`). Quality and search work trade
against each other through the heuristic's weight and the re-request
throttle; quote both.

## Reading the two together

On the path benchmark's 181 authored scenes Modern traffic stands between the
retail rules and the baseline: of 22,386 units it brings 4,057 near their
goals in the window, Strict 3.1 2,885 and the baseline 5,908, for four times
the baseline's search work. One scene shows why better routes can do worse at
a gap
(`traffic/choke_one`, sixteen units and a gap one unit wide on their first
row): retail's greedy routes are one straight row that a convoy keeps to,
while routes that lead each unit the short way converge on the gap from
every angle, and two units arrive each holding one of the two rows of cells
the gap needs. The baseline's route straightening does the same and its
overlap hides it. With the gap moved off the group's rows the order is the
baseline, Modern traffic, the retail rules.


The path benchmark's scenes are authored to be hard: dense blocks of 64 to
256 units ordered from a standing start, and opposed friendly columns through
apertures one or two footprints wide. The recordings say how often those
occur. Measured over 847 recordings:

- friendly units meeting head-on within three cells of terrain account for
  about 2% of blocked time, and blocked time is distributed over ground
  clearance as travel itself is — terrain chokes are not over-represented;
- group orders of 32 units or more are 4% of group orders but a fifth of all
  unit trips made in a group; 64 or more, 0.7% and 7%.

So a policy is judged on the recorded corpus first, large groups included; an
authored choke is a stress test whose weight is its share of real play.
