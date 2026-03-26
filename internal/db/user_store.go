package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"
	"github.com/adham90/opentrace/pkg/store"
)

type userStore struct {
	db *sql.DB
	q  *Queries
}

func NewUserStore(db *sql.DB) store.UserStore {
	return &userStore{db: db, q: New(db)}
}

func (s *userStore) Create(ctx context.Context, params store.CreateUserParams) (*store.User, error) {
	id := uuid.New().String()
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)

	role := params.Role
	if role == "" {
		role = store.RoleMember
	}

	var mcpToken sql.NullString
	if params.MCPToken != nil {
		mcpToken = sql.NullString{String: *params.MCPToken, Valid: true}
	}

	err := s.q.CreateUser(ctx, CreateUserParams{
		ID:           id,
		Email:        params.Email,
		PasswordHash: params.PasswordHash,
		DisplayName:  params.DisplayName,
		Role:         string(role),
		McpToken:     mcpToken,
		CreatedAt:    nowStr,
		UpdatedAt:    nowStr,
	})
	if err != nil {
		if isUniqueConstraintError(err, "users.email") {
			return nil, store.ErrEmailTaken
		}
		return nil, fmt.Errorf("creating user: %w", err)
	}

	return &store.User{
		ID:           id,
		Email:        params.Email,
		PasswordHash: params.PasswordHash,
		DisplayName:  params.DisplayName,
		Role:         role,
		MCPToken:     params.MCPToken,
		IsActive:     true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

func (s *userStore) GetByID(ctx context.Context, id string) (*store.User, error) {
	row, err := s.q.GetUserByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning user: %w", err)
	}
	return userToStore(row), nil
}

func (s *userStore) GetByEmail(ctx context.Context, email string) (*store.User, error) {
	row, err := s.q.GetUserByEmail(ctx, email)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning user: %w", err)
	}
	return userToStore(row), nil
}

func (s *userStore) GetByMCPToken(ctx context.Context, token string) (*store.User, error) {
	row, err := s.q.GetUserByMCPToken(ctx, sql.NullString{String: token, Valid: true})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning user: %w", err)
	}
	return userToStore(row), nil
}

func (s *userStore) List(ctx context.Context) ([]store.User, error) {
	rows, err := s.q.ListUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing users: %w", err)
	}
	result := usersToStore(rows)
	if result == nil {
		result = []store.User{}
	}
	return result, nil
}

func (s *userStore) Update(ctx context.Context, id string, params store.UpdateUserParams) (*store.User, error) {
	// If demoting from admin, check last-admin constraint.
	if params.Role != nil && *params.Role == store.RoleMember {
		if err := s.checkLastAdmin(ctx, id); err != nil {
			return nil, err
		}
	}
	if params.IsActive != nil && !*params.IsActive {
		if err := s.checkLastAdmin(ctx, id); err != nil {
			return nil, err
		}
	}

	// Use squirrel for dynamic UPDATE
	qb := psql.Update("users").Set("updated_at", time.Now().UTC().Format(time.RFC3339))

	if params.DisplayName != nil {
		qb = qb.Set("display_name", *params.DisplayName)
	}
	if params.Role != nil {
		qb = qb.Set("role", string(*params.Role))
	}
	if params.MCPEnabled != nil {
		qb = qb.Set("mcp_enabled", boolToInt64(*params.MCPEnabled))
	}
	if params.IsActive != nil {
		qb = qb.Set("is_active", boolToInt64(*params.IsActive))
	}

	qb = qb.Where(sq.Eq{"id": id})

	query, args, err := qb.ToSql()
	if err != nil {
		return nil, fmt.Errorf("building update query: %w", err)
	}

	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("updating user: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("checking rows affected: %w", err)
	}
	if n == 0 {
		return nil, store.ErrNotFound
	}
	return s.GetByID(ctx, id)
}

func (s *userStore) UpdatePassword(ctx context.Context, id string, passwordHash string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	n, err := s.q.UpdatePassword(ctx, UpdatePasswordParams{
		PasswordHash: passwordHash,
		UpdatedAt:    now,
		ID:           id,
	})
	if err != nil {
		return fmt.Errorf("updating password: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *userStore) UpdateMCPToken(ctx context.Context, id string, token string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	n, err := s.q.UpdateMCPToken(ctx, UpdateMCPTokenParams{
		McpToken:  sql.NullString{String: token, Valid: true},
		UpdatedAt: now,
		ID:        id,
	})
	if err != nil {
		return fmt.Errorf("updating mcp token: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *userStore) Delete(ctx context.Context, id string) error {
	if err := s.checkLastAdmin(ctx, id); err != nil {
		return err
	}

	n, err := s.q.DeleteUser(ctx, id)
	if err != nil {
		return fmt.Errorf("deleting user: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *userStore) Count(ctx context.Context) (int, error) {
	n, err := s.q.CountUsers(ctx)
	if err != nil {
		return 0, fmt.Errorf("counting users: %w", err)
	}
	return int(n), nil
}

// checkLastAdmin returns ErrLastAdmin if the given user is the only active admin.
func (s *userStore) checkLastAdmin(ctx context.Context, userID string) error {
	user, err := s.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if user.Role != store.RoleAdmin {
		return nil
	}
	count, err := s.q.CountActiveAdminsExcluding(ctx, userID)
	if err != nil {
		return fmt.Errorf("checking admin count: %w", err)
	}
	if count == 0 {
		return store.ErrLastAdmin
	}
	return nil
}
