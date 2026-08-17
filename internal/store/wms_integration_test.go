package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"iag-warehouse/backend/internal/db"
	"iag-warehouse/backend/internal/migrate"
	"iag-warehouse/backend/internal/models"
)

// End-to-end tests for the execution layer against a real Postgres.
//
// These run only when WAREHOUSE_TEST_DATABASE_URL points at a scratch database,
// so `go test ./...` stays green on a machine with no server. They are worth the
// setup: almost everything added here is enforced by SQL — partial unique
// indexes, CHECK constraints, FOR UPDATE ordering, a recursive CTE — and none of
// that is exercised by a unit test that stubs the database out.

// testDSN returns the scratch database to run against, or "" to skip.
//
// Two names are honoured because two arrived independently: WAREHOUSE_TEST_DB
// predates this file, WAREHOUSE_TEST_DATABASE_URL came with it. Rather than
// leave CI having to set both — and someone eventually setting only one and
// silently skipping half the database tests — every DB-backed test in this
// package resolves through here.
func testDSN() string {
	if v := os.Getenv("WAREHOUSE_TEST_DB"); v != "" {
		return v
	}
	return os.Getenv("WAREHOUSE_TEST_DATABASE_URL")
}

func testPool(t *testing.T) (*Store, context.Context) {
	t.Helper()
	url := testDSN()
	if url == "" {
		t.Skip("set WAREHOUSE_TEST_DB (or WAREHOUSE_TEST_DATABASE_URL) to run database tests")
	}
	ctx := context.Background()
	pool, err := db.NewPool(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.Up(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := New(pool)
	s.SetCosting(true, "UGX")
	return s, ctx
}

// fixture builds an isolated facility/zone/bin/item set. Codes are suffixed with
// a fresh UUID so repeated runs against the same scratch database never collide.
type fixture struct {
	facility models.Facility
	bulk     models.Zone
	pickZone models.Zone
	bulkBin  models.Bin
	pickBin  models.Bin
	item     models.Item
	suffix   string
}

func newFixture(t *testing.T, s *Store, ctx context.Context) fixture {
	t.Helper()
	sfx := uuid.NewString()[:8]
	f, err := s.CreateFacility(ctx, "FAC-"+sfx, "Test Mill "+sfx, "mill", nil)
	if err != nil {
		t.Fatalf("facility: %v", err)
	}
	bulk, err := s.CreateZone(ctx, f.Code, "ZB-"+sfx, "Bulk", "bulk", nil)
	if err != nil {
		t.Fatalf("bulk zone: %v", err)
	}
	pick, err := s.CreateZone(ctx, f.Code, "ZP-"+sfx, "Staging", "staging", nil)
	if err != nil {
		t.Fatalf("pick zone: %v", err)
	}
	bulkBin, err := s.CreateBin(ctx, bulk.Code, "BULK-"+sfx, nil, nil, nil)
	if err != nil {
		t.Fatalf("bulk bin: %v", err)
	}
	pickBin, err := s.CreateBin(ctx, pick.Code, "PICK-"+sfx, nil, nil, nil)
	if err != nil {
		t.Fatalf("pick bin: %v", err)
	}
	item, err := s.CreateItem(ctx, "SKU-"+sfx, "Test Coffee", "raw_material", "bulk", "kg", 0, nil, nil)
	if err != nil {
		t.Fatalf("item: %v", err)
	}
	return fixture{facility: f, bulk: bulk, pickZone: pick, bulkBin: bulkBin, pickBin: pickBin, item: item, suffix: sfx}
}

func (f fixture) seed(t *testing.T, s *Store, ctx context.Context, binCode string, qty, unitCost float64) {
	t.Helper()
	r, err := s.CreateReceipt(ctx, CreateReceiptInput{
		ReceiptType: "standard",
		Lines: []ReceiptLineInput{{
			ItemID: f.item.ID, Qty: qty, UOM: "kg", BinCode: binCode, UnitCost: unitCost,
		}},
	})
	if err != nil {
		t.Fatalf("seed receipt: %v", err)
	}
	if _, err := s.PostReceipt(ctx, r.ID, nil); err != nil {
		t.Fatalf("seed post: %v", err)
	}
}

func binQty(t *testing.T, s *Store, ctx context.Context, itemID uuid.UUID, binCode string) (qty, reserved float64) {
	t.Helper()
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(sb.qty), 0), COALESCE(SUM(sb.reserved), 0)
		FROM wh_stock_balances sb JOIN wh_bins b ON b.id = sb.bin_id
		WHERE sb.item_id = $1 AND b.code = $2`, itemID, binCode).Scan(&qty, &reserved)
	if err != nil {
		t.Fatalf("balance read: %v", err)
	}
	return qty, reserved
}

// TestUOMConversionRebasesQtyAndCost is the load-bearing claim of migration 018:
// a line entered in bags lands in stock as kilos, and its purchase price lands
// as a price per kilo. Getting the second half wrong would misvalue inventory
// silently, which is worse than getting the first half wrong.
func TestUOMConversionRebasesQtyAndCost(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)

	if _, err := s.UpsertItemUOM(ctx, f.item.ID, ItemUOMInput{UOM: "bag", Factor: 60}); err != nil {
		t.Fatalf("define bag: %v", err)
	}

	r, err := s.CreateReceipt(ctx, CreateReceiptInput{
		ReceiptType: "standard",
		Lines: []ReceiptLineInput{{
			ItemID: f.item.ID, Qty: 5, UOM: "bag", BinCode: f.bulkBin.Code, UnitCost: 600_000,
		}},
	})
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	line := r.Lines[0]
	if line.Qty != 300 {
		t.Errorf("qty in base units = %v, want 300", line.Qty)
	}
	if line.EnteredQty != 5 || line.EnteredUOM != "bag" || line.UOMFactor != 60 {
		t.Errorf("entered figures not preserved: %v %v x%v", line.EnteredQty, line.EnteredUOM, line.UOMFactor)
	}

	if _, err := s.PostReceipt(ctx, r.ID, nil); err != nil {
		t.Fatalf("post: %v", err)
	}
	qty, _ := binQty(t, s, ctx, f.item.ID, f.bulkBin.Code)
	if qty != 300 {
		t.Errorf("on hand = %v, want 300 kg", qty)
	}

	var avg float64
	if err := s.pool.QueryRow(ctx, `SELECT avg_cost FROM wh_items WHERE id = $1`, f.item.ID).Scan(&avg); err != nil {
		t.Fatalf("avg cost: %v", err)
	}
	// 600,000 per 60kg bag is 10,000 per kg.
	if avg != 10_000 {
		t.Errorf("avg cost = %v, want 10000 per kg", avg)
	}
}

// TestUOMUnknownUnitRejectedOnlyOnceDeclared pins the lenient/strict split: an
// item with no declared alternates keeps the pre-conversion behaviour, and one
// with alternates refuses a unit it does not know.
func TestUOMUnknownUnitRejectedOnlyOnceDeclared(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)

	conv, err := s.ConvertToBase(ctx, f.item.ID, 7, "crate")
	if err != nil {
		t.Fatalf("undeclared item should accept any unit: %v", err)
	}
	if conv.BaseQty != 7 || conv.Factor != 1 {
		t.Errorf("expected passthrough at factor 1, got %v x%v", conv.BaseQty, conv.Factor)
	}

	if _, err := s.UpsertItemUOM(ctx, f.item.ID, ItemUOMInput{UOM: "bag", Factor: 60}); err != nil {
		t.Fatalf("define bag: %v", err)
	}
	if _, err := s.ConvertToBase(ctx, f.item.ID, 7, "crate"); err == nil {
		t.Error("expected an unknown unit to be rejected once alternates are declared")
	}
}

// TestDirectedPutawayPrefersHigherPriorityRule checks the ordering that makes a
// rule set usable at all: a specific rule in front of a catch-all.
func TestDirectedPutawayPrefersHigherPriorityRule(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)

	if _, err := s.CreatePutawayRule(ctx, PutawayRuleInput{
		Name: "catch-all " + f.suffix, Priority: 900, Active: true,
		FacilityID: &f.facility.ID, TargetZoneID: &f.bulk.ID, Strategy: models.PutawayLeastUsed,
	}); err != nil {
		t.Fatalf("catch-all rule: %v", err)
	}
	if _, err := s.CreatePutawayRule(ctx, PutawayRuleInput{
		Name: "home slot " + f.suffix, Priority: 10, Active: true,
		ItemID: &f.item.ID, TargetBinID: &f.pickBin.ID, Strategy: models.PutawayFixedBin,
	}); err != nil {
		t.Fatalf("fixed rule: %v", err)
	}

	sug, err := s.ResolvePutawayBin(ctx, PutawayRequest{ItemID: f.item.ID, Qty: 10, FacilityID: &f.facility.ID})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if sug.BinCode != f.pickBin.Code {
		t.Errorf("directed to %s, want the fixed home slot %s", sug.BinCode, f.pickBin.Code)
	}
	if sug.Strategy != models.PutawayFixedBin {
		t.Errorf("strategy = %s, want fixed_bin", sug.Strategy)
	}
}

// TestDirectedPutawayRefusesToOverfillABin is the guarantee that capacity is
// enforced for every strategy rather than only for capacity_fit.
func TestDirectedPutawayRefusesToOverfillABin(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)

	capacity := 100.0
	small, err := s.CreateBin(ctx, f.bulk.Code, "SMALL-"+f.suffix, &capacity, nil, nil)
	if err != nil {
		t.Fatalf("small bin: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE wh_items SET weight_kg = 1 WHERE id = $1`, f.item.ID); err != nil {
		t.Fatalf("set weight: %v", err)
	}
	if _, err := s.CreatePutawayRule(ctx, PutawayRuleInput{
		Name: "small only " + f.suffix, Priority: 5, Active: true,
		ItemID: &f.item.ID, TargetBinID: &small.ID, Strategy: models.PutawayFixedBin,
	}); err != nil {
		t.Fatalf("rule: %v", err)
	}

	if _, err := s.ResolvePutawayBin(ctx, PutawayRequest{ItemID: f.item.ID, Qty: 80, FacilityID: &f.facility.ID}); err != nil {
		t.Fatalf("80kg should fit in a 100kg bin: %v", err)
	}
	if _, err := s.ResolvePutawayBin(ctx, PutawayRequest{ItemID: f.item.ID, Qty: 140, FacilityID: &f.facility.ID}); err == nil {
		t.Error("expected 140kg to be refused by a 100kg bin")
	}
}

