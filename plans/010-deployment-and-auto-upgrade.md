# Plan 010: Easy Deployment & Auto-Upgrade

## Goal

Make OpenTrace trivially deployable to any provider with one click, and give
users a dashboard button to check for / apply upgrades without SSH.

---

## Current State

| What exists | Details |
|---|---|
| Dockerfile | Multi-stage Alpine build, works well |
| docker-compose.yml | Single service + volume, loads `.env` |
| Hetzner deploy script | `deploy/deploy.sh` + `deploy/cloud-init.yml` — full one-click |
| Health endpoint | `GET /healthz` returns `{"status":"ok"}` |
| No version string | Binary has no embedded version — can't tell what's running |
| No CI/CD | No `.github/workflows/`, no GoReleaser, no registry pushes |
| No one-click buttons | No DigitalOcean / Railway / Render / Fly configs |
| No upgrade mechanism | No way to check for or apply updates from the dashboard |

---

## Architecture Overview

```
GitHub repo
    │
    ├─ git tag v1.2.3
    │       │
    │       ▼
    │  GitHub Actions CI
    │       │
    │       ├─► Run tests
    │       ├─► GoReleaser → GitHub Releases (multi-arch binaries)
    │       └─► Docker build+push → ghcr.io/adham90/opentrace:1.2.3
    │
    ▼
One-Click Deploy Buttons (README)
    │
    ├─ DigitalOcean App Platform  (do-app.yaml)
    ├─ Railway                    (railway.toml)
    ├─ Render                     (render.yaml)
    ├─ Fly.io                     (fly.toml)
    └─ Coolify / Caprover         (Dockerfile already works)

Running Instance
    │
    ├─ GET /api/version → returns current version + checks latest release
    ├─ Dashboard banner: "v1.3.0 available — click to upgrade"
    └─ Upgrade mechanism: Watchtower sidecar (Docker) or self-update (binary)
```

---

## Phase 1: Version Management

**Goal:** Embed a version string in the binary so the app knows what it's running.

### 1.1 Add version package

Create `internal/version/version.go`:

```go
package version

// Set at build time via -ldflags:
//   go build -ldflags "-X github.com/adham90/opentrace/internal/version.Version=1.2.3
//                       -X github.com/adham90/opentrace/internal/version.Commit=abc1234
//                       -X github.com/adham90/opentrace/internal/version.Date=2025-06-15"
var (
    Version = "dev"
    Commit  = "unknown"
    Date    = "unknown"
)

func Full() string {
    return Version + " (" + Commit + ") built " + Date
}
```

### 1.2 Log version on startup

In `cmd/opentrace/main.go`, add at the top of `run()`:

```go
log.Printf("opentrace %s", version.Full())
```

Also add a `version` subcommand:

```go
case "version":
    fmt.Println("opentrace " + version.Full())
    return nil
```

### 1.3 Enhance /healthz → /api/version

Add `GET /api/version` endpoint:

```go
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
    writeJSON(w, http.StatusOK, map[string]any{
        "version": version.Version,
        "commit":  version.Commit,
        "date":    version.Date,
    })
}
```

Also add version to the existing `/healthz` response.

### 1.4 Update Dockerfile

```dockerfile
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown

RUN CGO_ENABLED=1 go build \
    -ldflags "-X github.com/adham90/opentrace/internal/version.Version=${VERSION} \
              -X github.com/adham90/opentrace/internal/version.Commit=${COMMIT} \
              -X github.com/adham90/opentrace/internal/version.Date=${DATE}" \
    -o /opentrace ./cmd/opentrace
```

### Files changed
- `internal/version/version.go` (new)
- `cmd/opentrace/main.go` (version subcommand + startup log)
- `internal/web/server.go` (add `/api/version` route + handler)
- `Dockerfile` (build args + ldflags)

---

## Phase 2: GitHub Actions CI/CD

**Goal:** Automated testing on every push, automated releases with multi-arch
Docker images + binaries on every tag.

### 2.1 Test workflow

Create `.github/workflows/ci.yml`:

```yaml
name: CI
on:
  push:
    branches: [main]
  pull_request:

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.25"
      - run: go test -short -race ./...

  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.25"
      - uses: golangci/golangci-lint-action@v6
        with:
          version: latest
```

### 2.2 Release workflow

Create `.github/workflows/release.yml`:

Triggered on `v*` tags. Uses GoReleaser for multi-platform binaries and
builds+pushes Docker images to `ghcr.io`.

