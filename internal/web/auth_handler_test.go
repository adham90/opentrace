package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/adham90/opentrace/internal/store"
)

// newTestServer creates a Server with all mock stores wired up, suitable for
// testing auth handler flows via the full chi router.
func newTestServer(t *testing.T) (*Server, *mockUserStore, *mockSessionStore) {
	t.Helper()
	us := newMockUserStore()
	ss := newMockSessionStore()
	srv := NewServerWithDeps(ServerDeps{
		DSStore:      newMockStore(),
		LogStore:     newMockLogStore(),
		UserStore:    us,
		SessionStore: ss,
	})
	return srv, us, ss
}

// createTestUser creates a user in the mock store with a bcrypt-hashed password.
func createTestUser(t *testing.T, us *mockUserStore, email, password string, role store.UserRole, active bool) *store.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hashing password: %v", err)
	}
	user, err := us.Create(context.Background(), store.CreateUserParams{
		Email:        email,
		PasswordHash: string(hash),
		DisplayName:  strings.Split(email, "@")[0],
		Role:         role,
	})
	if err != nil {
		t.Fatalf("creating user: %v", err)
	}
	if !active {
		us.mu.Lock()
		user.IsActive = false
		us.mu.Unlock()
	}
	return user
}

// createTestSession creates a session for the given user and returns the token.
func createTestSession(t *testing.T, ss *mockSessionStore, userID string) string {
	t.Helper()
	token := "test-session-token-" + userID
	_, err := ss.Create(context.Background(), userID, token, time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("creating session: %v", err)
	}
	return token
}

// postForm builds an application/x-www-form-urlencoded POST request with CSRF token.
func postForm(target string, values url.Values) *http.Request {
	values.Set("_csrf", testCSRFToken)
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(csrfCookie())
	return req
}

// --- Login tests ---

func TestLoginPage_RedirectsToOnboardingWhenNoUsers(t *testing.T) {
	srv, _, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "/onboarding" {
		t.Fatalf("expected redirect to /onboarding, got %s", loc)
	}
}

func TestLoginSubmit_Success(t *testing.T) {
	srv, us, _ := newTestServer(t)

	// Create a user with a known password.
	createTestUser(t, us, "alice@example.com", "correctpassword", store.RoleAdmin, true)

	form := url.Values{}
	form.Set("email", "alice@example.com")
	form.Set("password", "correctpassword")
	req := postForm("/login", form)
	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "/" {
		t.Fatalf("expected redirect to /, got %s", loc)
	}

	// Check that a session cookie was set.
	cookies := rec.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == sessionCookieName && c.Value != "" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected session cookie to be set after successful login")
	}
}

func TestLoginSubmit_InvalidPassword(t *testing.T) {
	srv, us, _ := newTestServer(t)

	createTestUser(t, us, "bob@example.com", "realpassword", store.RoleMember, true)

	form := url.Values{}
	form.Set("email", "bob@example.com")
	form.Set("password", "wrongpassword")
	req := postForm("/login", form)
	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rec.Code)
	}
}

func TestLoginSubmit_InactiveUser(t *testing.T) {
	srv, us, _ := newTestServer(t)

	createTestUser(t, us, "disabled@example.com", "password123", store.RoleMember, false)

	form := url.Values{}
	form.Set("email", "disabled@example.com")
	form.Set("password", "password123")
	req := postForm("/login", form)
	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Account is disabled") {
		t.Fatalf("expected body to contain 'Account is disabled', got: %s", body)
	}
}

// --- Register tests ---

func TestRegisterSubmit_FirstUserBecomesAdmin(t *testing.T) {
	srv, us, _ := newTestServer(t)

	form := url.Values{}
	form.Set("email", "first@example.com")
	form.Set("password", "securepassword")
	form.Set("display_name", "First User")
	req := postForm("/register", form)
	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "/" {
		t.Fatalf("expected redirect to /, got %s", loc)
	}

	// Verify the user was created with admin role.
	users, err := us.List(context.Background())
	if err != nil {
		t.Fatalf("listing users: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(users))
	}
	if users[0].Role != store.RoleAdmin {
		t.Fatalf("expected first user to have role %q, got %q", store.RoleAdmin, users[0].Role)
	}
	if users[0].Email != "first@example.com" {
		t.Fatalf("expected email first@example.com, got %s", users[0].Email)
	}
}

func TestRegisterSubmit_DuplicateEmail(t *testing.T) {
	srv, us, ss := newTestServer(t)

	// Create an existing admin user.
	admin := createTestUser(t, us, "existing@example.com", "password123", store.RoleAdmin, true)

	// Since a user already exists, we must be logged in as admin to register.
	adminToken := createTestSession(t, ss, admin.ID)

	form := url.Values{}
	form.Set("email", "existing@example.com")
	form.Set("password", "anotherpassword123")
	req := postForm("/register", form)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: adminToken})
	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for duplicate email, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Email is already registered") {
		t.Fatalf("expected body to contain 'Email is already registered', got: %s", body)
	}
}