// TestReplenishmentReservesAndMoves covers the whole loop, including the detail
// that matters most: an open task holds its source stock so a picker cannot take
// it, and completing releases exactly what it moved.
func TestReplenishmentReservesAndMoves(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)
	f.seed(t, s, ctx, f.bulkBin.Code, 500, 0)
	f.seed(t, s, ctx, f.pickBin.Code, 10, 0)

	if _, err := s.UpsertReplenLevel(ctx, ReplenLevelInput{
		ItemID: f.item.ID, BinID: f.pickBin.ID, MinQty: 50, MaxQty: 200, Active: true,
	}); err != nil {
		t.Fatalf("level: %v", err)
	}

	tasks, err := s.GenerateReplenTasks(ctx, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("generated %d tasks, want 1", len(tasks))
	}
	task := tasks[0]
	if task.Qty != 190 { // top up 10 -> 200
		t.Errorf("task qty = %v, want 190", task.Qty)
	}

	_, reserved := binQty(t, s, ctx, f.item.ID, f.bulkBin.Code)
	if reserved != 190 {
		t.Errorf("source reserved = %v, want the task to hold 190", reserved)
	}

	// A second sweep must not duplicate an instruction that is already open.
	again, err := s.GenerateReplenTasks(ctx, nil)
	if err != nil {
		t.Fatalf("second generate: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("second sweep raised %d duplicate tasks, want 0", len(again))
	}

	// Complete short: only 150 was actually there to carry.
	moved := 150.0
	if _, err := s.CompleteReplenTask(ctx, task.ID, &moved, nil); err != nil {
		t.Fatalf("complete: %v", err)
	}
	srcQty, srcReserved := binQty(t, s, ctx, f.item.ID, f.bulkBin.Code)
	if srcQty != 350 {
		t.Errorf("source qty = %v, want 350", srcQty)
	}
	if srcReserved != 0 {
		t.Errorf("source still reserving %v after a short move — the shortfall was not released", srcReserved)
	}
	dstQty, _ := binQty(t, s, ctx, f.item.ID, f.pickBin.Code)
	if dstQty != 160 {
		t.Errorf("destination qty = %v, want 160", dstQty)
	}
}

