package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"iag-warehouse/backend/internal/inventory"
	"iag-warehouse/backend/internal/models"
)

type balanceKey struct {
	ItemID    uuid.UUID
	BinID     uuid.UUID
	LotKey    string
	SerialKey string
}

func normalizeKeys(lotKey, serialKey string) (string, string) {
	if lotKey == "" {
		lotKey = ""
	}
	if serialKey == "" {
		serialKey = ""
	}
	return lotKey, serialKey
}

// deductAvailableBalanceTx removes qty only from an available balance row (blocks hold/damaged).
func (s *Store) deductAvailableBalanceTx(ctx context.Context, tx pgx.Tx, key balanceKey, qty float64) error {
	if qty <= 0 {
		return nil
	}
	lotKey, serialKey := normalizeKeys(key.LotKey, key.SerialKey)
	var currentQty float64
	var status string
	err := tx.QueryRow(ctx, `
		SELECT qty, status FROM wh_stock_balances
		WHERE item_id = $1 AND bin_id = $2 AND lot_key = $3 AND serial_key = $4
		FOR UPDATE`,
		key.ItemID, key.BinID, lotKey, serialKey,
	).Scan(&currentQty, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInsufficientStock
	}
	if err != nil {
		return err
	}
	if status != models.StatusAvailable {
		return ErrStockNotAvailable
	}
	if currentQty < qty {
		return ErrInsufficientStock
	}
	newQty := currentQty - qty
	_, err = tx.Exec(ctx, `
		UPDATE wh_stock_balances SET qty = $5, updated_at = NOW()
		WHERE item_id = $1 AND bin_id = $2 AND lot_key = $3 AND serial_key = $4`,
		key.ItemID, key.BinID, lotKey, serialKey, newQty,
	)
	return err
}

func (s *Store) adjustBalanceTx(ctx context.Context, tx pgx.Tx, key balanceKey, delta float64, status string) error {
	lotKey, serialKey := normalizeKeys(key.LotKey, key.SerialKey)
	if status == "" {
		status = models.StatusAvailable
	}
	var newQty float64
	err := tx.QueryRow(ctx, `
		INSERT INTO wh_stock_balances (item_id, bin_id, lot_key, serial_key, qty, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (item_id, bin_id, lot_key, serial_key) DO UPDATE SET
			qty = wh_stock_balances.qty + EXCLUDED.qty,
			status = CASE WHEN EXCLUDED.qty = 0 THEN wh_stock_balances.status ELSE $6 END,
			updated_at = NOW()
		RETURNING qty`,
		key.ItemID, key.BinID, lotKey, serialKey, delta, status,
	).Scan(&newQty)
	if err != nil {
		return err
	}
	if newQty < 0 {
		return ErrInsufficientStock
	}
	return nil
}

