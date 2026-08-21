package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"

	platformevents "github.com/alvor-technologies/iag-platform-go/events"
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"iag-warehouse/backend/internal/store"
)

type Config struct {
	Brokers          []string
	GroupID          string
	CommercialTopic  string
	ProductionTopic  string
	QualityTopic     string
	OperationsTopic  string
	SupplyChainTopic string
	// DLQTopic receives messages the handler cannot process. Empty disables it,
	// and poison messages are logged and skipped instead.
	DLQTopic string
}

// An event that cannot be identified or routed is a producer contract problem,
// not a transient failure — it is dead-lettered rather than retried.
var (
	errMissingEventID   = errors.New("event has no id; cannot be deduplicated safely")
	errMissingEventType = errors.New("event has no type; cannot be routed")
)

type Consumer struct {
	cfg   Config
	store *store.Store
}

func New(cfg Config, st *store.Store) *Consumer {
	return &Consumer{cfg: cfg, store: st}
}

// Run consumes every configured topic through the shared platform consumer.
//
// This service used to drive its own Kafka loop. That loop marked an event
// handled before handling it, so a failed dispatch was never retried and the
// redelivery skipped the work — a goods receipt lost to a transient error. It
// also had no retries and no dead-letter path, which left a permanently failing
// message blocking its offset forever.
//
// The shared consumer already answers all three: it dedupes on the way in,
// retries transient failures with backoff, marks the event only after the
// handler succeeds, and routes poison messages to a DLQ. One consumer is run
// per topic because it takes a single topic; they share the dedupe table and
// the handler.
func (c *Consumer) Run(ctx context.Context) error {
	if len(c.cfg.Brokers) == 0 {
		log.Printf("warehouse consumer: KAFKA_BROKERS unset — skipping")
		return nil
	}
	topics := uniqueTopics(c.cfg.CommercialTopic, c.cfg.ProductionTopic, c.cfg.QualityTopic, c.cfg.OperationsTopic, c.cfg.SupplyChainTopic)
	if len(topics) == 0 {
		return nil
	}

	var dlq *platformevents.Producer
	if c.cfg.DLQTopic != "" {
		dlq = platformevents.NewProducer(platformevents.ProducerConfig{
			Brokers:  c.cfg.Brokers,
			ClientID: "iag-warehouse-dlq",
		})
		defer dlq.Close()
	} else {
		// Without a DLQ the shared consumer logs a poison message and moves on,
		// which is still better than the old loop wedging on it — but it is a
		// silent drop, so say so at boot rather than at three in the morning.
		log.Printf("warehouse consumer: KAFKA_DLQ_TOPIC unset — poison messages will be logged and skipped")
	}

	dedupe := platformevents.PostgresDedupe(c.store.Pool(), "kafka_dedupe")

	log.Printf("warehouse consumer: listening on %v (group=%s)", topics, c.cfg.GroupID)
	grp, gctx := errgroup.WithContext(ctx)
	for _, topic := range topics {
		inner, err := platformevents.NewConsumer(platformevents.ConsumerConfig{
			Brokers:     c.cfg.Brokers,
			Topic:       topic,
			GroupID:     c.cfg.GroupID,
			Handler:     platformevents.HandlerFunc(c.handle),
			Dedupe:      dedupe,
			DLQProducer: dlq,
			DLQTopic:    c.cfg.DLQTopic,
		})
		if err != nil {
			return err
		}
		grp.Go(func() error { return inner.Run(gctx) })
	}
	return grp.Wait()
}

// handle routes one event to its handler.
//
// An event with no id is refused rather than processed. Dedupe is keyed on that
// id and none of these handlers is idempotent on its own, so an unidentifiable
// event cannot be replayed safely — every redelivery would add the stock again.
// Sending it to the DLQ makes it a question for a person instead of a silent
// duplicate.
func (c *Consumer) handle(ctx context.Context, env platformevents.Envelope) error {
	if strings.TrimSpace(env.ID) == "" {
		return platformevents.Permanent(errMissingEventID)
	}
	if env.Type == "" {
		return platformevents.Permanent(errMissingEventType)
	}
	return c.dispatch(ctx, env.Type, env.Data)
}

