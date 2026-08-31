package auth

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/pkg/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// Mock Stores for testing
// ─────────────────────────────────────────────────────────────────────────────

type mockUserStore struct {
	mu        sync.Mutex
	users     map[string]*store.User
	countErr  error
	getErr    error
	createErr error
	updateErr error
}

func newMockUserStore() *mockUserStore {
	return &mockUserStore{
		users: make(map[string]*store.User),
	}
}

func (m *mockUserStore) Count(ctx context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.countErr != nil {
		return 0, m.countErr
	}
	return len(m.users), nil
}

func (m *mockUserStore) Create(ctx context.Context, params store.CreateUserParams) (*store.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createErr != nil {
		return nil, m.createErr
	}

	user := &store.User{
		ID:           "test-" + params.Email,
		Email:        params.Email,
		PasswordHash: params.PasswordHash,
		DisplayName:  params.DisplayName,
		Role:         params.Role,
		MCPToken:     params.MCPToken,
		MCPEnabled:   true,
		IsActive:     true,
	}
	m.users[params.Email] = user
	return user, nil
}

func (m *mockUserStore) GetByEmail(ctx context.Context, email string) (*store.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getErr != nil {
		return nil, m.getErr
	}
	user, ok := m.users[email]
	if !ok {
		return nil, store.ErrNotFound
	}
	return user, nil
}

func (m *mockUserStore) GetByID(ctx context.Context, id string) (*store.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.ID == id {
			return u, nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *mockUserStore) GetByMCPToken(ctx context.Context, token string) (*store.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.MCPToken != nil && *u.MCPToken == token {
			return u, nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *mockUserStore) List(ctx context.Context) ([]store.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]store.User, 0)
	for _, u := range m.users {
		result = append(result, *u)
	}
	return result, nil
}

func (m *mockUserStore) Update(ctx context.Context, id string, params store.UpdateUserParams) (*store.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.updateErr != nil {
		return nil, m.updateErr
	}
	for _, u := range m.users {
		if u.ID == id {
			if params.MCPEnabled != nil {
				u.MCPEnabled = *params.MCPEnabled
			}
			return u, nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *mockUserStore) UpdatePassword(ctx context.Context, id string, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.ID == id {
			u.PasswordHash = hash
			return nil
		}
	}
	return store.ErrNotFound
}

func (m *mockUserStore) UpdateMCPToken(ctx context.Context, id string, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.ID == id {
			u.MCPToken = &token
			return nil
		}
	}
	return store.ErrNotFound
}

func (m *mockUserStore) Delete(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for email, u := range m.users {
		if u.ID == id {
			delete(m.users, email)
			return nil
		}
	}
	return store.ErrNotFound
}

type mockAuditStore struct {
	mu     sync.Mutex
	logs   []store.AuditEntry
	logErr error
}

func newMockAuditStore() *mockAuditStore {
	return &mockAuditStore{
		logs: make([]store.AuditEntry, 0),
	}
}

func (m *mockAuditStore) Log(ctx context.Context, params store.LogAuditParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.logErr != nil {
		return m.logErr
	}
	return nil
}

func (m *mockAuditStore) Recent(ctx context.Context, limit int) ([]store.AuditEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.logs, nil
}

func (m *mockAuditStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	return 0, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Test Cases
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleConnect_ZeroUsersCreateAdmin(t *testing.T) {
	userStore := newMockUserStore()
	auditStore := newMockAuditStore()
	h := &handler{
		userStore:    userStore,
		auditStore:   auditStore,
		cfg:          &config.Config{},
		loginTracker: newLoginTracker(),
	}

	body := connectRequest{
		Email:    "admin@example.com",
		Password: "SecurePassword123",
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/auth/connect", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	h.handleConnect(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected status %d, got %d", http.StatusCreated, w.Code)
	}

	var resp connectResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.CreatedAdmin != true {
		t.Error("expected CreatedAdmin=true")
	}
	if resp.Token == "" {
		t.Error("expected non-empty token")
	}
	if resp.User.Email != "admin@example.com" {
		t.Errorf("expected email admin@example.com, got %s", resp.User.Email)
	}
}

