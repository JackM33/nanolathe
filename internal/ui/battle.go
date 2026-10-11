package ui

import (
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// Panel detents and throttle are authored battle-rail values [07 §6].  The
// timing is presentation-only; it never enters the simulation clock [I6].
const (
	// BattleEntryMode is the established production battle-composition mode
	// value. Its 0x04 bit starts the command panel visible [07 §6] C13; it is
	// not the unrelated digit-routing mode bit.
	BattleEntryMode byte   = 0x04
	PanelParked     int8   = -31
	PanelVisible    int8   = 0
	PanelThrottleMs uint32 = 15
)

// BattleModal is the authored in-battle modal window currently at the top of
// the modal chain. The chain is ARMOPT -> EXITMENU -> YESORNO [07 "Tab
// options menu and manual exit"].
type BattleModal uint8

const (
	BattleModalClosed BattleModal = iota
	BattleModalOptions
	BattleModalExit
	BattleModalConfirmMain
	BattleModalConfirmExit
	// BattleModalRestart replaces EXITMENU while the authored RESTART.GUI
	// child is open. ARMOPT remains the pause-owning root underneath it
	// [07 R-FE-01 §7][08 R-CAMP-01 §8].
	BattleModalRestart
	// ARMOPT's two read-only children. `MISSION` opens BRIEFING.GUI in a
	// campaign and GAMEOPTIONS.GUI otherwise; `HELP` opens HELP.GUI. Each one
	// opens over the surviving options root, which keeps the pause bit it set,
	// and its `OK` returns to that root [07 R-FE-01 §7][07 R-WGT-01 §1].
	BattleModalBriefing
	BattleModalGameOptions
	BattleModalHelp
	// The networked session's own windows: the Tab strip `TABMENU.GUI`, the
	// transfer dialog `SHARE.GUI` and the allies window `ALLIES.GUI`
	// [07 "Tab options menu and manual exit"][07 R-HUD-03 §9]
	// [05 R-SHARE-01 §5]. None is `ARMOPT` or sits over it, and none sets the
	// pause bit: retail's network mode takes no local pause [07 §11].
	BattleModalTabMenu
	BattleModalShare
	BattleModalAllies
)

// SharingModal reports the networked session's windows, which open and close
// without the options root or its pause intent.
func SharingModal(m BattleModal) bool {
	return m == BattleModalTabMenu || m == BattleModalShare || m == BattleModalAllies
}

// BattleModalAction is the concrete result of activating a modal control.
// Navigation remains owned by the battle composition root; UI state only
// reports the authored choice [07 "Tab options menu and manual exit"].
type BattleModalAction uint8

const (
	BattleModalActionNone BattleModalAction = iota
	BattleModalActionMainMenu
	BattleModalActionExitGame
	// ARMOPT routes LOADGAME and SAVEGAME to the two LOADGAME.GUI modes. Both
	// gadgets are available in a campaign (session kind 1) and a skirmish
	// (kind 2); only network multiplayer greys them, and this build has no
	// network session [07 R-FE-01 §7] [08 R-SAVE-02 §4].
	BattleModalActionSaveGame
	BattleModalActionLoadGame
	// ARMOPT's PREFS opens the options root as a child window over the battle.
	// ARMOPT stays on the modal chain underneath and the pause bit it set stays
	// set, so this reports the request and emits no schedule intent
	// [07 R-FE-01 §6][07 R-FE-01 §7].
	BattleModalActionPrefs
	// RESTART.GUI owns only a three-stage difficulty selector and the
	// request-producing button. The battle composition supplies the stage and
	// consumes the request because both cross the session boundary.
	BattleModalActionRestartDifficulty
	BattleModalActionRestart
	// ARMOPT's `MISSION` and `HELP` each open a read-only child window whose
	// rows the composition root fills from the running session and from
	// `gamedata\help.tdf`. The modal transition has already happened when the
	// action is reported; the action is the request to build that content
	// [07 R-FE-01 §7][08 R-SKIR-01 §11].
	BattleModalActionMission
	BattleModalActionHelp
	// HELP.GUI's three-stage `Page` button refills the window with page `p`,
	// and a click on BRIEFING.GUI's `TextRegion` or `MOREBAR` advances its
	// pager. Both are content requests on an already-open child
	// [07 R-FE-01 §7][07 R-HUD-03 §10].
	BattleModalActionHelpPage
	BattleModalActionBriefingPage
)

// BattleScheduleIntent is a plain presentation value. Session applies it at
// the scheduling boundary synchronously, so a pause transition cannot wait
// for a simulation tick [01 §4.3][07 §11].
type BattleScheduleIntent struct {
	PauseSet   bool
	Pause      bool
	SpeedDelta int
}

// BattleInputState is the single owner of mutable battle presentation/input
// state. It contains no simulation handles or services: the command adapter
// reads this value, turns gestures into semantic commands, applies the local
// interface ones (selection, build pages, group recall, BigBrother, Shift) to
// the host's local interface state (DESIGN_MULTIPLAYER §7.3), and leaves the
// rest to Session.EnqueueHumanCommand [01 §4.4][07 §3][07 §9].
//
// Keeping the gesture latches together is important. A HUD press, placement
// press, or world drag owns the complete button gesture; separate owners can
// otherwise observe the same release and issue two actions.
type BattleInputState struct {
	Latch input.Latch

	DragActive     bool
	DragPressClock uint32
	// A drag records both endpoints as whole three-component WORLD points, and
	// the band — the tested rectangle and the drawn box alike — is those two
	// points projected at the moment the band is used [07 §9 "Drag-rectangle
	// conversion is closed"]. No screen pair is kept: one recorded in the
	// press-time screen frame slides off the terrain as soon as the camera
	// moves during the gesture, which is the common case of dragging into the
	// edge-scroll strip.
	DragStartWorldX, DragStartWorldY, DragStartWorldZ int32
	DragEndWorldX, DragEndWorldY, DragEndWorldZ       int32
	HUDCaptured                                       bool
	HUDPressX                                         int32
	HUDPressY                                         int32
	PlaceCaptured                                     bool
	ShiftHeld                                         bool
	ShiftLatchSticky                                  bool
	PointerX                                          int32
	PointerY                                          int32
	PrevMouseX                                        float32
	PrevMouseY                                        float32

	BuildDef        string
	BuildFootX      int32
	BuildFootZ      int32
	BuildOK         bool
	BuildNeedsClear bool
	// BuildSnapPreview reports that the community click-snap preview owns
	// the armed site: an extractor centred over a deposit found within the
	// snap radius, or a geothermal the snap moved. The patch draws that site
	// with its own physical preview colours and switches the engine's ghost
	// off (community patch engine CP-CON-6).
	BuildSnapPreview bool
	BuildMX          int32
	BuildMY          int32
	BuildCellX       int32
	BuildCellZ       int32
	BuildSiteH       int32
	BuildSticky      bool

	ResultDismissed bool
}

// BattleState owns all mutable authored battle-interface state that is not
// simulation state: modal windows, latches, gestures, placement, status, and
// result dismissal. The command composition layer only adapts this state to
// the host's local interface state and Session.EnqueueHumanCommand
// [07 §3][07 §9].
type BattleState struct {
	modal        BattleModal
	pressed      int
	pressedModal BattleModal
	// stripBeneath records that the networked session's Tab strip is still
	// open beneath the top window, which one of its buttons opened: SHARE,
	// ALLIES or the options root. Closing that window makes the strip the
	// top window again [07 "Tab options menu and manual exit"]. Only a window
	// opened over the strip sets it, so a single-player battle never does.
	stripBeneath bool
	// paused is the UI's synchronous pause truth. It is initialized to the
	// running production state and updated only by the scheduling boundary;
	// committed frames may synchronize it until that first UI-issued pause
	// transition, because a paused session can publish no newer tick [01 §4.3]
	// [07 §11].
	paused              bool
	pauseScheduleIssued bool
	// campaign is doc 08's session kind reduced to the one distinction ARMOPT
	// makes: kind 1 (a campaign mission) sends `MISSION` to BRIEFING.GUI, and
	// every other kind sends it to GAMEOPTIONS.GUI [07 R-FE-01 §7].
	campaign bool

	// Input is intentionally public as a plain value so the cmd adapter can
	// render and update it without introducing another state bridge. No mutable
	// simulation state is reachable through this value [I6].
	Input BattleInputState

	// PanelOffset, PanelTarget, and PanelDeadline are the sole owner of the
	// §6 slide. Only the input / host-frame update advances it [07 §6][I6].
	//
	// What the offset moves is the bottom slide strip — `Game Time` /
	// `Total Units` / `Game Speed` — which "slides up from the bottom edge of
	// the view when Space is held" [07 R-HUD-03 §1 "the panel-slide gate"] and
	// is drawn at `(x, yBottom + off)`, off screen at 0 [07 R-HUD-04 §4]. It is
	// NOT a side-rail offset: PANELSIDE is stamped at (0,0) and nowhere else,
	// and "every rail window and gadget rectangle" is fixed in authored
	// coordinates [07 R-HUD-05]. The one reader is the composer's slide-strip
	// draw; nothing else may translate art or a hit test by this word
	// (WU-19-223).
	//
	// PanelParked/PanelVisible keep the names §6's parenthetical labels gave
	// them (-31 "parked", 0 "fully visible"); by the two later closures above
	// the strip is fully drawn at -31 and invisible at 0. The arithmetic is the
	// same under either label, so the constant names are left alone.
	// TODO(question): the deadline remains per battle as a host boundary until
	// the remaining reset/rebuild writers close its retail lifetime [07 §6].
	PanelOffset   int8
	PanelTarget   int8
	PanelDeadline uint32
	panelCue      func(string)
}

// NewBattleState returns a closed modal state with no captured press. The
// entering session mode byte is the sole source for the initial rail detent:
// bit 0x04 means visible, while a clear bit means parked [07 §6] C13.
func NewBattleState(modeByte byte) *BattleState {
	offset := PanelParked
	if modeByte&0x04 != 0 {
		offset = PanelVisible
	}
	return &BattleState{modal: BattleModalClosed, pressed: -1, paused: false, Input: BattleInputState{Latch: input.LatchNormal}, PanelOffset: offset, PanelTarget: offset}
}

// NewProductionBattleState applies the established battle-entry composition
// value. Keeping this in ui makes every production construction path use the
// same canonical initialization while tests can still exercise both mode
// polarities through NewBattleState [07 §6] C13.
func NewProductionBattleState() *BattleState {
	return NewBattleState(BattleEntryMode)
}

// SetPanelCue installs the optional authored cue sink. UI owns when a cue is
// due — a rail detent transition or a recognised options action — and the
// composition root owns how the alias is played, so no interface sound is
// reached from this layer [07 §6][07 R-WGT-01 §3].
func (s *BattleState) SetPanelCue(cue func(string)) {
	if s != nil {
		s.panelCue = cue
	}
}

// playCue hands one authored alias to the installed sink. A state composed
// without a sink is silent, as a battle composed without audio is.
func (s *BattleState) playCue(alias string) {
	if s == nil || s.panelCue == nil || alias == "" {
		return
	}
	s.panelCue(alias)
}

// SetPanelTarget applies the retail Space/editor polarity [07 §6] C14.
func (s *BattleState) SetPanelTarget(spaceHeld, editorFocused bool) {
	if s == nil {
		return
	}
	if spaceHeld && !editorFocused {
		s.PanelTarget = PanelParked
	} else {
		s.PanelTarget = PanelVisible
	}
}

// AdvancePanel uses one explicit timestamp for both clock samples. Production
// uses AdvancePanelWithClock so an admitted visit takes a fresh sample [07 §6].
func (s *BattleState) AdvancePanel(now uint32, spaceHeld, editorFocused bool) {
	s.advancePanel(func() uint32 { return now }, spaceHeld, editorFocused)
}

// AdvancePanelWithClock reads admission time and, only on admission, a second
// time for the next deadline. The source must share the deadline's origin;
// low Unix milliseconds with a fresh zero deadline are not equivalent [07 §6].
func (s *BattleState) AdvancePanelWithClock(source clock.MillisSource, spaceHeld, editorFocused bool) {
	if source == nil {
		return
	}
	s.advancePanel(source.Millis32, spaceHeld, editorFocused)
}

func (s *BattleState) advancePanel(sample func() uint32, spaceHeld, editorFocused bool) {
	if s == nil {
		return
	}
	if int32(sample()) <= int32(s.PanelDeadline) {
		return
	}
	// Refresh before inspecting input, including a visit already at a detent.
	s.PanelDeadline = sample() + PanelThrottleMs
	s.SetPanelTarget(spaceHeld, editorFocused)
	if s.PanelOffset == s.PanelTarget {
		return
	}
	if (s.PanelOffset == PanelParked && s.PanelTarget == PanelVisible) || (s.PanelOffset == PanelVisible && s.PanelTarget == PanelParked) {
		if s.panelCue != nil {
			s.panelCue("Panel")
		}
	}
	remaining := int(s.PanelTarget) - int(s.PanelOffset)
	step := remaining / 3
	if step == 0 {
		if remaining > 0 {
			step = 1
		} else {
			step = -1
		}
	}
	next := int(s.PanelOffset) + step
	if remaining > 0 && next > int(s.PanelTarget) {
		next = int(s.PanelTarget)
	}
	if remaining < 0 && next < int(s.PanelTarget) {
		next = int(s.PanelTarget)
	}
	previous := s.PanelOffset
	s.PanelOffset = int8(next)
	if s.PanelOffset != previous && (s.PanelOffset == PanelVisible || s.PanelOffset == PanelParked) && s.panelCue != nil {
		s.panelCue("Options")
	}
}

// Latch returns the currently armed semantic order. A nil state is idle.
func (s *BattleState) Latch() input.Latch {
	if s == nil || !s.Input.Latch.IsValid() {
		return input.LatchNormal
	}
	return s.Input.Latch
}

// SetLatch changes the semantic order latch. Invalid values are rejected so a
// malformed button name cannot leak an arbitrary dispatcher index into the
// command adapter [07 §9].
func (s *BattleState) SetLatch(l input.Latch) bool {
	if s == nil || !l.IsValid() {
		return false
	}
	if l != input.LatchMobileBuild {
		s.ClearPlacement()
	}
	s.Input.Latch = l
	return true
}

// ResetInteraction clears in-flight gesture state and returns the order latch
// to idle. Placement data is cleared too, matching the retail cancel path
// [07 §9].
func (s *BattleState) ResetInteraction() {
	if s == nil {
		return
	}
	// Reinitialize the complete presentation/input value so no stale pointer,
	// modifier, status, result, or gesture bit survives a modal/result reset.
	s.Input = BattleInputState{Latch: input.LatchNormal}
}

// ArmPlacement records the authored product and footprint for the mobile
// build ghost. The caller supplies compiled content values; UI never invents
// a product or validates simulation placement [07 §9].
func (s *BattleState) ArmPlacement(product string, footX, footZ int32) {
	if s == nil {
		return
	}
	s.Input.BuildDef = product
	s.Input.BuildFootX, s.Input.BuildFootZ = footX, footZ
	s.Input.BuildOK = false
	s.Input.BuildNeedsClear = false
	s.Input.BuildSnapPreview = false
	s.Input.BuildSticky = false
	s.Input.Latch = input.LatchMobileBuild
}

// ClearPlacement disarms the mobile-build ghost and clears its derived site.
func (s *BattleState) ClearPlacement() {
	if s == nil {
		return
	}
	s.Input.BuildDef = ""
	s.Input.BuildFootX, s.Input.BuildFootZ = 0, 0
	s.Input.BuildOK = false
	s.Input.BuildNeedsClear = false
	s.Input.BuildSnapPreview = false
	s.Input.BuildMX, s.Input.BuildMY = 0, 0
	s.Input.BuildCellX, s.Input.BuildCellZ = 0, 0
	s.Input.BuildSiteH = 0
	s.Input.BuildSticky = false
	if s.Input.Latch == input.LatchMobileBuild {
		s.Input.Latch = input.LatchNormal
	}
}

// Modal reports the top authored modal window.
func (s *BattleState) Modal() BattleModal {
	if s == nil {
		return BattleModalClosed
	}
	return s.modal
}

// HasOptionsLayer reports whether ARMOPT remains visible beneath the active
// child modal. The root survives exit/confirmation replacement [07 R-FE-01 §7].
// The networked session's windows are not ARMOPT's children.
func (s *BattleState) HasOptionsLayer() bool {
	return s != nil && s.modal != BattleModalClosed && !SharingModal(s.modal)
}

// OpenSharing makes one of the networked session's windows the top modal. It
// emits no schedule intent: those windows never pause [07 §11]. A window
// opened while the Tab strip is on top opens over it, and the strip stays
// open beneath [07 "Tab options menu and manual exit"]. Any other value is
// refused.
func (s *BattleState) OpenSharing(m BattleModal) bool {
	if s == nil || !SharingModal(m) {
		return false
	}
	s.stripBeneath = m != BattleModalTabMenu && s.modal == BattleModalTabMenu
	s.modal = m
	s.ClearModalPress()
	return true
}

// CloseSharing closes the networked session's window on top, if one is, again
// with no schedule intent. A strip beneath it becomes the top window again.
func (s *BattleState) CloseSharing() {
	if s != nil && SharingModal(s.modal) {
		s.modal = BattleModalClosed
		if s.stripBeneath {
			s.modal = BattleModalTabMenu
		}
		s.stripBeneath = false
		s.ClearModalPress()
	}
}

// DropSharing closes every one of the networked session's windows, a strip
// beneath another window included, and leaves an options root on top open.
func (s *BattleState) DropSharing() {
	if s == nil {
		return
	}
	s.stripBeneath = false
	if SharingModal(s.modal) {
		s.modal = BattleModalClosed
		s.ClearModalPress()
	}
}

// StripBeneath reports that the Tab strip is open beneath the top window.
func (s *BattleState) StripBeneath() bool { return s != nil && s.stripBeneath }

// HasExitLayer reports whether EXITMENU itself is open. Its callback closes
// it before opening YESORNO [07 R-FE-01 §7].
func (s *BattleState) HasExitLayer() bool {
	if s == nil {
		return false
	}
	return s.modal == BattleModalExit
}

// ConfirmTitle selects the authored confirmation title variant.
func (s *BattleState) ConfirmTitle() string {
	if s == nil {
		return ""
	}
	switch s.modal {
	case BattleModalConfirmMain:
		return "Surrender this battle and return to main menu?"
	case BattleModalConfirmExit:
		return "Surrender this battle and exit to Windows?"
	default:
		return ""
	}
}

// OpenOptions opens ARMOPT and emits the synchronous local pause intent.
// Opened by the Tab strip's `OPTIONS`, it opens over the strip, which stays
// open beneath [07 "Tab options menu and manual exit"].
func (s *BattleState) OpenOptions() BattleScheduleIntent {
	if s == nil {
		return BattleScheduleIntent{}
	}
	if s.modal == BattleModalTabMenu {
		s.stripBeneath = true
	}
	s.modal = BattleModalOptions
	s.ClearModalPress()
	return BattleScheduleIntent{PauseSet: true, Pause: true}
}

// CloseOptions closes the complete modal chain and emits the synchronous
// resume intent. This is the only modal transition that resumes battle time.
// A Tab strip the chain was opened from becomes the top window again.
func (s *BattleState) CloseOptions() BattleScheduleIntent {
	if s == nil {
		return BattleScheduleIntent{}
	}
	s.modal = BattleModalClosed
	if s.stripBeneath {
		s.modal = BattleModalTabMenu
	}
	s.stripBeneath = false
	s.ClearModalPress()
	return BattleScheduleIntent{PauseSet: true, Pause: false}
}

// Back applies the authored Escape/back transition and returns the pause
// intent only when the root options window is closed.
func (s *BattleState) Back() BattleScheduleIntent {
	if s == nil {
		return BattleScheduleIntent{}
	}
	s.ClearModalPress()
	switch s.modal {
	case BattleModalOptions:
		return s.CloseOptions()
	case BattleModalConfirmMain, BattleModalConfirmExit, BattleModalRestart,
		BattleModalBriefing, BattleModalGameOptions, BattleModalHelp:
		s.modal = BattleModalOptions
	case BattleModalExit:
		s.modal = BattleModalOptions
	case BattleModalTabMenu, BattleModalShare, BattleModalAllies:
		// Not a child of the options root: nothing to resume. A window over
		// the Tab strip returns to the strip.
		s.CloseSharing()
	}
	return BattleScheduleIntent{}
}

// SetCampaign records whether the running battle is a campaign mission. It is
// the one session fact ARMOPT's `MISSION` branch reads, and the same fact its
// opener reads to relabel the button `Settings` [07 R-FE-01 §7].
func (s *BattleState) SetCampaign(campaign bool) {
	if s != nil {
		s.campaign = campaign
	}
}

// Campaign reports the recorded session kind distinction.
func (s *BattleState) Campaign() bool { return s != nil && s.campaign }

// ShowBriefing, ShowGameOptions and ShowHelp push ARMOPT's read-only children
// over the surviving options root [07 R-FE-01 §7].
func (s *BattleState) ShowBriefing() {
	if s != nil && s.modal == BattleModalOptions {
		s.modal = BattleModalBriefing
		s.ClearModalPress()
	}
}

func (s *BattleState) ShowGameOptions() {
	if s != nil && s.modal == BattleModalOptions {
		s.modal = BattleModalGameOptions
		s.ClearModalPress()
	}
}

func (s *BattleState) ShowHelp() {
	if s != nil && s.modal == BattleModalOptions {
		s.modal = BattleModalHelp
		s.ClearModalPress()
	}
}

// ShowExit pushes EXITMENU over ARMOPT.
func (s *BattleState) ShowExit() {
	if s != nil && s.modal == BattleModalOptions {
		s.modal = BattleModalExit
		s.ClearModalPress()
	}
}

// ShowConfirmation replaces EXITMENU with YESORNO above the surviving options
// root [07 R-FE-01 §7].
func (s *BattleState) ShowConfirmation(mainMenu bool) {
	if s == nil || s.modal != BattleModalExit {
		return
	}
	if mainMenu {
		s.modal = BattleModalConfirmMain
	} else {
		s.modal = BattleModalConfirmExit
	}
	s.ClearModalPress()
}

// ShowRestart replaces EXITMENU with RESTART.GUI above the surviving options
// root. The caller has already established that this is a campaign or
// skirmish battle; multiplayer never reaches this branch [07 R-FE-01 §7].
func (s *BattleState) ShowRestart() {
	if s == nil || s.modal != BattleModalExit {
		return
	}
	s.modal = BattleModalRestart
	s.ClearModalPress()
}

// Activate applies one authored modal button and returns a concrete action for
// the composition owner. Only CHOICE1 commits YESORNO [07 §11].
func (s *BattleState) Activate(name string) BattleModalAction {
	if s == nil {
		return BattleModalActionNone
	}
	name = gui.CallbackName(name)
	switch s.modal {
	case BattleModalOptions:
		// Every `ARMOPT` button plays the `Options` cue before it runs its
		// route, `OK` included, and the child openers run after that cue
		// [07 R-FE-01 §7]. A name the window does not author reaches no route
		// and so plays nothing.
		switch name {
		case "OK", "CANCEL", "EXIT", "SAVEGAME", "LOADGAME", "PREFS", "MISSION", "HELP":
			s.playCue("Options")
		}
		switch name {
		case "OK", "CANCEL":
			s.CloseOptions()
		case "EXIT":
			s.ShowExit()
		case "SAVEGAME":
			// The dialog opens over ARMOPT, which stays on the modal chain
			// [07 R-FE-01 §7] [07 R-FE-01 §8].
			return BattleModalActionSaveGame
		case "LOADGAME":
			return BattleModalActionLoadGame
		case "PREFS":
			// The options root opens over ARMOPT, which stays on the modal
			// chain [07 R-FE-01 §6][07 R-FE-01 §7].
			return BattleModalActionPrefs
		case "MISSION":
			// The branch is the session kind: a campaign mission reaches the
			// in-battle briefing, every other kind the read-only game-settings
			// overlay [07 R-FE-01 §7][08 R-SKIR-01 §11].
			if s.campaign {
				s.ShowBriefing()
			} else {
				s.ShowGameOptions()
			}
			return BattleModalActionMission
		case "HELP":
			s.ShowHelp()
			return BattleModalActionHelp
		}
	case BattleModalExit:
		switch name {
		case "CANCEL":
			s.modal = BattleModalOptions
		case "RESTART":
			s.ShowRestart()
		case "MAINMENU":
			s.ShowConfirmation(true)
		case "EXITGAME":
			s.ShowConfirmation(false)
		}
	case BattleModalConfirmMain, BattleModalConfirmExit:
		switch name {
		case "CHOICE2", "CANCEL":
			s.modal = BattleModalOptions
		case "CHOICE1":
			if s.modal == BattleModalConfirmMain {
				return BattleModalActionMainMenu
			}
			return BattleModalActionExitGame
		}
	case BattleModalBriefing:
		switch name {
		case "OK":
			s.playCue("Options")
			s.modal = BattleModalOptions
			s.ClearModalPress()
		case "TextRegion", "MOREBAR":
			s.playCue("Options")
			// The in-battle briefing clears the inert-label attribute bit on
			// both, so unlike MSNBRIEF they are ordinary fired gadgets here;
			// either one pages the text [07 R-FE-01 §7][07 R-HUD-03 §10].
			return BattleModalActionBriefingPage
		}
	case BattleModalGameOptions:
		if name == "OK" {
			s.playCue("Options")
			s.modal = BattleModalOptions
			s.ClearModalPress()
		}
	case BattleModalHelp:
		switch name {
		case "OK":
			s.playCue("Options")
			s.modal = BattleModalOptions
			s.ClearModalPress()
		case "Page":
			s.playCue("Options")
			return BattleModalActionHelpPage
		}
	case BattleModalRestart:
		switch name {
		case "CANCEL":
			s.modal = BattleModalOptions
		case "Difficulty":
			return BattleModalActionRestartDifficulty
		case "RESTART":
			return BattleModalActionRestart
		}
	}
	return BattleModalActionNone
}

// PressModal captures a gadget and the modal that owned it. A later release
// must match both values to activate, preserving release-inside semantics.
func (s *BattleState) PressModal(index int) {
	if s == nil {
		return
	}
	s.pressed = index
	s.pressedModal = s.modal
}

// ReleaseModal completes the captured gesture only when the same gadget is
// still under the pointer and the modal has not changed meanwhile.
func (s *BattleState) ReleaseModal(index int) (int, bool) {
	if s == nil {
		return -1, false
	}
	pressed, modal := s.pressed, s.pressedModal
	s.ClearModalPress()
	if pressed < 0 || pressed != index || modal != s.modal {
		return -1, false
	}
	return pressed, true
}

// ClearModalPress cancels an in-flight modal gesture.
func (s *BattleState) ClearModalPress() {
	if s == nil {
		return
	}
	s.pressed = -1
	s.pressedModal = BattleModalClosed
}

// Paused reports the canonical UI pause truth. It is intentionally separate
// from the last committed frame: pausing may stop simulation before another
// tick-end publication can carry the new state [01 §4.3][I6].
func (s *BattleState) Paused() bool {
	return s != nil && s.paused
}

// SetPauseTruth records the actual result returned by Session.SetPaused at the
// scheduling boundary. Once UI has issued a pause transition, later committed
// frames cannot overwrite this synchronous truth with a stale tick [I6].
func (s *BattleState) SetPauseTruth(paused bool) {
	if s == nil {
		return
	}
	s.paused = paused
	s.pauseScheduleIssued = true
}

// SyncCommittedPause adopts the first/current committed pause value only
// before a UI-issued scheduling transition. This keeps initial presentation
// aligned while preserving immediate pause/unpause transitions [I6].
func (s *BattleState) SyncCommittedPause(paused bool) {
	if s == nil || s.pauseScheduleIssued {
		return
	}
	s.paused = paused
}

// PauseIntent creates the concrete pause value used by modal entry/exit and
// by the pause hotkey.
func PauseIntent(paused bool) BattleScheduleIntent {
	return BattleScheduleIntent{PauseSet: true, Pause: paused}
}

// SpeedIntent creates a concrete relative speed request.
func SpeedIntent(delta int) BattleScheduleIntent {
	return BattleScheduleIntent{SpeedDelta: delta}
}

// PlacementArmed is the sole mobile-build state gate [07 R-CAM-01 §14].
func (s *BattleState) PlacementArmed() bool {
	return s != nil && s.Input.Latch == input.LatchMobileBuild && s.Input.BuildDef != ""
}
