package store

import (
	"time"

	"github.com/uptrace/bun"
)

// UserRole represents the access level of a user.
type UserRole string

const (
	RoleAdmin  UserRole = "admin"
	RoleMember UserRole = "member"
)

// User represents an authenticated user.
//
// AllowedEnvironments controls which envs this user's MCP token may read/write.
// Values:
//   - []string{}            — no scope granted; all env-scoped tool calls deny.
//   - []string{"production"} — single-env scope; ResolveEnv auto-fills.
//   - []string{"staging", "production"} — multi-env; explicit env arg required.
//   - []string{"*"}          — legacy wildcard; all envs, with deprecation fallback
//                              on writes (see internal/mcp/envscope.go).
type User struct {
	bun.BaseModel        `bun:"table:users" json:"-"`
	ID                   string    `bun:"id,pk" json:"id"`
	Email                string    `bun:"email" json:"email"`
	PasswordHash         string    `bun:"password_hash" json:"-"`
	DisplayName          string    `bun:"display_name" json:"display_name"`
	Role                 UserRole  `bun:"role" json:"role"`
	MCPEnabled           bool      `bun:"mcp_enabled" json:"mcp_enabled"`
	MCPToken             *string   `bun:"mcp_token" json:"mcp_token,omitempty"`
	IsActive             bool      `bun:"is_active" json:"is_active"`
	AllowedEnvironments  []string  `bun:"-" json:"allowed_environments"`
	CreatedAt            time.Time `bun:"created_at" json:"created_at"`
	UpdatedAt            time.Time `bun:"updated_at" json:"updated_at"`
}

// CreateUserParams defines the input for creating a user.
type CreateUserParams struct {
	Email               string   `json:"email"`
	PasswordHash        string   `json:"-"`
	DisplayName         string   `json:"display_name"`
	Role                UserRole `json:"role"`
	MCPToken            *string  `json:"-"`
	AllowedEnvironments []string `json:"allowed_environments,omitempty"`
}

// UpdateUserParams defines the input for updating a user.
type UpdateUserParams struct {
	DisplayName         *string   `json:"display_name,omitempty"`
	Role                *UserRole `json:"role,omitempty"`
	MCPEnabled          *bool     `json:"mcp_enabled,omitempty"`
	IsActive            *bool     `json:"is_active,omitempty"`
	AllowedEnvironments *[]string `json:"allowed_environments,omitempty"`
}

// Session represents an authenticated browser session.
type Session struct {
	bun.BaseModel `bun:"table:sessions" json:"-"`
	ID            string    `bun:"id,pk" json:"id"`
	UserID        string    `bun:"user_id" json:"user_id"`
	Token         string    `bun:"token" json:"-"`
	ExpiresAt     time.Time `bun:"expires_at" json:"expires_at"`
	CreatedAt     time.Time `bun:"created_at" json:"created_at"`
}
