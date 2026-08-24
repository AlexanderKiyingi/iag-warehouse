package store

import (
	"testing"

	"iag-warehouse/backend/internal/models"
)

// The lifecycle rules are a small table, and a small table is exactly the kind
// of thing that gets edited later by somebody reasoning about one status in
// isolation. These pin all five against both actions.
func TestItemStatusAllows(t *testing.T) {
	cases := []struct {
		status          string
		action          string
		allowRestricted bool
		want            bool
		why             string
	}{
		{models.ItemStatusActive, ItemActionIssue, false, true, "active issues"},
		{models.ItemStatusActive, ItemActionReceive, false, true, "active receives"},

		{models.ItemStatusDraft, ItemActionIssue, false, false, "draft never transacts"},
		{models.ItemStatusDraft, ItemActionReceive, false, false, "draft never transacts"},
		{models.ItemStatusDraft, ItemActionIssue, true, false,
			"the override lifts restricted only — a draft item is not yet a real part"},

		{models.ItemStatusBlocked, ItemActionIssue, false, false, "blocked never transacts"},
		{models.ItemStatusBlocked, ItemActionReceive, true, false,
			"a permission that could transact a blocked item would make the block advisory"},

		{models.ItemStatusRestricted, ItemActionIssue, false, false, "restricted needs the override"},
		{models.ItemStatusRestricted, ItemActionIssue, true, true, "restricted yields to the override"},
		{models.ItemStatusRestricted, ItemActionReceive, true, true, "both directions"},

		// The asymmetry that makes `obsolete` useful rather than just another
		// block: stop buying it, keep using up what is on the shelf.
		{models.ItemStatusObsolete, ItemActionReceive, false, false, "obsolete stops purchasing"},
		{models.ItemStatusObsolete, ItemActionReceive, true, false,
			"obsolete is a purchasing decision, not a permission problem"},
		{models.ItemStatusObsolete, ItemActionIssue, false, true, "obsolete stock still runs down"},

		{"something-else", ItemActionIssue, true, false, "an unknown status refuses rather than defaults open"},
	}

	for _, tc := range cases {
		got := itemStatusAllows(tc.status, tc.action, tc.allowRestricted)
		if got != tc.want {
			t.Errorf("itemStatusAllows(%q, %q, override=%v) = %v, want %v — %s",
				tc.status, tc.action, tc.allowRestricted, got, tc.want, tc.why)
		}
	}
}

func TestItemStatusErrorNamesTheItemAndTheReason(t *testing.T) {
	// A refusal that says "not transactable" sends the storekeeper to find a
	// developer. One that names the SKU and the status sends them to a supervisor.
	err := &ItemStatusError{SKU: "SP-1042", Status: models.ItemStatusObsolete, Action: ItemActionReceive}
	msg := err.Error()
	for _, want := range []string{"SP-1042", "obsolete", "issued"} {
		if !contains(msg, want) {
			t.Errorf("refusal %q does not mention %q", msg, want)
		}
	}
}

func TestValidItemStatus(t *testing.T) {
	for _, s := range models.ItemStatuses {
		if !models.ValidItemStatus(s) {
			t.Errorf("%q is in ItemStatuses but fails ValidItemStatus", s)
		}
	}
	for _, s := range []string{"", "Active", "retired", "deleted"} {
		if models.ValidItemStatus(s) {
			t.Errorf("%q accepted as a lifecycle status", s)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

// The flat gate-pass contract speaks three words; the slip model speaks seven
// states. A mapping that quietly returned "released" for an unrecognised word
// would reopen a cancelled pass.
func TestFlatStatusToSlip(t *testing.T) {
	cases := map[string]string{
		"On Loan":   "released",
		"on loan":   "released",
		"":          "released",
		"Returned":  "returned",
		"closed":    "returned",
		"Cancelled": "cancelled",
		"canceled":  "cancelled",
		"void":      "cancelled",
		"gibberish": "", // leave the stored status alone
	}
	for in, want := range cases {
		if got := flatStatusToSlip(in); got != want {
			t.Errorf("flatStatusToSlip(%q) = %q, want %q", in, got, want)
		}
	}
}
