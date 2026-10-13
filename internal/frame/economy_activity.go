package frame

import "github.com/nanolathe-gg/nanolathe/internal/pool"

// EconomyFlowView is one copied last-settlement observation for the Enhanced
// resource explanation. Kind names an economy activity; resource pairs are
// metal then energy. Requested is demand, never a claim about payment.
// These observations are presentation data (DESIGN_ECONOMY_CONSTRUCTION).
type EconomyFlowView struct {
	Player              uint8
	Kind                uint8
	Unit                pool.UnitRef
	Produced, Requested [2]float32
}

// EconomyHoldView publishes a currently paused Modern activity. Both resource
// dropdowns control the same owner/activity pause.
type EconomyHoldView struct {
	Player uint8
	Kind   uint8
}
