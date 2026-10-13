package camera

import "math"

// Zoom's feel-tuning knobs and the state machine that spends them
// (DESIGN_GPU_RENDERER §16.6, §16.7).
//
// EVERY constant in this block is a FEEL-TUNING KNOB. None of them is a retail
// finding — retail has one view scale and no wheel zoom at all — and none of
// them is derived from anything. They live together here so they can be tuned
// by hand in one place; changing one changes only how the zoom feels.
const (
	// ZoomScrollThreshold is the travel required for one zoom step, in
	// thousandths of an Ebitengine wheel unit: one conventional wheel click.
	// Trackpad fractions accumulate toward the same threshold (§16.6).
	ZoomScrollThreshold int32 = 1000

	// ZoomScrollCooldownMillis is the quiet gap that releases the wheel's lock
	// stop. Every discarded event restarts it; a continuous burst cannot pass
	// through the preferred stop. Other zoom factors have no cooldown (§16.6).
	ZoomScrollCooldownMillis uint32 = 180

	// ZoomLegacyScrollCooldownMillis preserves the earlier controller's fixed
	// hold after every accepted preset. Ignored events do not extend it.
	ZoomLegacyScrollCooldownMillis uint32 = 500

	// ZoomWheelRatio gives one notch a repeatable proportional change; several
	// notches in one frame travel further without queuing extra motion (§16.6).
	ZoomWheelRatio = 1.25

	// The wheel's snap band is proportional to the chosen lock (§16.6).
	ZoomSnapPercent = 15

	// ZoomEaseFraction is how much of the remaining gap the live factor closes
	// per host Update. 0.50 closes 87.5% in three Updates, a tenth of a second
	// at the 30 Hz Update grid. Pinch writes its live factor directly (§16.6).
	ZoomEaseFraction = 0.50

	// ZoomLegacyEaseFraction preserves the earlier controller's slower glide.
	ZoomLegacyEaseFraction = 0.30

	// ZoomSettleEpsilon is how close the live factor has to be to the target
	// before it is snapped onto it and the animation stops, in 1/ZoomUnit units.
	// Without it the exponential ease never terminates.
	ZoomSettleEpsilon Zoom = 2
)

// ZoomSteps retain the legacy preset list for callers using NextZoomStep: a
// tactical overview, the default native view, and the detail view. The lowest
// target is clamped to MinZoom when the map cannot fill the viewport at 0.25x.
// These are presentation feel choices, like the constants above.
var ZoomSteps = [3]Zoom{
	ZoomUnit / 4, // 0.25x
	ZoomUnit,     // 1x
	ZoomMax,      // 2x
}

// NextZoomStep is the next zoom stop in the requested direction: the first step
// strictly above it when in is set, the first strictly below it otherwise, and
// false at either end of the list. A factor between two steps goes to the
// nearest preset in the direction of travel. WheelLegacy uses this list;
// Modern controls use proportional targets or NextZoomStop instead.
func NextZoomStep(current Zoom, in bool) (Zoom, bool) {
	if in {
		for _, s := range ZoomSteps {
			if s > current {
				return s, true
			}
		}
		return 0, false
	}
	for i := len(ZoomSteps) - 1; i >= 0; i-- {
		if ZoomSteps[i] < current {
			return ZoomSteps[i], true
		}
	}
	return 0, false
}

// NextZoomStop selects the next Modern target: full map, tactical, the
// preferred lock, detail. Tactical and lock are ordered even below 0.25x.
// Strict comparisons skip duplicates and targets below the usable floor
// (DESIGN_GPU_RENDERER §16.6, §16.7).
func NextZoomStop(current, floor, lock Zoom, in bool) (Zoom, bool) {
	floor = max(floor, 1)
	lock = max(floor, lock.Norm())
	stops := [4]Zoom{floor, max(floor, min(ZoomUnit/4, lock)), max(floor, max(ZoomUnit/4, lock)), ZoomMax}
	if in {
		for _, stop := range stops {
			if stop >= floor && stop <= ZoomMax && stop > current {
				return stop, true
			}
		}
		return 0, false
	}
	for i := len(stops) - 1; i >= 0; i-- {
		if stop := stops[i]; stop >= floor && stop <= ZoomMax && stop < current {
			return stop, true
		}
	}
	return 0, false
}

// SnapZoom catches nearby targets and crossings at the preferred lock. Once
// a gesture's hold is released, a new gesture can leave that stop (§16.6).
func SnapZoom(from, want, lock Zoom) Zoom {
	lock = lock.Norm()
	radius := lock * ZoomSnapPercent / 100
	low, high := lock-radius, lock+radius
	if want >= low && want <= high ||
		from < lock && want > lock || from > lock && want < lock {
		return lock
	}
	return want
}

