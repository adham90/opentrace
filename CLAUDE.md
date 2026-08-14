# CLAUDE.md

## Build & Test

```bash
go build -o opentrace ./cmd/opentrace
go test -short -race ./...          # unit tests (no Docker)
go test -race ./...                 # full suite (needs Docker)
go vet ./...                        # linting
go mod tidy                         # after adding new imports
```

## Do

- Use `store.ErrNotFound` sentinel for 404 responses
- Use `server.WriteJSON(w, status, data)` / `server.WriteError(w, status, msg)` for HTTP responses
- Use `log/slog` with structured key-value fields — always include `"error"` key on errors
- Guard integration tests with `testing.Short()` skip
- Use `?` placeholders in SQLite queries — never string interpolation
- Store booleans as INTEGER 0/1, timestamps as RFC3339 TEXT, UUIDs as TEXT
- Cast MCP tool args from `float64` (JSON default) before using as `int`
- When adding a store interface method, update ALL mock implementations (`internal/testutil/mocks/`, `internal/api/mock_test.go`, and any package-local test doubles — `grep` the method name)
- When adding a new store, add to `pkg/store/` (interface + model) and `internal/adapter/sqlite/` (implementation, wired in `internal/adapter/sqlite/stores.go`)
- Use the `server.Module` pattern for new HTTP features — see `internal/routes/<name>/module.go` for examples
- Run `go mod tidy` after adding new imports
- Cap all pagination `limit` params to a reasonable maximum (100-500)
- Validate and sanitize all user input at handler boundaries

## Don't

- Don't use `fmt.Println` or the old `log` package — use `slog.*` only
- Don't shadow an imported package name with a local variable (e.g. a local `watcher` in a file that imports `internal/watcher`)
- Don't use CGO — all dependencies must be pure Go for cross-compilation
- Don't leak internal error details (SQL, file paths, stack traces) in HTTP responses
- Don't add store fields directly to `server.Deps` — embed them in `store.Stores` instead
- Don't skip `testing.Short()` guards on tests that need Docker or network
- Don't hardcode magic numbers — use named constants
- Don't write functions longer than ~100 lines — extract helpers

## Conventions

- **HTTP handlers**: Use Chi router, return JSON via `server.WriteJSON`/`server.WriteError`
- **Store layer**: Interfaces in `pkg/store/iface_*.go`, models in `pkg/store/models_*.go`, SQLite implementations in `internal/adapter/sqlite/`
- **Queries**: Raw SQL written by hand and run through bun (`db.NewRaw(...)`), always with `?` placeholders. There is no sqlc and no query builder — don't generate code or add one.
- **HTTP routes**: Each feature in `internal/routes/<name>/` with a `Module` var (`module.go`) and its handlers alongside
- **Domain services**: Business logic in `internal/domain/<name>/` (service + repository interface), kept free of HTTP concerns
- **MCP tools**: Consolidated tools in `internal/mcp/tools/`, registered via `internal/mcp/catalog.go`
- **Migrations**: Sequential numbered SQL files in `migrations/`, embedded via `migrations/embed.go`
- **Log storage**: Logs live in the columnar store under `internal/logstore/`, not in SQLite
- **Tests**: In-memory SQLite for store tests, hand-written behavioral mocks in `internal/testutil/mocks/` and `internal/api/mock_test.go`, shared helpers in `internal/testutil/`

## Telegram Notifications

Use `telegram-cli` to send updates during long tasks:

- **Starting work**: `telegram-cli "Starting: <brief description>"`
- **Blocker/Error**: `telegram-cli "Blocker: <what went wrong>"`
- **Progress milestone**: `telegram-cli "Progress: <what was completed>"`
- **Task complete**: `telegram-cli "Done: <summary>"`

Send when: starting non-trivial tasks, hitting blockers, completing milestones, finishing tasks.
Don't notify for trivial operations like reading files or small edits.

If `telegram-cli` is not on PATH, skip the notification and carry on — it is a
convenience, not a build dependency.
