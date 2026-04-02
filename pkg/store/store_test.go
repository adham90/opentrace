package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Sentinel errors
// ---------------------------------------------------------------------------

func TestSentinelErrors_AreDistinct(t *testing.T) {
	sentinels := []struct {
		name string
		err  error
	}{
		{"ErrNotFound", ErrNotFound},
		{"ErrEmailTaken", ErrEmailTaken},
		{"ErrLastAdmin", ErrLastAdmin},
	}

	for i, a := range sentinels {
		if a.err == nil {
			t.Fatalf("%s is nil", a.name)
		}
		if a.err.Error() == "" {
			t.Fatalf("%s has empty message", a.name)
		}
		for j, b := range sentinels {
			if i != j && errors.Is(a.err, b.err) {
				t.Errorf("%s should not match %s", a.name, b.name)
			}
		}
	}
}

func TestSentinelErrors_WrappingPreservesIs(t *testing.T) {
	wrapped := fmt.Errorf("context: %w", ErrNotFound)
	if !errors.Is(wrapped, ErrNotFound) {
		t.Error("wrapped error should match ErrNotFound via errors.Is")
	}
	if errors.Is(wrapped, ErrEmailTaken) {
		t.Error("wrapped ErrNotFound should not match ErrEmailTaken")
	}
}

// ---------------------------------------------------------------------------
// Enum string constants — catches copy-paste or renaming drift
// ---------------------------------------------------------------------------

func TestErrorGroupStatusValues(t *testing.T) {
	cases := map[ErrorGroupStatus]string{
		ErrorGroupUnresolved: "unresolved",
		ErrorGroupResolved:   "resolved",
		ErrorGroupIgnored:    "ignored",
	}
	for got, want := range cases {
		if string(got) != want {
			t.Errorf("ErrorGroupStatus: got %q, want %q", got, want)
		}
	}
}

func TestHealthCheckStatusValues(t *testing.T) {
	cases := map[HealthCheckStatus]string{
		HealthCheckUp:       "up",
		HealthCheckDown:     "down",
		HealthCheckDegraded: "degraded",
	}
	for got, want := range cases {
		if string(got) != want {
			t.Errorf("HealthCheckStatus: got %q, want %q", got, want)
		}
	}
}

func TestWatchMetricValues(t *testing.T) {
	cases := map[WatchMetric]string{
		WatchMetricErrorRate:    "error_rate",
		WatchMetricResponseTime: "response_time",
		WatchMetricP95Response:  "p95_response",
		WatchMetricLogCount:     "log_count",
		WatchMetricErrorCount:   "error_count",
		WatchMetricHeartbeat:    "heartbeat",
		WatchMetricSQLCount:     "sql_count",
		WatchMetricCacheHitRate: "cache_hit_rate",
	}
	for got, want := range cases {
		if string(got) != want {
			t.Errorf("WatchMetric: got %q, want %q", got, want)
		}
	}
}

func TestWatchOperatorValues(t *testing.T) {
	cases := map[WatchOperator]string{
		WatchOpGreaterThan:      "gt",
		WatchOpGreaterThanEqual: "gte",
		WatchOpLessThan:         "lt",
		WatchOpLessThanEqual:    "lte",
		WatchOpEqual:            "eq",
		WatchOpNotEqual:         "neq",
	}
	for got, want := range cases {
		if string(got) != want {
			t.Errorf("WatchOperator: got %q, want %q", got, want)
		}
	}
}

func TestWatchStatusValues(t *testing.T) {
	cases := map[WatchStatus]string{
		WatchStatusActive:    "active",
		WatchStatusTriggered: "triggered",
		WatchStatusResolved:  "resolved",
		WatchStatusExpired:   "expired",
	}
	for got, want := range cases {
		if string(got) != want {
			t.Errorf("WatchStatus: got %q, want %q", got, want)
		}
	}
}

