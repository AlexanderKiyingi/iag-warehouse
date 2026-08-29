package store

import (
	"errors"
	"testing"
)

// The bulk stock summary is what makes an item list able to show stock.
//
// The per-item balances endpoint has always existed and is the wrong shape for
// a list: filling one column for a hundred items costs a hundred calls, so in
// practice clients left the column blank and an inventory system displayed no
// inventory. The two claims worth pinning are that the numbers aggregate across
// bins, and that an item nobody has ever stocked still appears — at zero rather
// than missing, because a summary that quietly omits rows disagrees with the
// item list it is joined onto.
func TestListStockSummary(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)

	// Same item in two bins: the summary must add them up, not report either.
	f.seed(t, s, ctx, f.bulkBin.Code, 30, 4)
	f.seed(t, s, ctx, f.pickBin.Code, 12, 4)

	// A second item, catalogued and never stocked.
	empty, err := s.CreateItem(ctx, "SKU-EMPTY-"+f.suffix, "Never Stocked", "consumable", "bulk", "kg", 0, nil, nil)
	if err != nil {
		t.Fatalf("empty item: %v", err)
	}

	rows, err := s.ListStockSummary(ctx, "")
	if err != nil {
		t.Fatalf("summary: %v", err)
	}

	bySKU := map[string]int{}
	for i, row := range rows {
		bySKU[row.SKU] = i
	}

	i, ok := bySKU[f.item.SKU]
	if !ok {
		t.Fatalf("stocked item %s missing from summary", f.item.SKU)
	}
	if got := rows[i].Qty; got != 42 {
		t.Errorf("qty across two bins = %v, want 42", got)
	}
	if got := rows[i].BinCount; got != 2 {
		t.Errorf("bin_count = %v, want 2", got)
	}
	if got := rows[i].Available; got != 42 {
		t.Errorf("available with nothing reserved = %v, want 42", got)
	}
	if rows[i].UpdatedAt == nil {
		t.Error("stocked item reported no last-movement time")
	}

	j, ok := bySKU[empty.SKU]
	if !ok {
		t.Fatal("an item with no stock dropped out of the summary — the list it " +
			"joins onto would show it with no answer at all")
	}
	if got := rows[j].Qty; got != 0 {
		t.Errorf("never-stocked qty = %v, want 0", got)
	}
	if rows[j].UpdatedAt != nil {
		t.Error("never-stocked item reported a last-movement time; it has had no movement")
	}
}

// Filtering by facility must not turn "no stock here" into "no such item".
func TestListStockSummaryByFacility(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)
	other := newFixture(t, s, ctx)

	f.seed(t, s, ctx, f.bulkBin.Code, 25, 3)

	rows, err := s.ListStockSummary(ctx, other.facility.Code)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}

	var found bool
	for _, row := range rows {
		if row.SKU != f.item.SKU {
			continue
		}
		found = true
		if row.Qty != 0 {
			t.Errorf("qty at a facility holding none = %v, want 0", row.Qty)
		}
	}
	if !found {
		t.Error("an item stocked elsewhere vanished from a facility-filtered summary")
	}
}

// A caller that knows the site but not the bin still has to be able to write.
//
// Requiring a bin code is what kept receiving, transferring and adjusting
// unreachable from any screen whose location field says "Warehouse / location".
func TestDefaultBinCodeForFacility(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)

	code, err := s.DefaultBinCodeForFacility(ctx, f.facility.Code)
	if err != nil {
		t.Fatalf("default bin: %v", err)
	}
	if code != f.bulkBin.Code && code != f.pickBin.Code {
		t.Fatalf("default bin %q belongs to neither of the facility's bins", code)
	}

	if _, err := s.DefaultBinCodeForFacility(ctx, "NO-SUCH-FACILITY"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown facility gave %v, want ErrNotFound so the handler can "+
			"answer with a 400 naming the code", err)
	}
}

// A receiving zone wins over bulk, because that is where goods physically
// arrive and where directed putaway expects to find them. Landing an
// unaddressed receipt in bulk would record stock as already put away when
// nobody has moved it.
func TestDefaultBinPrefersReceiving(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)

	recv, err := s.CreateZone(ctx, f.facility.Code, "ZR-"+f.suffix, "Receiving", "receiving", nil)
	if err != nil {
		t.Fatalf("receiving zone: %v", err)
	}
	recvBin, err := s.CreateBin(ctx, recv.Code, "RECV-"+f.suffix, nil, nil, nil)
	if err != nil {
		t.Fatalf("receiving bin: %v", err)
	}

	code, err := s.DefaultBinCodeForFacility(ctx, f.facility.Code)
	if err != nil {
		t.Fatalf("default bin: %v", err)
	}
	if code != recvBin.Code {
		t.Errorf("default bin = %q, want the receiving bin %q", code, recvBin.Code)
	}
}

// Facility address and status arrived with migration 033. Before it, both were
// collected by clients, stored by nothing, and read back as a blank address and
// a hard-coded "Active" — so every site looked open whatever its real state.
func TestFacilityAddressAndStatusRoundTrip(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)

	if f.facility.Status != "active" {
		t.Errorf("new facility status = %q, want the default %q", f.facility.Status, "active")
	}

	addr := "Plot 5, Jinja Road"
	closed := "inactive"
	updated, err := s.UpdateFacility(ctx, f.facility.Code, nil, nil, &addr, &closed, nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Address != addr {
		t.Errorf("address = %q, want %q", updated.Address, addr)
	}
	if updated.Status != closed {
		t.Errorf("status = %q, want %q", updated.Status, closed)
	}

	// And it survives a re-read, rather than only living in the UPDATE's echo.
	reread, err := s.GetFacilityByCode(ctx, f.facility.Code)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if reread.Address != addr || reread.Status != closed {
		t.Errorf("after re-read address=%q status=%q, want %q / %q",
			reread.Address, reread.Status, addr, closed)
	}
}
