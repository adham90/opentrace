package servers

import (
	"context"
	"errors"
	"testing"

	"github.com/adham90/opentrace/internal/domain"
	"github.com/adham90/opentrace/pkg/store"
	"github.com/google/uuid"
)

// fakeServers implements ServerRepository for testing.
type fakeServers struct {
	servers []store.Server
	server  *store.Server
	err     error
}

func (f *fakeServers) List(_ context.Context, _ store.ListServerParams) ([]store.Server, error) {
	return f.servers, f.err
}

func (f *fakeServers) Register(_ context.Context, _ store.RegisterServerParams) (*store.Server, error) {
	return f.server, f.err
}

func (f *fakeServers) GetByID(_ context.Context, _ uuid.UUID) (*store.Server, error) {
	return f.server, f.err
}

func (f *fakeServers) Update(_ context.Context, _ uuid.UUID, _ store.UpdateServerParams) (*store.Server, error) {
	return f.server, f.err
}

var _ ServerRepository = (*fakeServers)(nil)

// fakeMetrics implements MetricRepository for testing.
type fakeMetrics struct {
	points []store.MetricPoint
	err    error
}

func (f *fakeMetrics) Query(_ context.Context, _ store.MetricQuery) ([]store.MetricPoint, error) {
	return f.points, f.err
}

func (f *fakeMetrics) LatestByServer(_ context.Context, _ uuid.UUID) ([]store.MetricPoint, error) {
	return f.points, f.err
}

var _ MetricRepository = (*fakeMetrics)(nil)

// --- List ---

func TestList_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.List(context.Background(), store.ListServerParams{})
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestList_HappyPath(t *testing.T) {
	id1 := uuid.New()
	id2 := uuid.New()
	f := &fakeServers{
		servers: []store.Server{
			{ID: id1, Hostname: "web-1", Status: store.ServerOnline},
			{ID: id2, Hostname: "web-2", Status: store.ServerOffline},
		},
	}
	svc := NewService(f, nil)
	result, err := svc.List(context.Background(), store.ListServerParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("got %d servers, want 2", len(result))
	}
}

func TestList_StoreError(t *testing.T) {
	f := &fakeServers{err: errors.New("db down")}
	svc := NewService(f, nil)
	_, err := svc.List(context.Background(), store.ListServerParams{})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Register ---

func TestRegister_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.Register(context.Background(), store.RegisterServerParams{Hostname: "web-1"})
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestRegister_MissingHostname(t *testing.T) {
	svc := NewService(&fakeServers{}, nil)
	_, err := svc.Register(context.Background(), store.RegisterServerParams{})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestRegister_HappyPath(t *testing.T) {
	id := uuid.New()
	srv := &store.Server{ID: id, Hostname: "web-1", Status: store.ServerOnline}
	f := &fakeServers{server: srv}
	svc := NewService(f, nil)
	result, err := svc.Register(context.Background(), store.RegisterServerParams{Hostname: "web-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Hostname != "web-1" {
		t.Errorf("hostname = %q, want web-1", result.Hostname)
	}
}

// --- Health ---

func TestHealth_NilRepo(t *testing.T) {
	svc := NewService(&fakeServers{}, nil)
	_, err := svc.Health(context.Background(), uuid.New())
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestHealth_NilServerID(t *testing.T) {
	svc := NewService(nil, &fakeMetrics{})
	_, err := svc.Health(context.Background(), uuid.Nil)
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestHealth_HappyPath(t *testing.T) {
	id := uuid.New()
	f := &fakeMetrics{
		points: []store.MetricPoint{
			{ServerID: id, MetricName: "cpu_pct", MetricValue: 42.5},
			{ServerID: id, MetricName: "mem_pct", MetricValue: 68.0},
		},
	}
	svc := NewService(nil, f)
	result, err := svc.Health(context.Background(), id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("got %d points, want 2", len(result))
	}
}

// --- QueryMetrics ---

func TestQueryMetrics_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.QueryMetrics(context.Background(), store.MetricQuery{ServerID: uuid.New()})
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestQueryMetrics_NilServerID(t *testing.T) {
	svc := NewService(nil, &fakeMetrics{})
	_, err := svc.QueryMetrics(context.Background(), store.MetricQuery{})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestQueryMetrics_HappyPath(t *testing.T) {
	id := uuid.New()
	f := &fakeMetrics{
		points: []store.MetricPoint{
			{ServerID: id, MetricName: "cpu_pct", MetricValue: 42.5},
		},
	}
	svc := NewService(nil, f)
	result, err := svc.QueryMetrics(context.Background(), store.MetricQuery{ServerID: id})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("got %d points, want 1", len(result))
	}
}