func (c *Consumer) dispatch(ctx context.Context, eventType string, data map[string]any) error {
	if data == nil {
		data = map[string]any{}
	}
	switch eventType {
	case "procurement.grn.posted":
		return c.handleGRNPosted(ctx, data)
	case "mes.wetmill.completed", "mes.drying.completed", "mes.drymill.completed":
		if eventType == "mes.drymill.completed" {
			return c.handleMESCompleted(ctx, eventType, data)
		}
		return c.handleMESBackflush(ctx, eventType, data)
	case "scm.intake.received", "scm.batch.received":
		return c.handleSCMIntake(ctx, data)
	case "qc.coa.issued":
		return c.handleCOAIssued(ctx, data)
	case "dms.dispatch.created":
		return c.handleDispatchCreated(ctx, data)
	default:
		return nil
	}
}

func (c *Consumer) handleGRNPosted(ctx context.Context, data map[string]any) error {
	grnID, _ := strField(data, "grn_id")
	poID, _ := strField(data, "po_id")
	if grnID == "" {
		return nil
	}
	linesRaw, ok := data["lines"].([]any)
	if !ok {
		_, err := c.store.CreateDraftReceiptFromGRNEvent(ctx, grnID, poID, nil)
		return err
	}
	var lines []store.ReceiptLineInput
	for _, row := range linesRaw {
		m, ok := row.(map[string]any)
		if !ok {
			continue
		}
		itemIDStr, _ := strField(m, "item_id")
		sku, _ := strField(m, "sku")
		binCode, _ := strField(m, "bin_code")
		qty := numField(m, "qty")
		if qty <= 0 {
			continue
		}
		var itemID uuid.UUID
		if itemIDStr != "" {
			id, err := uuid.Parse(itemIDStr)
			if err != nil {
				continue
			}
			itemID = id
		} else if sku != "" {
			id, err := c.store.GetItemIDBySKU(ctx, sku)
			if err != nil {
				continue
			}
			itemID = id
		} else {
			continue
		}
		if binCode == "" {
			binCode = c.store.DefaultReceivingBinCode(ctx)
		}
		uom, _ := strField(m, "uom")
		if uom == "" {
			uom = "ea"
		}
		lotKey, _ := strField(m, "lot_key")
		var batchID *string
		if b, ok := strField(m, "batch_business_id"); ok {
			batchID = &b
		}
		// The PO price, so the intake is valued rather than received at zero
		// cost. Safe now that a GRN-raised receipt no longer books its own GL
		// entry: while it did, a priced receipt double-counted the delivery
		// against GR/IR. Absent on emitters predating the field → unpriced, and
		// the weighted-average engine leaves the item's cost alone.
		lines = append(lines, store.ReceiptLineInput{
			ItemID: itemID, Qty: qty, UOM: uom, BinCode: binCode, LotKey: lotKey, BatchBusinessID: batchID,
			UnitCost: numField(m, "unit_price"),
		})
	}
	_, err := c.store.CreateDraftReceiptFromGRNEvent(ctx, grnID, poID, lines)
	return err
}

func (c *Consumer) handleMESCompleted(ctx context.Context, eventType string, data map[string]any) error {
	batchID, _ := strField(data, "batch_business_id")
	if batchID == "" {
		return nil
	}
	if eventType != "mes.drymill.completed" {
		return nil
	}
	sku, _ := strField(data, "sku")
	binCode, _ := strField(data, "bin_code")
	qty := numField(data, "qty")
	if sku == "" || binCode == "" || qty <= 0 {
		return nil
	}
	items, err := c.store.ListItems(ctx, "finished_good")
	if err != nil {
		return err
	}
	var itemID uuid.UUID
	for _, it := range items {
		if it.SKU == sku {
			itemID = it.ID
			break
		}
	}
	if itemID == uuid.Nil {
		return nil
	}
	lotKey, _ := strField(data, "lot_key")
	_, err = c.store.ProductionOutput(ctx, store.ProductionOutputInput{
		BatchBusinessID: batchID,
		SKU:             sku,
		ItemID:          itemID,
		Qty:             qty,
		BinCode:         binCode,
		LotKey:          lotKey,
		QCHold:          true,
	})
	return err
}

