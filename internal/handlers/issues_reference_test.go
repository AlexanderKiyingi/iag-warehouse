package handlers

import (
	"testing"

	"iag-warehouse/backend/internal/store"
)

func ref(s string) *string { return &s }

// Principle P3 and acceptance criterion 1. The rule this pins is the one that
// looks like a nicety and is not: department answers who took the stock, and an
// issue that can only answer "who" cannot be costed to an order, reconciled
// against a backflush, or defended in a variance review.
func TestHasIssueReference(t *testing.T) {
	cases := []struct {
		name string
		in   store.CreateIssueInput
		want bool
	}{
		{"nothing at all", store.CreateIssueInput{}, false},
		{
			"department only — the gap this closes",
			store.CreateIssueInput{Department: ref("Maintenance")},
			false,
		},
		{
			"whitespace is not a reference",
			store.CreateIssueInput{CostCenter: ref("   "), WorkOrderRef: ref("\t")},
			false,
		},
		{
			"empty strings are not a reference",
			store.CreateIssueInput{CostCenter: ref(""), ProductionOrderRef: ref("")},
			false,
		},
		{"cost centre", store.CreateIssueInput{CostCenter: ref("CC-100")}, true},
		{"production order", store.CreateIssueInput{ProductionOrderRef: ref("PO-77")}, true},
		{
			// iag-fleet's work-order completion path sends exactly this shape.
			"work order — how fleet issues parts",
			store.CreateIssueInput{Department: ref("Fleet"), WorkOrderRef: ref("WO-9")},
			true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasIssueReference(tc.in); got != tc.want {
				t.Errorf("hasIssueReference() = %v, want %v", got, tc.want)
			}
		})
	}
}

// The refusal has to be actionable. A caller reading it is one field away from
// a valid request and should not need the schema to work out which field.
func TestRefusalNamesEveryAcceptedField(t *testing.T) {
	for _, field := range []string{"cost_center", "production_order_ref", "work_order_ref"} {
		if !contains(errNoIssueReference, field) {
			t.Errorf("refusal does not name %q: %s", field, errNoIssueReference)
		}
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
