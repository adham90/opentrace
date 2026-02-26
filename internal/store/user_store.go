package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type userStore struct {
	db *sql.DB
}

func NewUserStore(db *sql.DB) UserStore {
	return &userStore{db: db}
}

func (s *userStore) Create(ctx context.Context, params CreateUserParams) (*User, error) {
	id := uuid.New().String()
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)

	role := params.Role
	if role == "" {
		role = RoleMember
	}

	var mcpToken *string
	if params.MCPToken != nil {
		mcpToken = params.MCPToken
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, display_name, role, mcp_token, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, params.Email, params.PasswordHash, params.DisplayName, string(role), mcpToken, nowStr, nowStr,
	)
	if err != nil {
		if isUniqueConstraintError(err, "users.email") {
			return nil, ErrEmailTaken
		}
		return nil, fmt.Errorf("creating user: %w", err)
	}

	return &User{
		ID:           id,
		Email:        params.Email,
		PasswordHash: params.PasswordHash,
		DisplayName:  params.DisplayName,
		Role:         role,
		MCPToken:     mcpToken,
		IsActive:     true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

func (s *userStore) GetByID(ctx context.Context, id string) (*User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, email, password_hash, display_name, role, mcp_enabled, mcp_token, is_active, created_at, updated_at
		 FROM users WHERE id = ?`, id,
	))
}

func (s *userStore) GetByEmail(ctx context.Context, email string) (*User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, email, password_hash, display_name, role, mcp_enabled, mcp_token, is_active, created_at, updated_at
		 FROM users WHERE email = ?`, email,
	))
}

func (s *userStore) GetByMCPToken(ctx context.Context, token string) (*User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, email, password_hash, display_name, role, mcp_enabled, mcp_token, is_active, created_at, updated_at
		 FROM users WHERE mcp_token = ? AND mcp_enabled = 1 AND is_active = 1`, token,
	))
}

func (s *userStore) List(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, email, password_hash, display_name, role, mcp_enabled, mcp_token, is_active, created_at, updated_at
		 FROM users ORDER BY created_at ASC LIMIT 1000`,
	)
	if err != nil {
		return nil, fmt.Errorf("listing users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		u, err := s.scanUserRow(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, *u)
	}
	if users == nil {
		users = []User{}
	}
	return users, rows.Err()
}

func (s *userStore) Update(ctx context.Context, id string, params UpdateUserParams) (*User, error) {
	// If demoting from admin, check last-admin constraint.
	if params.Role != nil && *params.Role == RoleMember {
		if err := s.checkLastAdmin(ctx, id); err != nil {
			return nil, err
		}
	}
	if params.IsActive != nil && !*params.IsActive {
		if err := s.checkLastAdmin(ctx, id); err != nil {
			return nil, err
		}
	}

	sets := []string{"updated_at = ?"}
	args := []any{time.Now().UTC().Format(time.RFC3339)}

	if params.DisplayName != nil {
		sets = append(sets, "display_name = ?")
		args = append(args, *params.DisplayName)
	}
	if params.Role != nil {
		sets = append(sets, "role = ?")
		args = append(args, string(*params.Role))
	}
	if params.MCPEnabled != nil {
		v := 0
		if *params.MCPEnabled {
			v = 1
		}
		sets = append(sets, "mcp_enabled = ?")
		args = append(args, v)
	}
	if params.IsActive != nil {
		v := 0
		if *params.IsActive {
			v = 1
		}
		sets = append(sets, "is_active = ?")
		args = append(args, v)
	}

	args = append(args, id)
	// sets contains only hardcoded column names from the switch cases above — safe for interpolation.
	query := fmt.Sprintf("UPDATE users SET %s WHERE id = ?", strings.Join(sets, ", "))
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("updating user: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("checking rows affected: %w", err)
	}
	if n == 0 {
		return nil, ErrNotFound
	}
	return s.GetByID(ctx, id)
}

func (s *userStore) UpdatePassword(ctx context.Context, id string, passwordHash string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := s.db.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`,
		passwordHash, now, id,
	)
	if err != nil {
		return fmt.Errorf("updating password: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *userStore) UpdateMCPToken(ctx context.Context, id string, token string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := s.db.ExecContext(ctx,
		`UPDATE users SET mcp_token = ?, updated_at = ? WHERE id = ?`,
		token, now, id,
	)
	if err != nil {
		return fmt.Errorf("updating mcp token: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *userStore) Delete(ctx context.Context, id string) error {
	if err := s.checkLastAdmin(ctx, id); err != nil {
		return err
	}

	result, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting user: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *userStore) Count(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting users: %w", err)
	}
	return count, nil
}

// checkLastAdmin returns ErrLastAdmin if the given user is the only active admin.
func (s *userStore) checkLastAdmin(ctx context.Context, userID string) error {
	user, err := s.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if user.Role != RoleAdmin {
		return nil
	}
	var count int
	err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE role = 'admin' AND is_active = 1 AND id != ?`, userID,
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("checking admin count: %w", err)
	}
	if count == 0 {
		return ErrLastAdmin
	}
	return nil
}

func (s *userStore) scanUser(row *sql.Row) (*User, error) {
	u := &User{}
	var createdAt, updatedAt string
	var mcpEnabledInt, isActiveInt int
	var mcpToken sql.NullString

	err := row.Scan(
		&u.ID, &u.Email, &u.PasswordHash, &u.DisplayName, &u.Role,
		&mcpEnabledInt, &mcpToken, &isActiveInt, &createdAt, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scanning user: %w", err)
	}

	u.MCPEnabled = mcpEnabledInt != 0
	u.IsActive = isActiveInt != 0
	if mcpToken.Valid {
		u.MCPToken = &mcpToken.String
	}
	u.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	u.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return u, nil
}

func (s *userStore) scanUserRow(rows *sql.Rows) (*User, error) {
	u := &User{}
	var createdAt, updatedAt string
	var mcpEnabledInt, isActiveInt int
	var mcpToken sql.NullString

	err := rows.Scan(
		&u.ID, &u.Email, &u.PasswordHash, &u.DisplayName, &u.Role,
		&mcpEnabledInt, &mcpToken, &isActiveInt, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning user row: %w", err)
	}

	u.MCPEnabled = mcpEnabledInt != 0
	u.IsActive = isActiveInt != 0
	if mcpToken.Valid {
		u.MCPToken = &mcpToken.String
	}
	u.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	u.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return u, nil
}
