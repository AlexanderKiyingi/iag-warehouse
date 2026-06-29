package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"

	"iag-warehouse/backend/internal/db"
	"iag-warehouse/backend/internal/migrate"
	"iag-warehouse/backend/internal/models"
)

// Integration round-trip for the migration-010 stores-domain CRUD. Skipped
// unless WAREHOUSE_TEST_DB points at a disposable Postgres, e.g.:
//
//	WAREHOUSE_TEST_DB="postgres://postgres:postgres@localhost:5432/warehouse_test?sslmode=disable" \
//	  go test ./internal/store -run StoresDomains -v
//
// It applies all migrations (incl. 010) then exercises create→list→update→
// delete (plus the gate-pass return action) against the real tables.
func TestStoresDomainsCRUD(t *testing.T) {
	dsn := os.Getenv("WAREHOUSE_TEST_DB")
	if dsn == "" {
		t.Skip("set WAREHOUSE_TEST_DB to run the stores-domain integration test")
	}
	ctx := context.Background()
	pool, err := db.NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	if err := migrate.Up(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := New(pool)

	t.Run("thresholds", func(t *testing.T) {
		c, err := s.CreateThreshold(ctx, models.StockThreshold{Item: "Test Item", Dept: "Stores", MinQty: 5, ReorderQty: 20})
		if err != nil || c.ID == uuid.Nil {
			t.Fatalf("create: %v id=%v", err, c.ID)
		}
		if _, err := s.ListThresholds(ctx); err != nil {
			t.Fatalf("list: %v", err)
		}
		c.MinQty = 9
		u, err := s.UpdateThreshold(ctx, c.ID, c)
		if err != nil || u.MinQty != 9 {
			t.Fatalf("update: %v min=%v", err, u.MinQty)
		}
		if err := s.DeleteThreshold(ctx, c.ID); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if err := s.DeleteThreshold(ctx, c.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("returns", func(t *testing.T) {
		c, err := s.CreateReturn(ctx, models.StockReturn{Item: "Returned Item", Qty: 3, Condition: "Good", Action: "Restocked"})
		if err != nil || c.ID == uuid.Nil {
			t.Fatalf("create: %v", err)
		}
		c.Action = "Write-off"
		u, err := s.UpdateReturn(ctx, c.ID, c)
		if err != nil || u.Action != "Write-off" {
			t.Fatalf("update: %v", err)
		}
		if _, err := s.ListReturns(ctx); err != nil {
			t.Fatalf("list: %v", err)
		}
		if err := s.DeleteReturn(ctx, c.ID); err != nil {
			t.Fatalf("delete: %v", err)
		}
	})

	t.Run("gate_passes", func(t *testing.T) {
		c, err := s.CreateGatePass(ctx, models.GatePass{Items: "Laptop", IssuedTo: "Tester", Dept: "IT"})
		if err != nil || c.ID == uuid.Nil {
			t.Fatalf("create: %v", err)
		}
		r, err := s.ReturnGatePass(ctx, c.ID, "2026-06-30")
		if err != nil || r.Status != "Returned" || r.ReturnDate != "2026-06-30" {
			t.Fatalf("return: %v status=%q date=%q", err, r.Status, r.ReturnDate)
		}
		if _, err := s.ListGatePasses(ctx); err != nil {
			t.Fatalf("list: %v", err)
		}
		if err := s.DeleteGatePass(ctx, c.ID); err != nil {
			t.Fatalf("delete: %v", err)
		}
	})

	t.Run("warranties", func(t *testing.T) {
		c, err := s.CreateWarranty(ctx, models.Warranty{Item: "Generator", Supplier: "ACME", Duration: "1 year"})
		if err != nil || c.ID == uuid.Nil {
			t.Fatalf("create: %v", err)
		}
		c.Status = "Expired"
		u, err := s.UpdateWarranty(ctx, c.ID, c)
		if err != nil || u.Status != "Expired" {
			t.Fatalf("update: %v", err)
		}
		if _, err := s.ListWarranties(ctx); err != nil {
			t.Fatalf("list: %v", err)
		}
		if err := s.DeleteWarranty(ctx, c.ID); err != nil {
			t.Fatalf("delete: %v", err)
		}
	})

	t.Run("event_requests", func(t *testing.T) {
		c, err := s.CreateEventRequest(ctx, models.EventRequest{EventName: "AGM", Items: "chairs", Qty: 50, Dept: "Events"})
		if err != nil || c.ID == uuid.Nil {
			t.Fatalf("create: %v", err)
		}
		c.Status = "Approved"
		u, err := s.UpdateEventRequest(ctx, c.ID, c)
		if err != nil || u.Status != "Approved" {
			t.Fatalf("update: %v", err)
		}
		if _, err := s.ListEventRequests(ctx); err != nil {
			t.Fatalf("list: %v", err)
		}
		if err := s.DeleteEventRequest(ctx, c.ID); err != nil {
			t.Fatalf("delete: %v", err)
		}
	})
}
