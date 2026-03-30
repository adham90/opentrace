package tools

import (
	"context"
	"fmt"
)

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