// ZoomController owns a target and a live factor that eases toward it on the
// host Update grid (§16.6). Modern wheel input holds its lock until a quiet gap;
// legacy wheel input retains its fixed cooldown after each preset.
//
// It is presentation state and is driven from the platform layer's Update, so
// easing uses host Updates and scroll cooldown uses host milliseconds [I6].
// It holds no camera:
// each call takes the one it drives, which keeps the battle session the single
// owner of the camera.
type ZoomController struct {
	// LockZoom is the preferred Modern stop. Zero retains native. It is host
	// configuration, preserved when Reset discards an input gesture.
	LockZoom Zoom
	// target is the factor the live one is easing toward; zero means "no zoom
	// in flight", which is the state a camera that has never been zoomed is in.
	target Zoom
	// anchorX, anchorY is the beam-space screen point the zoom is taken about,
	// so the world point under it stays put for the whole animation.
	anchorX, anchorY int32
	anchored         bool
	// travel is the wheel movement banked toward the next zoom step, in
	// thousandths of a wheel unit, signed: a trackpad's fractions add up here
	// until they are worth a step.
	travel int32
	// Modern records the last wheel event; legacy records the last accepted
	// preset. Host milliseconds wrap. scrollCooling selects the active hold.
	scrollAt        uint32
	scrollCooling   bool
	scrollDirection int32
	// The accepted target chooses the ease; cancelling wheel input retains it.
	legacyEase bool
}

// Target reports the factor the controller is easing toward, falling back to
// the camera's live factor when nothing is in flight.
func (z *ZoomController) Target(cam *Camera) Zoom {
	if z == nil || z.target <= 0 {
		return cam.EffectiveZoom()
	}
	return z.target
}

// Active reports whether a zoom is still in flight, which is what the caller
// uses to decide whether this Update has to touch the camera at all.
func (z *ZoomController) Active(cam *Camera) bool {
	if z == nil || z.target <= 0 || cam == nil {
		return false
	}
	return z.target != cam.EffectiveZoom()
}

// Wheel changes the target proportionally about (mx, my). Notches accumulate
// without cooldown away from the lock; arrivals snap there immediately and hold
// until the wheel is quiet. Reversals start from the live view so the camera
// responds instead of first finishing its old target (§16.6).
func (z *ZoomController) Wheel(cam *Camera, mx, my int32, dy float64, now uint32) {
	z.wheelModern(cam, mx, my, dy, now, false)
}

// WheelStepped spends whole notches on Modern's four usable stops. Several
// notches can reach a limit in one event, but crossing the lock arrives there
// immediately and discards the rest of that burst (§16.6, §16.7).
func (z *ZoomController) WheelStepped(cam *Camera, mx, my int32, dy float64, now uint32) {
	z.wheelModern(cam, mx, my, dy, now, true)
}

func (z *ZoomController) wheelModern(cam *Camera, mx, my int32, dy float64, now uint32, stepped bool) {
	if z == nil || cam == nil || dy == 0 || math.IsNaN(dy) || math.IsInf(dy, 0) {
		return
	}
	if z.scrollCooling && now-z.scrollAt < ZoomScrollCooldownMillis {
		z.scrollAt = now
		return
	}
	if now-z.scrollAt >= ZoomScrollCooldownMillis {
		z.travel = 0
	}
	z.scrollAt, z.scrollCooling = now, false
	units := wheelUnits(dy)
	if units == 0 {
		return
	}
	direction := int32(-1)
	if units > 0 {
		direction = 1
	}
	floor := cam.MinZoom()
	lock := max(floor, z.LockZoom.Norm())
	current := z.Target(cam)
	if z.scrollDirection != 0 && direction != z.scrollDirection {
		z.travel = 0
		live := cam.EffectiveZoom()
		current = SnapZoom(live, live, lock)
		// Cancel the old glide even if this reversed sample is fractional.
		z.setTarget(cam, mx, my, current)
		z.legacyEase = false
		if current == lock && live != current {
			// Cancelling a glide inside the band must not strand the view near
			// the lock. This arrival gets the same barrier as a crossed target.
			cam.SetZoomAbout(mx, my, current)
			z.target, z.scrollDirection, z.scrollCooling = current, direction, true
			return
		}
	}
	z.scrollDirection = direction
	if current <= floor && direction < 0 || current >= ZoomMax && direction > 0 {
		z.travel = 0
		return
	}
	z.travel += units
	notches := z.travel / ZoomScrollThreshold
	if notches == 0 {
		return
	}
	z.travel -= notches * ZoomScrollThreshold
	want := current
	if stepped {
		for notches != 0 {
			next, ok := NextZoomStop(want, floor, lock, direction > 0)
			if !ok {
				break
			}
			want = next
			notches -= direction
			if want == lock || want == floor || want == ZoomMax {
				break
			}
		}
	} else {
		factor := float64(current) * math.Pow(ZoomWheelRatio, float64(notches))
		factor = max(float64(floor), min(float64(ZoomMax), factor))
		want = SnapZoom(current, Zoom(math.Round(factor)), lock)
	}
	z.setTarget(cam, mx, my, want)
	z.legacyEase = false
	if want == floor || want == ZoomMax {
		z.travel = 0
	}
	if want == lock && current != lock {
		cam.SetZoomAbout(mx, my, want)
		z.travel = 0
		z.scrollCooling = true
	}
}

