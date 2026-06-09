package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type IdempotencyRecord struct {
	StatusCode int
	Body       json.RawMessage
}

func (s *Store) GetIdempotency(ctx context.Context, actorID uuid.UUID, key string) (IdempotencyRecord, bool, error) {
	var rec IdempotencyRecord
	err := s.pool.QueryRow(ctx, `
		SELECT status_code, response_body FROM wh_idempotency
		WHERE actor_id = $1 AND idempotency_key = $2`, actorID, key,
	).Scan(&rec.StatusCode, &rec.Body)
	if errors.Is(err, pgx.ErrNoRows) {
		return rec, false, nil
	}
	if err != nil {
		return rec, false, err
	}
	return rec, true, nil
}

func (s *Store) SaveIdempotency(ctx context.Context, actorID uuid.UUID, key, route string, statusCode int, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO wh_idempotency (actor_id, idempotency_key, route, status_code, response_body)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (actor_id, idempotency_key) DO NOTHING`,
		actorID, key, route, statusCode, raw)
	return err
}
