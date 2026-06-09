package inventory

import (
	"context"

	"iag-warehouse/backend/internal/events"
)

// Bridge emits warehouse.movement.posted for Phase 4 inventory delegation.
type Bridge struct {
	bus *events.Bus
}

func NewBridge(bus *events.Bus) *Bridge {
	return &Bridge{bus: bus}
}

// MovementPayload is the handoff contract to iag-inventory.
type MovementPayload struct {
	MovementID      string         `json:"movement_id"`
	MovementType    string         `json:"movement_type"`
	ItemID          string         `json:"item_id"`
	SKU             string         `json:"sku"`
	FromBinID       string         `json:"from_bin_id,omitempty"`
	ToBinID         string         `json:"to_bin_id,omitempty"`
	Qty             float64        `json:"qty"`
	LotKey          string         `json:"lot_key"`
	SerialKey       string         `json:"serial_key"`
	BatchBusinessID string         `json:"batch_business_id,omitempty"`
	Attrs           map[string]any `json:"attrs,omitempty"`
}

// EmitMovementPosted publishes warehouse.movement.posted when inventory service is live.
func (b *Bridge) EmitMovementPosted(ctx context.Context, payload MovementPayload) {
	if b == nil || b.bus == nil || !b.bus.Enabled() {
		return
	}
	data := map[string]any{
		"movement_id":   payload.MovementID,
		"movement_type": payload.MovementType,
		"item_id":       payload.ItemID,
		"sku":           payload.SKU,
		"qty":           payload.Qty,
		"lot_key":       payload.LotKey,
		"serial_key":    payload.SerialKey,
	}
	if payload.FromBinID != "" {
		data["from_bin_id"] = payload.FromBinID
	}
	if payload.ToBinID != "" {
		data["to_bin_id"] = payload.ToBinID
	}
	if payload.BatchBusinessID != "" {
		data["batch_business_id"] = payload.BatchBusinessID
	}
	if payload.Attrs != nil {
		data["attrs"] = payload.Attrs
	}
	b.bus.Publish(ctx, events.TypeMovementPosted, data, payload.MovementID)
}
