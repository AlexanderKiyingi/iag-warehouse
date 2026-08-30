package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"iag-warehouse/backend/internal/events"
	"iag-warehouse/backend/internal/models"
)

type TransferLineInput struct {
	ItemID      uuid.UUID
	Qty         float64
	FromBinCode string
	ToBinCode   string
	LotKey      string
	SerialKey   string
}

type CreateTransferInput struct {
	FromFacilityCode *string
	ToFacilityCode   *string
	Notes            *string
	// ReceivedBy is who took delivery at the destination (migration 035).
	// Optional: internal transfers raised by replenishment or a handling-unit
	// move have no receiver to name.
	ReceivedBy string
	Lines      []TransferLineInput
	CreatedBy  *uuid.UUID
}

func (s *Store) CreateTransfer(ctx context.Context, in CreateTransferInput) (models.Transfer, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.Transfer{}, err
	}
	defer tx.Rollback(ctx)

	var fromFacID, toFacID *uuid.UUID
	if in.FromFacilityCode != nil {
		f, err := s.GetFacilityByCode(ctx, *in.FromFacilityCode)
		if err != nil {
			return models.Transfer{}, err
		}
		fromFacID = &f.ID
	}
	if in.ToFacilityCode != nil {
		f, err := s.GetFacilityByCode(ctx, *in.ToFacilityCode)
		if err != nil {
			return models.Transfer{}, err
		}
		toFacID = &f.ID
	}

	var tr models.Transfer
	err = tx.QueryRow(ctx, `
		INSERT INTO wh_transfers (from_facility_id, to_facility_id, notes, created_by, received_by)
		VALUES ($1, $2, $3, $4, NULLIF($5,''))
		RETURNING id, status, from_facility_id, to_facility_id, notes, posted_at, received_by, created_by, created_at, updated_at`,
		fromFacID, toFacID, in.Notes, in.CreatedBy, in.ReceivedBy,
	).Scan(&tr.ID, &tr.Status, &tr.FromFacilityID, &tr.ToFacilityID, &tr.Notes, &tr.PostedAt, &tr.ReceivedBy, &tr.CreatedBy, &tr.CreatedAt, &tr.UpdatedAt)
	if err != nil {
		return tr, err
	}

	for _, line := range in.Lines {
		fromBin, _, err := s.GetBinByCode(ctx, line.FromBinCode)
		if err != nil {
			return tr, err
		}
		toBin, _, err := s.GetBinByCode(ctx, line.ToBinCode)
		if err != nil {
			return tr, err
		}
		lotKey, serialKey := normalizeKeys(line.LotKey, line.SerialKey)
		var tl models.TransferLine
		err = tx.QueryRow(ctx, `
			INSERT INTO wh_transfer_lines (transfer_id, item_id, qty, from_bin_id, to_bin_id, lot_key, serial_key)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING id, transfer_id, item_id, qty, from_bin_id, to_bin_id, lot_key, serial_key`,
			tr.ID, line.ItemID, line.Qty, fromBin.ID, toBin.ID, lotKey, serialKey,
		).Scan(&tl.ID, &tl.TransferID, &tl.ItemID, &tl.Qty, &tl.FromBinID, &tl.ToBinID, &tl.LotKey, &tl.SerialKey)
		if err != nil {
			return tr, err
		}
		tr.Lines = append(tr.Lines, tl)
	}

	if err := tx.Commit(ctx); err != nil {
		return tr, err
	}
	return s.postTransfer(ctx, tr.ID, in.CreatedBy)
}

