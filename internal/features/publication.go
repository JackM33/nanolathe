package features

// Publication change tracking: what lets the committed frame keep a feature's
// view instead of rebuilding it every tick (DESIGN_RUNTIME_DETERMINISM §2.5
// "Feature publication"). None of it is simulation state.
//
// A record's committed view is built from its own fields, its runtime record,
// its animation cursor, the plot placer nibble written at load, and the
// session's wreck-heat table. While a record is on the active list its view
// can change on every visit — a sinking wreck's height, an animation's frame —
// so a publication compares it in full. Off the list, every change to those
// inputs passes through one of the points that renew its revision: joining or
// leaving the list (ignition, a die or reclaim transition, a stamp, the
// dormant move, a submerged start), taking or releasing its runtime record,
// and the wreck-heat table's own mark. A removed or replaced record is a
// different record, and a reset of the whole set changes the epoch.

// touchPublication renews inst's publication revision.
func (s *Service) touchPublication(inst *Instance) {
	if s == nil || inst == nil {
		return
	}
	s.publicationRevision++
	inst.publication = s.publicationRevision
}

// PublicationState reports the record's publication revision and whether it
// is on the active list. A publication that built the record's view at the
// same revision while it was off the list may keep that view.
func (i *Instance) PublicationState() (revision uint64, active bool) {
	if i == nil {
		return 0, false
	}
	return i.publication, i.onActive
}

// MarkPublicationChanged renews inst's publication revision for a change to a
// presentation input kept outside the record — the session's wreck-heat
// table. It writes nothing a simulation read consults.
func (s *Service) MarkPublicationChanged(inst *Instance) { s.touchPublication(inst) }

// PublicationEpoch changes whenever the whole instance set is replaced, so a
// publication cache built against the previous set compares every record.
func (s *Service) PublicationEpoch() uint64 {
	if s == nil {
		return 0
	}
	return s.publicationEpoch
}
