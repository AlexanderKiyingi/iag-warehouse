package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"iag-warehouse/backend/internal/events"
	"iag-warehouse/backend/internal/models"
)

func (s *Store) ListAssets(ctx context.Context) ([]models.Asset, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, asset_tag, serial_no, item_id, current_bin_id, condition, book_value_ref, attrs, created_at, updated_at
		FROM wh_assets ORDER BY asset_tag`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Asset
	for rows.Next() {
		var a models.Asset
		if err := rows.Scan(&a.ID, &a.AssetTag, &a.SerialNo, &a.ItemID, &a.CurrentBinID, &a.Condition, &a.BookValueRef, &a.Attrs, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) CreateAsset(ctx context.Context, assetTag string, serialNo *string, itemID uuid.UUID, binCode *string, condition string, attrs map[string]any) (models.Asset, error) {
	var binID *uuid.UUID
	if binCode != nil && *binCode != "" {
		bin, _, err := s.GetBinByCode(ctx, *binCode)
		if err != nil {
			return models.Asset{}, err
		}
		binID = &bin.ID
	}
	var a models.Asset
	err := s.pool.QueryRow(ctx, `
		INSERT INTO wh_assets (asset_tag, serial_no, item_id, current_bin_id, condition, attrs)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, asset_tag, serial_no, item_id, current_bin_id, condition, book_value_ref, attrs, created_at, updated_at`,
		assetTag, serialNo, itemID, binID, condition, attrsOrEmpty(attrs),
	).Scan(&a.ID, &a.AssetTag, &a.SerialNo, &a.ItemID, &a.CurrentBinID, &a.Condition, &a.BookValueRef, &a.Attrs, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

func (s *Store) GetAssetByTag(ctx context.Context, tag string) (models.Asset, error) {
	var a models.Asset
	err := s.pool.QueryRow(ctx, `
		SELECT id, asset_tag, serial_no, item_id, current_bin_id, condition, book_value_ref, attrs, created_at, updated_at
		FROM wh_assets WHERE asset_tag = $1`, tag,
	).Scan(&a.ID, &a.AssetTag, &a.SerialNo, &a.ItemID, &a.CurrentBinID, &a.Condition, &a.BookValueRef, &a.Attrs, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

func (s *Store) CheckInAsset(ctx context.Context, assetTag, binCode string, actorID *uuid.UUID) (models.Asset, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.Asset{}, err
	}
	defer tx.Rollback(ctx)

	a, err := s.GetAssetByTag(ctx, assetTag)
	if err != nil {
		return a, err
	}
	bin, _, err := s.GetBinByCode(ctx, binCode)
	if err != nil {
		return a, err
	}

	_, err = tx.Exec(ctx, `UPDATE wh_assets SET current_bin_id = $2, updated_at = NOW() WHERE asset_tag = $1`, assetTag, bin.ID)
	if err != nil {
		return a, err
	}

	refType := refType("asset")
	_, err = s.insertMovementTx(ctx, tx, movementInput{
		MovementType: models.MovementAssetCheckin,
		ItemID:       &a.ItemID,
		ToBinID:      &bin.ID,
		Qty:          1,
		RefType:      refType,
		RefID:        &a.ID,
		ActorID:      actorID,
		Attrs:        map[string]any{"asset_tag": assetTag},
	})
	if err != nil {
		return a, err
	}

	if err := tx.Commit(ctx); err != nil {
		return a, err
	}
	return s.GetAssetByTag(ctx, assetTag)
}

type CheckOutAssetInput struct {
	AssetTag     string
	ToDepartment string
	Custodian    string
	Notes        string
	ActorID      *uuid.UUID
}

func (s *Store) CheckOutAsset(ctx context.Context, in CheckOutAssetInput) (models.Asset, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.Asset{}, err
	}
	defer tx.Rollback(ctx)

	a, err := s.GetAssetByTag(ctx, in.AssetTag)
	if err != nil {
		return a, err
	}
	var fromBinID *uuid.UUID
	if a.CurrentBinID != nil {
		fromBinID = a.CurrentBinID
	}

	_, err = tx.Exec(ctx, `UPDATE wh_assets SET current_bin_id = NULL, updated_at = NOW() WHERE asset_tag = $1`, in.AssetTag)
	if err != nil {
		return a, err
	}

	refType := refType("asset")
	_, err = s.insertMovementTx(ctx, tx, movementInput{
		MovementType: models.MovementAssetCheckout,
		ItemID:       &a.ItemID,
		FromBinID:    fromBinID,
		Qty:          1,
		RefType:      refType,
		RefID:        &a.ID,
		ActorID:      in.ActorID,
		Attrs: map[string]any{
			"asset_tag":      in.AssetTag,
			"to_department":  in.ToDepartment,
			"custodian":      in.Custodian,
		},
	})
	if err != nil {
		return a, err
	}

	if s.bus != nil && s.bus.Enabled() {
		data := map[string]any{
			"asset_tag":      in.AssetTag,
			"to_department":  in.ToDepartment,
			"custodian":      in.Custodian,
		}
		if err := s.bus.PublishTx(ctx, tx, events.TypeAssetCheckedOut, data, in.AssetTag); err != nil {
			return a, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return a, err
	}
	return s.GetAssetByTag(ctx, in.AssetTag)
}

func (s *Store) ListSparePartsByAsset(ctx context.Context, assetTag string) ([]models.Item, error) {
	a, err := s.GetAssetByTag(ctx, assetTag)
	if err != nil {
		return nil, err
	}
	item, err := s.GetItem(ctx, a.ItemID)
	if err != nil {
		return nil, err
	}
	assetType := ""
	if v, ok := item.Attrs["asset_type"].(string); ok {
		assetType = v
	}
	if assetType == "" {
		assetType = "equipment"
	}
	rows, err := s.pool.Query(ctx, `
		SELECT i.id, i.sku, i.name, i.material_class, i.tracking_mode, i.uom, i.min_qty, i.max_qty, i.attrs, i.created_at, i.updated_at
		FROM wh_items i
		JOIN wh_spare_compat sc ON sc.item_id = i.id
		WHERE sc.asset_type = $1 AND i.material_class = 'spare_part'
		ORDER BY i.sku`, assetType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanItems(rows)
}