func (s *Store) setBalanceStatusTx(ctx context.Context, tx pgx.Tx, itemID, binID uuid.UUID, lotKey, status string) error {
	lotKey, _ = normalizeKeys(lotKey, "")
	tag, err := tx.Exec(ctx, `
		UPDATE wh_stock_balances SET status = $4, updated_at = NOW()
		WHERE item_id = $1 AND bin_id = $2 AND lot_key = $3`,
		itemID, binID, lotKey, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) insertMovementTx(ctx context.Context, tx pgx.Tx, in movementInput) (uuid.UUID, error) {
	lotKey, serialKey := normalizeKeys(in.LotKey, in.SerialKey)
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO wh_movements (
			movement_type, item_id, from_bin_id, to_bin_id, qty, lot_key, serial_key,
			ref_type, ref_id, batch_business_id, actor_id, occurred_at, attrs
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,COALESCE($12,NOW()),$13)
		RETURNING id`,
		in.MovementType, in.ItemID, in.FromBinID, in.ToBinID, in.Qty, lotKey, serialKey,
		in.RefType, in.RefID, in.BatchBusinessID, in.ActorID, in.OccurredAt, in.Attrs,
	).Scan(&id)
	return id, err
}

type movementInput struct {
	MovementType    string
	ItemID          *uuid.UUID
	FromBinID       *uuid.UUID
	ToBinID         *uuid.UUID
	Qty             float64
	LotKey          string
	SerialKey       string
	RefType         *string
	RefID           *uuid.UUID
	BatchBusinessID *string
	ActorID         *uuid.UUID
	OccurredAt      *time.Time
	Attrs           map[string]any
}

type LowStockItem struct {
	ItemID  uuid.UUID `json:"item_id"`
	SKU     string    `json:"sku"`
	Name    string    `json:"name"`
	Qty     float64   `json:"qty"`
	MinQty  float64   `json:"min_qty"`
	BinID   uuid.UUID `json:"bin_id"`
	BinCode string    `json:"bin_code"`
}

func (s *Store) ListLowStock(ctx context.Context) ([]LowStockItem, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT i.id, i.sku, i.name, COALESCE(SUM(b.qty), 0), i.min_qty, b.bin_id, bn.code
		FROM wh_items i
		JOIN wh_stock_balances b ON b.item_id = i.id AND b.status = 'available'
		JOIN wh_bins bn ON bn.id = b.bin_id
		WHERE i.min_qty > 0
		GROUP BY i.id, i.sku, i.name, i.min_qty, b.bin_id, bn.code
		HAVING COALESCE(SUM(b.qty), 0) < i.min_qty`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LowStockItem
	for rows.Next() {
		var row LowStockItem
		if err := rows.Scan(&row.ItemID, &row.SKU, &row.Name, &row.Qty, &row.MinQty, &row.BinID, &row.BinCode); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) emitInventoryMovement(ctx context.Context, movementID uuid.UUID, movementType string, itemID uuid.UUID, sku string, fromBin, toBin *uuid.UUID, qty float64, lotKey, serialKey string, batchID *string) {
	if s.invBridge == nil {
		return
	}
	payload := inventory.MovementPayload{
		MovementID:   movementID.String(),
		MovementType: movementType,
		ItemID:       itemID.String(),
		SKU:          sku,
		Qty:          qty,
		LotKey:       lotKey,
		SerialKey:    serialKey,
	}
	if fromBin != nil {
		payload.FromBinID = fromBin.String()
	}
	if toBin != nil {
		payload.ToBinID = toBin.String()
	}
	if batchID != nil {
		payload.BatchBusinessID = *batchID
	}
	s.invBridge.EmitMovementPosted(ctx, payload)
}

// pickAvailableBinCode chooses a concrete bin to issue an item from when the
// caller didn't specify one. It prefers the smallest available bin that still
// holds the full requested qty (best-fit, so large bins aren't fragmented);
// if no single bin can satisfy the qty it falls back to the fullest bin so the
// downstream deduction returns a clean ErrInsufficientStock rather than a
// confusing "bin not found". Lot/serial-tracked lines must still target an
// exact balance, so the chosen bin is scoped to the (lot, serial) on the line.
func (s *Store) pickAvailableBinCode(ctx context.Context, itemID uuid.UUID, qty float64, lotKey, serialKey string) (string, error) {
	lotKey, serialKey = normalizeKeys(lotKey, serialKey)
	var code string
	err := s.pool.QueryRow(ctx, `
		SELECT bn.code
		FROM wh_stock_balances b
		JOIN wh_bins bn ON bn.id = b.bin_id
		WHERE b.item_id = $1 AND b.status = 'available'
		  AND b.lot_key = $2 AND b.serial_key = $3 AND b.qty >= $4
		ORDER BY b.qty ASC
		LIMIT 1`, itemID, lotKey, serialKey, qty).Scan(&code)
	if errors.Is(err, pgx.ErrNoRows) {
		err = s.pool.QueryRow(ctx, `
			SELECT bn.code
			FROM wh_stock_balances b
			JOIN wh_bins bn ON bn.id = b.bin_id
			WHERE b.item_id = $1 AND b.status = 'available'
			  AND b.lot_key = $2 AND b.serial_key = $3 AND b.qty > 0
			ORDER BY b.qty DESC
			LIMIT 1`, itemID, lotKey, serialKey).Scan(&code)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrInsufficientStock
	}
	return code, err
}

func (s *Store) getItemSKU(ctx context.Context, tx pgx.Tx, itemID uuid.UUID) (string, error) {
	var sku string
	err := tx.QueryRow(ctx, `SELECT sku FROM wh_items WHERE id = $1`, itemID).Scan(&sku)
	return sku, err
}

func attrsOrEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func refType(s string) *string { return &s }

func fmtErr(wrap string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", wrap, err)
}
