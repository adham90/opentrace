package ingest

import (
	"encoding/json"
	"testing"
	"time"
)

// The deep-capture arrays are a contract between the SDKs and the deep_capture
// MCP tool, which reads them out of LogEntry.Metadata by name. Nothing between
// the two validates the shape, so this test pins it: an exact opentrace_ruby /
// opentrace_node payload must survive ingest with every documented key intact.
//
// It is the cheapest place to catch a rename on either side — a body key that
// stops matching does not error anywhere, it just makes the data invisible.
func TestFlatToLogEntry_PreservesDeepCaptureArrays(t *testing.T) {
	body := `{
		"request_body": "{\"cart_id\":\"c_991\"}",
		"sql": [
			{"raw_sql": "SELECT * FROM carts WHERE id = ?", "duration_ms": 4.2,
			 "fingerprint": "fp-carts", "table": "carts", "cached": false}
		],
		"http": [
			{"method": "POST", "url": "https://api.stripe.com/v1/charges",
			 "host": "api.stripe.com", "vendor": "stripe", "status": 200, "duration_ms": 61},
			{"method": "POST", "url": "https://api.openai.com/v1/chat/completions",
			 "host": "api.openai.com", "vendor": "openai", "status": 200, "duration_ms": 812,
			 "ai_model": "gpt-4o", "ai_input_tokens": 820, "ai_output_tokens": 145}
		],
		"email": [{"mailer_class": "OrderMailer", "subject": "Your order"}],
		"file":  [{"action": "upload", "filename": "receipt.pdf", "size_bytes": 2048}],
		"audit": [{"action": "update", "record_type": "Order", "record_id": "42", "actor_id": "u-7"}]
	}`

	h := &Handler{}
	entry := h.flatToLogEntry(flatEntry{
		Level: "info", Message: "POST /checkout 200", Body: json.RawMessage(body),
	}, time.Now().UTC())

	for _, key := range []string{"sql", "http", "email", "file", "audit"} {
		rows, ok := entry.Metadata[key].([]any)
		if !ok || len(rows) == 0 {
			t.Fatalf("body.%s did not survive ingest as a non-empty array: %#v", key, entry.Metadata[key])
		}
	}
	if entry.Metadata["request_body"] != `{"cart_id":"c_991"}` {
		t.Errorf("request_body lost: %#v", entry.Metadata["request_body"])
	}

	// Per-call HTTP is the whole point of body.http: host attribution, and the
	// token counts riding on the call that spent them.
	calls := entry.Metadata["http"].([]any)
	if len(calls) != 2 {
		t.Fatalf("want 2 http calls, got %d", len(calls))
	}
	stripe := calls[0].(map[string]any)
	if stripe["host"] != "api.stripe.com" || stripe["vendor"] != "stripe" {
		t.Errorf("host/vendor lost: %#v", stripe)
	}
	llm := calls[1].(map[string]any)
	if llm["ai_model"] != "gpt-4o" || llm["ai_input_tokens"] != float64(820) || llm["ai_output_tokens"] != float64(145) {
		t.Errorf("ai_* fields lost: %#v", llm)
	}
}
