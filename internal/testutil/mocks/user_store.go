package mocks

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/adham90/opentrace/pkg/store"
)

// Compile-time interface checks.
var _ store.UserStore = (*UserStore)(nil)
var _ store.SessionStore = (*SessionStore)(nil)

// UserStore is a thread-safe in-memory mock implementing store.UserStore.
type UserStore struct {
	mu    sync.Mutex
	Users map[string]*store.User
}

// NewUserStore returns an initialised UserStore mock.
func NewUserStore() *UserStore {
	return &UserStore{
		Users: make(map[string]*store.User),
	}
}

func (m *UserStore) Create(_ context.Context, params store.CreateUserParams) (*store.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.Users {
		if u.Email == params.Email {
			return nil, store.ErrEmailTaken
		}
	}
	id := uuid.New().String()
	u := &store.User{
		ID:           id,
		Email:        params.Email,
		PasswordHash: params.PasswordHash,
		DisplayName:  params.DisplayName,
		Role:         params.Role,
		IsActive:     true,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if params.MCPToken != nil {
		u.MCPToken = params.MCPToken
	}
	m.Users[id] = u
	return u, nil
}

func (m *UserStore) GetByID(_ context.Context, id string) (*store.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.Users[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return u, nil
}

func (m *UserStore) GetByEmail(_ context.Context, email string) (*store.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.Users {
		if u.Email == email {
			return u, nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *UserStore) GetByMCPToken(_ context.Context, token string) (*store.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.Users {
		if u.MCPToken != nil && *u.MCPToken == token && u.MCPEnabled && u.IsActive {
			return u, nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *UserStore) List(_ context.Context) ([]store.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]store.User, 0, len(m.Users))
	for _, u := range m.Users {
		result = append(result, *u)
	}
	return result, nil
}

func (m *UserStore) Update(_ context.Context, id string, params store.UpdateUserParams) (*store.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.Users[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	if params.DisplayName != nil {
		u.DisplayName = *params.DisplayName
	}
	if params.Role != nil {
		u.Role = *params.Role
	}
	if params.MCPEnabled != nil {
		u.MCPEnabled = *params.MCPEnabled
	}
	if params.IsActive != nil {
		u.IsActive = *params.IsActive
	}
	u.UpdatedAt = time.Now()
	return u, nil
}

func (m *UserStore) UpdatePassword(_ context.Context, id string, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.Users[id]
	if !ok {
		return store.ErrNotFound
	}
	u.PasswordHash = hash
	return nil
}

func (m *UserStore) UpdateMCPToken(_ context.Context, id string, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.Users[id]
	if !ok {
		return store.ErrNotFound
	}
	u.MCPToken = &token
	return nil
}

func (m *UserStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.Users[id]; !ok {
		return store.ErrNotFound
	}
	delete(m.Users, id)
	return nil
}

func (m *UserStore) Count(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.Users), nil
}

// SessionStore is a thread-safe in-memory mock implementing store.SessionStore.
type SessionStore struct {
	mu       sync.Mutex
	Sessions map[string]*store.Session
}

// NewSessionStore returns an initialised SessionStore mock.
func NewSessionStore() *SessionStore {
	return &SessionStore{
		Sessions: make(map[string]*store.Session),
	}
}

func (m *SessionStore) Create(_ context.Context, userID string, token string, expiresAt time.Time) (*store.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := &store.Session{
		ID:        uuid.New().String(),
		UserID:    userID,
		Token:     token,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	}
	m.Sessions[s.ID] = s
	return s, nil
}

func (m *SessionStore) GetByToken(_ context.Context, token string) (*store.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.Sessions {
		if s.Token == token && s.ExpiresAt.After(time.Now()) {
			return s, nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *SessionStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.Sessions, id)
	return nil
}

func (m *SessionStore) DeleteExpired(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for id, s := range m.Sessions {
		if s.ExpiresAt.Before(time.Now()) {
			delete(m.Sessions, id)
			count++
		}
	}
	return count, nil
}

func (m *SessionStore) DeleteAllForUser(_ context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, s := range m.Sessions {
		if s.UserID == userID {
			delete(m.Sessions, id)
		}
	}
	return nil
}
