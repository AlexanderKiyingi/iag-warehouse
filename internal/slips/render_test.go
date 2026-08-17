package slips

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"iag-warehouse/backend/internal/models"
)

func sampleSlip() models.Slip {
	no := "GP-2026-000417"
	token := "K3XQ7ZP4WM2NRB6A"
	due := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	return models.Slip{
		ID:             uuid.New(),
		SlipNo:         &no,
		SlipType:       models.SlipCargoGatePass,
		Status:         models.SlipIssued,
		Purpose:        "Delivery to buyer",
		FacilityName:   "Kasese Mill",
		IssuedToName:   "Bugisu Coffee Ltd",
		Dept:           "Stores",
		DriverName:     "J. Okello",
		DriverIDNo:     "CM90210",
		VehicleReg:     "UAX 123K",
		Transporter:    "Rwenzori Haulage",
		Destination:    "Kampala",
		Returnable:     true,
		ReturnBy:       &due,
		AuthorizedName: "A. Nakato",
		VerifyToken:    &token,
		Lines: []models.SlipLine{
			{Description: "Green coffee, washed arabica", ItemSKU: "COF-AR-01", Qty: 300, UOM: "kg", ConditionOut: "sealed"},
			{Description: "Pallet, wooden", Qty: 6, UOM: "ea"},
		},
	}
}

// TestRenderProducesAScannableSheet checks the things that decide whether a
// printed slip works at a barrier: the number, the barcode, the returns warning,
// and no external references (depot machines have no internet).
func TestRenderProducesAScannableSheet(t *testing.T) {
	html, err := Render(sampleSlip(), PrintOptions{OrgName: "Inspire Africa Group"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		"GP-2026-000417", "CARGO GATE PASS", "UAX 123K", "J. Okello",
		"K3XQ7ZP4WM2NRB6A", "<svg", "OUTSTANDING", "Green coffee, washed arabica",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("printed slip is missing %q", want)
		}
	}
	// A slip that pulls a stylesheet or a font off the internet prints blank in a
	// depot with no connectivity.
	for _, forbidden := range []string{"<link", "<script", "src=\"http", "@import"} {
		if strings.Contains(html, forbidden) {
			t.Errorf("printed slip references something external: %q", forbidden)
		}
	}
}

// TestRenderMarksADraftAsInvalid is the safety property: an unauthorised slip
// must be obviously worthless to a guard glancing at it, and must carry no
// barcode that could be scanned into looking legitimate.
func TestRenderMarksADraftAsInvalid(t *testing.T) {
	draft := sampleSlip()
	draft.SlipNo = nil
	draft.VerifyToken = nil
	draft.Status = models.SlipDraft

	html, err := Render(draft, PrintOptions{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, "not valid at the gate") {
		t.Error("a draft must be visibly marked as not valid")
	}
	if strings.Contains(html, "<svg") {
		t.Error("a draft must not carry a scannable barcode")
	}
}

// TestRenderEscapesUntrustedText — driver names, destinations and line
// descriptions are free text typed by users, and the sheet is HTML.
func TestRenderEscapesUntrustedText(t *testing.T) {
	slip := sampleSlip()
	slip.DriverName = `<script>alert(1)</script>`
	html, err := Render(slip, PrintOptions{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Error("free-text fields must be escaped")
	}
}

// TestRenderSample writes a slip to disk for a human to look at. It is opt-in so
// it does not litter a normal test run.
func TestRenderSample(t *testing.T) {
	dir := os.Getenv("SLIP_SAMPLE_DIR")
	if dir == "" {
		t.Skip("SLIP_SAMPLE_DIR not set")
	}
	html, err := Render(sampleSlip(), PrintOptions{VerifyURL: "https://iag.example/api/v1/warehouse/slips/verify"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	path := filepath.Join(dir, "gate-pass-sample.html")
	if err := os.WriteFile(path, []byte(html), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Logf("wrote %s", path)
}
