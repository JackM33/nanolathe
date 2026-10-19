package aikit

import (
	"math"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func TestStockpileRolesUseAuthoredWeapons(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		stockpile, interceptor, inactive bool
		want                             Role
	}{
		{"renamed_launcher", true, false, false, RoleStockpile},
		{"renamed_interceptor", true, true, false, RoleInterceptor},
		{"nonstock_interceptor", false, true, false, RoleInterceptor},
		{"inactive_launcher", true, false, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			weapon := &content.WeaponDef{ID: 1, Stockpile: tc.stockpile, Interceptor: tc.interceptor, DamageDefault: 100, Range: 3000, ReloadTime: 30}
			if tc.inactive {
				weapon.RestoreActiveByte(0)
			}
			def := &content.UnitDef{UnitName: tc.name, Weapon2Def: weapon, BMCode: 1, CanMove: true, MaxVelocity: 3 << 16}
			got := summarize(tc.name, def, def.Commander)
			if got.Role&(RoleStockpile|RoleInterceptor) != tc.want {
				t.Fatalf("roles %b, want %b", got.Role, tc.want)
			}
			if got.DPS != 0 || got.AirDPS != 0 || got.Range != 0 || got.Role.Any(RoleCombat|RoleDefense|RoleAntiAir|RoleArtillery) {
				t.Fatalf("strategic weapon supplied sustained fire: %+v", got)
			}
			if !tc.inactive && got.Role.Has(RoleScout) {
				t.Fatal("strategic carrier classified as scout")
			}
		})
	}
	// A conventional second weapon retains its own ordinary combat role.
	d := &content.UnitDef{Weapon1Def: &content.WeaponDef{ID: 1, Stockpile: true, DamageDefault: 1000}, Weapon2Def: &content.WeaponDef{ID: 2, DamageDefault: 20, ReloadTime: 30, Range: 400}}
	if got := summarize("mixed", d, d.Commander); got.DPS != 20 || got.Range != 400 || !got.Role.Has(RoleDefense|RoleStockpile) {
		t.Fatalf("mixed carrier %+v", got)
	}
}

func TestStockpileRolesRetail(t *testing.T) {
	cat, _ := retailcat.Shared(t)
	tab := BuildTable(cat, nil)
	for _, tc := range []struct {
		key  string
		role Role
	}{
		{"armsilo", RoleStockpile}, {"corsilo", RoleStockpile},
		{"armamd", RoleInterceptor}, {"corfmd", RoleInterceptor}, {"armscab", RoleInterceptor}, {"cormabm", RoleInterceptor},
	} {
		u := tab.Lookup(tc.key)
		if u == nil {
			t.Errorf("missing %s", tc.key)
			continue
		}
		if !u.Role.Has(tc.role) || u.Role.Any(RoleDefense|RoleCombat|RoleScout|RoleStorage) || u.DPS != 0 || u.Range != 0 {
			t.Errorf("%s roles %b DPS %d range %d", tc.key, u.Role, u.DPS, u.Range)
		}
	}
}

func stockpileFixture(t *testing.T) (*Host, *units.World, *units.Unit) {
	t.Helper()
	d := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "launcher"}, UnitName: "launcher", MaxDamage: 100,
		Weapon1Def: &content.WeaponDef{ID: 1, Stockpile: true, ReloadTime: 300},
		Weapon2Def: &content.WeaponDef{ID: 2, Stockpile: true, Interceptor: true, ReloadTime: 300},
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"launcher": d}}
	w := fixtureWorld(cat)
	hd, err := w.Create(d, 0, 100<<16, 0, 100<<16)
	if err != nil {
		t.Fatal(err)
	}
	u := w.Unit(hd)
	u.Remaining = 0
	r := rng.NewSimulation(17)
	binding := &orders.QueueBinding{SimRNG: &r}
	m := &ai.Manager{Player: 0, Catalog: cat, RNG: &r, OrderBinding: binding}
	h := NewHost(m, &countBrain{}, PersonaMax)
	h.kit.Table = BuildTable(cat, nil)
	h.ex.m = m
	h.buildObs(1, w, computerEconomy())
	h.kit.out = &h.b
	return h, w, u
}

