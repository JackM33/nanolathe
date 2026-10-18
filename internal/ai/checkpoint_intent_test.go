package ai

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
)

func applicationCodecHex(t *testing.T, value string) []byte {
	t.Helper()
	return aiCheckpointHex(t, strings.Join(strings.Fields(value), ""))
}

// Literal bytes are authored from §16.3.23, independently of its encoder.
const applicationOperandsVector = `03000000
	04030201 1817161514131211 02000000 0300000000000000 04030201 1817161514131211
	01 24232221 3837363534333231
	01 01 44434241 06000000 756e69742f55`

const classicOrderVector = `01 feffffffffffffff 00 81 feffffff
	0000000000010000 fdffffffffffffff 0807060504030201 efcdab89`

const modernIntentVector = `0e 01 efcdab89 00000080 ffffff7f feffffff
	03000000 410042 ffffffff 00000080 fdffffff 04030201 00 01 fcffffff`

func applicationOperandsFixture() ApplicationOperands {
	a := checkpoint.Allocation{Handle: 0x01020304, Serial: 0x1112131415161718}
	return ApplicationOperands{
		Actors:  []checkpoint.Allocation{a, {Handle: 2, Serial: 3}, a},
		Target:  &checkpoint.Allocation{Handle: 0x21222324, Serial: 0x3132333435363738},
		Product: &checkpoint.Definition{Family: 1, Ordinal: 0x41424344, Key: "unit/U"},
	}
}

func classicOrderFixture() ClassicApplicationIntent {
	return ClassicApplicationIntent{Kind: 1, Code: -2, ResolvedRow: 0, Modifier: 0x81, Argument: -2,
		X: 1 << 40, Y: -3, Z: 0x0102030405060708, RawTarget: 0x89abcdef, Operands: applicationOperandsFixture()}
}

func modernIntentFixture() ModernApplicationIntent {
	return ModernApplicationIntent{Kind: 14, Queued: true, RawTarget: 0x89abcdef,
		X: math.MinInt32, Z: math.MaxInt32, ProductIndex: -2, ProductKey: "A\x00B",
		Slot: -1, Count: math.MinInt32, Spot: -3, Spacing: 0x01020304, Exact: true, RowNear: -4,
		Operands: applicationOperandsFixture()}
}

func applicationIntentBytes(t *testing.T, write func(*checkpoint.Encoder) error) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := write(checkpoint.NewEncoder(&out)); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestClassicApplicationIntentVectors(t *testing.T) {
	tests := []struct {
		name string
		v    ClassicApplicationIntent
		want string
	}{
		{"order including row zero", classicOrderFixture(), classicOrderVector + applicationOperandsVector},
		{"typed build retains request tick zero", ClassicApplicationIntent{Kind: 2, BuildKind: math.MinInt64,
			UnitKey: "A\x00B", X: -2, Z: 1 << 40, Count: math.MaxInt64},
			`02 0000000000000080 03000000 410042 feffffffffffffff 0000000000010000 ffffffffffffff7f 00000000 00000000 00 00`},
		{"activation false", ClassicApplicationIntent{Kind: 3}, `03 00 00000000 00 00`},
		{"activation true", ClassicApplicationIntent{Kind: 3, Active: true}, `03 01 00000000 00 00`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, want := applicationIntentBytes(t, tt.v.WriteCheckpoint), applicationCodecHex(t, tt.want); !bytes.Equal(got, want) {
				t.Fatalf("intent\n got %x\nwant %x", got, want)
			}
		})
	}
}

func TestClassicApplicationIntentVariantExclusion(t *testing.T) {
	for kind := uint8(1); kind <= 3; kind++ {
		v := ClassicApplicationIntent{Kind: kind}
		baseline := applicationIntentBytes(t, v.WriteCheckpoint)
		if kind != 1 {
			v.Code, v.ResolvedRow, v.Modifier, v.Argument, v.RawTarget, v.Y = -99, 255, 255, -1, 0xffffffff, -3
		}
		if kind != 2 {
			v.UnitKey, v.BuildKind, v.Count, v.RequestTick = "ignored", -4, 5, 6
		}
		if kind != 3 {
			v.Active = true
		} else {
			v.X, v.Z = 1<<40, -2
		}
		if got := applicationIntentBytes(t, v.WriteCheckpoint); !bytes.Equal(got, baseline) {
			t.Fatalf("kind %d encoded another variant's fields", kind)
		}
	}
}

func TestModernApplicationIntentVocabularyAndWidths(t *testing.T) {
	v := modernIntentFixture()
	want := applicationCodecHex(t, modernIntentVector+applicationOperandsVector)
	for kind := uint8(1); kind <= 14; kind++ {
		v.Kind, want[0] = kind, kind
		if got := applicationIntentBytes(t, v.WriteCheckpoint); !bytes.Equal(got, want) {
			t.Fatalf("kind %d\n got %x\nwant %x", kind, got, want)
		}
	}
}

