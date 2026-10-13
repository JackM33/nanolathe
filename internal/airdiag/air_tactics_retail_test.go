package airdiag

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
)

// A stock Thunder ordered onto a solar collector 320 world units away: retail
// first flies 2240 world units out and back [04 R-AIR-01 §8], so Strict 3.1
// has released nothing after 360 ticks; the Modern pilot turns straight in and
// times the string on the dropped motion, releasing within 120 ticks with its
// middle bomb on the target, a whole retail string at the weapon's reload
// (DESIGN_MOVEMENT_PATH "Modern aircraft tactics").
func TestModernBomberAttacksCloseTargetAtOnce(t *testing.T) {
	root := testsupport.RetailRoot(t)
	for _, rules := range []string{"strict-3.1", "modern"} {
		t.Run(rules, func(t *testing.T) {
			h := labHarness(t, root, diagMap, 11)
			if err := h.Session.SetRules(rules); err != nil {
				t.Fatal(err)
			}
			w := h.Session.World
			c := labVec{float64(w.CellW) * 8, float64(w.CellH) * 8}
			s := newLabScene(h, "close", c)
			tgt := s.spawn(t, 1, "CORSOLAR", c)
			b := s.spawn(t, 0, "ARMTHUND", labVec{c.X - 320, c.Z})
			s.targets, s.air = append(s.targets, tgt), append(s.air, b)
			s.begin()
			h.Step(30)
			s.order(t, b, input.LatchAttack, tgt, labVec{})
			for i := 0; i < 360; i++ {
				h.Step(1)
				s.observe()
			}
			s.finish()
			if rules != "modern" {
				if s.firstRelease != 0 {
					t.Fatalf("Strict released at tick %d; retail flies out first", s.firstRelease)
				}
				return
			}
			if s.firstRelease == 0 || s.firstRelease > 120 || len(s.strings) == 0 {
				t.Fatalf("the Modern bomber's first release came at tick %d (strings %d)", s.firstRelease, len(s.strings))
			}
			if mid := s.strings[0].middleMiss; mid > 48 {
				t.Fatalf("the first string's middle bomb missed by %.1f world units", mid)
			}
			// The whole retail string — a Thunder at speed drops four — and
			// never two bombs closer than the weapon's reload.
			if n := len(s.strings[0].bombs); n < 4 {
				t.Fatalf("the first string dropped %d bombs, not the whole string", n)
			}
			if reload := int(b.SlotAt(0).Weapon.ReloadTime); s.minBombGap < reload {
				t.Fatalf("bombs %d ticks apart under a %d-tick reload", s.minBombGap, reload)
			}
		})
	}
}