```yaml
name: Release
on:
  push:
    tags: ["v*"]

permissions:
  contents: write
  packages: write

jobs:
  goreleaser:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: actions/setup-go@v5
        with:
          go-version: "1.25"
      - uses: goreleaser/goreleaser-action@v6
        with:
          version: latest
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}

  docker:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: docker/setup-qemu-action@v3
      - uses: docker/setup-buildx-action@v3
      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - uses: docker/metadata-action@v5
        id: meta
        with:
          images: ghcr.io/adham90/opentrace
          tags: |
            type=semver,pattern={{version}}
            type=semver,pattern={{major}}.{{minor}}
            type=sha
            type=raw,value=latest,enable={{is_default_branch}}
      - uses: docker/build-push-action@v6
        with:
          context: .
          push: true
          platforms: linux/amd64,linux/arm64
          tags: ${{ steps.meta.outputs.tags }}
          labels: ${{ steps.meta.outputs.labels }}
          build-args: |
            VERSION=${{ github.ref_name }}
            COMMIT=${{ github.sha }}
            DATE=${{ github.event.head_commit.timestamp }}
```

### 2.3 GoReleaser config

Create `.goreleaser.yml`:

```yaml
version: 2

builds:
  - main: ./cmd/opentrace
    binary: opentrace
    env:
      - CGO_ENABLED=0
    goos:
      - linux
      - darwin
      - windows
    goarch:
      - amd64
      - arm64
    ldflags:
      - -s -w
      - -X github.com/adham90/opentrace/internal/version.Version={{.Version}}
      - -X github.com/adham90/opentrace/internal/version.Commit={{.ShortCommit}}
      - -X github.com/adham90/opentrace/internal/version.Date={{.Date}}

archives:
  - format: tar.gz
    name_template: "opentrace_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
    format_overrides:
      - goos: windows
        format: zip

checksum:
  name_template: checksums.txt

changelog:
  sort: asc
  filters:
    exclude:
      - "^docs:"
      - "^test:"
      - "^ci:"
```

> **Note on CGO_ENABLED=0:** GoReleaser cross-compiles without CGO. The
> `modernc.org/sqlite` driver is pure Go and works without CGO. The Dockerfile
> still uses `CGO_ENABLED=1` with musl for the Docker image build, but both
> paths work. The pure-Go path produces slightly larger binaries but is fully
> portable.

### Files changed
- `.github/workflows/ci.yml` (new)
- `.github/workflows/release.yml` (new)
- `.goreleaser.yml` (new)

---

## Phase 3: One-Click Deploy Configs

**Goal:** Add deploy buttons to README for major platforms. All pull the Docker
image from `ghcr.io/adham90/opentrace:latest`.

### 3.1 DigitalOcean App Platform

Create `deploy/digitalocean/app.yaml`:

```yaml
name: opentrace
services:
  - name: opentrace
    image:
      registry_type: GHCR
      registry: adham90
      repository: opentrace
      tag: latest
    instance_size_slug: basic-xxs        # $5/mo
    instance_count: 1
    http_port: 8080
    envs:
      - key: OPENTRACE_DATA_DIR
        value: /data
      - key: OPENTRACE_LLM_PROVIDER
        scope: RUN_TIME
        type: SECRET
      - key: OPENTRACE_ANTHROPIC_API_KEY
        scope: RUN_TIME
        type: SECRET
      - key: OPENTRACE_API_KEY
        scope: RUN_TIME
        type: SECRET
    health_check:
      http_path: /healthz
      initial_delay_seconds: 10
      period_seconds: 30
```

README button:

```markdown
[![Deploy to DO](https://www.deploytodo.com/do-btn-blue.svg)](https://cloud.digitalocean.com/apps/new?repo=https://github.com/adham90/opentrace/tree/main)
```

### 3.2 Railway

Create `railway.toml`:

```toml
[build]
dockerfilePath = "Dockerfile"

[deploy]
healthcheckPath = "/healthz"
healthcheckTimeout = 30
restartPolicyType = "ON_FAILURE"
restartPolicyMaxRetries = 3

[[mounts]]
mountPath = "/data"
```

README button:

```markdown
[![Deploy on Railway](https://railway.com/button.svg)](https://railway.com/template/opentrace?referralCode=...)
```

### 3.3 Render

Create `render.yaml`:

```yaml
services:
  - type: web
    name: opentrace
    runtime: docker
    plan: starter          # $7/mo, persistent disk available
    healthCheckPath: /healthz
    envVars:
      - key: OPENTRACE_DATA_DIR
        value: /data
      - key: OPENTRACE_LLM_PROVIDER
        sync: false
      - key: OPENTRACE_ANTHROPIC_API_KEY
        sync: false
      - key: OPENTRACE_API_KEY
        generateValue: true
    disk:
      name: opentrace-data
      mountPath: /data
      sizeGB: 1
```