// TestCountWorkflowEnforcesSeparateApprover is the control the whole count
// module exists for.
func TestCountWorkflowEnforcesSeparateApprover(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)
	f.seed(t, s, ctx, f.bulkBin.Code, 100, 5)

	counter := uuid.New()
	approver := uuid.New()

	task, err := s.CreateCountTask(ctx, CountTaskInput{
		ScopeType: models.CountScopeBin, ScopeRef: f.bulkBin.Code, Blind: true, CreatedBy: &counter,
	})
	if err != nil {
		t.Fatalf("create count: %v", err)
	}
	if len(task.Lines) != 1 {
		t.Fatalf("snapshot produced %d lines, want 1", len(task.Lines))
	}
	if task.Lines[0].SystemQty != nil {
		t.Error("a blind count must not disclose the system quantity while counting")
	}

	line := task.Lines[0]
	if _, err := s.RecordCount(ctx, task.ID, line.ID, 94, nil, &counter); err != nil {
		t.Fatalf("record count: %v", err)
	}
	if _, err := s.SubmitCountTask(ctx, task.ID, &counter); err != nil {
		t.Fatalf("submit: %v", err)
	}

	// Outside tolerance (none was set), so it needs an explicit verdict.
	if _, err := s.ApproveCountTask(ctx, task.ID, &approver, true); err == nil {
		t.Error("expected approval to be blocked while a variance is unresolved")
	}
	if _, err := s.SetCountLineStatus(ctx, task.ID, line.ID, models.CountLineAccepted, nil); err != nil {
		t.Fatalf("accept line: %v", err)
	}

	if _, err := s.ApproveCountTask(ctx, task.ID, &counter, true); err == nil {
		t.Error("expected the counter to be refused as their own approver")
	}
	approved, err := s.ApproveCountTask(ctx, task.ID, &approver, true)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.Status != models.CountApproved {
		t.Errorf("status = %s, want approved", approved.Status)
	}

	qty, _ := binQty(t, s, ctx, f.item.ID, f.bulkBin.Code)
	if qty != 94 {
		t.Errorf("on hand = %v, want the counted 94", qty)
	}
	// Variance is valued at moving-average cost: 6 kg short at 5 each.
	if approved.VarianceValue != -30 {
		t.Errorf("variance value = %v, want -30", approved.VarianceValue)
	}
}