func TestApplicationIntentObservedIdentityOrderAndPresence(t *testing.T) {
	v := classicOrderFixture()
	baseline := applicationIntentBytes(t, v.WriteCheckpoint)
	v.Operands.Actors[0], v.Operands.Actors[1] = v.Operands.Actors[1], v.Operands.Actors[0]
	if bytes.Equal(baseline, applicationIntentBytes(t, v.WriteCheckpoint)) {
		t.Fatal("actor order was sorted")
	}
	v = classicOrderFixture()
	v.Operands.Actors = v.Operands.Actors[:2]
	if bytes.Equal(baseline, applicationIntentBytes(t, v.WriteCheckpoint)) {
		t.Fatal("duplicate actor was dropped")
	}
	// A raw target never manufactures or suppresses an observed allocation.
	v = ClassicApplicationIntent{Kind: 1, RawTarget: 7}
	absent := applicationIntentBytes(t, v.WriteCheckpoint)
	v.Operands.Target = &checkpoint.Allocation{Handle: 9, Serial: 11}
	present := applicationIntentBytes(t, v.WriteCheckpoint)
	if bytes.Equal(absent, present) || !bytes.HasSuffix(absent, []byte{0, 0}) || !bytes.HasSuffix(present, applicationCodecHex(t, `01 09000000 0b00000000000000 00`)) {
		t.Fatal("target presence was inferred from its raw handle")
	}
	v.RawTarget = 0
	if got := applicationIntentBytes(t, v.WriteCheckpoint); !bytes.HasSuffix(got, applicationCodecHex(t, `01 09000000 0b00000000000000 00`)) {
		t.Fatal("zero raw target erased its observed target")
	}
	v.Operands.Target = nil
	v.Operands.Product = &checkpoint.Definition{Family: 1, Ordinal: 1, Key: "unit/U"}
	if got := applicationIntentBytes(t, v.WriteCheckpoint); !bytes.HasSuffix(got, applicationCodecHex(t, `00 01 01 01000000 06000000 756e69742f55`)) {
		t.Fatal("product presence or exact key changed")
	}
}

func TestApplicationIntentStructuralRefusals(t *testing.T) {
	badOperands := []ApplicationOperands{
		{Actors: []checkpoint.Allocation{{Serial: 1}}},
		{Actors: []checkpoint.Allocation{{Handle: 1}}},
		{Target: &checkpoint.Allocation{Serial: 1}},
		{Target: &checkpoint.Allocation{Handle: 1}},
		{Product: &checkpoint.Definition{Family: 2, Ordinal: 1, Key: "unit/a"}},
		{Product: &checkpoint.Definition{Family: 1, Key: "unit/a"}},
		{Product: &checkpoint.Definition{Family: 1, Ordinal: 1}},
		{Product: &checkpoint.Definition{Family: 1, Ordinal: 1, Key: "weapon/a"}},
		{Product: &checkpoint.Definition{Family: 1, Ordinal: 1, Key: "unit/"}},
	}
	var writers []func(*checkpoint.Encoder) error
	for _, operands := range badOperands {
		writers = append(writers, (ClassicApplicationIntent{Kind: 3, Operands: operands}).WriteCheckpoint,
			(ModernApplicationIntent{Kind: 1, Operands: operands}).WriteCheckpoint)
	}
	for _, kind := range []uint8{0, 4, 255} {
		writers = append(writers, (ClassicApplicationIntent{Kind: kind}).WriteCheckpoint)
	}
	for _, kind := range []uint8{0, ModernIntentKinds + 1, 255} {
		writers = append(writers, (ModernApplicationIntent{Kind: kind}).WriteCheckpoint)
	}
	for i, write := range writers {
		var out bytes.Buffer
		e := checkpoint.NewEncoder(&out)
		if err := write(e); err == nil || e.Err() == nil || out.Len() != 0 {
			t.Fatalf("invalid intent %d: error %v, bytes %x", i, err, out.Bytes())
		}
	}
	if err := (ClassicApplicationIntent{Kind: 3}).WriteCheckpoint(nil); err == nil {
		t.Fatal("Classic accepted a nil encoder")
	}
	if err := (ModernApplicationIntent{Kind: 1}).WriteCheckpoint(nil); err == nil {
		t.Fatal("Modern accepted a nil encoder")
	}
	// The ordinary typed callback boundary makes codec failures sticky too.
	h := historyFixture(t, 2)
	before := h.digest
	a := h.BeginAttempt(17, h.NextSerial(), 0, (ModernApplicationIntent{Kind: ModernIntentKinds + 1}).WriteCheckpoint)
	a.Finish(2, 3)
	if _, err := h.Snapshot(); err == nil || h.digest != before || h.count != 0 {
		t.Fatal("invalid typed intent advanced its history")
	}
}
