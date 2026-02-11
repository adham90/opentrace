package web

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/adham90/opentrace/internal/version"
)

// selfUpdater handles downloading and applying binary updates from GitHub releases.
type selfUpdater struct {
	owner, repo string
	baseURL     string // override for testing; empty = use GitHub API
}

// UpdateResult holds the result of a self-update operation.
type UpdateResult struct {
	OldVersion string `json:"old_version"`
	NewVersion string `json:"new_version"`
	BackupPath string `json:"backup_path"`
}

// githubRelease is the GitHub API response for a release (with assets).
type githubRelease struct {
	TagName string         `json:"tag_name"`
	Assets  []githubAsset  `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func newSelfUpdater(owner, repo string) *selfUpdater {
	return &selfUpdater{owner: owner, repo: repo}
}

// NewSelfUpdater creates a selfUpdater for use outside the web package (e.g., auto-update goroutine).
func NewSelfUpdater(owner, repo string) *selfUpdater {
	return newSelfUpdater(owner, repo)
}

// Update downloads the latest release, extracts the binary, and replaces the current executable.
func (u *selfUpdater) Update(ctx context.Context) (*UpdateResult, error) {
	if version.Version == "dev" {
		return nil, fmt.Errorf("cannot self-update a dev build")
	}

	release, err := u.fetchRelease(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching release: %w", err)
	}

	newVersion := strings.TrimPrefix(release.TagName, "v")
	if compareSemver(version.Version, newVersion) >= 0 {
		return nil, fmt.Errorf("already running %s (latest: %s)", version.Version, newVersion)
	}

	assetURL, err := u.findAssetURL(release.Assets, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return nil, err
	}

	archivePath, err := u.downloadArchive(ctx, assetURL)
	if err != nil {
		return nil, fmt.Errorf("downloading archive: %w", err)
	}
	defer os.Remove(archivePath)

	newBinaryPath, err := extractBinary(archivePath, "opentrace")
	if err != nil {
		return nil, fmt.Errorf("extracting binary: %w", err)
	}
	defer func() {
		// Clean up temp binary if applyUpdate moves it (rename is atomic)
		os.Remove(newBinaryPath)
	}()

	backupPath, err := applyUpdate(newBinaryPath)
	if err != nil {
		return nil, fmt.Errorf("applying update: %w", err)
	}

	return &UpdateResult{
		OldVersion: version.Version,
		NewVersion: newVersion,
		BackupPath: backupPath,
	}, nil
}

func (u *selfUpdater) fetchRelease(ctx context.Context) (*githubRelease, error) {
	var url string
	if u.baseURL != "" {
		url = u.baseURL + "/repos/" + u.owner + "/" + u.repo + "/releases/latest"
	} else {
		url = fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", u.owner, u.repo)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decoding release: %w", err)
	}
	return &release, nil
}

// findAssetURL returns the download URL for the matching OS/arch asset.
func (u *selfUpdater) findAssetURL(assets []githubAsset, goos, goarch string) (string, error) {
	// GoReleaser naming: opentrace_{version}_{os}_{arch}.tar.gz
	suffix := fmt.Sprintf("_%s_%s.tar.gz", goos, goarch)
	for _, a := range assets {
		if strings.HasSuffix(a.Name, suffix) {
			url := a.BrowserDownloadURL
			// In tests, the base URL may override the download URL
			if u.baseURL != "" && !strings.HasPrefix(url, "http") {
				url = u.baseURL + "/" + a.Name
			}
			return url, nil
		}
	}
	return "", fmt.Errorf("no release asset found for %s/%s", goos, goarch)
}

// downloadArchive downloads the archive to a temp file and returns the path.
func (u *selfUpdater) downloadArchive(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "opentrace-update-*.tar.gz")
	if err != nil {
		return "", err
	}

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	tmp.Close()
	return tmp.Name(), nil
}

// extractBinary opens a tar.gz archive and extracts the named binary to a temp file.
func extractBinary(archivePath, binaryName string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("reading tar: %w", err)
		}

		// Match just the filename (GoReleaser may or may not include directories)
		name := hdr.Name
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}

		if name == binaryName && hdr.Typeflag == tar.TypeReg {
			tmp, err := os.CreateTemp("", "opentrace-new-*")
			if err != nil {
				return "", err
			}
			if _, err := io.Copy(tmp, tr); err != nil {
				tmp.Close()
				os.Remove(tmp.Name())
				return "", err
			}
			tmp.Close()
			if err := os.Chmod(tmp.Name(), 0755); err != nil {
				os.Remove(tmp.Name())
				return "", err
			}
			return tmp.Name(), nil
		}
	}
	return "", fmt.Errorf("binary %q not found in archive", binaryName)
}

// applyUpdate replaces the current executable with the new binary.
// Returns the backup path of the old binary.
func applyUpdate(newBinaryPath string) (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("getting executable path: %w", err)
	}

	backupPath := execPath + ".bak"

	// Remove old backup if it exists
	os.Remove(backupPath)

	// Rename current binary to .bak
	if err := os.Rename(execPath, backupPath); err != nil {
		return "", fmt.Errorf("backing up current binary: %w", err)
	}

	// Move new binary into place
	if err := os.Rename(newBinaryPath, execPath); err != nil {
		// Try to restore the backup
		os.Rename(backupPath, execPath)
		return "", fmt.Errorf("replacing binary: %w", err)
	}

	return backupPath, nil
}

// isDocker returns true if running inside a Docker container.
func isDocker() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}
