package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Document numbering.
//
// Count sheets, gate passes and handover slips are documents people quote at
// each other — over a radio, in a security ledger, on a phone call to the mill —
// so they need short readable numbers rather than UUIDs. The counter row is
// locked for the life of the caller's transaction, which means a number is only
// consumed if the document that claimed it is actually committed: an abandoned
// draft leaves no gap, and a gap in a gate-pass series is exactly the kind of
// thing that has to be explained to an auditor.
//
// The scope key carries the year, so each series restarts at 1 each January and
// the year in the number is the year in the sequence.
func nextDocumentNumber(ctx context.Context, tx pgx.Tx, series, prefix string) (string, error) {
	year := time.Now().UTC().Year()
	scope := fmt.Sprintf("%s:%d", series, year)

	var n int64
	err := tx.QueryRow(ctx, `
		INSERT INTO wh_document_counters (scope, next_value)
		VALUES ($1, 2)
		ON CONFLICT (scope) DO UPDATE SET
			next_value = wh_document_counters.next_value + 1,
			updated_at = NOW()
		RETURNING next_value - 1`, scope).Scan(&n)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%d-%06d", prefix, year, n), nil
}
