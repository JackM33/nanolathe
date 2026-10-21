package session

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/survival"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// A Survival battle can be saved (DESIGN_SURVIVAL §11). The retail bank
// holds the world — units, features, economy — and the save's sidecar holds
// what the bank cannot: the wave director. A restored director plays on from
// the saved phase, wave and spawn cursor, with the same live attackers on the
// same targets. Unit references are pool slots: a save writes each unit's
// slot as its stable identifier and a load restores the unit in that slot
// [08 R-SAVE-02 §6].

// SurvivalRecordVersion is the one record layout this build writes and reads.
const SurvivalRecordVersion = 1

// SurvivalRecord is a Survival battle's director as a save's sidecar carries
// it.
type SurvivalRecord struct {
	Version int `json:"version"`
	// Setup is the battle's setup record — rows, rule words and Survival
	// switches — so a restored battle restarts as the saved one began.
	Setup    SkirmishConfig `json:"setup"`
	Attacker uint8          `json:"attacker"`
	Team     []uint8        `json:"team"`
	Settled  [10]uint32     `json:"settled"`

	CentreX     int32      `json:"centreX"`
	CentreZ     int32      `json:"centreZ"`
	Deposits    int        `json:"deposits"`
	DepositAt   [][2]int32 `json:"depositAt,omitempty"`
	DepositWant int        `json:"depositWant"`
	DepositHave int        `json:"depositHave"`

	Phase        uint8                `json:"phase"`
	PhaseEnd     uint32               `json:"phaseEnd"`
	Wave         int                  `json:"wave"`
	Plan         SurvivalPlanRecord   `json:"plan"`
	NextGroup    int                  `json:"nextGroup"`
	NextPick     int                  `json:"nextPick"`
	LastSpawn    uint32               `json:"lastSpawn"`
	WaveUnits    []uint16             `json:"waveUnits,omitempty"`
	Units        []SurvivalUnitRecord `json:"units,omitempty"`
	NextRetarget uint32               `json:"nextRetarget"`

	Survived   int                  `json:"survived"`
	WavePoints int64                `json:"wavePoints"`
	CleanLost  bool                 `json:"cleanLost,omitempty"`
	Stats      [10]SurvivalStats    `json:"stats"`
	Removed    []SurvivalRemoved    `json:"removed,omitempty"`
	History    []SurvivalWaveRecord `json:"history,omitempty"`
	Info       *SurvivalInfoRecord  `json:"info,omitempty"`
}

// SurvivalPlanRecord is the planned wave, its picks named by pool key: the
// pool is rebuilt from the catalog at the load, so an index would only be
// right for an identical catalog.
type SurvivalPlanRecord struct {
	Number int                    `json:"number"`
	Budget int64                  `json:"budget"`
	Groups []SurvivalPlanGroupRec `json:"groups,omitempty"`
}

// SurvivalPlanGroupRec is one entry direction of the planned wave.
type SurvivalPlanGroupRec struct {
	Angle  uint16   `json:"angle"`
	Domain uint8    `json:"domain"`
	Units  []string `json:"units"`
}

// SurvivalUnitRecord is one live attacker the director commands.
type SurvivalUnitRecord struct {
	Unit      uint16 `json:"unit"`
	Wave      int    `json:"wave"`
	Target    uint16 `json:"target,omitempty"`
	Infecting bool   `json:"infecting,omitempty"`
}

// SurvivalRemoved is the health taken so far from one live attacker unit.
type SurvivalRemoved struct {
	Unit   uint16 `json:"unit"`
	Health int32  `json:"health"`
}

// SurvivalInfoRecord is the scenario record the survivors' managers hold.
type SurvivalInfoRecord struct {
	CentreX  int32                `json:"centreX"`
	CentreZ  int32                `json:"centreZ"`
	Team     []uint8              `json:"team"`
	Computer []bool               `json:"computer"`
	Starts   [][2]int32           `json:"starts"`
	Warnings []ai.SurvivalWarning `json:"warnings,omitempty"`
}

