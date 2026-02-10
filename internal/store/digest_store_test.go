package store

import (
	"context"
	"testing"
	"time"
)

func TestDigestStore_SaveAndGetLatest(t *testing.T) {
	db := setupTestDB(t)
	ds := NewDigestStore(db)
	ctx := context.Background()

	now := time.Now().UTC()

	err := ds.Save(ctx, SaveDigestParams{
		ID:            "digest-1",
		Environment:   "production",
		Status:        "critical",
		PeriodStart:   now.Add(-24 * time.Hour),
		PeriodEnd:     now,
		AlertTotal:    5,
		AlertCritical: 2,
		AlertWarning:  3,
		MonitorTotal:  8,
		MonitorErrored: 1,
		FailedRuns:    2,
		Data:          `{"summary":"test digest"}`,
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Get latest
	d, err := ds.GetLatest(ctx, "")
	if err != nil {
		t.Fatalf("GetLatest: %v", err)
	}
	if d.ID != "digest-1" {
		t.Errorf("id = %q, want digest-1", d.ID)
	}
	if d.Status != "critical" {
		t.Errorf("status = %q, want critical", d.Status)
	}
	if d.AlertTotal != 5 {
		t.Errorf("alert_total = %d, want 5", d.AlertTotal)
	}
	if d.AlertCritical != 2 {
		t.Errorf("alert_critical = %d, want 2", d.AlertCritical)
	}
	if d.Data != `{"summary":"test digest"}` {
		t.Errorf("data = %q, unexpected", d.Data)
	}
}

func TestDigestStore_GetLatest_EnvironmentFilter(t *testing.T) {
	db := setupTestDB(t)
	ds := NewDigestStore(db)
	ctx := context.Background()

	now := time.Now().UTC()

	// Save production digest
	err := ds.Save(ctx, SaveDigestParams{
		ID:          "digest-prod",
		Environment: "production",
		Status:      "critical",
		PeriodStart: now.Add(-24 * time.Hour),
		PeriodEnd:   now,
		AlertTotal:  3,
		Data:        `{"env":"production"}`,
	})
	if err != nil {
		t.Fatalf("Save prod: %v", err)
	}

	// Save staging digest
	err = ds.Save(ctx, SaveDigestParams{
		ID:          "digest-stag",
		Environment: "staging",
		Status:      "healthy",
		PeriodStart: now.Add(-24 * time.Hour),
		PeriodEnd:   now,
		AlertTotal:  0,
		Data:        `{"env":"staging"}`,
	})
	if err != nil {
		t.Fatalf("Save staging: %v", err)
	}

	// Get latest for production
	d, err := ds.GetLatest(ctx, "production")
	if err != nil {
		t.Fatalf("GetLatest production: %v", err)
	}
	if d.ID != "digest-prod" {
		t.Errorf("id = %q, want digest-prod", d.ID)
	}

	// Get latest for staging
	d, err = ds.GetLatest(ctx, "staging")
	if err != nil {
		t.Fatalf("GetLatest staging: %v", err)
	}
	if d.ID != "digest-stag" {
		t.Errorf("id = %q, want digest-stag", d.ID)
	}
}

func TestDigestStore_GetLatest_NotFound(t *testing.T) {
	db := setupTestDB(t)
	ds := NewDigestStore(db)
	ctx := context.Background()

	_, err := ds.GetLatest(ctx, "")
	if err != ErrNotFound {
		t.Errorf("GetLatest empty = %v, want ErrNotFound", err)
	}
}

func TestDigestStore_List(t *testing.T) {
	db := setupTestDB(t)
	ds := NewDigestStore(db)
	ctx := context.Background()

	now := time.Now().UTC()

	// Save 3 digests
	for i, id := range []string{"d1", "d2", "d3"} {
		err := ds.Save(ctx, SaveDigestParams{
			ID:          id,
			Status:      "healthy",
			PeriodStart: now.Add(-time.Duration(3-i) * 24 * time.Hour),
			PeriodEnd:   now.Add(-time.Duration(2-i) * 24 * time.Hour),
			Data:        `{}`,
		})
		if err != nil {
			t.Fatalf("Save %s: %v", id, err)
		}
	}

	// List all
	all, err := ds.List(ctx, 10, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("list len = %d, want 3", len(all))
	}

	// Should be ordered by period_end DESC (most recent first)
	if all[0].ID != "d3" {
		t.Errorf("first = %q, want d3 (most recent)", all[0].ID)
	}

	// List with limit
	limited, err := ds.List(ctx, 2, "")
	if err != nil {
		t.Fatalf("List limited: %v", err)
	}
	if len(limited) != 2 {
		t.Errorf("limited len = %d, want 2", len(limited))
	}
}

func TestDigestStore_DeleteOlderThan(t *testing.T) {
	db := setupTestDB(t)
	ds := NewDigestStore(db)
	ctx := context.Background()

	now := time.Now().UTC()

	// Save an old digest (backdated via direct SQL)
	_, err := db.ExecContext(ctx,
		`INSERT INTO digests (id, status, period_start, period_end, data, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"old-digest", "healthy",
		now.Add(-48*time.Hour).Format(time.RFC3339),
		now.Add(-24*time.Hour).Format(time.RFC3339),
		`{}`,
		now.Add(-48*time.Hour).Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("insert old: %v", err)
	}

	// Save a recent digest
	err = ds.Save(ctx, SaveDigestParams{
		ID:          "recent-digest",
		Status:      "healthy",
		PeriodStart: now.Add(-24 * time.Hour),
		PeriodEnd:   now,
		Data:        `{}`,
	})
	if err != nil {
		t.Fatalf("Save recent: %v", err)
	}

	// Delete older than 24 hours
	deleted, err := ds.DeleteOlderThan(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("DeleteOlderThan: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}

	// Verify only recent remains
	all, err := ds.List(ctx, 10, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("remaining = %d, want 1", len(all))
	}
	if all[0].ID != "recent-digest" {
		t.Errorf("remaining id = %q, want recent-digest", all[0].ID)
	}
}

func TestDigestStore_List_EnvironmentFilter(t *testing.T) {
	db := setupTestDB(t)
	ds := NewDigestStore(db)
	ctx := context.Background()

	now := time.Now().UTC()

	err := ds.Save(ctx, SaveDigestParams{
		ID: "prod-1", Environment: "production", Status: "critical",
		PeriodStart: now.Add(-24 * time.Hour), PeriodEnd: now,
		Data: `{}`,
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	err = ds.Save(ctx, SaveDigestParams{
		ID: "stag-1", Environment: "staging", Status: "healthy",
		PeriodStart: now.Add(-24 * time.Hour), PeriodEnd: now,
		Data: `{}`,
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	prod, err := ds.List(ctx, 10, "production")
	if err != nil {
		t.Fatalf("List production: %v", err)
	}
	if len(prod) != 1 {
		t.Errorf("production digests = %d, want 1", len(prod))
	}
	if prod[0].ID != "prod-1" {
		t.Errorf("id = %q, want prod-1", prod[0].ID)
	}
}
