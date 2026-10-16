package movement

import (
	"errors"
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
)

// Accessor graph discovery follows callback field order and visits inputs
// before their consumer. Only reachable metadata survives replacement.
func checkpointAccessorNodes(c *CheckpointContext, roots *checkpointAccessorRoots) ([]*checkpointAccessorCapture, error) {
	if roots == nil {
		return nil, nil
	}
	if roots.unsupported != "" {
		return nil, errors.New(roots.unsupported)
	}
	if c == nil || c.system == nil {
		return nil, errors.New("missing accessor owner")
	}
	state := make(map[*checkpointAccessorCapture]uint8)
	var nodes []*checkpointAccessorCapture
	var visit func(*checkpointAccessorCapture) error
	visit = func(n *checkpointAccessorCapture) error {
		if n == nil {
			return nil
		}
		if state[n] == 1 {
			return errors.New("cyclic accessor capture")
		}
		if state[n] == 2 {
			return nil
		}
		state[n] = 1
		if err := validateCheckpointAccessorCapture(c.system, n); err != nil {
			return err
		}
		for _, input := range n.inputs {
			if input == nil {
				return errors.New("absent accessor input")
			}
			if err := visit(input); err != nil {
				return err
			}
		}
		state[n] = 2
		nodes = append(nodes, n)
		return nil
	}
	for _, root := range []*checkpointAccessorCapture{roots.cost, roots.leg, roots.passable, roots.revise} {
		if err := visit(root); err != nil {
			return nil, err
		}
	}
	return nodes, nil
}

func checkpointCapturedProfile(v [8]int32) (Profile, error) {
	if v[0] < -32768 || v[0] > 32767 || v[1] < -32768 || v[1] > 32767 {
		return Profile{}, errors.New("captured footprint exceeds source width")
	}
	for _, x := range v[4:] {
		if x < 0 || x > 255 {
			return Profile{}, errors.New("captured slope exceeds source width")
		}
	}
	return Profile{FootPrintX: int16(v[0]), FootPrintZ: int16(v[1]), MaxWaterDepth: v[2], MinWaterDepth: v[3], MaxSlope: uint8(v[4]), BadSlope: uint8(v[5]), MaxWaterSlope: uint8(v[6]), BadWaterSlope: uint8(v[7])}, nil
}

func validateCheckpointAccessorCapture(s *System, n *checkpointAccessorCapture) error {
	v := n.value
	if v.Inputs != nil || v.Layer != 0 || v.Learned != 0 || v.ClaimCounts != 0 || v.ClaimOwn != 0 {
		return errors.New("capture contains prematurely resolved IDs")
	}
	if n.layer != nil && v.Kind != 1 && v.Kind != 4 && v.Kind != 8 && v.Kind != 9 || n.learned != nil && v.Kind != 2 || n.learnedOwner != 0 && v.Kind != 2 || (n.counts != nil || n.own != nil) && v.Kind != 7 || n.system != nil && v.Kind != 5 || (n.registry != nil || n.class != "") && v.Kind != 8 {
		return errors.New("accessor has irrelevant pointer operands")
	}
	switch v.Kind {
	case 1, 4, 8, 9:
		if n.layer == nil {
			return errors.New("missing captured class layer")
		}
		if v.Kind == 1 || v.Kind == 8 || v.Kind == 9 {
			if _, err := checkpointCapturedProfile(v.Profile); err != nil {
				return err
			}
		}
		if v.Kind == 8 {
			if n.registry == nil || n.registry != s.layerRegistry {
				return errors.New("revision registry alias differs")
			}
			key := n.class
			if key == "" {
				profile, err := checkpointCapturedProfile(v.Profile)
				if err != nil {
					return err
				}
				key = scratchLayerKey(profile)
			}
			if n.registry.byName[key] != n.layer {
				return errors.New("revision class lookup differs from captured layer")
			}
		}
	case 2:
		if n.learnedOwner > 9 {
			return errors.New("invalid captured learned owner")
		}
		if n.learned != nil && n.learned != s.learned {
			return errors.New("learned grid alias differs")
		}
	case 3, 6:
	case 5:
		if n.system != s {
			return errors.New("wall receiver differs from movement owner")
		}
	case 7:
		if n.counts == nil || n.own == nil || n.counts.kind != 2 || n.own.kind != 1 {
			return errors.New("claims accessor lacks typed row holders")
		}
		if err := validateCheckpointClaimRow(n.counts); err != nil {
			return err
		}
		if err := validateCheckpointClaimRow(n.own); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported captured accessor kind %d", v.Kind)
	}
	return nil
}

func collectCheckpointAccessors(c *CheckpointContext, roots *checkpointAccessorRoots) (added int, err error) {
	nodes, err := checkpointAccessorNodes(c, roots)
	if err != nil {
		return 0, err
	}
	for _, v := range nodes {
		n, err := movementCheckpointAdd(&c.claimRows, v.counts)
		added += n
		if err != nil {
			return added, err
		}
		n, err = movementCheckpointAdd(&c.claimRows, v.own)
		added += n
		if err != nil {
			return added, err
		}
		n, err = movementCheckpointAdd(&c.Layers, v.layer)
		added += n
		if err != nil {
			return added, err
		}
	}
	return added, nil
}

func lowerCheckpointAccessors(c *CheckpointContext, roots *checkpointAccessorRoots) (path.CheckpointAccessors, error) {
	nodes, err := checkpointAccessorNodes(c, roots)
	if err != nil {
		return path.CheckpointAccessors{}, err
	}
	var out path.CheckpointAccessors
	ids := make(map[*checkpointAccessorCapture]uint32, len(nodes))
	for i, n := range nodes {
		v := n.value
		var ok bool
		v.ClaimCounts, ok = c.claimRows.Find(n.counts)
		if !ok {
			return out, errors.New("undiscovered count row")
		}
		v.ClaimOwn, ok = c.claimRows.Find(n.own)
		if !ok {
			return out, errors.New("undiscovered own row")
		}
		v.Layer, ok = c.Layers.Find(n.layer)
		if !ok {
			return out, errors.New("undiscovered class layer")
		}
		if n.learned != nil {
			v.Learned = checkpoint.ObjectID(n.learnedOwner) + 1
		}
		for _, input := range n.inputs {
			id := ids[input]
			if id == 0 {
				return out, errors.New("accessor input lacks preceding ID")
			}
			v.Inputs = append(v.Inputs, id)
		}
		out.Nodes = append(out.Nodes, v)
		ids[n] = uint32(i + 1)
	}
	if roots != nil {
		out.Cost = ids[roots.cost]
		out.Leg = ids[roots.leg]
		out.Passable = ids[roots.passable]
		out.Revise = ids[roots.revise]
	}
	return out, nil
}
