package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"iag-warehouse/backend/internal/models"
)

// Replenishment.
//
// A level is a min/max on one bin — normally a pick face. When free stock there
// drops below min, a task is raised to top it back up to max from bulk. The task
// reserves its source stock immediately, so replenishment that has been planned
// but not yet walked cannot be picked out from under itself; completing consumes
// that reservation and posts an ordinary transfer movement.

type ReplenLevelInput struct {
	ItemID       uuid.UUID
	BinID        uuid.UUID
	MinQty       float64
	MaxQty       float64
	SourceZoneID *uuid.UUID
	Active       bool
	CreatedBy    *uuid.UUID
}

// replenLevelCols carries the level plus what is actually in the bin right now,
// because a level is meaningless to look at without it.
const replenLevelCols = `l.id, l.item_id, l.bin_id, l.min_qty, l.max_qty, l.source_zone_id, l.active,
	l.created_by, l.created_at, l.updated_at, i.sku, i.name, b.code,
	COALESCE(bal.on_hand, 0), COALESCE(bal.available, 0)`

const replenLevelFrom = `
	FROM wh_replen_levels l
	JOIN wh_items i ON i.id = l.item_id
	JOIN wh_bins b ON b.id = l.bin_id
	LEFT JOIN LATERAL (
		SELECT COALESCE(SUM(sb.qty), 0) AS on_hand,
		       COALESCE(SUM(sb.qty - sb.reserved), 0) AS available
		FROM wh_stock_balances sb
		WHERE sb.item_id = l.item_id AND sb.bin_id = l.bin_id AND sb.status = 'available'
	) bal ON TRUE`

func scanReplenLevel(row pgx.Row) (models.ReplenLevel, error) {
	var l models.ReplenLevel
	err := row.Scan(&l.ID, &l.ItemID, &l.BinID, &l.MinQty, &l.MaxQty, &l.SourceZoneID, &l.Active,
		&l.CreatedBy, &l.CreatedAt, &l.UpdatedAt, &l.ItemSKU, &l.ItemName, &l.BinCode, &l.OnHand, &l.Available)
	return l, err
}

func (s *Store) ListReplenLevels(ctx context.Context) ([]models.ReplenLevel, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+replenLevelCols+replenLevelFrom+` ORDER BY i.sku, b.code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.ReplenLevel{}
	for rows.Next() {
		l, err := scanReplenLevel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) UpsertReplenLevel(ctx context.Context, in ReplenLevelInput) (models.ReplenLevel, error) {
	if in.MaxQty <= 0 {
		return models.ReplenLevel{}, fmt.Errorf("%w: max_qty must be greater than zero", ErrInvalidArgument)
	}
	if in.MinQty < 0 || in.MaxQty < in.MinQty {
		return models.ReplenLevel{}, fmt.Errorf("%w: max_qty must be at least min_qty", ErrInvalidArgument)
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO wh_replen_levels (item_id, bin_id, min_qty, max_qty, source_zone_id, active, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (item_id, bin_id) DO UPDATE SET
			min_qty = EXCLUDED.min_qty, max_qty = EXCLUDED.max_qty,
			source_zone_id = EXCLUDED.source_zone_id, active = EXCLUDED.active, updated_at = NOW()
		RETURNING id`,
		in.ItemID, in.BinID, in.MinQty, in.MaxQty, in.SourceZoneID, in.Active, in.CreatedBy).Scan(&id)
	if err != nil {
		return models.ReplenLevel{}, err
	}
	return s.GetReplenLevel(ctx, id)
}

