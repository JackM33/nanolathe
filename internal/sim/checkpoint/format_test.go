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
	wantBytes := "4e4c4350535441540300" +
		"000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f" +
		"202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f" +
		"0d00" +
		"010001040302010105000000656e747279" +
		"020000030000040000" +
		"0500010200000003000000726f7700000000" +
		"0600000700000800000900000a00000b00000c00000d0000"
	const wantFull = "460d47e29981686885118988918abfe74f173fe4bc08d5b66733d71c959561db"
	wantOwners := [OwnerCount]string{
		"55827d28fae97abae50566ec32d5a56a91b9f358901798ece9b3f4d888e373bc",
		"7e5a83f50da8322625dd6aa1cddab061f85968fd67df1340b102b0fa7b607fd0",
		"1fd709c653bca2bd36e5a749e28e92248c95f239ec5ab7f4578978c9c961e5e7",
		"2e5a6e7a34c936026f34d3a3ac4bc8e5521c0fef00c58f934322163721bdfeff",
		"82d9af219b70577d2e8247ada8bb00b9286a9f3e6c7a38bfb9454a44533d5e8d",
		"22cb708a7b2c63343901cdcc95778d05a4ef9b00a175df74849ce66c656fba2d",
		"45d73adce0445b5bc4da26316c4ae768d154b2a9056bbbf2c67dd8d7fdda6bfc",
		"87fe4937dc2f2483526c5185f3ffbd55ea182d70ba965117dbe6e31ebe0fbfc0",
		"baf13c09ad0670b2415e0001ee485568651de9e48400e10f00877594fac1cc13",
		"74ae88a352185531e0b05b5e235bef555275eebb0fc0b836663c3db4cbd26c24",
		"8aa9eb30f6d751a1bb4c3748486ba331c5fc1a36e0a926b2b58dd03c033f7845",
		"aeca64e4cbb6e1bd8d5c1b9aeb8eec26d6112d9746e9d2952cecaf685778d59a",
		"c959b75169d02c98194cea99423b31c2e9f75a21f116291950da227296b3f2d6",
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
	expected.Write([]byte{3, 0})
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
		domain.Write([]byte{3, 0})
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