// RecordSurvival returns the battle's director for a save's sidecar, or nil
// outside Survival. Call it between ticks, as the bank writer is called.
func RecordSurvival(s *Session) *SurvivalRecord {
	if s == nil || s.Survival == nil {
		return nil
	}
	st := s.Survival
	r := &SurvivalRecord{
		Version: SurvivalRecordVersion, Setup: s.Skirmish,
		Attacker: st.attacker, Team: slices.Clone(st.team), Settled: st.settled,
		CentreX: st.centreX, CentreZ: st.centreZ,
		Deposits: st.deposits, DepositAt: slices.Clone(st.depositAt), DepositWant: st.depositWant, DepositHave: st.depositHave,
		Phase: uint8(st.phase), PhaseEnd: st.phaseEnd, Wave: st.wave,
		NextGroup: st.nextG, NextPick: st.nextP, LastSpawn: st.lastSpawn, NextRetarget: st.nextRetarget,
		Survived: st.survived, WavePoints: st.wavePoints, CleanLost: st.cleanLost, Stats: st.stats,
		History: slices.Clone(st.history),
	}
	r.Plan = SurvivalPlanRecord{Number: st.plan.Number, Budget: st.plan.Budget}
	for _, g := range st.plan.Groups {
		rec := SurvivalPlanGroupRec{Angle: g.Angle, Domain: uint8(g.Domain), Units: make([]string, 0, len(g.Picks))}
		for _, i := range g.Picks {
			key := ""
			if i >= 0 && i < len(st.pool.Units) {
				key = st.pool.Units[i].Key
			}
			rec.Units = append(rec.Units, key)
		}
		r.Plan.Groups = append(r.Plan.Groups, rec)
	}
	for _, h := range st.waveUnits {
		r.WaveUnits = append(r.WaveUnits, uint16(h))
	}
	for _, su := range st.units {
		r.Units = append(r.Units, SurvivalUnitRecord{Unit: uint16(su.h), Wave: su.wave, Target: uint16(su.target), Infecting: su.infecting})
	}
	// The totals are a lookup map; the record lists them in slot order by
	// walking the slots, never the map (INVARIANTS I1). A captured host keeps
	// its own slot, so the walk covers every slot, not the attacker's slice.
	if len(st.removed) > 0 && s.Units != nil {
		for slot := 1; slot < s.Units.TotalRecords() && slot <= math.MaxUint16; slot++ {
			if health, ok := st.removed[pool.Handle(slot)]; ok {
				r.Removed = append(r.Removed, SurvivalRemoved{Unit: uint16(slot), Health: health})
			}
		}
	}
	if info := st.info; info != nil {
		r.Info = &SurvivalInfoRecord{
			CentreX: info.CentreX, CentreZ: info.CentreZ,
			Team: slices.Clone(info.Team), Computer: slices.Clone(info.Computer), Starts: slices.Clone(info.Starts),
			Warnings: info.Warnings(math.MaxUint32, nil),
		}
	}
	return r
}

// SidecarSurvival decodes a sidecar's `survival` record; nil when it has
// none. A record that does not parse, or another version wrote, refuses the
// load rather than restoring a director it cannot trust.
func SidecarSurvival(raw []byte) (*SurvivalRecord, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var rec SurvivalRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("nanolathe: save sidecar survival record: %w", err)
	}
	if rec.Version != SurvivalRecordVersion {
		return nil, fmt.Errorf("nanolathe: save sidecar survival record: version %d: logical path save/sidecar/survival, providers searched [sidecar], expected version %d", rec.Version, SurvivalRecordVersion)
	}
	return &rec, nil
}

