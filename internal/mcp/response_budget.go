package mcp

import "encoding/json"

const (
	DefaultResponseBudget = 16 * 1024 // 16KB
	MinResponseBudget     = 4 * 1024  // 4KB
)

// ResponseBudget tracks response size and enforces a maximum.
type ResponseBudget struct {
	maxBytes  int
	usedBytes int
	truncated bool
}

// NewResponseBudget creates a budget with the given max bytes.
func NewResponseBudget(maxBytes int) *ResponseBudget {
	if maxBytes < MinResponseBudget {
		maxBytes = MinResponseBudget
	}
	return &ResponseBudget{maxBytes: maxBytes}
}

// CanFit returns true if adding the given data would stay within budget.
func (rb *ResponseBudget) CanFit(data any) bool {
	b, err := json.Marshal(data)
	if err != nil {
		return false
	}
	return rb.usedBytes+len(b) <= rb.maxBytes
}

// Add adds data to the budget tracker. Returns true if it fit.
func (rb *ResponseBudget) Add(data any) bool {
	b, err := json.Marshal(data)
	if err != nil {
		return false
	}
	size := len(b)
	if rb.usedBytes+size > rb.maxBytes {
		rb.truncated = true
		return false
	}
	rb.usedBytes += size
	return true
}

// AddToResponse adds a field to the response map if it fits within budget.
// Returns true if the field was added.
func (rb *ResponseBudget) AddToResponse(resp map[string]any, key string, value any) bool {
	b, err := json.Marshal(value)
	if err != nil {
		return false
	}
	// Account for key overhead: "key":value, ≈ len(key) + 4
	size := len(b) + len(key) + 4
	if rb.usedBytes+size > rb.maxBytes {
		rb.truncated = true
		return false
	}
	rb.usedBytes += size
	resp[key] = value
	return true
}

// Truncated returns true if any data was omitted due to budget.
func (rb *ResponseBudget) Truncated() bool {
	return rb.truncated
}

// Remaining returns the remaining bytes in the budget.
func (rb *ResponseBudget) Remaining() int {
	r := rb.maxBytes - rb.usedBytes
	if r < 0 {
		return 0
	}
	return r
}

// FinalizeResponse adds metadata about truncation to the response if needed.
func (rb *ResponseBudget) FinalizeResponse(resp map[string]any) {
	if rb.truncated {
		resp["truncated"] = true
		resp["budget_bytes"] = rb.maxBytes
	}
}
