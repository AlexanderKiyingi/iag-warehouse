package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"iag-warehouse/backend/internal/models"
)

// Unit-of-measure conversion.
//
// Every quantity this service stores — balances, movements, costs — is in the
// item's base unit (wh_items.uom). Alternate units live in wh_item_uoms with a
// factor giving how many base units one alternate unit contains, and document
// lines are converted on entry rather than on read, so nothing downstream has to
// know that alternates exist.

// ItemUOMInput is the write shape for an alternate unit.
type ItemUOMInput struct {
	UOM               string
	Factor            float64
	IsPurchaseDefault bool
	IsSalesDefault    bool
}

func (s *Store) ListItemUOMs(ctx context.Context, itemID uuid.UUID) ([]models.ItemUOM, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, item_id, uom, factor, is_purchase_default, is_sales_default, created_at, updated_at
		FROM wh_item_uoms WHERE item_id = $1 ORDER BY factor`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.ItemUOM{}
	for rows.Next() {
		var u models.ItemUOM
		if err := rows.Scan(&u.ID, &u.ItemID, &u.UOM, &u.Factor, &u.IsPurchaseDefault, &u.IsSalesDefault, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpsertItemUOM defines (or redefines) an alternate unit. The base unit cannot
// be redefined here — it is always factor 1 by definition — and setting a
// purchase/sales default clears whichever row previously held it, since the
// partial unique indexes allow only one of each per item.
func (s *Store) UpsertItemUOM(ctx context.Context, itemID uuid.UUID, in ItemUOMInput) (models.ItemUOM, error) {
	uom := strings.TrimSpace(in.UOM)
	if uom == "" {
		return models.ItemUOM{}, fmt.Errorf("%w: uom is required", ErrInvalidArgument)
	}
	if in.Factor <= 0 {
		return models.ItemUOM{}, fmt.Errorf("%w: factor must be greater than zero", ErrInvalidArgument)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.ItemUOM{}, err
	}
	defer tx.Rollback(ctx)

	var baseUOM string
	err = tx.QueryRow(ctx, `SELECT uom FROM wh_items WHERE id = $1`, itemID).Scan(&baseUOM)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.ItemUOM{}, ErrNotFound
	}
	if err != nil {
		return models.ItemUOM{}, err
	}
	if strings.EqualFold(uom, baseUOM) {
		return models.ItemUOM{}, fmt.Errorf("%w: %q is the item's base unit and is always factor 1", ErrInvalidArgument, baseUOM)
	}

	if in.IsPurchaseDefault {
		if _, err := tx.Exec(ctx,
			`UPDATE wh_item_uoms SET is_purchase_default = FALSE, updated_at = NOW()
			 WHERE item_id = $1 AND is_purchase_default AND uom <> $2`, itemID, uom); err != nil {
			return models.ItemUOM{}, err
		}
	}
	if in.IsSalesDefault {
		if _, err := tx.Exec(ctx,
			`UPDATE wh_item_uoms SET is_sales_default = FALSE, updated_at = NOW()
			 WHERE item_id = $1 AND is_sales_default AND uom <> $2`, itemID, uom); err != nil {
			return models.ItemUOM{}, err
		}
	}

	var u models.ItemUOM
	err = tx.QueryRow(ctx, `
		INSERT INTO wh_item_uoms (item_id, uom, factor, is_purchase_default, is_sales_default)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (item_id, uom) DO UPDATE SET
			factor = EXCLUDED.factor,
			is_purchase_default = EXCLUDED.is_purchase_default,
			is_sales_default = EXCLUDED.is_sales_default,
			updated_at = NOW()
		RETURNING id, item_id, uom, factor, is_purchase_default, is_sales_default, created_at, updated_at`,
		itemID, uom, in.Factor, in.IsPurchaseDefault, in.IsSalesDefault,
	).Scan(&u.ID, &u.ItemID, &u.UOM, &u.Factor, &u.IsPurchaseDefault, &u.IsSalesDefault, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return u, err
	}
	if err := tx.Commit(ctx); err != nil {
		return u, err
	}
	return u, nil
}

func (s *Store) DeleteItemUOM(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM wh_item_uoms WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UOMConversion is the resolved result of interpreting a document line's unit.
type UOMConversion struct {
	EnteredQty float64
	EnteredUOM string
	Factor     float64
	BaseQty    float64
	BaseUOM    string
}

// ConvertToBase interprets qty expressed in uom for an item and returns the
// equivalent in the item's base unit.
//
// The lenient/strict split is deliberate. An item with no alternate units
// configured accepts any unit label at factor 1 — which is exactly how this
// service behaved before conversions existed, so existing callers and existing
// data are untouched. Once an item HAS alternate units, its unit regime is
// declared, and a label that is neither the base unit nor one of the declared
// alternates is a mistake worth refusing rather than silently valuing at 1:1.
func (s *Store) ConvertToBase(ctx context.Context, itemID uuid.UUID, qty float64, uom string) (UOMConversion, error) {
	return s.convertToBase(ctx, s.pool, itemID, qty, uom)
}

func (s *Store) convertToBase(ctx context.Context, q querier, itemID uuid.UUID, qty float64, uom string) (UOMConversion, error) {
	var baseUOM string
	err := q.QueryRow(ctx, `SELECT uom FROM wh_items WHERE id = $1`, itemID).Scan(&baseUOM)
	if errors.Is(err, pgx.ErrNoRows) {
		return UOMConversion{}, ErrNotFound
	}
	if err != nil {
		return UOMConversion{}, err
	}

	entered := strings.TrimSpace(uom)
	conv := UOMConversion{EnteredQty: qty, EnteredUOM: entered, Factor: 1, BaseQty: qty, BaseUOM: baseUOM}
	if entered == "" || strings.EqualFold(entered, baseUOM) {
		conv.EnteredUOM = baseUOM
		return conv, nil
	}

	var factor float64
	err = q.QueryRow(ctx, `SELECT factor FROM wh_item_uoms WHERE item_id = $1 AND lower(uom) = lower($2)`, itemID, entered).Scan(&factor)
	if err == nil {
		conv.Factor = factor
		conv.BaseQty = qty * factor
		return conv, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return UOMConversion{}, err
	}

	var declared int
	if err := q.QueryRow(ctx, `SELECT COUNT(*) FROM wh_item_uoms WHERE item_id = $1`, itemID).Scan(&declared); err != nil {
		return UOMConversion{}, err
	}
	if declared > 0 {
		return UOMConversion{}, fmt.Errorf("%w: unit %q is not defined for this item (base unit is %q)", ErrInvalidArgument, entered, baseUOM)
	}
	return conv, nil
}

// querier is the read surface shared by *pgxpool.Pool and pgx.Tx, so conversion
// can run either standalone or inside a document's transaction.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}
