package store

import "testing"

// withinTolerance decides which count variances need a human, so the boundary
// behaviour is worth pinning: with no tolerance set nothing is waved through,
// and either bound alone is sufficient.
func TestWithinTolerance(t *testing.T) {
	cases := []struct {
		name                               string
		variance, varianceValue, systemQty float64
		tolPct, tolValue                   float64
		want                               bool
	}{
		{"no tolerance configured waves nothing through", -6, -30, 100, 0, 0, false},
		{"inside the percentage bound", -1, -5, 100, 2, 0, true},
		{"exactly on the percentage bound", -2, -10, 100, 2, 0, true},
		{"outside the percentage bound", -3, -15, 100, 2, 0, false},
		{"inside the cash bound", -50, -9, 100, 0, 10, true},
		{"exactly on the cash bound", -50, -10, 100, 0, 10, true},
		{"outside the cash bound", -50, -11, 100, 0, 10, false},
		{"either bound alone is enough", -50, -9, 100, 1, 10, true},
		{"a surplus is judged the same as a shortfall", 2, 10, 100, 2, 0, true},
		// A percentage of nothing is not a percentage: stock the system thought
		// was absent must always be looked at, however little of it turned up.
		{"percentage cannot excuse a find against a zero system quantity", 5, 25, 0, 50, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := withinTolerance(tc.variance, tc.varianceValue, tc.systemQty, tc.tolPct, tc.tolValue)
			if got != tc.want {
				t.Errorf("withinTolerance(%v, %v, %v, %v, %v) = %v, want %v",
					tc.variance, tc.varianceValue, tc.systemQty, tc.tolPct, tc.tolValue, got, tc.want)
			}
		})
	}
}