// TestHandlingUnitCannotOverclaimBinStock pins the one invariant the licence-plate
// design depends on: two plates in a bin cannot both claim the same sack.
func TestHandlingUnitCannotOverclaimBinStock(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)
	f.seed(t, s, ctx, f.bulkBin.Code, 100, 0)

	huA, err := s.CreateHandlingUnit(ctx, CreateHUInput{BinCode: f.bulkBin.Code})
	if err != nil {
		t.Fatalf("hu A: %v", err)
	}
	huB, err := s.CreateHandlingUnit(ctx, CreateHUInput{BinCode: f.bulkBin.Code})
	if err != nil {
		t.Fatalf("hu B: %v", err)
	}
	if _, err := s.AddToHandlingUnit(ctx, huA.ID, f.item.ID, "", "", 80); err != nil {
		t.Fatalf("load A: %v", err)
	}
	if _, err := s.AddToHandlingUnit(ctx, huB.ID, f.item.ID, "", "", 40); err == nil {
		t.Error("expected the second plate to be refused: only 20 kg is uncontainerised")
	}
	if _, err := s.AddToHandlingUnit(ctx, huB.ID, f.item.ID, "", "", 20); err != nil {
		t.Fatalf("the remaining 20 kg should load: %v", err)
	}
}

// TestHandlingUnitMoveRelocatesContents checks that moving one plate moves
// everything on it, as a real stock movement.
func TestHandlingUnitMoveRelocatesContents(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)
	f.seed(t, s, ctx, f.bulkBin.Code, 100, 0)

	hu, err := s.CreateHandlingUnit(ctx, CreateHUInput{BinCode: f.bulkBin.Code})
	if err != nil {
		t.Fatalf("hu: %v", err)
	}
	if _, err := s.AddToHandlingUnit(ctx, hu.ID, f.item.ID, "", "", 60); err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := s.MoveHandlingUnit(ctx, hu.ID, f.pickBin.Code, nil); err != nil {
		t.Fatalf("move: %v", err)
	}

	src, _ := binQty(t, s, ctx, f.item.ID, f.bulkBin.Code)
	dst, _ := binQty(t, s, ctx, f.item.ID, f.pickBin.Code)
	if src != 40 || dst != 60 {
		t.Errorf("after moving the plate: source %v (want 40), destination %v (want 60)", src, dst)
	}
}

