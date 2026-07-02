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

// MovementPayload is the handoff contract to iag-inventory and iag-finance.
//
// The cost fields (UnitCost/TotalCost/AvgCostAfter) are the perpetual-inventory
// valuation carried to finance so it can book the GL (Dr 1400/Cr 2150 on
// receipt, Dr 5000/Cr 1400 on issue). They are zero until the weighted-average-
// cost engine populates them; finance's consumer no-ops on a zero-cost movement,
// so this contract is safe to ship ahead of the costing engine. See
// docs/PERPETUAL_INVENTORY_EVENTS.md.
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

	// Perpetual-inventory valuation (base currency); zero until the WAC engine
	// computes it for this movement.
	Ref          string  `json:"ref,omitempty"`            // source doc (GRN/issue ref) for the finance memo
	Currency     string  `json:"currency,omitempty"`       // base currency of the cost figures
	UnitCost     float64 `json:"unit_cost,omitempty"`      // receipt: PO cost; issue: avg cost used
	TotalCost    float64 `json:"total_cost,omitempty"`     // signed valuation delta (+in / -out)
	AvgCostAfter float64 `json:"avg_cost_after,omitempty"` // moving-average cost after the move
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
	// Valuation for finance's GL posting — emitted only once the WAC engine has
	// computed a cost, so finance stays dormant until then.
	if payload.TotalCost != 0 {
		data["unit_cost"] = payload.UnitCost
		data["total_cost"] = payload.TotalCost
		data["avg_cost_after"] = payload.AvgCostAfter
		if payload.Ref != "" {
			data["ref"] = payload.Ref
		}
		if payload.Currency != "" {
			data["currency"] = payload.Currency
		}
	}
	b.bus.Publish(ctx, events.TypeMovementPosted, data, payload.MovementID)
}
