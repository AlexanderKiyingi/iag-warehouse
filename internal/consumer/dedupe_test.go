package consumer

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	platformevents "github.com/alvor-technologies/iag-platform-go/events"

	"iag-warehouse/backend/migrations"
)

// This service used to drive its own Kafka loop, and it marked an event handled
// before handling it: the dedupe row was committed, then the handler ran, and a
// failure left a claim that made the redelivery skip the work. No handler here
// is idempotent on its own, so that row is the only guard — a goods receipt hit
// by a transient database error was discarded permanently, recorded as done.
//
// The loop is now the shared platform consumer, which dedupes on the way in,
// retries, marks only after the handler succeeds, and dead-letters poison
// messages. These tests guard the properties that fix depends on.

func TestConsumerDoesNotDriveItsOwnKafkaLoop(t *testing.T) {
	src := readSource(t, "consumer.go")
	// Reintroducing a hand-rolled loop is how the original bug got written, and
	// it would quietly opt this service out of retries and the DLQ as well.
	for _, banned := range []string{"FetchMessage", "CommitMessages", "kafka.NewReader"} {
		if strings.Contains(src, banned) {
			t.Errorf("consumer calls %s directly; it should route through platformevents.NewConsumer", banned)
		}
	}
	if !strings.Contains(src, "platformevents.NewConsumer") {
		t.Error("consumer no longer uses the shared platform consumer")
	}
	if !strings.Contains(src, "DLQTopic") {
		t.Error("consumer no longer configures a dead-letter topic")
	}
}

// Dedupe is keyed on the event id. Without one, a redelivery cannot be
// recognised and every delivery would add the stock again — so an unidentified
// event has to be refused, and refused permanently rather than retried forever.
func TestEventWithoutAnIDIsDeadLetteredNotProcessed(t *testing.T) {
	c := &Consumer{}
	err := c.handle(context.Background(), platformevents.Envelope{
		Type: "procurement.grn.posted",
		Data: map[string]any{},
	})
	if err == nil {
		t.Fatal("an event with no id was accepted; it cannot be deduplicated and would duplicate stock")
	}
	if !errors.Is(err, errMissingEventID) {
		t.Errorf("error %v does not identify the missing id", err)
	}
	var perm *platformevents.PermanentError
	if !errors.As(err, &perm) {
		t.Error("the failure is retryable; it will never succeed and must go to the DLQ")
	}
}

func TestEventWithoutATypeIsDeadLetteredNotProcessed(t *testing.T) {
	c := &Consumer{}
	err := c.handle(context.Background(), platformevents.Envelope{ID: "evt-1"})
	if !errors.Is(err, errMissingEventType) {
		t.Fatalf("error %v does not identify the missing type", err)
	}
	var perm *platformevents.PermanentError
	if !errors.As(err, &perm) {
		t.Error("an unroutable event is retryable; it must be dead-lettered instead")
	}
}

// The shared dedupe writes only the event id, and a row's presence now means
// "handled". Rows the old loop left incomplete represent work that never
// happened, so they have to go — otherwise they would read as handled and
// suppress exactly the redeliveries the fix exists to allow.
func TestMigrationClearsClaimsTheOldLoopNeverCompleted(t *testing.T) {
	raw, err := migrations.FS.ReadFile("017_dedupe_platform_consumer.sql")
	if err != nil {
		t.Fatalf("reading migration: %v", err)
	}
	sql := string(raw)
	if !strings.Contains(sql, "DELETE FROM kafka_dedupe WHERE completed_at IS NULL") {
		t.Error("incomplete claims are not cleared; they would suppress the redelivery of work that never ran")
	}
	if !strings.Contains(sql, "ALTER COLUMN topic DROP NOT NULL") {
		t.Error("topic is still required, but the shared dedupe only writes the event id")
	}
}

func readSource(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(raw)
}
