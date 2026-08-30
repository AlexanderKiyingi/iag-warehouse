package store

import "testing"

// The rule that completes a write-off's declared valuation.
//
// Same shape as TestResolveQtyAfter next door, and for the same class of
// reason: a screen knows one half of a valuation, and the half it knows differs
// by screen. A write-off form asks for a unit cost; an insurance-driven one
// asks for a total. Storing whichever arrived and leaving the other blank makes
// the column unusable for reporting; storing both as sent lets them disagree
// about a single movement.
//
// The derivation runs against the quantity that actually moved, computed inside
// the transaction, not a quantity the client sent — so a movement that was
// clamped or partially applied is valued at what it moved.
func TestResolveDeclaredValue(t *testing.T) {
	cases := []struct {
		name         string
		in           AdjustmentInput
		qtyMoved     float64
		wantUnitCost *float64
		wantValue    *float64
	}{
		{
			name:     "nothing declared stays nothing",
			in:       AdjustmentInput{},
			qtyMoved: 5,
		},
		{
			name:         "unit cost fills in the total",
			in:           AdjustmentInput{DeclaredUnitCost: qty(2500)},
			qtyMoved:     4,
			wantUnitCost: qty(2500),
			wantValue:    qty(10000),
		},
		{
			name:         "total fills in the unit cost",
			in:           AdjustmentInput{DeclaredValue: qty(10000)},
			qtyMoved:     4,
			wantUnitCost: qty(2500),
			wantValue:    qty(10000),
		},
		{
			// The handler refuses this pair, so reaching it means an internal
			// caller meant both figures. Restating one from the other would
			// overwrite a deliberate number.
			name:         "both sent are both kept",
			in:           AdjustmentInput{DeclaredUnitCost: qty(2500), DeclaredValue: qty(9000)},
			qtyMoved:     4,
			wantUnitCost: qty(2500),
			wantValue:    qty(9000),
		},
		{
			// Dividing by zero here would store +Inf as the unit cost of a
			// movement that moved nothing.
			name:      "a total with no movement keeps the total and derives no unit cost",
			in:        AdjustmentInput{DeclaredValue: qty(10000)},
			qtyMoved:  0,
			wantValue: qty(10000),
		},
		{
			name:         "a unit cost with no movement values the write-off at zero",
			in:           AdjustmentInput{DeclaredUnitCost: qty(2500)},
			qtyMoved:     0,
			wantUnitCost: qty(2500),
			wantValue:    qty(0),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotUnit, gotValue := resolveDeclaredValue(tc.in, tc.qtyMoved)
			assertMoney(t, "unit cost", gotUnit, tc.wantUnitCost)
			assertMoney(t, "value", gotValue, tc.wantValue)
		})
	}
}

func assertMoney(t *testing.T, label string, got, want *float64) {
	t.Helper()
	switch {
	case got == nil && want == nil:
	case got == nil:
		t.Errorf("%s: got nil, want %v", label, *want)
	case want == nil:
		t.Errorf("%s: got %v, want nil", label, *got)
	case *got != *want:
		t.Errorf("%s: got %v, want %v", label, *got, *want)
	}
}
