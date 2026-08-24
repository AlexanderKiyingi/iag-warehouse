package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// The kinds of control this service can bypass.
//
// Closed set, stable values: the exception report groups on them, and a
// free-text kind invented at a call site would drop silently out of every
// count — which is the one failure an exception report must not have.
const (
	OverrideKindItemStatus    = "item_status"
	OverrideKindFEFO          = "fefo"
	OverrideKindNegativeStock = "negative_stock"
	OverrideKindEmergency     = "emergency_issue"
	OverrideKindTolerance     = "tolerance"
	OverrideKindGate          = "gate_exception"
	OverrideKindSoD           = "segregation_of_duties"
)

// ControlOverride is one recorded bypass of a warehouse control.
type ControlOverride struct {
	ID         uuid.UUID  `json:"id"`
	Kind       string     `json:"kind"`
	Subject    string     `json:"subject,omitempty"`
	FromState  string     `json:"from_state,omitempty"`
	ToState    string     `json:"to_state,omitempty"`
	Reason     string     `json:"reason,omitempty"`
	RefType    string     `json:"ref_type,omitempty"`
	RefID      string     `json:"ref_id,omitempty"`
	Permission string     `json:"permission,omitempty"`
	ActorID    *uuid.UUID `json:"actor_id,omitempty"`
	ActorName  string     `json:"actor_name,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// RecordControlOverride appends to the override log.
//
// It never returns an error to the caller's critical path — see the handler
// helper. The log matters, but losing one entry is a smaller failure than
// refusing a legitimate stock movement because the log was briefly unwritable,
// and a warehouse that stops when its audit table hiccups is a warehouse that
// gets the audit table switched off.
func (s *Store) RecordControlOverride(ctx context.Context, o ControlOverride) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO wh_control_overrides
			(kind, subject, from_state, to_state, reason, ref_type, ref_id, permission, actor_id, actor_name)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id`,
		o.Kind, o.Subject, o.FromState, o.ToState, o.Reason,
		o.RefType, o.RefID, o.Permission, o.ActorID, o.ActorName,
	).Scan(&id)
	return id, err
}

// ListControlOverridesFilter narrows the exception report.
type ListControlOverridesFilter struct {
	Kind  string
	Since *time.Time
	Limit int
}

// ListControlOverrides returns the exception report, newest first.
func (s *Store) ListControlOverrides(ctx context.Context, f ListControlOverridesFilter) ([]ControlOverride, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, kind, subject, from_state, to_state, reason, ref_type, ref_id,
		       permission, actor_id, actor_name, created_at
		FROM wh_control_overrides
		WHERE ($1 = '' OR kind = $1)
		  AND ($2::timestamptz IS NULL OR created_at >= $2)
		ORDER BY created_at DESC
		LIMIT $3`, f.Kind, f.Since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]ControlOverride, 0, 32)
	for rows.Next() {
		var o ControlOverride
		if err := rows.Scan(&o.ID, &o.Kind, &o.Subject, &o.FromState, &o.ToState, &o.Reason,
			&o.RefType, &o.RefID, &o.Permission, &o.ActorID, &o.ActorName, &o.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// CountControlOverridesByKind powers the summary line on the exception report.
func (s *Store) CountControlOverridesByKind(ctx context.Context, since time.Time) (map[string]int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT kind, COUNT(*) FROM wh_control_overrides
		WHERE created_at >= $1 GROUP BY kind ORDER BY kind`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var kind string
		var n int
		if err := rows.Scan(&kind, &n); err != nil {
			return nil, err
		}
		out[kind] = n
	}
	return out, rows.Err()
}
