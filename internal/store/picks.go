package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"iag-warehouse/backend/internal/events"
	"iag-warehouse/backend/internal/models"
)

type PickLineInput struct {
	ItemID  uuid.UUID
	Qty     float64
	BinCode string
	LotKey  string
}

type CreatePickListInput struct {
	OrderRef  *string
	Notes     *string
	Lines     []PickLineInput
	CreatedBy *uuid.UUID
}

func (s *Store) CreatePickList(ctx context.Context, in CreatePickListInput) (models.PickList, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.PickList{}, err
	}
	defer tx.Rollback(ctx)

	var pl models.PickList
	err = tx.QueryRow(ctx, `
		INSERT INTO wh_pick_lists (order_ref, notes, created_by)
		VALUES ($1, $2, $3)
		RETURNING id, status, order_ref, notes, confirmed_at, created_by, created_at, updated_at`,
		in.OrderRef, in.Notes, in.CreatedBy,
	).Scan(&pl.ID, &pl.Status, &pl.OrderRef, &pl.Notes, &pl.ConfirmedAt, &pl.CreatedBy, &pl.CreatedAt, &pl.UpdatedAt)
	if err != nil {
		return pl, err
	}

	for _, line := range in.Lines {
		bin, _, err := s.GetBinByCode(ctx, line.BinCode)
		if err != nil {
			return pl, err
		}
		lotKey, _ := normalizeKeys(line.LotKey, "")
		// Reserve the stock now so it can't be issued or picked twice while this
		// list is open. Fails the whole pick if free stock can't cover the line.
		if err := s.reserveBalanceTx(ctx, tx, balanceKey{line.ItemID, bin.ID, lotKey, ""}, line.Qty); err != nil {
			return pl, err
		}
		var pline models.PickLine
		err = tx.QueryRow(ctx, `
			INSERT INTO wh_pick_lines (pick_list_id, item_id, qty, bin_id, lot_key)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING id, pick_list_id, item_id, qty, bin_id, lot_key, picked_qty`,
			pl.ID, line.ItemID, line.Qty, bin.ID, lotKey,
		).Scan(&pline.ID, &pline.PickListID, &pline.ItemID, &pline.Qty, &pline.BinID, &pline.LotKey, &pline.PickedQty)
		if err != nil {
			return pl, err
		}
		pl.Lines = append(pl.Lines, pline)
	}

	if err := tx.Commit(ctx); err != nil {
		return pl, err
	}
	return pl, nil
}