func TestWatchUrgencyValues(t *testing.T) {
	cases := map[WatchUrgency]string{
		WatchUrgencyLow:      "low",
		WatchUrgencyNormal:   "normal",
		WatchUrgencyHigh:     "high",
		WatchUrgencyCritical: "critical",
	}
	for got, want := range cases {
		if string(got) != want {
			t.Errorf("WatchUrgency: got %q, want %q", got, want)
		}
	}
}

func TestServerStatusValues(t *testing.T) {
	cases := map[ServerStatus]string{
		ServerOnline:  "online",
		ServerOffline: "offline",
		ServerUnknown: "unknown",
	}
	for got, want := range cases {
		if string(got) != want {
			t.Errorf("ServerStatus: got %q, want %q", got, want)
		}
	}
}

func TestUserRoleValues(t *testing.T) {
	cases := map[UserRole]string{
		RoleAdmin:  "admin",
		RoleMember: "member",
	}
	for got, want := range cases {
		if string(got) != want {
			t.Errorf("UserRole: got %q, want %q", got, want)
		}
	}
}

func TestConnectorTypeValues(t *testing.T) {
	cases := map[ConnectorType]string{
		ConnectorLogs:          "logs",
		ConnectorDatabase:      "database",
		ConnectorMySQL:         "mysql",
		ConnectorRedis:         "redis",
		ConnectorTurso:         "turso",
		ConnectorMonitoring:    "monitoring",
		ConnectorServerMetrics: "server_metrics",
	}
	for got, want := range cases {
		if string(got) != want {
			t.Errorf("ConnectorType: got %q, want %q", got, want)
		}
	}
}

func TestConnectorStatusValues(t *testing.T) {
	cases := map[ConnectorStatus]string{
		StatusConnected:    "connected",
		StatusDisconnected: "disconnected",
		StatusError:        "error",
	}
	for got, want := range cases {
		if string(got) != want {
			t.Errorf("ConnectorStatus: got %q, want %q", got, want)
		}
	}
}

