package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/adham90/opentrace/internal/version"
)

// versionChecker caches the latest GitHub release info.
type versionChecker struct {
	mu          sync.RWMutex
	lastCheck   time.Time
	latest      *releaseInfo
	cacheTTL    time.Duration
	owner, repo string
}

type releaseInfo struct {
	TagName     string `json:"tag_name"`
	HTMLURL     string `json:"html_url"`
	Body        string `json:"body"`
	PublishedAt string `json:"published_at"`
}

type versionCheckResponse struct {
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version"`
	UpdateAvailable bool   `json:"update_available"`
	ReleaseURL      string `json:"release_url,omitempty"`
	ReleaseNotes    string `json:"release_notes,omitempty"`
	CheckedAt       string `json:"checked_at"`
}

func newVersionChecker(owner, repo string) *versionChecker {
	return &versionChecker{
		cacheTTL: 1 * time.Hour,
		owner:    owner,
		repo:     repo,
	}
}

func (vc *versionChecker) check() (*versionCheckResponse, error) {
	current := version.Version
	resp := &versionCheckResponse{
		CurrentVersion: current,
		CheckedAt:      time.Now().UTC().Format(time.RFC3339),
	}

	// Skip check for dev builds
	if current == "dev" {
		resp.LatestVersion = "dev"
		return resp, nil
	}

	release, err := vc.getLatestRelease()
	if err != nil {
		return resp, err
	}

	latestTag := strings.TrimPrefix(release.TagName, "v")
	resp.LatestVersion = latestTag
	resp.ReleaseURL = release.HTMLURL
	resp.ReleaseNotes = release.Body
	resp.UpdateAvailable = compareSemver(current, latestTag) < 0

	return resp, nil
}

func (vc *versionChecker) getLatestRelease() (*releaseInfo, error) {
	vc.mu.RLock()
	if vc.latest != nil && time.Since(vc.lastCheck) < vc.cacheTTL {
		cached := vc.latest
		vc.mu.RUnlock()
		return cached, nil
	}
	vc.mu.RUnlock()

	vc.mu.Lock()
	defer vc.mu.Unlock()

	// Double-check after acquiring write lock
	if vc.latest != nil && time.Since(vc.lastCheck) < vc.cacheTTL {
		return vc.latest, nil
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", vc.owner, vc.repo)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetching latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release releaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decoding release: %w", err)
	}

	vc.latest = &release
	vc.lastCheck = time.Now()
	return &release, nil
}

// compareSemver compares two semver strings (without "v" prefix).
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
func compareSemver(a, b string) int {
	aParts := parseSemver(a)
	bParts := parseSemver(b)

	for i := 0; i < 3; i++ {
		if aParts[i] < bParts[i] {
			return -1
		}
		if aParts[i] > bParts[i] {
			return 1
		}
	}
	return 0
}

// parseSemver parses "1.2.3" into [1, 2, 3]. Non-numeric parts are treated as 0.
func parseSemver(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	var result [3]int
	for i := 0; i < 3 && i < len(parts); i++ {
		// Strip pre-release suffix (e.g. "3-beta" → "3")
		clean := strings.SplitN(parts[i], "-", 2)[0]
		n, _ := strconv.Atoi(clean)
		result[i] = n
	}
	return result
}
