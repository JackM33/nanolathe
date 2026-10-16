package session

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// featureViewInputs is every value one feature's committed view is built from.
// Two publications whose inputs compare equal produce byte-identical views, so
// a frame slot that already holds the view built from these inputs keeps it.
//
// The definition enters as its pointer: a FeatureDef is an immutable catalog
// object, so identity covers every field the view copies from it. The event
// sequence names are a function of the definition, the animation selector
// and whether the cursor is running, so those enter instead of the strings
// and the comparison stays a scalar one.
type featureViewInputs struct {
	inst           *features.Instance
	def            *content.FeatureDef
	x, y, z        numeric.Fixed
	cx, cz         int32
	footX, footZ   int32
	wreckBornTick  uint32
	eventVisit     int32
	bank, heading  uint16
	pitch          uint16
	owner          uint8
	status         uint8
	animSelector   uint8
	ownerKnown     bool
	isBurning      bool
	isSinking      bool
	runtimeLive    bool
	shadowEnabled  bool
	wreckHeatKnown bool
	eventOK        bool
}

// featurePublicationCache remembers, per committed-frame slot, the inputs each
// retained FeatureView element was built from. The frame buffer rotates through
// its slots — two by default, all frame.MaxBufferSlots of them under the
// asynchronous window — and Frame.Reset keeps feature element storage, so the
// element at index i of a slot is the view this cache built there one rotation
// ago; when its inputs are unchanged, nothing is written. There is one cache
// per slot the rotation can reach: with fewer, the widened rotation evicted a
// binding on every publication, rebuilt every view and reallocated the whole
// input table each tick.
//
// The cache is presentation-side bookkeeping: it reads simulation state and
// writes only the frame, draws nothing and changes no authoritative field [I6].
type featurePublicationCache struct {
	frame  *frame.Frame
	inputs []featureViewInputs
	// revisions holds, parallel to inputs, the record's publication revision
	// when its view was built or confirmed, and epoch the feature service's
	// instance-set epoch then (features.Instance.PublicationState).
	revisions []uint64
	epoch     uint64
}

// featureCacheFor returns the cache bound to published, binding a free entry
// on first sight. A frame the session has not written before starts with no
// inputs, so every element is built.
func (s *Session) featureCacheFor(published *frame.Frame) *featurePublicationCache {
	for i := range s.featurePubCaches {
		if s.featurePubCaches[i].frame == published {
			return &s.featurePubCaches[i]
		}
	}
	for i := range s.featurePubCaches {
		if s.featurePubCaches[i].frame == nil {
			s.featurePubCaches[i].frame = published
			return &s.featurePubCaches[i]
		}
	}
	// More distinct frames than slots means the buffer was replaced; the
	// first binding is rebuilt from nothing.
	c := &s.featurePubCaches[0]
	*c = featurePublicationCache{frame: published}
	return c
}

// fillFeatureInputs reads one instance's inputs into in. It fills through a
// pointer so the caller's local is written once, not built and copied.
func fillFeatureInputs(in *featureViewInputs, inst *features.Instance, publication *publicationState) {
	owner, ownerKnown := featureOwnerSelector(inst)
	runtime := inst.RuntimeView()
	*in = featureViewInputs{
		inst:          inst,
		def:           inst.Def,
		owner:         owner,
		ownerKnown:    ownerKnown,
		cx:            int32(inst.CX),
		cz:            int32(inst.CZ),
		x:             inst.X,
		y:             inst.Y,
		z:             inst.Z,
		bank:          inst.Bank,
		heading:       inst.Heading,
		pitch:         inst.Pitch,
		status:        inst.Status,
		isBurning:     inst.IsBurning,
		isSinking:     inst.IsSinking,
		footX:         inst.FootprintX,
		footZ:         inst.FootprintZ,
		runtimeLive:   runtime.Live,
		shadowEnabled: runtime.ShadowEnabled,
		animSelector:  inst.AnimationSelector,
	}
	// Only successful death placements enter the birth table, and many
	// publications carry none, so the lookup is skipped while it is empty.
	if publication != nil && len(publication.wrecks.births) != 0 {
		in.wreckBornTick, in.wreckHeatKnown = publication.wrecks.births[inst]
	}
	_, _, in.eventVisit, in.eventOK = inst.EventSequence()
}

