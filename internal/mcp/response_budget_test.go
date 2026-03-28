package mcp

import "testing"

func TestResponseBudget_Basic(t *testing.T) {
	rb := NewResponseBudget(MinResponseBudget)
	resp := make(map[string]any)

	// Small field should fit
	if !rb.AddToResponse(resp, "name", "test") {
		t.Error("small field should fit")
	}
	if resp["name"] != "test" {
		t.Error("field should be added to response")
	}

	// Large field should be rejected (exceeds 4KB budget)
	bigData := make([]byte, MinResponseBudget+1)
	if rb.AddToResponse(resp, "big", string(bigData)) {
		t.Error("oversized field should not fit")
	}
	if !rb.Truncated() {
		t.Error("should be marked truncated")
	}

	rb.FinalizeResponse(resp)
	if resp["truncated"] != true {
		t.Error("response should have truncated flag")
	}
}

func TestResponseBudget_MinBudget(t *testing.T) {
	rb := NewResponseBudget(10) // below minimum
	if rb.maxBytes != MinResponseBudget {
		t.Errorf("expected min budget %d, got %d", MinResponseBudget, rb.maxBytes)
	}
}
