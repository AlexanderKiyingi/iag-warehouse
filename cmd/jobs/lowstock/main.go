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

	items, err := st.ListLowStock(ctx)
	if err != nil {
		log.Fatalf("low stock scan: %v", err)
	}

	for _, item := range items {
		data := map[string]any{
			"item_id": item.ItemID.String(),
			"sku":     item.SKU,
			"qty":     item.Qty,
			"min_qty": item.MinQty,
			"bin_code": item.BinCode,
		}
		if bus.Enabled() {
			bus.Publish(ctx, events.TypeStockBelowMinimum, data, item.SKU)
			// Raise a user-visible replenishment alert to the warehouse desk.
			if notifyRecipient != "" {
				bus.PublishAlert(ctx, "", notifyRecipient, "warehouse.alert", map[string]string{
					"Title": "Low stock: " + item.SKU,
					"Body":  fmt.Sprintf("SKU %s in bin %s is at %.2f (minimum %.2f) — replenishment needed.", item.SKU, item.BinCode, item.Qty, item.MinQty),
				}, "warehouse-lowstock-"+item.SKU)
			}
		} else {
			_ = outboxStore.Enqueue(ctx, events.TypeStockBelowMinimum, item.SKU, map[string]any{
				"type": events.TypeStockBelowMinimum,
				"data": data,
			})
		}
		log.Printf("low stock: sku=%s qty=%.2f min=%.2f", item.SKU, item.Qty, item.MinQty)
	}

	if bus.Enabled() {
		pub := outbox.NewPublisher(outboxStore, bus)
		drainCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if _, err := pub.DrainOnce(drainCtx); err != nil {
			log.Printf("outbox drain: %v", err)
		}
	}

	log.Printf("low stock job complete: %d items", len(items))
	os.Exit(0)
}