// TestShortPickReleasesTheShortfall is the reservation-hygiene case: stock the
// picker could not find must go back to free, not stay held by a closed list.
func TestShortPickReleasesTheShortfall(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)
	f.seed(t, s, ctx, f.bulkBin.Code, 100, 0)

	pl, err := s.CreatePickList(ctx, CreatePickListInput{
		Lines: []PickLineInput{{ItemID: f.item.ID, Qty: 40, BinCode: f.bulkBin.Code}},
	})
	if err != nil {
		t.Fatalf("pick list: %v", err)
	}
	if _, reserved := binQty(t, s, ctx, f.item.ID, f.bulkBin.Code); reserved != 40 {
		t.Fatalf("reserved = %v, want 40 held by the open list", reserved)
	}

	if _, err := s.RecordPick(ctx, pl.ID, pl.Lines[0].ID, 25, "only 25 on the shelf", nil); err != nil {
		t.Fatalf("record pick: %v", err)
	}
	if _, err := s.ConfirmPickList(ctx, pl.ID, nil); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	qty, reserved := binQty(t, s, ctx, f.item.ID, f.bulkBin.Code)
	if qty != 75 {
		t.Errorf("on hand = %v, want 75 (only 25 picked)", qty)
	}
	if reserved != 0 {
		t.Errorf("reserved = %v after confirm, want 0 — the 15 short was not released", reserved)
	}
}

// TestShortPickNeedsAReason mirrors the platform's decline-with-reason rule.
func TestShortPickNeedsAReason(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)
	f.seed(t, s, ctx, f.bulkBin.Code, 100, 0)

	pl, err := s.CreatePickList(ctx, CreatePickListInput{
		Lines: []PickLineInput{{ItemID: f.item.ID, Qty: 40, BinCode: f.bulkBin.Code}},
	})
	if err != nil {
		t.Fatalf("pick list: %v", err)
	}
	if _, err := s.RecordPick(ctx, pl.ID, pl.Lines[0].ID, 25, "", nil); err == nil {
		t.Error("expected a short pick with no reason to be refused")
	}
}

// TestScanResolvesNaturalKeys covers the day-one behaviour: scanning works
// before anybody has registered a label.
func TestScanResolvesNaturalKeys(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)
	f.seed(t, s, ctx, f.bulkBin.Code, 100, 0)

	res, err := s.ResolveBarcode(ctx, f.bulkBin.Code)
	if err != nil {
		t.Fatalf("resolve bin: %v", err)
	}
	if res.EntityType != models.BarcodeBin || res.Bin == nil {
		t.Fatalf("expected a bin, got %q", res.EntityType)
	}
	if len(res.Balances) == 0 {
		t.Error("expected the bin's stock to come back with the scan")
	}

	res, err = s.ResolveBarcode(ctx, f.item.SKU)
	if err != nil {
		t.Fatalf("resolve sku: %v", err)
	}
	if res.EntityType != models.BarcodeItem || res.Item == nil {
		t.Fatalf("expected an item, got %q", res.EntityType)
	}
}