func TestHandleConnect_ExistingUsersLogin(t *testing.T) {
	userStore := newMockUserStore()

	// Pre-populate with a user
	token := "mcp_test123"
	user := &store.User{
		ID:          "test-user",
		Email:       "user@example.com",
		DisplayName: "User",
		Role:        store.RoleMember,
		MCPToken:    &token,
		MCPEnabled:  true,
		IsActive:    true,
	}
	userStore.mu.Lock()
	userStore.users["user@example.com"] = user
	userStore.mu.Unlock()

	// Verify user exists in store
	cnt, _ := userStore.Count(context.Background())
	if cnt != 1 {
		t.Fatalf("expected 1 user, got %d", cnt)
	}
}

func TestHandleConnectCreateAdmin_PasswordTooShort(t *testing.T) {
	userStore := newMockUserStore()
	auditStore := newMockAuditStore()
	h := &handler{
		userStore:    userStore,
		auditStore:   auditStore,
		cfg:          &config.Config{},
		loginTracker: newLoginTracker(),
	}

	body := connectRequest{
		Email:    "admin@example.com",
		Password: "short",
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/auth/connect", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	h.handleConnectCreateAdmin(w, req, body)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestHandleConnectCreateAdmin_ValidPassword(t *testing.T) {
	userStore := newMockUserStore()
	auditStore := newMockAuditStore()
	h := &handler{
		userStore:    userStore,
		auditStore:   auditStore,
		cfg:          &config.Config{},
		loginTracker: newLoginTracker(),
	}

	body := connectRequest{
		Email:    "admin@example.com",
		Password: "VerySecurePassword123",
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/auth/connect", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	h.handleConnectCreateAdmin(w, req, body)

	if w.Code != http.StatusCreated {
		t.Errorf("expected status %d, got %d", http.StatusCreated, w.Code)
	}

	var resp connectResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Token == "" {
		t.Error("expected non-empty token")
	}
	if !strings.HasPrefix(resp.Token, "mcp_") {
		t.Errorf("expected token to start with 'mcp_', got %s", resp.Token[:4])
	}

	// Verify user was created with correct role
	if resp.User.Role != store.RoleAdmin {
		t.Errorf("expected role %v, got %v", store.RoleAdmin, resp.User.Role)
	}

	// Verify displayName derived from email
	if resp.User.Email != "admin@example.com" {
		t.Errorf("expected email admin@example.com, got %s", resp.User.Email)
	}
}

func TestHandleConnectCheck_NeedsSetupTrue(t *testing.T) {
	userStore := newMockUserStore()
	auditStore := newMockAuditStore()
	h := &handler{
		userStore:    userStore,
		auditStore:   auditStore,
		cfg:          &config.Config{},
		loginTracker: newLoginTracker(),
	}

	req := httptest.NewRequest("GET", "/api/auth/connect", nil)
	w := httptest.NewRecorder()

	h.handleConnectCheck(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["needs_setup"] != true {
		t.Errorf("expected needs_setup=true, got %v", resp["needs_setup"])
	}
}

func TestHandleConnectCheck_NeedsSetupFalse(t *testing.T) {
	userStore := newMockUserStore()
	auditStore := newMockAuditStore()
	h := &handler{
		userStore:    userStore,
		auditStore:   auditStore,
		cfg:          &config.Config{},
		loginTracker: newLoginTracker(),
	}

	// Add a user to make needs_setup false
	token := "mcp_test"
	userStore.users["user@example.com"] = &store.User{
		ID:         "test-id",
		Email:      "user@example.com",
		MCPToken:   &token,
		IsActive:   true,
		MCPEnabled: true,
	}

	req := httptest.NewRequest("GET", "/api/auth/connect", nil)
	w := httptest.NewRecorder()

	h.handleConnectCheck(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["needs_setup"] != false {
		t.Errorf("expected needs_setup=false, got %v", resp["needs_setup"])
	}
}

func TestHandleConnectScript_ServerURLInference(t *testing.T) {
	userStore := newMockUserStore()
	auditStore := newMockAuditStore()
	h := &handler{
		userStore:    userStore,
		auditStore:   auditStore,
		cfg:          &config.Config{},
		loginTracker: newLoginTracker(),
	}

	// Test with HTTPS (TLS)
	req := httptest.NewRequest("GET", "/connect", nil)
	req.Host = "example.com:8080"
	req.TLS = &tls.ConnectionState{} // Indicates HTTPS
	w := httptest.NewRecorder()

	h.handleConnectScript(w, req)

	if !bytes.Contains(w.Body.Bytes(), []byte("https://example.com:8080")) {
		t.Error("expected HTTPS URL in script")
	}

	// Test with HTTP (no TLS)
	req2 := httptest.NewRequest("GET", "/connect", nil)
	req2.Host = "localhost:8080"
	req2.TLS = nil
	req2.Header.Set("X-Forwarded-Proto", "http")
	w2 := httptest.NewRecorder()

	h.handleConnectScript(w2, req2)

	if !bytes.Contains(w2.Body.Bytes(), []byte("http://localhost:8080")) {
		t.Error("expected HTTP URL in script")
	}
}

func TestLoginTracker_RecordFailure(t *testing.T) {
	lt := newLoginTracker()

	lt.recordFailure("user@example.com")
	lt.recordFailure("user@example.com")
	lt.recordFailure("user@example.com")

	entry := lt.entries["user@example.com"]
	if entry.failures != 3 {
		t.Errorf("expected 3 failures, got %d", entry.failures)
	}
}

func TestLoginTracker_Lockout(t *testing.T) {
	lt := newLoginTracker()

	// Record 5 failures
	for i := 0; i < 5; i++ {
		lt.recordFailure("user@example.com")
	}

	if !lt.isLocked("user@example.com") {
		t.Error("expected user to be locked after 5 failures")
	}
}

func TestLoginTracker_LockoutExpiry(t *testing.T) {
	lt := newLoginTracker()

	// Record 5 failures
	for i := 0; i < 5; i++ {
		lt.recordFailure("user@example.com")
	}

	// Manually set lockedAt to before the lockout duration
	lt.mu.Lock()
	lt.entries["user@example.com"].lockedAt = time.Now().Add(-20 * time.Minute)
	lt.mu.Unlock()

	// Should be unlocked after expiry
	if lt.isLocked("user@example.com") {
		t.Error("expected user to be unlocked after lockout duration expired")
	}

	// Entry should be deleted
	lt.mu.Lock()
	_, ok := lt.entries["user@example.com"]
	lt.mu.Unlock()
	if ok {
		t.Error("expected entry to be deleted after lockout expiry")
	}
}

func TestLoginTracker_RecordSuccess(t *testing.T) {
	lt := newLoginTracker()

	lt.recordFailure("user@example.com")
	lt.recordFailure("user@example.com")
	lt.recordSuccess("user@example.com")

	lt.mu.Lock()
	_, ok := lt.entries["user@example.com"]
	lt.mu.Unlock()

	if ok {
		t.Error("expected entry to be deleted on success")
	}
}

func TestHandleConnect_MissingCredentials(t *testing.T) {
	userStore := newMockUserStore()
	auditStore := newMockAuditStore()
	h := &handler{
		userStore:    userStore,
		auditStore:   auditStore,
		cfg:          &config.Config{},
		loginTracker: newLoginTracker(),
	}

	tests := []struct {
		name     string
		email    string
		password string
	}{
		{"empty email", "", "password123"},
		{"empty password", "user@example.com", ""},
		{"both empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := connectRequest{
				Email:    tt.email,
				Password: tt.password,
			}
			bodyBytes, _ := json.Marshal(body)

			req := httptest.NewRequest("POST", "/api/auth/connect", bytes.NewReader(bodyBytes))
			w := httptest.NewRecorder()

			h.handleConnect(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
			}
		})
	}
}

func TestHandleConnect_InvalidJSON(t *testing.T) {
	userStore := newMockUserStore()
	auditStore := newMockAuditStore()
	h := &handler{
		userStore:    userStore,
		auditStore:   auditStore,
		cfg:          &config.Config{},
		loginTracker: newLoginTracker(),
	}

	req := httptest.NewRequest("POST", "/api/auth/connect", bytes.NewReader([]byte("invalid json")))
	w := httptest.NewRecorder()

	h.handleConnect(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestHandleConnect_NoUserStore(t *testing.T) {
	h := &handler{
		userStore:    nil,
		auditStore:   nil,
		cfg:          &config.Config{},
		loginTracker: newLoginTracker(),
	}

	body := connectRequest{
		Email:    "user@example.com",
		Password: "password",
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/auth/connect", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	h.handleConnect(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d", http.StatusServiceUnavailable, w.Code)
	}
}

func TestHandleConnectLogin_BruteForceProtection(t *testing.T) {
	userStore := newMockUserStore()
	auditStore := newMockAuditStore()
	h := &handler{
		userStore:    userStore,
		auditStore:   auditStore,
		cfg:          &config.Config{},
		loginTracker: newLoginTracker(),
	}

	// Pre-populate with a test user (non-existent user to trigger login failure)
	hashedPassword := "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcg7b3XeKeUxWdeS86E36P4/TVm2"
	userStore.users["user@example.com"] = &store.User{
		ID:           "test-user",
		Email:        "user@example.com",
		PasswordHash: hashedPassword,
		IsActive:     true,
	}

	body := connectRequest{
		Email:    "user@example.com",
		Password: "wrongpassword",
	}

	// Simulate 5 failed attempts
	for i := 0; i < 5; i++ {
		bodyBytes, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/api/auth/connect", bytes.NewReader(bodyBytes))
		w := httptest.NewRecorder()
		h.handleConnectLogin(w, req, body)
	}

	// 6th attempt should be blocked
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/auth/connect", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()
	h.handleConnectLogin(w, req, body)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected status %d, got %d", http.StatusTooManyRequests, w.Code)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Additional edge cases and integration tests
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleConnectCreateAdmin_DisplayNameFromEmail(t *testing.T) {
	userStore := newMockUserStore()
	auditStore := newMockAuditStore()
	h := &handler{
		userStore:    userStore,
		auditStore:   auditStore,
		cfg:          &config.Config{},
		loginTracker: newLoginTracker(),
	}

	body := connectRequest{
		Email:    "john.doe@company.com",
		Password: "VerySecurePassword123",
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/auth/connect", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	h.handleConnectCreateAdmin(w, req, body)

	var resp connectResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.User.Email != "john.doe@company.com" {
		t.Errorf("expected email john.doe@company.com, got %s", resp.User.Email)
	}
}

func TestHandleConnect_EmailTrimming(t *testing.T) {
	userStore := newMockUserStore()
	auditStore := newMockAuditStore()
	h := &handler{
		userStore:    userStore,
		auditStore:   auditStore,
		cfg:          &config.Config{},
		loginTracker: newLoginTracker(),
	}

	body := connectRequest{
		Email:    "  admin@example.com  ",
		Password: "VerySecurePassword123",
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/auth/connect", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	h.handleConnect(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected status %d, got %d", http.StatusCreated, w.Code)
	}

	// Verify email was trimmed
	userStore.mu.Lock()
	_, ok := userStore.users["admin@example.com"]
	userStore.mu.Unlock()

	if !ok {
		t.Error("expected email to be trimmed and stored")
	}
}

// Catch-up cursor: these doubles do not exercise it, so the methods exist only
// to satisfy store.UserStore.
func (m *mockUserStore) CatchupCursor(context.Context, string) (time.Time, error) {
	return time.Time{}, nil
}

func (m *mockUserStore) SetCatchupCursor(context.Context, string, time.Time) error {
	return nil
}
