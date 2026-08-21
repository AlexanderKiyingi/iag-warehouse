package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Row struct {
	ID          int64
	EventType   string
	EventKey    string
	Payload     json.RawMessage
	CreatedAt   time.Time
	AvailableAt time.Time
	Attempts    int
	LastError   string
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Enqueue(ctx context.Context, eventType, key string, payload any) error {
	if s == nil || s.pool == nil {
		return ErrNotEnqueued
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal outbox payload: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO wh_event_outbox (event_type, event_key, payload)
		VALUES ($1, $2, $3::jsonb)
	`, eventType, nullable(key), body)
	return err
}

func (s *Store) ClaimBatch(ctx context.Context, limit int, backoff time.Duration) ([]Row, error) {
	if limit <= 0 {
		limit = 32
	}
	rows, err := s.pool.Query(ctx, `
		WITH due AS (
			SELECT id FROM wh_event_outbox
			WHERE dispatched_at IS NULL AND available_at <= NOW()
			ORDER BY id
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE wh_event_outbox o
		SET attempts = o.attempts + 1,
		    available_at = NOW() + $2::interval
		FROM due
		WHERE o.id = due.id
		RETURNING o.id, o.event_type, o.event_key, o.payload, o.created_at,
		          o.available_at, o.attempts, COALESCE(o.last_error, '')
	`, limit, fmt.Sprintf("%d milliseconds", backoff.Milliseconds()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Row{}
	for rows.Next() {
		var r Row
		var key *string
		if err := rows.Scan(&r.ID, &r.EventType, &key, &r.Payload, &r.CreatedAt,
			&r.AvailableAt, &r.Attempts, &r.LastError); err != nil {
			return nil, err
		}
		if key != nil {
			r.EventKey = *key
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) MarkDispatched(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE wh_event_outbox
		SET dispatched_at = NOW(), last_error = NULL
		WHERE id = $1
	`, id)
	return err
}

func (s *Store) MarkFailed(ctx context.Context, id int64, errMsg string, retryDelay time.Duration) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE wh_event_outbox
		SET last_error = $1, available_at = NOW() + $2::interval
		WHERE id = $3
	`, errMsg, fmt.Sprintf("%d milliseconds", retryDelay.Milliseconds()), id)
	return err
}

type Dispatcher interface {
	DispatchOutbox(ctx context.Context, row Row) error
}

type Publisher struct {
	store      *Store
	dispatcher Dispatcher
	tick       time.Duration
	batch      int
	maxBackoff time.Duration
}

func NewPublisher(store *Store, d Dispatcher) *Publisher {
	return &Publisher{
		store:      store,
		dispatcher: d,
		tick:       2 * time.Second,
		batch:      32,
		maxBackoff: 5 * time.Minute,
	}
}

// outboxIdleBackoffMax bounds how far the poll interval stretches when the
// outbox is empty.
//
// Each poll is a write transaction — FOR UPDATE SKIP LOCKED plus
// UPDATE ... RETURNING — issued whether or not there is anything to send. Across
// the services that run one of these, a fixed two-second tick is a constant
// floor of write traffic and WAL against the one shared Postgres, for a table
// that is empty most of the time.
//
// The cost of the backoff is latency on the FIRST event after a quiet spell:
// up to this long. It is kept deliberately short for that reason — a drain that
// finds anything resets to p.tick immediately, so a busy outbox keeps its
// original latency and only genuinely idle periods stretch out.
//
// The real fix is LISTEN/NOTIFY: the enqueue side signals, this side wakes
// immediately, and an idle outbox costs nothing at all. That needs a dedicated
// connection per service and is a larger change than this one.
const outboxIdleBackoffMax = 8 * time.Second

// nextOutboxInterval doubles the poll interval towards outboxIdleBackoffMax.
func nextOutboxInterval(current, base time.Duration) time.Duration {
	if current < base {
		current = base
	}
	if next := current * 2; next < outboxIdleBackoffMax {
		return next
	}
	return outboxIdleBackoffMax
}

func (p *Publisher) Run(ctx context.Context) {
	if p == nil || p.store == nil || p.dispatcher == nil {
		return
	}
	// A timer rather than a ticker, so the interval can adapt — see
	// outboxIdleBackoffMax.
	interval := p.tick
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			n, err := p.drainOnce(ctx)
			switch {
			case err != nil:
				slog.Warn("outbox drain", "err", err)
				// Back off on failure too: retrying a failing drain every two
				// seconds mostly multiplies whatever load is causing it.
				interval = nextOutboxInterval(interval, p.tick)
			case n > 0:
				if n >= p.batch {
					_, _ = p.drainOnce(ctx)
				}
				// There was work, so there is probably more: go straight back
				// to the base interval.
				interval = p.tick
			default:
				interval = nextOutboxInterval(interval, p.tick)
			}
			timer.Reset(interval)
		}
	}
}

func (p *Publisher) DrainOnce(ctx context.Context) (int, error) {
	return p.drainOnce(ctx)
}

func (p *Publisher) drainOnce(ctx context.Context) (int, error) {
	rows, err := p.store.ClaimBatch(ctx, p.batch, time.Second)
	if err != nil {
		return 0, err
	}
	for _, r := range rows {
		if err := p.dispatcher.DispatchOutbox(ctx, r); err != nil {
			delay := backoffFor(r.Attempts, p.maxBackoff)
			_ = p.store.MarkFailed(ctx, r.ID, err.Error(), delay)
			slog.Warn("outbox dispatch failed", "id", r.ID, "type", r.EventType, "err", err)
			continue
		}
		_ = p.store.MarkDispatched(ctx, r.ID)
	}
	return len(rows), nil
}

func backoffFor(attempts int, max time.Duration) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	d := time.Duration(math.Pow(2, float64(attempts))) * time.Second
	if d > max {
		return max
	}
	return d
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

var ErrNotEnqueued = errors.New("outbox: publisher not configured")