func TestRegisterSubmit_ShortPassword(t *testing.T) {
	srv, _, _ := newTestServer(t)

	// No users exist, so registration is open.
	form := url.Values{}
	form.Set("email", "short@example.com")
	form.Set("password", "abc")
	req := postForm("/register", form)
	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for short password, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Password must be at least 12 characters") {
		t.Fatalf("expected body to contain password length error, got: %s", body)
	}
}

// --- Logout tests ---

func TestLogout_ClearsCookie(t *testing.T) {
	srv, us, ss := newTestServer(t)

	user := createTestUser(t, us, "logout@example.com", "password123", store.RoleMember, true)
	token := createTestSession(t, ss, user.ID)

	req := withCSRF(httptest.NewRequest(http.MethodPost, "/logout", nil))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "/login" {
		t.Fatalf("expected redirect to /login, got %s", loc)
	}

	// Verify the cookie is cleared (MaxAge < 0).
	cookies := rec.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == sessionCookieName {
			found = true
			if c.MaxAge >= 0 {
				t.Fatalf("expected cookie MaxAge < 0 (cleared), got %d", c.MaxAge)
			}
			break
		}
	}
	if !found {
		t.Fatal("expected session cookie to be present with negative MaxAge")
	}
}

// --- Delete user tests ---

func TestDeleteUser_PreventSelfDeletion(t *testing.T) {
	srv, us, ss := newTestServer(t)

	admin := createTestUser(t, us, "admin@example.com", "adminpass123", store.RoleAdmin, true)
	token := createTestSession(t, ss, admin.ID)

	req := withCSRF(httptest.NewRequest(http.MethodDelete, "/api/users/"+admin.ID, nil))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 conflict for self-deletion, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "cannot delete your own account") {
		t.Fatalf("expected body to contain self-deletion error, got: %s", body)
	}
}

// --- Lockout tests ---

func TestLogin_LockoutAfterFailedAttempts(t *testing.T) {
	srv, us, _ := newTestServer(t)
	createTestUser(t, us, "lockme@example.com", "correctpassword", store.RoleAdmin, true)

	// Fail 5 times.
	for i := 0; i < maxFailedLogins; i++ {
		form := url.Values{}
		form.Set("email", "lockme@example.com")
		form.Set("password", "wrongpassword")
		req := postForm("/login", form)
		rec := httptest.NewRecorder()
		srv.Router.ServeHTTP(rec, req)
	}

	// 6th attempt with correct password should be locked.
	form := url.Values{}
	form.Set("email", "lockme@example.com")
	form.Set("password", "correctpassword")
	req := postForm("/login", form)
	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 (locked), got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "temporarily locked") {
		t.Fatalf("expected lockout message, got: %s", rec.Body.String())
	}
}

func TestLogin_LockoutExpires(t *testing.T) {
	tracker := newLoginTracker()

	// Record enough failures to lock.
	for i := 0; i < maxFailedLogins; i++ {
		tracker.recordFailure("expire@example.com")
	}
	if !tracker.isLocked("expire@example.com") {
		t.Fatal("expected account to be locked")
	}

	// Simulate time passing by directly setting lockedAt in the past.
	tracker.mu.Lock()
	tracker.entries["expire@example.com"].lockedAt = time.Now().Add(-(lockoutDuration + time.Second))
	tracker.mu.Unlock()

	if tracker.isLocked("expire@example.com") {
		t.Fatal("expected lockout to have expired")
	}
}

