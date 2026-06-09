package store

import (
	"context"
	"testing"
)

func TestNormalizeKeysEmpty(t *testing.T) {
	lot, serial := normalizeKeys("", "")
	if lot != "" || serial != "" {
		t.Fatalf("expected empty keys, got lot=%q serial=%q", lot, serial)
	}
}

func TestDefaultReceivingBinFallback(t *testing.T) {
	// nil pool — method returns hardcoded fallback without DB.
	st := &Store{}
	if got := st.DefaultReceivingBinCode(context.Background()); got != "RCV-01" {
		t.Fatalf("expected RCV-01 fallback, got %q", got)
	}
}
