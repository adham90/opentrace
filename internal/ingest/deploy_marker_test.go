package ingest

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// recordingDeployStore captures Record calls. Only Record is exercised on the
// ingest path, so the reads are inert.
type recordingDeployStore struct {
	mu       sync.Mutex
	recorded []store.Deploy
}

func (m *recordingDeployStore) Record(_ context.Context, d store.Deploy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recorded = append(m.recorded, d)
	return nil
}

func (m *recordingDeployStore) Latest(context.Context, string, string) (*store.Deploy, error) {
	return nil, store.ErrNotFound
}

func (m *recordingDeployStore) List(context.Context, store.ListDeployParams) ([]store.Deploy, error) {
	return nil, nil
}

func (m *recordingDeployStore) Prune(context.Context, time.Duration) (int64, error) { return 0, nil }

func (m *recordingDeployStore) snapshot() []store.Deploy {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]store.Deploy(nil), m.recorded...)
}

func TestProcessAfterInsert_RecordsDeployMarker(t *testing.T) {
	ds := &recordingDeployStore{}
	h := &Handler{LogStore: &mockLogStore{}, DeployStore: ds}

	body := `{"level":"info","message":"hi","service":"api","env":"production","version":"abc123"}`
	rec := post(t, h, "/api/logs", body, "application/json", false)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}

	waitFor(t, "Record", func() bool { return len(ds.snapshot()) == 1 })
	got := ds.snapshot()[0]
	if got.CommitHash != "abc123" {
		t.Errorf("CommitHash = %q, want abc123", got.CommitHash)
	}
	if got.Service != "api" {
		t.Errorf("Service = %q, want api", got.Service)
	}
	if got.Environment != "production" {
		t.Errorf("Environment = %q, want production", got.Environment)
	}
	if got.FirstSeenAt.IsZero() {
		t.Error("FirstSeenAt is zero — the marker would sort unpredictably")
	}
}

// A batch is usually all one commit. Recording once per entry would turn a
// 500-entry batch into 500 writes on the ingest path.
func TestProcessAfterInsert_DedupesDeployWithinBatch(t *testing.T) {
	ds := &recordingDeployStore{}
	h := &Handler{LogStore: &mockLogStore{}, DeployStore: ds}

	body := `[
		{"level":"info","message":"a","service":"api","env":"production","version":"abc123"},
		{"level":"info","message":"b","service":"api","env":"production","version":"abc123"},
		{"level":"info","message":"c","service":"api","env":"production","version":"abc123"},
		{"level":"info","message":"d","service":"worker","env":"production","version":"abc123"}
	]`
	rec := post(t, h, "/api/logs", body, "application/json", false)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}

	// Two distinct (commit, service, env) triples in the batch.
	waitFor(t, "Record", func() bool { return len(ds.snapshot()) == 2 })
	time.Sleep(50 * time.Millisecond) // catch a late third write, if any
	if n := len(ds.snapshot()); n != 2 {
		t.Fatalf("recorded %d deploys, want 2 (one per service)", n)
	}
}

// Clients that do not report a version must not create a phantom deploy.
func TestProcessAfterInsert_SkipsEmptyCommit(t *testing.T) {
	ds := &recordingDeployStore{}
	h := &Handler{LogStore: &mockLogStore{}, DeployStore: ds}

	body := `{"level":"info","message":"hi","service":"api","env":"production"}`
	rec := post(t, h, "/api/logs", body, "application/json", false)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}

	time.Sleep(100 * time.Millisecond)
	if n := len(ds.snapshot()); n != 0 {
		t.Fatalf("recorded %d deploys for a versionless log, want 0", n)
	}
}