func (s *Store) GetReplenLevel(ctx context.Context, id uuid.UUID) (models.ReplenLevel, error) {
	l, err := scanReplenLevel(s.pool.QueryRow(ctx, `SELECT `+replenLevelCols+replenLevelFrom+` WHERE l.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return l, ErrNotFound
	}
	return l, err
}

func (s *Store) DeleteReplenLevel(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM wh_replen_levels WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

const replenTaskCols = `t.id, t.item_id, t.from_bin_id, t.to_bin_id, t.lot_key, t.qty, t.moved_qty, t.status,
	t.trigger, t.level_id, t.notes, t.created_by, t.completed_by, t.created_at, t.completed_at,
	i.sku, i.name, fb.code, tb.code`

const replenTaskFrom = `
	FROM wh_replen_tasks t
	JOIN wh_items i ON i.id = t.item_id
	JOIN wh_bins fb ON fb.id = t.from_bin_id
	JOIN wh_bins tb ON tb.id = t.to_bin_id`

func scanReplenTask(row pgx.Row) (models.ReplenTask, error) {
	var t models.ReplenTask
	err := row.Scan(&t.ID, &t.ItemID, &t.FromBinID, &t.ToBinID, &t.LotKey, &t.Qty, &t.MovedQty, &t.Status,
		&t.Trigger, &t.LevelID, &t.Notes, &t.CreatedBy, &t.CompletedBy, &t.CreatedAt, &t.CompletedAt,
		&t.ItemSKU, &t.ItemName, &t.FromBinCode, &t.ToBinCode)
	return t, err
}

func (s *Store) ListReplenTasks(ctx context.Context, status string, limit int) ([]models.ReplenTask, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	query := `SELECT ` + replenTaskCols + replenTaskFrom
	args := []any{}
	if status != "" {
		args = append(args, status)
		query += ` WHERE t.status = $1`
	}
	args = append(args, limit)
	query += fmt.Sprintf(` ORDER BY t.created_at DESC LIMIT $%d`, len(args))

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.ReplenTask{}
	for rows.Next() {
		t, err := scanReplenTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetReplenTask(ctx context.Context, id uuid.UUID) (models.ReplenTask, error) {
	t, err := scanReplenTask(s.pool.QueryRow(ctx, `SELECT `+replenTaskCols+replenTaskFrom+` WHERE t.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

type ReplenTaskInput struct {
	ItemID    uuid.UUID
	FromBinID uuid.UUID
	ToBinID   uuid.UUID
	LotKey    string
	Qty       float64
	Trigger   string
	LevelID   *uuid.UUID
	Notes     *string
	CreatedBy *uuid.UUID
}

// CreateReplenTask raises one move instruction and reserves the stock it will
// move. A second open task for the same (item, destination) is a conflict rather
// than a duplicate — that is what the partial unique index is for, and it is why
// the generator can be run on a schedule without coordination.
func (s *Store) CreateReplenTask(ctx context.Context, in ReplenTaskInput) (models.ReplenTask, error) {
	if in.Qty <= 0 {
		return models.ReplenTask{}, fmt.Errorf("%w: qty must be greater than zero", ErrInvalidArgument)
	}
	if in.FromBinID == in.ToBinID {
		return models.ReplenTask{}, fmt.Errorf("%w: source and destination bin are the same", ErrInvalidArgument)
	}
	if in.Trigger == "" {
		in.Trigger = models.ReplenTriggerManual
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.ReplenTask{}, err
	}
	defer tx.Rollback(ctx)

	lotKey, _ := normalizeKeys(in.LotKey, "")
	if err := s.reserveBalanceTx(ctx, tx, balanceKey{in.ItemID, in.FromBinID, lotKey, ""}, in.Qty); err != nil {
		return models.ReplenTask{}, err
	}

	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO wh_replen_tasks (item_id, from_bin_id, to_bin_id, lot_key, qty, trigger, level_id, notes, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
		in.ItemID, in.FromBinID, in.ToBinID, lotKey, in.Qty, in.Trigger, in.LevelID, in.Notes, in.CreatedBy).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			return models.ReplenTask{}, ErrConflict
		}
		return models.ReplenTask{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return models.ReplenTask{}, err
	}
	return s.GetReplenTask(ctx, id)
}

// CompleteReplenTask walks the stock. movedQty allows a short move (the bulk
// pallet ran out); the unmoved remainder's reservation is released rather than
// left dangling, because a reservation nobody is coming back for is stock that
// silently stops existing.
func (s *Store) CompleteReplenTask(ctx context.Context, id uuid.UUID, movedQty *float64, actorID *uuid.UUID) (models.ReplenTask, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.ReplenTask{}, err
	}
	defer tx.Rollback(ctx)

	var itemID, fromBin, toBin uuid.UUID
	var lotKey, status string
	var qty float64
	err = tx.QueryRow(ctx, `
		SELECT item_id, from_bin_id, to_bin_id, lot_key, qty, status
		FROM wh_replen_tasks WHERE id = $1 FOR UPDATE`, id,
	).Scan(&itemID, &fromBin, &toBin, &lotKey, &qty, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.ReplenTask{}, ErrNotFound
	}
	if err != nil {
		return models.ReplenTask{}, err
	}
	if status == models.ReplenCompleted {
		return s.GetReplenTask(ctx, id)
	}
	if status != models.ReplenOpen {
		return models.ReplenTask{}, ErrConflict
	}

	moved := qty
	if movedQty != nil {
		moved = *movedQty
		if moved < 0 || moved > qty {
			return models.ReplenTask{}, fmt.Errorf("%w: moved_qty must be between 0 and the task quantity", ErrInvalidArgument)
		}
	}

	if moved > 0 {
		if err := s.consumeReservedTx(ctx, tx, balanceKey{itemID, fromBin, lotKey, ""}, moved); err != nil {
			return models.ReplenTask{}, err
		}
		if err := s.adjustBalanceTx(ctx, tx, balanceKey{itemID, toBin, lotKey, ""}, moved, models.StatusAvailable); err != nil {
			return models.ReplenTask{}, err
		}
		sku, err := s.getItemSKU(ctx, tx, itemID)
		if err != nil {
			return models.ReplenTask{}, err
		}
		movID, err := s.insertMovementTx(ctx, tx, movementInput{
			MovementType: models.MovementTransfer,
			ItemID:       &itemID,
			FromBinID:    &fromBin,
			ToBinID:      &toBin,
			Qty:          moved,
			LotKey:       lotKey,
			RefType:      refType("replenishment"),
			RefID:        &id,
			ActorID:      actorID,
		})
		if err != nil {
			return models.ReplenTask{}, err
		}
		// Relocating stock is cost-neutral — same as any other bin transfer.
		if err := s.emitInventoryMovement(ctx, tx, movID, models.MovementTransfer, itemID, sku, &fromBin, &toBin, moved, lotKey, "", nil, movementCost{}, ""); err != nil {
			return models.ReplenTask{}, err
		}
	}
	if shortfall := qty - moved; shortfall > 0 {
		if err := s.releaseReservationTx(ctx, tx, balanceKey{itemID, fromBin, lotKey, ""}, shortfall); err != nil {
			return models.ReplenTask{}, err
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE wh_replen_tasks SET status = 'completed', moved_qty = $2, completed_by = $3, completed_at = NOW()
		WHERE id = $1`, id, moved, actorID); err != nil {
		return models.ReplenTask{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return models.ReplenTask{}, err
	}
	return s.GetReplenTask(ctx, id)
}

func (s *Store) CancelReplenTask(ctx context.Context, id uuid.UUID, actorID *uuid.UUID) (models.ReplenTask, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.ReplenTask{}, err
	}
	defer tx.Rollback(ctx)

	var itemID, fromBin uuid.UUID
	var lotKey, status string
	var qty float64
	err = tx.QueryRow(ctx, `
		SELECT item_id, from_bin_id, lot_key, qty, status FROM wh_replen_tasks WHERE id = $1 FOR UPDATE`, id,
	).Scan(&itemID, &fromBin, &lotKey, &qty, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.ReplenTask{}, ErrNotFound
	}
	if err != nil {
		return models.ReplenTask{}, err
	}
	if status == models.ReplenCancelled {
		return s.GetReplenTask(ctx, id)
	}
	if status != models.ReplenOpen {
		return models.ReplenTask{}, ErrConflict
	}
	if err := s.releaseReservationTx(ctx, tx, balanceKey{itemID, fromBin, lotKey, ""}, qty); err != nil {
		return models.ReplenTask{}, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE wh_replen_tasks SET status = 'cancelled', completed_by = $2, completed_at = NOW() WHERE id = $1`,
		id, actorID); err != nil {
		return models.ReplenTask{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return models.ReplenTask{}, err
	}
	return s.GetReplenTask(ctx, id)
}

// dueReplenLevel is a pick face that has fallen below its minimum.
type dueReplenLevel struct {
	LevelID      uuid.UUID
	ItemID       uuid.UUID
	BinID        uuid.UUID
	MaxQty       float64
	Available    float64
	SourceZoneID *uuid.UUID
}

// GenerateReplenTasks raises a task for every active level whose free stock has
// fallen below its minimum and that has no open task already. Levels with no
// source stock anywhere are skipped silently — there is nothing to instruct
// anyone to do — and are visible in the low-stock report instead, which is the
// signal to buy rather than to move.
func (s *Store) GenerateReplenTasks(ctx context.Context, actorID *uuid.UUID) ([]models.ReplenTask, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT l.id, l.item_id, l.bin_id, l.max_qty, COALESCE(bal.available, 0), l.source_zone_id
		FROM wh_replen_levels l
		LEFT JOIN LATERAL (
			SELECT COALESCE(SUM(sb.qty - sb.reserved), 0) AS available
			FROM wh_stock_balances sb
			WHERE sb.item_id = l.item_id AND sb.bin_id = l.bin_id AND sb.status = 'available'
		) bal ON TRUE
		WHERE l.active
		  AND COALESCE(bal.available, 0) < l.min_qty
		  AND NOT EXISTS (
			SELECT 1 FROM wh_replen_tasks t
			WHERE t.item_id = l.item_id AND t.to_bin_id = l.bin_id AND t.status = 'open'
		  )`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var due []dueReplenLevel
	for rows.Next() {
		var d dueReplenLevel
		if err := rows.Scan(&d.LevelID, &d.ItemID, &d.BinID, &d.MaxQty, &d.Available, &d.SourceZoneID); err != nil {
			return nil, err
		}
		due = append(due, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := []models.ReplenTask{}
	for _, d := range due {
		need := d.MaxQty - d.Available
		if need <= 0 {
			continue
		}
		srcBin, lotKey, free, err := s.findReplenSource(ctx, d.ItemID, d.BinID, d.SourceZoneID)
		if errors.Is(err, ErrInsufficientStock) {
			continue
		}
		if err != nil {
			return out, err
		}
		qty := need
		if free < qty {
			qty = free
		}
		levelID := d.LevelID
		task, err := s.CreateReplenTask(ctx, ReplenTaskInput{
			ItemID:    d.ItemID,
			FromBinID: srcBin,
			ToBinID:   d.BinID,
			LotKey:    lotKey,
			Qty:       qty,
			Trigger:   models.ReplenTriggerMinMax,
			LevelID:   &levelID,
			CreatedBy: actorID,
		})
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrInsufficientStock) {
			// Raced with another generator run or with a picker taking the stock
			// between the scan and the reservation. Neither is an error worth
			// failing the whole sweep for.
			continue
		}
		if err != nil {
			return out, err
		}
		out = append(out, task)
	}
	return out, nil
}

// findReplenSource picks the bulk bin to draw from. Ordering is FEFO to match
// how picking chooses stock, so replenishment never pushes fresher stock in
// front of older stock that is already on the floor.
func (s *Store) findReplenSource(ctx context.Context, itemID, excludeBin uuid.UUID, sourceZoneID *uuid.UUID) (uuid.UUID, string, float64, error) {
	var binID uuid.UUID
	var lotKey string
	var free float64
	err := s.pool.QueryRow(ctx, `
		SELECT sb.bin_id, sb.lot_key, (sb.qty - sb.reserved)
		FROM wh_stock_balances sb
		JOIN wh_bins bn ON bn.id = sb.bin_id
		JOIN wh_zones z ON z.id = bn.zone_id
		WHERE sb.item_id = $1 AND sb.status = 'available' AND sb.bin_id <> $2
		  AND (sb.qty - sb.reserved) > 0 AND bn.status = 'active'
		  AND ($3::uuid IS NULL OR z.id = $3)
		ORDER BY (
			SELECT MIN(lt.expiry_on) FROM wh_lots lt WHERE lt.lot_key = sb.lot_key AND sb.lot_key <> ''
		) ASC NULLS LAST, (sb.qty - sb.reserved) DESC
		LIMIT 1`, itemID, excludeBin, sourceZoneID).Scan(&binID, &lotKey, &free)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", 0, ErrInsufficientStock
	}
	return binID, lotKey, free, err
}
