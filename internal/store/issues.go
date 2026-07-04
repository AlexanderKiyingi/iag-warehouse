package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"iag-warehouse/backend/internal/events"
	"iag-warehouse/backend/internal/models"
)

type IssueLineInput struct {
	ItemID    uuid.UUID
	Qty       float64
	UOM       string
	BinCode   string
	LotKey    string
	SerialKey string
}

type CreateIssueInput struct {
	Department         *string
	CostCenter         *string
	ProductionOrderRef *string
	WorkOrderRef       *string
	RequestedBy        *string
	Priority           *string
	BatchBusinessID    *string
	Notes              *string
	Lines              []IssueLineInput
	CreatedBy          *uuid.UUID
}

const issueReadCols = `id, status, department, cost_center, production_order_ref, work_order_ref,
	requested_by, priority, batch_business_id, notes, posted_at, created_by, created_at, updated_at`

func scanIssueRow(row pgx.Row) (models.Issue, error) {
	var iss models.Issue
	err := row.Scan(&iss.ID, &iss.Status, &iss.Department, &iss.CostCenter, &iss.ProductionOrderRef,
		&iss.WorkOrderRef, &iss.RequestedBy, &iss.Priority, &iss.BatchBusinessID, &iss.Notes,
		&iss.PostedAt, &iss.CreatedBy, &iss.CreatedAt, &iss.UpdatedAt)
	return iss, err
}

func (s *Store) CreateIssue(ctx context.Context, in CreateIssueInput) (models.Issue, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.Issue{}, err
	}
	defer tx.Rollback(ctx)

	issue, err := scanIssueRow(tx.QueryRow(ctx, `
		INSERT INTO wh_issues (department, cost_center, production_order_ref, work_order_ref, requested_by, priority, batch_business_id, notes, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING `+issueReadCols,
		in.Department, in.CostCenter, in.ProductionOrderRef, in.WorkOrderRef, in.RequestedBy, in.Priority, in.BatchBusinessID, in.Notes, in.CreatedBy,
	))
	if err != nil {
		return issue, err
	}

	for _, line := range in.Lines {
		// Callers that don't track warehouse bin topology (e.g. iag-fleet
		// issuing parts on a maintenance WO) leave bin_code empty; resolve a
		// concrete available bin here so delegation works without leaking
		// warehouse layout into the calling service.
		binCode := line.BinCode
		if binCode == "" {
			resolved, rerr := s.pickAvailableBinCode(ctx, line.ItemID, line.Qty, line.LotKey, line.SerialKey)
			if rerr != nil {
				return issue, fmt.Errorf("auto-select bin for item %s: %w", line.ItemID, rerr)
			}
			binCode = resolved
		}
		bin, _, err := s.GetBinByCode(ctx, binCode)
		if err != nil {
			return issue, err
		}
		lotKey, serialKey := normalizeKeys(line.LotKey, line.SerialKey)
		var il models.IssueLine
		err = tx.QueryRow(ctx, `
			INSERT INTO wh_issue_lines (issue_id, item_id, qty, uom, bin_id, lot_key, serial_key)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING id, issue_id, item_id, qty, uom, bin_id, lot_key, serial_key`,
			issue.ID, line.ItemID, line.Qty, line.UOM, bin.ID, lotKey, serialKey,
		).Scan(&il.ID, &il.IssueID, &il.ItemID, &il.Qty, &il.UOM, &il.BinID, &il.LotKey, &il.SerialKey)
		if err != nil {
			return issue, err
		}
		il.BinCode = binCode
		issue.Lines = append(issue.Lines, il)
	}

	if err := tx.Commit(ctx); err != nil {
		return issue, err
	}
	return issue, nil
}

