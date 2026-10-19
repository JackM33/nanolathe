package checkpoint

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"strings"
	"testing"
)

func authoredIdentity() Identity {
	var identity Identity
	for i := range identity.Content {
		identity.Content[i] = byte(i)
		identity.Config[i] = byte(i + 32)
	}
	return identity
}

func writeAuthoredCapture(t *testing.T, identity Identity, out io.Writer, changed bool) Digests {
	t.Helper()
	c, err := NewCapture(identity, out)
	if err != nil {
		t.Fatal(err)
	}
	// Named constants in published order also lock each exported ID against
	// the independently authored byte and digest vectors below.
	owners := [...]Owner{
		OwnerRuntime, OwnerUnits, OwnerOrders, OwnerScripts, OwnerWorld,
		OwnerVisibility, OwnerMovement, OwnerPaths, OwnerEconomy,
		OwnerConstruction, OwnerCombat, OwnerEffects, OwnerComputersScenario,
	}
	for _, owner := range owners {
		e, err := c.Section(owner, owner == OwnerRuntime || owner == OwnerWorld)
		if err != nil {
			t.Fatal(err)
		}
		switch owner {
		case OwnerRuntime:
			e.Field("authored.tick")
			e.U32(0x01020304)
			e.Bool(true)
			e.String("entry")
		case OwnerWorld:
			e.Field("authored.rows")
			e.Count(2)
			if changed {
				e.String("ROW")
			} else {
				e.String("row")
			}
			e.Bytes(nil)
		}
	}
	digests, err := c.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return digests
}

func TestCaptureAuthoredByteAndHashVectors(t *testing.T) {
	// The stream is authored independently of Encoder. Digest vectors were
	// computed with Python hashlib over these bytes and the specified domains.
	wantBytes := "4e4c4350535441540400" +
		"000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f" +
		"202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f" +
		"0d00" +
		"010001040302010105000000656e747279" +
		"020000030000040000" +
		"0500010200000003000000726f7700000000" +
		"0600000700000800000900000a00000b00000c00000d0000"
	const wantFull = "739665903c5f8cad76601f019c25a7526819f4bc2b302b809365310b97aef38e"
	wantOwners := [OwnerCount]string{
		"9edd92c0c38362efa8e67ba9ef5af84c93bdb445132643246f263bde09967c5c",
		"489de5e1b9d0f408d1d0abb3e23ed4bb4a564cceddbd664899a799d2dd32da73",
		"2d129b8192d1e01f9e6db9effaabbdbabffe11baa0ddcc3ad993d868509cadbf",
		"6ade6c15d28c636330fb9c5f75e7d32986222c9eac5dfc4580e36baf19226fd8",
		"d3bcfe65421ce83befe995044a7f562a6d70f276fd0a55f5e4572210d146b3ca",
		"91607925f8983db0ae0e0f0840bdeb2f3db191abd9334af9c4a3ebabf3749293",
		"651c390b5fb54f2eb1a37f5f64dbcffd410b436cd38a378caeb33b7c95b430d1",
		"4380fa7c224937828ece65df83eb71da6f178cc3438b9e619ac151d3c750c822",
		"9f0c9be3600a338dd7c0682e0a7eb3321322c56033f17437acf478d55c343129",
		"25c1a01841dab0ff5927610d408030996279ea41c9133bab8c8638bb3c34c0a2",
		"4f86b0ae5d83fc2baee5c8f6379731d06d7bddeb43591e5aa9ef1c8258f63aa8",
		"a8f32ca9761fdaaa4e6741761cbfdfb02bafb71c9dabd8ef3be9ffab42b26535",
		"9d69bb08f2c7b4584739c9fb602298249ad01e401ddd2910e2a73853b10dc2ac",
	}
	var out bytes.Buffer
	got := writeAuthoredCapture(t, authoredIdentity(), &out, false)
	if hex.EncodeToString(out.Bytes()) != wantBytes {
		t.Fatalf("stream = %x, want %s", out.Bytes(), wantBytes)
	}
	if hex.EncodeToString(got.Full[:]) != wantFull {
		t.Fatalf("full = %x, want %s", got.Full, wantFull)
	}
	for i, owner := range got.Owners {
		if hex.EncodeToString(owner[:]) != wantOwners[i] {
			t.Errorf("owner %d = %x, want %s", i+1, owner, wantOwners[i])
		}
	}
	if got.Full != sha256.Sum256(out.Bytes()) {
		t.Fatal("diagnostic bytes and full digest disagree")
	}
	if hashOnly := writeAuthoredCapture(t, authoredIdentity(), nil, false); got != hashOnly {
		t.Fatal("hash-only capture differs from diagnostic byte capture")
	}
}

