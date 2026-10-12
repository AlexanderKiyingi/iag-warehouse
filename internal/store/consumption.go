package store

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
)

// WorkOrderConsumption is one line of stock a work order consumed: what left
// which bin, how much, and what it cost.
//
// It is read from the movement ledger, not from issue lines, because the
// ledger is what moved — an issue that never posted consumed nothing — and
// because the movement carries the cost it moved at.
type WorkOrderConsumption struct {
	WorkOrderRef string    `json:"work_order_ref"`
	CostCenter   string    `json:"cost_center,omitempty"`
	IssueID      uuid.UUID `json:"issue_id"`
	PostedAt     time.Time `json:"posted_at"`
	ItemID       uuid.UUID `json:"item_id"`
	SKU          string    `json:"sku"`
	Name         string    `json:"name"`
	UOM          string    `json:"uom"`
	BinCode      string    `json:"bin_code"`
	Qty          float64   `json:"qty"`
	UnitCost     float64   `json:"unit_cost"`
	TotalCost    float64   `json:"total_cost"`
	Currency     string    `json:"currency"`
	// Estimated is true when the movement carried no cost (costing was off, or
	// the item had never been priced when it moved) and the item's current
	// average cost stands in for it. A report should say so rather than pass an
	// estimate off as the booked figure.
	Estimated bool `json:"estimated"`
}

// ConsumptionFilter narrows the read. Empty means every work order.
type ConsumptionFilter struct {
	WorkOrderRefs []string
	Since         *time.Time
	Until         *time.Time
	Limit         int
}

// ListWorkOrderConsumption returns what posted issues consumed against work
// orders, newest first.
func (s *Store) ListWorkOrderConsumption(ctx context.Context, f ConsumptionFilter) ([]WorkOrderConsumption, error) {
	if f.Limit <= 0 || f.Limit > 5000 {
		f.Limit = 1000
	}
	var refs []string
	for _, r := range f.WorkOrderRefs {
		if r = strings.TrimSpace(r); r != "" {
			refs = append(refs, r)
		}
	}
	rows, err := s.pool.Query(ctx, `
		SELECT i.work_order_ref, COALESCE(i.cost_center, ''), i.id, i.posted_at,
		       m.item_id, it.sku, it.name, it.uom, COALESCE(b.code, ''),
		       m.qty, m.unit_cost, m.total_cost, m.cost_currency, it.avg_cost
		FROM wh_issues i
		JOIN wh_movements m ON m.ref_type = 'issue' AND m.ref_id = i.id AND m.movement_type = 'issue'
		JOIN wh_items it ON it.id = m.item_id
		LEFT JOIN wh_bins b ON b.id = m.from_bin_id
		WHERE i.status = 'posted'
		  AND COALESCE(i.work_order_ref, '') <> ''
		  AND (COALESCE(cardinality($1::text[]), 0) = 0 OR i.work_order_ref = ANY($1::text[]))
		  AND ($2::timestamptz IS NULL OR i.posted_at >= $2)
		  AND ($3::timestamptz IS NULL OR i.posted_at < $3)
		ORDER BY i.posted_at DESC, i.id, it.sku
		LIMIT $4`, refs, f.Since, f.Until, f.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WorkOrderConsumption{}
	for rows.Next() {
		var c WorkOrderConsumption
		var avg float64
		var posted *time.Time
		if err := rows.Scan(&c.WorkOrderRef, &c.CostCenter, &c.IssueID, &posted,
			&c.ItemID, &c.SKU, &c.Name, &c.UOM, &c.BinCode,
			&c.Qty, &c.UnitCost, &c.TotalCost, &c.Currency, &avg); err != nil {
			return nil, err
		}
		if posted != nil {
			c.PostedAt = *posted
		}
		// The ledger signs cost by direction (negative out of stock); a work
		// order's consumption is a positive cost to it.
		c.Qty = math.Abs(c.Qty)
		c.TotalCost = math.Abs(c.TotalCost)
		if c.TotalCost == 0 && avg > 0 {
			c.UnitCost = avg
			c.TotalCost = avg * c.Qty
			c.Estimated = true
		}
		if c.Currency == "" {
			c.Currency = s.baseCurrency
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
