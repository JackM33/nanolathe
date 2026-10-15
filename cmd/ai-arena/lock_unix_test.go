//go:build unix

package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestSlotPoolIsExclusive checks that two holders never share a slot and that
// releasing one frees it for the next.
func TestSlotPoolIsExclusive(t *testing.T) {
	pool, err := parseSlots(filepath.Join(t.TempDir(), "slots") + ":1")
	if err != nil {
		t.Fatal(err)
	}
	release, err := pool.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	if _, err := pool.acquire(ctx); err == nil {
		t.Fatal("a second holder took the only slot")
	}
	release()
	again, err := pool.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	again()
}
