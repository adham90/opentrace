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
- When adding a store interface method, update ALL mock implementations (web, mcp, watcher)
- When adding a new store, add to `store.Stores` struct + `NewStores()` factory (2 places only)
- Use the `server.Module` pattern for new domain features — see `internal/domains/` for examples
- Run `go mod tidy` after adding new imports
- Cap all pagination `limit` params to a reasonable maximum (100-500)
- Validate and sanitize all user input at handler boundaries

## Don't

- Don't use `fmt.Println` or the old `log` package — use `slog.*` only
- Don't name local vars `watcher` in `web/watchers.go` — conflicts with package import
- Don't use CGO — all dependencies must be pure Go for cross-compilation
- Don't leak internal error details (SQL, file paths, stack traces) in HTTP responses
- Don't add store fields directly to `server.Deps` — embed them in `store.Stores` instead
- Don't skip `testing.Short()` guards on tests that need Docker or network
- Don't hardcode magic numbers — use named constants
- Don't write functions longer than ~100 lines — extract helpers

## Conventions

- **HTTP handlers**: Use Chi router, return JSON via `server.WriteJSON`/`server.WriteError`
- **Store layer**: One interface per domain in `internal/store/iface_*.go`, models in `models_*.go`
- **Domains**: Each domain in `internal/domains/<name>/` with a `Module` var and `mount()` function
- **MCP tools**: Consolidated tools in `internal/mcp/tools/`, registered via `internal/mcp/catalog.go`
- **Migrations**: Sequential numbered SQL files in `internal/store/sqlite_migrations/`
- **Tests**: In-memory SQLite for store tests, hand-written behavioral mocks in `web/mock_test.go`

## Telegram Notifications

Use `telegram-cli` to send updates during long tasks:

- **Starting work**: `telegram-cli "Starting: <brief description>"`
- **Blocker/Error**: `telegram-cli "Blocker: <what went wrong>"`
- **Progress milestone**: `telegram-cli "Progress: <what was completed>"`
- **Task complete**: `telegram-cli "Done: <summary>"`

Send when: starting non-trivial tasks, hitting blockers, completing milestones, finishing tasks.
Don't notify for trivial operations like reading files or small edits.
