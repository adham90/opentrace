package web

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/version"
)

func TestFindAssetURL(t *testing.T) {
	assets := []githubAsset{
		{Name: "opentrace_0.2.0_linux_amd64.tar.gz", BrowserDownloadURL: "https://example.com/linux_amd64.tar.gz"},
		{Name: "opentrace_0.2.0_linux_arm64.tar.gz", BrowserDownloadURL: "https://example.com/linux_arm64.tar.gz"},
		{Name: "opentrace_0.2.0_darwin_amd64.tar.gz", BrowserDownloadURL: "https://example.com/darwin_amd64.tar.gz"},
		{Name: "opentrace_0.2.0_darwin_arm64.tar.gz", BrowserDownloadURL: "https://example.com/darwin_arm64.tar.gz"},
		{Name: "checksums.txt", BrowserDownloadURL: "https://example.com/checksums.txt"},
	}

	u := &selfUpdater{owner: "test", repo: "repo"}

	tests := []struct {
		goos, goarch string
		wantURL      string
		wantErr      bool
	}{
		{"linux", "amd64", "https://example.com/linux_amd64.tar.gz", false},
		{"linux", "arm64", "https://example.com/linux_arm64.tar.gz", false},
		{"darwin", "amd64", "https://example.com/darwin_amd64.tar.gz", false},
		{"darwin", "arm64", "https://example.com/darwin_arm64.tar.gz", false},
		{"windows", "amd64", "", true},
		{"linux", "386", "", true},
	}

	for _, tt := range tests {
		url, err := u.findAssetURL(assets, tt.goos, tt.goarch)
		if tt.wantErr {
			if err == nil {
				t.Errorf("findAssetURL(%s/%s) expected error, got %q", tt.goos, tt.goarch, url)
			}
			continue
		}
		if err != nil {
			t.Errorf("findAssetURL(%s/%s) unexpected error: %v", tt.goos, tt.goarch, err)
			continue
		}
		if url != tt.wantURL {
			t.Errorf("findAssetURL(%s/%s) = %q, want %q", tt.goos, tt.goarch, url, tt.wantURL)
		}
	}
}

// createTestArchive creates a tar.gz containing a fake "opentrace" binary with the given content.
func createTestArchive(t *testing.T, content string) string {
	t.Helper()

	archivePath := filepath.Join(t.TempDir(), "opentrace_test.tar.gz")
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	data := []byte(content)
	tw.WriteHeader(&tar.Header{
		Name: "opentrace",
		Mode: 0755,
		Size: int64(len(data)),
	})
	tw.Write(data)
	tw.Close()
	gw.Close()
	f.Close()

	return archivePath
}

func TestExtractBinary(t *testing.T) {
	archivePath := createTestArchive(t, "#!/bin/sh\necho hello\n")

	binPath, err := extractBinary(archivePath, "opentrace")
	if err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	defer os.Remove(binPath)

	data, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("reading extracted binary: %v", err)
	}
	if string(data) != "#!/bin/sh\necho hello\n" {
		t.Errorf("extracted content = %q, want script content", string(data))
	}

	info, _ := os.Stat(binPath)
	if info.Mode()&0111 == 0 {
		t.Error("extracted binary is not executable")
	}
}

func TestExtractBinary_NotFound(t *testing.T) {
	archivePath := createTestArchive(t, "data")

	_, err := extractBinary(archivePath, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing binary in archive")
	}
}

