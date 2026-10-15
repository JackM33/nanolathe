package visibility

// Rules is the gameplay-policy seam owned by visibility. It decides whether
// one jammer suppresses the viewing player's contacts, answers one complete
// unit-visibility request, admits Modern main-view dots and selects the
// coverage refcount arithmetic. Each question is asked at its request
// boundary; implementations hold no session state and draw no random numbers
// [DESIGN_COMMUNITY_PATCH §4.4] [DESIGN_GAMEPLAY_RULES §9].
type Rules interface {
	JammerSuppresses(s *Service, viewer, jammerOwner PlayerID) bool
	Visible(s *Service, viewer PlayerID, target Target) bool
	// MainViewRadarDots admits the Modern presentation/attack feature.
	// The answer is published, never read from live rules by presentation.
	MainViewRadarDots() bool
	// CoverageCountSaturates selects the coverage refcount arithmetic of
	// DESIGN_WORLD_VISIBILITY "Modern coverage saturation". Strict keeps
	// retail's plain uint8 wrap [03 §3.1]; a saturating answer carries the
	// true overlap count in the service's wide row and stores
	// min(count, 255) in the byte grid.
	CoverageCountSaturates() bool
}

// StrictRules is the retail baseline. It is zero size, so binding it and asking
// a question adds no allocation [03 §3.2] [03 R-VIS-01 §5].
type StrictRules struct{}

// CommunityRules is the Community 3.9 layer. It embeds StrictRules so every
// unchanged answer remains retail's [DESIGN_COMMUNITY_PATCH §2].
type CommunityRules struct{ StrictRules }

// ModernRules inherits community visibility and admits main-view sensor dots
// (DESIGN_INTERFACE_HUD_INPUT "Modern radar dots").
type ModernRules struct{ CommunityRules }

// CommunityState is the resolved feature-table projection visibility owns.
// Allied reads the viewing player's alliance row; it is consulted only when
// AlliedJammingIgnored is enabled. The zero value is the Strict answer.
type CommunityState struct {
	AlliedJammingIgnored      bool
	OffMapAircraftMarginTiles int
	allied                    func(viewer, other PlayerID) bool
	// OffMap reads the canonical movement sort-bucket filing for a unit id.
	offMap func(unitID uint16) bool

	// Capture metadata travels with a copied projection but proves only its
	// original Service. Scalars and reader behavior do not depend on it; the
	// proof itself emits no bytes (DESIGN_MULTIPLAYER §16.3.49).
	checkpointAllied checkpointReaderProof
	checkpointOffMap checkpointReaderProof
}

// strictRules is converted once so an unbound Service takes the retail path
// without constructing an interface value per request.
var strictRules Rules = StrictRules{}

func (s *Service) rules() Rules {
	if s == nil || s.Rules == nil {
		return strictRules
	}
	return s.Rules
}

// JammerSuppresses answers the same policy for the sensor pass and a computer
// player's observation. Survival teammates do not jam each other in any rule
// set (DESIGN_SURVIVAL §4.3); otherwise the bound rule owns the answer
// [03 R-VIS-01 §4]. It reads the supplied viewer, not the presentation viewer.
func (s *Service) JammerSuppresses(viewer, jammerOwner PlayerID) bool {
	if !s.rules().JammerSuppresses(s, viewer, jammerOwner) {
		return false
	}
	return s == nil || viewer == jammerOwner || !validPlayer(viewer) || !validPlayer(jammerOwner) || s.team[viewer]&cellBit(jammerOwner) == 0
}

// JammerSuppresses retains retail's owner-only exemption. Pass 3 asks this
// before either jammer callback, so the answer applies equally to radar and
// sonar [03 R-VIS-01 §5].
func (StrictRules) JammerSuppresses(_ *Service, viewer, jammerOwner PlayerID) bool {
	return viewer != jammerOwner
}

// Visible evaluates the complete retail unit predicate.
func (StrictRules) Visible(s *Service, viewer PlayerID, target Target) bool {
	return s.strictVisible(viewer, target)
}

// JammerSuppresses exempts a jammer when the feature is enabled and the
// viewing player's alliance row declares its owner allied. The predicate is
// deliberately one-directional [community patch engine behavior CP-FIX-6].
func (CommunityRules) JammerSuppresses(s *Service, viewer, jammerOwner PlayerID) bool {
	if s == nil || !s.Community.AlliedJammingIgnored || s.Community.AlliedReader() == nil {
		return StrictRules{}.JammerSuppresses(s, viewer, jammerOwner)
	}
	if viewer == jammerOwner {
		return false
	}
	return !s.Community.AlliedReader()(viewer, jammerOwner)
}

// Visible keeps the retail ordered gate unless the community off-map aircraft
// substitute owns this request. The substitute itself is in predicate.go,
// beside the source grids and projection arithmetic it reads
// [community patch engine behavior CP-ENV-1(a)].
func (CommunityRules) Visible(s *Service, viewer PlayerID, target Target) bool {
	if s == nil || s.Community.OffMapAircraftMarginTiles <= 0 {
		return StrictRules{}.Visible(s, viewer, target)
	}
	return s.communityVisible(viewer, target)
}

// MainViewRadarDots keeps the viewport's retail hull-only contact boundary
// [07 R-SEL-02B2]. Community inherits this answer.
func (StrictRules) MainViewRadarDots() bool { return false }

// MainViewRadarDots enables the approved Modern display/attack preference
// (DESIGN_INTERFACE_HUD_INPUT "Modern radar dots").
func (ModernRules) MainViewRadarDots() bool { return true }

// CoverageCountSaturates keeps retail's un-clamped uint8 coverage refcount,
// whose 256-observer wrap reads as fog [03 §3.1]. Community inherits it.
func (StrictRules) CoverageCountSaturates() bool { return false }

// CoverageCountSaturates carries the true overlap count in the wide row and
// stores min(count, 255) in the byte grid (DESIGN_WORLD_VISIBILITY "Modern
// coverage saturation").
func (ModernRules) CoverageCountSaturates() bool { return true }