// TestBarcodeScanCarriesCaseQuantity checks that a case label credits a case.
func TestBarcodeScanCarriesCaseQuantity(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)

	if _, err := s.UpsertItemUOM(ctx, f.item.ID, ItemUOMInput{UOM: "bag", Factor: 60}); err != nil {
		t.Fatalf("define bag: %v", err)
	}
	code := "GTIN-" + f.suffix
	if _, err := s.CreateBarcode(ctx, BarcodeInput{
		Barcode: code, EntityType: models.BarcodeItem, EntityID: &f.item.ID, UOM: "bag",
	}); err != nil {
		t.Fatalf("register barcode: %v", err)
	}
	res, err := s.ResolveBarcode(ctx, code)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.QtyPerScan != 60 {
		t.Errorf("qty per scan = %v, want 60 kg for one bag label", res.QtyPerScan)
	}
}

// TestSlipLifecycle walks a cargo gate pass from draft to released, including
// the separate-authoriser rule and the fact that a draft is worthless at the
// gate.
func TestSlipLifecycle(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)

	storeman := uuid.New()
	manager := uuid.New()
	guard := uuid.New()

	slip, err := s.CreateSlip(ctx, CreateSlipInput{
		SlipType:     models.SlipCargoGatePass,
		Purpose:      "Delivery to buyer",
		DriverName:   "J. Okello",
		VehicleReg:   "UAX 123K",
		Destination:  "Kampala",
		FacilityID:   &f.facility.ID,
		IssuedToName: "Buyer Ltd",
		RequestedBy:  &storeman,
		CreatedBy:    &storeman,
		Lines: []SlipLineInput{{
			ItemID: &f.item.ID, Description: "Green coffee", Qty: 300, UOM: "kg",
		}},
	})
	if err != nil {
		t.Fatalf("create slip: %v", err)
	}
	if slip.SlipNo != nil || slip.VerifyToken != nil {
		t.Error("a draft must have neither a number nor a gate token")
	}

	if _, err := s.AuthorizeSlip(ctx, slip.ID, &storeman, "Storeman", true); err == nil {
		t.Error("expected the raiser to be refused as their own authoriser")
	}
	issued, err := s.AuthorizeSlip(ctx, slip.ID, &manager, "Manager", true)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if issued.SlipNo == nil || issued.VerifyToken == nil {
		t.Fatal("an authorised slip needs a number and a gate token")
	}
	if !strings.HasPrefix(*issued.SlipNo, "GP-") {
		t.Errorf("slip number %q should be in the gate-pass series", *issued.SlipNo)
	}

	verified, err := s.VerifySlipToken(ctx, *issued.VerifyToken)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if verified.ID != slip.ID {
		t.Error("the token resolved to the wrong slip")
	}

	if _, err := s.ReleaseSlip(ctx, slip.ID, "Main gate", "", &guard, "Guard"); err != nil {
		t.Fatalf("release: %v", err)
	}
	// The same paper must not pass twice.
	released, err := s.GetSlip(ctx, slip.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if released.Status != models.SlipReleased {
		t.Errorf("status = %s, want released", released.Status)
	}
	if len(released.Events) < 3 {
		t.Errorf("expected raised/authorised/released in the history, got %d events", len(released.Events))
	}
}

