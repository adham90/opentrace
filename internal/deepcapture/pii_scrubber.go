package deepcapture

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"strings"
)

// PIIConfig is the scrubbing configuration stored in app_config.
type PIIConfig struct {
	Enabled         bool            `json:"enabled"`
	Builtin         BuiltinPatterns `json:"builtin"`
	SensitiveFields []string        `json:"sensitive_fields"`
	CustomPatterns  []CustomPattern `json:"custom_patterns"`
	SkipDomains     []string        `json:"skip_domains"`
	SkipServices    []string        `json:"skip_services"`
}

// BuiltinPatterns controls which built-in PII regex patterns are active.
type BuiltinPatterns struct {
	CreditCards  bool `json:"credit_cards"`
	Emails       bool `json:"emails"`
	PhoneNumbers bool `json:"phone_numbers"`
	SSN          bool `json:"ssn"`
	IPAddresses  bool `json:"ip_addresses"`
}

// CustomPattern defines a user-supplied regex pattern for PII detection.
type CustomPattern struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern"`
}

// FilteredValue is the replacement string for scrubbed PII.
const FilteredValue = "[FILTERED]"

// Pre-compiled builtin patterns.
var (
	creditCardRe = regexp.MustCompile(`\b(?:\d[ -]*?){13,19}\b`)
	emailRe      = regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Z|a-z]{2,}\b`)
	phoneRe      = regexp.MustCompile(`\b(?:\+?\d{1,3}[-.\s]?)?\(?\d{3}\)?[-.\s]?\d{3}[-.\s]?\d{4}\b`)
	ssnRe        = regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)
	ipRe         = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
)

// piiQuerier is satisfied by both *sql.DB and *sql.Tx, allowing LoadPIIConfig
// to work inside or outside a transaction.
type piiQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// LoadPIIConfig reads the PII config from app_config. If the row does not
// exist or cannot be parsed, sensible defaults are returned.
func LoadPIIConfig(ctx context.Context, q piiQuerier) PIIConfig {
	cfg := defaultPIIConfig()

	var configJSON string
	err := q.QueryRowContext(ctx, "SELECT value FROM app_config WHERE key = 'pii_scrubbing'").Scan(&configJSON)
	if err == nil && configJSON != "" {
		_ = json.Unmarshal([]byte(configJSON), &cfg)
	}
	return cfg
}

// defaultPIIConfig returns the default PII scrubbing configuration.
func defaultPIIConfig() PIIConfig {
	return PIIConfig{
		Enabled: true,
		Builtin: BuiltinPatterns{
			CreditCards:  true,
			Emails:       true,
			PhoneNumbers: true,
			SSN:          true,
		},
		SensitiveFields: []string{"password", "token", "secret", "authorization", "api_key"},
	}
}

// ScrubDocument walks all string fields in the document and replaces PII
// matches according to the supplied configuration.
func ScrubDocument(doc *Document, cfg PIIConfig) {
	if !cfg.Enabled || doc == nil || doc.Event == nil {
		return
	}

	// Build regex list from config.
	var patterns []*regexp.Regexp
	if cfg.Builtin.CreditCards {
		patterns = append(patterns, creditCardRe)
	}
	if cfg.Builtin.Emails {
		patterns = append(patterns, emailRe)
	}
	if cfg.Builtin.PhoneNumbers {
		patterns = append(patterns, phoneRe)
	}
	if cfg.Builtin.SSN {
		patterns = append(patterns, ssnRe)
	}
	if cfg.Builtin.IPAddresses {
		patterns = append(patterns, ipRe)
	}

	for _, cp := range cfg.CustomPatterns {
		if re, err := regexp.Compile(cp.Pattern); err == nil {
			patterns = append(patterns, re)
		}
	}

	// Build sensitive field set (case-insensitive lookup).
	sensitiveFields := make(map[string]bool, len(cfg.SensitiveFields))
	for _, f := range cfg.SensitiveFields {
		sensitiveFields[strings.ToLower(f)] = true
	}

	// Build skip-domains set.
	skipDomains := make(map[string]bool, len(cfg.SkipDomains))
	for _, d := range cfg.SkipDomains {
		skipDomains[d] = true
	}

	// Scrub each domain section of the event.
	if !skipDomains["request"] {
		doc.Event.Request = scrubJSON(doc.Event.Request, patterns, sensitiveFields)
	}
	if !skipDomains["response"] {
		doc.Event.Response = scrubJSON(doc.Event.Response, patterns, sensitiveFields)
	}
	if !skipDomains["db"] && doc.Event.DB != nil {
		doc.Event.DB.Queries = scrubJSON(doc.Event.DB.Queries, patterns, sensitiveFields)
	}
	if !skipDomains["external"] {
		doc.Event.External = scrubJSON(doc.Event.External, patterns, sensitiveFields)
	}
	if !skipDomains["email"] {
		doc.Event.Email = scrubJSON(doc.Event.Email, patterns, sensitiveFields)
	}
	if !skipDomains["audit"] {
		doc.Event.Audit = scrubJSON(doc.Event.Audit, patterns, sensitiveFields)
	}
}

// scrubJSON parses JSON, walks all strings, scrubs PII, and re-marshals.
func scrubJSON(raw json.RawMessage, patterns []*regexp.Regexp, sensitiveFields map[string]bool) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}

	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		return raw
	}

	scrubbed := scrubValue("", data, patterns, sensitiveFields)
	result, err := json.Marshal(scrubbed)
	if err != nil {
		return raw
	}
	return result
}

func scrubValue(key string, val any, patterns []*regexp.Regexp, sensitiveFields map[string]bool) any {
	switch v := val.(type) {
	case map[string]any:
		for k, child := range v {
			if sensitiveFields[strings.ToLower(k)] {
				v[k] = FilteredValue
			} else {
				v[k] = scrubValue(k, child, patterns, sensitiveFields)
			}
		}
		return v
	case []any:
		for i, child := range v {
			v[i] = scrubValue(key, child, patterns, sensitiveFields)
		}
		return v
	case string:
		return scrubString(v, patterns)
	default:
		return val
	}
}

func scrubString(s string, patterns []*regexp.Regexp) string {
	for _, re := range patterns {
		s = re.ReplaceAllString(s, FilteredValue)
	}
	return s
}
