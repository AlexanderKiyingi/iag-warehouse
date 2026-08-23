package store

import (
	"context"
	"time"
)

// ValuationHealthSummary is the evidence behind "is P5 actually holding".
type ValuationHealthSummary struct {
	TotalMovements    int `json:"total_movements"`
	ValuedMovements   int `json:"valued_movements"`
	UnvaluedMovements int `json:"unvalued_movements"`
	// ValuationDelta is the signed sum of movement value in the window: what
	// finance should have booked against the inventory account if every event
	// was consumed. It is the figure to reconcile the GL against.
	ValuationDelta   float64 `json:"valuation_delta"`
	ItemsWithoutCost int     `json:"items_without_cost"`
	StockValueOnHand float64 `json:"stock_value_on_hand"`
}

// ValuationHealth measures how much stock movement carried a value.
//
// Only the movement types that have a financial consequence are counted. A
// bin-to-bin transfer moves no value by definition, so including it would
// report a healthy system as broken and train everyone to ignore the number.
func (s *Store) ValuationHealth(ctx context.Context, since time.Time) (ValuationHealthSummary, error) {
	var out ValuationHealthSummary

	err := s.pool.QueryRow(ctx, `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE total_cost <> 0),
			COUNT(*) FILTER (WHERE total_cost = 0),
			COALESCE(SUM(total_cost), 0)
		FROM wh_movements
		WHERE occurred_at >= $1
		  AND movement_type IN ('receipt', 'issue', 'adjustment',
		                        'production_consume', 'production_output', 'return')`,
		since,
	).Scan(&out.TotalMovements, &out.ValuedMovements, &out.UnvaluedMovements, &out.ValuationDelta)
	if err != nil {
		return out, err
	}

	// Items holding stock but carrying no average cost. Each one is an issue
	// waiting to post at zero, which understates cost of sales and overstates
	// the closing balance — the exact shape of an audit finding.
	err = s.pool.QueryRow(ctx, `
		SELECT COUNT(DISTINCT i.id)
		FROM wh_items i
		JOIN wh_stock_balances b ON b.item_id = i.id AND b.qty > 0
		WHERE i.avg_cost = 0`,
	).Scan(&out.ItemsWithoutCost)
	if err != nil {
		return out, err
	}

	err = s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(b.qty * i.avg_cost), 0)
		FROM wh_stock_balances b JOIN wh_items i ON i.id = b.item_id`,
	).Scan(&out.StockValueOnHand)
	return out, err
}