// TestReturnableSlipStaysOutstandingUntilEverythingIsBack is the tracking
// behaviour the whole returnable flag exists for.
func TestReturnableSlipStaysOutstandingUntilEverythingIsBack(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)

	raiser := uuid.New()
	manager := uuid.New()
	yesterday := time.Now().AddDate(0, 0, -1)

	slip, err := s.CreateSlip(ctx, CreateSlipInput{
		SlipType:     models.SlipEquipmentHandover,
		Purpose:      "Site works",
		FacilityID:   &f.facility.ID,
		IssuedToName: "Site team",
		Returnable:   true,
		ReturnBy:     &yesterday,
		CreatedBy:    &raiser,
		Lines: []SlipLineInput{
			{Description: "Angle grinder", Qty: 1, UOM: "ea"},
			{Description: "Extension reel", Qty: 2, UOM: "ea"},
		},
	})
	if err != nil {
		t.Fatalf("create slip: %v", err)
	}
	issued, err := s.AuthorizeSlip(ctx, slip.ID, &manager, "Manager", true)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if !strings.HasPrefix(*issued.SlipNo, "EH-") {
		t.Errorf("slip number %q should be in the handover series", *issued.SlipNo)
	}

	overdue, err := s.ListOverdueSlips(ctx)
	if err != nil {
		t.Fatalf("overdue: %v", err)
	}
	if !containsSlip(overdue, slip.ID) {
		t.Error("a returnable slip past its date should show as overdue")
	}

	// Only the grinder comes back.
	partial, err := s.ReturnSlip(ctx, slip.ID, []SlipReturnLine{
		{LineID: issued.Lines[0].ID, ReturnedQty: issued.Lines[0].Qty, ConditionIn: "good"},
	}, "", nil, "Storeman")
	if err != nil {
		t.Fatalf("partial return: %v", err)
	}
	if partial.Status == models.SlipReturned {
		t.Error("a slip with an outstanding line must not be marked returned")
	}

	full, err := s.ReturnSlip(ctx, slip.ID, []SlipReturnLine{
		{LineID: issued.Lines[1].ID, ReturnedQty: issued.Lines[1].Qty, ConditionIn: "good"},
	}, "all back", nil, "Storeman")
	if err != nil {
		t.Fatalf("full return: %v", err)
	}
	if full.Status != models.SlipReturned {
		t.Errorf("status = %s, want returned once everything is back", full.Status)
	}
}

func containsSlip(list []models.Slip, id uuid.UUID) bool {
	for _, s := range list {
		if s.ID == id {
			return true
		}
	}
	return false
}

// TestNegativeAdjustmentOnExistingPosition is a regression test for a bug that
// predates the execution layer: adjustBalanceTx used INSERT ... ON CONFLICT DO
// UPDATE, and Postgres checks constraints against the proposed insert tuple
// before resolving the conflict, so the `qty >= 0` CHECK added in migration 005
// rejected every downward adjustment even when the resulting balance was
// positive. Shrinkage, damage write-offs and count corrections all 500'd.
func TestNegativeAdjustmentOnExistingPosition(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)
	f.seed(t, s, ctx, f.bulkBin.Code, 100, 0)

	reason := "damaged in handling"
	if _, err := s.CreateAdjustment(ctx, AdjustmentInput{
		ItemID: f.item.ID, BinCode: f.bulkBin.Code, QtyAfter: 88, Reason: &reason,
	}); err != nil {
		t.Fatalf("downward adjustment: %v", err)
	}
	if qty, _ := binQty(t, s, ctx, f.item.ID, f.bulkBin.Code); qty != 88 {
		t.Errorf("on hand = %v, want 88", qty)
	}

	// Taking a position below zero is still refused, and as a clean sentinel
	// rather than a raw constraint error.
	if _, err := s.CreateAdjustment(ctx, AdjustmentInput{
		ItemID: f.item.ID, BinCode: f.bulkBin.Code, QtyAfter: -5, Reason: &reason,
	}); !errors.Is(err, ErrInsufficientStock) {
		t.Errorf("negative balance error = %v, want ErrInsufficientStock", err)
	}
}

// TestMovementAttrsDefaulted is a regression test for the other latent bug: no
// caller sets movementInput.Attrs, pgx encodes a nil map as SQL NULL, and
// wh_movements.attrs is NOT NULL — so posting anything at all failed.
func TestMovementAttrsDefaulted(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)
	f.seed(t, s, ctx, f.bulkBin.Code, 25, 0)

	var nullAttrs int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM wh_movements WHERE item_id = $1 AND attrs IS NULL`, f.item.ID).Scan(&nullAttrs); err != nil {
		t.Fatalf("attrs read: %v", err)
	}
	if nullAttrs != 0 {
		t.Errorf("%d movements stored a null attrs", nullAttrs)
	}
}
