package tools

import (
	"context"
	"fmt"
	"time"


	"github.com/adham90/opentrace/pkg/store"
)

// SetupDeps holds the stores needed by the setup tool.
type SetupDeps struct {
	LogStore      store.LogStore
	UserStore     store.UserStore
	SettingsStore store.SettingsStore
	DSStore       store.DataSourceStore
}

// SetupHandler returns a handler for the setup tool.
func SetupHandler(d SetupDeps) ToolHandlerFunc {
	return func(ctx context.Context, request *CallToolRequest) (*CallToolResult, error) {
		args := GetArguments(request)
		action := ArgString(args, "action")

		switch action {
		case "status":
			return HandleSetupStatus(ctx, d)
		case "detect":
			return HandleSetupDetect(ctx, d, args)
		case "guide":
			return HandleSetupGuide(ctx, d, args)
		case "db_guide":
			return HandleSetupDBGuide(ctx, d, args)
		case "verify":
			return HandleSetupVerify(ctx, d)
		default:
			return NewToolResultError("unknown action: " + action + ". Use: status, detect, guide, verify"), nil
		}
	}
}

func HandleSetupStatus(ctx context.Context, d SetupDeps) (*CallToolResult, error) {
	status := map[string]any{
		"server": "ok",
	}

	// User count
	if d.UserStore != nil {
		if count, err := d.UserStore.Count(ctx); err == nil {
			status["users"] = count
		}
	}

	// Log stats
	if d.LogStore != nil {
		now := time.Now()
		oneHourAgo := now.Add(-1 * time.Hour)
		oneDayAgo := now.Add(-24 * time.Hour)

		if counts, err := d.LogStore.CountByLevel(ctx, store.LogCountParams{Since: oneHourAgo, Until: now}); err == nil {
			total := 0
			for _, c := range counts {
				total += c
			}
			status["logs_last_hour"] = total
		}

		if counts, err := d.LogStore.CountByLevel(ctx, store.LogCountParams{Since: oneDayAgo, Until: now}); err == nil {
			total := 0
			for _, c := range counts {
				total += c
			}
			status["logs_last_24h"] = total
		}
	}

	// Connectors
	if d.DSStore != nil {
		if sources, err := d.DSStore.List(ctx, store.ListDataSourceParams{}); err == nil {
			connected := 0
			for _, s := range sources {
				if s.Status == store.StatusConnected {
					connected++
				}
			}
			status["connectors_total"] = len(sources)
			status["connectors_connected"] = connected
		}
	}

	// Data flowing?
	logsHour, _ := status["logs_last_hour"].(int)
	if logsHour > 0 {
		status["data_flowing"] = true
		status["message"] = fmt.Sprintf("OpenTrace is healthy. Receiving %d logs/hour.", logsHour)
	} else {
		status["data_flowing"] = false
		status["message"] = "OpenTrace is running but not receiving logs yet. Set up an SDK to start sending data."
	}

	return JSONResult(status)
}

func HandleSetupDetect(_ context.Context, _ SetupDeps, args map[string]any) (*CallToolResult, error) {
	filesStr := ArgString(args, "files")
	if filesStr == "" {
		return NewToolResultText(`To detect the framework, provide a comma-separated list of files in the project root.

Example: setup(action: "detect", files: "Gemfile,config/application.rb,Rakefile")

Or look for these common indicators:
- Gemfile + config/application.rb → Rails
- package.json + next.config.js → Next.js
- package.json + express in dependencies → Express/Node
- requirements.txt + manage.py → Django
- requirements.txt + main.py → FastAPI/Python
- go.mod → Go
- Cargo.toml → Rust`), nil
	}

	// Simple file-based framework detection
	framework := detectFramework(filesStr)

	result := map[string]any{
		"detected_framework": framework.name,
		"confidence":         framework.confidence,
		"sdk":                framework.sdk,
		"next_step":          fmt.Sprintf(`Run: setup(action: "guide", framework: "%s")`, framework.id),
	}

	return JSONResult(result)
}

type frameworkDetection struct {
	id         string
	name       string
	sdk        string
	confidence string
}

func detectFramework(files string) frameworkDetection {
	has := func(name string) bool {
		for _, f := range splitAndTrim(files) {
			if f == name {
				return true
			}
		}
		return false
	}

	switch {
	case has("Gemfile") && has("config/application.rb"):
		return frameworkDetection{"rails", "Ruby on Rails", "opentrace (Ruby gem)", "high"}
	case has("Gemfile"):
		return frameworkDetection{"ruby", "Ruby", "opentrace (Ruby gem)", "medium"}
	case has("package.json") && has("next.config.js"):
		return frameworkDetection{"nextjs", "Next.js", "@opentrace/node", "high"}
	case has("package.json") && has("nuxt.config.ts"):
		return frameworkDetection{"nuxt", "Nuxt.js", "@opentrace/node", "high"}
	case has("package.json"):
		return frameworkDetection{"node", "Node.js", "@opentrace/node", "medium"}
	case has("requirements.txt") && has("manage.py"):
		return frameworkDetection{"django", "Django", "opentrace (Python package)", "high"}
	case has("requirements.txt") && has("main.py"):
		return frameworkDetection{"fastapi", "FastAPI", "opentrace (Python package)", "medium"}
	case has("requirements.txt") || has("pyproject.toml"):
		return frameworkDetection{"python", "Python", "opentrace (Python package)", "medium"}
	case has("go.mod"):
		return frameworkDetection{"go", "Go", "opentrace (Go module)", "medium"}
	case has("Cargo.toml"):
		return frameworkDetection{"rust", "Rust", "opentrace (coming soon)", "medium"}
	default:
		return frameworkDetection{"unknown", "Unknown", "Check https://github.com/adham90/opentrace for available SDKs", "low"}
	}
}

