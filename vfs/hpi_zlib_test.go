package vfs

import (
	"bytes"
	"compress/zlib"
	"testing"
)

// zlibStream compresses payload; with finish false the writer is only
// flushed, so the stream has no final deflate block and no Adler-32 trailer,
// as third-party packers write the chunks of the community "V maps.ufo" pack.
func zlibStream(t *testing.T, payload []byte, finish bool) []byte {
	t.Helper()
	var out bytes.Buffer
	w := zlib.NewWriter(&out)
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	if finish {
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	} else if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestZlibAcceptsStreamWithoutFinalBlock(t *testing.T) {
	payload := bytes.Repeat([]byte("total annihilation "), 400)
	complete := zlibStream(t, payload, true)
	open := zlibStream(t, payload, false)
	for _, test := range []struct {
		name     string
		input    []byte
		expected uint64
		valid    bool
	}{
		{"complete stream", complete, uint64(len(payload)), true},
		{"no final block", open, uint64(len(payload)), true},
		{"no final block, fewer bytes than declared", open, uint64(len(payload)) + 1, false},
		{"no final block, more bytes than declared", open, uint64(len(payload)) - 1, false},
		{"cut inside the data", open[:len(open)/2], uint64(len(payload)), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := decodeZlib(bytes.NewReader(test.input), test.expected)
			if (err == nil) != test.valid {
				t.Fatalf("decode error = %v, want valid %v", err, test.valid)
			}
			if test.valid && !bytes.Equal(got, payload) {
				t.Fatalf("decoded %d bytes that differ from the payload", len(got))
			}
		})
	}
}
