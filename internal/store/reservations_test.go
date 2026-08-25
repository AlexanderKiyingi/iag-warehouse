package store

import (
	"testing"

	"github.com/google/uuid"
)

// The reservations read model is a five-table join over pick lines, and the
// pack-session update leans on COALESCE to tell "leave this alone" apart from
// "set it to empty". Neither is expressible as a unit test with the database
// stubbed out, and both are the kind of SQL that compiles fine and returns the
// wrong rows.

func TestListReservationsProjectsOpenPickLines(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)
	f.seed(t, s, ctx, f.bulkBin.Code, 100, 5)

	orderRef := "SO-" + f.suffix
	pl, err := s.CreatePickList(ctx, CreatePickListInput{
		OrderRef: strp(orderRef),
		Lines: []PickLineInput{
			{ItemID: f.item.ID, Qty: 30, BinCode: f.bulkBin.Code},
		},
	})
	if err != nil {
		t.Fatalf("create pick list: %v", err)
	}

	rows, err := s.ListReservations(ctx, "", orderRef, nil, "", 0)
	if err != nil {
		t.Fatalf("list reservations: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 reservation for %s, got %d", orderRef, len(rows))
	}

	r := rows[0]
	if r.PickListID != pl.ID {
		t.Errorf("pick_list_id = %s, want %s", r.PickListID, pl.ID)
	}
	if r.OrderRef == nil || *r.OrderRef != orderRef {
		t.Errorf("order_ref = %v, want %s", r.OrderRef, orderRef)
	}
	if r.Status != "open" {
		t.Errorf("status = %q, want open", r.Status)
	}
	// The join must resolve the item, its bin and the owning facility. A wrong
	// join key here still returns rows — just the wrong ones — so assert the
	// values, not the count.
	if r.ItemID != f.item.ID {
		t.Errorf("item_id = %s, want %s", r.ItemID, f.item.ID)
	}
	if r.SKU != f.item.SKU {
		t.Errorf("sku = %q, want %q", r.SKU, f.item.SKU)
	}
	if r.BinCode != f.bulkBin.Code {
		t.Errorf("bin_code = %q, want %q", r.BinCode, f.bulkBin.Code)
	}
	if r.FacilityCode != f.facility.Code {
		t.Errorf("facility_code = %q, want %q", r.FacilityCode, f.facility.Code)
	}
	if r.Qty != 30 {
		t.Errorf("qty = %v, want 30", r.Qty)
	}
	// Nothing picked yet, so the whole line is still held.
	if r.ReservedQty != 30 {
		t.Errorf("reserved_qty = %v, want 30", r.ReservedQty)
	}
}

func TestListReservationsReportsUnpickedRemainder(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)
	f.seed(t, s, ctx, f.bulkBin.Code, 100, 5)

	orderRef := "SO-PART-" + f.suffix
	pl, err := s.CreatePickList(ctx, CreatePickListInput{
		OrderRef: strp(orderRef),
		Lines:    []PickLineInput{{ItemID: f.item.ID, Qty: 40, BinCode: f.bulkBin.Code}},
	})
	if err != nil {
		t.Fatalf("create pick list: %v", err)
	}

	// Record a partial pick straight on the line: the picker found 15 of 40.
	if _, err := s.pool.Exec(ctx,
		`UPDATE wh_pick_lines SET picked_qty = 15 WHERE pick_list_id = $1`, pl.ID); err != nil {
		t.Fatalf("set picked_qty: %v", err)
	}

	rows, err := s.ListReservations(ctx, "", orderRef, nil, "", 0)
	if err != nil {
		t.Fatalf("list reservations: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	// This is the number a planner acts on. Reporting qty (40) would overstate
	// the claim on free stock by everything already taken.
	if rows[0].ReservedQty != 25 {
		t.Errorf("reserved_qty = %v, want 25 (40 claimed − 15 picked)", rows[0].ReservedQty)
	}
	if rows[0].PickedQty != 15 {
		t.Errorf("picked_qty = %v, want 15", rows[0].PickedQty)
	}
}

func TestListReservationsExcludesReleasedAndConsumedPicks(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)
	f.seed(t, s, ctx, f.bulkBin.Code, 200, 5)

	cancelledRef := "SO-CANCEL-" + f.suffix
	cancelled, err := s.CreatePickList(ctx, CreatePickListInput{
		OrderRef: strp(cancelledRef),
		Lines:    []PickLineInput{{ItemID: f.item.ID, Qty: 10, BinCode: f.bulkBin.Code}},
	})
	if err != nil {
		t.Fatalf("create cancelled pick: %v", err)
	}
	if _, err := s.CancelPickList(ctx, cancelled.ID, nil); err != nil {
		t.Fatalf("cancel pick: %v", err)
	}

	confirmedRef := "SO-CONFIRM-" + f.suffix
	confirmed, err := s.CreatePickList(ctx, CreatePickListInput{
		OrderRef: strp(confirmedRef),
		Lines:    []PickLineInput{{ItemID: f.item.ID, Qty: 10, BinCode: f.bulkBin.Code}},
	})
	if err != nil {
		t.Fatalf("create confirmed pick: %v", err)
	}
	if _, err := s.ConfirmPickList(ctx, confirmed.ID, nil); err != nil {
		t.Fatalf("confirm pick: %v", err)
	}

	// A cancelled pick released its reservation and a confirmed one consumed it;
	// neither still holds stock, so neither is an allocation any more.
	for _, ref := range []string{cancelledRef, confirmedRef} {
		rows, err := s.ListReservations(ctx, "", ref, nil, "", 0)
		if err != nil {
			t.Fatalf("list reservations (%s): %v", ref, err)
		}
		if len(rows) != 0 {
			t.Errorf("%s: want 0 open reservations, got %d (status %q)",
				ref, len(rows), rows[0].Status)
		}
	}

	// ...but an explicit status filter still reaches them, for history views.
	rows, err := s.ListReservations(ctx, "cancelled", cancelledRef, nil, "", 0)
	if err != nil {
		t.Fatalf("list cancelled: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("status filter: want 1 cancelled row, got %d", len(rows))
	}
}

func TestListReservationsFiltersByItemAndFacility(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)
	other := newFixture(t, s, ctx)
	f.seed(t, s, ctx, f.bulkBin.Code, 50, 5)
	other.seed(t, s, ctx, other.bulkBin.Code, 50, 5)

	if _, err := s.CreatePickList(ctx, CreatePickListInput{
		OrderRef: strp("SO-A-" + f.suffix),
		Lines:    []PickLineInput{{ItemID: f.item.ID, Qty: 5, BinCode: f.bulkBin.Code}},
	}); err != nil {
		t.Fatalf("pick A: %v", err)
	}
	if _, err := s.CreatePickList(ctx, CreatePickListInput{
		OrderRef: strp("SO-B-" + other.suffix),
		Lines:    []PickLineInput{{ItemID: other.item.ID, Qty: 7, BinCode: other.bulkBin.Code}},
	}); err != nil {
		t.Fatalf("pick B: %v", err)
	}

	byItem, err := s.ListReservations(ctx, "", "", &f.item.ID, "", 0)
	if err != nil {
		t.Fatalf("filter by item: %v", err)
	}
	for _, r := range byItem {
		if r.ItemID != f.item.ID {
			t.Fatalf("item filter leaked %s", r.ItemID)
		}
	}
	if len(byItem) != 1 {
		t.Errorf("item filter: want 1 row, got %d", len(byItem))
	}

	byFacility, err := s.ListReservations(ctx, "", "", nil, other.facility.Code, 0)
	if err != nil {
		t.Fatalf("filter by facility: %v", err)
	}
	for _, r := range byFacility {
		if r.FacilityCode != other.facility.Code {
			t.Fatalf("facility filter leaked %s", r.FacilityCode)
		}
	}
	if len(byFacility) != 1 {
		t.Errorf("facility filter: want 1 row, got %d", len(byFacility))
	}
}

