package store

import "testing"

// Verifies the weighted-average-cost arithmetic that drives perpetual-inventory
// valuation. Pure math (no DB) — the SQL wrappers around it are exercised by the
// WAREHOUSE_TEST_DB integration tests.
func TestMovingAverage(t *testing.T) {
	const eps = 1e-9
	cases := []struct {
		name                          string
		onHand, avg, qty, unitCost    float64
		want                          float64
	}{
		{"first receipt into empty stock", 0, 0, 100, 15.00, 15.00},
		{"second receipt at higher cost blends", 100, 15.00, 100, 17.00, 16.00},
		{"receipt at same cost is unchanged", 50, 20.00, 25, 20.00, 20.00},
		{"receipt at lower cost pulls average down", 10, 10.00, 10, 6.00, 8.00},
		{"tiny top-up barely moves a large balance", 1000, 12.00, 1, 100.00, 12.087912087912088},
		{"zero net quantity falls back to unit cost", -10, 8.00, 10, 9.00, 9.00},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := movingAverage(tc.onHand, tc.avg, tc.qty, tc.unitCost)
			if diff := got - tc.want; diff > eps || diff < -eps {
				t.Fatalf("movingAverage(%v,%v,%v,%v) = %v, want %v", tc.onHand, tc.avg, tc.qty, tc.unitCost, got, tc.want)
			}
		})
	}
}
