package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/segmentio/kafka-go"

	"iag-warehouse/backend/internal/outbox"
)

const (
	SpecVersion = "1.0"
	Source      = "iag.warehouse"
	TopicOps    = "iag.operations"

	TypeReceiptPosted       = "warehouse.receipt.posted"
	TypeIssuePosted         = "warehouse.issue.posted"
	TypeTransferCompleted   = "warehouse.transfer.completed"
	TypeProductionConsumed  = "warehouse.production.consumed"
	TypeProductionOutput    = "warehouse.production.output"
	TypePickConfirmed       = "warehouse.pick.confirmed"
	TypeAssetCheckedOut     = "warehouse.asset.checked_out"
	TypeAssetDisposed       = "warehouse.asset.disposed"
	TypeStockBelowMinimum   = "warehouse.stock.below_minimum"
	TypeMovementPosted      = "warehouse.movement.posted"
	TypeAlertRaised         = "warehouse.alert.raised"
)

type PlatformEvent struct {
	ID            string         `json:"id"`
	Type          string         `json:"type"`
	Time          string         `json:"time"`
	Source        string         `json:"source"`
	SpecVersion   string         `json:"specversion"`
	CorrelationID string         `json:"correlationId,omitempty"`
	CausationID   string         `json:"causationId,omitempty"`
	Data          map[string]any `json:"data"`
}

type Bus struct {
	writer  *kafka.Writer
	enabled bool
	outbox  *outbox.Store
}

type Config struct {
	Brokers []string
	Enabled bool
}

func New(cfg Config) *Bus {
	if !cfg.Enabled || len(cfg.Brokers) == 0 {
		return &Bus{enabled: false}
	}
	return &Bus{
		enabled: true,
		writer: &kafka.Writer{
			Addr:         kafka.TCP(cfg.Brokers...),
			Topic:        TopicOps,
			Balancer:     &kafka.LeastBytes{},
			RequiredAcks: kafka.RequireAll,
			Transport:    &kafka.Transport{ClientID: Source},
		},
	}
}

func (b *Bus) SetOutbox(store *outbox.Store) {
	if b != nil {
		b.outbox = store
	}
}

func (b *Bus) Enabled() bool { return b != nil && b.enabled }

func (b *Bus) Close() error {
	if b == nil || !b.enabled || b.writer == nil {
		return nil
	}
	return b.writer.Close()
}

func (b *Bus) Publish(ctx context.Context, eventType string, data map[string]any, key string) {
	if b == nil || !b.enabled {
		return
	}
	evt := PlatformEvent{
		ID:          uuid.NewString(),
		Type:        eventType,
		Time:        time.Now().UTC().Format(time.RFC3339Nano),
		Source:      Source,
		SpecVersion: SpecVersion,
		Data:        data,
	}
	if b.outbox != nil {
		if err := b.outbox.Enqueue(ctx, eventType, key, evt); err != nil {
			slog.Warn("warehouse event enqueue failed", "type", eventType, "err", err)
		}
		return
	}
	if err := b.publishDirect(ctx, evt, key); err != nil {
		slog.Warn("warehouse event publish failed", "type", eventType, "err", err)
	}
}

func (b *Bus) PublishTx(ctx context.Context, tx pgx.Tx, eventType string, data map[string]any, key string) error {
	if b == nil || !b.enabled {
		return nil
	}
	evt := PlatformEvent{
		ID:          uuid.NewString(),
		Type:        eventType,
		Time:        time.Now().UTC().Format(time.RFC3339Nano),
		Source:      Source,
		SpecVersion: SpecVersion,
		Data:        data,
	}
	body, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO wh_event_outbox (event_type, event_key, payload)
		VALUES ($1, $2, $3::jsonb)
	`, eventType, nullableKey(key), body)
	return err
}

func (b *Bus) DispatchOutbox(ctx context.Context, row outbox.Row) error {
	if !b.enabled || b.writer == nil {
		return nil
	}
	var evt PlatformEvent
	if err := json.Unmarshal(row.Payload, &evt); err != nil {
		return fmt.Errorf("decode outbox payload: %w", err)
	}
	if evt.Type == "" {
		evt.Type = row.EventType
	}
	if evt.ID == "" {
		evt.ID = uuid.NewString()
	}
	if evt.Source == "" {
		evt.Source = Source
	}
	if evt.SpecVersion == "" {
		evt.SpecVersion = SpecVersion
	}
	if evt.Time == "" {
		evt.Time = time.Now().UTC().Format(time.RFC3339Nano)
	}
	key := row.EventKey
	if key == "" {
		key = evt.ID
	}
	return b.publishDirect(ctx, evt, key)
}

func (b *Bus) publishDirect(ctx context.Context, evt PlatformEvent, key string) error {
	body, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	if key == "" {
		key = evt.ID
	}
	return b.writer.WriteMessages(ctx, kafka.Message{
		Topic: TopicOps,
		Key:   []byte(key),
		Value: body,
		Headers: []kafka.Header{
			{Key: "ce-type", Value: []byte(evt.Type)},
			{Key: "ce-source", Value: []byte(evt.Source)},
		},
	})
}

// PublishAlert emits warehouse.alert.raised on iag.operations for the
// notifications policy consumer, using the shared
// {channel,recipient,templateId,variables} envelope.
func (b *Bus) PublishAlert(ctx context.Context, channel, recipient, templateID string, variables map[string]string, key string) {
	if b == nil || !b.enabled || recipient == "" || templateID == "" {
		return
	}
	vars := map[string]any{}
	for k, v := range variables {
		vars[k] = v
	}
	if channel == "" {
		channel = defaultNotifyChannel()
	}
	b.Publish(ctx, TypeAlertRaised, map[string]any{
		"channel":    channel,
		"recipient":  recipient,
		"templateId": templateID,
		"variables":  vars,
	}, key)
}

func ParseBrokers(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func defaultNotifyChannel() string {
	if ch := strings.TrimSpace(os.Getenv("NOTIFY_CHANNEL")); ch != "" {
		return ch
	}
	return "email"
}

// DefaultNotifyRecipient is the fallback recipient (e.g. the warehouse/ops
// desk) used when an alert has no specific user to address.
func DefaultNotifyRecipient() string {
	return strings.TrimSpace(os.Getenv("NOTIFY_DEFAULT_RECIPIENT"))
}

func nullableKey(s string) any {
	if s == "" {
		return nil
	}
	return s
}
