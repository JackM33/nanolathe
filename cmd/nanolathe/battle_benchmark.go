package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/platform/ebitenapp"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func runBattleBenchmark(opts Options, cs *contentSet, b *battleSession, c *client.Client, field *headless.SimBenchScene) error {
	if err := os.MkdirAll(filepath.Dir(opts.BattleBenchmark), 0755); err != nil {
		return err
	}
	if err := os.Mkdir(opts.BattleBenchmark, 0755); err != nil {
		return fmt.Errorf("nanolathe: create fresh benchmark directory: %w", err)
	}
	s := b.sess
	// The fixture is the documented one whatever the player has saved: every
	// benchmark comparison assumes the normal speed, and the lead-in's exact
	// tick check counts one tick per viewer step. A saved game-speed
	// preference is a player setting, not a benchmark input [01 §4.3].
	if s.Clock != nil {
		s.Clock.Requested, s.Clock.Active = settings.DefaultGameSpeed, settings.DefaultGameSpeed
	}
	diagnostics := newBattleBenchmarkDiagnostics(opts.BattleBenchmark, s)
	defer diagnostics.End()
	var scene *coastalBenchmarkScene
	var captureScene *captureBenchmarkScene
	var factories []*units.Unit
	var cx, cz int32
	var err error
	if field != nil {
		cx, cz = field.CentreX, field.CentreZ
		for _, handle := range field.FactoryHandles() {
			factories = append(factories, s.Units.Unit(handle))
		}
		// The passive human is deliberately remote. Select the first fighting
		// computer's ordinary visibility; do not unmask a different workload.
		if err := s.EnqueueHumanCommand(session.HumanCommand{Kind: session.HumanView, View: session.HumanViewCommand{Player: 1}}); err != nil {
			return err
		}
	} else if opts.BenchmarkCapture != "" {
		captureScene, err = stageCaptureBenchmark(opts, s)
		if err != nil {
			return err
		}
		cx, cz = captureScene.CameraX, captureScene.CameraZ
		factories = captureScene.Factories
	} else {
		scene, err = stageCoastalBenchmark(opts, s)
		if err != nil {
			return err
		}
		cx, cz = scene.X, scene.Z
		factories = scene.Factories
	}
	c.SetAsyncSimulation(false)
	c.SetFocused(false)
	c.SetEnhanced(opts.Renderer == "modern")
	c.SetInterpolation(opts.Renderer == "modern" && opts.BenchmarkTPS > 30)
	statePoints := make([]benchmarkStatePoint, 0, 4)
	appendState := func(phase string) error {
		point, err := benchmarkState(s, phase)
		if err == nil {
			statePoints = append(statePoints, point)
		}
		return err
	}
	if err := appendState("fixture"); err != nil {
		return err
	}
	millis := &shotMillisSource{}
	b.millisSource = millis
	// NANOLATHE_BENCH_FREEZE=1 is a diagnostic render-only mode: lead-in and
	// warmup step normally, then the measured draws repeat the final pair.
	freeze, frozen := os.Getenv("NANOLATHE_BENCH_FREEZE") == "1", false
	step := func() {
		if frozen {
			return
		}
		diagnostics.Step(func() { millis.step++; b.viewerStep(1.0/30, c) })
	}
	positionCamera := func() {
		if captureScene != nil {
			b.cam.JumpTo(cx, cz)
		} else {
			b.cam.JumpToBattleViewCenter(cx, cz)
		}
	}
	positionCamera()
	openingTick := s.Clock.GlobalTick
	for i := 0; i < opts.BenchmarkPreTicks; i++ {
		step()
		// Drain each committed tick so the first displayed frame does not replay
		// the entire lead-in's retained sound and status queue.
		c.TickPresentationAudio()
	}
	if s.Clock.GlobalTick != openingTick+uint32(opts.BenchmarkPreTicks) {
		return fmt.Errorf("nanolathe: benchmark lead-in stopped advancing: expected tick %d, got %d", openingTick+uint32(opts.BenchmarkPreTicks), s.Clock.GlobalTick)
	}
	positionCamera()
	// The benchmark scene at the detail view is the same battle drawn from
	// twice the pixels (DESIGN_GPU_RENDERER §14.1). The scale is applied after
	// the scene's own camera jump and about the viewport centre, so the army
	// stays framed, and it is recorded in the scene metadata below: two runs
	// are comparable only at the same scale.
	// The benchmark defaults to native, as do the window and capture routes.
	// An explicit --zoom is part of the scene metadata (§14.6).
	if opts.Zoom != 0 && opts.Zoom != camera.ZoomUnit {
		mx, my := battleViewCentre(b.cam)
		jumpBattleZoom(b, mx, my, opts.Zoom, modernRenderer(opts))
	}
	if err := appendState("pre-window"); err != nil {
		return err
	}
	census := func() any {
		f := s.Snapshot.Current()
		nanoframes, nano, damaged := 0, 0, 0
		visibleUnits, visibleProjectiles, visibleEffects := 0, 0, 0
		visible := func(x, y, z numeric.Fixed) bool {
			sx, sy := b.cam.WorldToScreen(x, y, z)
			return sx >= camera.OriginX && sx < b.cam.ViewW && sy >= camera.OriginY && sy < b.cam.ViewH-32
		}
		moving := benchmarkMovingUnits(f, s.Snapshot.Previous())
		// In-view counts use projected anchors inside the battle viewport;
		// they do not claim pixel visibility after fog or sprite occlusion.
		sprites, burning, visibleSprites, visibleBurning := 0, 0, 0, 0
		for _, feature := range f.Features {
			if feature.Filename != "" {
				sprites++
				sx, sy := b.cam.WorldToScreen(feature.X, feature.Y, feature.Z)
				visible := sx >= camera.OriginX && sx < b.cam.ViewW && sy >= camera.OriginY && sy < b.cam.ViewH-32
				if visible {
					visibleSprites++
				}
				if feature.IsBurning {
					burning++
					if visible {
						visibleBurning++
					}
				}
			}
		}
		production := make([]benchmarkFactory, len(factories))
		for i, u := range factories {
			if u == nil || !u.Alive || u.Def == nil {
				continue
			}
			row := benchmarkFactory{Unit: u.Def.UnitName, Owner: u.Owner, Stance: u.InBuildStance, YardOpen: u.YardOpen}
			if q := orders.QueueOfUnit(u); q != nil {
				for _, n := range q.Primary() {
					if n.BuildDefKey != "" {
						row.Phase = n.Phase
						row.Product = n.BuildDefKey
						row.Deadline = n.Deadline
						row.Target = uint32(n.Target)
						break
					}
				}
			}
			production[i] = row
		}
		for _, u := range f.Units {
			if visible(u.X, u.Y, u.Z) {
				visibleUnits++
			}
			if u.Health < u.MaxHealth && u.BuildRemaining == 0 {
				damaged++
			}
			if u.BuildRemaining > 0 {
				nanoframes++
			}
		}
		for _, p := range f.Projectiles {
			if visible(p.X, p.Y, p.Z) {
				visibleProjectiles++
			}
		}
		for _, e := range f.Effects {
			if visible(e.X, e.Y, e.Z) {
				visibleEffects++
			}
		}
		for _, e := range f.Events {
			if e.Kind == frame.EventKindNanolathe {
				nano++
			}
		}
		previousTick, currentTick, blended := c.PresentedTicks()
		sample := map[string]any{"in_view_units": visibleUnits, "in_view_projectiles": visibleProjectiles, "in_view_effects": visibleEffects, "features": len(f.Features), "sprite_features": sprites, "burning_features": burning, "in_view_sprite_features": visibleSprites, "in_view_burning_features": visibleBurning, "damaged_units": damaged, "moving_units": moving, "tick": s.Clock.GlobalTick, "units": len(f.Units), "projectiles": len(f.Projectiles), "effects": len(f.Effects), "fragments": len(f.Fragments), "state": s.State.String(), "nanoframes": nanoframes, "nanolathe_events": nano, "factory_production": production, "builds": len(f.Builds), "shake": f.ShakeActive, "camera_x": b.cam.X, "camera_z": b.cam.Z, "view_player": f.ViewingPlayer, "previous_tick": previousTick, "current_tick": currentTick, "blended": blended, "tick_fraction": c.TickFraction()}
		if scene != nil {
			addCoastalBenchmarkCensus(sample, f, s, b.cam)
		}
		return sample
	}
	metadata := map[string]any{"scene_kind": opts.BenchmarkScene, "scene_version": 5, "gameplay": s.Gameplay.Normalize(), "rules": s.Rules.Name, "gameplay_features": s.Community, "entry_gameplay_features": s.EntryCommunity, "gameplay_features_digest": s.Community.Digest(), "content_profile": cs.contentProfileName(), "mod": cs.modSelector(), "mutators": s.Mutators.String(), "restrictions": s.Restrictions.String(), "phase_timing": true, "tps": opts.BenchmarkTPS, "game_speed": s.Clock.Active, "map": opts.Map, "seed": opts.Seed, "simulation_seed": s.RNGSimSeed, "crt_seed": s.RNGCrtSeed, "factories": opts.BenchmarkFactories, "viewport": []int32{b.cam.ViewW, b.cam.ViewH}, "zoom": viewZoomOf(b).Float(), "auto_remaster": opts.AutoRemaster, "pre_window_ticks": opts.BenchmarkPreTicks, "display": loadedSettings().Display, "effects": c.Effects(), "ground_light_strength": c.GroundLightStrength(), "blast_ring_strength": c.BlastRingStrength(), "unit_limit": s.Units.UnitLimit(), "root": opts.Root, "roots": opts.Roots, "fixture_state": statePoints[0], "input": "disabled", "simulation_host": "synchronous one viewer pump per authoritative tick"}
	if scene != nil {
		metadata["coastal_scene"] = scene
		metadata["battle_center"] = []int32{cx, cz}
		metadata["mobiles_per_side"] = 160
		metadata["mobile_roster"] = coastalBenchmarkMobiles()
	} else if field != nil {
		metadata["scene_version"] = field.Version
		metadata["field_scene"] = field
		metadata["battle_center"] = []int32{cx, cz}
	} else {
		metadata["scene_version"] = 1
		metadata["scene_kind"] = "capture"
		metadata["capture_scene"] = captureScene
		metadata["camera_origin"] = []int32{cx, cz}
	}
	metadata["visibility"] = "normal"
	var measuredOpeningTick uint32
	beforeMeasure := func() error {
		measuredOpeningTick = s.Clock.GlobalTick
		frozen = freeze
		if err := appendState("window-open"); err != nil {
			return err
		}
		return diagnostics.Begin()
	}
	afterMeasure := func() error {
		expected := measuredOpeningTick + uint32((opts.BenchmarkFrames+opts.BenchmarkTPS/30-1)/(opts.BenchmarkTPS/30))
		if frozen {
			expected = measuredOpeningTick
		}
		if s.Clock.GlobalTick != expected {
			return fmt.Errorf("nanolathe: benchmark measurement stopped advancing: expected tick %d, got %d", expected, s.Clock.GlobalTick)
		}
		if err := diagnostics.End(); err != nil {
			return err
		}
		if err := appendState("window-close"); err != nil {
			return err
		}
		return battleBenchmarkWriteJSON(filepath.Join(opts.BattleBenchmark, "benchmark-state.json"), statePoints)
	}
	benchmark := ebitenapp.BenchmarkOptions{Directory: opts.BattleBenchmark, Renderer: opts.Renderer, Frames: opts.BenchmarkFrames, TPS: opts.BenchmarkTPS, Width: int(b.cam.ViewW), Height: int(b.cam.ViewH), BeforeMeasure: beforeMeasure, AfterMeasure: afterMeasure, Metadata: metadata,
		Frozen: func() bool { return frozen }}
	metadata["simulation_frozen_during_measurement"] = freeze
	if err := ebitenapp.BattleBenchmark(c, step, census, benchmark); err != nil {
		return err
	}
	var blocked []map[string]any
	for _, u := range factories {
		if u == nil || !u.Alive || u.Def == nil {
			continue
		}
		rect, ok := s.Build.PlacementForProduct(u.Handle)
		if !ok {
			continue
		}
		yard, e := world.ParseYardMap(u.Def.YardMap, int(rect.Width()), int(rect.Depth()))
		if e != nil {
			return e
		}
		for z := rect.MinZ(); z < rect.MaxZ(); z++ {
			for x := rect.MinX(); x < rect.MaxX(); x++ {
				if yard[int((z-rect.MinZ())*rect.Width()+x-rect.MinX())]&(0x02|0x08) == 0 {
					continue
				}
				h, held := s.Movement.Grid.OccupantAt(movement.Cell{X: x, Z: z})
				if held && h != int(u.Handle) {
					blocked = append(blocked, map[string]any{"factory": u.Def.UnitName, "handle": u.Handle, "cell": []int32{x, z}, "occupant": h})
				}
			}
		}
	}
	f, e := os.Create(filepath.Join(opts.BattleBenchmark, "factory-blockers.json"))
	if e != nil {
		return e
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(blocked)
}

