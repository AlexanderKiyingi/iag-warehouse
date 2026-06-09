package store

import (
	"context"
	"errors"

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
	BatchBusinessID    *string
	Notes              *string
	Lines              []IssueLineInput
	CreatedBy          *uuid.UUID
}

func (s *Store) CreateIssue(ctx context.Context, in CreateIssueInput) (models.Issue, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.Issue{}, err
	}
	defer tx.Rollback(ctx)

	var issue models.Issue
	err = tx.QueryRow(ctx, `
		INSERT INTO wh_issues (department, cost_center, production_order_ref, batch_business_id, notes, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, status, department, cost_center, production_order_ref, batch_business_id, notes, posted_at, created_by, created_at, updated_at`,
		in.Department, in.CostCenter, in.ProductionOrderRef, in.BatchBusinessID, in.Notes, in.CreatedBy,
	).Scan(&issue.ID, &issue.Status, &issue.Department, &issue.CostCenter, &issue.ProductionOrderRef, &issue.BatchBusinessID, &issue.Notes, &issue.PostedAt, &issue.CreatedBy, &issue.CreatedAt, &issue.UpdatedAt)
	if err != nil {
		return issue, err
	}

	for _, line := range in.Lines {
		bin, _, err := s.GetBinByCode(ctx, line.BinCode)
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
		il.BinCode = line.BinCode
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
			SELECT id, status, department, cost_center, production_order_ref, batch_business_id, notes, posted_at, created_by, created_at, updated_at
			FROM wh_issues WHERE status = $1 ORDER BY created_at DESC LIMIT $2`, status, limit)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT id, status, department, cost_center, production_order_ref, batch_business_id, notes, posted_at, created_by, created_at, updated_at
			FROM wh_issues ORDER BY created_at DESC LIMIT $1`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Issue
	for rows.Next() {
		var iss models.Issue
		if err := rows.Scan(&iss.ID, &iss.Status, &iss.Department, &iss.CostCenter, &iss.ProductionOrderRef, &iss.BatchBusinessID, &iss.Notes, &iss.PostedAt, &iss.CreatedBy, &iss.CreatedAt, &iss.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, iss)
	}
	return out, rows.Err()
}

func (s *Store) GetIssue(ctx context.Context, id uuid.UUID) (models.Issue, error) {
	var iss models.Issue
	err := s.pool.QueryRow(ctx, `
		SELECT id, status, department, cost_center, production_order_ref, batch_business_id, notes, posted_at, created_by, created_at, updated_at
		FROM wh_issues WHERE id = $1`, id,
	).Scan(&iss.ID, &iss.Status, &iss.Department, &iss.CostCenter, &iss.ProductionOrderRef, &iss.BatchBusinessID, &iss.Notes, &iss.PostedAt, &iss.CreatedBy, &iss.CreatedAt, &iss.UpdatedAt)
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
		s.emitInventoryMovement(ctx, movID, models.MovementIssue, l.itemID, l.sku, &l.binID, nil, l.qty, lotKey, serialKey, nil)
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
