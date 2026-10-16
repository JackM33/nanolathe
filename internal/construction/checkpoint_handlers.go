package construction

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The source is created only with this exact service. Copied values travel
// with the handlers when queues inherit them; the cached rows remain derived
// metadata and add no construction bytes (DESIGN_MULTIPLAYER §16.3.62/64).
type checkpointOrderHandlers struct {
	owner     *Service
	authority *checkpoint.BindingAuthority
	source    *orders.CheckpointHandlerSource
	wake      orders.CheckpointOwnedHandler
	built     orders.CheckpointOwnedHandler
}

// NewServiceWithCheckpointBinding shares the ordinary constructor body. A
// nonnil authority retains a source but does not resolve rows or create the
// lazy handler closures earlier than their existing first registration.
func NewServiceWithCheckpointBinding(terrain *world.Terrain, catalog *content.Catalog, w *units.World, econ *economy.Service, authority *checkpoint.BindingAuthority) *Service {
	s := &Service{Terrain: terrain, Catalog: catalog, World: w, Economy: econ}
	s.builderLinks = make(map[pool.Handle]pool.Handle)
	s.placements = make(map[pool.Handle]placementRecord)
	if authority != nil {
		s.checkpointHandlers = checkpointOrderHandlers{owner: s, authority: authority, source: orders.NewCheckpointHandlerSource(s, authority)}
	}
	return s
}

func (s *Service) hasCheckpointHandlerSource() bool {
	return s != nil && s.checkpointHandlers.owner == s && s.checkpointHandlers.authority != nil && s.checkpointHandlers.source != nil
}

// Every caller is an existing installation site. Ordinary cached functions
// have no copied value, so neither later registration nor a copied Service can
// retroactively attest them. The setter still installs the function once.
//
// handler and proof are this service's own fields, which hold their values
// for the service's lifetime once ensureRegistrationRows has filled them, so
// their addresses name the installation: the queue skips a row it already
// holds under the same key (orders.Queue.RegisterOwnedHandler). A copied
// Service has fields of its own and therefore keys of its own.
func (s *Service) setCheckpointOrderHandler(q *orders.Queue, id orders.ID, handler *orders.OwnedHandler, proof *orders.CheckpointOwnedHandler) {
	if s.hasCheckpointHandlerSource() && proof.Handler() != nil {
		q.RegisterOwnedHandlerWithCheckpointBinding(id, proof, *proof)
	} else {
		q.RegisterOwnedHandler(id, handler, *handler)
	}
}

// orderHandlerKey is the key setCheckpointOrderHandler installs a row under
// for this handler and proof pair, so a registrant can name its whole
// registration with it (orders.Queue.MarkOwnedRows).
func (s *Service) orderHandlerKey(handler *orders.OwnedHandler, proof *orders.CheckpointOwnedHandler) orders.OwnedKey {
	if s.hasCheckpointHandlerSource() && proof.Handler() != nil {
		return orders.OwnedKey{Proof: proof}
	}
	return orders.OwnedKey{Handler: handler}
}

// RegisterCheckpointOrderHandlers admits only the constructor's original
// source. It neither creates lazy rows nor validates unrelated construction
// bindings. Orders rechecks the source and each copied row during capture.
func (s *Service) RegisterCheckpointOrderHandlers(c *orders.CheckpointContext, authority *checkpoint.BindingAuthority) error {
	if c == nil || !s.hasCheckpointHandlerSource() || !s.checkpointHandlers.authority.Matches(authority) {
		return constructionCheckpointError("construction.Service.handlers", "the original constructor owner, source and matching nonnil authority")
	}
	// Source registration changes only value fields of the lower context. Stage
	// both kinds together so a conflicting second kind leaves it untouched.
	next := *c
	for _, kind := range []uint8{orders.CheckpointConstructionWake, orders.CheckpointGetBuilt} {
		if err := orders.RegisterCheckpointHandlerSource(&next, kind, s.checkpointHandlers.source, s, authority); err != nil {
			return err
		}
	}
	*c = next
	return nil
}