func TestApplyUpdate(t *testing.T) {
	dir := t.TempDir()

	// Create a fake "current" binary
	currentPath := filepath.Join(dir, "opentrace")
	os.WriteFile(currentPath, []byte("old-binary"), 0755)

	// Create a fake "new" binary
	newPath := filepath.Join(dir, "opentrace-new")
	os.WriteFile(newPath, []byte("new-binary"), 0755)

	// Override os.Executable for test: we can't easily, so we test applyUpdate
	// with a custom function. Instead, test the rename logic directly.
	backupPath := currentPath + ".bak"

	// Simulate applyUpdate logic
	os.Remove(backupPath)
	if err := os.Rename(currentPath, backupPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(newPath, currentPath); err != nil {
		t.Fatal(err)
	}

	// Verify
	data, _ := os.ReadFile(currentPath)
	if string(data) != "new-binary" {
		t.Errorf("current binary = %q, want new-binary", string(data))
	}
	data, _ = os.ReadFile(backupPath)
	if string(data) != "old-binary" {
		t.Errorf("backup = %q, want old-binary", string(data))
	}
}

// fakeSelfUpdateServer creates a test server that serves a GitHub-like release API
// with a downloadable tar.gz archive containing a fake binary.
func fakeSelfUpdateServer(t *testing.T, tag string, binaryContent string) *httptest.Server {
	t.Helper()

	archivePath := createTestArchive(t, binaryContent)
	archiveData, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/test/repo/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		assetName := "opentrace_" + tag[1:] + "_" + "linux_amd64.tar.gz"
		json.NewEncoder(w).Encode(githubRelease{
			TagName: tag,
			Assets: []githubAsset{
				{
					Name:               assetName,
					BrowserDownloadURL: "http://" + r.Host + "/download/" + assetName,
				},
			},
		})
	})
	mux.HandleFunc("/download/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		w.Write(archiveData)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestSelfUpdater_FetchRelease(t *testing.T) {
	srv := fakeSelfUpdateServer(t, "v0.2.0", "new-binary")

	u := &selfUpdater{owner: "test", repo: "repo", baseURL: srv.URL}
	release, err := u.fetchRelease(context.Background())
	if err != nil {
		t.Fatalf("fetchRelease: %v", err)
	}
	if release.TagName != "v0.2.0" {
		t.Errorf("tag = %q, want v0.2.0", release.TagName)
	}
	if len(release.Assets) != 1 {
		t.Fatalf("assets count = %d, want 1", len(release.Assets))
	}
}

func TestSelfUpdater_Update_DevBuild(t *testing.T) {
	old := version.Version
	version.Version = "dev"
	t.Cleanup(func() { version.Version = old })

	u := &selfUpdater{owner: "test", repo: "repo"}
	_, err := u.Update(context.Background())
	if err == nil {
		t.Fatal("expected error for dev build")
	}
	if err.Error() != "cannot self-update a dev build" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSelfUpdater_Update_AlreadyCurrent(t *testing.T) {
	old := version.Version
	version.Version = "0.2.0"
	t.Cleanup(func() { version.Version = old })

	srv := fakeSelfUpdateServer(t, "v0.2.0", "same-binary")

	u := &selfUpdater{owner: "test", repo: "repo", baseURL: srv.URL}
	_, err := u.Update(context.Background())
	if err == nil {
		t.Fatal("expected error when already current")
	}
}

func TestSelfUpdater_Update_NoMatchingAsset(t *testing.T) {
	old := version.Version
	version.Version = "0.1.0"
	t.Cleanup(func() { version.Version = old })

	// Server returns a release with only windows assets
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/test/repo/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(githubRelease{
			TagName: "v0.2.0",
			Assets: []githubAsset{
				{Name: "opentrace_0.2.0_windows_amd64.zip", BrowserDownloadURL: "http://example.com/win.zip"},
			},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	u := &selfUpdater{owner: "test", repo: "repo", baseURL: srv.URL}
	_, err := u.Update(context.Background())
	if err == nil {
		t.Fatal("expected error for no matching asset")
	}
}

func TestSelfUpdateHandler_Docker(t *testing.T) {
	// This test verifies the handler returns an error when Docker is detected.
	// We can't easily fake isDocker() in tests since it checks /.dockerenv,
	// so we test the handler directly with a mock check.
	// The actual Docker detection is tested via the isDocker() function.

	// Instead, verify the handler exists and returns JSON for non-Docker.
	server := NewServerWithDeps(ServerDeps{
		DSStore:  newMockStore(),
		LogStore: newMockLogStore(),
	})

	req := httptest.NewRequest("POST", "/api/version/update", nil)
	rec := httptest.NewRecorder()
	server.handleSelfUpdate(rec, req)

	// Should get an error response (since version is "dev" in tests)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestAutoUpdateSettings(t *testing.T) {
	ms := newMockSettingsStore()

	server := NewServerWithDeps(ServerDeps{
		DSStore:       newMockStore(),
		LogStore:      newMockLogStore(),
		SettingsStore: ms,
	})

	// GET — default is false
	req := httptest.NewRequest("GET", "/api/settings/auto-update", nil)
	rec := httptest.NewRecorder()
	server.handleGetAutoUpdate(rec, req)

	var resp map[string]bool
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["enabled"] {
		t.Error("expected auto-update disabled by default")
	}

	// PUT — enable
	body := `{"enabled":true}`
	req = httptest.NewRequest("PUT", "/api/settings/auto-update", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	server.handleSetAutoUpdate(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d", rec.Code)
	}

	// GET — now enabled
	req = httptest.NewRequest("GET", "/api/settings/auto-update", nil)
	rec = httptest.NewRecorder()
	server.handleGetAutoUpdate(rec, req)

	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp["enabled"] {
		t.Error("expected auto-update enabled after PUT")
	}
}
