package movement

import "github.com/nanolathe-gg/nanolathe/internal/pool"

// Nanolathe Modern parallel path batches: each search reads only its own
// requester's footprint as open (docs/DESIGN_MOVEMENT_PATH.md "Modern parallel
// path batches", "Own footprint"). Strict 3.1 and Community 3.9 keep the retail
// single-request scheduler and never reach any of this.
//
// Retail opens one search at a time. Its request revision pass makes the
// requester's own footprint passable in the class layer every request of the
// class shares, by classifying the requester's rectangle under a temporarily
// current commit tick, and the request's release walls it again
// [04 R-PATH-01 §14]. No other search runs in between, so no other search reads
// that transparency. The batch scheduler opens several searches before any of
// them publishes, and each would read the footprints of the others opened
// before it as open. A search the batch opens therefore runs the revision pass
// with no requester — the shared layer walls the requester as it walls every
// other unit whose commit tick the watermark has passed — and reads its own
// footprint through the view below.

// ownFootprint is one batch search's reading of its requester's occupancy.
// Within the anchors whose footprint or classifier ring can read a cell of the
// requester's committed footprint — the rectangle the revision pass restamps
// for its requester [04 R-MOV-03 §3] — an anchor the shared layer does not
// read clear is classified again as the restamp classifies it, with the
// requester's occupancy passing the occupant-age gate as the revision pass's
// temporarily current tick lets it pass. Every other anchor, and every other
// unit's occupancy, reads as the shared layer reads it.
//
// The view is a pure read of the terrain, the occupancy grid, the layer's
// commit row and watermark and the mover port, none of which anything writes
// while a batch round expands searches, so a worker goroutine may evaluate
// it. It is fixed when the search opens and never written afterwards.
type ownFootprint struct {
	requester pool.Handle
	// minX, minZ, maxX and maxZ bound the anchor rectangle, inclusively: the
	// committed anchor less the class footprint on the low sides, plus the
	// committed footprint on the high sides.
	minX, minZ, maxX, maxZ int32
}

// ownFootprintFor is the own reading of requester's footprint for a search
// over layer, or nil when the requester has no committed footprint, which
// leaves nothing of its own to read.
func (s *System) ownFootprintFor(requester pool.Handle, layer *ClassLayer) *ownFootprint {
	if layer == nil || requester == 0 {
		return nil
	}
	anchor, fx, fz, ok := s.CommittedFootprint(requester)
	if !ok {
		return nil
	}
	rx, rz := layer.footprintSize()
	return &ownFootprint{
		requester: requester,
		minX:      anchor.X - rx,
		minZ:      anchor.Z - rz,
		maxX:      anchor.X + int32(fx),
		maxZ:      anchor.Z + int32(fz),
	}
}

// valueOwn is the layer's stamped value at anchor (x, z) read through own: the
// shared value, except within own's rectangle where that value is not clear.
// A clear value cannot change: reading the requester's occupancy as fresh only
// ever opens cells.
func (l *ClassLayer) valueOwn(x, z int32, own *ownFootprint) uint8 {
	v := l.Value(x, z)
	if own == nil || v == LayerClear || x < own.minX || x > own.maxX || z < own.minZ || z > own.maxZ {
		return v
	}
	return l.ownValue(x, z, own.requester)
}

// ownValue classifies one anchor as RestampRect does, with fresh's occupancy
// passing the occupant-age gate: blocked at the restamp's own edge bound,
// otherwise the minimum tier over the footprint, a clear result demoted to
// steep when any cell of the surrounding one-cell ring is not clear
// [04 R-SLOPE-01 §3][04 R-PATH-01 §2][04 R-MOV-03 §3]. It is the per-anchor form
// of classifyTiers and keeps no scratch.
func (l *ClassLayer) ownValue(x, z int32, fresh pool.Handle) uint8 {
	if x < 0 || z < 0 || x >= l.W || z >= l.H {
		return LayerBlocked
	}
	fx, fz := l.footprintSize()
	if x+fx >= l.W || z+fz >= l.H {
		return LayerBlocked // the restamp's own edge bound
	}
	result := LayerClear
	for cz := z; cz < z+fz; cz++ {
		for cx := x; cx < x+fx; cx++ {
			switch l.classifyCellFresh(cx, cz, fresh) {
			case LayerBlocked:
				return LayerBlocked
			case LayerSteep:
				result = LayerSteep
			}
		}
	}
	if result != LayerClear {
		return result
	}
	for cz := z - 1; cz <= z+fz; cz++ {
		for cx := x - 1; cx <= x+fx; cx++ {
			if cz >= z && cz < z+fz && cx >= x && cx < x+fx {
				continue // the footprint, clear above
			}
			if l.classifyCellFresh(cx, cz, fresh) != LayerClear {
				return LayerSteep
			}
		}
	}
	return LayerClear
}
