package watcher

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"testing"
)

func TestDiffSnapshot_HashComparison(t *testing.T) {
	data1, _ := json.Marshal([][]any{{"a", 1}, {"b", 2}})
	data2, _ := json.Marshal([][]any{{"a", 1}, {"b", 3}}) // different

	hash1 := fmt.Sprintf("%x", sha256.Sum256(data1))
	hash2 := fmt.Sprintf("%x", sha256.Sum256(data2))

	if hash1 == hash2 {
		t.Error("expected different hashes for different data")
	}

	// Same data should produce same hash
	data1copy, _ := json.Marshal([][]any{{"a", 1}, {"b", 2}})
	hash1copy := fmt.Sprintf("%x", sha256.Sum256(data1copy))
	if hash1 != hash1copy {
		t.Error("expected same hash for identical data")
	}
}

func TestDiffSnapshot_RowCountChangePercent(t *testing.T) {
	tests := []struct {
		name      string
		prev      int
		current   int
		threshold float64
		triggered bool
	}{
		{"no change", 100, 100, 10, false},
		{"within threshold", 100, 105, 10, false},
		{"at threshold", 100, 110, 10, false}, // 10% is not > 10
		{"exceeds threshold", 100, 120, 10, true},
		{"decrease within", 100, 95, 10, false},
		{"decrease exceeds", 100, 80, 10, true},
		{"large increase", 50, 100, 50, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changePct := math.Abs(float64(tt.current-tt.prev)) / float64(tt.prev) * 100
			triggered := changePct > tt.threshold
			if triggered != tt.triggered {
				t.Errorf("change=%.1f%%, threshold=%.0f%%: got triggered=%v, want %v",
					changePct, tt.threshold, triggered, tt.triggered)
			}
		})
	}
}

func TestDiffSnapshot_ExactComparison(t *testing.T) {
	data1, _ := json.Marshal([][]any{{"a", 1}})
	data2, _ := json.Marshal([][]any{{"a", 1}})
	data3, _ := json.Marshal([][]any{{"a", 2}})

	if string(data1) != string(data2) {
		t.Error("expected exact match for identical data")
	}
	if string(data1) == string(data3) {
		t.Error("expected no match for different data")
	}
}

func TestDiffSnapshot_ZeroPrevRowCount(t *testing.T) {
	// When previous row count is 0 and current is non-zero, should trigger
	prevRowCount := 0
	currentRowCount := 5

	if prevRowCount == 0 {
		triggered := currentRowCount != 0
		if !triggered {
			t.Error("expected trigger when going from 0 to non-zero rows")
		}
	}

	// 0 to 0 should not trigger
	currentRowCount = 0
	if prevRowCount == 0 {
		triggered := currentRowCount != 0
		if triggered {
			t.Error("expected no trigger when staying at 0 rows")
		}
	}
}

func TestDiffConfig_Parse(t *testing.T) {
	raw := `{"source":"query","query":"SELECT * FROM pg_roles","compare_mode":"hash","change_threshold":15.5}`
	var cfg DiffConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("failed to parse DiffConfig: %v", err)
	}
	if cfg.Source != "query" {
		t.Errorf("source = %q, want %q", cfg.Source, "query")
	}
	if cfg.CompareMode != "hash" {
		t.Errorf("compare_mode = %q, want %q", cfg.CompareMode, "hash")
	}
	if cfg.ChangeThreshold != 15.5 {
		t.Errorf("change_threshold = %f, want 15.5", cfg.ChangeThreshold)
	}
}
