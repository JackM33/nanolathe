// Explored-map sharing: the mapping-grid merge of [05 R-SHARE-01 §6].

package visibility

// CopyExplored is the mapping-grid merge [05 R-SHARE-01 §6]: every tile whose
// mapping word carries src's bit gains dst's bit. It is one-directional and
// idempotent — nothing ever clears a copied bit short of a bulk rebuild, so
// switching sharing off removes nothing — and it walks the whole grid with no
// window and no incremental form.
//
// It reads no alliance row, option bit or mode word, touches no current-sight
// byte grid, and consumes no random draw: the merge is the only thing it does
// [05 R-SHARE-01 §6]. In a Mapped battle the grid is already all ones and the
// merge changes nothing; under Permanent line of sight the copied tiles become
// visible as well as explored, and under Circular or True they are explored
// but fogged — both follow from the predicate reading the grid, not from
// anything done here [05 R-SHARE-01 §6] [03 §3.2].
//
// It raises neither presentation dirty flag: the fog cache and the minimap
// composite show the new tiles at their next rebuild for another reason
// [05 R-SHARE-01 §6]. So neither the fog-cache-valid bit nor the mapping
// revision moves here.
//
// Retail's callers resolve a slot first and drop the request when either side
// is the no-slot sentinel [05 R-SHARE-01 §4]; an invalid slot here is
// likewise a no-op. The merge's two callers — the recipient applying a
// periodic share request [05 R-SHARE-01 §3] and the share screen's map gift
// [05 R-SHARE-01 §5] — are the session's (DESIGN_MULTIPLAYER §6.7 Q25).
func (s *Service) CopyExplored(src, dst PlayerID) {
	if s == nil || !validPlayer(src) || !validPlayer(dst) {
		return
	}
	// Bits are one per slot within the sixteen-bit word, slots 0..9 as bits
	// 0..9 [05 R-SHARE-01 §6] [03 §3.1].
	from, to := cellBit(src), cellBit(dst)
	for i, word := range s.wordMask {
		if word&from != 0 {
			s.wordMask[i] = word | to
		}
	}
}
