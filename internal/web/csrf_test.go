package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// okHandler returns 200 for all requests.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func TestCSRFProtect_AllowsGETWithoutToken(t *testing.T) {
	handler := CSRFProtect(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("GET without token: got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestCSRFProtect_SetsCSRFCookie(t *testing.T) {
	handler := CSRFProtect(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	found := false
	for _, c := range rr.Result().Cookies() {
		if c.Name == csrfCookieName {
			found = true
			if c.Value == "" {
				t.Error("CSRF cookie value is empty")
			}
			if c.HttpOnly {
				t.Error("CSRF cookie should NOT be HttpOnly (JS needs to read it)")
			}
			break
		}
	}
	if !found {
		t.Error("CSRF cookie not set on first request")
	}
}

func TestCSRFProtect_StoresTokenInContext(t *testing.T) {
	var ctxToken string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxToken = CSRFToken(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	handler := CSRFProtect(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if ctxToken == "" {
		t.Error("CSRF token not stored in context")
	}
}

func TestCSRFProtect_RejectsPOSTWithoutToken(t *testing.T) {
	handler := CSRFProtect(okHandler)

	// First GET to obtain the cookie.
	getReq := httptest.NewRequest(http.MethodGet, "/", nil)
	getRR := httptest.NewRecorder()
	handler.ServeHTTP(getRR, getReq)

	var csrfCookie *http.Cookie
	for _, c := range getRR.Result().Cookies() {
		if c.Name == csrfCookieName {
			csrfCookie = c
			break
		}
	}
	if csrfCookie == nil {
		t.Fatal("no CSRF cookie from GET")
	}

	// POST with cookie but no token field.
	postReq := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("email=a@b.com"))
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.AddCookie(csrfCookie)
	postRR := httptest.NewRecorder()
	handler.ServeHTTP(postRR, postReq)

	if postRR.Code != http.StatusForbidden {
		t.Errorf("POST without CSRF token: got %d, want %d", postRR.Code, http.StatusForbidden)
	}
}

func TestCSRFProtect_AcceptsPOSTWithValidFormToken(t *testing.T) {
	handler := CSRFProtect(okHandler)

	// GET to obtain cookie + token.
	getReq := httptest.NewRequest(http.MethodGet, "/", nil)
	getRR := httptest.NewRecorder()
	handler.ServeHTTP(getRR, getReq)

	var csrfCookie *http.Cookie
	for _, c := range getRR.Result().Cookies() {
		if c.Name == csrfCookieName {
			csrfCookie = c
			break
		}
	}
	if csrfCookie == nil {
		t.Fatal("no CSRF cookie from GET")
	}

	// POST with valid _csrf form field.
	body := "_csrf=" + csrfCookie.Value + "&email=a@b.com"
	postReq := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.AddCookie(csrfCookie)
	postRR := httptest.NewRecorder()
	handler.ServeHTTP(postRR, postReq)

	if postRR.Code != http.StatusOK {
		t.Errorf("POST with valid _csrf form field: got %d, want %d", postRR.Code, http.StatusOK)
	}
}

func TestCSRFProtect_AcceptsPOSTWithValidHeader(t *testing.T) {
	handler := CSRFProtect(okHandler)

	// GET to obtain cookie.
	getReq := httptest.NewRequest(http.MethodGet, "/", nil)
	getRR := httptest.NewRecorder()
	handler.ServeHTTP(getRR, getReq)

	var csrfCookie *http.Cookie
	for _, c := range getRR.Result().Cookies() {
		if c.Name == csrfCookieName {
			csrfCookie = c
			break
		}
	}
	if csrfCookie == nil {
		t.Fatal("no CSRF cookie from GET")
	}

	// POST with X-CSRF-Token header.
	postReq := httptest.NewRequest(http.MethodPost, "/api/settings/retention", strings.NewReader(`{"retention_days":30}`))
	postReq.Header.Set("Content-Type", "application/json")
	postReq.Header.Set("X-CSRF-Token", csrfCookie.Value)
	postReq.AddCookie(csrfCookie)
	postRR := httptest.NewRecorder()
	handler.ServeHTTP(postRR, postReq)

	if postRR.Code != http.StatusOK {
		t.Errorf("POST with X-CSRF-Token header: got %d, want %d", postRR.Code, http.StatusOK)
	}
}

func TestCSRFProtect_RejectsMismatchedToken(t *testing.T) {
	handler := CSRFProtect(okHandler)

	// GET to obtain cookie.
	getReq := httptest.NewRequest(http.MethodGet, "/", nil)
	getRR := httptest.NewRecorder()
	handler.ServeHTTP(getRR, getReq)

	var csrfCookie *http.Cookie
	for _, c := range getRR.Result().Cookies() {
		if c.Name == csrfCookieName {
			csrfCookie = c
			break
		}
	}
	if csrfCookie == nil {
		t.Fatal("no CSRF cookie from GET")
	}

	// POST with wrong token.
	postReq := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("_csrf=wrong_token&email=a@b.com"))
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.AddCookie(csrfCookie)
	postRR := httptest.NewRecorder()
	handler.ServeHTTP(postRR, postReq)

	if postRR.Code != http.StatusForbidden {
		t.Errorf("POST with mismatched token: got %d, want %d", postRR.Code, http.StatusForbidden)
	}
}

func TestCSRFProtect_SkipsBearerAuth(t *testing.T) {
	handler := CSRFProtect(okHandler)

	// POST with Bearer token — should skip CSRF.
	req := httptest.NewRequest(http.MethodPost, "/api/logs", strings.NewReader(`[{"message":"test"}]`))
	req.Header.Set("Authorization", "Bearer some-api-key")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("POST with Bearer auth: got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestCSRFProtect_SkipsMCPPaths(t *testing.T) {
	handler := CSRFProtect(okHandler)

	req := httptest.NewRequest(http.MethodPost, "/mcp/message", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("POST to /mcp/ path: got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestCSRFProtect_ValidatesOnPUTAndDELETE(t *testing.T) {
	handler := CSRFProtect(okHandler)

	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		// Without token should be rejected.
		req := httptest.NewRequest(method, "/api/connectors/1", nil)
		req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "some-token"})
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("%s without CSRF token: got %d, want %d", method, rr.Code, http.StatusForbidden)
		}
	}
}

func TestCSRFProtect_ReusesCookieOnSubsequentRequests(t *testing.T) {
	handler := CSRFProtect(okHandler)

	// First request — new cookie.
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)

	var csrfCookie *http.Cookie
	for _, c := range rr1.Result().Cookies() {
		if c.Name == csrfCookieName {
			csrfCookie = c
			break
		}
	}
	if csrfCookie == nil {
		t.Fatal("no CSRF cookie from first request")
	}

	// Second request with existing cookie — should NOT set a new one.
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(csrfCookie)
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)

	newCookieSet := false
	for _, c := range rr2.Result().Cookies() {
		if c.Name == csrfCookieName {
			newCookieSet = true
			break
		}
	}
	if newCookieSet {
		t.Error("CSRF cookie should be reused, not regenerated on every request")
	}
}
