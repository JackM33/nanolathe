package session

import (
	"bytes"
	"reflect"
	"testing"
)

// Typed choices and raw known records resolve to one value. Opaque optional
// metadata keeps its bytes alongside both required gameplay records.
func TestSetupExtensionsShareTheVersion2Tail(t *testing.T) {
	r := validMatchRequest(t)
	r.RoomContent = []MatchContentEntry{{Kind: MatchContentBaseArchive, Name: "totala1.hpi", Size: 42}}
	base := encodeMatchForTest(t, resolveMatch(t, r))
	r.Seats[0].Start = StartPoint{Set: true, X: 3, Z: 4}
	r.AlliedSight = MatchAlliedSightShared
	r.Extensions = []MatchConfigExtension{{ID: 32, Data: []byte("annotation")}}
	c := resolveMatch(t, r)
	tail := extensionRecordForTest(17, 1, []byte{1, 3, 4, 0, 0})
	tail = append(tail, extensionRecordForTest(18, 1, []byte{2})...)
	tail = append(tail, extensionRecordForTest(32, 0, []byte("annotation"))...)
	tail = append(tail, 0)
	payload := withMatchTailForTest(base, tail)
	if !bytes.Equal(encodeMatchForTest(t, c), payload) {
		t.Fatal("gameplay and metadata records did not follow room content in ID order")
	}
	back, err := DecodeMatchConfig(payload)
	if err != nil || back.Digest() != c.Digest() || !reflect.DeepEqual(back.Request(), c.Request()) {
		t.Fatalf("typed choices or opaque metadata did not round-trip: %v", err)
	}
	q := back.Request()
	q.Seats[0].Start = StartPoint{}
	q.AlliedSight = MatchAlliedSightVersion1
	q.Extensions = nil
	if got := encodeMatchForTest(t, resolveMatch(t, q)); !bytes.Equal(got, base) {
		t.Fatal("clearing typed choices left stale gameplay extension records")
	}
	q = validMatchRequest(t)
	q.RoomContent = r.RoomContent
	q.Extensions = []MatchConfigExtension{{ID: 17, Required: true, Data: []byte{1, 3, 4, 0, 0}}, {ID: 18, Required: true, Data: []byte{2}}, {ID: 32, Data: []byte("annotation")}}
	if got := resolveMatch(t, q); got.Digest() != c.Digest() || !reflect.DeepEqual(got.Request(), c.Request()) {
		t.Fatal("raw known records did not project into the typed fields")
	}
	q.Seats[0].Start = StartPoint{Set: true, X: 9, Z: 4}
	if _, err := ResolveMatchConfig(q); err == nil {
		t.Fatal("conflicting typed and raw choices resolved")
	}
	q = back.Request()
	q.Extensions[0].Data[0] ^= 1
	if resolveMatch(t, q).Digest() == c.Digest() || !bytes.Equal(encodeMatchForTest(t, back), payload) {
		t.Fatal("metadata mutation changed frozen storage or failed to change identity")
	}
}

func TestSetupExtensionsRefuseOptionalOrMalformedGameplay(t *testing.T) {
	base := encodeMatchForTest(t, resolveMatch(t, validMatchRequest(t)))
	tooLarge := append([]byte{1}, uvarintBytes(StartPointLimit)...)
	tooLarge = append(tooLarge, 4, 0, 0)
	for _, tc := range []struct {
		name  string
		id    uint64
		flags byte
		data  []byte
	}{
		{"optional starts", 17, 0, []byte{1, 3, 4, 0, 0}},
		{"optional sight", 18, 0, []byte{2}},
		{"all Auto", 17, 1, []byte{0, 0, 0}},
		{"short starts", 17, 1, []byte{1, 3, 4}},
		{"extra starts", 17, 1, []byte{1, 3, 4, 0, 0, 0}},
		{"start flag", 17, 1, []byte{2, 0, 0}},
		{"coordinate bound", 17, 1, tooLarge},
		{"short sight", 18, 1, nil},
		{"extra sight", 18, 1, []byte{2, 0}},
		{"default sight", 18, 1, []byte{0}},
		{"unknown sight", 18, 1, []byte{3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := withMatchTailForTest(base, append(extensionRecordForTest(tc.id, tc.flags, tc.data), 0))
			if c, err := DecodeMatchConfig(payload); err == nil || c.Digest() != ([32]byte{}) {
				t.Fatalf("malformed gameplay extension accepted: %v", err)
			}
		})
	}
	// Typed records count toward the same 128-record bound as opaque metadata.
	r := validMatchRequest(t)
	r.AlliedSight = MatchAlliedSightShared
	for i := range 128 {
		r.Extensions = append(r.Extensions, MatchConfigExtension{ID: uint16(32 + i)})
	}
	if _, err := ResolveMatchConfig(r); err == nil {
		t.Fatal("129 records including a gameplay choice resolved")
	}
	for _, extension := range []MatchConfigExtension{
		{ID: 17, Required: true, Data: []byte{1, 3, 4, 0, 0}},
		{ID: 18, Required: true, Data: []byte{1}},
	} {
		survival := validSurvivalRequest(t)
		survival.Extensions = []MatchConfigExtension{extension}
		if _, err := ResolveMatchConfig(survival); err == nil {
			t.Fatal("Survival accepted a skirmish gameplay-choice extension")
		}
	}
}