func TestUpdatePackSessionStatusAndAttrs(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)
	f.seed(t, s, ctx, f.bulkBin.Code, 20, 5)

	pl, err := s.CreatePickList(ctx, CreatePickListInput{
		OrderRef: strp("SO-PACK-" + f.suffix),
		Lines:    []PickLineInput{{ItemID: f.item.ID, Qty: 5, BinCode: f.bulkBin.Code}},
	})
	if err != nil {
		t.Fatalf("create pick list: %v", err)
	}
	id, err := s.CreatePackSession(ctx, &pl.ID, nil, map[string]any{
		"packages":  "3",
		"packaging": "carton",
	})
	if err != nil {
		t.Fatalf("create pack session: %v", err)
	}

	// Status-only patch: attrs must survive untouched. Passing nil through
	// attrsOrEmpty instead of attrsOrNil would blank them here, which is the
	// bug this asserts against.
	got, err := s.UpdatePackSession(ctx, id, strp("packed"), nil)
	if err != nil {
		t.Fatalf("status-only update: %v", err)
	}
	if got.Status != "packed" {
		t.Errorf("status = %q, want packed", got.Status)
	}
	if got.Attrs["packaging"] != "carton" {
		t.Errorf("status-only patch wiped attrs: %#v", got.Attrs)
	}

	// Attrs-only patch: status must survive, and attrs are REPLACED, not merged
	// — a merge cannot express removal, so dropping "packaging" must drop it.
	got, err = s.UpdatePackSession(ctx, id, nil, map[string]any{"packages": "4"})
	if err != nil {
		t.Fatalf("attrs-only update: %v", err)
	}
	if got.Status != "packed" {
		t.Errorf("attrs-only patch changed status to %q", got.Status)
	}
	if got.Attrs["packages"] != "4" {
		t.Errorf("packages = %v, want 4", got.Attrs["packages"])
	}
	if _, still := got.Attrs["packaging"]; still {
		t.Errorf("attrs merged instead of replaced — packaging survived: %#v", got.Attrs)
	}

	// It must round-trip through a read, not just through the RETURNING clause.
	reread, err := s.GetPackSession(ctx, id)
	if err != nil {
		t.Fatalf("get pack session: %v", err)
	}
	if reread.Status != "packed" || reread.Attrs["packages"] != "4" {
		t.Errorf("re-read disagrees: status=%q attrs=%#v", reread.Status, reread.Attrs)
	}
}

func TestUpdatePackSessionUnknownIDIsNotFound(t *testing.T) {
	s, ctx := testPool(t)
	if _, err := s.UpdatePackSession(ctx, uuid.New(), strp("packed"), nil); err != ErrNotFound {
		t.Fatalf("want ErrNotFound for an unknown id, got %v", err)
	}
}

func strp(s string) *string { return &s }
