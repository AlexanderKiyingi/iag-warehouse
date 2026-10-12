package store

import (
	"testing"
)

// What a maintenance work order consumed, with cost — the parts half of a
// job's cost. Database-backed (see testPool).
func TestWorkOrderConsumption(t *testing.T) {
	s, ctx := testPool(t)
	f := newFixture(t, s, ctx)
	f.seed(t, s, ctx, f.bulkBin.Code, 10, 2000)

	issue := func(wo string, qty float64, post bool) {
		t.Helper()
		ref := wo
		iss, err := s.CreateIssue(ctx, CreateIssueInput{
			WorkOrderRef: &ref,
			Lines:        []IssueLineInput{{ItemID: f.item.ID, Qty: qty, UOM: "kg", BinCode: f.bulkBin.Code}},
		})
		if err != nil {
			t.Fatalf("issue %s: %v", wo, err)
		}
		if post {
			if _, err := s.PostIssue(ctx, iss.ID, nil); err != nil {
				t.Fatalf("post %s: %v", wo, err)
			}
		}
	}
	wo1, wo2 := "WO-T1-"+f.suffix, "WO-T2-"+f.suffix
	issue(wo1, 3, true)
	issue(wo1, 1, false) // a draft consumed nothing
	issue(wo2, 2, true)

	got, err := s.ListWorkOrderConsumption(ctx, ConsumptionFilter{WorkOrderRefs: []string{wo1}})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want the one posted line for %s, got %+v", wo1, got)
	}
	c := got[0]
	if c.Qty != 3 || c.SKU != f.item.SKU || c.BinCode != f.bulkBin.Code {
		t.Fatalf("line: %+v", c)
	}
	// Costing is on and no finance bridge is configured: the movement must
	// still carry its cost (it used to be stored only alongside the bridge).
	if c.Estimated || c.TotalCost != 6000 || c.UnitCost != 2000 || c.Currency != "UGX" {
		t.Fatalf("cost: %+v", c)
	}

	both, err := s.ListWorkOrderConsumption(ctx, ConsumptionFilter{WorkOrderRefs: []string{wo1, wo2}})
	if err != nil || len(both) != 2 {
		t.Fatalf("two work orders: %+v %v", both, err)
	}

	// With costing off the movement carries no cost; the item's average
	// stands in, and says it is an estimate.
	s.SetCosting(false, "")
	wo3 := "WO-T3-" + f.suffix
	issue(wo3, 1, true)
	est, err := s.ListWorkOrderConsumption(ctx, ConsumptionFilter{WorkOrderRefs: []string{wo3}})
	if err != nil || len(est) != 1 {
		t.Fatalf("estimate read: %+v %v", est, err)
	}
	if !est[0].Estimated || est[0].TotalCost != 2000 {
		t.Fatalf("estimate: %+v", est[0])
	}
}
