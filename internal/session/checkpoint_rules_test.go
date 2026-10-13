package session

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// The fixture authors the owner projections independently. It never calls
// projectCommunity, aiCommunity, orderCommunity or a feature resolver.
func checkpointRuleFixture(entry, current gameplay.Mode) (*Session, checkpointRuleContext) {
	c := checkpointRuleContext{
		entryMode: entry,
		defaultCommunity: community.Features{
			GridClaimTieBreak: true, AlliedJammingIgnored: true,
			AIBuilderPlacementLimit: 37, OffMapAircraftMarginTiles: 5,
			RepairRate: community.RepairRate{RepairMultiplier: 1, SelfHealMultiplier: 1},
		},
	}
	if entry != gameplay.Strict31 {
		c.entryCommunity = community.Features{
			ConstructionKickout: true, GuardingBuildersHold: true, PatrollingBuilderFilters: true,
			ReclaimToggleKeepsBuild: true, StructureRotation: true, AreaDamageOverflow: true,
			AreaDamageDedupCap: true, GridClaimTieBreak: true, TransportedExplosions: true,
			BuildWeaponSlotGuard: true, AntinukeCircularCoverage: true, AlliedJammingIgnored: true,
			ResurrectionFinalization: true, WeaponTargetKeys: true, Veterancy: true,
			AirCorpseFall: true, AIDifficultyIncome: true, AIStockpileProducts: true,
			TargetLockRelease: true, AIApplianceEnergy: true, AIBuilderStopThreshold: true,
			WorkingWeaponsAutonomous: true, AttackSingleSlotTake: true, ResurrectionTextFix: true,
			HealTimeBitmask: true, AIBuilderPlacementLimit: 53, OffMapAircraftMarginTiles: 7,
			RepairRate: community.RepairRate{Enabled: true, RepairMultiplier: 3, SelfHealMultiplier: 9},
			// Unprojected fields must remain in the session only.
			ScriptPorts: true, MexSnap: true, MapFeatureOwnerEleven: true, SfxLimit: 61,
		}
	}
	s := &Session{Gameplay: current, EntryCommunity: c.entryCommunity}
	switch current {
	case gameplay.Strict31:
		s.Rules = StrictRuleSet()
	case gameplay.Community39:
		s.Rules = CommunityRuleSet()
	case gameplay.Modern:
		s.Rules = ModernRuleSet()
	default:
		panic("unsupported fixture mode")
	}
	if entry != gameplay.Strict31 {
		base := c.entryCommunity
		s.CommunitySources.CommandLine = []community.Overrides{{Base: &base}}
	}
	f := c.entryCommunity
	if current == gameplay.Strict31 {
		f = community.Features{}
	} else if entry == gameplay.Strict31 {
		f = c.defaultCommunity
	}
	s.Community = f
	s.Vis = &visibility.Service{Rules: s.Rules.Visibility, Community: visibility.CommunityState{
		AlliedJammingIgnored: f.AlliedJammingIgnored, OffMapAircraftMarginTiles: f.OffMapAircraftMarginTiles,
	}}
	s.Combat = &combat.Service{Rules: s.Rules.Combat, Community: community.Features{
		AirCorpseFall: f.AirCorpseFall, AntinukeCircularCoverage: f.AntinukeCircularCoverage,
		AreaDamageDedupCap: f.AreaDamageDedupCap, AreaDamageOverflow: f.AreaDamageOverflow,
		BuildWeaponSlotGuard: f.BuildWeaponSlotGuard, OffMapAircraftMarginTiles: f.OffMapAircraftMarginTiles,
		TargetLockRelease: f.TargetLockRelease, TransportedExplosions: f.TransportedExplosions,
		Veterancy: f.Veterancy, WeaponTargetKeys: f.WeaponTargetKeys,
	}}
	s.Build = &construction.Service{Rules: s.Rules.Construction, Community: community.Features{
		HealTimeBitmask: f.HealTimeBitmask, RepairRate: f.RepairRate,
		ResurrectionFinalization: f.ResurrectionFinalization, StructureRotation: f.StructureRotation,
		ConstructionKickout: f.ConstructionKickout,
	}, OrderBinding: &orders.QueueBinding{Rules: s.Rules.Orders, Community: community.Features{
		AttackSingleSlotTake: f.AttackSingleSlotTake, BuildWeaponSlotGuard: f.BuildWeaponSlotGuard,
		ConstructionKickout: f.ConstructionKickout, GuardingBuildersHold: f.GuardingBuildersHold,
		PatrollingBuilderFilters: f.PatrollingBuilderFilters, ReclaimToggleKeepsBuild: f.ReclaimToggleKeepsBuild,
		ResurrectionTextFix: f.ResurrectionTextFix, Veterancy: f.Veterancy,
		WeaponTargetKeys: f.WeaponTargetKeys, WorkingWeaponsAutonomous: f.WorkingWeaponsAutonomous,
	}}}
	s.Movement = &movement.System{Rules: s.Rules.Movement, Kernel: s.Rules.Path,
		Community: community.Features{GridClaimTieBreak: f.GridClaimTieBreak}}
	s.Econ = &economy.Service{Rules: s.Rules.Economy, Community: community.Features{AIDifficultyIncome: f.AIDifficultyIncome}}
	step := checkpointModernStep{}
	s.modernAI, s.modernAICheckpointWitness = step, ai.NewCheckpointModernPlanner(step)
	for _, player := range []int{0, 9} {
		s.AI[player] = &ai.Manager{ConstructionRules: s.Rules.Construction, Planner: s.Rules.Planner,
			Community: community.Features{
				AIBuilderPlacementLimit: f.AIBuilderPlacementLimit, AIBuilderStopThreshold: f.AIBuilderStopThreshold,
				AIApplianceEnergy: f.AIApplianceEnergy, AIStockpileProducts: f.AIStockpileProducts,
			}}
	}
	s.AI[9].Controller, s.AI[9].Planner = ai.ControllerModern, step
	return s, c
}

