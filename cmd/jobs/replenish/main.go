// Command replenish raises replenishment tasks for every pick face that has
// fallen below its minimum.
//
// This is the counterpart to the lowstock job. lowstock answers "should we buy
// more of this", which is a purchasing question; this answers "is there stock on
// site that should be moved to where the pickers are", which is a warehouse
// question. Running it on a schedule is what turns a min/max level from a number
// somebody typed once into work that appears on someone's list.
package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"

	"iag-warehouse/backend/internal/config"
	"iag-warehouse/backend/internal/db"
	"iag-warehouse/backend/internal/events"
	"iag-warehouse/backend/internal/outbox"
	"iag-warehouse/backend/internal/store"
)

func main() {
	_ = godotenv.Load()
	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pool.Close()

	st := store.New(pool)
	outboxStore := outbox.NewStore(pool)
	bus := events.New(events.Config{
		Brokers: cfg.KafkaBrokers,
		Enabled: cfg.EventBusEnabled && len(cfg.KafkaBrokers) > 0,
	})
	bus.SetOutbox(outboxStore)
	st.SetEventBus(bus)
	defer bus.Close()

	tasks, err := st.GenerateReplenTasks(ctx, nil)
	if err != nil {
		log.Fatalf("replenishment sweep: %v", err)
	}
	for _, t := range tasks {
		log.Printf("replenishment raised: sku=%s %.3f from %s to %s", t.ItemSKU, t.Qty, t.FromBinCode, t.ToBinCode)
	}

	if bus.Enabled() {
		pub := outbox.NewPublisher(outboxStore, bus)
		drainCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if _, err := pub.DrainOnce(drainCtx); err != nil {
			log.Printf("outbox drain: %v", err)
		}
	}

	log.Printf("replenishment sweep complete: %d task(s) raised", len(tasks))
	os.Exit(0)
}
