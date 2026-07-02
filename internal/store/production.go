package store

import (
	"context"

	"github.com/google/uuid"

	"iag-warehouse/backend/internal/events"
	"iag-warehouse/backend/internal/models"
)

type ProductionConsumeLine struct {
	ItemID  uuid.UUID
	Qty     float64
	BinCode string
	LotKey  string
}

type ProductionConsumeInput struct {
	BatchBusinessID string
	FacilityCode    string
	Lines           []ProductionConsumeLine
	ActorID         *uuid.UUID
}

func (s *Store) ProductionConsume(ctx context.Context, in ProductionConsumeInput) (map[string]any, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	refType := refType("production_consume")
	var eventLines []map[string]any
	batchID := strPtr(in.BatchBusinessID)

	for _, line := range in.Lines {
		bin, _, err := s.GetBinByCode(ctx, line.BinCode)
		if err != nil {
			return nil, err
		}
		lotKey, serialKey := normalizeKeys(line.LotKey, "")
		if err := s.deductAvailableBalanceTx(ctx, tx, balanceKey{line.ItemID, bin.ID, lotKey, serialKey}, line.Qty); err != nil {
			return nil, err
		}
		sku, _ := s.getItemSKU(ctx, tx, line.ItemID)
		movID, err := s.insertMovementTx(ctx, tx, movementInput{
			MovementType:    models.MovementProductionConsume,
			ItemID:          &line.ItemID,
			FromBinID:       &bin.ID,
			Qty:             line.Qty,
			LotKey:          lotKey,
			SerialKey:       serialKey,
			RefType:         refType,
			BatchBusinessID: batchID,
			ActorID:         in.ActorID,
		})
		if err != nil {
			return nil, err
		}
		// Production consume/output move value through WIP, not COGS — finance
		// does not yet book these, so they emit no valuation (see roadmap).
		s.emitInventoryMovement(ctx, movID, models.MovementProductionConsume, line.ItemID, sku, &bin.ID, nil, line.Qty, lotKey, serialKey, batchID, movementCost{})
		eventLines = append(eventLines, map[string]any{
			"item_id": line.ItemID.String(),
			"qty":     line.Qty,
			"bin":     line.BinCode,
		})
	}

	if s.bus != nil && s.bus.Enabled() {
		data := map[string]any{
			"batch_business_id": in.BatchBusinessID,
			"facility":          in.FacilityCode,
			"lines":             eventLines,
		}
		if err := s.bus.PublishTx(ctx, tx, events.TypeProductionConsumed, data, in.BatchBusinessID); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return map[string]any{
		"batch_business_id": in.BatchBusinessID,
		"lines":             eventLines,
	}, nil
}

type ProductionOutputInput struct {
	BatchBusinessID string
	SKU             string
	ItemID          uuid.UUID
	Qty             float64
	BinCode         string
	LotKey          string
	QCHold          bool
	ActorID         *uuid.UUID
}

func (s *Store) ProductionOutput(ctx context.Context, in ProductionOutputInput) (map[string]any, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	bin, _, err := s.GetBinByCode(ctx, in.BinCode)
	if err != nil {
		return nil, err
	}
	lotKey, serialKey := normalizeKeys(in.LotKey, "")
	status := models.StatusAvailable
	if in.QCHold {
		status = models.StatusHold
	}
	if err := s.adjustBalanceTx(ctx, tx, balanceKey{in.ItemID, bin.ID, lotKey, serialKey}, in.Qty, status); err != nil {
		return nil, err
	}
	if lotKey != "" {
		_, _ = tx.Exec(ctx, `
			INSERT INTO wh_lots (lot_key, batch_business_id) VALUES ($1, $2)
			ON CONFLICT (lot_key) DO NOTHING`, lotKey, in.BatchBusinessID)
	}

	batchID := strPtr(in.BatchBusinessID)
	refType := refType("production_output")
	movID, err := s.insertMovementTx(ctx, tx, movementInput{
		MovementType:    models.MovementProductionOutput,
		ItemID:          &in.ItemID,
		ToBinID:         &bin.ID,
		Qty:             in.Qty,
		LotKey:          lotKey,
		SerialKey:       serialKey,
		RefType:         refType,
		BatchBusinessID: batchID,
		ActorID:         in.ActorID,
		Attrs: map[string]any{
			"qc_hold": in.QCHold,
		},
	})
	if err != nil {
		return nil, err
	}
	sku, _ := s.getItemSKU(ctx, tx, in.ItemID)
	s.emitInventoryMovement(ctx, movID, models.MovementProductionOutput, in.ItemID, sku, nil, &bin.ID, in.Qty, lotKey, serialKey, batchID, movementCost{})

	if s.bus != nil && s.bus.Enabled() {
		data := map[string]any{
			"batch_business_id": in.BatchBusinessID,
			"sku":               in.SKU,
			"qty":               in.Qty,
			"bin_code":          in.BinCode,
			"lot_key":           lotKey,
			"qc_hold":           in.QCHold,
		}
		if err := s.bus.PublishTx(ctx, tx, events.TypeProductionOutput, data, in.BatchBusinessID); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return map[string]any{
		"batch_business_id": in.BatchBusinessID,
		"sku":               in.SKU,
		"qty":               in.Qty,
		"bin_code":          in.BinCode,
		"lot_key":           lotKey,
		"qc_hold":           in.QCHold,
	}, nil
}

func (s *Store) ReleaseQCHold(ctx context.Context, lotKey string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		UPDATE wh_stock_balances SET status = 'available', updated_at = NOW()
		WHERE lot_key = $1 AND status = 'hold'`, lotKey)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

func (s *Store) ReleaseQCHoldByBatch(ctx context.Context, batchBusinessID string) error {
	rows, err := s.pool.Query(ctx, `SELECT lot_key FROM wh_lots WHERE batch_business_id = $1`, batchBusinessID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var lotKey string
		if err := rows.Scan(&lotKey); err != nil {
			return err
		}
		_ = s.ReleaseQCHold(ctx, lotKey)
	}
	return rows.Err()
}