func TestStockpileObservationDetachedAndSigned(t *testing.T) {
	h, w, u := stockpileFixture(t)
	u.Slots[0].Ammo, u.Slots[1].Ammo = 4, 9
	q := orders.BindQueueBinding(u, h.m.OrderBinding)
	id := orders.Lookup("BuildWeapon")
	q.SetSecondary([]*orders.Node{
		{ID: id, Param1: 0, Param2: 2}, {ID: id, Param1: 1, Param2: 7},
		{ID: id, Param1: 0, Param2: 3}, {ID: id, Param1: 0, Param2: math.MaxUint32},
		{ID: id, Param1: 3, Param2: 100}, {ID: orders.Lookup("Standby"), Param1: 0, Param2: 100},
	})
	h.buildObs(2, w, computerEconomy())
	got := h.obs.Own[0]
	if got.Ammo != [3]int32{4, 9, 0} || got.StockpileQueued != [3]int32{5, 7, 0} || got.QueueLen != 0 {
		t.Fatalf("observation %+v", got)
	}
	u.Slots[0].Ammo = 100
	q.Secondary()[0].Param2 = 100
	if h.obs.Own[0].Ammo != got.Ammo || h.obs.Own[0].StockpileQueued != got.StockpileQueued {
		t.Fatal("observation shares live state")
	}
	q.SetSecondary([]*orders.Node{{ID: id, Param2: math.MaxInt32}, {ID: id, Param2: 1}})
	_, queued := stockpileState(u)
	if queued[0] != math.MaxInt32 {
		t.Fatalf("overflowed positive queue sum: %v", queued)
	}
}

func TestStockpileQueueSlotsCoalesceWithoutFreeRounds(t *testing.T) {
	for _, rules := range []orders.Rules{orders.StrictRules{}, orders.CommunityRules{}, &orders.ModernRules{}} {
		h, w, u := stockpileFixture(t)
		h.m.OrderBinding.Rules = rules
		q := orders.BindQueueBinding(u, h.m.OrderBinding)
		move := orders.Lookup("Move_Ground")
		q.Push(move, orders.NewNodeForOrder(move, 0, 200<<16, 0, 200<<16, 1, u.Handle, false))
		before := *q.PrimaryAt(0)
		u.Slots[1].Ammo = 3
		econ := computerEconomy()
		econ.Players[0].Stock = [2]float32{1000, 2000}
		h.m.OrderBinding.Economy = econ
		buckets := *econ.UnitBuckets(u.Handle)
		ledger := econ.Players[0]
		random := *h.m.RNG
		for _, request := range []struct{ slot, count int32 }{{1, 2}, {1, 4}, {0, 3}} {
			h.kit.Stockpile(u.Handle, request.slot, request.count)
		}
		h.ex.apply(&h.b, 7, w, &Persona{})
		ammo, queued := stockpileState(u)
		if queued != [3]int32{3, 6, 0} || ammo != [3]int32{0, 3, 0} {
			t.Fatalf("ammo %v queue %v", ammo, queued)
		}
		if q.LenSecondary() != 2 || q.PrimaryLen() != 1 || !reflect.DeepEqual(*q.PrimaryAt(0), before) {
			t.Fatal("did not coalesce per slot or changed primary work")
		}
		if *h.m.RNG != random || !reflect.DeepEqual(econ.Players[0], ledger) || *econ.UnitBuckets(u.Handle) != buckets {
			t.Fatal("enqueue spent resources or RNG")
		}
		if h.ex.stats.Applied != 3 {
			t.Fatalf("stats %+v", h.ex.stats)
		}
	}
}