func TestLogin_SuccessResetsFailureCount(t *testing.T) {
	srv, us, _ := newTestServer(t)
	createTestUser(t, us, "reset@example.com", "correctpassword", store.RoleAdmin, true)

	// Fail 3 times (below lockout threshold).
	for i := 0; i < 3; i++ {
		form := url.Values{}
		form.Set("email", "reset@example.com")
		form.Set("password", "wrongpassword")
		req := postForm("/login", form)
		rec := httptest.NewRecorder()
		srv.Router.ServeHTTP(rec, req)
	}

	// Succeed — should reset the counter.
	form := url.Values{}
	form.Set("email", "reset@example.com")
	form.Set("password", "correctpassword")
	req := postForm("/login", form)
	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302 (success), got %d", rec.Code)
	}

	// Now fail 4 more times — should NOT be locked (counter was reset).
	for i := 0; i < maxFailedLogins-1; i++ {
		form := url.Values{}
		form.Set("email", "reset@example.com")
		form.Set("password", "wrongpassword")
		req := postForm("/login", form)
		rec := httptest.NewRecorder()
		srv.Router.ServeHTTP(rec, req)
	}

	// Should still be able to login (not locked yet).
	form = url.Values{}
	form.Set("email", "reset@example.com")
	form.Set("password", "correctpassword")
	req = postForm("/login", form)
	rec = httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302 (not locked), got %d", rec.Code)
	}
}

// --- Remember me ---

func TestLogin_RememberMeExtendsSession(t *testing.T) {
	srv, us, ss := newTestServer(t)
	createTestUser(t, us, "remember@example.com", "correctpassword", store.RoleAdmin, true)

	form := url.Values{}
	form.Set("email", "remember@example.com")
	form.Set("password", "correctpassword")
	form.Set("remember", "on")
	req := postForm("/login", form)
	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rec.Code)
	}

	// Check cookie max-age is roughly 7 days (not 1 day).
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			// 7 days = 604800 seconds, 1 day = 86400.
			if c.MaxAge < 600000 {
				t.Errorf("remember-me cookie MaxAge = %d, want ~604800", c.MaxAge)
			}
			break
		}
	}

	// Verify session was created in the store.
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if len(ss.sessions) == 0 {
		t.Fatal("no session created")
	}
}

// --- Password change ---

func TestChangePassword_Success(t *testing.T) {
	srv, us, ss := newTestServer(t)
	user := createTestUser(t, us, "pw@example.com", "oldpassword12", store.RoleAdmin, true)
	token := createTestSession(t, ss, user.ID)

	form := url.Values{}
	form.Set("current_password", "oldpassword12")
	form.Set("new_password", "newpassword12")
	req := postForm("/api/profile/password", form)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestChangePassword_WrongCurrent(t *testing.T) {
	srv, us, ss := newTestServer(t)
	user := createTestUser(t, us, "pw2@example.com", "oldpassword12", store.RoleAdmin, true)
	token := createTestSession(t, ss, user.ID)

	form := url.Values{}
	form.Set("current_password", "wrongpassword")
	form.Set("new_password", "newpassword12")
	req := postForm("/api/profile/password", form)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestChangePassword_TooShort(t *testing.T) {
	srv, us, ss := newTestServer(t)
	user := createTestUser(t, us, "pw3@example.com", "oldpassword12", store.RoleAdmin, true)
	token := createTestSession(t, ss, user.ID)

	form := url.Values{}
	form.Set("current_password", "oldpassword12")
	form.Set("new_password", "short")
	req := postForm("/api/profile/password", form)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// --- Registration authorization ---

func TestRegister_NonAdminRejected(t *testing.T) {
	srv, us, ss := newTestServer(t)

	// Create a member (non-admin) user.
	member := createTestUser(t, us, "member@example.com", "memberpassword", store.RoleMember, true)
	token := createTestSession(t, ss, member.ID)

	form := url.Values{}
	form.Set("email", "new@example.com")
	form.Set("password", "newpassword12")
	req := postForm("/register", form)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	// Non-admin should be rejected (403 or redirect to login).
	if rec.Code == http.StatusFound {
		loc := rec.Header().Get("Location")
		if loc == "/" || loc == "/users" {
			t.Fatalf("non-admin should not be able to register users, but got redirect to %s", loc)
		}
	}
}

// --- Login with unknown email ---

func TestLogin_UnknownEmail(t *testing.T) {
	srv, _, _ := newTestServer(t)

	form := url.Values{}
	form.Set("email", "nobody@example.com")
	form.Set("password", "somepassword12")
	req := postForm("/login", form)
	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for unknown email, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Invalid email or password") {
		t.Fatal("expected generic error message (no email enumeration)")
	}
}
