package store

import (
	"encoding/json"
	"strings"
	"testing"
)

// Migration 034 against a real database: a write-off's reason code, expense
// account, declared valuation and evidence notes survive the write and come
// back on the list.
//
// The list query is the one the frontend actually reads, so this asserts on it
// rather than on the INSERT's RETURNING row. A column that persists but is
// missing from the SELECT is indistinguishable from a column that was dropped,
// and that is the exact failure this whole change is fixing.
func TestWriteOffProvenanceRoundTrip(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)
	f.seed(t, s, ctx, f.bulkBin.Code, 100, 4000)

	delta := -12.0
	reason := "Wet damage in transit"
	adj, err := s.CreateAdjustment(ctx, AdjustmentInput{
		ItemID:           f.item.ID,
		BinCode:          f.bulkBin.Code,
		QtyDelta:         &delta,
		Reason:           &reason,
		ReasonCode:       "damage",
		ExpenseAccount:   "5410-Stock losses",
		EvidenceNotes:    "Photos on file; carrier claim CLM-2026-0091",
		DeclaredUnitCost: qty(4000),
	})
	if err != nil {
		t.Fatalf("create adjustment: %v", err)
	}

	// Derived, not sent: 12 kg written off at 4,000 each.
	if adj.Value == nil || *adj.Value != 48000 {
		t.Errorf("declared value = %v, want 48000 derived from unit cost x quantity moved", adj.Value)
	}

	list, err := s.ListAdjustments(ctx, "adjustment", 200)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found bool
	for _, row := range list {
		if row.ID != adj.ID {
			continue
		}
		found = true
		if row.ReasonCode == nil || *row.ReasonCode != "damage" {
			t.Errorf("reason_code = %v, want damage", row.ReasonCode)
		}
		if row.ExpenseAccount == nil || *row.ExpenseAccount != "5410-Stock losses" {
			t.Errorf("expense_account = %v — the required form field that used to be discarded", row.ExpenseAccount)
		}
		if row.UnitCost == nil || *row.UnitCost != 4000 {
			t.Errorf("unit_cost = %v, want 4000", row.UnitCost)
		}
		if row.Value == nil || *row.Value != 48000 {
			t.Errorf("value = %v, want 48000", row.Value)
		}
		if row.EvidenceNotes == nil || *row.EvidenceNotes == "" {
			t.Error("evidence_notes came back empty")
		}
	}
	if !found {
		t.Fatal("the adjustment is not in the list it was written to")
	}
}

// Migration 036: the documents that justify the write-off survive the round
// trip, and only the references do — the bytes belong in iag-dms.
func TestWriteOffAttachmentsRoundTrip(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)
	f.seed(t, s, ctx, f.bulkBin.Code, 60, 2000)

	refs := json.RawMessage(`[{"id":"a1","storageId":"dms-8f2","name":"claim.pdf",` +
		`"mime":"application/pdf","size":48219,"uploadedAt":"2026-08-31T06:00:00Z"}]`)
	delta := -4.0
	reason := "Crushed in transit"
	adj, err := s.CreateAdjustment(ctx, AdjustmentInput{
		ItemID:      f.item.ID,
		BinCode:     f.bulkBin.Code,
		QtyDelta:    &delta,
		Reason:      &reason,
		ReasonCode:  "damage",
		Attachments: refs,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	list, err := s.ListAdjustments(ctx, "adjustment", 200)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, row := range list {
		if row.ID != adj.ID {
			continue
		}
		if len(row.Attachments) == 0 {
			t.Fatal("attachments came back empty — the reference was stored and then not selected")
		}
		var parsed []map[string]any
		if err := json.Unmarshal(row.Attachments, &parsed); err != nil {
			t.Fatalf("attachments are not valid JSON: %v", err)
		}
		if len(parsed) != 1 || parsed[0]["storageId"] != "dms-8f2" {
			t.Errorf("attachments round-tripped as %s", row.Attachments)
		}
		// The whole point of storing references: a row every stock query joins
		// must not carry file payloads.
		if strings.Contains(string(row.Attachments), "data:") {
			t.Error("a data URL reached the database — only references belong here")
		}
		return
	}
	t.Fatal("the adjustment is not in the list")
}

// An adjustment raised without any of it — which is every peer service, and
// count approval — must still write. The columns are nullable for exactly this
// reason and a NOT NULL slipped in later would break callers behaving correctly.
func TestAdjustmentWithoutProvenanceStillWrites(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)
	f.seed(t, s, ctx, f.bulkBin.Code, 40, 1000)

	delta := -3.0
	reason := "Recount"
	adj, err := s.CreateAdjustment(ctx, AdjustmentInput{
		ItemID:   f.item.ID,
		BinCode:  f.bulkBin.Code,
		QtyDelta: &delta,
		Reason:   &reason,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if adj.ReasonCode != nil || adj.ExpenseAccount != nil || adj.UnitCost != nil || adj.Value != nil {
		t.Errorf("provenance invented where none was given: %+v", adj)
	}
}

// The reason codes are a closed set in the database. If the CHECK is ever
// dropped the column stops aggregating, which is the only thing it is for.
func TestUnknownReasonCodeIsRefusedByTheDatabase(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)
	f.seed(t, s, ctx, f.bulkBin.Code, 10, 1000)

	delta := -1.0
	reason := "Test"
	_, err := s.CreateAdjustment(ctx, AdjustmentInput{
		ItemID:     f.item.ID,
		BinCode:    f.bulkBin.Code,
		QtyDelta:   &delta,
		Reason:     &reason,
		ReasonCode: "vaporised",
	})
	if err == nil {
		t.Fatal("a reason code outside the set was accepted; the CHECK is not doing its job")
	}
}
