package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

)

type SpareCompat struct {
	ID        uuid.UUID `json:"id"`
	ItemID    uuid.UUID `json:"item_id"`
	AssetType string    `json:"asset_type"`
}

func (s *Store) ListSpareCompat(ctx context.Context, itemID *uuid.UUID) ([]SpareCompat, error) {
	var rows pgx.Rows
	var err error
	if itemID != nil {
		rows, err = s.pool.Query(ctx, `
			SELECT id, item_id, asset_type FROM wh_spare_compat WHERE item_id = $1 ORDER BY asset_type`, *itemID)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT id, item_id, asset_type FROM wh_spare_compat ORDER BY asset_type, item_id`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SpareCompat
	for rows.Next() {
		var row SpareCompat
		if err := rows.Scan(&row.ID, &row.ItemID, &row.AssetType); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) AddSpareCompat(ctx context.Context, itemID uuid.UUID, assetType string) (SpareCompat, error) {
	if _, err := s.GetItem(ctx, itemID); errors.Is(err, ErrNotFound) {
		return SpareCompat{}, ErrNotFound
	}
	var row SpareCompat
	err := s.pool.QueryRow(ctx, `
		INSERT INTO wh_spare_compat (item_id, asset_type) VALUES ($1, $2)
		ON CONFLICT (item_id, asset_type) DO UPDATE SET asset_type = EXCLUDED.asset_type
		RETURNING id, item_id, asset_type`, itemID, assetType,
	).Scan(&row.ID, &row.ItemID, &row.AssetType)
	return row, err
}

func (s *Store) DeleteSpareCompat(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM wh_spare_compat WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DefaultReceivingBinCode(ctx context.Context) string {
	if s.pool == nil {
		return "RCV-01"
	}
	var code string
	_ = s.pool.QueryRow(ctx, `
		SELECT b.code FROM wh_bins b
		JOIN wh_zones z ON z.id = b.zone_id
		WHERE z.zone_type = 'receiving' AND b.status = 'active'
		ORDER BY b.code LIMIT 1`).Scan(&code)
	if code == "" {
		return "RCV-01"
	}
	return code
}
