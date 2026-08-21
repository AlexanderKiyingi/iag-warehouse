package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"iag-warehouse/backend/internal/events"
	"iag-warehouse/backend/internal/inventory"
	"iag-warehouse/backend/internal/models"
)

type ReceiptLineInput struct {
	ItemID          uuid.UUID
	Qty             float64
	UOM             string
	BinCode         string
	LotKey          string
	BatchBusinessID *string
	UnitCost        float64 // purchase cost per unit (from PO/GRN); 0 = unpriced
}

type CreateReceiptInput struct {
	ReceiptType string
	SourceRef   *string
	GRNID       *string
	POID        *string
	Supplier    *string
	ReceivedBy  *string
	Notes       *string
	// FacilityID narrows directed putaway to the site the goods actually arrived
	// at, so a delivery to the mill is never directed to a bin in the yard.
	FacilityID *uuid.UUID
	Lines      []ReceiptLineInput
	CreatedBy  *uuid.UUID
}

// receiptReadCols selects a receipt plus its computed monetary value (Σ of the
// priced lines) — value is not a stored column.
const receiptReadCols = `id, receipt_type, status, source_ref, grn_id, po_id, supplier, received_by, notes,
	COALESCE((SELECT SUM(rl.qty * rl.unit_cost) FROM wh_receipt_lines rl WHERE rl.receipt_id = wh_receipts.id), 0) AS value,
	posted_at, created_by, created_at, updated_at`