README button:

```markdown
[![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy?repo=https://github.com/adham90/opentrace)
```

### 3.4 Fly.io

Create `fly.toml`:

```toml
app = "opentrace"
primary_region = "iad"

[build]
  dockerfile = "Dockerfile"

[env]
  OPENTRACE_DATA_DIR = "/data"
  OPENTRACE_LISTEN_ADDR = ":8080"

[http_service]
  internal_port = 8080
  force_https = true
  auto_stop_machines = false       # keep running for watchers/schedulers
  auto_start_machines = true
  min_machines_running = 1

  [http_service.checks]
    [http_service.checks.health]
      interval = "30s"
      timeout = "5s"
      grace_period = "10s"
      method = "GET"
      path = "/healthz"

[mounts]
  source = "opentrace_data"
  destination = "/data"
```

Deploy command:

```bash
fly launch --copy-config --no-deploy
fly secrets set OPENTRACE_LLM_PROVIDER=anthropic OPENTRACE_ANTHROPIC_API_KEY=sk-ant-xxx
fly deploy
```

### 3.5 Coolify / Caprover / Dokku

These work out of the box with the existing Dockerfile. Add a note in README
pointing to the Dockerfile and `OPENTRACE_DATA_DIR=/data` volume mount.

### Files changed
- `deploy/digitalocean/app.yaml` (new)
- `railway.toml` (new — must be in repo root)
- `render.yaml` (new — must be in repo root)
- `fly.toml` (new — must be in repo root)
- `README.md` (deploy buttons section)

---

## Phase 4: Version Check API & Dashboard Banner

**Goal:** The dashboard shows a banner when a newer version is available.

### 4.1 GitHub Release check endpoint

Add `GET /api/version/check` that:

1. Returns the current running version
2. Queries `https://api.github.com/repos/adham90/opentrace/releases/latest`
   (cached for 1 hour in memory)
3. Compares semver and returns whether an upgrade is available

```go
// GET /api/version/check
{
    "current_version": "1.2.0",
    "latest_version":  "1.3.0",
    "update_available": true,
    "release_url": "https://github.com/adham90/opentrace/releases/tag/v1.3.0",
    "release_notes": "### What's new\n- Feature X\n- Bug fix Y",
    "checked_at": "2025-06-15T10:00:00Z"
}
```

Implementation notes:
- Use `net/http` client with 5s timeout to fetch GitHub API
- Cache result in a `sync.Once`-style struct with 1-hour TTL
- If the running version is `dev`, skip the check (return `update_available: false`)
- Parse versions with a minimal semver compare (no external dep needed — split on
  `.` and compare integers)

### 4.2 Dashboard "Update Available" banner

Add a small HTMX fragment that loads on every page:

```html
<!-- in base layout, after nav -->
<div hx-get="/api/version/banner" hx-trigger="load" hx-swap="innerHTML"></div>
```

The `/api/version/banner` endpoint returns either empty (no update) or:

```html
<div class="update-banner">
  New version <strong>v1.3.0</strong> available.
  <a href="/settings/update">View update instructions</a>
  <button hx-post="/api/version/dismiss" hx-swap="outerHTML">Dismiss</button>
</div>
```

Dismiss state stored in a cookie or SQLite settings table so it doesn't nag.

### 4.3 Update instructions page

Add a `/settings/update` page that shows context-aware instructions:

- **Detect deployment method** via env var `OPENTRACE_DEPLOY_METHOD` (set by
  each deploy config) or auto-detect (check if running in Docker, check if
  Watchtower is present, etc.)
- Show platform-specific upgrade steps:
  - **Docker Compose:** `docker compose pull && docker compose up -d`
  - **Fly.io:** `fly deploy --image ghcr.io/adham90/opentrace:1.3.0`
  - **Railway/Render:** "Deploys automatically on new image push"
  - **Binary:** Download link + restart command
  - **Hetzner (cloud-init):** SSH commands

### Files changed
- `internal/web/server.go` (routes)
- `internal/web/version_check.go` (new — GitHub release checker + cache)
- `internal/web/templates/update.html` (new — update instructions page)
- `internal/web/templates/base.html` (add banner div)

---

## Phase 5: Auto-Upgrade Mechanism

**Goal:** For Docker deployments, enable fully automatic upgrades. For binary
deployments, provide a one-click update button.

### 5.1 Docker: Watchtower sidecar (recommended)

The simplest, most battle-tested approach. Add Watchtower to docker-compose:

```yaml
services:
  app:
    image: ghcr.io/adham90/opentrace:latest
    # ... existing config ...
    labels:
      - "com.centurylinklabs.watchtower.enable=true"

  watchtower:
    image: containrrr/watchtower
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
    environment:
      - WATCHTOWER_CLEANUP=true
      - WATCHTOWER_POLL_INTERVAL=3600    # check every hour
      - WATCHTOWER_LABEL_ENABLE=true     # only update labeled containers
      - WATCHTOWER_NOTIFICATIONS=shoutrrr # optional notifications
    restart: unless-stopped
```

Update the cloud-init.yml to include Watchtower in the Hetzner deploy.

When Watchtower detects a new `ghcr.io/adham90/opentrace:latest` image, it:
1. Pulls the new image
2. Gracefully stops the old container
3. Starts a new container with the same config
4. Removes the old image

The dashboard can detect Watchtower via the Docker socket or the
`OPENTRACE_AUTO_UPDATE=watchtower` env var and show "Auto-updates enabled"
instead of a manual upgrade button.

### 5.2 Binary: Self-update endpoint (optional, future)

For users running the raw binary (no Docker), add a `POST /api/version/update`
that:

1. Downloads the correct binary from GitHub Releases (matching OS/arch)
2. Verifies the checksum
3. Replaces the running binary (using `selfupdate` pattern — write new binary
   next to current, then swap via rename)
4. Triggers a graceful restart (exit with code 0, let systemd restart)

This is the most complex piece and should only be built if there's demand. For
now, the update instructions page (Phase 4.3) covers this case.

Implementation sketch:

```go
// POST /api/version/update
func (s *Server) handleSelfUpdate(w http.ResponseWriter, r *http.Request) {
    // 1. Fetch latest release from GitHub API
    // 2. Find asset matching runtime.GOOS + runtime.GOARCH
    // 3. Download to temp file
    // 4. Verify SHA256 against checksums.txt from release
    // 5. Replace binary: os.Rename(tmpPath, currentBinaryPath)
    // 6. Respond with success
    // 7. Trigger graceful shutdown (os.Process.Signal(syscall.SIGTERM))
    //    → systemd/Docker will restart with the new binary
}
```

Security considerations:
- Only allow if authenticated (admin session)
- Only download from `github.com/adham90/opentrace` releases
- Always verify checksums
- Add `OPENTRACE_DISABLE_SELF_UPDATE=true` env var to opt out

### 5.3 Dashboard UI for upgrade

On the `/settings/update` page:

```
┌─────────────────────────────────────────────────┐
│  OpenTrace Update                               │
│                                                 │
│  Current version:  v1.2.0                       │
│  Latest version:   v1.3.0   ✓ Update available  │
│                                                 │
│  ┌─────────────────────────────────────────┐    │
│  │ What's new in v1.3.0                    │    │
│  │ • Added webhook notifications           │    │
│  │ • Fixed memory leak in log ingestion    │    │
│  │ • Improved watcher scheduling           │    │
│  └─────────────────────────────────────────┘    │
│                                                 │
│  Deployment: Docker Compose                     │
│  Auto-update: ✓ Watchtower active               │
│  (or)                                           │
│  [ Update Now ]  ← triggers pull+restart        │
│                                                 │
│  ─── Manual Instructions ───                    │
│  docker compose pull && docker compose up -d    │
│                                                 │
└─────────────────────────────────────────────────┘
```

### Files changed
- `docker-compose.yml` (add Watchtower sidecar, switch to ghcr.io image)
- `deploy/cloud-init.yml` (add Watchtower to Hetzner deploy)
- `internal/web/version_check.go` (add self-update logic)
- `internal/web/templates/update.html` (upgrade UI)

---

## Phase 6: README Deploy Section

**Goal:** Rewrite the Deployment section of README with one-click buttons and
clear instructions for each platform.

### New README deployment section structure

```markdown
## Deploy

### One-Click Deploy

| Platform | Button | Notes |
|---|---|---|
| DigitalOcean | [![Deploy to DO](badge)](link) | App Platform, $5/mo |
| Railway | [![Deploy on Railway](badge)](link) | Free tier available |
| Render | [![Deploy to Render](badge)](link) | Free tier, persistent disk |
| Fly.io | `fly launch` | See instructions below |
| Hetzner | `./deploy/deploy.sh` | Full VPS with Caddy + backups |

### Docker (recommended)

\`\`\`bash
docker run -d --name opentrace \
  -p 8080:8080 \
  -v opentrace-data:/data \
  -e OPENTRACE_LLM_PROVIDER=anthropic \
  -e OPENTRACE_ANTHROPIC_API_KEY=sk-ant-xxx \
  ghcr.io/adham90/opentrace:latest
\`\`\`

### Docker Compose

\`\`\`bash
curl -O https://raw.githubusercontent.com/adham90/opentrace/main/docker-compose.yml
docker compose up -d
\`\`\`

### Docker Compose with Auto-Updates

\`\`\`bash
curl -O https://raw.githubusercontent.com/adham90/opentrace/main/docker-compose.prod.yml
docker compose -f docker-compose.prod.yml up -d
\`\`\`
Includes Watchtower for automatic image updates.

### Binary

Download from [GitHub Releases](https://github.com/adham90/opentrace/releases):

\`\`\`bash
# Linux amd64
curl -L https://github.com/adham90/opentrace/releases/latest/download/opentrace_linux_amd64.tar.gz | tar xz
./opentrace
\`\`\`
```