func (s *Store) ListIssues(ctx context.Context, status string, limit int) ([]models.Issue, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var rows pgx.Rows
	var err error
	if status != "" {
		rows, err = s.pool.Query(ctx, `
			SELECT `+issueReadCols+`
			FROM wh_issues WHERE status = $1 ORDER BY created_at DESC LIMIT $2`, status, limit)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT `+issueReadCols+`
			FROM wh_issues ORDER BY created_at DESC LIMIT $1`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Issue{}
	for rows.Next() {
		iss, err := scanIssueRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, iss)
	}
	return out, rows.Err()
}

func (s *Store) GetIssue(ctx context.Context, id uuid.UUID) (models.Issue, error) {
	iss, err := scanIssueRow(s.pool.QueryRow(ctx, `
		SELECT `+issueReadCols+`
		FROM wh_issues WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return iss, ErrNotFound
	}
	if err != nil {
		return iss, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT il.id, il.issue_id, il.item_id, il.qty, il.uom, il.bin_id, il.lot_key, il.serial_key, b.code
		FROM wh_issue_lines il JOIN wh_bins b ON b.id = il.bin_id WHERE il.issue_id = $1`, id)
	if err != nil {
		return iss, err
	}
	defer rows.Close()
	for rows.Next() {
		var line models.IssueLine
		if err := rows.Scan(&line.ID, &line.IssueID, &line.ItemID, &line.Qty, &line.UOM, &line.BinID, &line.LotKey, &line.SerialKey, &line.BinCode); err != nil {
			return iss, err
		}
		iss.Lines = append(iss.Lines, line)
	}
	return iss, rows.Err()
}

func (s *Store) PostIssue(ctx context.Context, issueID uuid.UUID, actorID *uuid.UUID) (models.Issue, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.Issue{}, err
	}
	defer tx.Rollback(ctx)

	var status string
	var department *string
	err = tx.QueryRow(ctx, `SELECT status, department FROM wh_issues WHERE id = $1 FOR UPDATE`, issueID).Scan(&status, &department)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Issue{}, ErrNotFound
	}
	if err != nil {
		return models.Issue{}, err
	}
	if status == models.IssuePosted {
		return s.GetIssue(ctx, issueID)
	}
	if status != models.IssueDraft {
		return models.Issue{}, ErrConflict
	}

	rows, err := tx.Query(ctx, `
		SELECT il.item_id, il.qty, il.bin_id, il.lot_key, il.serial_key, i.sku
		FROM wh_issue_lines il JOIN wh_items i ON i.id = il.item_id WHERE il.issue_id = $1`, issueID)
	if err != nil {
		return models.Issue{}, err
	}
	type lineRow struct {
		itemID    uuid.UUID
		qty       float64
		binID     uuid.UUID
		lotKey    string
		serialKey string
		sku       string
	}
	var lines []lineRow
	for rows.Next() {
		var l lineRow
		if err := rows.Scan(&l.itemID, &l.qty, &l.binID, &l.lotKey, &l.serialKey, &l.sku); err != nil {
			rows.Close()
			return models.Issue{}, err
		}
		lines = append(lines, l)
	}
	rows.Close()

	refType := refType("issue")
	var eventLines []map[string]any
	for _, l := range lines {
		lotKey, serialKey := normalizeKeys(l.lotKey, l.serialKey)
		if err := s.deductAvailableBalanceTx(ctx, tx, balanceKey{l.itemID, l.binID, lotKey, serialKey}, l.qty); err != nil {
			return models.Issue{}, err
		}
		movID, err := s.insertMovementTx(ctx, tx, movementInput{
			MovementType: models.MovementIssue,
			ItemID:       &l.itemID,
			FromBinID:    &l.binID,
			Qty:          l.qty,
			LotKey:       lotKey,
			SerialKey:    serialKey,
			RefType:      refType,
			RefID:        &issueID,
			ActorID:      actorID,
		})
		if err != nil {
			return models.Issue{}, err
		}
		cost, err := s.outboundCostTx(ctx, tx, l.itemID, l.qty, issueID.String())
		if err != nil {
			return models.Issue{}, err
		}
		s.emitInventoryMovement(ctx, movID, models.MovementIssue, l.itemID, l.sku, &l.binID, nil, l.qty, lotKey, serialKey, nil, cost)
		eventLines = append(eventLines, map[string]any{
			"item_id": l.itemID.String(),
			"sku":     l.sku,
			"qty":     l.qty,
		})
	}

	_, err = tx.Exec(ctx, `UPDATE wh_issues SET status = 'posted', posted_at = NOW(), updated_at = NOW() WHERE id = $1`, issueID)
	if err != nil {
		return models.Issue{}, err
	}

	if s.bus != nil && s.bus.Enabled() {
		data := map[string]any{
			"issue_id":   issueID.String(),
			"department": department,
			"lines":      eventLines,
		}
		if err := s.bus.PublishTx(ctx, tx, events.TypeIssuePosted, data, issueID.String()); err != nil {
			return models.Issue{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return models.Issue{}, err
	}
	return s.GetIssue(ctx, issueID)
}
