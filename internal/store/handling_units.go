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

// Handling units (licence plates).
//
// A handling unit is a container: scanning one plate identifies everything on
// it, and moving one plate moves everything on it. Its contents are NOT a second
// stock ledger — wh_stock_balances stays authoritative and a handling unit
// records which of the stock standing in a bin is sitting on which plate. That
// keeps every existing balance, valuation and integration correct whether or not
// anyone uses plates, at the cost of one invariant this file has to hold:
// contents in a bin can never exceed the balance in that bin.

const huCols = `h.id, h.lpn, h.hu_type, h.status, h.bin_id, h.parent_hu_id, h.attrs,
	h.created_by, h.created_at, h.updated_at, h.closed_at,
	COALESCE(b.code, ''), COALESCE(p.lpn, ''),
	(SELECT COUNT(*) FROM wh_handling_units c WHERE c.parent_hu_id = h.id)`

const huFrom = `
	FROM wh_handling_units h
	LEFT JOIN wh_bins b ON b.id = h.bin_id
	LEFT JOIN wh_handling_units p ON p.id = h.parent_hu_id`

func scanHU(row pgx.Row) (models.HandlingUnit, error) {
	var h models.HandlingUnit
	err := row.Scan(&h.ID, &h.LPN, &h.HUType, &h.Status, &h.BinID, &h.ParentHUID, &h.Attrs,
		&h.CreatedBy, &h.CreatedAt, &h.UpdatedAt, &h.ClosedAt, &h.BinCode, &h.ParentLPN, &h.ChildCount)
	return h, err
}

type CreateHUInput struct {
	LPN       string
	HUType    string
	BinCode   string
	Attrs     map[string]any
	CreatedBy *uuid.UUID
}

// CreateHandlingUnit opens a plate. An empty LPN gets one from the document
// sequence, because in practice most plates are created by a terminal that has
// no label printer next to it and needs a number to print later.
func (s *Store) CreateHandlingUnit(ctx context.Context, in CreateHUInput) (models.HandlingUnit, error) {
	if in.HUType == "" {
		in.HUType = "pallet"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.HandlingUnit{}, err
	}
	defer tx.Rollback(ctx)

	lpn := strings.TrimSpace(in.LPN)
	if lpn == "" {
		lpn, err = nextDocumentNumber(ctx, tx, "hu", "LPN")
		if err != nil {
			return models.HandlingUnit{}, err
		}
	}

	var binID *uuid.UUID
	if strings.TrimSpace(in.BinCode) != "" {
		bin, _, err := s.GetBinByCode(ctx, in.BinCode)
		if err != nil {
			return models.HandlingUnit{}, err
		}
		binID = &bin.ID
	}

	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO wh_handling_units (lpn, hu_type, bin_id, attrs, created_by)
		VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		lpn, in.HUType, binID, attrsOrEmpty(in.Attrs), in.CreatedBy).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			return models.HandlingUnit{}, ErrConflict
		}
		return models.HandlingUnit{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return models.HandlingUnit{}, err
	}
	return s.GetHandlingUnit(ctx, id)
}