// writeFeatureView builds the committed view from its inputs. It is the one
// definition of the feature view; the cache only decides whether to run it.
func writeFeatureView(fv *frame.FeatureView, in *featureViewInputs) {
	def := in.def
	*fv = frame.FeatureView{
		Owner:      in.owner,
		OwnerKnown: in.ownerKnown,
		CX:         in.cx,
		CZ:         in.cz,
		X:          in.x,
		Y:          in.y,
		Z:          in.z,
		// The live record's orientation triple, published because the
		// 3DO feature pass fills a pseudo-unit with "model pointer,
		// position and the slot's orientation words"
		// [03 R-RAST-01 §6][05 "Feature instance and terrain cell"].
		// Zero for everything but a wreck.
		Bank:      in.bank,
		Heading:   in.heading,
		Pitch:     in.pitch,
		DefName:   def.CanonicalKey,
		Model:     def.Object,
		Status:    uint32(in.status),
		IsBurning: in.isBurning,
		IsSinking: in.isSinking,
		FootX:     int8(in.footX),
		FootZ:     int8(in.footZ),

		Filename:        def.Filename,
		SeqName:         def.SeqName,
		SeqNameShad:     def.SeqNameShad,
		Animating:       def.Animating != 0,
		AnimTrans:       def.AnimTrans != 0,
		ShadTrans:       def.ShadTrans != 0,
		Blocking:        def.Blocking,
		Reclaimable:     def.Reclaimable,
		NoDrawUnderGray: def.NoDrawUnderGray,
		Height:          def.Height,
		Geothermal:      def.Geothermal,
		RuntimeLive:     in.runtimeLive,
		ShadowEnabled:   in.shadowEnabled,
		WreckBornTick:   in.wreckBornTick,
		WreckHeatKnown:  in.wreckHeatKnown,
	}
	if fv.Model == "" {
		fv.Model = def.Filename
	}
	// A cell carrying a live EVENT record draws that record's own
	// cursor, not the definition's rest cursor [03 R-RAST-01 §6]
	// [05 R-FEAT-01 §10] pass 3. Publishing only the rest sequence left
	// a reclaimed tree standing on its idle frame for the whole
	// animation and then popping straight to its successor: the
	// reclaim sequence the definition names was resolved by the
	// simulation, which timed the record from it, and never drawn.
	if in.eventOK {
		name, shadow, visit, ok := in.inst.EventSequence()
		if ok {
			fv.EventSeqName = name
			fv.EventSeqNameShad = shadow
			fv.EventSeqVisit = visit
		}
	}
}

// publishFeatures writes the committed feature views for this publication into
// published, in the feature service's deterministic row/anchor order, and
// returns how many elements were rebuilt (the rest were retained unchanged).
//
// A record the slot already holds a view of, built at the record's current
// publication revision while it was off the active list, is kept without
// reading its inputs at all: off the list, every change to them renews the
// revision (features/publication.go). Every other record — on the active
// list, new at this index, or renewed — has its inputs read and compared, and
// its view is rebuilt only when they differ.
func (s *Session) publishFeatures(published *frame.Frame, publication *publicationState) int {
	published.Features = published.Features[:0]
	if s.Features == nil {
		return 0
	}
	// The same check covers the service's grid reconciliation skip: with it
	// on, every motion phase reconciles and a skip that would have missed a
	// change panics (features.Service.SetVerifyGridSync).
	s.Features.SetVerifyGridSync(s.verifyFeatureRevisions)
	// And the Modern patrol's restricted feature scan, which with it on also
	// scans every feature and panics on a different pick.
	if s.Build != nil && s.Build.OrderBinding != nil {
		s.Build.OrderBinding.World.SetVerifyFeatureScan(s.verifyFeatureRevisions)
	}
	cache := s.featureCacheFor(published)
	known := cache.inputs // the inputs of the views the slot still holds
	revisions := cache.revisions
	inputs := known[:0]
	epoch := s.Features.PublicationEpoch()
	if cache.epoch != epoch {
		known = nil
	}
	rebuilt := 0
	s.featurePublicationScratch = s.Features.AppendInstances(s.featurePublicationScratch[:0])
	for _, inst := range s.featurePublicationScratch {
		if inst == nil || inst.Def == nil {
			continue
		}
		idx := len(published.Features)
		published.Features = reserveFeatureView(published.Features)
		revision, active := inst.PublicationState()
		if !active && idx < len(known) && known[idx].inst == inst && revisions[idx] == revision && !s.verifyFeatureRevisions {
			// The element, its inputs and its revision stay exactly as the
			// slot holds them; inputs shares known's storage.
			inputs = inputs[:idx+1]
			continue
		}
		var in featureViewInputs
		fillFeatureInputs(&in, inst, publication)
		fv := &published.Features[idx]
		// Compare before the append below overwrites the same index.
		if idx >= len(known) || known[idx] != in {
			if s.verifyFeatureRevisions && !active && idx < len(known) && known[idx].inst == inst && revisions[idx] == revision {
				panic(fmt.Sprintf("session: feature at (%d,%d) changed its published inputs without renewing its publication revision", inst.CX, inst.CZ))
			}
			writeFeatureView(fv, &in)
			rebuilt++
		}
		inputs = append(inputs, in)
		if idx < len(revisions) {
			revisions[idx] = revision
		} else {
			revisions = append(revisions, revision)
		}
	}
	cache.inputs = inputs
	cache.revisions = revisions[:len(inputs)]
	cache.epoch = epoch
	// The committed frame owns values; release the borrowed live pointers
	// while retaining only the scratch capacity for the next publication [I6].
	clear(s.featurePublicationScratch)
	return rebuilt
}
