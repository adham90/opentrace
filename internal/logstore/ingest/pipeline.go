package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"regexp"
	"strings"

	"github.com/adham90/opentrace/internal/logstore/chunk"
)

// Pipeline processes raw SDK entries before WAL storage.
// Steps: Parse → Sample → PII scrub body → Extract error fields → Expand in-request logs
type Pipeline struct {
	samplingRules  []SamplingRule
	piiConfig      PIIConfig
}

// NewPipeline creates a new ingest pipeline.
func NewPipeline(samplingRules []SamplingRule, piiConfig PIIConfig) *Pipeline {
	return &Pipeline{
		samplingRules: samplingRules,
		piiConfig:     piiConfig,
	}
}

// Process runs the full ingest pipeline on a batch of entries.
// Returns processed entries (may be more than input due to log expansion,
// or fewer due to sampling).
func (p *Pipeline) Process(entries []chunk.Entry) []chunk.Entry {
	// Step 1: Sampling
	entries = p.applySampling(entries)

	// Steps 2-4: For each entry, process body if present
	var result []chunk.Entry
	for i := range entries {
		e := &entries[i]

		// Step 2: Compute error fingerprint from SDK-provided flat fields
		if isErrorLevel(e.Level) {
			computeErrorFingerprint(e)
		}

		if len(e.Body) > 0 {
			// Step 3: PII scrub body
			e.Body = p.scrubBody(e.Body)

			// Step 4: Expand in-request logs
			expanded := expandInRequestLogs(e)
			result = append(result, *e)
			result = append(result, expanded...)
		} else {
			result = append(result, *e)
		}
	}

	return result
}

// --- Sampling ---

// SamplingRule defines a per-service log sampling policy.
type SamplingRule struct {
	Service    string  `json:"service"`     // service name, or "*" for default
	Rate       float64 `json:"rate"`        // 0.0-1.0 (1.0 = keep all)
	KeepErrors bool    `json:"keep_errors"` // always keep error/warn/fatal logs
}

