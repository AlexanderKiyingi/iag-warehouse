package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	"iag-warehouse/backend/internal/events"
	"iag-warehouse/backend/internal/inventory"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")
var ErrInsufficientStock = errors.New("insufficient stock")
var ErrStockNotAvailable = errors.New("stock not available for issue")
var ErrForbidden = errors.New("forbidden")
var ErrInvalidArgument = errors.New("invalid argument")

type Store struct {
	pool    *pgxpool.Pool
	bus     *events.Bus
	invBridge *inventory.Bridge
	// costingEnabled turns on weighted-average costing on valued movement paths
	// (receipt/issue/adjustment). Off → movements carry no cost and finance no-ops.
	costingEnabled bool
	baseCurrency   string
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, baseCurrency: "UGX"}
}

// SetCosting enables weighted-average costing and sets the base currency stamped
// on valued movement events.
func (s *Store) SetCosting(enabled bool, baseCurrency string) {
	s.costingEnabled = enabled
	if baseCurrency != "" {
		s.baseCurrency = baseCurrency
	}
}

func (s *Store) SetEventBus(bus *events.Bus) {
	s.bus = bus
	if bus != nil {
		s.invBridge = inventory.NewBridge(bus)
	}
}

func (s *Store) Pool() *pgxpool.Pool { return s.pool }

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *Store) EventBus() *events.Bus { return s.bus }

func (s *Store) InventoryBridge() *inventory.Bridge { return s.invBridge }
