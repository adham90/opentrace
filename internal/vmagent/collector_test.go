package vmagent

import (
	"context"
	"testing"
)

func TestCollector_Collect(t *testing.T) {
	c := NewCollector()
	ctx := context.Background()

	samples, err := c.Collect(ctx)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if len(samples) == 0 {
		t.Fatal("Collect returned no samples")
	}

	// Verify we get at least some expected metric names
	found := make(map[string]bool)
	for _, s := range samples {
		found[s.Name] = true
	}

	expected := []string{"cpu.usage_percent", "memory.total_bytes", "memory.usage_percent"}
	for _, name := range expected {
		if !found[name] {
			t.Errorf("missing expected metric %q", name)
		}
	}

	// Verify values are reasonable
	for _, s := range samples {
		if s.Name == "cpu.usage_percent" && (s.Value < 0 || s.Value > 100) {
			t.Errorf("cpu.usage_percent = %f, want 0-100", s.Value)
		}
		if s.Name == "memory.total_bytes" && s.Value <= 0 {
			t.Errorf("memory.total_bytes = %f, want > 0", s.Value)
		}
	}
}

func TestCollector_NetworkDelta(t *testing.T) {
	c := NewCollector()
	ctx := context.Background()

	// First collection: no network delta (no previous data)
	samples1, _ := c.Collect(ctx)
	hasNet := false
	for _, s := range samples1 {
		if s.Name == "network.bytes_sent_per_sec" {
			hasNet = true
		}
	}
	if hasNet {
		t.Error("first collection should not have network delta metrics")
	}

	// Second collection: should have network deltas
	samples2, _ := c.Collect(ctx)
	hasNet = false
	for _, s := range samples2 {
		if s.Name == "network.bytes_sent_per_sec" {
			hasNet = true
		}
	}
	if !hasNet {
		t.Error("second collection should have network delta metrics")
	}
}

func TestGetHostInfo(t *testing.T) {
	info, err := GetHostInfo()
	if err != nil {
		t.Fatalf("GetHostInfo: %v", err)
	}
	if info.Hostname == "" {
		t.Error("hostname is empty")
	}
	if info.OS == "" {
		t.Error("OS is empty")
	}
	if info.Arch == "" {
		t.Error("arch is empty")
	}
}
