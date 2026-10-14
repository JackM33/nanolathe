//go:build retail

package content

import (
	"testing"
)

// Issue 120 investigation: the real construction-aircraft sight inputs.
func TestIssue120ConstructorSightCensus(t *testing.T) {
	cat := compiledRetailCatalog(t)
	for _, key := range []string{"armca", "corca", "armaca", "coraca"} {
		def, ok := cat.Unit(key)
		if !ok || def == nil {
			t.Logf("%s: absent", key)
			continue
		}
		q := int(int16(def.SightDistance)) / 32
		idx := q - 5
		t.Logf("%s: sightdistance=%d canfly=%v shapeIndex(floor/32-5)=%d", def.UnitName, def.SightDistance, def.CanFly, idx)
	}
	sh, err := CompileSightShapes(mountRetail(t))
	if err != nil {
		t.Fatal(err)
	}
	for k := 0; k < sh.Count(); k++ {
		s := sh.Shape(k)
		t.Logf("shape %d: %dx%d anchor=(%d,%d)", k, s.W, s.H, s.AnchorX, s.AnchorY)
	}
}