func (p *Pipeline) applySampling(entries []chunk.Entry) []chunk.Entry {
	if len(p.samplingRules) == 0 {
		return entries
	}

	ruleMap := make(map[string]*SamplingRule, len(p.samplingRules))
	var defaultRule *SamplingRule
	for i := range p.samplingRules {
		if p.samplingRules[i].Service == "*" {
			defaultRule = &p.samplingRules[i]
		} else {
			ruleMap[p.samplingRules[i].Service] = &p.samplingRules[i]
		}
	}

	filtered := make([]chunk.Entry, 0, len(entries))
	for _, e := range entries {
		rule := ruleMap[e.Service]
		if rule == nil {
			rule = defaultRule
		}
		if rule == nil {
			filtered = append(filtered, e)
			continue
		}

		if rule.KeepErrors && isErrorLevel(e.Level) {
			filtered = append(filtered, e)
			continue
		}

		if rule.Rate >= 1.0 || rand.Float64() < rule.Rate {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// --- PII Scrubbing ---

// PIIConfig controls what gets scrubbed from body JSON.
type PIIConfig struct {
	Enabled         bool     `json:"enabled"`
	ScrubCreditCards bool    `json:"scrub_credit_cards"`
	ScrubEmails      bool    `json:"scrub_emails"`
	ScrubPhones      bool    `json:"scrub_phones"`
	ScrubSSN         bool    `json:"scrub_ssn"`
	SensitiveFields []string `json:"sensitive_fields"`
}

// DefaultPIIConfig returns sensible PII scrubbing defaults.
func DefaultPIIConfig() PIIConfig {
	return PIIConfig{
		Enabled:          true,
		ScrubCreditCards: true,
		ScrubEmails:      true,
		ScrubPhones:      true,
		ScrubSSN:         true,
		SensitiveFields:  []string{"password", "token", "secret", "authorization", "api_key", "credit_card"},
	}
}

const filteredValue = "[FILTERED]"

var (
	creditCardRe = regexp.MustCompile(`\b(?:\d[ -]*?){13,19}\b`)
	emailRe      = regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Z|a-z]{2,}\b`)
	phoneRe      = regexp.MustCompile(`\b(?:\+?\d{1,3}[-.\s]?)?\(?\d{3}\)?[-.\s]?\d{3}[-.\s]?\d{4}\b`)
	ssnRe        = regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)
)

func (p *Pipeline) scrubBody(body json.RawMessage) json.RawMessage {
	if !p.piiConfig.Enabled || len(body) == 0 {
		return body
	}

	var patterns []*regexp.Regexp
	if p.piiConfig.ScrubCreditCards {
		patterns = append(patterns, creditCardRe)
	}
	if p.piiConfig.ScrubEmails {
		patterns = append(patterns, emailRe)
	}
	if p.piiConfig.ScrubPhones {
		patterns = append(patterns, phoneRe)
	}
	if p.piiConfig.ScrubSSN {
		patterns = append(patterns, ssnRe)
	}

	sensitiveFields := make(map[string]bool, len(p.piiConfig.SensitiveFields))
	for _, f := range p.piiConfig.SensitiveFields {
		sensitiveFields[strings.ToLower(f)] = true
	}

	var data any
	if err := json.Unmarshal(body, &data); err != nil {
		return body
	}

	scrubbed := scrubValue("", data, patterns, sensitiveFields)
	result, err := json.Marshal(scrubbed)
	if err != nil {
		return body
	}
	return result
}

func scrubValue(key string, val any, patterns []*regexp.Regexp, sensitiveFields map[string]bool) any {
	switch v := val.(type) {
	case map[string]any:
		for k, child := range v {
			if sensitiveFields[strings.ToLower(k)] {
				v[k] = filteredValue
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
		for _, re := range patterns {
			v = re.ReplaceAllString(v, filteredValue)
		}
		return v
	default:
		return val
	}
}

// --- Error Fingerprint Computation ---

// computeErrorFingerprint sets the error_fingerprint from SDK-provided flat fields.
// The SDK sends error_class, source_file, source_line as top-level fields.
// The server only computes the fingerprint hash — no body parsing needed.
func computeErrorFingerprint(e *chunk.Entry) {
	if e.ErrorClass != "" || e.SourceFile != "" {
		e.ErrorFingerprint = computeFingerprint(e.ErrorClass, e.SourceFile, e.SourceLine)
	}
}

func computeFingerprint(class, file string, line int) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d", class, file, line)))
	return hex.EncodeToString(h[:8]) // 16-char hex
}

// --- In-Request Log Expansion ---

// expandInRequestLogs extracts body.logs entries into separate log entries.
func expandInRequestLogs(parent *chunk.Entry) []chunk.Entry {
	if len(parent.Body) == 0 {
		return nil
	}

	var body struct {
		Logs []struct {
			Level   string         `json:"level"`
			Message string         `json:"message"`
			At      float64        `json:"at"` // offset_ms from request start
			Meta    map[string]any `json:"metadata,omitempty"`
		} `json:"logs"`
	}

	if err := json.Unmarshal(parent.Body, &body); err != nil || len(body.Logs) == 0 {
		return nil
	}

	expanded := make([]chunk.Entry, 0, len(body.Logs))
	for _, l := range body.Logs {
		level := l.Level
		if level == "" {
			level = "info"
		}

		child := chunk.Entry{
			// ID and ReceivedAt will be assigned by WAL writer
			Ts:        parent.Ts + int64(l.At), // parent timestamp + offset
			Level:     level,
			Service:   parent.Service,
			Env:       parent.Env,
			Version:   parent.Version,
			Message:   l.Message,
			EventType: "in_request_log",
			TraceID:   parent.TraceID,
			RequestID: parent.RequestID,
			UserID:    parent.UserID,
			TenantID:  parent.TenantID,
		}
		expanded = append(expanded, child)
	}

	return expanded
}

// --- Helpers ---

func isErrorLevel(level string) bool {
	switch strings.ToLower(level) {
	case "error", "fatal":
		return true
	default:
		return false
	}
}