func TestCheckpointRuleBindingsEntryAndCurrentModes(t *testing.T) {
	modes := []gameplay.Mode{gameplay.Strict31, gameplay.Community39, gameplay.Modern}
	for _, entry := range modes {
		for _, current := range modes {
			t.Run(string(entry)+"/"+string(current), func(t *testing.T) {
				s, c := checkpointRuleFixture(entry, current)
				if err := s.validateCheckpointRuleBindings(c); err != nil {
					t.Fatal(err)
				}
				// Value identity, not the address of the retained Base, is the
				// source contract. Empty source containers need no allocation.
				s.CommunitySources.Content = []community.Overrides{}
				if entry == gameplay.Strict31 {
					s.CommunitySources.CommandLine = []community.Overrides{}
				} else {
					base := c.entryCommunity
					s.CommunitySources.CommandLine[0].Base = &base
				}
				if err := s.validateCheckpointRuleBindings(c); err != nil {
					t.Fatal(err)
				}
				// This leaf permits absent owners; complete admission must
				// still establish their required presence and aliases.
				s.Vis, s.Combat, s.Build, s.Movement, s.Econ = nil, nil, nil, nil, nil
				s.AI = [10]*ai.Manager{}
				if err := s.validateCheckpointRuleBindings(c); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestCheckpointRuleBindingsSourceRefusals(t *testing.T) {
	for _, tc := range []struct {
		field string
		edit  func(*Session, *checkpointRuleContext)
	}{
		{"Rules.Base", func(s *Session, _ *checkpointRuleContext) { s.Rules.Base = "" }},
		{"Rules.Name", func(s *Session, _ *checkpointRuleContext) { s.Rules.Name = "Modern" }},
		{"Gameplay", func(s *Session, _ *checkpointRuleContext) { s.Gameplay = gameplay.Strict31 }},
		{"Rules.Features", func(s *Session, _ *checkpointRuleContext) { f := community.Features{}; s.Rules.Features.Base = &f }},
		{"CommunitySources.Content", func(s *Session, _ *checkpointRuleContext) { s.CommunitySources.Content = []community.Overrides{{}} }},
		{"CommunitySources.Player", func(s *Session, _ *checkpointRuleContext) { b := false; s.CommunitySources.Player.MexSnap = &b }},
		{"CommunitySources.CommandLine", func(s *Session, _ *checkpointRuleContext) { s.CommunitySources.CommandLine = nil }},
		{"CommunitySources.CommandLine", func(s *Session, _ *checkpointRuleContext) {
			s.CommunitySources.CommandLine = append(s.CommunitySources.CommandLine, community.Overrides{})
		}},
		{"CommunitySources.CommandLine[0]", func(s *Session, _ *checkpointRuleContext) { s.CommunitySources.CommandLine[0].Base = nil }},
		{"CommunitySources.CommandLine[0]", func(s *Session, _ *checkpointRuleContext) { s.CommunitySources.CommandLine[0].Base.SfxLimit++ }},
		{"CommunitySources.CommandLine[0]", func(s *Session, _ *checkpointRuleContext) {
			s.CommunitySources.CommandLine[0].Table = community.Mainline
		}},
		{"CommunitySources.CommandLine[0]", func(s *Session, _ *checkpointRuleContext) {
			b := true
			s.CommunitySources.CommandLine[0].GridClaimTieBreak = &b
		}},
		{"EntryCommunity", func(s *Session, _ *checkpointRuleContext) { s.EntryCommunity.SfxLimit++ }},
		{"Community", func(s *Session, _ *checkpointRuleContext) { s.Community.SfxLimit++ }},
		{"entryMode", func(_ *Session, c *checkpointRuleContext) { c.entryMode = "unknown" }},
	} {
		t.Run(tc.field, func(t *testing.T) {
			s, c := checkpointRuleFixture(gameplay.Modern, gameplay.Community39)
			tc.edit(s, &c)
			if err := s.validateCheckpointRuleBindings(c); err == nil || !strings.Contains(err.Error(), "session."+tc.field) {
				t.Fatalf("wanted %s refusal, got %v", tc.field, err)
			}
		})
	}
	s, c := checkpointRuleFixture(gameplay.Strict31, gameplay.Modern)
	s.CommunitySources.CommandLine = []community.Overrides{{}}
	if err := s.validateCheckpointRuleBindings(c); err == nil {
		t.Fatal("Strict entry acquired command-line sources")
	}
	s, c = checkpointRuleFixture(gameplay.Strict31, gameplay.Modern)
	c.entryCommunity.MexSnap = true
	s.EntryCommunity = c.entryCommunity
	if err := s.validateCheckpointRuleBindings(c); err == nil {
		t.Fatal("Strict entry acquired a nonzero table")
	}
	s, c = checkpointRuleFixture(gameplay.Modern, gameplay.Strict31)
	s.Community = c.entryCommunity
	if err := s.validateCheckpointRuleBindings(c); err == nil {
		t.Fatal("Strict current mode retained a nonzero table")
	}
	if err := (*Session)(nil).validateCheckpointRuleBindings(c); err == nil {
		t.Fatal("nil session accepted")
	}
}

func TestCheckpointRuleBindingsProjectionRefusals(t *testing.T) {
	for _, tc := range []struct {
		field string
		edit  func(*Session)
	}{
		{"Vis.Rules", func(s *Session) { s.Vis.Rules = visibility.StrictRules{} }},
		{"Vis.Community", func(s *Session) { s.Vis.Community.AlliedJammingIgnored = false }},
		{"Vis.Community", func(s *Session) { s.Vis.Community.OffMapAircraftMarginTiles++ }},
		{"Combat.Rules", func(s *Session) { s.Combat.Rules = nil }},
		{"Combat.Community", func(s *Session) { s.Combat.Community.TargetLockRelease = false }},
		{"Combat.Community", func(s *Session) { s.Combat.Community.MexSnap = true }},
		{"Build.Rules", func(s *Session) { s.Build.Rules = (*construction.ModernRules)(nil) }},
		{"Build.Community", func(s *Session) { s.Build.Community.RepairRate.SelfHealMultiplier++ }},
		{"Build.Community", func(s *Session) { s.Build.Community.AIStockpileProducts = true }},
		{"Build.OrderBinding.Rules", func(s *Session) { s.Build.OrderBinding.Rules = orders.StrictRules{} }},
		{"Build.OrderBinding.Community", func(s *Session) { s.Build.OrderBinding.Community.WorkingWeaponsAutonomous = false }},
		{"Build.OrderBinding.Community", func(s *Session) { s.Build.OrderBinding.Community.AreaDamageOverflow = true }},
		{"Movement.Rules", func(s *Session) { s.Movement.Rules = &movement.OverlapRules{} }},
		{"Movement.Kernel", func(s *Session) { s.Movement.Kernel = path.StraightenKernel{} }},
		{"Movement.Community", func(s *Session) { s.Movement.Community.GridClaimTieBreak = false }},
		{"Movement.Community", func(s *Session) { s.Movement.Community.AIDifficultyIncome = true }},
		{"Econ.Community", func(s *Session) { s.Econ.Community.AIDifficultyIncome = false }},
		{"Econ.Community", func(s *Session) { s.Econ.Community.GridClaimTieBreak = true }},
		{"AI[0].ConstructionRules", func(s *Session) { s.AI[0].ConstructionRules = construction.StrictRules{} }},
		{"AI[0].Community", func(s *Session) { s.AI[0].Community.AIBuilderPlacementLimit++ }},
		{"AI[0].Community", func(s *Session) { s.AI[0].Community.AIDifficultyIncome = true }},
		{"AI[0].Planner", func(s *Session) { s.AI[0].Planner = ai.RetailPlanner{} }},
		{"AI[9].Controller", func(s *Session) { s.AI[9].Controller = 255 }},
		{"AI[9].Planner", func(s *Session) { s.modernAICheckpointWitness = ai.CheckpointModernPlanner{} }},
		{"AI[9].Planner", func(s *Session) { s.modernAI = checkpointModernEmbedding{} }},
		{"AI[9].Planner", func(s *Session) { s.AI[9].Planner = checkpointModernSlice{1} }},
		{"AI[9].Planner", func(s *Session) { s.AI[9].Planner = (*checkpointModernStep)(nil) }},
		{"AI[9].Planner", func(s *Session) { s.AI[9].Planner = ai.ModernPlanner{} }},
	} {
		t.Run(tc.field, func(t *testing.T) {
			s, c := checkpointRuleFixture(gameplay.Modern, gameplay.Modern)
			tc.edit(s)
			if err := s.validateCheckpointRuleBindings(c); err == nil || !strings.Contains(err.Error(), "session."+tc.field) {
				t.Fatalf("wanted %s refusal, got %v", tc.field, err)
			}
		})
	}
	s, c := checkpointRuleFixture(gameplay.Strict31, gameplay.Strict31)
	s.Movement.Kernel = nil
	if err := s.validateCheckpointRuleBindings(c); err == nil {
		t.Fatal("nil retail kernel fallback accepted as an explicit projection")
	}
	s, c = checkpointRuleFixture(gameplay.Modern, gameplay.Modern)
	s.Build.OrderBinding = nil
	if err := s.validateCheckpointRuleBindings(c); err != nil {
		t.Fatalf("absent shared binding belongs to whole-session admission: %v", err)
	}
}

func TestCheckpointRuleBindingsPurity(t *testing.T) {
	s, c := checkpointRuleFixture(gameplay.Modern, gameplay.Modern)
	// Snapshot values and owner identities without copying Session's locks or
	// comparing the generated witness function. Owner bodies are copied below.
	sessionValues := func() []any {
		return []any{s.Gameplay, s.Rules, s.Community, s.EntryCommunity, s.CommunitySources,
			s.Vis, s.Combat, s.Build, s.Movement, s.Econ, s.AI, s.modernAI, s.rngSim.State, s.rngCrt.State}
	}
	before := sessionValues()
	vis, combatState, build, binding, move, econ := *s.Vis, *s.Combat, *s.Build, *s.Build.OrderBinding, *s.Movement, *s.Econ
	classic, modern := *s.AI[0], *s.AI[9]
	source, base := s.CommunitySources.CommandLine[0], *s.CommunitySources.CommandLine[0].Base
	for i := 0; i < 2; i++ {
		if err := s.validateCheckpointRuleBindings(c); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(before, sessionValues()) || !reflect.DeepEqual(vis, *s.Vis) ||
		!reflect.DeepEqual(combatState, *s.Combat) || !reflect.DeepEqual(build, *s.Build) ||
		!reflect.DeepEqual(binding, *s.Build.OrderBinding) || !reflect.DeepEqual(move, *s.Movement) ||
		!reflect.DeepEqual(econ, *s.Econ) || !reflect.DeepEqual(classic, *s.AI[0]) ||
		!reflect.DeepEqual(modern, *s.AI[9]) || source != s.CommunitySources.CommandLine[0] ||
		base != *s.CommunitySources.CommandLine[0].Base {
		t.Fatal("rule preflight mutated retained state")
	}
	if allocations := testing.AllocsPerRun(20, func() {
		if err := s.validateCheckpointRuleBindings(c); err != nil {
			panic(err)
		}
	}); allocations != 0 {
		t.Fatalf("successful preflight allocated %v times", allocations)
	}
	// These unrelated ports are intentionally beyond this leaf's authority.
	s.Vis.Community.SetAllied(func(visibility.PlayerID, visibility.PlayerID) bool { panic("alliance reader called") })
	s.Vis.Community.SetOffMap(func(uint16) bool { panic("filing reader called") })
	s.AI[0].SetQueueBuildTyped(func(ai.BuildRequest) error { panic("producer called") })
	s.AI[9].Ext = []int{1, 2} // Controller/worker admission is a separate leaf.
	if err := s.validateCheckpointRuleBindings(c); err != nil {
		t.Fatal(err)
	}
}

func checkpointRuleSeamRefusals[T any](t *testing.T, field string, set func(*RuleSet, T), values []T) {
	t.Helper()
	for _, value := range values {
		s, c := checkpointRuleFixture(gameplay.Strict31, gameplay.Strict31)
		set(&s.Rules, value)
		if err := s.validateCheckpointRuleBindings(c); err == nil || !strings.Contains(err.Error(), "session.Rules."+field) {
			t.Fatalf("%s accepted %T or reported the wrong field: %v", field, value, err)
		}
	}
}

func TestCheckpointRuleBindingsClosedSeams(t *testing.T) {
	// Each unknown implementation embeds a nil interface so any gameplay call
	// panics; its slice also makes arbitrary interface comparisons unsafe.
	checkpointRuleSeamRefusals(t, "Visibility", func(r *RuleSet, v visibility.Rules) { r.Visibility = v }, []visibility.Rules{
		nil, (*visibility.StrictRules)(nil), (*visibility.CommunityRules)(nil), (*visibility.ModernRules)(nil),
		struct {
			visibility.Rules
			values []int
		}{}, struct{ visibility.StrictRules }{}, &visibility.CommunityRules{},
	})
	checkpointRuleSeamRefusals(t, "Movement", func(r *RuleSet, v movement.Rules) { r.Movement = v }, []movement.Rules{
		nil, (*movement.StrictRules)(nil), (*movement.CommunityRules)(nil), (*movement.ModernRules)(nil),
		struct {
			movement.Rules
			values []int
		}{}, struct{ movement.StrictRules }{}, &movement.CommunityRules{},
	})
	checkpointRuleSeamRefusals(t, "Orders", func(r *RuleSet, v orders.Rules) { r.Orders = v }, []orders.Rules{
		nil, (*orders.StrictRules)(nil), (*orders.CommunityRules)(nil), (*orders.ModernRules)(nil),
		struct {
			orders.Rules
			values []int
		}{}, struct{ orders.StrictRules }{}, &orders.CommunityRules{},
	})
	checkpointRuleSeamRefusals(t, "Construction", func(r *RuleSet, v construction.Rules) { r.Construction = v }, []construction.Rules{
		nil, (*construction.StrictRules)(nil), (*construction.CommunityRules)(nil), (*construction.ModernRules)(nil),
		struct {
			construction.Rules
			values []int
		}{}, struct{ construction.StrictRules }{}, &construction.CommunityRules{},
	})
	checkpointRuleSeamRefusals(t, "Combat", func(r *RuleSet, v combat.Rules) { r.Combat = v }, []combat.Rules{
		nil, (*combat.StrictRules)(nil), (*combat.CommunityRules)(nil), (*combat.ModernRules)(nil),
		struct {
			combat.Rules
			values []int
		}{}, struct{ combat.StrictRules }{}, &combat.CommunityRules{},
	})
	checkpointRuleSeamRefusals(t, "Path", func(r *RuleSet, v path.Kernel) { r.Path = v }, []path.Kernel{
		nil, (*path.RetailKernel)(nil), (*path.StraightenKernel)(nil), (*path.SmoothKernel)(nil),
		struct {
			path.Kernel
			values []int
		}{}, struct{ path.RetailKernel }{}, &path.StraightenKernel{},
	})
	checkpointRuleSeamRefusals(t, "Planner", func(r *RuleSet, v ai.Planner) { r.Planner = v }, []ai.Planner{
		nil, (*ai.RetailPlanner)(nil), (*ai.ModernPlanner)(nil),
		struct {
			ai.Planner
			values []int
		}{}, struct{ ai.RetailPlanner }{}, &ai.ModernPlanner{},
	})
	checkpointRuleSeamRefusals(t, "UnitLimit", func(r *RuleSet, v UnitLimitRules) { r.UnitLimit = v }, []UnitLimitRules{
		nil, (*StrictUnitLimit)(nil), (*ModernUnitLimit)(nil),
		struct {
			UnitLimitRules
			values []int
		}{}, struct{ StrictUnitLimit }{}, &ModernUnitLimit{},
	})
	checkpointRuleSeamRefusals(t, "ScriptPorts", func(r *RuleSet, v ScriptPortRules) { r.ScriptPorts = v }, []ScriptPortRules{
		nil, (*StrictScriptPorts)(nil), (*CommunityScriptPorts)(nil), (*ModernScriptPorts)(nil),
		struct {
			ScriptPortRules
			values []int
		}{}, struct{ StrictScriptPorts }{}, &CommunityScriptPorts{},
	})
	checkpointRuleSeamRefusals(t, "ComputerIncome", func(r *RuleSet, v ComputerIncomeRules) { r.ComputerIncome = v }, []ComputerIncomeRules{
		nil, (*StrictComputerIncome)(nil), (*CommunityComputerIncome)(nil), (*ModernComputerIncome)(nil),
		FullComputerIncome{}, &FullComputerIncome{},
		struct {
			ComputerIncomeRules
			values []int
		}{}, struct{ StrictComputerIncome }{}, &CommunityComputerIncome{},
	})
	checkpointRuleSeamRefusals(t, "Seats", func(r *RuleSet, v SeatRules) { r.Seats = v }, []SeatRules{
		nil, (*StrictSeats)(nil), (*ModernSeats)(nil),
		struct {
			SeatRules
			values []int
		}{}, struct{ StrictSeats }{}, &ModernSeats{},
	})
}

func TestCheckpointRuleBindingsPointerForms(t *testing.T) {
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Community39, gameplay.Modern} {
		s, c := checkpointRuleFixture(mode, mode)
		switch mode {
		case gameplay.Strict31:
			s.Rules.Visibility, s.Rules.Movement = &visibility.StrictRules{}, &movement.StrictRules{}
			s.Rules.Orders, s.Rules.Construction, s.Rules.Combat = &orders.StrictRules{}, &construction.StrictRules{}, &combat.StrictRules{}
			s.Rules.Path, s.Rules.Planner = &path.RetailKernel{}, &ai.RetailPlanner{}
			s.Rules.UnitLimit, s.Rules.ScriptPorts = &StrictUnitLimit{}, &StrictScriptPorts{}
			s.Rules.ComputerIncome, s.Rules.Seats = &StrictComputerIncome{}, &StrictSeats{}
		case gameplay.Community39:
			// The reserved constructor uses some pointers and some values.
			// Both forms of every reviewed concrete leaf carry the same tag.
			s.Rules.Visibility, s.Rules.Movement = &visibility.CommunityRules{}, movement.CommunityRules{}
			s.Rules.Orders, s.Rules.Construction, s.Rules.Combat = orders.CommunityRules{}, construction.CommunityRules{}, combat.CommunityRules{}
			s.Rules.Path, s.Rules.Planner = &path.RetailKernel{}, &ai.RetailPlanner{}
			s.Rules.UnitLimit, s.Rules.ScriptPorts = &ModernUnitLimit{}, &CommunityScriptPorts{}
			s.Rules.ComputerIncome, s.Rules.Seats = &CommunityComputerIncome{}, &ModernSeats{}
		case gameplay.Modern:
			s.Rules.Visibility = &visibility.ModernRules{}
			s.Rules.Path, s.Rules.Planner = &path.SmoothKernel{}, &ai.ModernPlanner{}
			s.Rules.UnitLimit, s.Rules.ScriptPorts = &ModernUnitLimit{}, &ModernScriptPorts{}
			s.Rules.ComputerIncome, s.Rules.Seats = &ModernComputerIncome{}, &ModernSeats{}
		}
		if err := s.validateCheckpointRuleBindings(c); err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
	}
}
