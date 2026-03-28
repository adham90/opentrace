package errors

import (
	"context"
	stderrors "errors"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/domain"
	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// Fake repository
// ---------------------------------------------------------------------------

type fakeRepo struct {
	groups    []store.ErrorGroup
	group     *store.ErrorGroup
	events    []store.ErrorGroupEvent
	count     int
	listErr   error
	getErr    error
	resolveErr error
	ignoreErr  error
}

func (f *fakeRepo) List(_ context.Context, _ store.ListErrorGroupParams) ([]store.ErrorGroup, error) {
	return f.groups, f.listErr
}

func (f *fakeRepo) Get(_ context.Context, fp string) (*store.ErrorGroup, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.group != nil {
		return f.group, nil
	}
	return nil, nil
}

func (f *fakeRepo) Count(_ context.Context, _ store.ErrorGroupStatus) (int, error) {
	return f.count, nil
}

func (f *fakeRepo) Resolve(_ context.Context, _ string, _ string) error {
	return f.resolveErr
}

func (f *fakeRepo) Ignore(_ context.Context, _ string, _ string) error {
	return f.ignoreErr
}

func (f *fakeRepo) ListEvents(_ context.Context, _ string, _ int) ([]store.ErrorGroupEvent, error) {
	return f.events, nil
}

// Compile-time check.
var _ Repository = (*fakeRepo)(nil)

// ---------------------------------------------------------------------------
// Fake impact repository
// ---------------------------------------------------------------------------

type fakeImpact struct {
	impact *store.ErrorImpact
	users  []store.AffectedUser
	impErr error
}

func (f *fakeImpact) GetImpact(_ context.Context, _ string) (*store.ErrorImpact, error) {
	return f.impact, f.impErr
}

func (f *fakeImpact) GetAffectedUsers(_ context.Context, _ string, _ int) ([]store.AffectedUser, error) {
	return f.users, nil
}

func (f *fakeImpact) GetUserErrors(_ context.Context, _ string, _ time.Time) ([]store.ErrorSummary, error) {
	return nil, nil
}

// Compile-time check.
var _ ImpactRepository = (*fakeImpact)(nil)

// ---------------------------------------------------------------------------
// List tests
// ---------------------------------------------------------------------------

func TestList_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.List(context.Background(), ListParams{})
	if !stderrors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestList_Empty(t *testing.T) {
	svc := NewService(&fakeRepo{}, nil)
	result, err := svc.List(context.Background(), ListParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Returned != 0 {
		t.Errorf("expected 0 returned, got %d", result.Returned)
	}
}

func TestList_WithData(t *testing.T) {
	now := time.Now()
	repo := &fakeRepo{
		groups: []store.ErrorGroup{
			{
				Fingerprint:     "fp1",
				Service:         "api",
				ExceptionClass:  "RuntimeError",
				Message:         "something broke",
				Status:          store.ErrorGroupUnresolved,
				OccurrenceCount: 42,
				FirstSeenAt:     now.Add(-time.Hour),
				LastSeenAt:      now,
			},
			{
				Fingerprint:     "fp2",
				Service:         "worker",
				ExceptionClass:  "TimeoutError",
				Message:         "timed out",
				Status:          store.ErrorGroupUnresolved,
				OccurrenceCount: 7,
				FirstSeenAt:     now.Add(-2 * time.Hour),
				LastSeenAt:      now.Add(-30 * time.Minute),
			},
		},
		count: 2,
	}
	svc := NewService(repo, nil)
	result, err := svc.List(context.Background(), ListParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Returned != 2 {
		t.Errorf("expected 2 returned, got %d", result.Returned)
	}
	if result.TotalUnresolved != 2 {
		t.Errorf("expected 2 unresolved, got %d", result.TotalUnresolved)
	}
	if result.ErrorGroups[0].Fingerprint != "fp1" {
		t.Errorf("expected fp1, got %s", result.ErrorGroups[0].Fingerprint)
	}
}

func TestList_LimitCapped(t *testing.T) {
	svc := NewService(&fakeRepo{}, nil)
	result, err := svc.List(context.Background(), ListParams{Limit: 9999})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestList_StoreError(t *testing.T) {
	repo := &fakeRepo{listErr: stderrors.New("db down")}
	svc := NewService(repo, nil)
	_, err := svc.List(context.Background(), ListParams{})
	if err == nil {
		t.Fatal("expected error")
	}
}

// ---------------------------------------------------------------------------
// Detail tests
// ---------------------------------------------------------------------------

func TestDetail_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.Detail(context.Background(), "fp1")
	if !stderrors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestDetail_EmptyFingerprint(t *testing.T) {
	svc := NewService(&fakeRepo{}, nil)
	_, err := svc.Detail(context.Background(), "")
	if !stderrors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestDetail_NotFound(t *testing.T) {
	svc := NewService(&fakeRepo{}, nil)
	_, err := svc.Detail(context.Background(), "nonexistent")
	if !stderrors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDetail_Found(t *testing.T) {
	now := time.Now()
	repo := &fakeRepo{
		group: &store.ErrorGroup{
			Fingerprint:     "fp1",
			Service:         "api",
			ExceptionClass:  "RuntimeError",
			Message:         "boom",
			Status:          store.ErrorGroupUnresolved,
			OccurrenceCount: 5,
			FirstSeenAt:     now.Add(-time.Hour),
			LastSeenAt:      now,
		},
		events: []store.ErrorGroupEvent{
			{ID: 1, Fingerprint: "fp1", Action: "reopened", CreatedAt: now},
		},
	}
	svc := NewService(repo, nil)
	result, err := svc.Detail(context.Background(), "fp1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Group.Fingerprint != "fp1" {
		t.Errorf("expected fp1, got %s", result.Group.Fingerprint)
	}
	if len(result.Events) != 1 {
		t.Errorf("expected 1 event, got %d", len(result.Events))
	}
}

func TestDetail_GetError(t *testing.T) {
	repo := &fakeRepo{getErr: stderrors.New("db error")}
	svc := NewService(repo, nil)
	_, err := svc.Detail(context.Background(), "fp1")
	if err == nil {
		t.Fatal("expected error")
	}
}

// ---------------------------------------------------------------------------
// Resolve tests
// ---------------------------------------------------------------------------

func TestResolve_NilRepo(t *testing.T) {
	svc := &Service{}
	err := svc.Resolve(context.Background(), "fp1", "fixed")
	if !stderrors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestResolve_EmptyFingerprint(t *testing.T) {
	svc := NewService(&fakeRepo{}, nil)
	err := svc.Resolve(context.Background(), "", "fixed")
	if !stderrors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestResolve_EmptyReason(t *testing.T) {
	svc := NewService(&fakeRepo{}, nil)
	err := svc.Resolve(context.Background(), "fp1", "")
	if !stderrors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestResolve_Success(t *testing.T) {
	svc := NewService(&fakeRepo{}, nil)
	err := svc.Resolve(context.Background(), "fp1", "Fixed in PR #42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Ignore tests
// ---------------------------------------------------------------------------

func TestIgnore_NilRepo(t *testing.T) {
	svc := &Service{}
	err := svc.Ignore(context.Background(), "fp1", "noise")
	if !stderrors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestIgnore_EmptyFingerprint(t *testing.T) {
	svc := NewService(&fakeRepo{}, nil)
	err := svc.Ignore(context.Background(), "", "noise")
	if !stderrors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestIgnore_EmptyReason(t *testing.T) {
	svc := NewService(&fakeRepo{}, nil)
	err := svc.Ignore(context.Background(), "fp1", "")
	if !stderrors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestIgnore_Success(t *testing.T) {
	svc := NewService(&fakeRepo{}, nil)
	err := svc.Ignore(context.Background(), "fp1", "Known noise from health checks")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Impact tests
// ---------------------------------------------------------------------------

func TestImpact_NilImpactRepo(t *testing.T) {
	svc := NewService(&fakeRepo{}, nil)
	_, err := svc.Impact(context.Background(), "fp1", 10)
	if !stderrors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestImpact_EmptyFingerprint(t *testing.T) {
	svc := NewService(&fakeRepo{}, &fakeImpact{})
	_, err := svc.Impact(context.Background(), "", 10)
	if !stderrors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestImpact_Success(t *testing.T) {
	now := time.Now()
	imp := &fakeImpact{
		impact: &store.ErrorImpact{
			Fingerprint:      "fp1",
			UniqueUsers:      3,
			TotalOccurrences: 15,
			ImpactScore:      0.85,
		},
		users: []store.AffectedUser{
			{UserID: "user1", OccurrenceCount: 10, FirstSeenAt: now.Add(-time.Hour), LastSeenAt: now},
			{UserID: "user2", OccurrenceCount: 5, FirstSeenAt: now.Add(-30 * time.Minute), LastSeenAt: now},
		},
	}
	svc := NewService(&fakeRepo{}, imp)
	result, err := svc.Impact(context.Background(), "fp1", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Impact.UniqueUsers != 3 {
		t.Errorf("expected 3 unique users, got %d", result.Impact.UniqueUsers)
	}
	if len(result.AffectedUsers) != 2 {
		t.Errorf("expected 2 affected users, got %d", len(result.AffectedUsers))
	}
}

func TestImpact_StoreError(t *testing.T) {
	imp := &fakeImpact{impErr: stderrors.New("db error")}
	svc := NewService(&fakeRepo{}, imp)
	_, err := svc.Impact(context.Background(), "fp1", 10)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestImpact_DefaultLimit(t *testing.T) {
	imp := &fakeImpact{
		impact: &store.ErrorImpact{Fingerprint: "fp1"},
	}
	svc := NewService(&fakeRepo{}, imp)
	result, err := svc.Impact(context.Background(), "fp1", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}
