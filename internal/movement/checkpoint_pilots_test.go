package movement

import (
	"bytes"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
)

func TestMovementCheckpointClaimPilotVector(t *testing.T) {
	own := &checkpointClaimRow{kind: 1, own: []uint32{8, 9}}
	all := &checkpointClaimRow{kind: 2, counts: [][8]uint8{{1, 2, 3, 4, 5, 6, 7, 8}}}
	st := &claimState{grids: []*claimGrid{nil, {all: all.counts, checkpointAll: all, written: []int32{-1, 2}}}, h: 3, have: true, own: own.own, checkpointOwn: own, serial: 4, w: 5}
	s := &System{PilotState: st}
	c := movementCheckpointContext()
	collectMovementCheckpoint(t, s, c)
	got := movementCheckpointWrite(t, func(e *checkpoint.Encoder) {
		writeMovementPilot(e, c, s, st, "pilot")
		for _, r := range c.claimRows.Values() {
			writeMovementClaimRow(e, r, "row")
		}
	})
	want := movementCheckpointVector(t, uint8(2), uint32(2), uint8(0), uint8(1), uint16(7), uint32(1), uint16(7), uint32(0), uint32(2), int32(-1), int32(2), int32(3), uint8(1), uint16(7), uint32(2), uint32(4), uint32(0), int32(5),
		uint8(2), uint32(1), [8]uint8{1, 2, 3, 4, 5, 6, 7, 8}, uint8(1), uint32(2), uint32(8), uint32(9))
	if !bytes.Equal(got, want) {
		t.Fatalf("claim pilot\ngot  %x\nwant %x", got, want)
	}
	st.trail = []int32{99}
	if after := movementCheckpointWrite(t, func(e *checkpoint.Encoder) {
		writeMovementPilot(e, c, s, st, "pilot")
		for _, r := range c.claimRows.Values() {
			writeMovementClaimRow(e, r, "row")
		}
	}); !bytes.Equal(got, after) {
		t.Fatal("trail scratch changed bytes")
	}
}

func TestMovementCheckpointArrivalPilotVector(t *testing.T) {
	st := &arriveState{claim: []uint32{1}, gen: 2, moveGround: 3, resv: []pool.Handle{4}, standby: 5, rows: []arriveRow{{bestAt: 6, bestD: -7, exchanges: 8, fx: 9, fz: 10, member: 11, nextCheck: 12, place: Cell{X: -13, Z: 14}, stood: true, stoodAt: 15, stoodX: 16, stoodZ: -17, x: 18, z: -19}}}
	s := &System{PilotState: st}
	c := movementCheckpointContext()
	collectMovementCheckpoint(t, s, c)
	got := movementCheckpointWrite(t, func(e *checkpoint.Encoder) { writeMovementPilot(e, c, s, st, "arrival") })
	want := movementCheckpointVector(t, uint8(3), uint32(1), uint32(1), uint32(2), uint8(3), uint32(1), uint32(4), uint32(1),
		uint32(6), int64(-7), uint8(8), int32(9), int32(10), uint32(11), uint32(12), uint16(3), uint32(0), int32(-13), int32(14), uint16(3), uint32(0), uint8(1), uint32(15), int32(16), int32(-17), int64(18), int64(-19), uint8(5))
	if !bytes.Equal(got, want) {
		t.Fatalf("arrival pilot\ngot  %x\nwant %x", got, want)
	}
}

func TestMovementCheckpointCompositePilotCyclesTerminate(t *testing.T) {
	st := &pilotsState{}
	st[0] = st
	st[1] = &NoPilot{}
	s := &System{PilotState: st}
	c := movementCheckpointContext()
	collectMovementCheckpoint(t, s, c)
	if len(c.pilots.Values()) != 2 {
		t.Fatal("composite identity cycle did not converge")
	}
	got := movementCheckpointWrite(t, func(e *checkpoint.Encoder) {
		writeMovementPilot(e, c, s, st, "composite")
		writeMovementPilot(e, c, s, st[1], "none")
	})
	want := movementCheckpointVector(t, uint8(4), uint16(6), uint32(1), uint16(6), uint32(2), uint16(6), uint32(0), uint16(6), uint32(0), uint8(1))
	if !bytes.Equal(got, want) {
		t.Fatalf("composite %x != %x", got, want)
	}
}
