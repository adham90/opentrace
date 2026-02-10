package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/store"
)

func newTestServerWithDigest(ds store.DigestStore) *Server {
	us := newMockUserStore()
	us.Create(context.Background(), store.CreateUserParams{
		Email: "admin@test.com", PasswordHash: "hash",
		DisplayName: "Admin", Role: store.RoleAdmin,
	})
	return NewServerWithDeps(ServerDeps{
		DSStore:       newMockStore(),
		LogStore:      newMockLogStore(),
		WatcherStore:  newMockWatcherStore(),
		RunStore:      newMockWatcherRunStore(),
		AlertStore:    newMockAlertStore(),
		ServerStore:   newMockServerStore(),
		MetricStore:   newMockMetricStore(),
		UserStore:     us,
		SessionStore:  newMockSessionStore(),
		SettingsStore: newMockSettingsStore(),
		DigestStore:   ds,
		Registry:      connector.NewRegistry(),
		Cfg:           &config.Config{},
	})
}

func digestRequest(t *testing.T, srv *Server, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	user := &store.User{ID: "admin1", Role: store.RoleAdmin, IsActive: true}
	ctx := context.WithValue(req.Context(), ctxKeyUser, user)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	return w
}

func TestHandleGetLatestDigest_Empty(t *testing.T) {
	ds := newMockDigestStore()
	srv := newTestServerWithDigest(ds)

	w := digestRequest(t, srv, http.MethodGet, "/api/digests/latest")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "empty" {
		t.Errorf("status = %q, want empty", body["status"])
	}
}

func TestHandleGetLatestDigest_WithData(t *testing.T) {
	ds := newMockDigestStore()
	now := time.Now().UTC()
	ds.Save(nil, store.SaveDigestParams{
		ID:            "d1",
		Status:        "critical",
		PeriodStart:   now.Add(-24 * time.Hour),
		PeriodEnd:     now,
		AlertTotal:    5,
		AlertCritical: 2,
		AlertWarning:  3,
		MonitorTotal:  10,
		FailedRuns:    1,
		Data:          `{}`,
	})

	srv := newTestServerWithDigest(ds)
	w := digestRequest(t, srv, http.MethodGet, "/api/digests/latest")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var body store.StoredDigest
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.ID != "d1" {
		t.Errorf("id = %q, want d1", body.ID)
	}
	if body.AlertTotal != 5 {
		t.Errorf("alert_total = %d, want 5", body.AlertTotal)
	}
}

func TestHandleGetLatestDigest_EnvFilter(t *testing.T) {
	ds := newMockDigestStore()
	now := time.Now().UTC()
	ds.Save(nil, store.SaveDigestParams{
		ID: "prod", Environment: "production", Status: "critical",
		PeriodStart: now.Add(-24 * time.Hour), PeriodEnd: now,
		AlertTotal: 3, Data: `{}`,
	})
	ds.Save(nil, store.SaveDigestParams{
		ID: "stag", Environment: "staging", Status: "healthy",
		PeriodStart: now.Add(-24 * time.Hour), PeriodEnd: now,
		Data: `{}`,
	})

	srv := newTestServerWithDigest(ds)
	w := digestRequest(t, srv, http.MethodGet, "/api/digests/latest?env=production")

	var body store.StoredDigest
	json.NewDecoder(w.Body).Decode(&body)
	if body.ID != "prod" {
		t.Errorf("id = %q, want prod", body.ID)
	}
}

func TestHandleListDigests(t *testing.T) {
	ds := newMockDigestStore()
	now := time.Now().UTC()
	for i, id := range []string{"d1", "d2", "d3"} {
		ds.Save(nil, store.SaveDigestParams{
			ID: id, Status: "healthy",
			PeriodStart: now.Add(-time.Duration(3-i) * 24 * time.Hour),
			PeriodEnd:   now.Add(-time.Duration(2-i) * 24 * time.Hour),
			Data:        `{}`,
		})
	}

	srv := newTestServerWithDigest(ds)
	w := digestRequest(t, srv, http.MethodGet, "/api/digests")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var body []store.StoredDigest
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body) != 3 {
		t.Errorf("len = %d, want 3", len(body))
	}
}

func TestHandleListDigests_WithLimit(t *testing.T) {
	ds := newMockDigestStore()
	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		ds.Save(nil, store.SaveDigestParams{
			ID: "d" + string(rune('0'+i)), Status: "healthy",
			PeriodStart: now.Add(-24 * time.Hour), PeriodEnd: now,
			Data: `{}`,
		})
	}

	srv := newTestServerWithDigest(ds)
	w := digestRequest(t, srv, http.MethodGet, "/api/digests?limit=2")

	var body []store.StoredDigest
	json.NewDecoder(w.Body).Decode(&body)
	if len(body) != 2 {
		t.Errorf("len = %d, want 2", len(body))
	}
}

func TestHandleGetLatestDigest_NilStore(t *testing.T) {
	us := newMockUserStore()
	us.Create(context.Background(), store.CreateUserParams{
		Email: "admin@test.com", PasswordHash: "hash",
		DisplayName: "Admin", Role: store.RoleAdmin,
	})
	srv := NewServerWithDeps(ServerDeps{
		DSStore:      newMockStore(),
		LogStore:     newMockLogStore(),
		UserStore:    us,
		SessionStore: newMockSessionStore(),
		Registry:     connector.NewRegistry(),
		Cfg:          &config.Config{},
	})

	w := digestRequest(t, srv, http.MethodGet, "/api/digests/latest")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	if body["status"] != "unavailable" {
		t.Errorf("status = %q, want unavailable", body["status"])
	}
}
