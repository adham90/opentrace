package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

func TestHealthCheckStore_CreateAndGet(t *testing.T) {
	db := setupTestDB(t)
	s := NewHealthCheckStore(db)
	ctx := context.Background()

	hc, err := s.Create(ctx, store.CreateHealthCheckParams{
		Name: "My API",
		URL:  "https://api.example.com/health",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if hc.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if hc.Name != "My API" {
		t.Errorf("Name = %q, want My API", hc.Name)
	}
	if hc.Method != "GET" {
		t.Errorf("Method = %q, want GET", hc.Method)
	}
	if hc.IntervalSecs != 60 {
		t.Errorf("IntervalSecs = %d, want 60", hc.IntervalSecs)
	}
	if hc.ExpectedStatus != 200 {
		t.Errorf("ExpectedStatus = %d, want 200", hc.ExpectedStatus)
	}
	if !hc.Enabled {
		t.Error("expected Enabled = true")
	}

	got, err := s.Get(ctx, hc.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "My API" {
		t.Errorf("Get Name = %q, want My API", got.Name)
	}
}

func TestHealthCheckStore_CreateCustomParams(t *testing.T) {
	db := setupTestDB(t)
	s := NewHealthCheckStore(db)
	ctx := context.Background()

	hc, err := s.Create(ctx, store.CreateHealthCheckParams{
		Name:           "Custom",
		URL:            "https://example.com/ping",
		Method:         "HEAD",
		IntervalSecs:   30,
		TimeoutSecs:    5,
		ExpectedStatus: 204,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if hc.Method != "HEAD" {
		t.Errorf("Method = %q, want HEAD", hc.Method)
	}
	if hc.IntervalSecs != 30 {
		t.Errorf("IntervalSecs = %d, want 30", hc.IntervalSecs)
	}
	if hc.TimeoutSecs != 5 {
		t.Errorf("TimeoutSecs = %d, want 5", hc.TimeoutSecs)
	}
	if hc.ExpectedStatus != 204 {
		t.Errorf("ExpectedStatus = %d, want 204", hc.ExpectedStatus)
	}
}

func TestHealthCheckStore_List(t *testing.T) {
	db := setupTestDB(t)
	s := NewHealthCheckStore(db)
	ctx := context.Background()

	s.Create(ctx, store.CreateHealthCheckParams{Name: "A", URL: "https://a.com"})
	s.Create(ctx, store.CreateHealthCheckParams{Name: "B", URL: "https://b.com"})

	list, err := s.List(ctx, store.ListHealthCheckParams{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len = %d, want 2", len(list))
	}
}

func TestHealthCheckStore_Delete(t *testing.T) {
	db := setupTestDB(t)
	s := NewHealthCheckStore(db)
	ctx := context.Background()

	hc, _ := s.Create(ctx, store.CreateHealthCheckParams{Name: "ToDelete", URL: "https://del.com"})

	if err := s.Delete(ctx, hc.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := s.Get(ctx, hc.ID)
	if err != store.ErrNotFound {
		t.Errorf("Get after delete: err = %v, want ErrNotFound", err)
	}

	// Delete non-existent returns ErrNotFound
	if err := s.Delete(ctx, "nonexistent"); err != store.ErrNotFound {
		t.Errorf("Delete nonexistent: err = %v, want ErrNotFound", err)
	}
}

func TestHealthCheckStore_SetEnabled(t *testing.T) {
	db := setupTestDB(t)
	s := NewHealthCheckStore(db)
	ctx := context.Background()

	hc, _ := s.Create(ctx, store.CreateHealthCheckParams{Name: "Toggle", URL: "https://toggle.com"})

	if err := s.SetEnabled(ctx, hc.ID, false); err != nil {
		t.Fatalf("SetEnabled(false): %v", err)
	}
	got, _ := s.Get(ctx, hc.ID)
	if got.Enabled {
		t.Error("expected Enabled = false after SetEnabled(false)")
	}

	if err := s.SetEnabled(ctx, hc.ID, true); err != nil {
		t.Fatalf("SetEnabled(true): %v", err)
	}
	got, _ = s.Get(ctx, hc.ID)
	if !got.Enabled {
		t.Error("expected Enabled = true after SetEnabled(true)")
	}

	if err := s.SetEnabled(ctx, "nonexistent", true); err != store.ErrNotFound {
		t.Errorf("SetEnabled nonexistent: err = %v, want ErrNotFound", err)
	}
}

func TestHealthCheckStore_RecordAndLatestResults(t *testing.T) {
	db := setupTestDB(t)
	s := NewHealthCheckStore(db)
	ctx := context.Background()

	hc, _ := s.Create(ctx, store.CreateHealthCheckParams{Name: "Results", URL: "https://results.com"})

	code200 := 200
	ms50 := 50
	ms200 := 200

	now := time.Now().UTC()
	s.RecordResult(ctx, store.HealthCheckResult{
		HealthCheckID: hc.ID,
		Status:        store.HealthCheckUp,
		StatusCode:    &code200,
		ResponseMs:    &ms50,
		CheckedAt:     now.Add(-2 * time.Minute),
	})
	s.RecordResult(ctx, store.HealthCheckResult{
		HealthCheckID: hc.ID,
		Status:        store.HealthCheckDown,
		Error:         "timeout",
		CheckedAt:     now.Add(-1 * time.Minute),
	})
	s.RecordResult(ctx, store.HealthCheckResult{
		HealthCheckID: hc.ID,
		Status:        store.HealthCheckDegraded,
		StatusCode:    &code200,
		ResponseMs:    &ms200,
		CheckedAt:     now,
	})

	results, err := s.LatestResults(ctx, hc.ID, 10)
	if err != nil {
		t.Fatalf("LatestResults: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("len = %d, want 3", len(results))
	}
	// Most recent first
	if results[0].Status != store.HealthCheckDegraded {
		t.Errorf("results[0].Status = %q, want degraded", results[0].Status)
	}
	if results[1].Status != store.HealthCheckDown {
		t.Errorf("results[1].Status = %q, want down", results[1].Status)
	}
}

func TestHealthCheckStore_UptimeSummaries(t *testing.T) {
	db := setupTestDB(t)
	s := NewHealthCheckStore(db)
	ctx := context.Background()

	hc, _ := s.Create(ctx, store.CreateHealthCheckParams{Name: "Uptime", URL: "https://uptime.com"})

	code200 := 200
	ms100 := 100
	now := time.Now().UTC()

	// 3 up, 1 down
	for i := 0; i < 3; i++ {
		s.RecordResult(ctx, store.HealthCheckResult{
			HealthCheckID: hc.ID,
			Status:        store.HealthCheckUp,
			StatusCode:    &code200,
			ResponseMs:    &ms100,
			CheckedAt:     now.Add(-time.Duration(i) * time.Minute),
		})
	}
	s.RecordResult(ctx, store.HealthCheckResult{
		HealthCheckID: hc.ID,
		Status:        store.HealthCheckDown,
		Error:         "refused",
		CheckedAt:     now.Add(-5 * time.Minute),
	})

	summaries, err := s.UptimeSummaries(ctx, now.Add(-10*time.Minute))
	if err != nil {
		t.Fatalf("UptimeSummaries: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("len = %d, want 1", len(summaries))
	}
	us := summaries[0]
	if us.TotalChecks != 4 {
		t.Errorf("TotalChecks = %d, want 4", us.TotalChecks)
	}
	if us.DownChecks != 1 {
		t.Errorf("DownChecks = %d, want 1", us.DownChecks)
	}
	if us.UptimePct != 75.0 {
		t.Errorf("UptimePct = %f, want 75.0", us.UptimePct)
	}
	if us.CurrentStatus != "up" {
		t.Errorf("CurrentStatus = %q, want up", us.CurrentStatus)
	}
}

func TestHealthCheckStore_PruneResults(t *testing.T) {
	db := setupTestDB(t)
	s := NewHealthCheckStore(db)
	ctx := context.Background()

	hc, _ := s.Create(ctx, store.CreateHealthCheckParams{Name: "Prune", URL: "https://prune.com"})

	code200 := 200
	ms50 := 50

	// Old result
	s.RecordResult(ctx, store.HealthCheckResult{
		HealthCheckID: hc.ID,
		Status:        store.HealthCheckUp,
		StatusCode:    &code200,
		ResponseMs:    &ms50,
		CheckedAt:     time.Now().UTC().Add(-48 * time.Hour),
	})
	// Recent result
	s.RecordResult(ctx, store.HealthCheckResult{
		HealthCheckID: hc.ID,
		Status:        store.HealthCheckUp,
		StatusCode:    &code200,
		ResponseMs:    &ms50,
		CheckedAt:     time.Now().UTC(),
	})

	n, err := s.PruneResults(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("PruneResults: %v", err)
	}
	if n != 1 {
		t.Errorf("pruned = %d, want 1", n)
	}

	results, _ := s.LatestResults(ctx, hc.ID, 10)
	if len(results) != 1 {
		t.Errorf("remaining = %d, want 1", len(results))
	}
}

func TestHealthCheckStore_List_Pagination(t *testing.T) {
	db := setupTestDB(t)
	s := NewHealthCheckStore(db)
	ctx := context.Background()

	// Create 4 health checks
	for _, name := range []string{"A", "B", "C", "D"} {
		s.Create(ctx, store.CreateHealthCheckParams{Name: name, URL: "https://" + name + ".com"})
	}

	// No limit returns all
	all, err := s.List(ctx, store.ListHealthCheckParams{})
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("List all len = %d, want 4", len(all))
	}

	// Limit=2 returns 2
	page1, err := s.List(ctx, store.ListHealthCheckParams{Limit: 2})
	if err != nil {
		t.Fatalf("List page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1 len = %d, want 2", len(page1))
	}

	// Limit=2, Offset=2 returns next 2
	page2, err := s.List(ctx, store.ListHealthCheckParams{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("List page2: %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("page2 len = %d, want 2", len(page2))
	}

	// No overlap between pages
	if page1[0].ID == page2[0].ID {
		t.Error("page1 and page2 should not overlap")
	}

	// Offset past end returns empty
	page3, err := s.List(ctx, store.ListHealthCheckParams{Limit: 10, Offset: 10})
	if err != nil {
		t.Fatalf("List page3: %v", err)
	}
	if len(page3) != 0 {
		t.Errorf("page3 len = %d, want 0", len(page3))
	}
}

func TestHealthCheckStore_DeleteCascadesResults(t *testing.T) {
	db := setupTestDB(t)
	s := NewHealthCheckStore(db)
	ctx := context.Background()

	hc, _ := s.Create(ctx, store.CreateHealthCheckParams{Name: "Cascade", URL: "https://cascade.com"})
	code200 := 200
	ms50 := 50
	s.RecordResult(ctx, store.HealthCheckResult{
		HealthCheckID: hc.ID,
		Status:        store.HealthCheckUp,
		StatusCode:    &code200,
		ResponseMs:    &ms50,
		CheckedAt:     time.Now().UTC(),
	})

	s.Delete(ctx, hc.ID)

	results, _ := s.LatestResults(ctx, hc.ID, 10)
	if len(results) != 0 {
		t.Errorf("results after cascade delete = %d, want 0", len(results))
	}
}

func TestHealthCheckStore_EnvRoundTrip(t *testing.T) {
	db := setupTestDB(t)
	hs := NewHealthCheckStore(db)
	ctx := context.Background()

	hc, err := hs.Create(ctx, store.CreateHealthCheckParams{
		Name:        "api-staging",
		URL:         "https://staging.example.com/health",
		Environment: "staging",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if hc.Environment != "staging" {
		t.Errorf("Create: Environment = %q, want staging", hc.Environment)
	}

	got, err := hs.Get(ctx, hc.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Environment != "staging" {
		t.Errorf("Get: Environment = %q, want staging", got.Environment)
	}

	// Seed a second check in production.
	_, err = hs.Create(ctx, store.CreateHealthCheckParams{
		Name:        "api-prod",
		URL:         "https://prod.example.com/health",
		Environment: "production",
	})
	if err != nil {
		t.Fatalf("Create prod: %v", err)
	}

	// List with env filter.
	stagingOnly, err := hs.List(ctx, store.ListHealthCheckParams{Environment: "staging"})
	if err != nil {
		t.Fatalf("List staging: %v", err)
	}
	if len(stagingOnly) != 1 || stagingOnly[0].Environment != "staging" {
		t.Errorf("List staging = %+v, expected one staging check", stagingOnly)
	}

	// List without filter returns both.
	all, err := hs.List(ctx, store.ListHealthCheckParams{})
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("List all = %d, want 2", len(all))
	}
}
