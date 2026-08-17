package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"iag-warehouse/backend/internal/events"
	"iag-warehouse/backend/internal/models"
)

// Controlled cycle counting.
//
// The shape of the workflow is: snapshot what the system believes, have somebody
// count it without being told the answer, review the differences, and let a
// second person authorise the ones that stand. Nothing touches a balance until
// that last step, and when it does it goes through the ordinary adjustment path
// so the movement ledger, the valuation and finance all see it the way they see
// every other stock change.

type CountTaskInput struct {
	ScopeType      string
	ScopeRef       string
	Blind          bool
	TolerancePct   float64
	ToleranceValue float64
	// DueOnly narrows an ABC-scoped count to items whose count interval has
	// actually elapsed, which is what makes a scheduled count a cycle count
	// rather than a full recount of the class every time it runs.
	DueOnly   bool
	Notes     *string
	CreatedBy *uuid.UUID
}

const countTaskCols = `id, code, status, scope_type, scope_ref, blind, tolerance_pct, tolerance_value,
	notes, created_by, submitted_by, approved_by, created_at, submitted_at, approved_at, updated_at`

func scanCountTask(row pgx.Row) (models.CountTask, error) {
	var t models.CountTask
	err := row.Scan(&t.ID, &t.Code, &t.Status, &t.ScopeType, &t.ScopeRef, &t.Blind, &t.TolerancePct,
		&t.ToleranceValue, &t.Notes, &t.CreatedBy, &t.SubmittedBy, &t.ApprovedBy,
		&t.CreatedAt, &t.SubmittedAt, &t.ApprovedAt, &t.UpdatedAt)
	return t, err
}

// CreateCountTask snapshots the scope into count lines. The snapshot is taken
// once, at creation: a count is a claim about a moment, and re-reading system
// quantities at approval time would quietly absorb every movement that happened
// while the counter was walking the aisle.
func (s *Store) CreateCountTask(ctx context.Context, in CountTaskInput) (models.CountTask, error) {
	scopeRef := strings.TrimSpace(in.ScopeRef)
	switch in.ScopeType {
	case models.CountScopeZone, models.CountScopeBin, models.CountScopeItem:
		if scopeRef == "" {
			return models.CountTask{}, fmt.Errorf("%w: scope_ref is required for a %s count", ErrInvalidArgument, in.ScopeType)
		}
	case models.CountScopeABC:
		scopeRef = strings.ToUpper(scopeRef)
		if scopeRef != "A" && scopeRef != "B" && scopeRef != "C" {
			return models.CountTask{}, fmt.Errorf("%w: an abc count needs scope_ref of A, B or C", ErrInvalidArgument)
		}
	default:
		return models.CountTask{}, fmt.Errorf("%w: unknown scope_type %q", ErrInvalidArgument, in.ScopeType)
	}
	if in.TolerancePct < 0 || in.ToleranceValue < 0 {
		return models.CountTask{}, fmt.Errorf("%w: tolerances cannot be negative", ErrInvalidArgument)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.CountTask{}, err
	}
	defer tx.Rollback(ctx)

	code, err := nextDocumentNumber(ctx, tx, "count", "CC")
	if err != nil {
		return models.CountTask{}, err
	}

	var task models.CountTask
	err = tx.QueryRow(ctx, `
		INSERT INTO wh_count_tasks (code, scope_type, scope_ref, blind, tolerance_pct, tolerance_value, notes, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING `+countTaskCols,
		code, in.ScopeType, scopeRef, in.Blind, in.TolerancePct, in.ToleranceValue, in.Notes, in.CreatedBy,
	).Scan(&task.ID, &task.Code, &task.Status, &task.ScopeType, &task.ScopeRef, &task.Blind, &task.TolerancePct,
		&task.ToleranceValue, &task.Notes, &task.CreatedBy, &task.SubmittedBy, &task.ApprovedBy,
		&task.CreatedAt, &task.SubmittedAt, &task.ApprovedAt, &task.UpdatedAt)
	if err != nil {
		return models.CountTask{}, err
	}

	n, err := s.snapshotCountLinesTx(ctx, tx, task.ID, in.ScopeType, scopeRef, in.DueOnly)
	if err != nil {
		return models.CountTask{}, err
	}
	if n == 0 {
		return models.CountTask{}, fmt.Errorf("%w: nothing to count in that scope", ErrInvalidArgument)
	}
	if err := tx.Commit(ctx); err != nil {
		return models.CountTask{}, err
	}
	return s.GetCountTask(ctx, task.ID)
}

