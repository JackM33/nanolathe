package utility

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// An Easy commander caught away from home by an enemy force (four
// commanders here) that has also reached the base retreats with the think's only action: the army's and the idle
// factory's reserves come after it.
func TestCommanderRetreatsBeforeReserves(t *testing.T) {
	w := newMetalWorld(DefaultParams(), aikit.PersonaEasy)
	base := w.metalBase()
	w.thinkWith(25*1800, base, metalAmpleRes, energyFullRes)

	base[0].X, base[0].Z = 1250, 1050 // the commander, out reclaiming
	var mem []aikit.Remembered
	for i := 0; i < 4; i++ {
		mem = append(mem, aikit.Remembered{H: pool.Handle(100 + i), Gen: 1, Info: w.d.com, Owner: 1,
			X: 1300 + int32(i)*20, Z: 1080, LastSeen: 25*1800 + 90})
	}
	tick := uint32(25*1800 + 90)
	w.e.s.tokens, w.e.s.lastFill = 1000, tick // one action this think
	w.obs = &aikit.Obs{Tick: tick, Own: base, Memory: mem, Metal: metalAmpleRes, Energy: energyFullRes}
	w.k.Tick = tick
	w.b.Update(w.k, w.obs)
	if w.b.NearHomeThreat == 0 {
		t.Fatal("fixture: the raiders are not near home")
	}
	w.st.Plan(w.b)
	if w.e.s.budget != 1 {
		t.Fatalf("fixture: budget = %d, want 1", w.e.s.budget)
	}
	w.e.Plan(w.b)
	if c := w.e.s.commitOf(&w.obs.Own[0]); c.kind != cRetreat {
		t.Fatalf("commander commitment = %s, want a retreat", commitNames[c.kind])
	}
}

// The commander takes no work away from home where its retreat test
// already holds, but still works at home under the same threat.
func TestCommanderAvoidsThreatenedSites(t *testing.T) {
	w := newMetalWorld(DefaultParams(), aikit.PersonaEasy)
	var mem []aikit.Remembered
	for i := 0; i < 4; i++ {
		mem = append(mem, aikit.Remembered{H: pool.Handle(100 + i), Gen: 1, Info: w.d.com, Owner: 1,
			X: 900 + int32(i)*20, Z: 700, LastSeen: 25 * 1800})
	}
	w.obs = &aikit.Obs{Tick: 25 * 1800, Own: w.metalBase(), Memory: mem, Metal: metalAmpleRes, Energy: energyFullRes}
	w.k.Tick = w.obs.Tick
	w.b.Update(w.k, w.obs)
	w.st.Plan(w.b)
	s := w.e.s
	if !s.comOff(w.b, 900, 720) {
		t.Fatal("a site beside the enemy force was offered to the commander")
	}
	if s.comOff(w.b, 600, 560) {
		t.Fatal("a site at home was refused under the same threat")
	}
	if s.comOff(w.b, 300, 1000) {
		t.Fatal("a quiet site away from home was refused")
	}
	if !s.comOff(w.b, 1800, 1800) {
		t.Fatal("a site beyond the leash was offered")
	}
}
