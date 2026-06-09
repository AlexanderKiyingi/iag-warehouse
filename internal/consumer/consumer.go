package consumer

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"

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
}

type Consumer struct {
	cfg   Config
	store *store.Store
}

func New(cfg Config, st *store.Store) *Consumer {
	return &Consumer{cfg: cfg, store: st}
}

func (c *Consumer) Run(ctx context.Context) error {
	if len(c.cfg.Brokers) == 0 {
		log.Printf("warehouse consumer: KAFKA_BROKERS unset — skipping")
		return nil
	}
	topics := uniqueTopics(c.cfg.CommercialTopic, c.cfg.ProductionTopic, c.cfg.QualityTopic, c.cfg.OperationsTopic, c.cfg.SupplyChainTopic)
	if len(topics) == 0 {
		return nil
	}

	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     c.cfg.Brokers,
		GroupID:     c.cfg.GroupID,
		GroupTopics: topics,
		MinBytes:    1,
		MaxBytes:    10e6,
	})
	defer r.Close()

	log.Printf("warehouse consumer: listening on %v (group=%s)", topics, c.cfg.GroupID)
	for {
		msg, err := r.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("warehouse consumer fetch: %v", err)
			continue
		}
		if err := c.handleMessage(ctx, msg); err != nil {
			log.Printf("warehouse consumer handle topic=%s: %v", msg.Topic, err)
			continue
		}
		if err := r.CommitMessages(ctx, msg); err != nil {
			log.Printf("warehouse consumer commit: %v", err)
		}
	}
}

type cloudEvent struct {
	ID   string         `json:"id"`
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

func (c *Consumer) handleMessage(ctx context.Context, msg kafka.Message) error {
	var env cloudEvent
	if err := json.Unmarshal(msg.Value, &env); err != nil {
		return err
	}
	eventID := env.ID
	if eventID == "" {
		eventID = string(msg.Key)
	}
	tag, err := c.store.Pool().Exec(ctx, `
		INSERT INTO kafka_dedupe (event_id, topic) VALUES ($1, $2)
		ON CONFLICT (event_id) DO NOTHING`, eventID, msg.Topic)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil
	}

	eventType := env.Type
	if eventType == "" {
		eventType = string(msg.Key)
	}
	return c.dispatch(ctx, eventType, env.Data)
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
		lines = append(lines, store.ReceiptLineInput{
			ItemID: itemID, Qty: qty, UOM: uom, BinCode: binCode, LotKey: lotKey, BatchBusinessID: batchID,
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