func scanReceiptRow(row pgx.Row) (models.Receipt, error) {
	var r models.Receipt
	err := row.Scan(&r.ID, &r.ReceiptType, &r.Status, &r.SourceRef, &r.GRNID, &r.POID,
		&r.Supplier, &r.ReceivedBy, &r.Notes, &r.Value, &r.PostedAt, &r.CreatedBy, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

func (s *Store) CreateReceipt(ctx context.Context, in CreateReceiptInput) (models.Receipt, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.Receipt{}, err
	}
	defer tx.Rollback(ctx)

	var receipt models.Receipt
	err = tx.QueryRow(ctx, `
		INSERT INTO wh_receipts (receipt_type, source_ref, grn_id, po_id, supplier, received_by, notes, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, receipt_type, status, source_ref, grn_id, po_id, supplier, received_by, notes, posted_at, created_by, created_at, updated_at`,
		in.ReceiptType, in.SourceRef, in.GRNID, in.POID, in.Supplier, in.ReceivedBy, in.Notes, in.CreatedBy,
	).Scan(&receipt.ID, &receipt.ReceiptType, &receipt.Status, &receipt.SourceRef, &receipt.GRNID, &receipt.POID, &receipt.Supplier, &receipt.ReceivedBy, &receipt.Notes, &receipt.PostedAt, &receipt.CreatedBy, &receipt.CreatedAt, &receipt.UpdatedAt)
	if err != nil {
		return receipt, err
	}

	for _, line := range in.Lines {
		// Interpret the unit first: a line for 5 "bag" is 300kg of stock, and both
		// the putaway search and the stored quantity have to be the 300.
		conv, err := s.convertToBase(ctx, tx, line.ItemID, line.Qty, line.UOM)
		if err != nil {
			return receipt, err
		}
		// The purchase price arrives per purchase unit. Costing works entirely in
		// base units, so rebase it here rather than leaving every downstream
		// valuation to remember that a "case" was priced as a case.
		unitCost := line.UnitCost
		if conv.Factor > 0 {
			unitCost = line.UnitCost / conv.Factor
		}
		lotKey, _ := normalizeKeys(line.LotKey, "")

		binCode := line.BinCode
		var ruleID *uuid.UUID
		if binCode == "" {
			// No bin named — this is where the system decides instead of recording
			// a decision somebody else already made.
			sug, perr := s.ResolvePutawayBin(ctx, PutawayRequest{
				ItemID:     line.ItemID,
				Qty:        conv.BaseQty,
				LotKey:     lotKey,
				FacilityID: in.FacilityID,
			})
			if perr != nil {
				return receipt, fmt.Errorf("putaway for item %s: %w", line.ItemID, perr)
			}
			binCode = sug.BinCode
			ruleID = sug.RuleID
		}
		bin, _, err := s.GetBinByCode(ctx, binCode)
		if err != nil {
			return receipt, err
		}

		var rl models.ReceiptLine
		err = tx.QueryRow(ctx, `
			INSERT INTO wh_receipt_lines (receipt_id, item_id, qty, uom, bin_id, lot_key, batch_business_id,
				unit_cost, entered_qty, entered_uom, uom_factor, putaway_rule_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			RETURNING id, receipt_id, item_id, qty, uom, bin_id, lot_key, batch_business_id,
				entered_qty, entered_uom, uom_factor, putaway_rule_id`,
			receipt.ID, line.ItemID, conv.BaseQty, conv.BaseUOM, bin.ID, lotKey, line.BatchBusinessID,
			unitCost, conv.EnteredQty, conv.EnteredUOM, conv.Factor, ruleID,
		).Scan(&rl.ID, &rl.ReceiptID, &rl.ItemID, &rl.Qty, &rl.UOM, &rl.BinID, &rl.LotKey, &rl.BatchBusinessID,
			&rl.EnteredQty, &rl.EnteredUOM, &rl.UOMFactor, &rl.PutawayRuleID)
		if err != nil {
			return receipt, err
		}
		rl.BinCode = binCode
		receipt.Lines = append(receipt.Lines, rl)
	}

	if err := tx.Commit(ctx); err != nil {
		return receipt, err
	}
	return receipt, nil
}

func (s *Store) ListReceipts(ctx context.Context, status string, limit int) ([]models.Receipt, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var rows pgx.Rows
	var err error
	if status != "" {
		rows, err = s.pool.Query(ctx, `
			SELECT `+receiptReadCols+`
			FROM wh_receipts WHERE status = $1 ORDER BY created_at DESC LIMIT $2`, status, limit)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT `+receiptReadCols+`
			FROM wh_receipts ORDER BY created_at DESC LIMIT $1`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReceipts(rows)
}

func (s *Store) GetReceipt(ctx context.Context, id uuid.UUID) (models.Receipt, error) {
	r, err := scanReceiptRow(s.pool.QueryRow(ctx, `
		SELECT `+receiptReadCols+`
		FROM wh_receipts WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT rl.id, rl.receipt_id, rl.item_id, rl.qty, rl.uom, rl.bin_id, rl.lot_key, rl.batch_business_id,
			rl.entered_qty, rl.entered_uom, rl.uom_factor, rl.putaway_rule_id, b.code
		FROM wh_receipt_lines rl
		JOIN wh_bins b ON b.id = rl.bin_id
		WHERE rl.receipt_id = $1`, id)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	for rows.Next() {
		var line models.ReceiptLine
		if err := rows.Scan(&line.ID, &line.ReceiptID, &line.ItemID, &line.Qty, &line.UOM, &line.BinID,
			&line.LotKey, &line.BatchBusinessID, &line.EnteredQty, &line.EnteredUOM, &line.UOMFactor,
			&line.PutawayRuleID, &line.BinCode); err != nil {
			return r, err
		}
		r.Lines = append(r.Lines, line)
	}
	return r, rows.Err()
}

func (s *Store) PostReceipt(ctx context.Context, receiptID uuid.UUID, actorID *uuid.UUID) (models.Receipt, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.Receipt{}, err
	}
	defer tx.Rollback(ctx)

	var status string
	var grnID *string
	err = tx.QueryRow(ctx, `SELECT status, grn_id FROM wh_receipts WHERE id = $1 FOR UPDATE`, receiptID).Scan(&status, &grnID)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Receipt{}, ErrNotFound
	}
	if err != nil {
		return models.Receipt{}, err
	}
	// Where these goods came from. A receipt raised against a procurement GRN is
	// already accounted for by procurement.grn.posted — finance accrues the
	// GR/IR from that event — so the movement this posting emits must say so and
	// let finance stand down. Both events booking the same delivery credited
	// GR/IR twice, and the vendor invoice only ever clears it once.
	sourceDocType := ""
	if grnID != nil && strings.TrimSpace(*grnID) != "" {
		sourceDocType = inventory.SourceDocProcurementGRN
	}
	if status == models.ReceiptPosted {
		return s.GetReceipt(ctx, receiptID)
	}
	if status != models.ReceiptDraft {
		return models.Receipt{}, ErrConflict
	}

	rows, err := tx.Query(ctx, `
		SELECT rl.item_id, rl.qty, rl.bin_id, rl.lot_key, rl.batch_business_id, i.sku, rl.unit_cost
		FROM wh_receipt_lines rl
		JOIN wh_items i ON i.id = rl.item_id
		WHERE rl.receipt_id = $1`, receiptID)
	if err != nil {
		return models.Receipt{}, err
	}
	type lineRow struct {
		itemID   uuid.UUID
		qty      float64
		binID    uuid.UUID
		lotKey   string
		batchID  *string
		sku      string
		unitCost float64
	}
	var lines []lineRow
	for rows.Next() {
		var l lineRow
		if err := rows.Scan(&l.itemID, &l.qty, &l.binID, &l.lotKey, &l.batchID, &l.sku, &l.unitCost); err != nil {
			rows.Close()
			return models.Receipt{}, err
		}
		lines = append(lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return models.Receipt{}, err
	}

	refType := refType("receipt")
	var eventLines []map[string]any
	for _, l := range lines {
		lotKey, serialKey := normalizeKeys(l.lotKey, "")
		// Recompute moving-average cost BEFORE applying this line's quantity, so
		// on-hand reflects the pre-receipt balance (no-op when costing is off or
		// the line is unpriced).
		cost, err := s.applyReceiptCostTx(ctx, tx, l.itemID, l.qty, l.unitCost, receiptID.String())
		if err != nil {
			return models.Receipt{}, err
		}
		if err := s.adjustBalanceTx(ctx, tx, balanceKey{l.itemID, l.binID, lotKey, serialKey}, l.qty, models.StatusAvailable); err != nil {
			return models.Receipt{}, err
		}
		movID, err := s.insertMovementTx(ctx, tx, movementInput{
			MovementType:    models.MovementReceipt,
			ItemID:          &l.itemID,
			ToBinID:         &l.binID,
			Qty:             l.qty,
			LotKey:          lotKey,
			SerialKey:       serialKey,
			RefType:         refType,
			RefID:           &receiptID,
			BatchBusinessID: l.batchID,
			ActorID:         actorID,
		})
		if err != nil {
			return models.Receipt{}, err
		}
		if err := s.emitInventoryMovement(ctx, tx, movID, models.MovementReceipt, l.itemID, l.sku, nil, &l.binID, l.qty, lotKey, serialKey, l.batchID, cost, sourceDocType); err != nil {
			return models.Receipt{}, err
		}
		eventLines = append(eventLines, map[string]any{
			"item_id": l.itemID.String(),
			"sku":     l.sku,
			"qty":     l.qty,
			"lot_key": lotKey,
		})
	}

	_, err = tx.Exec(ctx, `UPDATE wh_receipts SET status = 'posted', posted_at = NOW(), updated_at = NOW() WHERE id = $1`, receiptID)
	if err != nil {
		return models.Receipt{}, err
	}

	if s.bus != nil && s.bus.Enabled() {
		data := map[string]any{"receipt_id": receiptID.String(), "lines": eventLines}
		if err := s.bus.PublishTx(ctx, tx, events.TypeReceiptPosted, data, receiptID.String()); err != nil {
			return models.Receipt{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return models.Receipt{}, err
	}
	return s.GetReceipt(ctx, receiptID)
}

func (s *Store) CreateReceiptFromGRN(ctx context.Context, grnID, poID string, lines []ReceiptLineInput, createdBy *uuid.UUID) (models.Receipt, error) {
	return s.CreateReceipt(ctx, CreateReceiptInput{
		ReceiptType: "grn",
		SourceRef:   strPtr("procurement"),
		GRNID:       strPtr(grnID),
		POID:        strPtr(poID),
		Lines:       lines,
		CreatedBy:   createdBy,
	})
}

func (s *Store) LinkExternalRef(ctx context.Context, sourceService, sourceType, sourceID, targetType string, targetID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO wh_external_refs (source_service, source_type, source_id, target_type, target_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (source_service, source_type, source_id) DO NOTHING`,
		sourceService, sourceType, sourceID, targetType, targetID)
	return err
}

func scanReceipts(rows pgx.Rows) ([]models.Receipt, error) {
	out := []models.Receipt{}
	for rows.Next() {
		r, err := scanReceiptRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) CreateDraftReceiptFromGRNEvent(ctx context.Context, grnID string, poID string, lines []ReceiptLineInput) (models.Receipt, error) {
	var existing uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT target_id FROM wh_external_refs
		WHERE source_service = 'iag-procurement' AND source_type = 'grn' AND source_id = $1`,
		grnID).Scan(&existing)
	if err == nil {
		return s.GetReceipt(ctx, existing)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return models.Receipt{}, err
	}
	r, err := s.CreateReceipt(ctx, CreateReceiptInput{
		ReceiptType: "grn",
		SourceRef:   strPtr("procurement"),
		GRNID:       strPtr(grnID),
		POID:        strPtr(poID),
		Lines:       lines,
	})
	if err != nil {
		return r, err
	}
	_ = s.LinkExternalRef(ctx, "iag-procurement", "grn", grnID, "receipt", r.ID)
	return r, nil
}
