package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// movementCost is the perpetual-inventory valuation attached to an emitted
// movement so finance can book the GL. Zero when costing is disabled or the
// figures are unavailable (finance no-ops on zero total cost).
type movementCost struct {
	UnitCost     float64
	TotalCost    float64 // signed valuation delta: +in on receipt, -out on issue
	AvgCostAfter float64
	Ref          string
	Currency     string
}

// movingAverage returns the new weighted-average unit cost after receiving
// `qty` units at `unitCost`, given the current `onHand` quantity valued at `avg`.
// When there is no net quantity (on_hand + qty <= 0, e.g. a first receipt into a
// zero/negative balance) it falls back to the incoming unit cost so the average
// is well-defined rather than dividing by zero.
func movingAverage(onHand, avg, qty, unitCost float64) float64 {
	total := onHand + qty
	if total <= 0 {
		return unitCost
	}
	return (onHand*avg + qty*unitCost) / total
}

// applyReceiptCostTx recomputes an item's moving-average cost for one priced
// receipt line and persists it, returning the valuation to attach to the
// movement event. It locks the item row FOR UPDATE so concurrent receipts of the
// same item serialise, and reads on-hand from stock balances *before* the
// caller applies this line's quantity.
//
// new_avg = (on_hand*avg + qty*unit_cost) / (on_hand + qty)
//
// A non-positive unit_cost (unpriced receipt) is a no-op: avg is left unchanged
// and a zero-cost movementCost is returned so finance ignores it. MUST be called
// before the line's balance adjustment.
func (s *Store) applyReceiptCostTx(ctx context.Context, tx pgx.Tx, itemID uuid.UUID, qty, unitCost float64, ref string) (movementCost, error) {
	if !s.costingEnabled || unitCost <= 0 || qty <= 0 {
		return movementCost{}, nil
	}

	var avg float64
	if err := tx.QueryRow(ctx, `SELECT avg_cost FROM wh_items WHERE id = $1 FOR UPDATE`, itemID).Scan(&avg); err != nil {
		return movementCost{}, err
	}
	var onHand float64
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(qty), 0) FROM wh_stock_balances WHERE item_id = $1`, itemID).Scan(&onHand); err != nil {
		return movementCost{}, err
	}

	newAvg := movingAverage(onHand, avg, qty, unitCost)
	if _, err := tx.Exec(ctx, `UPDATE wh_items SET avg_cost = $2, updated_at = NOW() WHERE id = $1`, itemID, newAvg); err != nil {
		return movementCost{}, err
	}

	return movementCost{
		UnitCost:     unitCost,
		TotalCost:    qty * unitCost, // goods in → positive
		AvgCostAfter: newAvg,
		Ref:          ref,
		Currency:     s.baseCurrency,
	}, nil
}

// outboundCostTx values an outbound movement (issue / negative adjustment) at the
// item's current moving-average cost. `signedQty` is positive for goods leaving;
// the returned TotalCost is negated (goods out). Moving-average is unchanged by
// an outbound move, so AvgCostAfter echoes the current avg. Zero avg → zero cost
// (no-op downstream).
func (s *Store) outboundCostTx(ctx context.Context, tx pgx.Tx, itemID uuid.UUID, signedQty float64, ref string) (movementCost, error) {
	if !s.costingEnabled || signedQty == 0 {
		return movementCost{}, nil
	}
	var avg float64
	if err := tx.QueryRow(ctx, `SELECT avg_cost FROM wh_items WHERE id = $1`, itemID).Scan(&avg); err != nil {
		return movementCost{}, err
	}
	if avg <= 0 {
		return movementCost{}, nil
	}
	return movementCost{
		UnitCost:     avg,
		TotalCost:    -(signedQty * avg), // goods out → negative
		AvgCostAfter: avg,
		Ref:          ref,
		Currency:     s.baseCurrency,
	}, nil
}

// adjustmentCostTx values an inventory adjustment at avg cost. `delta` is the
// signed quantity change (qty_after − qty_before); a positive delta increases
// inventory value, negative decreases it. Returns a movementCost whose TotalCost
// carries the same sign as delta.
func (s *Store) adjustmentCostTx(ctx context.Context, tx pgx.Tx, itemID uuid.UUID, delta float64, ref string) (movementCost, error) {
	if !s.costingEnabled || delta == 0 {
		return movementCost{}, nil
	}
	var avg float64
	if err := tx.QueryRow(ctx, `SELECT avg_cost FROM wh_items WHERE id = $1`, itemID).Scan(&avg); err != nil {
		return movementCost{}, err
	}
	if avg <= 0 {
		return movementCost{}, nil
	}
	return movementCost{
		UnitCost:     avg,
		TotalCost:    delta * avg, // signed: + increases inventory, - decreases
		AvgCostAfter: avg,
		Ref:          ref,
		Currency:     s.baseCurrency,
	}, nil
}