func TestStockpileRefusesInvalidAndStaleActors(t *testing.T) {
	for _, test := range []string{"negative slot", "bad slot", "empty slot", "zero count", "negative count", "inactive", "ordinary weapon", "incomplete", "not owned", "dying", "dead", "reused"} {
		t.Run(test, func(t *testing.T) {
			h, w, u := stockpileFixture(t)
			slot, count := int32(0), int32(1)
			switch test {
			case "negative slot":
				slot = -1
			case "bad slot":
				slot = 3
			case "empty slot":
				slot = 2
			case "zero count":
				count = 0
			case "negative count":
				count = -1
			}
			h.kit.Stockpile(u.Handle, slot, count)
			switch test {
			case "inactive":
				u.Slots[0].Weapon.RestoreActiveByte(0)
			case "ordinary weapon":
				u.Slots[0].Weapon.Stockpile = false
			case "incomplete":
				u.Remaining = .5
			case "not owned":
				u.Owner = 1
			case "dying":
				u.Dying = true
			case "dead":
				u.Alive = false
			case "reused":
				hd := u.Handle
				w.Destroy(hd, units.DeathKilled)
				w.FinalizeDeath(hd, 2)
				if _, err := w.CreateWithForcedSlot(u.Def, 0, 100<<16, 0, 100<<16, hd); err != nil {
					t.Fatal(err)
				}
			}
			h.ex.apply(&h.b, 7, w, &Persona{})
			if h.ex.stats.Applied != 0 {
				t.Fatal("invalid command applied")
			}
			if q := orders.QueueOfUnit(w.Unit(u.Handle)); q != nil && q.LenSecondary() != 0 {
				t.Fatal("invalid command queued")
			}
		})
	}
}

func TestStockpileRequestBoundAndAPM(t *testing.T) {
	h, w, u := stockpileFixture(t)
	u.Slots[0].Ammo = 198
	h.kit.Stockpile(u.Handle, 0, math.MaxInt32)
	h.kit.Stockpile(u.Handle, 1, 1)
	h.ex.apply(&h.b, 7, w, &Persona{APM: 1, Burst: 1})
	_, queued := stockpileState(u)
	if queued != [3]int32{2, 0, 0} || h.ex.stats.DroppedAPM != 1 {
		t.Fatalf("bound/APM: %v %+v", queued, h.ex.stats)
	}
	h.b.reset()
	h.kit.Stockpile(u.Handle, 0, 1)
	h.ex.apply(&h.b, 8, w, &Persona{})
	_, queued = stockpileState(u)
	if queued[0] != 2 || h.ex.stats.Applied != 1 {
		t.Fatalf("exceeded completed+queued cap: %v %+v", queued, h.ex.stats)
	}
}

type stockpileBrain struct{ countBrain }

func (b *stockpileBrain) Think(k *Kit, o *Obs) {
	b.thinks++
	if b.thinks == 1 {
		k.Stockpile(o.Own[0].H, 0, 1)
	}
}

func TestStockpileRespectsReactionDelay(t *testing.T) {
	h, w, u := stockpileFixture(t)
	b := &stockpileBrain{}
	h = NewHost(h.m, b, PersonaMax)
	defer h.Close()
	tick := uint32(1)
	for ; b.thinks == 0 && tick < 100; tick++ {
		h.Step(tick, w, computerEconomy())
		h.Join()
	}
	if b.thinks != 1 {
		t.Fatal("brain did not think")
	}
	if _, q := stockpileState(u); q[0] != 0 {
		t.Fatal("queued before reaction delay")
	}
	due := h.b.due
	for ; tick < due; tick++ {
		h.Step(tick, w, computerEconomy())
	}
	if _, q := stockpileState(u); q[0] != 0 {
		t.Fatal("queued before due tick")
	}
	h.Step(due, w, computerEconomy())
	if _, q := stockpileState(u); q[0] != 1 {
		t.Fatalf("due command queue %v", q)
	}
}
