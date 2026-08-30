package store

import (
	"context"
	"encoding/json"
	"errors"
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
	// QtyDelta, when set, wins over QtyAfter: the closing quantity is computed
	// as (current + delta) inside the same transaction that reads current.
	//
	// This exists because the two callers mean different things. A counting
	// client knows the absolute figure — it just counted the bin. A movement
	// client knows only the change ("wrote off 5") and would have to read the
	// balance first to convert, which is both a race and, if it guessed and
	// sent the delta as an absolute, a silent overwrite of the true quantity
	// with the size of the movement.
	QtyDelta *float64
	Reason   *string
	AdjType  string
	ActorID  *uuid.UUID

	// Write-off provenance (migration 034). All optional: an adjustment raised
	// by a peer service or by count approval knows none of it.
	ReasonCode     string
	ExpenseAccount string
	EvidenceNotes  string
	// Attachments is the caller's reference list, passed through untouched.
	// Nil means "not sent" and leaves the column alone.
	Attachments json.RawMessage
	// DeclaredUnitCost and DeclaredValue are what the raiser says the movement
	// was worth. Exactly one is expected from a client; the other is derived
	// from the quantity actually moved, below, so the two can never disagree.
	// Neither feeds the GL — that is the costing engine's number.
	DeclaredUnitCost *float64
	DeclaredValue    *float64
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
	adj, err := s.applyStockChangeTx(ctx, tx, in, bin.ID)
	if err != nil {
		return adj, err
	}
	if err := tx.Commit(ctx); err != nil {
		return adj, err
	}
	return adj, nil
}

// applyStockChangeTx is applyStockChange's body against a caller-supplied
// transaction and an already-resolved bin. Count approval posts many variances
// that must all land or none of them land, so it needs to drive the adjustment
// path itself rather than call it once per line and hope.
// resolveQtyAfter turns whichever quantity the caller sent into the closing
// quantity to store.
//
// Kept separate from the transaction so the rule can be tested on its own: this
// is the arithmetic that decides what the stock becomes, and getting it backwards
// silently sets a bin to the size of a movement rather than moving it.
func resolveQtyAfter(in AdjustmentInput, qtyBefore float64) float64 {
	if in.QtyDelta != nil {
		return qtyBefore + *in.QtyDelta
	}
	return in.QtyAfter
}

// resolveDeclaredValue completes the raiser's valuation from whichever half of
// it they sent, against the quantity that actually moved.
//
// Same shape as resolveQtyAfter and for the same reason: a screen knows either
// a unit cost or a total, never reliably both, and storing an unreconciled pair
// leaves two numbers that can disagree about one movement. Derivation happens
// against |delta| computed in the transaction, not against a quantity the
// client sent, so a partially-applied movement values what it actually moved.
func resolveDeclaredValue(in AdjustmentInput, qtyMoved float64) (unitCost, value *float64) {
	if in.DeclaredUnitCost == nil && in.DeclaredValue == nil {
		return nil, nil
	}
	if in.DeclaredUnitCost != nil && in.DeclaredValue != nil {
		// Both given: trust them as sent rather than silently restating one.
		// The handler refuses this combination, so reaching it means an
		// internal caller meant it.
		return in.DeclaredUnitCost, in.DeclaredValue
	}
	if in.DeclaredUnitCost != nil {
		total := *in.DeclaredUnitCost * qtyMoved
		return in.DeclaredUnitCost, &total
	}
	if qtyMoved == 0 {
		// No quantity moved, so no unit cost is derivable. The declared total
		// still stands on its own.
		return nil, in.DeclaredValue
	}
	unit := *in.DeclaredValue / qtyMoved
	return &unit, in.DeclaredValue
}

// nullableJSON turns "the caller sent nothing" into a real SQL NULL.
//
// An empty json.RawMessage is a zero-length byte slice, and pgx sends that to a
// jsonb column as an empty string, which is not valid JSON and fails the insert.
// The distinction matters: nil means "leave it alone", not "store nothing".
func nullableJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return []byte(raw)
}

// adjustmentEventAttrs is the write-off provenance as the movement event
// carries it. Nil when there is nothing to say, so an ordinary adjustment's
// payload is byte-for-byte what it was before.
func adjustmentEventAttrs(in AdjustmentInput, unitCost, value *float64) map[string]any {
	attrs := map[string]any{}
	if in.ReasonCode != "" {
		attrs["reason_code"] = in.ReasonCode
	}
	if in.ExpenseAccount != "" {
		attrs["expense_account"] = in.ExpenseAccount
	}
	if unitCost != nil {
		attrs["declared_unit_cost"] = *unitCost
	}
	if value != nil {
		attrs["declared_value"] = *value
	}
	if len(attrs) == 0 {
		return nil
	}
	return attrs
}

