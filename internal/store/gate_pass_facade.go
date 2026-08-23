package store

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"iag-warehouse/backend/internal/models"
)

// The flat gate-pass API, projected from wh_slips.
//
// Two tables used to model the same physical event. wh_gate_passes came from
// the stores increment: flat, TEXT dates, one free-text `items` column, no
// lines, no approval, no gate verification. wh_slips came from the execution
// layer: typed, numbered, approval-gated, with a verify token, per-line serials
// and conditions, and an immutable event log.
//
// Both were live and both were routed, so what left the site depended on which
// endpoint the caller used. Migration 028 folded the flat table into wh_slips
// and dropped it. These functions keep the old contract working — storesiag and
// anything else speaking it are unaffected — but there is now one register.
//
// New callers should use /slips. This shape cannot express a non-returnable
// pass, a cargo load, per-line serials, an approval, or a gate verification,
// and those are most of what §7.19 is about.

// gatePassProjection renders a slip in the flat shape. Status is mapped back to
// the three words the old contract used, because a client written against it
// will be comparing against those strings.
const gatePassProjection = `
	s.id,
	COALESCE(s.slip_no, ''),
	COALESCE((SELECT string_agg(l.description, '; ' ORDER BY l.description)
	          FROM wh_slip_lines l WHERE l.slip_id = s.id), ''),
	s.issued_to_name,
	s.dept,
	s.purpose,
	COALESCE(s.attrs->>'legacy_date_out', TO_CHAR(s.created_at, 'YYYY-MM-DD')),
	COALESCE(TO_CHAR(s.return_by, 'YYYY-MM-DD'), ''),
	COALESCE(TO_CHAR(s.returned_at, 'YYYY-MM-DD'), ''),
	CASE s.status
	  WHEN 'returned'  THEN 'Returned'
	  WHEN 'closed'    THEN 'Returned'
	  WHEN 'cancelled' THEN 'Cancelled'
	  WHEN 'rejected'  THEN 'Cancelled'
	  ELSE 'On Loan' END,
	s.authorized_name,
	s.created_at,
	s.updated_at`

// gatePassScope keeps the facade to the slips it can represent honestly.
//
// A cargo gate pass with priced lines, a weighbridge reading and a gate
// verification would flatten to one string and the status "On Loan". That is
// not a projection of the truth, it is a distortion of it, so those slips are
// visible through /slips only.
const gatePassScope = `s.slip_type = 'equipment_handover' AND s.returnable`

func scanGatePass(row pgx.Row) (models.GatePass, error) {
	var g models.GatePass
	err := row.Scan(&g.ID, &g.GatePassNo, &g.Items, &g.IssuedTo, &g.Dept, &g.Purpose,
		&g.DateOut, &g.ReturnBy, &g.ReturnDate, &g.Status, &g.AuthorizedBy, &g.CreatedAt, &g.UpdatedAt)
	return g, err
}

func (s *Store) ListGatePasses(ctx context.Context) ([]models.GatePass, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+gatePassProjection+`
		FROM wh_slips s WHERE `+gatePassScope+` ORDER BY s.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.GatePass{}
	for rows.Next() {
		g, err := scanGatePass(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) getGatePass(ctx context.Context, id uuid.UUID) (models.GatePass, error) {
	g, err := scanGatePass(s.pool.QueryRow(ctx, `SELECT `+gatePassProjection+`
		FROM wh_slips s WHERE s.id = $1 AND `+gatePassScope, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return models.GatePass{}, ErrNotFound
	}
	return g, err
}

// flatStatusToSlip maps the three words the old contract used onto slip states.
// An unrecognised value leaves the status alone rather than guessing.
func flatStatusToSlip(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "returned", "closed":
		return "returned"
	case "cancelled", "canceled", "void":
		return "cancelled"
	case "on loan", "":
		return "released"
	}
	return ""
}

// CreateGatePass writes a released, returnable equipment-handover slip.
//
// Released rather than draft: the flat contract has no approval step, so a
// caller using it has already decided the pass is live. Filing it as a draft
// would produce a pass the gate refuses and nobody thinks to authorise.
func (s *Store) CreateGatePass(ctx context.Context, g models.GatePass) (models.GatePass, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.GatePass{}, err
	}
	defer tx.Rollback(ctx)

	slipNo := strings.TrimSpace(g.GatePassNo)
	if slipNo == "" {
		if slipNo, err = nextDocumentNumber(ctx, tx, "gate_pass", "GP"); err != nil {
			return models.GatePass{}, err
		}
	}
	status := flatStatusToSlip(g.Status)
	if status == "" {
		status = "released"
	}

	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO wh_slips (slip_no, slip_type, status, purpose, issued_to_name, dept,
			returnable, return_by, returned_at, authorized_name, requested_name, ref_type, attrs)
		VALUES ($1, 'equipment_handover', $2, $3, $4, $5, TRUE,
			NULLIF($6, '')::date, NULLIF($7, '')::date::timestamptz, $8, $4, 'flat_gate_pass',
			jsonb_build_object('legacy_date_out', $9::text))
		RETURNING id`,
		slipNo, status, g.Purpose, g.IssuedTo, g.Dept,
		g.ReturnBy, g.ReturnDate, g.AuthorizedBy, g.DateOut,
	).Scan(&id)
	if err != nil {
		return models.GatePass{}, err
	}

	items := strings.TrimSpace(g.Items)
	if items == "" {
		items = "unspecified"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO wh_slip_lines (slip_id, description, qty, uom) VALUES ($1, $2, 1, 'ea')`,
		id, items); err != nil {
		return models.GatePass{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO wh_slip_events (slip_id, event, actor_name, notes)
		VALUES ($1, $2, $3, 'Raised through the flat gate-pass API')`,
		id, status, g.AuthorizedBy); err != nil {
		return models.GatePass{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return models.GatePass{}, err
	}
	return s.getGatePass(ctx, id)
}

