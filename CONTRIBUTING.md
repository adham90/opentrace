# Contributing to OpenTrace

Thanks for your interest in contributing! Here's how to get started.

## Development Setup

```bash
git clone https://github.com/adham90/opentrace.git
cd opentrace
cp .env.example .env
go build -o opentrace ./cmd/opentrace
./opentrace
```

Requires Go 1.25+. All dependencies are pure Go — no CGO required.

## Running Tests

```bash
# Unit tests (fast, no Docker)
go test -short -race ./...

# Full suite including Postgres integration tests (needs Docker)
go test -race ./...

# Single package
go test -short -race ./internal/mcp/...

# Single test
go test -short -race -v -run TestScheduler ./internal/watcher/...
```

## Linting

```bash
go vet ./...
```

## Making Changes

1. Fork the repo and create a branch from `main`
2. Make your changes
3. Add or update tests as needed
4. Ensure `go test -short -race ./...` and `go vet ./...` pass
5. Open a pull request

## Code Conventions

- HTTP responses use `writeJSON(w, status, data)` / `writeError(w, status, msg)`
- Store interfaces are per-domain (`DataSourceStore`, `WatcherStore`, etc.) — when adding methods, update all mock implementations
- MCP tool args arrive as `float64` from JSON — cast accordingly
- SQLite: `?` placeholders, booleans as INTEGER 0/1, timestamps as RFC3339 TEXT
- Use `log/slog` for logging, never the old `log` package

## Reporting Issues

- **Bugs:** Use the [bug report template](https://github.com/adham90/opentrace/issues/new?template=bug_report.md)
- **Features:** Use the [feature request template](https://github.com/adham90/opentrace/issues/new?template=feature_request.md)

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