func TestCaptureOwnerDomainsAndIdentity(t *testing.T) {
	base := writeAuthoredCapture(t, authoredIdentity(), nil, false)
	changed := writeAuthoredCapture(t, authoredIdentity(), nil, true)
	if base.Full == changed.Full {
		t.Fatal("changed payload did not change full digest")
	}
	for i := range base.Owners {
		if equal := base.Owners[i] == changed.Owners[i]; equal == (i == int(OwnerWorld)-1) {
			t.Fatalf("owner %d isolated payload change incorrectly: equal %t", i+1, equal)
		}
	}
	for _, config := range []bool{false, true} {
		identity := authoredIdentity()
		if config {
			identity.Config[3] ^= 1
		} else {
			identity.Content[3] ^= 1
		}
		bound := writeAuthoredCapture(t, identity, nil, false)
		if base.Full == bound.Full {
			t.Fatal("identity did not change full digest")
		}
		for i := range base.Owners {
			if base.Owners[i] == bound.Owners[i] {
				t.Fatalf("identity did not change owner %d digest", i+1)
			}
		}
	}
}

func TestCaptureSectionOrderAndCompleteness(t *testing.T) {
	tests := []struct {
		name   string
		owners []Owner
	}{
		{"none", nil},
		{"missing", []Owner{OwnerRuntime}},
		{"repeat", []Owner{OwnerRuntime, OwnerRuntime}},
		{"skip", []Owner{OwnerRuntime, OwnerOrders}},
		{"backwards", []Owner{OwnerUnits}},
		{"zero", []Owner{0}},
		{"unknown", []Owner{14}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := NewCapture(Identity{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, owner := range tt.owners {
				_, err = c.Section(owner, false)
				if err != nil {
					break
				}
			}
			digests, finishErr := c.Finish()
			if finishErr == nil || digests != (Digests{}) {
				t.Fatalf("invalid section sequence accepted: %v, %v", digests, finishErr)
			}
			if _, err := c.Section(OwnerRuntime, false); err != finishErr {
				t.Fatalf("section error changed: %v, want %v", err, finishErr)
			}
		})
	}
}

func TestCaptureClosedAndAbsentSections(t *testing.T) {
	for _, misuse := range []string{"absent", "advanced", "copied advanced", "finished", "copied finished", "finish twice", "section after finish"} {
		t.Run(misuse, func(t *testing.T) {
			var out bytes.Buffer
			c, err := NewCapture(Identity{}, &out)
			if err != nil {
				t.Fatal(err)
			}
			first, err := c.Section(OwnerRuntime, misuse != "absent")
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(misuse, "copied") {
				copied := *first
				first = &copied
			}
			if misuse != "absent" {
				for owner := OwnerUnits; owner <= OwnerComputersScenario; owner++ {
					if _, err := c.Section(owner, false); err != nil {
						t.Fatal(err)
					}
				}
			}
			if misuse == "finished" || misuse == "copied finished" || misuse == "finish twice" || misuse == "section after finish" {
				if _, err := c.Finish(); err != nil {
					t.Fatal(err)
				}
			}
			before := out.Len()
			if misuse == "section after finish" {
				if _, err := c.Section(OwnerRuntime, true); err == nil {
					t.Fatal("section after finish accepted")
				}
			} else if misuse != "finish twice" {
				first.Field("late.payload")
				first.Bytes(nil)
				if first.Err() == nil || !strings.Contains(first.Err().Error(), "runtime") || !strings.Contains(first.Err().Error(), "late.payload") {
					t.Fatalf("missing section misuse context: %v", first.Err())
				}
			}
			if got, err := c.Finish(); err == nil || got != (Digests{}) {
				t.Fatalf("misuse retained usable digests: %v, %v", got, err)
			}
			if out.Len() != before {
				t.Fatal("misuse wrote bytes")
			}
		})
	}
}

func TestCaptureFailuresInvalidateAllDigests(t *testing.T) {
	const headerSize = 8 + 2 + 32 + 32 + 2
	cause := errors.New("capture sink failure")
	for _, allowance := range []int{0, 9, headerSize, headerSize + 2, headerSize + 5} {
		for _, sinkErr := range []error{nil, cause} {
			w := &faultSink{remaining: allowance, err: sinkErr}
			c, err := NewCapture(Identity{}, w)
			want := sinkErr
			if want == nil {
				want = io.ErrShortWrite
			}
			if err == nil {
				e, sectionErr := c.Section(OwnerRuntime, true)
				if sectionErr == nil {
					e.Field("authored.tick")
					e.U32(7)
				}
				calls := w.calls
				var got Digests
				got, err = c.Finish()
				if got != (Digests{}) || calls != w.calls {
					t.Fatal("failed sink produced digests or accepted more writes")
				}
			} else if c != nil {
				t.Fatal("header failure returned a capture")
			}
			if !errors.Is(err, want) {
				t.Fatalf("allowance %d: %v, want %v", allowance, err, want)
			}
		}
	}
	var out bytes.Buffer
	c, err := NewCapture(Identity{}, &out)
	if err != nil {
		t.Fatal(err)
	}
	e, err := c.Section(OwnerRuntime, true)
	if err != nil {
		t.Fatal(err)
	}
	e.Field("wind.scalar")
	e.F32(math.Float32frombits(0x7fc00001))
	before := out.Len()
	if got, err := c.Finish(); err == nil || got != (Digests{}) || !strings.Contains(err.Error(), "runtime") || !strings.Contains(err.Error(), "wind.scalar") {
		t.Fatalf("NaN did not invalidate capture with context: %v, %v", got, err)
	}
	e.U8(1)
	if out.Len() != before {
		t.Fatal("NaN allowed a later payload")
	}
}

func TestCaptureHashBuffersPreserveSectionBytes(t *testing.T) {
	var out, expected bytes.Buffer
	identity := authoredIdentity()
	c, err := NewCapture(identity, &out)
	if err != nil {
		t.Fatal(err)
	}
	expected.WriteString("NLCPSTAT")
	expected.Write([]byte{4, 0})
	expected.Write(identity.Content[:])
	expected.Write(identity.Config[:])
	expected.Write([]byte{13, 0})
	var want Digests
	for owner := OwnerRuntime; owner <= OwnerComputersScenario; owner++ {
		e, err := c.Section(owner, true)
		if err != nil {
			t.Fatal(err)
		}
		var section bytes.Buffer
		section.Write([]byte{byte(owner), 0, 1})
		// Sizes on both sides of the hash buffer boundary, in one-byte writes.
		for i := 0; i < 4090+int(owner); i++ {
			value := byte(i*7 + int(owner))
			e.U8(value)
			section.WriteByte(value)
		}
		expected.Write(section.Bytes())
		var domain bytes.Buffer
		domain.WriteString("NLCPSECT")
		domain.Write([]byte{4, 0})
		domain.Write(identity.Content[:])
		domain.Write(identity.Config[:])
		domain.Write(section.Bytes())
		want.Owners[owner-1] = sha256.Sum256(domain.Bytes())
	}
	want.Full = sha256.Sum256(expected.Bytes())
	got, err := c.Finish()
	if err != nil || got != want || !bytes.Equal(out.Bytes(), expected.Bytes()) {
		t.Fatalf("buffered canonical digest/bytes differ: %v", err)
	}
}