func (c *Consumer) handleCOAIssued(ctx context.Context, data map[string]any) error {
	lotKey, ok := strField(data, "lot_business_id")
	if !ok || lotKey == "" {
		lotKey, _ = strField(data, "lot_key")
	}
	if lotKey != "" {
		return c.store.ReleaseQCHold(ctx, lotKey)
	}
	batchID, _ := strField(data, "batch_business_id")
	if batchID != "" {
		return c.store.ReleaseQCHoldByBatch(ctx, batchID)
	}
	return nil
}

func (c *Consumer) handleMESBackflush(ctx context.Context, _ string, data map[string]any) error {
	batchID, _ := strField(data, "batch_business_id")
	if batchID == "" {
		return nil
	}
	linesRaw, ok := data["backflush_lines"].([]any)
	if !ok {
		return nil
	}
	var lines []store.ProductionConsumeLine
	for _, row := range linesRaw {
		m, ok := row.(map[string]any)
		if !ok {
			continue
		}
		sku, _ := strField(m, "sku")
		binCode, _ := strField(m, "bin_code")
		qty := numField(m, "qty")
		if sku == "" || binCode == "" || qty <= 0 {
			continue
		}
		itemID, err := c.store.GetItemIDBySKU(ctx, sku)
		if err != nil {
			continue
		}
		lotKey, _ := strField(m, "lot_key")
		lines = append(lines, store.ProductionConsumeLine{
			ItemID: itemID, Qty: qty, BinCode: binCode, LotKey: lotKey,
		})
	}
	if len(lines) == 0 {
		return nil
	}
	facility, _ := strField(data, "facility_code")
	_, err := c.store.ProductionConsume(ctx, store.ProductionConsumeInput{
		BatchBusinessID: batchID,
		FacilityCode:    facility,
		Lines:           lines,
	})
	return err
}

func (c *Consumer) handleSCMIntake(ctx context.Context, data map[string]any) error {
	batchID, _ := strField(data, "batch_business_id")
	if batchID == "" {
		batchID, _ = strField(data, "batch_id")
	}
	sku, _ := strField(data, "sku")
	qty := numField(data, "qty")
	if batchID == "" || sku == "" || qty <= 0 {
		return nil
	}
	itemID, err := c.store.GetItemIDBySKU(ctx, sku)
	if err != nil {
		return nil
	}
	binCode := c.store.DefaultReceivingBinCode(ctx)
	lotKey, _ := strField(data, "lot_key")
	if lotKey == "" {
		lotKey = batchID
	}
	_, err = c.store.CreateReceipt(ctx, store.CreateReceiptInput{
		ReceiptType: "scm_intake",
		SourceRef:   strPtr("scm"),
		Notes:       strPtr("auto-draft from scm intake"),
		Lines: []store.ReceiptLineInput{{
			ItemID: itemID, Qty: qty, UOM: "kg", BinCode: binCode, LotKey: lotKey,
			BatchBusinessID: &batchID,
		}},
	})
	return err
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (c *Consumer) handleDispatchCreated(ctx context.Context, data map[string]any) error {
	dispatchID, _ := strField(data, "dispatch_id")
	orderRef, _ := strField(data, "order_ref")
	if orderRef == "" {
		orderRef, _ = strField(data, "order_id")
	}
	return c.store.HandleDispatchCreated(ctx, dispatchID, orderRef)
}

func strField(data map[string]any, key string) (string, bool) {
	v, ok := data[key]
	if !ok {
		return "", false
	}
	switch t := v.(type) {
	case string:
		return t, t != ""
	default:
		return "", false
	}
}

func numField(data map[string]any, key string) float64 {
	v, ok := data[key]
	if !ok {
		return 0
	}
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case json.Number:
		f, _ := t.Float64()
		return f
	default:
		return 0
	}
}

func uniqueTopics(parts ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}
