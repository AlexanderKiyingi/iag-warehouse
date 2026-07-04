package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"iag-warehouse/backend/internal/models"
)

// CRUD for the flat stores-domain record tables (migration 010). These carry no
// stock side-effects — they are workspace record keeping for the storesiag
// alerts, returns, gate-pass, warranty, and event views.
//
// Update semantics: every text column uses COALESCE(NULLIF($n, ''), col) so a
// PATCH that omits a field (or sends it empty) PRESERVES the stored value rather
// than blanking it. This is a partial-update contract — clients only need to send
// the fields they are changing. It notably protects columns set by side-channel
// actions (e.g. a gate pass's return_date stamped by ReturnGatePass) from being
// wiped when the record is later edited from a form that never carried that field.
// Trade-off: a text field cannot be cleared back to empty via update (acceptable
// for these record tables). Numeric columns are set directly.

// --- stock thresholds (alerts) ---------------------------------------------

const thresholdCols = `id, item, dept, current_qty, min_qty, reorder_qty, alert_method, status, created_at, updated_at`

func scanThreshold(row pgx.Row) (models.StockThreshold, error) {
	var t models.StockThreshold
	err := row.Scan(&t.ID, &t.Item, &t.Dept, &t.CurrentQty, &t.MinQty, &t.ReorderQty, &t.AlertMethod, &t.Status, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

func (s *Store) ListThresholds(ctx context.Context) ([]models.StockThreshold, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+thresholdCols+` FROM wh_stock_thresholds ORDER BY item`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.StockThreshold{}
	for rows.Next() {
		t, err := scanThreshold(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) CreateThreshold(ctx context.Context, t models.StockThreshold) (models.StockThreshold, error) {
	return scanThreshold(s.pool.QueryRow(ctx, `
		INSERT INTO wh_stock_thresholds (item, dept, current_qty, min_qty, reorder_qty, alert_method, status)
		VALUES ($1, $2, $3, $4, $5, COALESCE(NULLIF($6, ''), 'System'), COALESCE(NULLIF($7, ''), 'Active'))
		RETURNING `+thresholdCols, t.Item, t.Dept, t.CurrentQty, t.MinQty, t.ReorderQty, t.AlertMethod, t.Status))
}

func (s *Store) UpdateThreshold(ctx context.Context, id uuid.UUID, t models.StockThreshold) (models.StockThreshold, error) {
	out, err := scanThreshold(s.pool.QueryRow(ctx, `
		UPDATE wh_stock_thresholds SET item=COALESCE(NULLIF($2, ''), item), dept=COALESCE(NULLIF($3, ''), dept),
			current_qty=$4, min_qty=$5, reorder_qty=$6,
			alert_method=COALESCE(NULLIF($7, ''), alert_method), status=COALESCE(NULLIF($8, ''), status), updated_at=NOW()
		WHERE id=$1 RETURNING `+thresholdCols, id, t.Item, t.Dept, t.CurrentQty, t.MinQty, t.ReorderQty, t.AlertMethod, t.Status))
	if errors.Is(err, pgx.ErrNoRows) {
		return models.StockThreshold{}, ErrNotFound
	}
	return out, err
}

func (s *Store) DeleteThreshold(ctx context.Context, id uuid.UUID) error {
	return s.deleteByID(ctx, `DELETE FROM wh_stock_thresholds WHERE id=$1`, id)
}

// --- stock returns ----------------------------------------------------------

const returnCols = `id, item, sku, qty, returned_by, condition, linked_ref, action, status, notes, return_date, created_at, updated_at`

func scanReturn(row pgx.Row) (models.StockReturn, error) {
	var r models.StockReturn
	err := row.Scan(&r.ID, &r.Item, &r.SKU, &r.Qty, &r.ReturnedBy, &r.Condition, &r.LinkedRef, &r.Action, &r.Status, &r.Notes, &r.ReturnDate, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

func (s *Store) ListReturns(ctx context.Context) ([]models.StockReturn, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+returnCols+` FROM wh_returns ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.StockReturn{}
	for rows.Next() {
		r, err := scanReturn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) CreateReturn(ctx context.Context, r models.StockReturn) (models.StockReturn, error) {
	return scanReturn(s.pool.QueryRow(ctx, `
		INSERT INTO wh_returns (item, sku, qty, returned_by, condition, linked_ref, action, status, notes, return_date)
		VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE(NULLIF($8, ''), 'Pending'), $9, $10)
		RETURNING `+returnCols, r.Item, r.SKU, r.Qty, r.ReturnedBy, r.Condition, r.LinkedRef, r.Action, r.Status, r.Notes, r.ReturnDate))
}

func (s *Store) UpdateReturn(ctx context.Context, id uuid.UUID, r models.StockReturn) (models.StockReturn, error) {
	out, err := scanReturn(s.pool.QueryRow(ctx, `
		UPDATE wh_returns SET item=COALESCE(NULLIF($2, ''), item), sku=COALESCE(NULLIF($3, ''), sku), qty=$4,
			returned_by=COALESCE(NULLIF($5, ''), returned_by), condition=COALESCE(NULLIF($6, ''), condition),
			linked_ref=COALESCE(NULLIF($7, ''), linked_ref), action=COALESCE(NULLIF($8, ''), action),
			status=COALESCE(NULLIF($9, ''), status), notes=COALESCE(NULLIF($10, ''), notes),
			return_date=COALESCE(NULLIF($11, ''), return_date), updated_at=NOW()
		WHERE id=$1 RETURNING `+returnCols, id, r.Item, r.SKU, r.Qty, r.ReturnedBy, r.Condition, r.LinkedRef, r.Action, r.Status, r.Notes, r.ReturnDate))
	if errors.Is(err, pgx.ErrNoRows) {
		return models.StockReturn{}, ErrNotFound
	}
	return out, err
}

func (s *Store) DeleteReturn(ctx context.Context, id uuid.UUID) error {
	return s.deleteByID(ctx, `DELETE FROM wh_returns WHERE id=$1`, id)
}

// --- gate passes ------------------------------------------------------------

const gatePassCols = `id, gate_pass_no, items, issued_to, dept, purpose, date_out, return_by, return_date, status, authorized_by, created_at, updated_at`

func scanGatePass(row pgx.Row) (models.GatePass, error) {
	var g models.GatePass
	err := row.Scan(&g.ID, &g.GatePassNo, &g.Items, &g.IssuedTo, &g.Dept, &g.Purpose, &g.DateOut, &g.ReturnBy, &g.ReturnDate, &g.Status, &g.AuthorizedBy, &g.CreatedAt, &g.UpdatedAt)
	return g, err
}

func (s *Store) ListGatePasses(ctx context.Context) ([]models.GatePass, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+gatePassCols+` FROM wh_gate_passes ORDER BY created_at DESC`)
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

func (s *Store) CreateGatePass(ctx context.Context, g models.GatePass) (models.GatePass, error) {
	return scanGatePass(s.pool.QueryRow(ctx, `
		INSERT INTO wh_gate_passes (gate_pass_no, items, issued_to, dept, purpose, date_out, return_by, return_date, status, authorized_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, COALESCE(NULLIF($9, ''), 'On Loan'), $10)
		RETURNING `+gatePassCols, g.GatePassNo, g.Items, g.IssuedTo, g.Dept, g.Purpose, g.DateOut, g.ReturnBy, g.ReturnDate, g.Status, g.AuthorizedBy))
}

func (s *Store) UpdateGatePass(ctx context.Context, id uuid.UUID, g models.GatePass) (models.GatePass, error) {
	out, err := scanGatePass(s.pool.QueryRow(ctx, `
		UPDATE wh_gate_passes SET gate_pass_no=COALESCE(NULLIF($2, ''), gate_pass_no), items=COALESCE(NULLIF($3, ''), items),
			issued_to=COALESCE(NULLIF($4, ''), issued_to), dept=COALESCE(NULLIF($5, ''), dept), purpose=COALESCE(NULLIF($6, ''), purpose),
			date_out=COALESCE(NULLIF($7, ''), date_out), return_by=COALESCE(NULLIF($8, ''), return_by),
			return_date=COALESCE(NULLIF($9, ''), return_date), status=COALESCE(NULLIF($10, ''), status),
			authorized_by=COALESCE(NULLIF($11, ''), authorized_by), updated_at=NOW()
		WHERE id=$1 RETURNING `+gatePassCols, id, g.GatePassNo, g.Items, g.IssuedTo, g.Dept, g.Purpose, g.DateOut, g.ReturnBy, g.ReturnDate, g.Status, g.AuthorizedBy))
	if errors.Is(err, pgx.ErrNoRows) {
		return models.GatePass{}, ErrNotFound
	}
	return out, err
}

// ReturnGatePass marks an outstanding gate pass as returned on the given date.
func (s *Store) ReturnGatePass(ctx context.Context, id uuid.UUID, returnDate string) (models.GatePass, error) {
	out, err := scanGatePass(s.pool.QueryRow(ctx, `
		UPDATE wh_gate_passes SET status='Returned', return_date=$2, updated_at=NOW()
		WHERE id=$1 RETURNING `+gatePassCols, id, returnDate))
	if errors.Is(err, pgx.ErrNoRows) {
		return models.GatePass{}, ErrNotFound
	}
	return out, err
}

func (s *Store) DeleteGatePass(ctx context.Context, id uuid.UUID) error {
	return s.deleteByID(ctx, `DELETE FROM wh_gate_passes WHERE id=$1`, id)
}

// --- warranties -------------------------------------------------------------

const warrantyCols = `id, item, supplier, asset_ref, purchase_date, expiry_date, duration, covers, contact, status, created_at, updated_at`

func scanWarranty(row pgx.Row) (models.Warranty, error) {
	var w models.Warranty
	err := row.Scan(&w.ID, &w.Item, &w.Supplier, &w.AssetRef, &w.PurchaseDate, &w.ExpiryDate, &w.Duration, &w.Covers, &w.Contact, &w.Status, &w.CreatedAt, &w.UpdatedAt)
	return w, err
}

func (s *Store) ListWarranties(ctx context.Context) ([]models.Warranty, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+warrantyCols+` FROM wh_warranties ORDER BY expiry_date`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Warranty{}
	for rows.Next() {
		w, err := scanWarranty(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) CreateWarranty(ctx context.Context, w models.Warranty) (models.Warranty, error) {
	return scanWarranty(s.pool.QueryRow(ctx, `
		INSERT INTO wh_warranties (item, supplier, asset_ref, purchase_date, expiry_date, duration, covers, contact, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, COALESCE(NULLIF($9, ''), 'Active'))
		RETURNING `+warrantyCols, w.Item, w.Supplier, w.AssetRef, w.PurchaseDate, w.ExpiryDate, w.Duration, w.Covers, w.Contact, w.Status))
}

func (s *Store) UpdateWarranty(ctx context.Context, id uuid.UUID, w models.Warranty) (models.Warranty, error) {
	out, err := scanWarranty(s.pool.QueryRow(ctx, `
		UPDATE wh_warranties SET item=COALESCE(NULLIF($2, ''), item), supplier=COALESCE(NULLIF($3, ''), supplier),
			asset_ref=COALESCE(NULLIF($4, ''), asset_ref), purchase_date=COALESCE(NULLIF($5, ''), purchase_date),
			expiry_date=COALESCE(NULLIF($6, ''), expiry_date), duration=COALESCE(NULLIF($7, ''), duration),
			covers=COALESCE(NULLIF($8, ''), covers), contact=COALESCE(NULLIF($9, ''), contact),
			status=COALESCE(NULLIF($10, ''), status), updated_at=NOW()
		WHERE id=$1 RETURNING `+warrantyCols, id, w.Item, w.Supplier, w.AssetRef, w.PurchaseDate, w.ExpiryDate, w.Duration, w.Covers, w.Contact, w.Status))
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Warranty{}, ErrNotFound
	}
	return out, err
}

func (s *Store) DeleteWarranty(ctx context.Context, id uuid.UUID) error {
	return s.deleteByID(ctx, `DELETE FROM wh_warranties WHERE id=$1`, id)
}

// --- event requests ---------------------------------------------------------

const eventCols = `id, event_name, items, qty, dept, requested_by, needed_by, return_date, status, notes, created_at, updated_at`

func scanEvent(row pgx.Row) (models.EventRequest, error) {
	var e models.EventRequest
	err := row.Scan(&e.ID, &e.EventName, &e.Items, &e.Qty, &e.Dept, &e.RequestedBy, &e.NeededBy, &e.ReturnDate, &e.Status, &e.Notes, &e.CreatedAt, &e.UpdatedAt)
	return e, err
}

func (s *Store) ListEventRequests(ctx context.Context) ([]models.EventRequest, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+eventCols+` FROM wh_event_requests ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.EventRequest{}
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) CreateEventRequest(ctx context.Context, e models.EventRequest) (models.EventRequest, error) {
	return scanEvent(s.pool.QueryRow(ctx, `
		INSERT INTO wh_event_requests (event_name, items, qty, dept, requested_by, needed_by, return_date, status, notes)
		VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE(NULLIF($8, ''), 'Requested'), $9)
		RETURNING `+eventCols, e.EventName, e.Items, e.Qty, e.Dept, e.RequestedBy, e.NeededBy, e.ReturnDate, e.Status, e.Notes))
}

func (s *Store) UpdateEventRequest(ctx context.Context, id uuid.UUID, e models.EventRequest) (models.EventRequest, error) {
	out, err := scanEvent(s.pool.QueryRow(ctx, `
		UPDATE wh_event_requests SET event_name=COALESCE(NULLIF($2, ''), event_name), items=COALESCE(NULLIF($3, ''), items),
			qty=$4, dept=COALESCE(NULLIF($5, ''), dept), requested_by=COALESCE(NULLIF($6, ''), requested_by),
			needed_by=COALESCE(NULLIF($7, ''), needed_by), return_date=COALESCE(NULLIF($8, ''), return_date),
			status=COALESCE(NULLIF($9, ''), status), notes=COALESCE(NULLIF($10, ''), notes), updated_at=NOW()
		WHERE id=$1 RETURNING `+eventCols, id, e.EventName, e.Items, e.Qty, e.Dept, e.RequestedBy, e.NeededBy, e.ReturnDate, e.Status, e.Notes))
	if errors.Is(err, pgx.ErrNoRows) {
		return models.EventRequest{}, ErrNotFound
	}
	return out, err
}

func (s *Store) DeleteEventRequest(ctx context.Context, id uuid.UUID) error {
	return s.deleteByID(ctx, `DELETE FROM wh_event_requests WHERE id=$1`, id)
}

// --- small tools ------------------------------------------------------------

const smallToolCols = `id, tag_no, name, category, custodian, dept, issued, return_by, condition, status, notes, created_at, updated_at`

func scanSmallTool(row pgx.Row) (models.SmallTool, error) {
	var t models.SmallTool
	err := row.Scan(&t.ID, &t.TagNo, &t.Name, &t.Category, &t.Custodian, &t.Dept, &t.Issued, &t.ReturnBy, &t.Condition, &t.Status, &t.Notes, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

func (s *Store) ListSmallTools(ctx context.Context) ([]models.SmallTool, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+smallToolCols+` FROM wh_small_tools ORDER BY tag_no, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.SmallTool{}
	for rows.Next() {
		t, err := scanSmallTool(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) CreateSmallTool(ctx context.Context, t models.SmallTool) (models.SmallTool, error) {
	return scanSmallTool(s.pool.QueryRow(ctx, `
		INSERT INTO wh_small_tools (tag_no, name, category, custodian, dept, issued, return_by, condition, status, notes)
		VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE(NULLIF($8, ''), 'Good'), COALESCE(NULLIF($9, ''), 'In Store'), $10)
		RETURNING `+smallToolCols, t.TagNo, t.Name, t.Category, t.Custodian, t.Dept, t.Issued, t.ReturnBy, t.Condition, t.Status, t.Notes))
}

func (s *Store) UpdateSmallTool(ctx context.Context, id uuid.UUID, t models.SmallTool) (models.SmallTool, error) {
	out, err := scanSmallTool(s.pool.QueryRow(ctx, `
		UPDATE wh_small_tools SET tag_no=COALESCE(NULLIF($2, ''), tag_no), name=COALESCE(NULLIF($3, ''), name),
			category=COALESCE(NULLIF($4, ''), category), custodian=COALESCE(NULLIF($5, ''), custodian), dept=COALESCE(NULLIF($6, ''), dept),
			issued=COALESCE(NULLIF($7, ''), issued), return_by=COALESCE(NULLIF($8, ''), return_by),
			condition=COALESCE(NULLIF($9, ''), condition), status=COALESCE(NULLIF($10, ''), status),
			notes=COALESCE(NULLIF($11, ''), notes), updated_at=NOW()
		WHERE id=$1 RETURNING `+smallToolCols, id, t.TagNo, t.Name, t.Category, t.Custodian, t.Dept, t.Issued, t.ReturnBy, t.Condition, t.Status, t.Notes))
	if errors.Is(err, pgx.ErrNoRows) {
		return models.SmallTool{}, ErrNotFound
	}
	return out, err
}

func (s *Store) DeleteSmallTool(ctx context.Context, id uuid.UUID) error {
	return s.deleteByID(ctx, `DELETE FROM wh_small_tools WHERE id=$1`, id)
}

// --- shared -----------------------------------------------------------------

func (s *Store) deleteByID(ctx context.Context, query string, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, query, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
