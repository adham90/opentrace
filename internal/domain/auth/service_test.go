package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/adham90/opentrace/internal/domain"
	"github.com/adham90/opentrace/pkg/store"
)

// fakeUserRepo implements UserRepository for testing.
type fakeUserRepo struct {
	user  *store.User
	users []store.User
	count int
	err   error
}

func (f *fakeUserRepo) GetByID(_ context.Context, _ string) (*store.User, error) {
	return f.user, f.err
}

func (f *fakeUserRepo) GetByEmail(_ context.Context, _ string) (*store.User, error) {
	return f.user, f.err
}

func (f *fakeUserRepo) GetByMCPToken(_ context.Context, _ string) (*store.User, error) {
	return f.user, f.err
}

func (f *fakeUserRepo) Create(_ context.Context, _ store.CreateUserParams) (*store.User, error) {
	return f.user, f.err
}

func (f *fakeUserRepo) Update(_ context.Context, _ string, _ store.UpdateUserParams) (*store.User, error) {
	return f.user, f.err
}

func (f *fakeUserRepo) Delete(_ context.Context, _ string) error {
	return f.err
}

func (f *fakeUserRepo) List(_ context.Context) ([]store.User, error) {
	return f.users, f.err
}

func (f *fakeUserRepo) Count(_ context.Context) (int, error) {
	return f.count, f.err
}

// Compile-time check.
var _ UserRepository = (*fakeUserRepo)(nil)

// --- Authenticate ---

func TestAuthenticate_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.Authenticate(context.Background(), "test@example.com")
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestAuthenticate_EmptyEmail(t *testing.T) {
	svc := NewService(&fakeUserRepo{})
	_, err := svc.Authenticate(context.Background(), "")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestAuthenticate_Success(t *testing.T) {
	f := &fakeUserRepo{user: &store.User{ID: "u1", Email: "test@example.com"}}
	svc := NewService(f)
	user, err := svc.Authenticate(context.Background(), "test@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user.Email != "test@example.com" {
		t.Errorf("email = %q, want 'test@example.com'", user.Email)
	}
}

func TestAuthenticate_NotFound(t *testing.T) {
	f := &fakeUserRepo{err: store.ErrNotFound}
	svc := NewService(f)
	_, err := svc.Authenticate(context.Background(), "missing@example.com")
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- ValidateMCPToken ---

func TestValidateMCPToken_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.ValidateMCPToken(context.Background(), "tok-123")
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestValidateMCPToken_EmptyToken(t *testing.T) {
	svc := NewService(&fakeUserRepo{})
	_, err := svc.ValidateMCPToken(context.Background(), "")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestValidateMCPToken_Success(t *testing.T) {
	f := &fakeUserRepo{user: &store.User{ID: "u1", Email: "test@example.com"}}
	svc := NewService(f)
	user, err := svc.ValidateMCPToken(context.Background(), "tok-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user.ID != "u1" {
		t.Errorf("id = %q, want 'u1'", user.ID)
	}
}

// --- CreateUser ---

func TestCreateUser_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.CreateUser(context.Background(), store.CreateUserParams{Email: "a@b.com"})
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestCreateUser_EmptyEmail(t *testing.T) {
	svc := NewService(&fakeUserRepo{})
	_, err := svc.CreateUser(context.Background(), store.CreateUserParams{})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestCreateUser_Success(t *testing.T) {
	f := &fakeUserRepo{user: &store.User{ID: "u1", Email: "new@example.com"}}
	svc := NewService(f)
	user, err := svc.CreateUser(context.Background(), store.CreateUserParams{Email: "new@example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user.Email != "new@example.com" {
		t.Errorf("email = %q, want 'new@example.com'", user.Email)
	}
}

// --- GetUser ---

func TestGetUser_EmptyID(t *testing.T) {
	svc := NewService(&fakeUserRepo{})
	_, err := svc.GetUser(context.Background(), "")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

// --- DeleteUser ---

func TestDeleteUser_EmptyID(t *testing.T) {
	svc := NewService(&fakeUserRepo{})
	err := svc.DeleteUser(context.Background(), "")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

// --- ListUsers ---

func TestListUsers_NilRepo(t *testing.T) {
	svc := &Service{}
	_, err := svc.ListUsers(context.Background())
	if !errors.Is(err, domain.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestListUsers_Success(t *testing.T) {
	f := &fakeUserRepo{users: []store.User{{ID: "u1"}, {ID: "u2"}}}
	svc := NewService(f)
	users, err := svc.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(users) != 2 {
		t.Errorf("len = %d, want 2", len(users))
	}
}