func TestCodeEntityTypeValues(t *testing.T) {
	cases := map[CodeEntityType]string{
		CodeEntityFile:       "file",
		CodeEntityController: "controller",
		CodeEntityEndpoint:   "endpoint",
	}
	for got, want := range cases {
		if string(got) != want {
			t.Errorf("CodeEntityType: got %q, want %q", got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// JSON round-trip for models with non-trivial serialization
// ---------------------------------------------------------------------------

func TestSamplingRule_JSONRoundTrip(t *testing.T) {
	orig := SamplingRule{
		Service:    "api-server",
		Rate:       0.25,
		KeepErrors: true,
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded SamplingRule
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded != orig {
		t.Errorf("round-trip mismatch:\n got  %+v\nwant %+v", decoded, orig)
	}

	// Verify the JSON keys match expected wire format.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	for _, key := range []string{"service", "rate", "keep_errors"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("expected JSON key %q not found in %s", key, string(data))
		}
	}
}

func TestTelegramConfig_JSONRoundTrip(t *testing.T) {
	orig := TelegramConfig{
		BotToken: "123456:ABC-DEF",
		ChatID:   "-100123",
		Enabled:  true,
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded TelegramConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded != orig {
		t.Errorf("round-trip mismatch:\n got  %+v\nwant %+v", decoded, orig)
	}
}

func TestRetentionSettings_JSONRoundTrip(t *testing.T) {
	orig := RetentionSettings{
		RetentionDays:       30,
		MetricRetentionDays: 90,
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded RetentionSettings
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded != orig {
		t.Errorf("round-trip mismatch:\n got  %+v\nwant %+v", decoded, orig)
	}
}

func TestWatchEvidenceBundle_JSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second) // truncate for JSON precision
	val := 42.5

	orig := WatchEvidenceBundle{
		RecentErrors: []WatchEvidenceError{
			{ExceptionClass: "RuntimeError", Message: "boom", Count: 5, IsNew: false},
		},
		NewErrors: []WatchEvidenceError{
			{ExceptionClass: "TypeError", Message: "nil ref", Count: 1, IsNew: true},
		},
		AffectedEndpoints: []WatchEndpointDelta{
			{Path: "/api/users", CurrentDurationMs: 500, BaselineDurationMs: 100, DeltaPct: 400},
		},
		RelevantLogs: []WatchEvidenceLog{
			{Timestamp: now, Level: "error", Message: "connection timeout", Service: "api"},
		},
		Timeline: []WatchTimelineEvent{
			{Timestamp: now, Event: "threshold_breached", Value: &val},
		},
		BaselineDiff: &WatchBaselineDiff{
			MetricName:    "error_rate",
			BaselineValue: 0.01,
			CurrentValue:  0.15,
			DeltaPct:      1400,
		},
		SourceFiles: []string{"app/controllers/users_controller.rb"},
		TraceIDs:    []string{"abc123", "def456"},
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded WatchEvidenceBundle
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Spot-check key fields rather than deep-equal (time comparison is tricky).
	if len(decoded.RecentErrors) != 1 || decoded.RecentErrors[0].ExceptionClass != "RuntimeError" {
		t.Errorf("RecentErrors mismatch: %+v", decoded.RecentErrors)
	}
	if len(decoded.NewErrors) != 1 || !decoded.NewErrors[0].IsNew {
		t.Errorf("NewErrors mismatch: %+v", decoded.NewErrors)
	}
	if len(decoded.AffectedEndpoints) != 1 || decoded.AffectedEndpoints[0].DeltaPct != 400 {
		t.Errorf("AffectedEndpoints mismatch: %+v", decoded.AffectedEndpoints)
	}
	if decoded.BaselineDiff == nil || decoded.BaselineDiff.MetricName != "error_rate" {
		t.Errorf("BaselineDiff mismatch: %+v", decoded.BaselineDiff)
	}
	if len(decoded.SourceFiles) != 1 {
		t.Errorf("SourceFiles mismatch: %+v", decoded.SourceFiles)
	}
	if len(decoded.TraceIDs) != 2 {
		t.Errorf("TraceIDs mismatch: %+v", decoded.TraceIDs)
	}
	if len(decoded.Timeline) != 1 || decoded.Timeline[0].Value == nil || *decoded.Timeline[0].Value != val {
		t.Errorf("Timeline mismatch: %+v", decoded.Timeline)
	}
}

func TestLogSearchParams_OmitsEmptyFields(t *testing.T) {
	// A zero-value LogSearchParams should not emit optional fields.
	p := LogSearchParams{}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}

	// These fields have omitempty and should be absent at zero value.
	shouldBeAbsent := []string{
		"query", "service", "level", "environment", "trace_id",
		"request_id", "event_type", "exception_class", "error_fingerprint",
		"source_file", "start", "end", "metadata_filter", "exclude",
	}
	for _, key := range shouldBeAbsent {
		if _, ok := raw[key]; ok {
			t.Errorf("zero-value LogSearchParams should omit %q, but it was present", key)
		}
	}
}


// ---------------------------------------------------------------------------
// Pointer/optional field serialization
// ---------------------------------------------------------------------------

func TestErrorGroup_OptionalFieldsOmitted(t *testing.T) {
	eg := ErrorGroup{
		Fingerprint: "abc123",
		Service:     "api",
		Status:      ErrorGroupUnresolved,
		FirstSeenAt: time.Now(),
		LastSeenAt:  time.Now(),
	}

	data, err := json.Marshal(eg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}

	// These should be omitted when nil/empty.
	shouldBeAbsent := []string{"last_log_id", "resolved_at", "ignored_at", "events", "common_context"}
	for _, key := range shouldBeAbsent {
		if _, ok := raw[key]; ok {
			t.Errorf("nil/empty field %q should be omitted from JSON", key)
		}
	}
}
