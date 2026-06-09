package store

import (
	"context"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"iag-warehouse/backend/internal/models"
)

func (s *Store) ListMovements(ctx context.Context, movementType string, itemID *uuid.UUID, limit int) ([]models.Movement, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := `
		SELECT id, movement_type, item_id, from_bin_id, to_bin_id, qty, lot_key, serial_key,
			ref_type, ref_id, batch_business_id, actor_id, occurred_at, attrs, created_at
		FROM wh_movements`
	var args []any
	clauses := []string{}
	if movementType != "" {
		args = append(args, movementType)
		clauses = append(clauses, "movement_type = $"+strconv.Itoa(len(args)))
	}
	if itemID != nil {
		args = append(args, *itemID)
		clauses = append(clauses, "item_id = $"+strconv.Itoa(len(args)))
	}
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	args = append(args, limit)
	query += " ORDER BY occurred_at DESC LIMIT $" + strconv.Itoa(len(args))

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMovements(rows)
}

func scanMovements(rows pgx.Rows) ([]models.Movement, error) {
	var out []models.Movement
	for rows.Next() {
		var m models.Movement
		if err := rows.Scan(
			&m.ID, &m.MovementType, &m.ItemID, &m.FromBinID, &m.ToBinID, &m.Qty,
			&m.LotKey, &m.SerialKey, &m.RefType, &m.RefID, &m.BatchBusinessID,
			&m.ActorID, &m.OccurredAt, &m.Attrs, &m.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
