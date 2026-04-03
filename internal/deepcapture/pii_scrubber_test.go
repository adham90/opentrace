package deepcapture

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// setupPIITestDB creates a minimal in-memory DB with an app_config table.
func setupPIITestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := t.TempDir() + "/pii_test.db"
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("opening test SQLite: %v", err)
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS app_config (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL DEFAULT ''
	)`)
	if err != nil {
		db.Close()
		t.Fatalf("creating app_config table: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestLoadPIIConfig_DefaultsWhenNoConfig(t *testing.T) {
	db := setupPIITestDB(t)

	cfg := LoadPIIConfig(context.Background(), db)

	if !cfg.Enabled {
		t.Error("expected Enabled=true by default")
	}
	if !cfg.Builtin.CreditCards {
		t.Error("expected Builtin.CreditCards=true by default")
	}
	if !cfg.Builtin.Emails {
		t.Error("expected Builtin.Emails=true by default")
	}
	if !cfg.Builtin.SSN {
		t.Error("expected Builtin.SSN=true by default")
	}
	if cfg.Builtin.IPAddresses {
		t.Error("expected Builtin.IPAddresses=false by default")
	}
	if len(cfg.SensitiveFields) == 0 {
		t.Error("expected non-empty default SensitiveFields")
	}
}

func TestLoadPIIConfig_ReadsFromDB(t *testing.T) {
	db := setupPIITestDB(t)

	cfgJSON := `{"enabled":false,"builtin":{"credit_cards":false,"emails":true,"phone_numbers":false,"ssn":false,"ip_addresses":true},"sensitive_fields":["secret_key"]}`
	_, err := db.Exec("INSERT INTO app_config (key, value) VALUES ('pii_scrubbing', ?)", cfgJSON)
	if err != nil {
		t.Fatalf("inserting config: %v", err)
	}

	cfg := LoadPIIConfig(context.Background(), db)

	if cfg.Enabled {
		t.Error("expected Enabled=false from DB config")
	}
	if cfg.Builtin.CreditCards {
		t.Error("expected CreditCards=false from DB config")
	}
	if !cfg.Builtin.IPAddresses {
		t.Error("expected IPAddresses=true from DB config")
	}
	if len(cfg.SensitiveFields) != 1 || cfg.SensitiveFields[0] != "secret_key" {
		t.Errorf("expected SensitiveFields=[secret_key], got %v", cfg.SensitiveFields)
	}
}

func TestScrubDocument_CreditCards(t *testing.T) {
	doc := &Document{
		Event: &Event{
			Request: json.RawMessage(`{"card":"4111 1111 1111 1111","name":"Alice"}`),
		},
	}
	cfg := PIIConfig{
		Enabled: true,
		Builtin: BuiltinPatterns{CreditCards: true},
	}

	ScrubDocument(doc, cfg)

	var req map[string]any
	if err := json.Unmarshal(doc.Event.Request, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	card, ok := req["card"].(string)
	if !ok {
		t.Fatal("card field missing or not a string")
	}
	if !strings.Contains(card, FilteredValue) {
		t.Errorf("expected credit card to be scrubbed, got: %s", card)
	}
	name, _ := req["name"].(string)
	if name != "Alice" {
		t.Errorf("expected name to remain 'Alice', got: %s", name)
	}
}

func TestScrubDocument_Emails(t *testing.T) {
	doc := &Document{
		Event: &Event{
			Request: json.RawMessage(`{"email":"user@example.com","subject":"hello"}`),
		},
	}
	cfg := PIIConfig{
		Enabled: true,
		Builtin: BuiltinPatterns{Emails: true},
	}

	ScrubDocument(doc, cfg)

	var req map[string]any
	if err := json.Unmarshal(doc.Event.Request, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	email, _ := req["email"].(string)
	if email != FilteredValue {
		t.Errorf("expected email to be scrubbed, got: %s", email)
	}
	subject, _ := req["subject"].(string)
	if subject != "hello" {
		t.Errorf("expected subject to remain 'hello', got: %s", subject)
	}
}

func TestScrubDocument_SSN(t *testing.T) {
	doc := &Document{
		Event: &Event{
			External: json.RawMessage(`[{"body":"SSN is 123-45-6789 here"}]`),
		},
	}
	cfg := PIIConfig{
		Enabled: true,
		Builtin: BuiltinPatterns{SSN: true},
	}

	ScrubDocument(doc, cfg)

	var ext []map[string]any
	if err := json.Unmarshal(doc.Event.External, &ext); err != nil {
		t.Fatalf("unmarshal external: %v", err)
	}
	body, _ := ext[0]["body"].(string)
	if strings.Contains(body, "123-45-6789") {
		t.Errorf("expected SSN to be scrubbed, got: %s", body)
	}
	if !strings.Contains(body, FilteredValue) {
		t.Errorf("expected FilteredValue in body, got: %s", body)
	}
}

func TestScrubDocument_SensitiveFields(t *testing.T) {
	doc := &Document{
		Event: &Event{
			Request: json.RawMessage(`{"password":"hunter2","username":"alice","Token":"abc123","API_KEY":"xyz"}`),
		},
	}
	cfg := PIIConfig{
		Enabled:         true,
		SensitiveFields: []string{"password", "token", "api_key"},
	}

	ScrubDocument(doc, cfg)

	var req map[string]any
	if err := json.Unmarshal(doc.Event.Request, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if req["password"] != FilteredValue {
		t.Errorf("expected password to be filtered, got: %v", req["password"])
	}
	if req["Token"] != FilteredValue {
		t.Errorf("expected Token to be filtered (case-insensitive), got: %v", req["Token"])
	}
	if req["API_KEY"] != FilteredValue {
		t.Errorf("expected API_KEY to be filtered (case-insensitive), got: %v", req["API_KEY"])
	}
	if req["username"] != "alice" {
		t.Errorf("expected username to remain 'alice', got: %v", req["username"])
	}
}

func TestScrubDocument_SkipDomains(t *testing.T) {
	doc := &Document{
		Event: &Event{
			Request:  json.RawMessage(`{"email":"user@example.com"}`),
			Response: json.RawMessage(`{"email":"admin@example.com"}`),
			External: json.RawMessage(`[{"email":"ext@example.com"}]`),
		},
	}
	cfg := PIIConfig{
		Enabled:     true,
		Builtin:     BuiltinPatterns{Emails: true},
		SkipDomains: []string{"request", "external"},
	}

	ScrubDocument(doc, cfg)

	// Request should NOT be scrubbed (skipped).
	var req map[string]any
	if err := json.Unmarshal(doc.Event.Request, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if req["email"] != "user@example.com" {
		t.Errorf("expected request email to remain (skip_domains), got: %v", req["email"])
	}

	// Response SHOULD be scrubbed.
	var resp map[string]any
	if err := json.Unmarshal(doc.Event.Response, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp["email"] != FilteredValue {
		t.Errorf("expected response email to be scrubbed, got: %v", resp["email"])
	}

	// External should NOT be scrubbed (skipped).
	var ext []map[string]any
	if err := json.Unmarshal(doc.Event.External, &ext); err != nil {
		t.Fatalf("unmarshal external: %v", err)
	}
	if ext[0]["email"] != "ext@example.com" {
		t.Errorf("expected external email to remain (skip_domains), got: %v", ext[0]["email"])
	}
}

func TestScrubDocument_DisabledIsNoOp(t *testing.T) {
	original := `{"password":"hunter2","email":"user@example.com"}`
	doc := &Document{
		Event: &Event{
			Request: json.RawMessage(original),
		},
	}
	cfg := PIIConfig{
		Enabled:         false,
		Builtin:         BuiltinPatterns{Emails: true},
		SensitiveFields: []string{"password"},
	}

	ScrubDocument(doc, cfg)

	if string(doc.Event.Request) != original {
		t.Errorf("expected no change when disabled, got: %s", doc.Event.Request)
	}
}

func TestScrubDocument_NilDocument(t *testing.T) {
	cfg := PIIConfig{Enabled: true}

	// Should not panic.
	ScrubDocument(nil, cfg)
}

func TestScrubDocument_NilEvent(t *testing.T) {
	doc := &Document{Event: nil}
	cfg := PIIConfig{Enabled: true}

	// Should not panic.
	ScrubDocument(doc, cfg)
}

func TestScrubDocument_CustomPatterns(t *testing.T) {
	doc := &Document{
		Event: &Event{
			Request: json.RawMessage(`{"data":"order ref ABC-12345 confirmed"}`),
		},
	}
	cfg := PIIConfig{
		Enabled: true,
		CustomPatterns: []CustomPattern{
			{Name: "order_ref", Pattern: `ABC-\d{5}`},
		},
	}

	ScrubDocument(doc, cfg)

	var req map[string]any
	if err := json.Unmarshal(doc.Event.Request, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	data, _ := req["data"].(string)
	if strings.Contains(data, "ABC-12345") {
		t.Errorf("expected custom pattern to be scrubbed, got: %s", data)
	}
	if !strings.Contains(data, FilteredValue) {
		t.Errorf("expected FilteredValue in data, got: %s", data)
	}
}

func TestScrubDocument_InvalidCustomPatternIgnored(t *testing.T) {
	doc := &Document{
		Event: &Event{
			Request: json.RawMessage(`{"data":"test value"}`),
		},
	}
	cfg := PIIConfig{
		Enabled: true,
		CustomPatterns: []CustomPattern{
			{Name: "bad_regex", Pattern: `[invalid`},
		},
	}

	// Should not panic; invalid pattern is simply skipped.
	ScrubDocument(doc, cfg)

	var req map[string]any
	if err := json.Unmarshal(doc.Event.Request, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if req["data"] != "test value" {
		t.Errorf("expected data unchanged when custom pattern is invalid, got: %v", req["data"])
	}
}

func TestScrubDocument_DBQueries(t *testing.T) {
	doc := &Document{
		Event: &Event{
			DB: &DBSection{
				Queries: json.RawMessage(`[{"raw_sql":"SELECT * FROM users WHERE email = 'user@example.com'"}]`),
			},
		},
	}
	cfg := PIIConfig{
		Enabled: true,
		Builtin: BuiltinPatterns{Emails: true},
	}

	ScrubDocument(doc, cfg)

	var queries []map[string]any
	if err := json.Unmarshal(doc.Event.DB.Queries, &queries); err != nil {
		t.Fatalf("unmarshal queries: %v", err)
	}
	rawSQL, _ := queries[0]["raw_sql"].(string)
	if strings.Contains(rawSQL, "user@example.com") {
		t.Errorf("expected email in SQL to be scrubbed, got: %s", rawSQL)
	}
	if !strings.Contains(rawSQL, FilteredValue) {
		t.Errorf("expected FilteredValue in SQL, got: %s", rawSQL)
	}
}

func TestScrubDocument_NestedObjects(t *testing.T) {
	doc := &Document{
		Event: &Event{
			Request: json.RawMessage(`{"user":{"password":"secret","profile":{"ssn":"123-45-6789"}}}`),
		},
	}
	cfg := PIIConfig{
		Enabled:         true,
		Builtin:         BuiltinPatterns{SSN: true},
		SensitiveFields: []string{"password"},
	}

	ScrubDocument(doc, cfg)

	var req map[string]any
	if err := json.Unmarshal(doc.Event.Request, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	user, _ := req["user"].(map[string]any)
	if user["password"] != FilteredValue {
		t.Errorf("expected nested password to be filtered, got: %v", user["password"])
	}
	profile, _ := user["profile"].(map[string]any)
	ssn, _ := profile["ssn"].(string)
	if strings.Contains(ssn, "123-45-6789") {
		t.Errorf("expected nested SSN to be scrubbed, got: %s", ssn)
	}
}

func TestScrubDocument_EmptyJSON(t *testing.T) {
	doc := &Document{
		Event: &Event{
			Request:  nil,
			Response: json.RawMessage(`{}`),
			External: json.RawMessage(``),
		},
	}
	cfg := PIIConfig{
		Enabled:         true,
		Builtin:         BuiltinPatterns{Emails: true},
		SensitiveFields: []string{"password"},
	}

	// Should not panic on nil/empty JSON sections.
	ScrubDocument(doc, cfg)
}

func TestScrubDocument_EmailSection(t *testing.T) {
	doc := &Document{
		Event: &Event{
			Email: json.RawMessage(`[{"to":"user@example.com","token":"abc123"}]`),
		},
	}
	cfg := PIIConfig{
		Enabled:         true,
		Builtin:         BuiltinPatterns{Emails: true},
		SensitiveFields: []string{"token"},
	}

	ScrubDocument(doc, cfg)

	var emails []map[string]any
	if err := json.Unmarshal(doc.Event.Email, &emails); err != nil {
		t.Fatalf("unmarshal email: %v", err)
	}
	if emails[0]["to"] != FilteredValue {
		t.Errorf("expected email 'to' to be scrubbed, got: %v", emails[0]["to"])
	}
	if emails[0]["token"] != FilteredValue {
		t.Errorf("expected token to be filtered, got: %v", emails[0]["token"])
	}
}

func TestScrubDocument_AuditSection(t *testing.T) {
	doc := &Document{
		Event: &Event{
			Audit: json.RawMessage(`[{"changed_fields":{"email":["old@test.com","new@test.com"]}}]`),
		},
	}
	cfg := PIIConfig{
		Enabled: true,
		Builtin: BuiltinPatterns{Emails: true},
	}

	ScrubDocument(doc, cfg)

	var audits []map[string]any
	if err := json.Unmarshal(doc.Event.Audit, &audits); err != nil {
		t.Fatalf("unmarshal audit: %v", err)
	}
	changed, _ := audits[0]["changed_fields"].(map[string]any)
	emailArr, _ := changed["email"].([]any)
	for _, e := range emailArr {
		s, _ := e.(string)
		if strings.Contains(s, "@") {
			t.Errorf("expected email in audit to be scrubbed, got: %s", s)
		}
	}
}
