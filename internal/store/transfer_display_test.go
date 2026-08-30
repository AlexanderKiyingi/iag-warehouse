package store

import "testing"

// A transfer list has to say what was moved and between which sites.
//
// It did not. The list selected the header only, so a client rendering a row
// got two facility UUIDs, no item and no quantity — the lines live on another
// table and were never fetched. Nothing failed; the screen simply could not
// describe its own records, which is why this is pinned against the list query
// rather than against the model.
func TestTransferListCarriesDisplayFields(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)
	f.seed(t, s, ctx, f.bulkBin.Code, 50, 3000)

	notes := "Weekly top-up"
	created, err := s.CreateTransfer(ctx, CreateTransferInput{
		FromFacilityCode: &f.facility.Code,
		ToFacilityCode:   &f.facility.Code,
		Notes:            &notes,
		ReceivedBy:       "A. Nabirye",
		Lines: []TransferLineInput{{
			ItemID: f.item.ID, Qty: 7, FromBinCode: f.bulkBin.Code, ToBinCode: f.pickBin.Code,
		}},
	})
	if err != nil {
		t.Fatalf("create transfer: %v", err)
	}

	list, err := s.ListTransfers(ctx, "", 200)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, tr := range list {
		if tr.ID != created.ID {
			continue
		}
		if tr.FromFacilityCode != f.facility.Code {
			t.Errorf("from_facility_code = %q, want %q — a UUID is not something a storekeeper can read",
				tr.FromFacilityCode, f.facility.Code)
		}
		if tr.ToFacilityCode != f.facility.Code {
			t.Errorf("to_facility_code = %q, want %q", tr.ToFacilityCode, f.facility.Code)
		}
		if tr.ItemSKU != f.item.SKU {
			t.Errorf("item_sku = %q, want %q — the list could not say what was transferred", tr.ItemSKU, f.item.SKU)
		}
		if tr.Qty != 7 {
			t.Errorf("qty = %v, want 7", tr.Qty)
		}
		if tr.LineCount != 1 {
			t.Errorf("line_count = %d, want 1", tr.LineCount)
		}
		if tr.ReceivedBy == nil || *tr.ReceivedBy != "A. Nabirye" {
			t.Errorf("received_by = %v — the field the transfer form has always collected and dropped", tr.ReceivedBy)
		}
		return
	}
	t.Fatal("the transfer is not in the list")
}