func (s *Store) postTransfer(ctx context.Context, transferID uuid.UUID, actorID *uuid.UUID) (models.Transfer, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.Transfer{}, err
	}
	defer tx.Rollback(ctx)

	var fromFacID, toFacID *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT from_facility_id, to_facility_id FROM wh_transfers WHERE id = $1 FOR UPDATE`, transferID).Scan(&fromFacID, &toFacID)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Transfer{}, ErrNotFound
	}
	if err != nil {
		return models.Transfer{}, err
	}

	rows, err := tx.Query(ctx, `
		SELECT tl.item_id, tl.qty, tl.from_bin_id, tl.to_bin_id, tl.lot_key, tl.serial_key, i.sku
		FROM wh_transfer_lines tl JOIN wh_items i ON i.id = tl.item_id WHERE tl.transfer_id = $1`, transferID)
	if err != nil {
		return models.Transfer{}, err
	}
	type lineRow struct {
		itemID, fromBin, toBin uuid.UUID
		qty                    float64
		lotKey, serialKey, sku string
	}
	var lines []lineRow
	for rows.Next() {
		var l lineRow
		if err := rows.Scan(&l.itemID, &l.qty, &l.fromBin, &l.toBin, &l.lotKey, &l.serialKey, &l.sku); err != nil {
			rows.Close()
			return models.Transfer{}, err
		}
		lines = append(lines, l)
	}
	rows.Close()

	refType := refType("transfer")
	for _, l := range lines {
		lotKey, serialKey := normalizeKeys(l.lotKey, l.serialKey)
		if err := s.deductAvailableBalanceTx(ctx, tx, balanceKey{l.itemID, l.fromBin, lotKey, serialKey}, l.qty); err != nil {
			return models.Transfer{}, err
		}
		if err := s.adjustBalanceTx(ctx, tx, balanceKey{l.itemID, l.toBin, lotKey, serialKey}, l.qty, models.StatusAvailable); err != nil {
			return models.Transfer{}, err
		}
		movID, err := s.insertMovementTx(ctx, tx, movementInput{
			MovementType: models.MovementTransfer,
			ItemID:       &l.itemID,
			FromBinID:    &l.fromBin,
			ToBinID:      &l.toBin,
			Qty:          l.qty,
			LotKey:       lotKey,
			SerialKey:    serialKey,
			RefType:      refType,
			RefID:        &transferID,
			ActorID:      actorID,
		})
		if err != nil {
			return models.Transfer{}, err
		}
		// A transfer relocates stock between bins — cost-neutral, no valuation.
		if err := s.emitInventoryMovement(ctx, tx, movID, models.MovementTransfer, l.itemID, l.sku, &l.fromBin, &l.toBin, l.qty, lotKey, serialKey, nil, movementCost{}, ""); err != nil {
			return models.Transfer{}, err
		}
	}

	_, err = tx.Exec(ctx, `UPDATE wh_transfers SET status = 'posted', posted_at = NOW(), updated_at = NOW() WHERE id = $1`, transferID)
	if err != nil {
		return models.Transfer{}, err
	}

	if s.bus != nil && s.bus.Enabled() {
		data := map[string]any{
			"transfer_id":   transferID.String(),
			"from_facility": fromFacID,
			"to_facility":   toFacID,
		}
		if err := s.bus.PublishTx(ctx, tx, events.TypeTransferCompleted, data, transferID.String()); err != nil {
			return models.Transfer{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return models.Transfer{}, err
	}
	return s.getTransfer(ctx, transferID)
}

func (s *Store) getTransfer(ctx context.Context, id uuid.UUID) (models.Transfer, error) {
	var tr models.Transfer
	err := s.pool.QueryRow(ctx, `
		SELECT t.id, t.status, t.from_facility_id, t.to_facility_id, t.notes, t.posted_at,
		       t.received_by, t.created_by, t.created_at, t.updated_at,
		       COALESCE(ff.code, ''), COALESCE(tf.code, '')
		FROM wh_transfers t
		LEFT JOIN wh_facilities ff ON ff.id = t.from_facility_id
		LEFT JOIN wh_facilities tf ON tf.id = t.to_facility_id
		WHERE t.id = $1`, id,
	).Scan(&tr.ID, &tr.Status, &tr.FromFacilityID, &tr.ToFacilityID, &tr.Notes, &tr.PostedAt,
		&tr.ReceivedBy, &tr.CreatedBy, &tr.CreatedAt, &tr.UpdatedAt,
		&tr.FromFacilityCode, &tr.ToFacilityCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return tr, ErrNotFound
	}
	return tr, err
}

func (s *Store) ListTransfers(ctx context.Context, status string, limit int) ([]models.Transfer, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	// The display joins are the point of this query.
	//
	// The list used to select the header alone, so a caller rendering a row got
	// two facility UUIDs, no item and no quantity — a transfer list that could
	// not say what had been transferred or where. The lateral picks the first
	// line and counts the rest; see the note on models.Transfer for why the
	// quantity is not summed across lines.
	const listQuery = `
		SELECT t.id, t.status, t.from_facility_id, t.to_facility_id, t.notes, t.posted_at,
		       t.received_by, t.created_by, t.created_at, t.updated_at,
		       COALESCE(ff.code, ''), COALESCE(tf.code, ''),
		       COALESCE(l.sku, ''), COALESCE(l.name, ''), COALESCE(l.qty, 0), COALESCE(l.line_count, 0)
		FROM wh_transfers t
		LEFT JOIN wh_facilities ff ON ff.id = t.from_facility_id
		LEFT JOIN wh_facilities tf ON tf.id = t.to_facility_id
		LEFT JOIN LATERAL (
			SELECT i.sku, i.name, tl.qty,
			       (SELECT count(*) FROM wh_transfer_lines WHERE transfer_id = t.id) AS line_count
			FROM wh_transfer_lines tl
			JOIN wh_items i ON i.id = tl.item_id
			WHERE tl.transfer_id = t.id
			ORDER BY tl.id
			LIMIT 1
		) l ON TRUE`

	var rows pgx.Rows
	var err error
	if status != "" {
		rows, err = s.pool.Query(ctx, listQuery+`
			WHERE t.status = $1 ORDER BY t.created_at DESC LIMIT $2`, status, limit)
	} else {
		rows, err = s.pool.Query(ctx, listQuery+`
			ORDER BY t.created_at DESC LIMIT $1`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Transfer{}
	for rows.Next() {
		var tr models.Transfer
		if err := rows.Scan(&tr.ID, &tr.Status, &tr.FromFacilityID, &tr.ToFacilityID, &tr.Notes, &tr.PostedAt,
			&tr.ReceivedBy, &tr.CreatedBy, &tr.CreatedAt, &tr.UpdatedAt,
			&tr.FromFacilityCode, &tr.ToFacilityCode,
			&tr.ItemSKU, &tr.ItemName, &tr.Qty, &tr.LineCount); err != nil {
			return nil, err
		}
		out = append(out, tr)
	}
	return out, rows.Err()
}

// GetTransfer returns a transfer with its lines (bin codes joined for display).
func (s *Store) GetTransfer(ctx context.Context, id uuid.UUID) (models.Transfer, error) {
	tr, err := s.getTransfer(ctx, id)
	if err != nil {
		return tr, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, transfer_id, item_id, qty, from_bin_id, to_bin_id, lot_key, serial_key
		FROM wh_transfer_lines WHERE transfer_id = $1`, id)
	if err != nil {
		return tr, err
	}
	defer rows.Close()
	for rows.Next() {
		var l models.TransferLine
		if err := rows.Scan(&l.ID, &l.TransferID, &l.ItemID, &l.Qty, &l.FromBinID, &l.ToBinID, &l.LotKey, &l.SerialKey); err != nil {
			return tr, err
		}
		tr.Lines = append(tr.Lines, l)
	}
	return tr, rows.Err()
}