// snapshotCountLinesTx materialises one line per stock position in scope.
//
// Only positions with stock are snapshotted. A count sheet listing every
// (item, bin) pair the warehouse has ever used would be mostly zeroes and would
// not get counted; stock that is somewhere it should not be is found by the
// counter and added with AddCountLine, which is the honest way round.
func (s *Store) snapshotCountLinesTx(ctx context.Context, tx pgx.Tx, taskID uuid.UUID, scopeType, scopeRef string, dueOnly bool) (int, error) {
	const insertPrefix = `
		INSERT INTO wh_count_lines (count_task_id, item_id, bin_id, lot_key, serial_key, system_qty)
		SELECT $1, sb.item_id, sb.bin_id, sb.lot_key, sb.serial_key, sb.qty
		FROM wh_stock_balances sb
		JOIN wh_bins b ON b.id = sb.bin_id
		JOIN wh_zones z ON z.id = b.zone_id
		JOIN wh_items i ON i.id = sb.item_id
		WHERE sb.qty > 0 `

	var tag pgconn.CommandTag
	var err error
	switch scopeType {
	case models.CountScopeZone:
		tag, err = tx.Exec(ctx, insertPrefix+`AND z.code = $2
			ON CONFLICT DO NOTHING`, taskID, scopeRef)
	case models.CountScopeBin:
		tag, err = tx.Exec(ctx, insertPrefix+`AND b.code = $2
			ON CONFLICT DO NOTHING`, taskID, scopeRef)
	case models.CountScopeItem:
		tag, err = tx.Exec(ctx, insertPrefix+`AND i.sku = $2
			ON CONFLICT DO NOTHING`, taskID, scopeRef)
	case models.CountScopeABC:
		// A due item is one never counted, or one whose interval has elapsed. The
		// interval falls back to the class default when the item has none of its
		// own, so classifying an item is enough to put it on a schedule.
		tag, err = tx.Exec(ctx, insertPrefix+`AND i.abc_class = $2
			AND (NOT $3::boolean OR i.last_counted_at IS NULL
			     OR i.last_counted_at < NOW() - (COALESCE(i.count_interval_days, CASE i.abc_class
					WHEN 'A' THEN 30 WHEN 'B' THEN 90 ELSE 180 END) || ' days')::interval)
			ON CONFLICT DO NOTHING`, taskID, scopeRef, dueOnly)
	default:
		return 0, fmt.Errorf("%w: unknown scope_type %q", ErrInvalidArgument, scopeType)
	}
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

const countLineCols = `l.id, l.count_task_id, l.item_id, l.bin_id, l.lot_key, l.serial_key, l.system_qty,
	l.counted_qty, l.variance_qty, l.variance_value, l.status, l.note, l.counted_by, l.counted_at,
	l.adjustment_id, i.sku, i.name, b.code`

// GetCountTask returns the task with its lines. System quantities are withheld
// from a blind task that is still being counted — that is the entire point of a
// blind count, and a UI that could read them from the API would defeat it
// without anyone noticing.
func (s *Store) GetCountTask(ctx context.Context, id uuid.UUID) (models.CountTask, error) {
	task, err := scanCountTask(s.pool.QueryRow(ctx, `SELECT `+countTaskCols+` FROM wh_count_tasks WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return task, ErrNotFound
	}
	if err != nil {
		return task, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT `+countLineCols+`
		FROM wh_count_lines l
		JOIN wh_items i ON i.id = l.item_id
		JOIN wh_bins b ON b.id = l.bin_id
		WHERE l.count_task_id = $1
		ORDER BY b.code, i.sku`, id)
	if err != nil {
		return task, err
	}
	defer rows.Close()

	hideSystem := task.Blind && task.Status == models.CountCounting
	for rows.Next() {
		var l models.CountLine
		var systemQty float64
		if err := rows.Scan(&l.ID, &l.CountTaskID, &l.ItemID, &l.BinID, &l.LotKey, &l.SerialKey, &systemQty,
			&l.CountedQty, &l.VarianceQty, &l.VarianceValue, &l.Status, &l.Note, &l.CountedBy, &l.CountedAt,
			&l.AdjustmentID, &l.ItemSKU, &l.ItemName, &l.BinCode); err != nil {
			return task, err
		}
		if !hideSystem {
			q := systemQty
			l.SystemQty = &q
		}
		task.LineCount++
		if l.CountedQty != nil {
			task.CountedCount++
		}
		if l.VarianceQty != 0 {
			task.VarianceLines++
			task.VarianceValue += l.VarianceValue
		}
		task.Lines = append(task.Lines, l)
	}
	return task, rows.Err()
}

func (s *Store) ListCountTasks(ctx context.Context, status string, limit int) ([]models.CountTask, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := `
		SELECT ` + countTaskCols + `,
			(SELECT COUNT(*) FROM wh_count_lines l WHERE l.count_task_id = wh_count_tasks.id),
			(SELECT COUNT(*) FROM wh_count_lines l WHERE l.count_task_id = wh_count_tasks.id AND l.counted_qty IS NOT NULL),
			(SELECT COUNT(*) FROM wh_count_lines l WHERE l.count_task_id = wh_count_tasks.id AND l.variance_qty <> 0),
			(SELECT COALESCE(SUM(l.variance_value), 0) FROM wh_count_lines l WHERE l.count_task_id = wh_count_tasks.id)
		FROM wh_count_tasks`
	args := []any{}
	if status != "" {
		args = append(args, status)
		query += ` WHERE status = $1`
	}
	args = append(args, limit)
	query += fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d`, len(args))

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.CountTask{}
	for rows.Next() {
		var t models.CountTask
		if err := rows.Scan(&t.ID, &t.Code, &t.Status, &t.ScopeType, &t.ScopeRef, &t.Blind, &t.TolerancePct,
			&t.ToleranceValue, &t.Notes, &t.CreatedBy, &t.SubmittedBy, &t.ApprovedBy,
			&t.CreatedAt, &t.SubmittedAt, &t.ApprovedAt, &t.UpdatedAt,
			&t.LineCount, &t.CountedCount, &t.VarianceLines, &t.VarianceValue); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RecordCount captures what the counter actually found. It is deliberately
// writable more than once while the task is open: a recount is a normal event,
// and forcing a correction to go through an administrator is how people learn to
// write the number they think you want.
func (s *Store) RecordCount(ctx context.Context, taskID, lineID uuid.UUID, countedQty float64, note *string, actorID *uuid.UUID) (models.CountLine, error) {
	if countedQty < 0 {
		return models.CountLine{}, fmt.Errorf("%w: counted_qty cannot be negative", ErrInvalidArgument)
	}
	var status string
	err := s.pool.QueryRow(ctx, `SELECT status FROM wh_count_tasks WHERE id = $1`, taskID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.CountLine{}, ErrNotFound
	}
	if err != nil {
		return models.CountLine{}, err
	}
	if status != models.CountCounting {
		return models.CountLine{}, ErrConflict
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE wh_count_lines
		SET counted_qty = $3, status = 'counted', note = COALESCE($4, note),
			counted_by = $5, counted_at = NOW()
		WHERE id = $2 AND count_task_id = $1`,
		taskID, lineID, countedQty, note, actorID)
	if err != nil {
		return models.CountLine{}, err
	}
	if tag.RowsAffected() == 0 {
		return models.CountLine{}, ErrNotFound
	}
	return s.GetCountLine(ctx, lineID)
}

// AddCountLine records stock the counter found that the system did not know was
// there. Its system quantity is whatever the balance says right now, which for
// genuinely unknown stock is zero, so the whole counted quantity becomes the
// variance.
func (s *Store) AddCountLine(ctx context.Context, taskID, itemID uuid.UUID, binCode, lotKey, serialKey string) (models.CountLine, error) {
	var status string
	if err := s.pool.QueryRow(ctx, `SELECT status FROM wh_count_tasks WHERE id = $1`, taskID).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.CountLine{}, ErrNotFound
		}
		return models.CountLine{}, err
	}
	if status != models.CountCounting {
		return models.CountLine{}, ErrConflict
	}
	bin, _, err := s.GetBinByCode(ctx, binCode)
	if err != nil {
		return models.CountLine{}, err
	}
	lot, serial := normalizeKeys(lotKey, serialKey)

	var systemQty float64
	err = s.pool.QueryRow(ctx, `
		SELECT COALESCE(qty, 0) FROM wh_stock_balances
		WHERE item_id = $1 AND bin_id = $2 AND lot_key = $3 AND serial_key = $4`,
		itemID, bin.ID, lot, serial).Scan(&systemQty)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return models.CountLine{}, err
	}

	var id uuid.UUID
	err = s.pool.QueryRow(ctx, `
		INSERT INTO wh_count_lines (count_task_id, item_id, bin_id, lot_key, serial_key, system_qty)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (count_task_id, item_id, bin_id, lot_key, serial_key) DO UPDATE SET system_qty = wh_count_lines.system_qty
		RETURNING id`, taskID, itemID, bin.ID, lot, serial, systemQty).Scan(&id)
	if err != nil {
		return models.CountLine{}, err
	}
	return s.GetCountLine(ctx, id)
}

