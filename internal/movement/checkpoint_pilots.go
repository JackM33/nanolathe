package movement

import (
	"errors"
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
)

func checkpointMovementPilot(p any) error {
	switch v := p.(type) {
	case nil:
		return nil
	case *NoPilot:
		if v != nil {
			return nil
		}
	case *claimState:
		if v != nil {
			return nil
		}
	case *arriveState:
		if v != nil {
			return nil
		}
	case *pilotsState:
		if v != nil {
			return nil
		}
	default:
		return fmt.Errorf("unsupported pilot state %T", p)
	}
	return errors.New("typed-nil pilot state")
}

func validateCheckpointClaimRow(v *checkpointClaimRow) error {
	if v == nil {
		return nil
	}
	switch v.kind {
	case 1:
		if v.counts != nil {
			return errors.New("own holder has count storage")
		}
	case 2:
		if v.own != nil {
			return errors.New("count holder has own storage")
		}
	default:
		return errors.New("unsupported claim-row holder")
	}
	return nil
}

func validateCheckpointOwnAlias(row []uint32, h *checkpointClaimRow) error {
	if h == nil {
		if len(row) != 0 {
			return errors.New("own row lacks retained holder")
		}
		return nil
	}
	if err := validateCheckpointClaimRow(h); err != nil {
		return err
	}
	if h.kind != 1 || len(h.own) != len(row) || len(row) > 0 && &row[0] != &h.own[0] {
		return errors.New("own holder does not alias live row")
	}
	return nil
}
func validateCheckpointCountsAlias(row [][8]uint8, h *checkpointClaimRow) error {
	if h == nil {
		if len(row) != 0 {
			return errors.New("count row lacks retained holder")
		}
		return nil
	}
	if err := validateCheckpointClaimRow(h); err != nil {
		return err
	}
	if h.kind != 2 || len(h.counts) != len(row) || len(row) > 0 && &row[0] != &h.counts[0] {
		return errors.New("count holder does not alias live row")
	}
	return nil
}

func collectMovementPilot(c *CheckpointContext, p any) (added int, err error) {
	if err := checkpointMovementPilot(p); err != nil {
		return 0, err
	}
	addRow := func(h *checkpointClaimRow) error {
		if err := validateCheckpointClaimRow(h); err != nil {
			return err
		}
		n, err := movementCheckpointAdd(&c.claimRows, h)
		added += n
		return err
	}
	switch v := p.(type) {
	case *claimState:
		for _, g := range v.grids {
			if g != nil {
				if err := validateCheckpointCountsAlias(g.all, g.checkpointAll); err != nil {
					return added, err
				}
				if err := addRow(g.checkpointAll); err != nil {
					return added, err
				}
				if err := validateCheckpointCountsAlias(g.slow, g.checkpointSlow); err != nil {
					return added, err
				}
				if err := addRow(g.checkpointSlow); err != nil {
					return added, err
				}
			}
		}
		if err := validateCheckpointOwnAlias(v.own, v.checkpointOwn); err != nil {
			return added, err
		}
		if err := addRow(v.checkpointOwn); err != nil {
			return added, err
		}
		for _, h := range v.slots {
			if h == nil {
				continue
			}
			if err := validateCheckpointOwnAlias(h.own, h); err != nil {
				return added, err
			}
			if err := addRow(h); err != nil {
				return added, err
			}
		}
	case *arriveState:
		for _, r := range v.rows {
			n, err := movementCheckpointAdd(&c.Orders.Nodes, r.node)
			added += n
			if err != nil {
				return added, err
			}
			n, err = movementCheckpointAdd(&c.Orders.Nodes, r.seen)
			added += n
			if err != nil {
				return added, err
			}
		}
	case *pilotsState:
		for _, child := range v {
			if err := checkpointMovementPilot(child); err != nil {
				return added, err
			}
			n, err := movementCheckpointAdd(&c.pilots, child)
			added += n
			if err != nil {
				return added, err
			}
		}
	}
	return added, nil
}

// Table6 variants retain claim grids/h/have/own/serial/slots/w, arrival
// claim/gen/moveGround/resv/rows/standby, or four ordered child references.
// Claim trail and arrival live/fresh/member walks are overwritten scratch.
func writeMovementPilot(e *checkpoint.Encoder, c *CheckpointContext, s *System, pilot any, p string) {
	m := movementCheckpointRecord{e, c, s, p}
	if err := checkpointMovementPilot(pilot); err != nil {
		m.fail("", err)
		return
	}
	switch v := pilot.(type) {
	case *NoPilot:
		e.U8(1)
	case *claimState:
		e.U8(2)
		m.count("grids", len(v.grids))
		for i, g := range v.grids {
			q := fmt.Sprintf("grids[%d]", i)
			m.boolean(q, g != nil)
			if g == nil {
				continue
			}
			if err := validateCheckpointCountsAlias(g.all, g.checkpointAll); err != nil {
				m.fail(q+".all", err)
				return
			}
			m.row(q+".all", g.checkpointAll)
			if err := validateCheckpointCountsAlias(g.slow, g.checkpointSlow); err != nil {
				m.fail(q+".slow", err)
				return
			}
			m.row(q+".slow", g.checkpointSlow)
			m.count(q+".written", len(g.written))
			for _, x := range g.written {
				e.I32(x)
			}
		}
		m.i32("h", v.h)
		m.boolean("have", v.have)
		if err := validateCheckpointOwnAlias(v.own, v.checkpointOwn); err != nil {
			m.fail("own", err)
			return
		}
		m.row("own", v.checkpointOwn)
		m.u32("serial", v.serial)
		// Modern parallel path batches: the own-mark rows of places one and
		// up, each present only once its place has been used.
		m.count("slots", len(v.slots))
		for i, h := range v.slots {
			q := fmt.Sprintf("slots[%d]", i)
			m.boolean(q, h != nil)
			if h == nil {
				continue
			}
			if err := validateCheckpointOwnAlias(h.own, h); err != nil {
				m.fail(q, err)
				return
			}
			m.row(q, h)
		}
		m.i32("w", v.w)
	case *arriveState:
		e.U8(3)
		m.count("claim", len(v.claim))
		for _, x := range v.claim {
			e.U32(x)
		}
		m.u32("gen", v.gen)
		m.u8("moveGround", uint8(v.moveGround))
		m.count("resv", len(v.resv))
		for _, x := range v.resv {
			e.U32(uint32(x))
		}
		m.count("rows", len(v.rows))
		for i := range v.rows {
			writeMovementArriveRow(e, c, s, &v.rows[i], fmt.Sprintf("%s.rows[%d]", p, i))
		}
		m.u8("standby", uint8(v.standby))
	case *pilotsState:
		e.U8(4)
		for i, v := range v {
			m.pilot(fmt.Sprintf("children[%d]", i), v)
		}
	default:
		m.fail("", errors.New("absent pilot table record"))
	}
}

func writeMovementClaimRow(e *checkpoint.Encoder, v *checkpointClaimRow, p string) {
	e.Field(p)
	if v == nil {
		e.Fail(errors.New("absent claim-row table record"))
		return
	}
	if err := validateCheckpointClaimRow(v); err != nil {
		e.Fail(err)
		return
	}
	e.U8(v.kind)
	if v.kind == 1 {
		e.Count(len(v.own))
		for _, x := range v.own {
			e.U32(x)
		}
	} else {
		e.Count(len(v.counts))
		for _, x := range v.counts {
			for _, v := range x {
				e.U8(v)
			}
		}
	}
}
