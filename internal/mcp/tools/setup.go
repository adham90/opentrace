package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/adham90/opentrace/internal/mcp/envscope"
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

// scopeWarning returns a short human-readable note for scope shapes that
// change how the agent must call tools. An empty string means the common
// single-env path where no warning is needed.
func scopeWarning(s envscope.EnvScope) string {
	switch s.Mode() {
	case "denied":
		return "this token has no environment scope — data-access tools will reject calls"
	case "multi":
		return "token covers multiple environments; every tool call must specify environment=..."
	case "legacy_wildcard":
		return "this token uses the deprecated wildcard scope; missing env args fall back to the server default on writes"
	default:
		return ""
	}
}

func HandleSetupStatus(ctx context.Context, d SetupDeps) (*CallToolResult, error) {
	status := map[string]any{
		"server": "ok",
	}

	// Env scope surfaces the caller's multi-env authorization so the agent
	// knows whether to auto-fill, require, or refuse environment args on
	// subsequent calls. See docs/multi-env-support.md decisions #3-#4.
	scope := envscope.From(ctx)
	status["env_scope"] = scope.Allowed
	if scope.Allowed == nil {
		status["env_scope"] = []string{}
	}
	status["scope_mode"] = scope.Mode()
	if warn := scopeWarning(scope); warn != "" {
		status["scope_warning"] = warn
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