// GetCountLine returns one line, withholding the system quantity while a blind
// count is still being counted — the same rule GetCountTask applies. Every
// single-line response goes through here for that reason: a blind count that
// leaks the expected figure from one endpoint is not a blind count.
func (s *Store) GetCountLine(ctx context.Context, lineID uuid.UUID) (models.CountLine, error) {
	var blind bool
	var status string
	err := s.pool.QueryRow(ctx, `
		SELECT t.blind, t.status FROM wh_count_lines l
		JOIN wh_count_tasks t ON t.id = l.count_task_id WHERE l.id = $1`, lineID).Scan(&blind, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.CountLine{}, ErrNotFound
	}
	if err != nil {
		return models.CountLine{}, err
	}
	return s.getCountLine(ctx, lineID, blind && status == models.CountCounting)
}

func (s *Store) getCountLine(ctx context.Context, lineID uuid.UUID, hideSystem bool) (models.CountLine, error) {
	var l models.CountLine
	var systemQty float64
	err := s.pool.QueryRow(ctx, `
		SELECT `+countLineCols+`
		FROM wh_count_lines l
		JOIN wh_items i ON i.id = l.item_id
		JOIN wh_bins b ON b.id = l.bin_id
		WHERE l.id = $1`, lineID,
	).Scan(&l.ID, &l.CountTaskID, &l.ItemID, &l.BinID, &l.LotKey, &l.SerialKey, &systemQty,
		&l.CountedQty, &l.VarianceQty, &l.VarianceValue, &l.Status, &l.Note, &l.CountedBy, &l.CountedAt,
		&l.AdjustmentID, &l.ItemSKU, &l.ItemName, &l.BinCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return l, ErrNotFound
	}
	if err != nil {
		return l, err
	}
	if !hideSystem {
		l.SystemQty = &systemQty
	}
	return l, nil
}

