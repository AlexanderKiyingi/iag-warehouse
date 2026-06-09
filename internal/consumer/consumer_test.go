package consumer

import "testing"

func TestUniqueTopics(t *testing.T) {
	got := uniqueTopics("iag.commercial", "", "iag.commercial", "iag.production")
	if len(got) != 2 || got[0] != "iag.commercial" || got[1] != "iag.production" {
		t.Fatalf("uniqueTopics: got %v", got)
	}
}

func TestStrField(t *testing.T) {
	data := map[string]any{"grn_id": "GRN-1", "qty": 12}
	if v, ok := strField(data, "grn_id"); !ok || v != "GRN-1" {
		t.Fatalf("grn_id: got %q ok=%v", v, ok)
	}
	if _, ok := strField(data, "missing"); ok {
		t.Fatal("expected missing key to be absent")
	}
}

func TestNumField(t *testing.T) {
	data := map[string]any{"qty": float64(42.5), "n": 3}
	if numField(data, "qty") != 42.5 {
		t.Fatal("qty")
	}
	if numField(data, "n") != 3 {
		t.Fatal("n")
	}
}
