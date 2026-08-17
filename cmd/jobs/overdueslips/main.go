// Command overdueslips chases equipment that left on a returnable gate pass or
// handover slip and has not come back.
//
// Issuing a slip is the easy half. The half that actually recovers a generator
// or a set of scaffold clamps is somebody being told, on the day after it was
// due, that it is still out and whose name is on it. Without this job the
// return date is decoration.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"

	"iag-warehouse/backend/internal/config"
	"iag-warehouse/backend/internal/db"
	"iag-warehouse/backend/internal/events"
	"iag-warehouse/backend/internal/models"
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

	notifyRecipient := events.DefaultNotifyRecipient()

	slips, err := st.ListOverdueSlips(ctx)
	if err != nil {
		log.Fatalf("overdue slip scan: %v", err)
	}

	for _, slip := range slips {
		no := "(unnumbered)"
		if slip.SlipNo != nil {
			no = *slip.SlipNo
		}
		holder := slip.IssuedToName
		if slip.SlipType == models.SlipCargoGatePass && slip.DriverName != "" {
			holder = slip.DriverName
		}
		due := "an unrecorded date"
		if slip.ReturnBy != nil {
			due = slip.ReturnBy.Format("2 Jan 2006")
		}

		if bus.Enabled() && notifyRecipient != "" {
			bus.PublishAlert(ctx, "", notifyRecipient, "warehouse.alert", map[string]string{
				"Title": "Overdue: " + no,
				"Body": fmt.Sprintf("%s issued to %s was due back on %s and is still outstanding (%d line(s)).",
					no, holder, due, len(slip.Lines)),
			}, "warehouse-overdue-slip-"+slip.ID.String())
		}
		log.Printf("overdue slip: no=%s type=%s holder=%q due=%s", no, slip.SlipType, holder, due)
	}

	if bus.Enabled() {
		pub := outbox.NewPublisher(outboxStore, bus)
		drainCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if _, err := pub.DrainOnce(drainCtx); err != nil {
			log.Printf("outbox drain: %v", err)
		}
	}

	log.Printf("overdue slip scan complete: %d outstanding", len(slips))
	os.Exit(0)
}
