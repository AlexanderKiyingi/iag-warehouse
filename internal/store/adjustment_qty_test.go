package store

import "testing"

func qty(f float64) *float64 { return &f }

// The rule that decides what a bin holds after a stock change.
//
// The failure this pins is not hypothetical: before qty_delta existed, the only
// field the service accepted was the absolute closing quantity, and every
// client that thinks in movements ("wrote off 5") had to convert. A client that
// got that backwards and sent the movement as the absolute would set the bin to
// 5 — silently, with a successful response, and with the difference posted as a
// real adjustment. That is why the write-off screen was left read-only rather
// than wired approximately.
func TestResolveQtyAfter(t *testing.T) {
	cases := []struct {
		name      string
		in        AdjustmentInput
		qtyBefore float64
		want      float64
	}{
		{
			// A counter has just counted the bin; the figure is the answer.
			name:      "absolute wins when only qty_after is sent",
			in:        AdjustmentInput{QtyAfter: 12},
			qtyBefore: 100,
			want:      12,
		},
		{
			name:      "a write-off subtracts from what is there",
			in:        AdjustmentInput{QtyDelta: qty(-5)},
			qtyBefore: 100,
			want:      95,
		},
		{
			name:      "a positive delta adds",
			in:        AdjustmentInput{QtyDelta: qty(7)},
			qtyBefore: 100,
			want:      107,
		},
		{
			// The precise regression: 5 sent as a movement must not become 5 on
			// hand. If this ever reads 5, the delta is being treated as absolute.
			name:      "a delta is never the closing quantity",
			in:        AdjustmentInput{QtyDelta: qty(-5)},
			qtyBefore: 5,
			want:      0,
		},
		{
			// Delta wins over a zero-valued QtyAfter, which is what an unset
			// float64 looks like — the pointer is the presence signal, not the
			// value.
			name:      "delta wins over an unset absolute",
			in:        AdjustmentInput{QtyAfter: 0, QtyDelta: qty(3)},
			qtyBefore: 10,
			want:      13,
		},
		{
			// Stock going negative is a real answer here, not a clamp: the
			// balance constraints decide whether it is allowed, and swallowing
			// it in the arithmetic would hide an over-issue.
			name:      "an over-large write-off is not clamped",
			in:        AdjustmentInput{QtyDelta: qty(-30)},
			qtyBefore: 10,
			want:      -20,
		},
		{
			name:      "counting a bin to zero is honoured",
			in:        AdjustmentInput{QtyAfter: 0},
			qtyBefore: 42,
			want:      0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveQtyAfter(tc.in, tc.qtyBefore)
			if got != tc.want {
				t.Fatalf("resolveQtyAfter(before=%v) = %v, want %v", tc.qtyBefore, got, tc.want)
			}
		})
	}
}