// restoreSurvival rebuilds a saved battle's director on the staged session.
// It runs after services and computer players are bound and before any unit
// is restored, as battle entry builds the director before the commanders:
// the vision team must exist before the first coverage is stamped. Nothing
// here draws from either stream.
func restoreSurvival(s *Session, rec *SurvivalRecord) error {
	if s == nil || rec == nil {
		return nil
	}
	if int(rec.Attacker) >= pool.PlayerCount || len(rec.Team) == 0 {
		return fmt.Errorf("nanolathe: save sidecar survival record: attacker %d with %d survivors: logical path save/sidecar/survival, providers searched [sidecar], expected an attacker slot below %d and at least one survivor", rec.Attacker, len(rec.Team), pool.PlayerCount)
	}
	var rules construction.Rules
	if s.Build != nil {
		rules = s.Build.Rules
	}
	wavePool, err := survival.BuildScenarioPool(s.Catalog, func(menu *content.BuildMenuPage) []string {
		return construction.BuildProducts(rules, menu)
	})
	if err != nil {
		return err
	}
	opts := rec.Setup.Survival
	opts.Enabled = true
	st := &survivalState{
		attacker: rec.Attacker, team: slices.Clone(rec.Team), settled: rec.Settled,
		tuning: survival.DefaultTuning(opts.Pace), opts: survival.Options{NoAir: opts.NoAir, NoNaval: opts.NoNaval}, pool: wavePool,
		centreX: rec.CentreX, centreZ: rec.CentreZ,
		deposits: rec.Deposits, depositAt: slices.Clone(rec.DepositAt), depositWant: rec.DepositWant, depositHave: rec.DepositHave,
		phase: survivalPhase(rec.Phase), phaseEnd: rec.PhaseEnd, wave: rec.Wave,
		nextG: rec.NextGroup, nextP: rec.NextPick, lastSpawn: rec.LastSpawn, nextRetarget: rec.NextRetarget,
		survived: rec.Survived, wavePoints: rec.WavePoints, cleanLost: rec.CleanLost, stats: rec.Stats,
		history: slices.Clone(rec.History),
	}
	// Picks whose unit the rebuilt pool lacks are dropped; the spawn cursor
	// steps back over any it had already passed.
	index := make(map[string]int, len(wavePool.Units)) // lookup only; never ranged
	for i, u := range wavePool.Units {
		if _, seen := index[u.Key]; !seen {
			index[u.Key] = i
		}
	}
	st.plan = survival.Wave{Number: rec.Plan.Number, Budget: rec.Plan.Budget}
	for gi, g := range rec.Plan.Groups {
		group := survival.Group{Angle: g.Angle, Domain: survival.Domain(g.Domain)}
		for pi, key := range g.Units {
			if i, ok := index[key]; ok {
				group.Picks = append(group.Picks, i)
			} else if gi == st.nextG && pi < rec.NextPick {
				st.nextP--
			}
		}
		st.plan.Groups = append(st.plan.Groups, group)
	}
	for _, h := range rec.WaveUnits {
		st.waveUnits = append(st.waveUnits, pool.Handle(h))
	}
	// The units themselves are restored later in the load; the director
	// already tolerates references to units that died, and its sweeps drop
	// them, so a reference is not checked here.
	for _, u := range rec.Units {
		st.units = append(st.units, survivalUnit{h: pool.Handle(u.Unit), wave: u.Wave, target: pool.Handle(u.Target), infecting: u.Infecting})
	}
	if len(rec.Removed) > 0 {
		st.removed = make(map[pool.Handle]int32, len(rec.Removed))
		for _, r := range rec.Removed {
			st.removed[pool.Handle(r.Unit)] = r.Health
		}
	}
	s.Survival = st
	s.EnemyOwner = rec.Attacker
	s.Skirmish.Survival = opts
	s.Skirmish.Players = rec.Setup.Players
	// The survivors share sight, explored map and radar (DESIGN_SURVIVAL §4.3),
	// set before any coverage exists, as battle entry sets it.
	team := make([]visibility.PlayerID, 0, len(st.team))
	for _, p := range st.team {
		team = append(team, visibility.PlayerID(p))
	}
	if s.Vis != nil {
		s.Vis.SetVisionTeam(team)
	}
	// Region labels are terrain-only, so the classes battle entry labelled
	// are labelled again here, in its order: the human's commander class,
	// every wave class, then each survivor's commander class.
	if s.World != nil && s.Catalog != nil {
		if human, err := skirmishCommander(s.Catalog, rec.Setup.Players[0].Side, 0); err == nil {
			cls := s.survivalClassFor(human)
			st.startClass, st.startRegion = cls, cls.regions.Largest()
		}
		for _, u := range st.pool.Units {
			if u.Domain != survival.Air {
				st.baseRegion(s.survivalClassFor(u.Def), s.survivalReach(u.Def))
			}
		}
		for _, p := range st.team {
			if def, err := skirmishCommander(s.Catalog, rec.Setup.Players[p].Side, int(p)); err == nil {
				st.baseRegion(s.survivalClassFor(def), s.survivalReach(def))
			}
		}
	}
	if rec.Info != nil {
		info := &ai.SurvivalInfo{
			CentreX: rec.Info.CentreX, CentreZ: rec.Info.CentreZ, Attacker: rec.Attacker,
			Team: slices.Clone(rec.Info.Team), Computer: slices.Clone(rec.Info.Computer), Starts: slices.Clone(rec.Info.Starts),
		}
		for _, w := range rec.Info.Warnings {
			info.PublishWarning(w)
		}
		st.info = info
		for _, p := range info.Team {
			if int(p) < len(s.AI) {
				if m := s.AI[p]; m != nil {
					m.Survival = info
				}
			}
		}
	}
	return nil
}