func (s *Store) GetHandlingUnit(ctx context.Context, id uuid.UUID) (models.HandlingUnit, error) {
	h, err := scanHU(s.pool.QueryRow(ctx, `SELECT `+huCols+huFrom+` WHERE h.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return h, ErrNotFound
	}
	if err != nil {
		return h, err
	}
	return s.withHUContents(ctx, h)
}

func (s *Store) GetHandlingUnitByLPN(ctx context.Context, lpn string) (models.HandlingUnit, error) {
	h, err := scanHU(s.pool.QueryRow(ctx, `SELECT `+huCols+huFrom+` WHERE h.lpn = $1`, lpn))
	if errors.Is(err, pgx.ErrNoRows) {
		return h, ErrNotFound
	}
	if err != nil {
		return h, err
	}
	return s.withHUContents(ctx, h)
}

func (s *Store) withHUContents(ctx context.Context, h models.HandlingUnit) (models.HandlingUnit, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.hu_id, c.item_id, c.lot_key, c.serial_key, c.qty, c.updated_at, i.sku, i.name
		FROM wh_hu_contents c JOIN wh_items i ON i.id = c.item_id
		WHERE c.hu_id = $1 AND c.qty > 0 ORDER BY i.sku`, h.ID)
	if err != nil {
		return h, err
	}
	defer rows.Close()
	h.Contents = []models.HUContent{}
	for rows.Next() {
		var c models.HUContent
		if err := rows.Scan(&c.ID, &c.HUID, &c.ItemID, &c.LotKey, &c.SerialKey, &c.Qty, &c.UpdatedAt, &c.ItemSKU, &c.ItemName); err != nil {
			return h, err
		}
		h.Contents = append(h.Contents, c)
	}
	return h, rows.Err()
}

func (s *Store) ListHandlingUnits(ctx context.Context, status, binCode string, limit int) ([]models.HandlingUnit, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	query := `SELECT ` + huCols + huFrom + ` WHERE TRUE`
	args := []any{}
	if status != "" {
		args = append(args, status)
		query += fmt.Sprintf(` AND h.status = $%d`, len(args))
	}
	if binCode != "" {
		args = append(args, binCode)
		query += fmt.Sprintf(` AND b.code = $%d`, len(args))
	}
	args = append(args, limit)
	query += fmt.Sprintf(` ORDER BY h.created_at DESC LIMIT $%d`, len(args))

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.HandlingUnit{}
	for rows.Next() {
		h, err := scanHU(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// AddToHandlingUnit puts stock that is already in the plate's bin onto the
// plate. It moves nothing: the stock was and remains in that bin, and all that
// changes is the record of what it is sitting on.
//
// The guard is the invariant this design rests on. Free stock here means the
// bin's balance for that key less whatever is already on other plates in the
// same bin, so two plates can never claim the same physical sack.
func (s *Store) AddToHandlingUnit(ctx context.Context, huID, itemID uuid.UUID, lotKey, serialKey string, qty float64) (models.HandlingUnit, error) {
	if qty <= 0 {
		return models.HandlingUnit{}, fmt.Errorf("%w: qty must be greater than zero", ErrInvalidArgument)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.HandlingUnit{}, err
	}
	defer tx.Rollback(ctx)

	var binID *uuid.UUID
	var status string
	err = tx.QueryRow(ctx, `SELECT bin_id, status FROM wh_handling_units WHERE id = $1 FOR UPDATE`, huID).Scan(&binID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.HandlingUnit{}, ErrNotFound
	}
	if err != nil {
		return models.HandlingUnit{}, err
	}
	if status != models.HUOpen {
		return models.HandlingUnit{}, fmt.Errorf("%w: handling unit is %s and cannot be loaded", ErrConflict, status)
	}
	if binID == nil {
		return models.HandlingUnit{}, fmt.Errorf("%w: handling unit has no bin — move it to a bin before loading it", ErrInvalidArgument)
	}

	lot, serial := normalizeKeys(lotKey, serialKey)
	var onHand float64
	err = tx.QueryRow(ctx, `
		SELECT qty FROM wh_stock_balances
		WHERE item_id = $1 AND bin_id = $2 AND lot_key = $3 AND serial_key = $4 AND status = 'available'
		FOR UPDATE`, itemID, *binID, lot, serial).Scan(&onHand)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.HandlingUnit{}, ErrInsufficientStock
	}
	if err != nil {
		return models.HandlingUnit{}, err
	}

	var containerised float64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(c.qty), 0)
		FROM wh_hu_contents c JOIN wh_handling_units h ON h.id = c.hu_id
		WHERE h.bin_id = $1 AND c.item_id = $2 AND c.lot_key = $3 AND c.serial_key = $4
		  AND h.status IN ('open', 'closed')`,
		*binID, itemID, lot, serial).Scan(&containerised); err != nil {
		return models.HandlingUnit{}, err
	}
	if onHand-containerised < qty {
		return models.HandlingUnit{}, ErrInsufficientStock
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO wh_hu_contents (hu_id, item_id, lot_key, serial_key, qty)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (hu_id, item_id, lot_key, serial_key)
		DO UPDATE SET qty = wh_hu_contents.qty + EXCLUDED.qty, updated_at = NOW()`,
		huID, itemID, lot, serial, qty); err != nil {
		return models.HandlingUnit{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return models.HandlingUnit{}, err
	}
	return s.GetHandlingUnit(ctx, huID)
}

// RemoveFromHandlingUnit takes stock off the plate and leaves it loose in the
// same bin. Again nothing moves — this is the inverse bookkeeping of Add.
func (s *Store) RemoveFromHandlingUnit(ctx context.Context, huID, itemID uuid.UUID, lotKey, serialKey string, qty float64) (models.HandlingUnit, error) {
	if qty <= 0 {
		return models.HandlingUnit{}, fmt.Errorf("%w: qty must be greater than zero", ErrInvalidArgument)
	}
	lot, serial := normalizeKeys(lotKey, serialKey)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.HandlingUnit{}, err
	}
	defer tx.Rollback(ctx)

	var current float64
	err = tx.QueryRow(ctx, `
		SELECT qty FROM wh_hu_contents
		WHERE hu_id = $1 AND item_id = $2 AND lot_key = $3 AND serial_key = $4 FOR UPDATE`,
		huID, itemID, lot, serial).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.HandlingUnit{}, ErrNotFound
	}
	if err != nil {
		return models.HandlingUnit{}, err
	}
	if current < qty {
		return models.HandlingUnit{}, fmt.Errorf("%w: handling unit holds %.3f", ErrInsufficientStock, current)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE wh_hu_contents SET qty = qty - $5, updated_at = NOW()
		WHERE hu_id = $1 AND item_id = $2 AND lot_key = $3 AND serial_key = $4`,
		huID, itemID, lot, serial, qty); err != nil {
		return models.HandlingUnit{}, err
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM wh_hu_contents WHERE hu_id = $1 AND item_id = $2 AND lot_key = $3 AND serial_key = $4 AND qty = 0`,
		huID, itemID, lot, serial); err != nil {
		return models.HandlingUnit{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return models.HandlingUnit{}, err
	}
	return s.GetHandlingUnit(ctx, huID)
}

// MoveHandlingUnit relocates a plate and everything on it, nested plates
// included, posting one ordinary transfer movement per content line so the
// ledger records a pallet move exactly as it records any other move.
//
// Stock allocated to an open pick list will refuse to move. That is deliberate:
// the pick has already told someone to go to a specific bin, and quietly moving
// the goods out from under it turns a picking instruction into a wild goose
// chase. Cancel the pick, or unload the reserved stock, then move the plate.
func (s *Store) MoveHandlingUnit(ctx context.Context, huID uuid.UUID, toBinCode string, actorID *uuid.UUID) (models.HandlingUnit, error) {
	toBin, _, err := s.GetBinByCode(ctx, toBinCode)
	if err != nil {
		return models.HandlingUnit{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.HandlingUnit{}, err
	}
	defer tx.Rollback(ctx)

	var fromBin *uuid.UUID
	var status string
	err = tx.QueryRow(ctx, `SELECT bin_id, status FROM wh_handling_units WHERE id = $1 FOR UPDATE`, huID).Scan(&fromBin, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.HandlingUnit{}, ErrNotFound
	}
	if err != nil {
		return models.HandlingUnit{}, err
	}
	if status != models.HUOpen && status != models.HUClosed {
		return models.HandlingUnit{}, fmt.Errorf("%w: handling unit is %s and cannot be moved", ErrConflict, status)
	}
	if fromBin != nil && *fromBin == toBin.ID {
		return s.GetHandlingUnit(ctx, huID)
	}

	// The subtree is the plate and anything nested on it. Depth is bounded so a
	// parent cycle introduced by a bad edit cannot spin here forever.
	rows, err := tx.Query(ctx, `
		WITH RECURSIVE tree AS (
			SELECT id, 1 AS depth FROM wh_handling_units WHERE id = $1
			UNION ALL
			SELECT h.id, t.depth + 1 FROM wh_handling_units h JOIN tree t ON h.parent_hu_id = t.id
			WHERE t.depth < 10
		)
		SELECT c.hu_id, c.item_id, c.lot_key, c.serial_key, c.qty, i.sku
		FROM wh_hu_contents c
		JOIN tree t ON t.id = c.hu_id
		JOIN wh_items i ON i.id = c.item_id
		WHERE c.qty > 0`, huID)
	if err != nil {
		return models.HandlingUnit{}, err
	}
	type contentRow struct {
		huID, itemID      uuid.UUID
		lotKey, serialKey string
		qty               float64
		sku               string
	}
	var contents []contentRow
	for rows.Next() {
		var c contentRow
		if err := rows.Scan(&c.huID, &c.itemID, &c.lotKey, &c.serialKey, &c.qty, &c.sku); err != nil {
			rows.Close()
			return models.HandlingUnit{}, err
		}
		contents = append(contents, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return models.HandlingUnit{}, err
	}
	if len(contents) > 0 && fromBin == nil {
		return models.HandlingUnit{}, fmt.Errorf("%w: handling unit holds stock but has no source bin", ErrConflict)
	}

	for _, c := range contents {
		if err := s.deductAvailableBalanceTx(ctx, tx, balanceKey{c.itemID, *fromBin, c.lotKey, c.serialKey}, c.qty); err != nil {
			return models.HandlingUnit{}, err
		}
		if err := s.adjustBalanceTx(ctx, tx, balanceKey{c.itemID, toBin.ID, c.lotKey, c.serialKey}, c.qty, models.StatusAvailable); err != nil {
			return models.HandlingUnit{}, err
		}
		movID, err := s.insertMovementTx(ctx, tx, movementInput{
			MovementType: models.MovementTransfer,
			ItemID:       &c.itemID,
			FromBinID:    fromBin,
			ToBinID:      &toBin.ID,
			Qty:          c.qty,
			LotKey:       c.lotKey,
			SerialKey:    c.serialKey,
			RefType:      refType("handling_unit"),
			RefID:        &huID,
			ActorID:      actorID,
		})
		if err != nil {
			return models.HandlingUnit{}, err
		}
		if err := s.emitInventoryMovement(ctx, tx, movID, models.MovementTransfer, c.itemID, c.sku, fromBin, &toBin.ID, c.qty, c.lotKey, c.serialKey, nil, movementCost{}, ""); err != nil {
			return models.HandlingUnit{}, err
		}
	}

	if _, err := tx.Exec(ctx, `
		WITH RECURSIVE tree AS (
			SELECT id, 1 AS depth FROM wh_handling_units WHERE id = $1
			UNION ALL
			SELECT h.id, t.depth + 1 FROM wh_handling_units h JOIN tree t ON h.parent_hu_id = t.id
			WHERE t.depth < 10
		)
		UPDATE wh_handling_units SET bin_id = $2, updated_at = NOW()
		WHERE id IN (SELECT id FROM tree)`, huID, toBin.ID); err != nil {
		return models.HandlingUnit{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return models.HandlingUnit{}, err
	}
	return s.GetHandlingUnit(ctx, huID)
}

// NestHandlingUnit puts one plate on another (cartons onto a pallet). Both must
// stand in the same bin, since a plate cannot be in two places, and a plate
// cannot be nested under its own descendant.
func (s *Store) NestHandlingUnit(ctx context.Context, huID uuid.UUID, parentLPN string) (models.HandlingUnit, error) {
	if strings.TrimSpace(parentLPN) == "" {
		if _, err := s.pool.Exec(ctx,
			`UPDATE wh_handling_units SET parent_hu_id = NULL, updated_at = NOW() WHERE id = $1`, huID); err != nil {
			return models.HandlingUnit{}, err
		}
		return s.GetHandlingUnit(ctx, huID)
	}

	parent, err := s.GetHandlingUnitByLPN(ctx, parentLPN)
	if err != nil {
		return models.HandlingUnit{}, err
	}
	child, err := s.GetHandlingUnit(ctx, huID)
	if err != nil {
		return models.HandlingUnit{}, err
	}
	if parent.ID == child.ID {
		return models.HandlingUnit{}, fmt.Errorf("%w: a handling unit cannot be nested on itself", ErrInvalidArgument)
	}
	if !sameBin(child.BinID, parent.BinID) {
		return models.HandlingUnit{}, fmt.Errorf("%w: both handling units must be in the same bin", ErrInvalidArgument)
	}

	var cycle bool
	if err := s.pool.QueryRow(ctx, `
		WITH RECURSIVE tree AS (
			SELECT id, 1 AS depth FROM wh_handling_units WHERE id = $1
			UNION ALL
			SELECT h.id, t.depth + 1 FROM wh_handling_units h JOIN tree t ON h.parent_hu_id = t.id
			WHERE t.depth < 10
		)
		SELECT EXISTS (SELECT 1 FROM tree WHERE id = $2)`, huID, parent.ID).Scan(&cycle); err != nil {
		return models.HandlingUnit{}, err
	}
	if cycle {
		return models.HandlingUnit{}, fmt.Errorf("%w: that would nest a handling unit inside its own contents", ErrInvalidArgument)
	}

	if _, err := s.pool.Exec(ctx,
		`UPDATE wh_handling_units SET parent_hu_id = $2, updated_at = NOW() WHERE id = $1`, huID, parent.ID); err != nil {
		return models.HandlingUnit{}, err
	}
	return s.GetHandlingUnit(ctx, huID)
}

func sameBin(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// SetHandlingUnitStatus closes, reopens or retires a plate. Closing is a
// build-complete marker rather than a stock event, so it does not touch
// balances; a plate that has shipped or been consumed keeps its contents as the
// record of what was on it when it left.
func (s *Store) SetHandlingUnitStatus(ctx context.Context, huID uuid.UUID, status string) (models.HandlingUnit, error) {
	switch status {
	case models.HUOpen, models.HUClosed, models.HUShipped, models.HUConsumed, models.HUCancelled:
	default:
		return models.HandlingUnit{}, fmt.Errorf("%w: unknown handling unit status %q", ErrInvalidArgument, status)
	}
	closedAt := "NULL"
	if status != models.HUOpen {
		closedAt = "NOW()"
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE wh_handling_units SET status = $2, closed_at = `+closedAt+`, updated_at = NOW() WHERE id = $1`,
		huID, status)
	if err != nil {
		return models.HandlingUnit{}, err
	}
	if tag.RowsAffected() == 0 {
		return models.HandlingUnit{}, ErrNotFound
	}
	return s.GetHandlingUnit(ctx, huID)
}