func (s *Store) ConfirmPickList(ctx context.Context, pickListID uuid.UUID, actorID *uuid.UUID) (models.PickList, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.PickList{}, err
	}
	defer tx.Rollback(ctx)

	var status string
	var orderRef *string
	err = tx.QueryRow(ctx, `SELECT status, order_ref FROM wh_pick_lists WHERE id = $1 FOR UPDATE`, pickListID).Scan(&status, &orderRef)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.PickList{}, ErrNotFound
	}
	if err != nil {
		return models.PickList{}, err
	}
	if status == "confirmed" {
		return s.getPickList(ctx, pickListID)
	}

	rows, err := tx.Query(ctx, `
		SELECT pl.item_id, pl.qty, pl.bin_id, pl.lot_key, i.sku
		FROM wh_pick_lines pl JOIN wh_items i ON i.id = pl.item_id
		WHERE pl.pick_list_id = $1`, pickListID)
	if err != nil {
		return models.PickList{}, err
	}
	type lineRow struct {
		itemID, binID uuid.UUID
		qty           float64
		lotKey, sku   string
	}
	var lines []lineRow
	var eventLines []map[string]any
	for rows.Next() {
		var l lineRow
		if err := rows.Scan(&l.itemID, &l.qty, &l.binID, &l.lotKey, &l.sku); err != nil {
			rows.Close()
			return models.PickList{}, err
		}
		lines = append(lines, l)
	}
	rows.Close()

	refTypePick := refType("pick_list")
	for _, l := range lines {
		lotKey, _ := normalizeKeys(l.lotKey, "")
		// Consume the reservation made at pick creation: removes on-hand and
		// releases the held quantity in one step.
		if err := s.consumeReservedTx(ctx, tx, balanceKey{l.itemID, l.binID, lotKey, ""}, l.qty); err != nil {
			return models.PickList{}, err
		}
		movID, err := s.insertMovementTx(ctx, tx, movementInput{
			MovementType: models.MovementPick,
			ItemID:       &l.itemID,
			FromBinID:    &l.binID,
			Qty:          l.qty,
			LotKey:       lotKey,
			RefType:      refTypePick,
			RefID:        &pickListID,
			ActorID:      actorID,
		})
		if err != nil {
			return models.PickList{}, err
		}
		// A pick is an internal allocation, not a costed goods-out event (the
		// subsequent issue/dispatch carries COGS), so it emits no valuation.
		s.emitInventoryMovement(ctx, movID, models.MovementPick, l.itemID, l.sku, &l.binID, nil, l.qty, lotKey, "", nil, movementCost{})
		eventLines = append(eventLines, map[string]any{
			"item_id": l.itemID.String(),
			"sku":     l.sku,
			"qty":     l.qty,
			"lot_key": lotKey,
		})
	}

	_, err = tx.Exec(ctx, `
		UPDATE wh_pick_lists SET status = 'confirmed', confirmed_at = NOW(), updated_at = NOW() WHERE id = $1`, pickListID)
	if err != nil {
		return models.PickList{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE wh_pick_lines SET picked_qty = qty WHERE pick_list_id = $1`, pickListID)
	if err != nil {
		return models.PickList{}, err
	}

	if s.bus != nil && s.bus.Enabled() {
		data := map[string]any{
			"pick_list_id": pickListID.String(),
			"order_ref":    orderRef,
			"lines":        eventLines,
		}
		if err := s.bus.PublishTx(ctx, tx, events.TypePickConfirmed, data, pickListID.String()); err != nil {
			return models.PickList{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return models.PickList{}, err
	}
	return s.getPickList(ctx, pickListID)
}

// CancelPickList releases the stock reserved by an open pick list and marks it
// cancelled. Idempotent (a second cancel returns the already-cancelled list);
// a confirmed list cannot be cancelled (its stock is already consumed).
func (s *Store) CancelPickList(ctx context.Context, pickListID uuid.UUID, actorID *uuid.UUID) (models.PickList, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.PickList{}, err
	}
	defer tx.Rollback(ctx)

	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM wh_pick_lists WHERE id = $1 FOR UPDATE`, pickListID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.PickList{}, ErrNotFound
	}
	if err != nil {
		return models.PickList{}, err
	}
	if status == "cancelled" {
		return s.getPickList(ctx, pickListID)
	}
	if status == "confirmed" {
		return models.PickList{}, ErrConflict
	}

	rows, err := tx.Query(ctx, `SELECT item_id, qty, bin_id, lot_key FROM wh_pick_lines WHERE pick_list_id = $1`, pickListID)
	if err != nil {
		return models.PickList{}, err
	}
	type lineRow struct {
		itemID, binID uuid.UUID
		qty           float64
		lotKey        string
	}
	var lines []lineRow
	for rows.Next() {
		var l lineRow
		if err := rows.Scan(&l.itemID, &l.qty, &l.binID, &l.lotKey); err != nil {
			rows.Close()
			return models.PickList{}, err
		}
		lines = append(lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return models.PickList{}, err
	}

	for _, l := range lines {
		lotKey, _ := normalizeKeys(l.lotKey, "")
		if err := s.releaseReservationTx(ctx, tx, balanceKey{l.itemID, l.binID, lotKey, ""}, l.qty); err != nil {
			return models.PickList{}, err
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE wh_pick_lists SET status = 'cancelled', updated_at = NOW() WHERE id = $1`, pickListID); err != nil {
		return models.PickList{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return models.PickList{}, err
	}
	return s.getPickList(ctx, pickListID)
}

func (s *Store) ListPickLists(ctx context.Context, status string, limit int) ([]models.PickList, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var rows pgx.Rows
	var err error
	if status != "" {
		rows, err = s.pool.Query(ctx, `
			SELECT id, status, order_ref, notes, confirmed_at, created_by, created_at, updated_at
			FROM wh_pick_lists WHERE status = $1 ORDER BY created_at DESC LIMIT $2`, status, limit)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT id, status, order_ref, notes, confirmed_at, created_by, created_at, updated_at
			FROM wh_pick_lists ORDER BY created_at DESC LIMIT $1`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.PickList
	for rows.Next() {
		var pl models.PickList
		if err := rows.Scan(&pl.ID, &pl.Status, &pl.OrderRef, &pl.Notes, &pl.ConfirmedAt, &pl.CreatedBy, &pl.CreatedAt, &pl.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, pl)
	}
	return out, rows.Err()
}

func (s *Store) GetPickList(ctx context.Context, id uuid.UUID) (models.PickList, error) {
	return s.getPickList(ctx, id)
}

func (s *Store) getPickList(ctx context.Context, id uuid.UUID) (models.PickList, error) {
	var pl models.PickList
	err := s.pool.QueryRow(ctx, `
		SELECT id, status, order_ref, notes, confirmed_at, created_by, created_at, updated_at
		FROM wh_pick_lists WHERE id = $1`, id,
	).Scan(&pl.ID, &pl.Status, &pl.OrderRef, &pl.Notes, &pl.ConfirmedAt, &pl.CreatedBy, &pl.CreatedAt, &pl.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return pl, ErrNotFound
	}
	if err != nil {
		return pl, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, pick_list_id, item_id, qty, bin_id, lot_key, picked_qty
		FROM wh_pick_lines WHERE pick_list_id = $1`, id)
	if err != nil {
		return pl, err
	}
	defer rows.Close()
	for rows.Next() {
		var line models.PickLine
		if err := rows.Scan(&line.ID, &line.PickListID, &line.ItemID, &line.Qty, &line.BinID, &line.LotKey, &line.PickedQty); err != nil {
			return pl, err
		}
		pl.Lines = append(pl.Lines, line)
	}
	return pl, rows.Err()
}

func (s *Store) CreatePackSession(ctx context.Context, pickListID *uuid.UUID, createdBy *uuid.UUID, attrs map[string]any) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO wh_pack_sessions (pick_list_id, created_by, attrs)
		VALUES ($1, $2, $3) RETURNING id`, pickListID, createdBy, attrsOrEmpty(attrs)).Scan(&id)
	return id, err
}

func (s *Store) ListPackSessions(ctx context.Context, limit int) ([]models.PackSession, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, pick_list_id, status, attrs, created_by, created_at
		FROM wh_pack_sessions ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.PackSession{}
	for rows.Next() {
		var p models.PackSession
		if err := rows.Scan(&p.ID, &p.PickListID, &p.Status, &p.Attrs, &p.CreatedBy, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetPackSession(ctx context.Context, id uuid.UUID) (models.PackSession, error) {
	var p models.PackSession
	err := s.pool.QueryRow(ctx, `
		SELECT id, pick_list_id, status, attrs, created_by, created_at
		FROM wh_pack_sessions WHERE id = $1`, id,
	).Scan(&p.ID, &p.PickListID, &p.Status, &p.Attrs, &p.CreatedBy, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

func (s *Store) HandleDispatchCreated(ctx context.Context, dispatchID, orderRef string) error {
	rows, err := s.pool.Query(ctx, `
		SELECT id FROM wh_pick_lists WHERE order_ref = $1 AND status = 'open' LIMIT 1`, orderRef)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil
	}
	var pickID uuid.UUID
	if err := rows.Scan(&pickID); err != nil {
		return err
	}
	_, err = s.ConfirmPickList(ctx, pickID, nil)
	return err
}
