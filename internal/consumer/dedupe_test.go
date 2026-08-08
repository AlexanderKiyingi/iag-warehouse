package consumer

import (
	"os"
	"strings"
	"testing"

	"iag-warehouse/backend/migrations"
)

// The dedupe table is the only thing standing between a redelivered event and
// duplicate stock — no handler in this consumer is idempotent on its own. It
// used to record that an event had been *seen*, committed before the handler
// ran, so a failed dispatch left a claim that made the redelivery skip the
// work: a goods receipt lost to a transient error, marked done.
//
// The fix is the completed_at column plus a claim that only defers to a
// completed row. These are drift guards. The behaviour itself is SQL and needs
// a database to exercise; what is checked here is that the pieces it rests on
// are still present, because removing either silently restores the data loss.

func TestDedupeTracksCompletionSeparatelyFromSighting(t *testing.T) {
	raw, err := migrations.FS.ReadFile("016_dedupe_completion.sql")
	if err != nil {
		t.Fatalf("reading migration: %v", err)
	}
	sql := string(raw)
	if !strings.Contains(sql, "completed_at") {
		t.Error("migration no longer adds completed_at; a claim would again mean 'handled'")
	}
	// Rows predating the column were final under the old semantics. Leaving them
	// null would re-open every historic event for reprocessing on redelivery.
	if !strings.Contains(sql, "SET completed_at = seen_at") {
		t.Error("migration no longer backfills existing rows as complete")
	}
}

func TestClaimOnlyDefersToCompletedEvents(t *testing.T) {
	raw, err := os.ReadFile("consumer.go")
	if err != nil {
		t.Fatalf("reading consumer source: %v", err)
	}
	src := string(raw)
	// An unconditional ON CONFLICT DO NOTHING is the original bug: it treats a
	// stale claim from a failed attempt as proof the work was done.
	if !strings.Contains(src, "kafka_dedupe.completed_at IS NULL") {
		t.Error("claim no longer takes over incomplete rows; a failed dispatch would be skipped on redelivery")
	}
	if !strings.Contains(src, "SET completed_at = NOW()") {
		t.Error("completion is never recorded; every event would be reprocessed forever")
	}
	if !strings.Contains(src, "DELETE FROM kafka_dedupe") {
		t.Error("a failed dispatch no longer releases its claim")
	}
}
