package store

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"iag-warehouse/backend/internal/models"
)

// These prove the W1 controls against a real database rather than against the
// structs that describe them. Reading the code tells you a CHECK constraint was
// written; only the database tells you it fires. Skips without
// WAREHOUSE_TEST_DB, like every other DB test in this package.

// The gate-pass facade has to survive a full round trip through wh_slips and
// come back out in the flat shape the old contract promised. Anything less and
// consolidating the two tables silently broke every client still on it.
func TestGatePassFacadeRoundTrip(t *testing.T) {
	s, ctx := testPool(t)
	sfx := uuid.NewString()[:8]

	created, err := s.CreateGatePass(ctx, models.GatePass{
		Items:        "Angle grinder; extension lead",
		IssuedTo:     "J. Okello " + sfx,
		Dept:         "Maintenance",
		Purpose:      "Repair at supplier",
		DateOut:      "2026-07-01",
		ReturnBy:     "2026-07-15",
		AuthorizedBy: "R. Mbabazi",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.GatePassNo == "" {
		t.Error("no number minted; a pass nobody can quote at the gate is not a pass")
	}
	if created.Status != "On Loan" {
		t.Errorf("status = %q, want the flat contract's \"On Loan\"", created.Status)
	}
	if created.Items != "Angle grinder; extension lead" {
		t.Errorf("items round-tripped as %q", created.Items)
	}
	if created.ReturnBy != "2026-07-15" {
		t.Errorf("return_by = %q, want 2026-07-15", created.ReturnBy)
	}
	if created.DateOut != "2026-07-01" {
		t.Errorf("date_out = %q — the field with no typed home on a slip", created.DateOut)
	}

	// It must appear in the list, which is the query storesiag actually calls.
	list, err := s.ListGatePasses(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found bool
	for _, g := range list {
		if g.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("created pass is absent from ListGatePasses")
	}

	// Empty fields mean "leave alone" — the behaviour the flat contract had.
	updated, err := s.UpdateGatePass(ctx, created.ID, models.GatePass{Purpose: "Calibration"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Purpose != "Calibration" {
		t.Errorf("purpose = %q, want Calibration", updated.Purpose)
	}
	if updated.IssuedTo != created.IssuedTo {
		t.Errorf("issued_to was clobbered by an empty field: %q", updated.IssuedTo)
	}
	if updated.Items != created.Items {
		t.Errorf("items were clobbered by an empty field: %q", updated.Items)
	}

	returned, err := s.ReturnGatePass(ctx, created.ID, "2026-07-14")
	if err != nil {
		t.Fatalf("return: %v", err)
	}
	if returned.Status != "Returned" {
		t.Errorf("status after return = %q, want Returned", returned.Status)
	}
	if returned.ReturnDate != "2026-07-14" {
		t.Errorf("return_date = %q, want 2026-07-14", returned.ReturnDate)
	}

	if err := s.DeleteGatePass(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.getGatePass(ctx, created.ID); err != ErrNotFound {
		t.Errorf("after delete, get returned %v, want ErrNotFound", err)
	}
}

// A cargo gate pass must not leak through the equipment-handover facade: it
// would flatten to one string and report as "On Loan", which is a distortion
// rather than a projection.
func TestGatePassFacadeExcludesCargoSlips(t *testing.T) {
	s, ctx := testPool(t)
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO wh_slips (slip_no, slip_type, status, returnable, issued_to_name)
		VALUES ($1, 'cargo_gate_pass', 'released', FALSE, 'Haulier')
		RETURNING id`, "CG-"+uuid.NewString()[:8]).Scan(&id)
	if err != nil {
		t.Fatalf("seed cargo slip: %v", err)
	}
	t.Cleanup(func() { _, _ = s.pool.Exec(context.Background(), `DELETE FROM wh_slips WHERE id=$1`, id) })

	list, err := s.ListGatePasses(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, g := range list {
		if g.ID == id {
			t.Fatal("a cargo gate pass surfaced through the equipment-handover facade")
		}
	}
}

// The lifecycle gate has to refuse at the database, on the real column, with
// the real statuses — not just in the pure function.
func TestItemLifecycleGateRefusesAtTheDatabase(t *testing.T) {
	s, ctx := testPool(t)
	s.SetItemLifecycle(true)
	f := newFixture(t, s, ctx)

	blocked, err := s.SetItemStatus(ctx, f.item.ID, models.ItemStatusBlocked)
	if err != nil {
		t.Fatalf("set status: %v", err)
	}
	if blocked.Status != models.ItemStatusBlocked {
		t.Fatalf("status = %q after SetItemStatus", blocked.Status)
	}

	_, err = s.CreateIssue(ctx, CreateIssueInput{
		Department:   strPtr("Maintenance"),
		WorkOrderRef: strPtr("WO-" + f.suffix),
		Lines:        []IssueLineInput{{ItemID: f.item.ID, Qty: 1, UOM: "ea", BinCode: f.pickBin.Code}},
	})
	var statusErr *ItemStatusError
	if !asItemStatusError(err, &statusErr) {
		t.Fatalf("issuing a blocked item returned %v, want an ItemStatusError", err)
	}
	if !strings.Contains(statusErr.Error(), f.item.SKU) {
		t.Errorf("refusal does not name the SKU: %s", statusErr.Error())
	}

	// The override must not lift a block — only `restricted`.
	_, err = s.CreateIssue(ctx, CreateIssueInput{
		Department:           strPtr("Maintenance"),
		WorkOrderRef:         strPtr("WO-" + f.suffix),
		AllowRestrictedItems: true,
		Lines:                []IssueLineInput{{ItemID: f.item.ID, Qty: 1, UOM: "ea", BinCode: f.pickBin.Code}},
	})
	if !asItemStatusError(err, &statusErr) {
		t.Fatalf("the override lifted a block; it must not: %v", err)
	}

	// Obsolete is the asymmetric one: no more receipts, but the shelf runs down.
	if _, err := s.SetItemStatus(ctx, f.item.ID, models.ItemStatusObsolete); err != nil {
		t.Fatalf("set obsolete: %v", err)
	}
	_, err = s.CreateReceipt(ctx, CreateReceiptInput{
		ReceiptType: "purchase",
		Lines:       []ReceiptLineInput{{ItemID: f.item.ID, Qty: 1, UOM: "ea", BinCode: f.bulkBin.Code}},
	})
	if !asItemStatusError(err, &statusErr) {
		t.Fatalf("receiving an obsolete item returned %v, want a refusal", err)
	}
	if _, err := s.CreateIssue(ctx, CreateIssueInput{
		Department:   strPtr("Maintenance"),
		WorkOrderRef: strPtr("WO-" + f.suffix),
		Lines:        []IssueLineInput{{ItemID: f.item.ID, Qty: 1, UOM: "ea", BinCode: f.pickBin.Code}},
	}); asItemStatusError(err, &statusErr) {
		t.Errorf("issuing obsolete stock was refused; it must be allowed to run down: %v", err)
	}

	// Restored to active, the same issue must work again — otherwise the test
	// above only proved that something was broken.
	if _, err := s.SetItemStatus(ctx, f.item.ID, models.ItemStatusActive); err != nil {
		t.Fatalf("set active: %v", err)
	}
	if _, err := s.CreateReceipt(ctx, CreateReceiptInput{
		ReceiptType: "purchase",
		Lines:       []ReceiptLineInput{{ItemID: f.item.ID, Qty: 1, UOM: "ea", BinCode: f.bulkBin.Code}},
	}); asItemStatusError(err, &statusErr) {
		t.Errorf("an active item was refused: %v", err)
	}
}

// Using the override has to leave a trace, in the same transaction as the
// movement it permitted.
func TestRestrictedOverrideIsRecorded(t *testing.T) {
	s, ctx := testPool(t)
	s.SetItemLifecycle(true)
	f := newFixture(t, s, ctx)

	if _, err := s.SetItemStatus(ctx, f.item.ID, models.ItemStatusRestricted); err != nil {
		t.Fatalf("set restricted: %v", err)
	}
	actor := uuid.New()
	if _, err := s.CreateIssue(ctx, CreateIssueInput{
		Department:           strPtr("Maintenance"),
		WorkOrderRef:         strPtr("WO-" + f.suffix),
		AllowRestrictedItems: true,
		CreatedBy:            &actor,
		Lines:                []IssueLineInput{{ItemID: f.item.ID, Qty: 1, UOM: "ea", BinCode: f.pickBin.Code}},
	}); err != nil {
		t.Fatalf("issue under override: %v", err)
	}

	var n int
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM wh_control_overrides
		WHERE kind = 'item_status' AND subject = $1 AND actor_id = $2`,
		f.item.SKU, actor).Scan(&n); err != nil {
		t.Fatalf("count overrides: %v", err)
	}
	if n != 1 {
		t.Fatalf("override entries for %s = %d, want 1", f.item.SKU, n)
	}
}

// A refused issue must leave no override entry behind. An exception report with
// rows for movements that never happened stops being read.
func TestRefusedIssueLeavesNoOverrideEntry(t *testing.T) {
	s, ctx := testPool(t)
	s.SetItemLifecycle(true)
	f := newFixture(t, s, ctx)

	if _, err := s.SetItemStatus(ctx, f.item.ID, models.ItemStatusRestricted); err != nil {
		t.Fatalf("set restricted: %v", err)
	}
	actor := uuid.New()
	// Enough quantity to guarantee the issue fails after the gate has passed.
	_, err := s.CreateIssue(ctx, CreateIssueInput{
		Department:           strPtr("Maintenance"),
		WorkOrderRef:         strPtr("WO-" + f.suffix),
		AllowRestrictedItems: true,
		CreatedBy:            &actor,
		Lines: []IssueLineInput{
			{ItemID: f.item.ID, Qty: 1, UOM: "ea", BinCode: f.pickBin.Code},
			{ItemID: uuid.New(), Qty: 1, UOM: "ea", BinCode: f.pickBin.Code}, // unknown item → rollback
		},
	})
	if err == nil {
		t.Fatal("expected the issue to fail on the unknown item")
	}

	var n int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM wh_control_overrides WHERE actor_id = $1`, actor).Scan(&n); err != nil {
		t.Fatalf("count overrides: %v", err)
	}
	if n != 0 {
		t.Fatalf("a rolled-back issue left %d override entries; the log must roll back with it", n)
	}
}

// Turning the gate off has to restore the old behaviour exactly, or the flag is
// not the revert switch it is documented as.
func TestLifecycleGateOffLetsBlockedItemsThrough(t *testing.T) {
	s, ctx := testPool(t)
	s.SetItemLifecycle(true)
	f := newFixture(t, s, ctx)
	if _, err := s.SetItemStatus(ctx, f.item.ID, models.ItemStatusBlocked); err != nil {
		t.Fatalf("set blocked: %v", err)
	}

	s.SetItemLifecycle(false)
	if _, err := s.CreateReceipt(ctx, CreateReceiptInput{
		ReceiptType: "purchase",
		Lines:       []ReceiptLineInput{{ItemID: f.item.ID, Qty: 1, UOM: "ea", BinCode: f.bulkBin.Code}},
	}); err != nil {
		var statusErr *ItemStatusError
		if asItemStatusError(err, &statusErr) {
			t.Fatalf("the gate fired with the flag off: %v", err)
		}
		t.Fatalf("receipt failed for another reason: %v", err)
	}
}

func TestControlOverrideLogIsAppendOnly(t *testing.T) {
	s, ctx := testPool(t)
	id, err := s.RecordControlOverride(ctx, ControlOverride{
		Kind: OverrideKindFEFO, Subject: "SKU-" + uuid.NewString()[:8], Reason: "near-expiry lot held for a customer",
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE wh_control_overrides SET reason='tidied' WHERE id=$1`, id); err == nil {
		t.Error("the override log accepted an UPDATE; it must be append-only")
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM wh_control_overrides WHERE id=$1`, id); err == nil {
		t.Error("the override log accepted a DELETE; it must be append-only")
	}
}

func asItemStatusError(err error, target **ItemStatusError) bool {
	if err == nil {
		return false
	}
	e, ok := err.(*ItemStatusError)
	if ok {
		*target = e
	}
	return ok
}