### Files changed
- `README.md` (rewrite Deployment section)
- `docker-compose.prod.yml` (new — production compose with Watchtower + ghcr.io image)

---

## Implementation Order & Dependencies

```
Phase 1: Version Management          ◄── no dependencies, do first
    │
    ▼
Phase 2: CI/CD Pipelines             ◄── needs Phase 1 for ldflags
    │
    ├──► Phase 3: One-Click Configs   ◄── needs ghcr.io images from Phase 2
    │
    └──► Phase 4: Version Check UI    ◄── needs version string from Phase 1
             │
             ▼
         Phase 5: Auto-Upgrade        ◄── needs Phase 2 (releases) + Phase 4 (UI)
             │
             ▼
         Phase 6: README Rewrite      ◄── needs everything above
```

### Estimated scope per phase

| Phase | New files | Modified files | Complexity |
|---|---|---|---|
| 1 — Version | 1 | 3 | Small |
| 2 — CI/CD | 3 | 0 | Medium |
| 3 — One-Click | 4-5 | 1 (README) | Medium |
| 4 — Version Check UI | 2 | 2 | Medium |
| 5 — Auto-Upgrade | 1 | 3 | Medium-Large |
| 6 — README | 1 | 1 | Small |

---

## Key Decisions

### Docker registry: ghcr.io vs Docker Hub

**Recommendation: `ghcr.io` (GitHub Container Registry)**
- Free for public repos
- No rate limits for authenticated pulls
- Integrated with GitHub Actions (no extra secrets)
- Image URL: `ghcr.io/adham90/opentrace`

### Auto-upgrade: Watchtower vs Docker socket vs self-update

**Recommendation: Watchtower sidecar**
- Battle-tested, widely used
- No security risk of exposing Docker socket to the app
- Works with any Docker/Compose deployment
- Users can opt out by not including the sidecar
- Self-update for binary installs can be added later if there's demand

### GoReleaser CGO: enabled vs disabled

**Recommendation: CGO_ENABLED=0 for GoReleaser, CGO_ENABLED=1 for Docker**
- `modernc.org/sqlite` works in pure-Go mode (no CGO needed)
- This allows cross-compilation for all OS/arch combos
- Docker image still uses CGO for optimal SQLite performance
- Both paths are already tested and working

### Config files placement: repo root vs deploy/

**Recommendation: Mixed**
- `railway.toml`, `render.yaml`, `fly.toml` — **repo root** (platforms require this)
- `deploy/digitalocean/app.yaml` — **deploy/ dir** (DO supports any path)
- `.goreleaser.yml` — **repo root** (GoReleaser default)
- `docker-compose.prod.yml` — **repo root** (easy curl download)

---

## Security Considerations

1. **Self-update endpoint** must require admin authentication
2. **Binary checksums** must be verified before replacing the running binary
3. **Watchtower** only updates containers with the explicit label
4. **GitHub API** calls are unauthenticated (60 req/hour limit) — 1-hour cache
   is fine
5. **`OPENTRACE_DISABLE_SELF_UPDATE=true`** env var to disable for
   security-sensitive deployments
6. **Docker socket** is never exposed to the OpenTrace container itself

---

## Testing Plan

| What | How |
|---|---|
| Version injection | `go build -ldflags ...` then `./opentrace version` |
| /api/version endpoint | Unit test + manual curl |
| CI workflow | Push to branch, verify Actions run |
| Release workflow | Create `v0.0.1-test` tag, verify releases + ghcr.io |
| One-click deploys | Manual test on each platform (DO, Railway, Render, Fly) |
| Version check cache | Unit test with mock HTTP server |
| Dashboard banner | Manual test with outdated version string |
| Watchtower | `docker compose up` with prod config, push new image, verify update |
| Self-update (Phase 5) | Test on Linux VM with systemd service |
