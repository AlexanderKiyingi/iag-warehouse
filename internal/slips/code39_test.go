package slips

import (
	"strings"
	"testing"
)

func TestEncodableRejectsCharactersCode39CannotCarry(t *testing.T) {
	for _, s := range []string{"GP-2026-000123", "LPN 0042", "ABC$/+%.", "abc123"} {
		if !Encodable(s) {
			t.Errorf("%q should be encodable", s)
		}
	}
	// Lower case is folded to upper, but these have no representation at all.
	for _, s := range []string{"token#1", "a=b", "café", "under_score"} {
		if Encodable(s) {
			t.Errorf("%q should not be encodable", s)
		}
	}
}

// TestBarcodeRefusesRatherThanTruncates is the property that matters on a
// printed gate pass: a symbol that renders but does not scan is worse than no
// symbol, because the guard trusts it until it fails at the barrier.
func TestBarcodeRefusesRatherThanTruncates(t *testing.T) {
	if got := Barcode39SVG("BAD#TOKEN"); got != "" {
		t.Errorf("expected no symbol for unencodable input, got %d bytes", len(got))
	}
	if got := Barcode39SVG("   "); got != "" {
		t.Error("expected no symbol for blank input")
	}
}

// TestBarcodeStructure checks the symbol is well-formed: framed by start/stop
// and carrying one bar group per character.
func TestBarcodeStructure(t *testing.T) {
	svg := Barcode39SVG("GP-2026-1")
	if !strings.HasPrefix(svg, "<svg") || !strings.HasSuffix(svg, "</svg>") {
		t.Fatal("output is not a complete svg element")
	}
	// 9 payload characters plus the two framing '*' characters, five bars each.
	const wantBars = (9 + 2) * 5
	if got := strings.Count(svg, "<rect x="); got != wantBars {
		t.Errorf("bar count = %d, want %d (9 characters framed by start/stop)", got, wantBars)
	}
	if strings.Contains(svg, "http://") && !strings.Contains(svg, "www.w3.org/2000/svg") {
		t.Error("the symbol must not reference anything external")
	}
}

// TestBarcodeIsCaseFolded — a scanner reads Code 39 as upper case, so the two
// spellings have to produce the same symbol or a token would fail to verify
// depending on how it was typed.
func TestBarcodeIsCaseFolded(t *testing.T) {
	if Barcode39SVG("abc123") != Barcode39SVG("ABC123") {
		t.Error("lower and upper case must render identically")
	}
}
