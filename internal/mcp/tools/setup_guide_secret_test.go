package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/testutil/mocks"
)

// The setup guide inlines the ingest API key so an operator can copy-paste it.
// That key authenticates ingest and CLI-read requests, so a read-only member
// receiving it is escalated to ingest-capable — the same credential and the
// same escalation that the overview settings action was fixed for.
func TestSetupGuide_DoesNotLeakAPIKeyToMembers(t *testing.T) {
	const secret = "ot_live_supersecret_key_value"

	ss := mocks.NewSettingsStore()
	ss.APIKey = secret

	member := SetupDeps{SettingsStore: ss, IsAdmin: false}
	for _, framework := range []string{"rails", "node", "express", "nextjs", "django", "fastapi", "python", "go"} {
		result, err := HandleSetupGuide(context.Background(), member, map[string]any{"framework": framework})
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", framework, err)
		}
		text := extractText(t, result)
		if strings.Contains(text, secret) {
			t.Errorf("%s: member guide leaked the ingest API key:\n%s", framework, text)
		}
		if !strings.Contains(text, "YOUR_API_KEY") {
			t.Errorf("%s: member guide should fall back to the placeholder, got:\n%s", framework, text)
		}
	}
}

// Admins keep the copy-pasteable guide.
func TestSetupGuide_AdminStillGetsRealKey(t *testing.T) {
	const secret = "ot_live_supersecret_key_value"

	adminStore := mocks.NewSettingsStore()
	adminStore.APIKey = secret

	admin := SetupDeps{SettingsStore: adminStore, IsAdmin: true}
	result, err := HandleSetupGuide(context.Background(), admin, map[string]any{"framework": "rails"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text := extractText(t, result); !strings.Contains(text, secret) {
		t.Errorf("admin guide should inline the real key, got:\n%s", text)
	}
}