// UpdateGatePass patches the projected fields. Empty means "leave alone", which
// is what the flat contract already did.
func (s *Store) UpdateGatePass(ctx context.Context, id uuid.UUID, g models.GatePass) (models.GatePass, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.GatePass{}, err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		UPDATE wh_slips s SET
			slip_no         = COALESCE(NULLIF($2, ''), s.slip_no),
			issued_to_name  = COALESCE(NULLIF($3, ''), s.issued_to_name),
			dept            = COALESCE(NULLIF($4, ''), s.dept),
			purpose         = COALESCE(NULLIF($5, ''), s.purpose),
			return_by       = COALESCE(NULLIF($6, '')::date, s.return_by),
			returned_at     = COALESCE(NULLIF($7, '')::date::timestamptz, s.returned_at),
			authorized_name = COALESCE(NULLIF($8, ''), s.authorized_name),
			status          = COALESCE(NULLIF($9, ''), s.status),
			attrs           = CASE WHEN NULLIF($10, '') IS NULL THEN s.attrs
			                       ELSE s.attrs || jsonb_build_object('legacy_date_out', $10::text) END,
			updated_at      = NOW()
		WHERE s.id = $1 AND `+gatePassScope,
		id, g.GatePassNo, g.IssuedTo, g.Dept, g.Purpose,
		g.ReturnBy, g.ReturnDate, g.AuthorizedBy, flatStatusToSlip(g.Status), g.DateOut)
	if err != nil {
		return models.GatePass{}, err
	}
	if tag.RowsAffected() == 0 {
		return models.GatePass{}, ErrNotFound
	}

	// The flat shape carries exactly one line, so replacing it is the only
	// faithful reading of an edit to `items`.
	if items := strings.TrimSpace(g.Items); items != "" {
		if _, err := tx.Exec(ctx, `DELETE FROM wh_slip_lines WHERE slip_id = $1`, id); err != nil {
			return models.GatePass{}, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO wh_slip_lines (slip_id, description, qty, uom) VALUES ($1, $2, 1, 'ea')`,
			id, items); err != nil {
			return models.GatePass{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return models.GatePass{}, err
	}
	return s.getGatePass(ctx, id)
}

// ReturnGatePass marks an outstanding pass as returned on the given date.
func (s *Store) ReturnGatePass(ctx context.Context, id uuid.UUID, returnDate string) (models.GatePass, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.GatePass{}, err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		UPDATE wh_slips s SET status = 'returned',
			returned_at = COALESCE(NULLIF($2, '')::date::timestamptz, NOW()),
			updated_at = NOW()
		WHERE s.id = $1 AND `+gatePassScope, id, returnDate)
	if err != nil {
		return models.GatePass{}, err
	}
	if tag.RowsAffected() == 0 {
		return models.GatePass{}, ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO wh_slip_events (slip_id, event, actor_name, notes)
		VALUES ($1, 'returned', 'system', 'Returned through the flat gate-pass API')`, id); err != nil {
		return models.GatePass{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return models.GatePass{}, err
	}
	return s.getGatePass(ctx, id)
}

// DeleteGatePass removes the slip; lines and events cascade.
func (s *Store) DeleteGatePass(ctx context.Context, id uuid.UUID) error {
	return s.deleteByID(ctx,
		`DELETE FROM wh_slips WHERE id=$1 AND slip_type='equipment_handover' AND returnable`, id)
}