// These hashes are host diagnostics, collected outside the measured window.
// The state fingerprint is deliberately partial; the frame hashes separately
// name the exact committed pair supplied to presentation [I6].
type benchmarkStatePoint struct {
	Phase             string `json:"phase"`
	Tick              uint32 `json:"tick"`
	StateFingerprint  string `json:"state_fingerprint"`
	PreviousFrameHash string `json:"previous_frame_hash"`
	CurrentFrameHash  string `json:"current_frame_hash"`
	PreviousTick      uint32 `json:"previous_tick"`
	CurrentTick       uint32 `json:"current_tick"`
	SimulationDraws   uint64 `json:"simulation_draws"`
	CRTDraws          uint64 `json:"crt_draws"`
}

func benchmarkState(s *session.Session, phase string) (benchmarkStatePoint, error) {
	out := benchmarkStatePoint{Phase: phase, Tick: s.Clock.GlobalTick}
	var err error
	out.StateFingerprint, err = s.PartialStateFingerprint()
	if err != nil {
		return out, err
	}
	if stream := s.SimRNG(); stream != nil {
		out.SimulationDraws = stream.Draws()
	}
	if stream := s.CrtRNG(); stream != nil {
		out.CRTDraws = stream.Draws()
	}
	if s.Snapshot != nil {
		for i, f := range []*frame.Frame{s.Snapshot.Previous(), s.Snapshot.Current()} {
			if f == nil {
				continue
			}
			hash := sha256.New()
			if err := json.NewEncoder(hash).Encode(f); err != nil {
				return out, err
			}
			value := fmt.Sprintf("frame-json-v1:%x", hash.Sum(nil))
			if i == 0 {
				out.PreviousFrameHash, out.PreviousTick = value, f.Tick
			} else {
				out.CurrentFrameHash, out.CurrentTick = value, f.Tick
			}
		}
	}
	return out, nil
}

type benchmarkFactory struct {
	Unit     string
	Owner    uint8
	YardOpen bool
	Stance   bool
	Phase    uint8
	Product  string
	Deadline int32
	Target   uint32
}

// Published units are in ascending pool-slot order [I1]. Compare only the same
// instance in adjacent committed frames: a blocked mover can retain nonzero
// speed, and a newly created unit is not evidence of displacement.
func benchmarkMovingUnits(current, previous *frame.Frame) int {
	if current == nil || previous == nil {
		return 0
	}
	moving, before := 0, 0
	for _, u := range current.Units {
		for before < len(previous.Units) && previous.Units[before].Slot < u.Slot {
			before++
		}
		if before == len(previous.Units) {
			break
		}
		p := previous.Units[before]
		if p.Slot == u.Slot && p.InstanceID == u.InstanceID && (p.X != u.X || p.Y != u.Y || p.Z != u.Z) {
			moving++
		}
	}
	return moving
}
