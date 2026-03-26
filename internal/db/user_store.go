package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/adham90/opentrace/pkg/store"
)

type userStore struct {
	db *bun.DB
}

func NewUserStore(db *bun.DB) store.UserStore {
	return &userStore{db: db}
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

	_, err := s.db.NewRaw(`
		INSERT INTO users (id, email, password_hash, display_name, role, mcp_token, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, params.Email, params.PasswordHash, params.DisplayName,
		string(role), mcpToken, nowStr, nowStr,
	).Exec(ctx)
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
	return s.getUser(ctx, "id = ?", id)
}

func (s *userStore) GetByEmail(ctx context.Context, email string) (*store.User, error) {
	return s.getUser(ctx, "email = ?", email)
}

func (s *userStore) GetByMCPToken(ctx context.Context, token string) (*store.User, error) {
	return s.getUser(ctx, "mcp_token = ? AND mcp_enabled = 1 AND is_active = 1", token)
}

func (s *userStore) getUser(ctx context.Context, whereClause string, arg any) (*store.User, error) {
	var u userRow
	err := s.db.NewRaw(`
		SELECT id, email, password_hash, display_name, role, mcp_token, mcp_enabled,
			is_active, created_at, updated_at
		FROM users WHERE `+whereClause, arg,
	).Scan(ctx,
		&u.ID, &u.Email, &u.PasswordHash, &u.DisplayName, &u.Role,
		&u.MCPToken, &u.MCPEnabled, &u.IsActive, &u.CreatedAt, &u.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning user: %w", err)
	}
	return u.toStore(), nil
}

func (s *userStore) List(ctx context.Context) ([]store.User, error) {
	type row struct {
		ID           string         `bun:"id"`
		Email        string         `bun:"email"`
		PasswordHash string         `bun:"password_hash"`
		DisplayName  string         `bun:"display_name"`
		Role         string         `bun:"role"`
		MCPToken     sql.NullString `bun:"mcp_token"`
		MCPEnabled   int64          `bun:"mcp_enabled"`
		IsActive     int64          `bun:"is_active"`
		CreatedAt    string         `bun:"created_at"`
		UpdatedAt    string         `bun:"updated_at"`
	}

	var rows []row
	err := s.db.NewRaw(`
		SELECT id, email, password_hash, display_name, role, mcp_token, mcp_enabled,
			is_active, created_at, updated_at
		FROM users ORDER BY created_at ASC`,
	).Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("listing users: %w", err)
	}

	result := make([]store.User, len(rows))
	for i, r := range rows {
		result[i] = store.User{
			ID:           r.ID,
			Email:        r.Email,
			PasswordHash: r.PasswordHash,
			DisplayName:  r.DisplayName,
			Role:         store.UserRole(r.Role),
			MCPEnabled:   r.MCPEnabled == 1,
			IsActive:     r.IsActive == 1,
			CreatedAt:    parseTime(r.CreatedAt),
			UpdatedAt:    parseTime(r.UpdatedAt),
		}
		if r.MCPToken.Valid {
			result[i].MCPToken = &r.MCPToken.String
		}
	}
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

	// Build dynamic UPDATE
	var sets []string
	var args []any

	sets = append(sets, "updated_at = ?")
	args = append(args, time.Now().UTC().Format(time.RFC3339))

	if params.DisplayName != nil {
		sets = append(sets, "display_name = ?")
		args = append(args, *params.DisplayName)
	}
	if params.Role != nil {
		sets = append(sets, "role = ?")
		args = append(args, string(*params.Role))
	}
	if params.MCPEnabled != nil {
		sets = append(sets, "mcp_enabled = ?")
		args = append(args, boolToInt64(*params.MCPEnabled))
	}
	if params.IsActive != nil {
		sets = append(sets, "is_active = ?")
		args = append(args, boolToInt64(*params.IsActive))
	}

	args = append(args, id)
	query := fmt.Sprintf("UPDATE users SET %s WHERE id = ?", strings.Join(sets, ", "))

	res, err := s.db.NewRaw(query, args...).Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("updating user: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, store.ErrNotFound
	}
	return s.GetByID(ctx, id)
}

func (s *userStore) UpdatePassword(ctx context.Context, id string, passwordHash string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.NewRaw(`
		UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`,
		passwordHash, now, id,
	).Exec(ctx)
	if err != nil {
		return fmt.Errorf("updating password: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *userStore) UpdateMCPToken(ctx context.Context, id string, token string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.NewRaw(`
		UPDATE users SET mcp_token = ?, updated_at = ? WHERE id = ?`,
		sql.NullString{String: token, Valid: true}, now, id,
	).Exec(ctx)
	if err != nil {
		return fmt.Errorf("updating mcp token: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *userStore) Delete(ctx context.Context, id string) error {
	if err := s.checkLastAdmin(ctx, id); err != nil {
		return err
	}

	res, err := s.db.NewRaw(`DELETE FROM users WHERE id = ?`, id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("deleting user: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *userStore) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.NewRaw(`SELECT COUNT(*) FROM users`).Scan(ctx, &n)
	if err != nil {
		return 0, fmt.Errorf("counting users: %w", err)
	}
	return n, nil
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
	var count int
	err = s.db.NewRaw(`
		SELECT COUNT(*) FROM users
		WHERE role = 'admin' AND is_active = 1 AND id != ?`, userID,
	).Scan(ctx, &count)
	if err != nil {
		return fmt.Errorf("checking admin count: %w", err)
	}
	if count == 0 {
		return store.ErrLastAdmin
	}
	return nil
}

// userRow is a scan helper for user queries.
type userRow struct {
	ID           string
	Email        string
	PasswordHash string
	DisplayName  string
	Role         string
	MCPToken     sql.NullString
	MCPEnabled   int64
	IsActive     int64
	CreatedAt    string
	UpdatedAt    string
}

func (r *userRow) toStore() *store.User {
	u := &store.User{
		ID:           r.ID,
		Email:        r.Email,
		PasswordHash: r.PasswordHash,
		DisplayName:  r.DisplayName,
		Role:         store.UserRole(r.Role),
		MCPEnabled:   r.MCPEnabled == 1,
		IsActive:     r.IsActive == 1,
		CreatedAt:    parseTime(r.CreatedAt),
		UpdatedAt:    parseTime(r.UpdatedAt),
	}
	if r.MCPToken.Valid {
		u.MCPToken = &r.MCPToken.String
	}
	return u
}
