package economy

import (
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Activity names the work shown in the Modern resource breakdown. Other is
// reporting-only: restored, unobserved and player-level activity has no unit
// operation that a temporary hold could safely stop.
type Activity uint8

const (
	ActivityOther Activity = iota
	ActivityGeneration
	ActivityConstruction
	ActivityWeapons
	ActivityUpkeep
	ActivityHealing
	ActivityCloaking
	ActivityReclaim
	ActivityCount
)

// Rules owns economy's Modern activity suspension policy. The ledger's retail
// arithmetic is shared by every implementation (DESIGN_ECONOMY_CONSTRUCTION).
type Rules interface{ TemporaryActivityHolds() bool }

type StrictRules struct{}
type CommunityRules struct{ StrictRules }
type ModernRules struct{ CommunityRules }

func (StrictRules) TemporaryActivityHolds() bool { return false }
func (ModernRules) TemporaryActivityHolds() bool { return true }

// ActivityHeld reports a live owner/category hold until explicitly resumed.
// A nil rules binding is the retail default.
func (s *Service) ActivityHeld(owner uint8, activity Activity) bool {
	if s == nil || s.Rules == nil || !s.Rules.TemporaryActivityHolds() || owner >= 10 || activity <= ActivityOther || activity >= ActivityCount {
		return false
	}
	return s.HoldActive[owner][activity]
}

// ActivityContribution is a detached last-settlement observation. Requested
// is ledger demand, including refused deferred work; it is not paid spending.
// Unit zero identifies the player mirror or an unattributed residual.
type ActivityContribution struct {
	Unit                pool.UnitRef
	Activity            Activity
	Produced, Requested [2]float32
}

type activityReport struct {
	units    []activityAccount
	archived [10][]ActivityContribution
	settled  [10]uint32
}

type activityAccount struct {
	unit  pool.UnitRef
	owner uint8
	flows [ActivityCount]activityAmounts
}

type activityAmounts struct {
	produced, requested [2]float32
}

// ActivitySnapshot copies the live inputs before a resource writer. A disabled
// report is an inert read and never grows the authoritative bucket arena.
func (s *Service) ActivitySnapshot(handle pool.Handle) [2]Bucket {
	if s == nil || s.activityReport == nil || handle == 0 || int(handle) >= len(s.unitBuckets) {
		return [2]Bucket{}
	}
	return s.unitBuckets[handle].Buckets
}

// ObserveActivity records only the stored differences after an existing
// resource writer. It neither repeats the writer's arithmetic nor infers an
// activity from an order that may already have completed. Allocation identity
// keeps immediate slot reuse from relabelling the previous occupant's work.
func (s *Service) ObserveActivity(u *units.Unit, activity Activity, before [2]Bucket) {
	if s == nil || s.activityReport == nil || u == nil || u.Handle == 0 || activity <= ActivityOther || activity >= ActivityCount || int(u.Handle) >= len(s.unitBuckets) {
		return
	}
	var delta ActivityContribution
	for res := Metal; res <= Energy; res++ {
		after := s.unitBuckets[u.Handle].Buckets[res]
		delta.Produced[res] = after.Production - before[res].Production
		delta.Requested[res] = after.Requested - before[res].Requested
	}
	if !activityContributionNonzero(delta) {
		return
	}
	ref := pool.UnitRef{Handle: u.Handle, Serial: u.AllocationSerial}
	r := s.activityReport
	if int(u.Handle) >= len(r.units) {
		r.units = append(r.units, make([]activityAccount, int(u.Handle)+1-len(r.units))...)
	}
	account := &r.units[u.Handle]
	if account.unit != ref || account.owner != u.Owner {
		*account = activityAccount{unit: ref, owner: u.Owner}
	}
	flow := &account.flows[activity]
	for res := Metal; res <= Energy; res++ {
		flow.produced[res] += delta.Produced[res]
		flow.requested[res] += delta.Requested[res]
	}
}

func activityContributionNonzero(c ActivityContribution) bool {
	return c.Produced[Metal] != 0 || c.Produced[Energy] != 0 || c.Requested[Metal] != 0 || c.Requested[Energy] != 0
}

// archiveActivityReport follows the settlement's exact live membership. Work
// on a unit removed before the pass is absent just as its buckets are absent;
// restored/unobserved inputs and fold-rounding differences remain Other. The
// authoritative pass totals, not the observations, always settle the player.
func (s *Service) archiveActivityReport(player int, tick uint32, w *units.World, totals [2]resourceTotals) {
	if s.activityReport == nil {
		return
	}
	r := s.activityReport
	out := r.archived[player][:0]
	var sum ActivityContribution
	appendFlow := func(flow ActivityContribution) {
		if !activityContributionNonzero(flow) {
			return
		}
		out = append(out, flow)
		for res := Metal; res <= Energy; res++ {
			sum.Produced[res] += flow.Produced[res]
			sum.Requested[res] += flow.Requested[res]
		}
	}
	ForEachUnitOrdered(w, player, func(u *units.Unit) {
		ref := pool.UnitRef{Handle: u.Handle, Serial: u.AllocationSerial}
		var known ActivityContribution
		if int(u.Handle) < len(r.units) {
			account := &r.units[u.Handle]
			if account.unit == ref && account.owner == u.Owner {
				for activity := ActivityGeneration; activity < ActivityCount; activity++ {
					amounts := account.flows[activity]
					flow := ActivityContribution{Unit: ref, Activity: activity, Produced: amounts.produced, Requested: amounts.requested}
					appendFlow(flow)
					for res := Metal; res <= Energy; res++ {
						known.Produced[res] += flow.Produced[res]
						known.Requested[res] += flow.Requested[res]
					}
				}
			}
			*account = activityAccount{unit: ref, owner: u.Owner}
		}
		other := ActivityContribution{Unit: ref, Activity: ActivityOther}
		for res := Metal; res <= Energy; res++ {
			b := s.unitBuckets[u.Handle].Buckets[res]
			other.Produced[res] = b.Production - known.Produced[res]
			other.Requested[res] = b.Requested - known.Requested[res]
		}
		appendFlow(other)
	})
	other := ActivityContribution{Activity: ActivityOther}
	for res := Metal; res <= Energy; res++ {
		other.Produced[res] = totals[res].production - sum.Produced[res]
		other.Requested[res] = totals[res].requested - sum.Requested[res]
	}
	appendFlow(other)
	r.archived[player], r.settled[player] = out, tick
}

// EnableActivityReport enables optional presentation observations. It neither
// samples nor changes the ledger and is safe to call more than once.
func (s *Service) EnableActivityReport() {
	if s != nil && s.activityReport == nil {
		s.activityReport = &activityReport{}
	}
}

// AppendActivityReport appends the latest settlement report to caller-owned
// storage, allowing publication to reuse its buffer. Observations are not save
// or checkpoint state.
func (s *Service) AppendActivityReport(dst []ActivityContribution, owner uint8) []ActivityContribution {
	if s == nil || s.activityReport == nil || owner >= 10 {
		return dst
	}
	return append(dst, s.activityReport.archived[owner]...)
}

// ActivityReportTick is the settlement tick of AppendActivityReport, or zero before
// any report was committed.
func (s *Service) ActivityReportTick(owner uint8) uint32 {
	if s == nil || s.activityReport == nil || owner >= 10 {
		return 0
	}
	return s.activityReport.settled[owner]
}
