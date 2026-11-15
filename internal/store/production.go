package store

import (
	"context"
	"log/slog"
	"strings"

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
	PlantCode       string
	Lines           []ProductionConsumeLine
	ActorID         *uuid.UUID
}

func productionPlantCode(facility, plant string) string {
	if plant != "" {
		return plant
	}
	return facility
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
	plant := productionPlantCode(in.FacilityCode, in.PlantCode)
	plantAttrs := map[string]any{"cost_center": "PROD"}
	if plant != "" {
		plantAttrs["plant_code"] = plant
	}

	for _, line := range in.Lines {
		binCode := strings.TrimSpace(line.BinCode)
		if binCode == "" {
			picked, err := s.pickAvailableBinCode(ctx, line.ItemID, line.Qty, line.LotKey, "")
			if err != nil {
				return nil, err
			}
			binCode = picked
		}
		bin, _, err := s.GetBinByCode(ctx, binCode)
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
		// Material leaves stock at average cost, exactly as an issue does; it
		// parks in WIP rather than going to COGS, because it is still an asset
		// — just one being worked on.
		cost, err := s.outboundCostTx(ctx, tx, line.ItemID, line.Qty, movID.String())
		if err != nil {
			return nil, err
		}
		if s.costingEnabled && cost.TotalCost == 0 {
			slog.Warn("production_consume posted with zero cost — item avg_cost is unset; finance will no-op",
				"item_id", line.ItemID, "sku", sku, "batch", in.BatchBusinessID)
		}
		if err := s.emitInventoryMovementWithAttrs(ctx, tx, movID, models.MovementProductionConsume, line.ItemID, sku, &bin.ID, nil, line.Qty, lotKey, serialKey, batchID, cost, "", plantAttrs); err != nil {
			return nil, err
		}
		eventLines = append(eventLines, map[string]any{
			"item_id": line.ItemID.String(),
			"qty":     line.Qty,
			"bin":     binCode,
		})
	}

	if s.bus != nil && s.bus.Enabled() {
		data := map[string]any{
			"batch_business_id": in.BatchBusinessID,
			"facility":          in.FacilityCode,
			"lines":             eventLines,
		}
		if plant != "" {
			data["plant_code"] = plant
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
	FacilityCode    string
	PlantCode       string
	ActorID         *uuid.UUID
}

func (s *Store) ProductionOutput(ctx context.Context, in ProductionOutputInput) (map[string]any, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	binCode := strings.TrimSpace(in.BinCode)
	if binCode == "" {
		facility := productionPlantCode(in.FacilityCode, in.PlantCode)
		if facility != "" {
			if code, err := s.DefaultBinCodeForFacility(ctx, facility); err == nil {
				binCode = code
			}
		}
		if binCode == "" {
			return nil, ErrInvalidArgument
		}
	}
	bin, _, err := s.GetBinByCode(ctx, binCode)
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
	plant := productionPlantCode(in.FacilityCode, in.PlantCode)
	refType := refType("production_output")
	movAttrs := map[string]any{"qc_hold": in.QCHold}
	if plant != "" {
		movAttrs["plant_code"] = plant
	}
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
		Attrs:           movAttrs,
	})
	if err != nil {
		return nil, err
	}
	sku, _ := s.getItemSKU(ctx, tx, in.ItemID)
	// Finished goods are valued at what the batch consumed, which is what
	// clears that batch's WIP back to zero.
	outCost, err := s.productionOutputCostTx(ctx, tx, batchID, in.Qty, movID.String())
	if err != nil {
		return nil, err
	}
	if s.costingEnabled && outCost.TotalCost == 0 {
		slog.Warn("production_output posted with zero cost — no valued consume WIP for batch; finance will no-op",
			"batch", in.BatchBusinessID, "sku", sku, "qty", in.Qty)
	}
	eventAttrs := map[string]any{"cost_center": "PROD"}
	if plant != "" {
		eventAttrs["plant_code"] = plant
	}
	if err := s.emitInventoryMovementWithAttrs(ctx, tx, movID, models.MovementProductionOutput, in.ItemID, sku, nil, &bin.ID, in.Qty, lotKey, serialKey, batchID, outCost, "", eventAttrs); err != nil {
		return nil, err
	}

	if s.bus != nil && s.bus.Enabled() {
		data := map[string]any{
			"batch_business_id": in.BatchBusinessID,
			"sku":               in.SKU,
			"qty":               in.Qty,
			"bin_code":          binCode,
			"lot_key":           lotKey,
			"qc_hold":           in.QCHold,
		}
		if plant != "" {
			data["plant_code"] = plant
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

// PlaceQCHold marks available balances for a lot as hold (quality gate).
// Idempotent when already on hold.
func (s *Store) PlaceQCHold(ctx context.Context, lotKey string) error {
	lotKey = strings.TrimSpace(lotKey)
	if lotKey == "" {
		return ErrInvalidArgument
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE wh_stock_balances SET status = 'hold', updated_at = NOW()
		WHERE lot_key = $1 AND status = 'available'`, lotKey)
	return err
}

// PlaceQCHoldByBatch holds all lots linked to a batch (qc.batch.held).
func (s *Store) PlaceQCHoldByBatch(ctx context.Context, batchBusinessID string) error {
	batchBusinessID = strings.TrimSpace(batchBusinessID)
	if batchBusinessID == "" {
		return ErrInvalidArgument
	}
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
		if err := s.PlaceQCHold(ctx, lotKey); err != nil {
			return err
		}
	}
	return rows.Err()
}