func (s *Store) applyStockChangeTx(ctx context.Context, tx pgx.Tx, in AdjustmentInput, binID uuid.UUID) (models.Adjustment, error) {
	lotKey, serialKey := normalizeKeys(in.LotKey, in.SerialKey)

	var qtyBefore float64
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(qty, 0) FROM wh_stock_balances
		WHERE item_id = $1 AND bin_id = $2 AND lot_key = $3 AND serial_key = $4`,
		in.ItemID, binID, lotKey, serialKey).Scan(&qtyBefore)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return models.Adjustment{}, err
	}

	// Resolve a delta against the quantity just read, under the same transaction,
	// so a concurrent movement cannot land between the read and the write.
	qtyAfter := resolveQtyAfter(in, qtyBefore)

	delta := qtyAfter - qtyBefore
	if delta != 0 {
		if err := s.adjustBalanceTx(ctx, tx, balanceKey{in.ItemID, binID, lotKey, serialKey}, delta, models.StatusAvailable); err != nil {
			return models.Adjustment{}, err
		}
	}

	declaredUnitCost, declaredValue := resolveDeclaredValue(in, abs(delta))

	var adj models.Adjustment
	err = tx.QueryRow(ctx, `
		INSERT INTO wh_adjustments (adj_type, item_id, bin_id, lot_key, serial_key, qty_before, qty_after, reason, actor_id,
			reason_code, expense_account, unit_cost, value, evidence_notes, attachments)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NULLIF($10,''), NULLIF($11,''), $12, $13, NULLIF($14,''), $15::jsonb)
		RETURNING id, adj_type, item_id, bin_id, lot_key, serial_key, qty_before, qty_after, reason, actor_id, created_at,
			reason_code, expense_account, unit_cost, value, evidence_notes, attachments`,
		in.AdjType, in.ItemID, binID, lotKey, serialKey, qtyBefore, qtyAfter, in.Reason, in.ActorID,
		in.ReasonCode, in.ExpenseAccount, declaredUnitCost, declaredValue, in.EvidenceNotes,
		nullableJSON(in.Attachments),
	).Scan(&adj.ID, &adj.AdjType, &adj.ItemID, &adj.BinID, &adj.LotKey, &adj.SerialKey, &adj.QtyBefore, &adj.QtyAfter, &adj.Reason, &adj.ActorID, &adj.CreatedAt,
		&adj.ReasonCode, &adj.ExpenseAccount, &adj.UnitCost, &adj.Value, &adj.EvidenceNotes, &adj.Attachments)
	if err != nil {
		return adj, err
	}

	refType := refType("adjustment")
	sku, _ := s.getItemSKU(ctx, tx, in.ItemID)
	movID, err := s.insertMovementTx(ctx, tx, movementInput{
		MovementType: models.MovementAdjustment,
		ItemID:       &in.ItemID,
		FromBinID:    ptrIf(delta < 0, binID),
		ToBinID:      ptrIf(delta > 0, binID),
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
			fromBin = &binID
		} else {
			toBin = &binID
		}
		cost, err := s.adjustmentCostTx(ctx, tx, in.ItemID, delta, adj.ID.String())
		if err != nil {
			return adj, err
		}
		// The raiser's account and valuation travel with the movement so
		// finance can reconcile against them. They are prefixed `declared_`
		// and left out of the cost fields on purpose: the GL amount is the
		// costing engine's, and a hand-typed figure quietly taking its place
		// would be the one change here nobody could see afterwards.
		if err := s.emitInventoryMovementWithAttrs(ctx, tx, movID, models.MovementAdjustment, in.ItemID, sku, fromBin, toBin, abs(delta), lotKey, serialKey, nil, cost, "", adjustmentEventAttrs(in, declaredUnitCost, declaredValue)); err != nil {
			return adj, err
		}
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
			a.reason, a.actor_id, a.created_at, i.sku, i.name, b.code,
			a.reason_code, a.expense_account, a.unit_cost, a.value, a.evidence_notes, a.attachments
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
			&a.QtyBefore, &a.QtyAfter, &a.Reason, &a.ActorID, &a.CreatedAt, &a.ItemSKU, &a.ItemName, &a.BinCode,
			&a.ReasonCode, &a.ExpenseAccount, &a.UnitCost, &a.Value, &a.EvidenceNotes, &a.Attachments); err != nil {
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