func splitAndTrim(s string) []string {
	var result []string
	for _, part := range splitComma(s) {
		trimmed := trimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func splitComma(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n') {
		end--
	}
	return s[start:end]
}

func HandleSetupGuide(ctx context.Context, d SetupDeps, args map[string]any) (*CallToolResult, error) {
	framework := ArgString(args, "framework")
	if framework == "" {
		return NewToolResultError("framework is required. Use: rails, node, express, nextjs, django, fastapi, python, go"), nil
	}

	// Get the real API key from settings
	apiKey := ""
	if d.SettingsStore != nil {
		if k, err := d.SettingsStore.GetAPIKey(ctx); err == nil && k != "" {
			apiKey = k
		}
	}

	guide := getFrameworkGuide(framework, apiKey)
	if guide == "" {
		return NewToolResultError("unknown framework: " + framework + ". Supported: rails, node, express, nextjs, django, fastapi, python, go"), nil
	}

	return NewToolResultText(guide), nil
}

func getFrameworkGuide(framework string, apiKey string) string {
	keyDisplay := apiKey
	if keyDisplay == "" {
		keyDisplay = "YOUR_API_KEY"
	}

	switch framework {
	case "rails":
		return fmt.Sprintf(`Add the gem to your Gemfile:

gem 'opentrace'

Run: bundle install

Create config/initializers/opentrace.rb:

OpenTrace.configure do |c|
  c.server_url = ENV.fetch("OPENTRACE_URL")
  c.api_key = ENV.fetch("OPENTRACE_KEY")
end

Add to your .env file:

OPENTRACE_URL=<server_url>
OPENTRACE_KEY=%s

Then restart your app and call setup(action: "verify") to confirm logs are flowing.`, keyDisplay)

	case "node", "express", "nextjs":
		return fmt.Sprintf(`Install the package:

npm install @opentrace-sdk/node

Add to your app entry point:

require('@opentrace-sdk/node').init({
  serverUrl: process.env.OPENTRACE_URL,
  apiKey: process.env.OPENTRACE_KEY,
});

Add to your .env file:

OPENTRACE_URL=<server_url>
OPENTRACE_KEY=%s

Then restart your app and call setup(action: "verify") to confirm logs are flowing.`, keyDisplay)

	case "python", "django", "fastapi":
		return fmt.Sprintf(`Install the package:

pip install opentrace

Add to your app:

import opentrace
opentrace.init(
    server_url=os.environ["OPENTRACE_URL"],
    api_key=os.environ["OPENTRACE_KEY"],
)

Add to your .env file:

OPENTRACE_URL=<server_url>
OPENTRACE_KEY=%s

Then restart your app and call setup(action: "verify") to confirm logs are flowing.`, keyDisplay)

	case "go":
		return fmt.Sprintf(`Add the module:

go get github.com/adham90/opentrace-go

Add to your app:

import opentrace "github.com/adham90/opentrace-go"

func main() {
    ot := opentrace.New(opentrace.Config{
        ServerURL: os.Getenv("OPENTRACE_URL"),
        APIKey:    os.Getenv("OPENTRACE_KEY"),
    })
    defer ot.Close()
}

Set environment variables:

OPENTRACE_URL=<server_url>
OPENTRACE_KEY=%s

Then restart your app and call setup(action: "verify") to confirm logs are flowing.`, keyDisplay)

	default:
		return ""
	}
}

func HandleSetupVerify(ctx context.Context, d SetupDeps) (*CallToolResult, error) {
	if d.LogStore == nil {
		return NewToolResultError("LogStore not configured"), nil
	}

	now := time.Now()
	fiveMinAgo := now.Add(-5 * time.Minute)
	oneHourAgo := now.Add(-1 * time.Hour)

	result := map[string]any{}

	// Check last 5 minutes
	if counts, err := d.LogStore.CountByLevel(ctx, store.LogCountParams{Since: fiveMinAgo, Until: now}); err == nil {
		total := 0
		for _, c := range counts {
			total += c
		}
		result["logs_last_5min"] = total

		if total > 0 {
			result["status"] = "receiving_data"
			result["message"] = fmt.Sprintf("Logs are flowing. %d logs received in the last 5 minutes.", total)
		} else {
			// Check last hour for recent data
			if counts, err := d.LogStore.CountByLevel(ctx, store.LogCountParams{Since: oneHourAgo, Until: now}); err == nil {
				hourTotal := 0
				for _, c := range counts {
					hourTotal += c
				}
				if hourTotal > 0 {
					result["status"] = "data_stale"
					result["logs_last_hour"] = hourTotal
					result["message"] = fmt.Sprintf("No logs in the last 5 minutes, but %d logs in the last hour. Your app may not be sending requests right now.", hourTotal)
				} else {
					result["status"] = "no_data"
					result["message"] = "No logs received yet. Make sure your app is running with the OpenTrace SDK configured and making requests."
					result["troubleshooting"] = []string{
						"Check that OPENTRACE_URL and OPENTRACE_KEY environment variables are set",
						"Verify your app can reach the OpenTrace server (curl https://YOUR_SERVER:8080/health)",
						"Make a few HTTP requests to your app to generate logs",
						"Check your app's logs for OpenTrace connection errors",
					}
				}
			}
		}
	}

	return JSONResult(result)
}

func HandleSetupDBGuide(_ context.Context, _ SetupDeps, args map[string]any) (*CallToolResult, error) {
	dbType := ArgString(args, "database")
	if dbType == "" {
		return NewToolResultError("database is required. Use: postgres, mysql, redis"), nil
	}

	guide := getDBGuide(dbType)
	if guide == "" {
		return NewToolResultError("unknown database: " + dbType + ". Supported: postgres, mysql, redis"), nil
	}

	return NewToolResultText(guide), nil
}

func getDBGuide(dbType string) string {
	switch dbType {
	case "postgres", "postgresql", "pg":
		return `## Connect Postgres to OpenTrace

### Step 1: Create a read-only user (IMPORTANT for security)

Run this on your Postgres server:

CREATE USER opentrace_reader WITH PASSWORD 'a_strong_password_here';
GRANT CONNECT ON DATABASE your_database TO opentrace_reader;
GRANT USAGE ON SCHEMA public TO opentrace_reader;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO opentrace_reader;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO opentrace_reader;

This user can only read — it cannot insert, update, delete, or drop anything.
OpenTrace also enforces read-only at the connection level (SET default_transaction_read_only = ON).

### Step 2: Connect via the agent

Tell your agent:
  "Connect my Postgres database at postgres://opentrace_reader:password@host:5432/dbname"

Or call:
  connectors(action: "create", type: "database", connection_string: "postgres://opentrace_reader:password@host:5432/dbname")

### Step 3: Test

  connectors(action: "test", id: "<connector_id>")

### What the agent can do after connecting

- Run SELECT queries against your tables
- EXPLAIN query plans (without ANALYZE for safety)
- Check pg_stat_statements for slow queries
- Inspect table sizes, index health, lock contention
- Detect N+1 queries and missing indexes

### Security

- Read-only Postgres user (database-level enforcement)
- SQL parsing rejects INSERT/UPDATE/DELETE/DROP before execution
- SET default_transaction_read_only = ON on every connection
- Statement timeout enforced (default: 5 seconds)
- Row limit enforced (default: 500 rows)
- Circuit breaker disconnects on repeated failures`

	case "mysql", "mariadb":
		return `## Connect MySQL to OpenTrace

### Step 1: Create a read-only user (IMPORTANT for security)

Run this on your MySQL server:

CREATE USER 'opentrace_reader'@'%' IDENTIFIED BY 'a_strong_password_here';
GRANT SELECT ON your_database.* TO 'opentrace_reader'@'%';
FLUSH PRIVILEGES;

This user can only read — it cannot modify any data.
OpenTrace also enforces read-only at the session level (SET SESSION transaction_read_only = 1).

### Step 2: Connect via the agent

Tell your agent:
  "Connect my MySQL database at opentrace_reader:password@tcp(host:3306)/dbname"

Or call:
  connectors(action: "create", type: "mysql", connection_string: "opentrace_reader:password@tcp(host:3306)/dbname")

### Step 3: Test

  connectors(action: "test", id: "<connector_id>")

### Security

- Read-only MySQL user (database-level enforcement)
- SQL parsing rejects INSERT/UPDATE/DELETE/DROP before execution
- SET SESSION transaction_read_only = 1 on every connection
- Statement timeout and row limits enforced`

	case "redis":
		return `## Connect Redis to OpenTrace

### Step 1: Create a read-only ACL user (recommended)

On Redis 6+:

ACL SETUSER opentrace on >a_strong_password_here ~* +@read +info +slowlog -@write -@admin -@dangerous

This user can only read keys and run INFO/SLOWLOG — no writes, no admin commands.

### Step 2: Connect via the agent

Tell your agent:
  "Connect my Redis at redis://opentrace:password@host:6379/0"

Or call:
  connectors(action: "create", type: "redis", connection_string: "redis://opentrace:password@host:6379/0")

### What the agent can do after connecting

- redis_info: Server stats, memory usage, connected clients
- redis_keys: Scan keys by pattern (non-blocking SCAN, not KEYS)
- redis_get: Read a key's value and type
- redis_slowlog: Recent slow commands

### Security

- Read-only Redis ACL user (server-level enforcement)
- Only read commands are exposed as tools
- No write, admin, or dangerous commands available`

	default:
		return ""
	}
}
