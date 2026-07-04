package store

import (
	"context"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"iag-warehouse/backend/internal/models"
)

type AdjustmentInput struct {
	ItemID    uuid.UUID
	BinCode   string
	LotKey    string
	SerialKey string
	QtyAfter  float64
	Reason    *string
	AdjType   string
	ActorID   *uuid.UUID
}

func (s *Store) CreateAdjustment(ctx context.Context, in AdjustmentInput) (models.Adjustment, error) {
	if in.AdjType == "" {
		in.AdjType = "adjustment"
	}
	return s.applyStockChange(ctx, in)
}

func (s *Store) CreateCycleCount(ctx context.Context, in AdjustmentInput) (models.Adjustment, error) {
	in.AdjType = "cycle_count"
	return s.applyStockChange(ctx, in)
}

func (s *Store) applyStockChange(ctx context.Context, in AdjustmentInput) (models.Adjustment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.Adjustment{}, err
	}
	defer tx.Rollback(ctx)

	bin, _, err := s.GetBinByCode(ctx, in.BinCode)
	if err != nil {
		return models.Adjustment{}, err
	}
	lotKey, serialKey := normalizeKeys(in.LotKey, in.SerialKey)

	var qtyBefore float64
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(qty, 0) FROM wh_stock_balances
		WHERE item_id = $1 AND bin_id = $2 AND lot_key = $3 AND serial_key = $4`,
		in.ItemID, bin.ID, lotKey, serialKey).Scan(&qtyBefore)
	if err != nil && err != pgx.ErrNoRows {
		return models.Adjustment{}, err
	}

	delta := in.QtyAfter - qtyBefore
	if delta != 0 {
		if err := s.adjustBalanceTx(ctx, tx, balanceKey{in.ItemID, bin.ID, lotKey, serialKey}, delta, models.StatusAvailable); err != nil {
			return models.Adjustment{}, err
		}
	}

	var adj models.Adjustment
	err = tx.QueryRow(ctx, `
		INSERT INTO wh_adjustments (adj_type, item_id, bin_id, lot_key, serial_key, qty_before, qty_after, reason, actor_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, adj_type, item_id, bin_id, lot_key, serial_key, qty_before, qty_after, reason, actor_id, created_at`,
		in.AdjType, in.ItemID, bin.ID, lotKey, serialKey, qtyBefore, in.QtyAfter, in.Reason, in.ActorID,
	).Scan(&adj.ID, &adj.AdjType, &adj.ItemID, &adj.BinID, &adj.LotKey, &adj.SerialKey, &adj.QtyBefore, &adj.QtyAfter, &adj.Reason, &adj.ActorID, &adj.CreatedAt)
	if err != nil {
		return adj, err
	}

	refType := refType("adjustment")
	sku, _ := s.getItemSKU(ctx, tx, in.ItemID)
	movID, err := s.insertMovementTx(ctx, tx, movementInput{
		MovementType: models.MovementAdjustment,
		ItemID:       &in.ItemID,
		FromBinID:    ptrIf(delta < 0, bin.ID),
		ToBinID:      ptrIf(delta > 0, bin.ID),
		Qty:          abs(delta),
		LotKey:       lotKey,
		SerialKey:    serialKey,
		RefType:      refType,
		RefID:        &adj.ID,
		ActorID:      in.ActorID,
	})
	if err != nil {
		return adj, err
	}
	if delta != 0 {
		var fromBin, toBin *uuid.UUID
		if delta < 0 {
			fromBin = &bin.ID
		} else {
			toBin = &bin.ID
		}
		cost, err := s.adjustmentCostTx(ctx, tx, in.ItemID, delta, adj.ID.String())
		if err != nil {
			return adj, err
		}
		s.emitInventoryMovement(ctx, movID, models.MovementAdjustment, in.ItemID, sku, fromBin, toBin, abs(delta), lotKey, serialKey, nil, cost)
	}

	if err := tx.Commit(ctx); err != nil {
		return adj, err
	}
	return adj, nil
}

// ListAdjustments returns adjustment/cycle-count records joined to item and bin
// display fields. adjType filters by 'adjustment' or 'cycle_count' when set.
func (s *Store) ListAdjustments(ctx context.Context, adjType string, limit int) ([]models.Adjustment, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	query := `
		SELECT a.id, a.adj_type, a.item_id, a.bin_id, a.lot_key, a.serial_key, a.qty_before, a.qty_after,
			a.reason, a.actor_id, a.created_at, i.sku, i.name, b.code
		FROM wh_adjustments a
		JOIN wh_items i ON i.id = a.item_id
		JOIN wh_bins b ON b.id = a.bin_id`
	var args []any
	if adjType != "" {
		args = append(args, adjType)
		query += ` WHERE a.adj_type = $1`
	}
	args = append(args, limit)
	query += ` ORDER BY a.created_at DESC LIMIT $` + strconv.Itoa(len(args))

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Adjustment{}
	for rows.Next() {
		var a models.Adjustment
		if err := rows.Scan(&a.ID, &a.AdjType, &a.ItemID, &a.BinID, &a.LotKey, &a.SerialKey,
			&a.QtyBefore, &a.QtyAfter, &a.Reason, &a.ActorID, &a.CreatedAt, &a.ItemSKU, &a.ItemName, &a.BinCode); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func ptrIf(cond bool, id uuid.UUID) *uuid.UUID {
	if cond {
		return &id
	}
	return nil
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