// wheelUnits bounds an event before narrowing. This prevents a large host
// delta from overflowing the signed travel bank or reversing its direction.
func wheelUnits(dy float64) int32 {
	limit := float64(math.MaxInt32 - ZoomScrollThreshold)
	return int32(max(-limit, min(limit, math.Round(dy*1000))))
}

// WheelLegacy retains the earlier three presets, one accepted step per event,
// and a fixed 500 ms cooldown after every accepted step. Fractions accumulate;
// reversals discard the old remainder while the accepted glide continues.
func (z *ZoomController) WheelLegacy(cam *Camera, mx, my int32, dy float64, now uint32) {
	if z == nil || cam == nil || dy == 0 || math.IsNaN(dy) || math.IsInf(dy, 0) {
		return
	}
	if z.scrollCooling && now-z.scrollAt < ZoomLegacyScrollCooldownMillis {
		return
	}
	units := wheelUnits(dy)
	if units == 0 {
		return
	}
	if (units > 0) != (z.travel > 0) {
		z.travel = 0
	}
	z.travel += units
	if z.travel < ZoomScrollThreshold && z.travel > -ZoomScrollThreshold {
		return
	}
	in := z.travel > 0
	z.travel = 0
	current := cam.RequestedZoom()
	next, ok := NextZoomStep(current, in)
	if !ok {
		return
	}
	z.setTarget(cam, mx, my, next)
	z.legacyEase = true
	z.scrollAt, z.scrollCooling = now, true
}

// SetTarget aims the zoom at a factor about (mx, my) without a wheel gesture —
// explicit animated camera controls take this route (§16.6).
func (z *ZoomController) SetTarget(cam *Camera, mx, my int32, want Zoom) {
	if z == nil || cam == nil {
		return
	}
	z.setTarget(cam, mx, my, want)
	z.legacyEase = false
	z.CancelWheel()
}

// SetTargetLegacy is an explicit target with the earlier controller's 0.30
// ease, used by legacy F9 and pinch. Like SetTarget it clears wheel state.
func (z *ZoomController) SetTargetLegacy(cam *Camera, mx, my int32, want Zoom) {
	if z == nil || cam == nil {
		return
	}
	z.setTarget(cam, mx, my, want)
	z.legacyEase = true
	z.CancelWheel()
}

// setTarget clamps a requested factor into the camera's usable range and
// records the anchor the animation is taken about.
func (z *ZoomController) setTarget(cam *Camera, mx, my int32, want Zoom) {
	want = want.Norm()
	cam.requestedZoom = want
	if minZ := cam.MinZoom(); want < minZ {
		want = minZ
	}
	z.target = want
	z.anchorX, z.anchorY, z.anchored = mx, my, true
}

// CancelWheel discards fractional travel and the lock hold when another
// input owner blocks the camera. It preserves an already accepted target,
// its pointer anchor and its ease (§16.6).
func (z *ZoomController) CancelWheel() {
	if z == nil {
		return
	}
	z.travel, z.scrollAt, z.scrollCooling, z.scrollDirection = 0, 0, false, 0
}

// Reset abandons any zoom in flight, which is what a route that jumps the
// camera outright (a battle restart, a save restore) wants.
func (z *ZoomController) Reset() {
	if z == nil {
		return
	}
	*z = ZoomController{LockZoom: z.LockZoom}
}

// Step advances the zoom by one host Update: it eases the live factor toward
// the target about the stored anchor (§16.6). It reports whether the camera
// moved.
//
// The clock is the caller's Update, which the platform layer paces from host
// time; no simulation tick is read [I6].
func (z *ZoomController) Step(cam *Camera) bool {
	if z == nil || cam == nil || z.target <= 0 {
		return false
	}
	live := cam.EffectiveZoom()
	if live == z.target {
		return false
	}
	fraction := ZoomEaseFraction
	if z.legacyEase {
		fraction = ZoomLegacyEaseFraction
	}
	next := easeZoom(live, z.target, fraction)
	mx, my := z.anchorX, z.anchorY
	if !z.anchored {
		mx, my = OriginX, OriginY
	}
	cam.setLiveZoomAbout(mx, my, next)
	// The camera's own floor may have refused the target outright (a map the
	// view already covers); adopt what it took so the ease terminates.
	if got := cam.EffectiveZoom(); got != next && (got > z.target) == (next > z.target) {
		z.target = got
	}
	return true
}

// easeZoom is one Update of the exponential ease: close the selected fraction
// of the remaining gap, always move at least one unit so an integer factor
// cannot stall, and settle outright inside ZoomSettleEpsilon.
func easeZoom(live, target Zoom, fraction float64) Zoom {
	gap := int64(target) - int64(live)
	if gap <= int64(ZoomSettleEpsilon) && gap >= -int64(ZoomSettleEpsilon) {
		return target
	}
	stepped := int64(float64(gap) * fraction)
	if stepped == 0 {
		if gap > 0 {
			stepped = 1
		} else {
			stepped = -1
		}
	}
	return live + Zoom(stepped)
}
