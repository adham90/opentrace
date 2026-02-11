package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/version"
)

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "1.0.1", -1},
		{"1.0.1", "1.0.0", 1},
		{"1.1.0", "1.0.9", 1},
		{"2.0.0", "1.9.9", 1},
		{"0.1.0", "0.2.0", -1},
		{"1.2.3", "1.2.3", 0},
		{"1.2", "1.2.0", 0},
		{"1", "1.0.0", 0},
		{"1.0.0-beta", "1.0.0", 0},
	}

	for _, tt := range tests {
		got := compareSemver(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("compareSemver(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestParseSemver(t *testing.T) {
	tests := []struct {
		input string
		want  [3]int
	}{
		{"1.2.3", [3]int{1, 2, 3}},
		{"v1.2.3", [3]int{1, 2, 3}},
		{"0.1.0", [3]int{0, 1, 0}},
		{"1.0", [3]int{1, 0, 0}},
		{"1", [3]int{1, 0, 0}},
		{"1.2.3-beta", [3]int{1, 2, 3}},
	}

	for _, tt := range tests {
		got := parseSemver(tt.input)
		if got != tt.want {
			t.Errorf("parseSemver(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// fakeGitHubServer returns an httptest.Server that serves release JSON.
// The tag can be changed atomically via the returned setter.
func fakeGitHubServer(t *testing.T, initialTag string) (*httptest.Server, func(string)) {
	t.Helper()
	var currentTag atomic.Value
	currentTag.Store(initialTag)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tag := currentTag.Load().(string)
		json.NewEncoder(w).Encode(releaseInfo{
			TagName: tag,
			HTMLURL: "https://github.com/test/repo/releases/tag/" + tag,
			Body:    "Release " + tag,
		})
	}))
	t.Cleanup(srv.Close)

	return srv, func(tag string) { currentTag.Store(tag) }
}

func TestVersionChecker_CacheReturnsStalResult(t *testing.T) {
	// This test reproduces the exact bug: the cache holds old data and
	// a subsequent check still sees the stale version.
	oldVersion := version.Version
	version.Version = "0.1.4"
	t.Cleanup(func() { version.Version = oldVersion })

	srv, setTag := fakeGitHubServer(t, "v0.1.4")

	vc := &versionChecker{
		cacheTTL: 1 * time.Hour,
		owner:    "test",
		repo:     "repo",
		baseURL:  srv.URL,
	}

	// First check — should report no update (0.1.4 == 0.1.4)
	resp, err := vc.check()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.UpdateAvailable {
		t.Fatal("expected no update available on first check")
	}
	if resp.LatestVersion != "0.1.4" {
		t.Fatalf("expected latest 0.1.4, got %s", resp.LatestVersion)
	}

	// Simulate a new release appearing on GitHub
	setTag("v0.1.5")

	// Second check — cache is still valid, should STILL say no update (the bug)
	resp, err = vc.check()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.UpdateAvailable {
		t.Fatal("cache should still return stale result (no update)")
	}
	if resp.LatestVersion != "0.1.4" {
		t.Fatalf("cache should still return 0.1.4, got %s", resp.LatestVersion)
	}
}

func TestVersionChecker_InvalidateCacheDetectsUpdate(t *testing.T) {
	oldVersion := version.Version
	version.Version = "0.1.4"
	t.Cleanup(func() { version.Version = oldVersion })

	srv, setTag := fakeGitHubServer(t, "v0.1.4")

	vc := &versionChecker{
		cacheTTL: 1 * time.Hour,
		owner:    "test",
		repo:     "repo",
		baseURL:  srv.URL,
	}

	// First check — no update
	resp, err := vc.check()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.UpdateAvailable {
		t.Fatal("expected no update available on first check")
	}

	// Simulate a new release
	setTag("v0.1.5")

	// Invalidate cache, then check — should now detect the update
	vc.invalidateCache()

	resp, err = vc.check()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.UpdateAvailable {
		t.Fatal("expected update available after cache invalidation")
	}
	if resp.LatestVersion != "0.1.5" {
		t.Fatalf("expected latest 0.1.5, got %s", resp.LatestVersion)
	}
}

func TestVersionCheckHandler_ForceBypassesCache(t *testing.T) {
	oldVersion := version.Version
	version.Version = "0.1.4"
	t.Cleanup(func() { version.Version = oldVersion })

	srv, setTag := fakeGitHubServer(t, "v0.1.4")

	server := NewServerWithDeps(ServerDeps{
		DSStore:  newMockStore(),
		LogStore: newMockLogStore(),
	})
	server.versionChecker = &versionChecker{
		cacheTTL: 1 * time.Hour,
		owner:    "test",
		repo:     "repo",
		baseURL:  srv.URL,
	}

	// First check — populates cache
	req := httptest.NewRequest("GET", "/api/version/check", nil)
	rec := httptest.NewRecorder()
	server.handleVersionCheck(rec, req)

	var resp versionCheckResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.UpdateAvailable {
		t.Fatal("expected no update on first check")
	}

	// New release appears
	setTag("v0.1.5")

	// Normal check (no force) — still cached
	req = httptest.NewRequest("GET", "/api/version/check", nil)
	rec = httptest.NewRecorder()
	server.handleVersionCheck(rec, req)

	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.UpdateAvailable {
		t.Fatal("expected cached result (no update) without force")
	}

	// Force check — should bypass cache and detect update
	req = httptest.NewRequest("GET", "/api/version/check?force=1", nil)
	rec = httptest.NewRecorder()
	server.handleVersionCheck(rec, req)

	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.UpdateAvailable {
		t.Fatal("expected update available with force=1")
	}
	if resp.LatestVersion != "0.1.5" {
		t.Fatalf("expected latest 0.1.5, got %s", resp.LatestVersion)
	}
	if resp.CurrentVersion != "0.1.4" {
		t.Fatalf("expected current 0.1.4, got %s", resp.CurrentVersion)
	}
}

func TestVersionChecker_DevBuildSkipsCheck(t *testing.T) {
	oldVersion := version.Version
	version.Version = "dev"
	t.Cleanup(func() { version.Version = oldVersion })

	vc := newVersionChecker("test", "repo")

	resp, err := vc.check()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.UpdateAvailable {
		t.Fatal("dev builds should never show update available")
	}
	if resp.LatestVersion != "dev" {
		t.Fatalf("expected latest 'dev', got %s", resp.LatestVersion)
	}
}
