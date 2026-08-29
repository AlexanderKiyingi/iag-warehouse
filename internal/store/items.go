package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"iag-warehouse/backend/internal/models"
)

func (s *Store) ListItems(ctx context.Context, materialClass string) ([]models.Item, error) {
	var rows pgx.Rows
	var err error
	if materialClass != "" {
		rows, err = s.pool.Query(ctx, `
			SELECT id, sku, name, material_class, tracking_mode, uom, min_qty, max_qty, status, attrs, created_at, updated_at
			FROM wh_items WHERE material_class = $1 ORDER BY sku`, materialClass)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT id, sku, name, material_class, tracking_mode, uom, min_qty, max_qty, status, attrs, created_at, updated_at
			FROM wh_items ORDER BY sku`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanItems(rows)
}

func (s *Store) CreateItem(ctx context.Context, sku, name, materialClass, trackingMode, uom string, minQty float64, maxQty *float64, attrs map[string]any) (models.Item, error) {
	var item models.Item
	err := s.pool.QueryRow(ctx, `
		INSERT INTO wh_items (sku, name, material_class, tracking_mode, uom, min_qty, max_qty, attrs)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, sku, name, material_class, tracking_mode, uom, min_qty, max_qty, status, attrs, created_at, updated_at`,
		sku, name, materialClass, trackingMode, uom, minQty, maxQty, attrsOrEmpty(attrs),
	).Scan(&item.ID, &item.SKU, &item.Name, &item.MaterialClass, &item.TrackingMode, &item.UOM, &item.MinQty, &item.MaxQty, &item.Status, &item.Attrs, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func (s *Store) GetItemBySKU(ctx context.Context, sku string) (models.Item, error) {
	var item models.Item
	err := s.pool.QueryRow(ctx, `
		SELECT id, sku, name, material_class, tracking_mode, uom, min_qty, max_qty, status, attrs, created_at, updated_at
		FROM wh_items WHERE sku = $1`, sku,
	).Scan(&item.ID, &item.SKU, &item.Name, &item.MaterialClass, &item.TrackingMode, &item.UOM, &item.MinQty, &item.MaxQty, &item.Status, &item.Attrs, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, ErrNotFound
	}
	return item, err
}

func (s *Store) GetItemIDBySKU(ctx context.Context, sku string) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT id FROM wh_items WHERE sku = $1`, sku).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return id, err
}

func (s *Store) GetItem(ctx context.Context, id uuid.UUID) (models.Item, error) {
	var item models.Item
	err := s.pool.QueryRow(ctx, `
		SELECT id, sku, name, material_class, tracking_mode, uom, min_qty, max_qty, status, attrs, created_at, updated_at
		FROM wh_items WHERE id = $1`, id,
	).Scan(&item.ID, &item.SKU, &item.Name, &item.MaterialClass, &item.TrackingMode, &item.UOM, &item.MinQty, &item.MaxQty, &item.Status, &item.Attrs, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, ErrNotFound
	}
	return item, err
}

// UpdateItemInput is a patch: every nil field is left as it is.
//
// Attrs is replace-not-merge, matching the previous behaviour. A caller that
// wants to keep existing keys reads the item first and sends the union — which
// is what a form does anyway, since it renders the stored values.
type UpdateItemInput struct {
	Name          *string
	SKU           *string
	UOM           *string
	MaterialClass *string
	TrackingMode  *string
	MinQty        *float64
	MaxQty        *float64
	Attrs         map[string]any
}

// ErrDuplicateSKU is a unique-violation on wh_items.sku, which migration 032
// made unique. It is a caller conflict, not a server failure.
var ErrDuplicateSKU = errors.New("duplicate sku")

func (s *Store) UpdateItem(ctx context.Context, id uuid.UUID, in UpdateItemInput) (models.Item, error) {
	item, err := s.GetItem(ctx, id)
	if err != nil {
		return item, err
	}
	if in.Name != nil {
		item.Name = *in.Name
	}
	if in.SKU != nil {
		item.SKU = *in.SKU
	}
	if in.UOM != nil {
		item.UOM = *in.UOM
	}
	if in.MaterialClass != nil {
		item.MaterialClass = *in.MaterialClass
	}
	if in.TrackingMode != nil {
		item.TrackingMode = *in.TrackingMode
	}
	if in.MinQty != nil {
		item.MinQty = *in.MinQty
	}
	if in.MaxQty != nil {
		item.MaxQty = in.MaxQty
	}
	if in.Attrs != nil {
		item.Attrs = in.Attrs
	}
	err = s.pool.QueryRow(ctx, `
		UPDATE wh_items
		SET name = $2, sku = $3, uom = $4, material_class = $5, tracking_mode = $6,
		    min_qty = $7, max_qty = $8, attrs = $9, updated_at = NOW()
		WHERE id = $1
		RETURNING id, sku, name, material_class, tracking_mode, uom, min_qty, max_qty, status, attrs, created_at, updated_at`,
		id, item.Name, item.SKU, item.UOM, item.MaterialClass, item.TrackingMode,
		item.MinQty, item.MaxQty, item.Attrs,
	).Scan(&item.ID, &item.SKU, &item.Name, &item.MaterialClass, &item.TrackingMode, &item.UOM, &item.MinQty, &item.MaxQty, &item.Status, &item.Attrs, &item.CreatedAt, &item.UpdatedAt)
	if isUniqueViolation(err) {
		return item, ErrDuplicateSKU
	}
	return item, err
}

func (s *Store) ListItemBalances(ctx context.Context, itemID uuid.UUID) ([]models.StockBalance, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT b.id, b.item_id, b.bin_id, b.lot_key, b.serial_key, b.qty, b.reserved, b.status, b.updated_at, i.sku, bn.code
		FROM wh_stock_balances b
		JOIN wh_items i ON i.id = b.item_id
		JOIN wh_bins bn ON bn.id = b.bin_id
		WHERE b.item_id = $1 AND b.qty > 0
		ORDER BY bn.code`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.StockBalance
	for rows.Next() {
		var bal models.StockBalance
		if err := rows.Scan(&bal.ID, &bal.ItemID, &bal.BinID, &bal.LotKey, &bal.SerialKey, &bal.Qty, &bal.Reserved, &bal.Status, &bal.UpdatedAt, &bal.ItemSKU, &bal.BinCode); err != nil {
			return nil, err
		}
		bal.Available = bal.Qty - bal.Reserved
		out = append(out, bal)
	}
	return out, rows.Err()
}

func scanItems(rows pgx.Rows) ([]models.Item, error) {
	var out []models.Item
	for rows.Next() {
		var item models.Item
		if err := rows.Scan(&item.ID, &item.SKU, &item.Name, &item.MaterialClass, &item.TrackingMode, &item.UOM, &item.MinQty, &item.MaxQty, &item.Status, &item.Attrs, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