// SubmitCountTask closes counting and works out what needs a human. Variance is
// valued at the item's moving-average cost, because the question an approver is
// really being asked is "is this worth arguing about", and that is a money
// question rather than a quantity one.
//
// Lines inside tolerance are accepted here; lines outside it stay 'counted' and
// must be explicitly accepted or sent for recount before the task can be
// approved. A line nobody counted at all is left pending and blocks approval —
// an uncounted position is not the same as a position that matched.
func (s *Store) SubmitCountTask(ctx context.Context, taskID uuid.UUID, actorID *uuid.UUID) (models.CountTask, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.CountTask{}, err
	}
	defer tx.Rollback(ctx)

	var status string
	var tolPct, tolValue float64
	err = tx.QueryRow(ctx,
		`SELECT status, tolerance_pct, tolerance_value FROM wh_count_tasks WHERE id = $1 FOR UPDATE`, taskID,
	).Scan(&status, &tolPct, &tolValue)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.CountTask{}, ErrNotFound
	}
	if err != nil {
		return models.CountTask{}, err
	}
	if status == models.CountReview {
		return s.GetCountTask(ctx, taskID)
	}
	if status != models.CountCounting {
		return models.CountTask{}, ErrConflict
	}

	rows, err := tx.Query(ctx, `
		SELECT l.id, l.system_qty, l.counted_qty, COALESCE(i.avg_cost, 0)
		FROM wh_count_lines l JOIN wh_items i ON i.id = l.item_id
		WHERE l.count_task_id = $1`, taskID)
	if err != nil {
		return models.CountTask{}, err
	}
	type lineRow struct {
		id         uuid.UUID
		systemQty  float64
		countedQty *float64
		avgCost    float64
	}
	var lines []lineRow
	for rows.Next() {
		var l lineRow
		if err := rows.Scan(&l.id, &l.systemQty, &l.countedQty, &l.avgCost); err != nil {
			rows.Close()
			return models.CountTask{}, err
		}
		lines = append(lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return models.CountTask{}, err
	}

	for _, l := range lines {
		if l.countedQty == nil {
			continue // left pending; blocks approval until counted or removed
		}
		variance := *l.countedQty - l.systemQty
		varianceValue := variance * l.avgCost
		lineStatus := models.CountLineCounted
		if variance == 0 || withinTolerance(variance, varianceValue, l.systemQty, tolPct, tolValue) {
			lineStatus = models.CountLineAccepted
		}
		if _, err := tx.Exec(ctx, `
			UPDATE wh_count_lines SET variance_qty = $2, variance_value = $3, status = $4 WHERE id = $1`,
			l.id, variance, varianceValue, lineStatus); err != nil {
			return models.CountTask{}, err
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE wh_count_tasks SET status = 'review', submitted_by = $2, submitted_at = NOW(), updated_at = NOW()
		WHERE id = $1`, taskID, actorID); err != nil {
		return models.CountTask{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return models.CountTask{}, err
	}
	return s.GetCountTask(ctx, taskID)
}

// withinTolerance is true when a variance is small enough to wave through.
// Either bound alone is sufficient: a percentage catches "a few grains out of a
// tonne", a cash value catches "a rounding error on something cheap", and an
// operator who sets only one should not have to set the other.
func withinTolerance(variance, varianceValue, systemQty, tolPct, tolValue float64) bool {
	if tolPct > 0 && systemQty > 0 && math.Abs(variance)/systemQty*100 <= tolPct {
		return true
	}
	if tolValue > 0 && math.Abs(varianceValue) <= tolValue {
		return true
	}
	return false
}

// SetCountLineStatus is the reviewer's verdict on one line: accept it (the count
// stands and the balance will move), reject it (the system stands and the count
// is discarded), or send it back for recount.
func (s *Store) SetCountLineStatus(ctx context.Context, taskID, lineID uuid.UUID, status string, note *string) (models.CountLine, error) {
	switch status {
	case models.CountLineAccepted, models.CountLineRejected, models.CountLineRecount:
	default:
		return models.CountLine{}, fmt.Errorf("%w: status must be accepted, rejected or recount", ErrInvalidArgument)
	}
	var taskStatus string
	if err := s.pool.QueryRow(ctx, `SELECT status FROM wh_count_tasks WHERE id = $1`, taskID).Scan(&taskStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.CountLine{}, ErrNotFound
		}
		return models.CountLine{}, err
	}
	if taskStatus != models.CountReview {
		return models.CountLine{}, ErrConflict
	}

	// A recount clears the count so the position genuinely has to be visited
	// again — leaving the old figure in place invites confirming it from a chair.
	query := `UPDATE wh_count_lines SET status = $3, note = COALESCE($4, note) WHERE id = $2 AND count_task_id = $1`
	if status == models.CountLineRecount {
		query = `UPDATE wh_count_lines SET status = $3, note = COALESCE($4, note),
			counted_qty = NULL, variance_qty = 0, variance_value = 0, counted_by = NULL, counted_at = NULL
			WHERE id = $2 AND count_task_id = $1`
	}
	tag, err := s.pool.Exec(ctx, query, taskID, lineID, status, note)
	if err != nil {
		return models.CountLine{}, err
	}
	if tag.RowsAffected() == 0 {
		return models.CountLine{}, ErrNotFound
	}
	return s.GetCountLine(ctx, lineID)
}

// ReopenCountTask sends a task in review back to counting, which is what has to
// happen once any line has been marked for recount.
func (s *Store) ReopenCountTask(ctx context.Context, taskID uuid.UUID) (models.CountTask, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE wh_count_tasks SET status = 'counting', submitted_by = NULL, submitted_at = NULL, updated_at = NOW()
		WHERE id = $1 AND status = 'review'`, taskID)
	if err != nil {
		return models.CountTask{}, err
	}
	if tag.RowsAffected() == 0 {
		return models.CountTask{}, ErrConflict
	}
	return s.GetCountTask(ctx, taskID)
}

// ApproveCountTask posts every accepted variance and stamps the items as
// counted. requireSeparateApprover enforces segregation of duties: whoever
// counted or submitted the sheet cannot also be the one who signs it off, which
// is the control that makes a count worth anything.
//
// All variances post in one transaction. A count that half-applied would leave
// the warehouse in a state no report could explain.
func (s *Store) ApproveCountTask(ctx context.Context, taskID uuid.UUID, actorID *uuid.UUID, requireSeparateApprover bool) (models.CountTask, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.CountTask{}, err
	}
	defer tx.Rollback(ctx)

	var status, code string
	var submittedBy, createdBy *uuid.UUID
	err = tx.QueryRow(ctx,
		`SELECT status, code, submitted_by, created_by FROM wh_count_tasks WHERE id = $1 FOR UPDATE`, taskID,
	).Scan(&status, &code, &submittedBy, &createdBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.CountTask{}, ErrNotFound
	}
	if err != nil {
		return models.CountTask{}, err
	}
	if status == models.CountApproved {
		return s.GetCountTask(ctx, taskID)
	}
	if status != models.CountReview {
		return models.CountTask{}, ErrConflict
	}

	if requireSeparateApprover && actorID != nil {
		if (submittedBy != nil && *submittedBy == *actorID) || (createdBy != nil && *createdBy == *actorID) {
			return models.CountTask{}, fmt.Errorf("%w: a count must be approved by someone other than the person who raised or submitted it", ErrForbidden)
		}
		var ownCounts int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM wh_count_lines WHERE count_task_id = $1 AND counted_by = $2`, taskID, *actorID,
		).Scan(&ownCounts); err != nil {
			return models.CountTask{}, err
		}
		if ownCounts > 0 {
			return models.CountTask{}, fmt.Errorf("%w: a count must be approved by someone who did not count it", ErrForbidden)
		}
	}

	var unresolved int
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM wh_count_lines WHERE count_task_id = $1 AND status NOT IN ('accepted', 'rejected')`, taskID,
	).Scan(&unresolved); err != nil {
		return models.CountTask{}, err
	}
	if unresolved > 0 {
		return models.CountTask{}, fmt.Errorf("%w: %d line(s) are still uncounted or unresolved", ErrConflict, unresolved)
	}

	rows, err := tx.Query(ctx, `
		SELECT l.id, l.item_id, l.bin_id, l.lot_key, l.serial_key, l.counted_qty, l.variance_qty
		FROM wh_count_lines l
		WHERE l.count_task_id = $1 AND l.status = 'accepted' AND l.variance_qty <> 0 AND l.counted_qty IS NOT NULL`, taskID)
	if err != nil {
		return models.CountTask{}, err
	}
	type postRow struct {
		lineID, itemID, binID uuid.UUID
		lotKey, serialKey     string
		countedQty            float64
		varianceQty           float64
	}
	var toPost []postRow
	for rows.Next() {
		var p postRow
		var counted *float64
		if err := rows.Scan(&p.lineID, &p.itemID, &p.binID, &p.lotKey, &p.serialKey, &counted, &p.varianceQty); err != nil {
			rows.Close()
			return models.CountTask{}, err
		}
		p.countedQty = *counted
		toPost = append(toPost, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return models.CountTask{}, err
	}

	reason := "cycle count " + code
	var totalValue float64
	for _, p := range toPost {
		adj, err := s.applyStockChangeTx(ctx, tx, AdjustmentInput{
			ItemID:    p.itemID,
			LotKey:    p.lotKey,
			SerialKey: p.serialKey,
			QtyAfter:  p.countedQty,
			Reason:    &reason,
			AdjType:   "cycle_count",
			ActorID:   actorID,
		}, p.binID)
		if err != nil {
			return models.CountTask{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE wh_count_lines SET adjustment_id = $2 WHERE id = $1`, p.lineID, adj.ID); err != nil {
			return models.CountTask{}, err
		}
	}
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(variance_value), 0) FROM wh_count_lines WHERE count_task_id = $1 AND status = 'accepted'`, taskID,
	).Scan(&totalValue); err != nil {
		return models.CountTask{}, err
	}

	// Every item on the sheet is now counted, including the ones that matched —
	// a position that agreed with the system was still counted, and the schedule
	// must not send someone back to it tomorrow.
	if _, err := tx.Exec(ctx, `
		UPDATE wh_items SET last_counted_at = NOW(), updated_at = NOW()
		WHERE id IN (SELECT DISTINCT item_id FROM wh_count_lines WHERE count_task_id = $1)`, taskID); err != nil {
		return models.CountTask{}, err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE wh_count_tasks SET status = 'approved', approved_by = $2, approved_at = NOW(), updated_at = NOW()
		WHERE id = $1`, taskID, actorID); err != nil {
		return models.CountTask{}, err
	}

	if s.bus != nil && s.bus.Enabled() {
		data := map[string]any{
			"count_task_id":  taskID.String(),
			"code":           code,
			"posted_lines":   len(toPost),
			"variance_value": totalValue,
		}
		if err := s.bus.PublishTx(ctx, tx, events.TypeCountApproved, data, taskID.String()); err != nil {
			return models.CountTask{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return models.CountTask{}, err
	}
	return s.GetCountTask(ctx, taskID)
}

func (s *Store) CancelCountTask(ctx context.Context, taskID uuid.UUID) (models.CountTask, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE wh_count_tasks SET status = 'cancelled', updated_at = NOW()
		WHERE id = $1 AND status IN ('counting', 'review')`, taskID)
	if err != nil {
		return models.CountTask{}, err
	}
	if tag.RowsAffected() == 0 {
		return models.CountTask{}, ErrConflict
	}
	return s.GetCountTask(ctx, taskID)
}

// ABCInput parameterises a classification run. Days is the trailing window of
// movement history to rank on; APct/BPct are the cumulative-value cut points.
type ABCInput struct {
	Days      int
	APct      float64
	BPct      float64
	Intervals map[string]int
}

// RunABCClassification ranks items by outbound value over a trailing window and
// assigns A/B/C by cumulative share, then sets each class's counting interval.
//
// The ranking basis is consumption value, which is what ABC has always meant:
// the small number of items that account for most of what leaves the warehouse
// are the ones worth counting often. When costing is switched off every movement
// values at zero, so the run falls back to ranking on quantity and says so
// rather than silently classifying the entire catalogue as C.
func (s *Store) RunABCClassification(ctx context.Context, in ABCInput) ([]models.ABCResult, string, error) {
	if in.Days <= 0 {
		in.Days = 365
	}
	if in.APct <= 0 || in.APct >= 100 {
		in.APct = 80
	}
	if in.BPct <= in.APct || in.BPct >= 100 {
		in.BPct = 95
	}

	basis := "consumption_value"
	results, total, err := s.abcRanking(ctx, in.Days, true)
	if err != nil {
		return nil, "", err
	}
	if total <= 0 {
		basis = "consumption_quantity"
		results, total, err = s.abcRanking(ctx, in.Days, false)
		if err != nil {
			return nil, "", err
		}
	}
	if total <= 0 {
		return []models.ABCResult{}, basis, nil
	}

	intervals := map[string]int{"A": 30, "B": 90, "C": 180}
	for k, v := range in.Intervals {
		if v > 0 {
			intervals[strings.ToUpper(k)] = v
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback(ctx)

	var cumulative float64
	for i := range results {
		cumulative += results[i].Value
		pct := cumulative / total * 100
		results[i].CumulativePct = pct
		switch {
		case results[i].Value <= 0:
			results[i].Class = "C"
		case pct <= in.APct:
			results[i].Class = "A"
		case pct <= in.BPct:
			results[i].Class = "B"
		default:
			results[i].Class = "C"
		}
		if _, err := tx.Exec(ctx, `
			UPDATE wh_items SET abc_class = $2, count_interval_days = $3, updated_at = NOW() WHERE id = $1`,
			results[i].ItemID, results[i].Class, intervals[results[i].Class]); err != nil {
			return nil, "", err
		}
	}

	// Items that nothing moved in the window are not "unclassified forever" — they
	// are the definition of a C item, and leaving them null would keep them off
	// every count schedule.
	if _, err := tx.Exec(ctx, `
		UPDATE wh_items SET abc_class = 'C', count_interval_days = $1, updated_at = NOW()
		WHERE abc_class IS NULL`, intervals["C"]); err != nil {
		return nil, "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, "", err
	}
	return results, basis, nil
}

func (s *Store) abcRanking(ctx context.Context, days int, byValue bool) ([]models.ABCResult, float64, error) {
	measure := "SUM(ABS(m.total_cost))"
	if !byValue {
		measure = "SUM(ABS(m.qty))"
	}
	rows, err := s.pool.Query(ctx, `
		SELECT m.item_id, i.sku, i.name, `+measure+` AS value, i.abc_class
		FROM wh_movements m
		JOIN wh_items i ON i.id = m.item_id
		WHERE m.occurred_at >= NOW() - ($1 || ' days')::interval
		  AND m.movement_type IN ('issue', 'production_consume', 'pick')
		GROUP BY m.item_id, i.sku, i.name, i.abc_class
		ORDER BY value DESC`, days)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []models.ABCResult{}
	var total float64
	for rows.Next() {
		var r models.ABCResult
		if err := rows.Scan(&r.ItemID, &r.SKU, &r.Name, &r.Value, &r.Previous); err != nil {
			return nil, 0, err
		}
		total += r.Value
		out = append(out, r)
	}
	return out, total, rows.Err()
}
