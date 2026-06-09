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

type Store struct {
	pool    *pgxpool.Pool
	bus     *events.Bus
	invBridge *inventory.Bridge
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
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
