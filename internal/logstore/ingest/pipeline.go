package ingest

import (
	"encoding/json"
	"math/rand"
	"regexp"
	"strings"

	"github.com/adham90/opentrace/internal/fingerprint"
	"github.com/adham90/opentrace/internal/logstore/chunk"
)

// Pipeline processes raw SDK entries before WAL storage.
// Steps: Parse → Sample → PII scrub body → Extract error fields → Expand in-request logs
type Pipeline struct {
	samplingRules []SamplingRule
	piiConfig     PIIConfig
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

	// Steps 2-4: For each entry, fingerprint, scrub PII (message + body), expand.
	var result []chunk.Entry
	for i := range entries {
		e := &entries[i]

		// Step 2: Compute error fingerprint from SDK-provided flat fields
		if isErrorLevel(e.Level) {
			computeErrorFingerprint(e)
		}

		// Step 3: PII scrub — message, flat error message, and body.
		p.scrubEntry(e)

		result = append(result, *e)

		// Step 4: Expand in-request logs (from the already-scrubbed body).
		if len(e.Body) > 0 {
			result = append(result, expandInRequestLogs(e)...)
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
	Enabled          bool     `json:"enabled"`
	ScrubCreditCards bool     `json:"scrub_credit_cards"`
	ScrubEmails      bool     `json:"scrub_emails"`
	ScrubPhones      bool     `json:"scrub_phones"`
	ScrubSSN         bool     `json:"scrub_ssn"`
	SensitiveFields  []string `json:"sensitive_fields"`
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

// buildScrubbers returns the active regex patterns and sensitive-field set from
// the PII config. Shared by message and body scrubbing so both stay consistent.
func (p *Pipeline) buildScrubbers() ([]*regexp.Regexp, map[string]bool) {
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
	return patterns, sensitiveFields
}

// scrubEntry redacts PII from the entry's message, flat error message, and body.
// Previously only the opaque body was scrubbed, so PII in the log message — the
// single most common location — shipped unredacted even with scrubbing on.
func (p *Pipeline) scrubEntry(e *chunk.Entry) {
	if !p.piiConfig.Enabled {
		return
	}
	patterns, sensitiveFields := p.buildScrubbers()
	if len(patterns) == 0 && len(sensitiveFields) == 0 {
		return
	}
	e.Message = scrubString(e.Message, patterns)
	e.ErrorMessage = scrubString(e.ErrorMessage, patterns)
	if len(e.Body) > 0 {
		e.Body = scrubBodyWith(e.Body, patterns, sensitiveFields)
	}
}

// scrubString applies the PII regex patterns to a plain string.
func scrubString(s string, patterns []*regexp.Regexp) string {
	if s == "" {
		return s
	}
	for _, re := range patterns {
		s = re.ReplaceAllString(s, filteredValue)
	}
	return s
}

func scrubBodyWith(body json.RawMessage, patterns []*regexp.Regexp, sensitiveFields map[string]bool) json.RawMessage {
	var data any
	if err := json.Unmarshal(body, &data); err != nil {
		return body
	}
	scrubbed := scrubValue("", data, patterns, sensitiveFields, 0)
	result, err := json.Marshal(scrubbed)
	if err != nil {
		return body
	}
	return result
}

func (p *Pipeline) scrubBody(body json.RawMessage) json.RawMessage {
	if !p.piiConfig.Enabled || len(body) == 0 {
		return body
	}
	patterns, sensitiveFields := p.buildScrubbers()
	return scrubBodyWith(body, patterns, sensitiveFields)
}

// maxScrubDepth bounds recursion into nested body JSON. json.Unmarshal already
// caps nesting, but an explicit guard keeps scrubbing cheap and panic-free on
// pathologically deep attacker-controlled bodies.
const maxScrubDepth = 32

func scrubValue(key string, val any, patterns []*regexp.Regexp, sensitiveFields map[string]bool, depth int) any {
	if depth > maxScrubDepth {
		return val
	}
	switch v := val.(type) {
	case map[string]any:
		for k, child := range v {
			if sensitiveFields[strings.ToLower(k)] {
				v[k] = filteredValue
			} else {
				v[k] = scrubValue(k, child, patterns, sensitiveFields, depth+1)
			}
		}
		return v
	case []any:
		for i, child := range v {
			v[i] = scrubValue(key, child, patterns, sensitiveFields, depth+1)
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
//
// This must produce the same value the error-group store keys rows by, which is why
// it delegates to internal/fingerprint rather than hashing here. It previously used
// its own SHA256("class:file:line"), so a log entry and the error group it belonged
// to carried different fingerprints and nothing could join them: an error group's
// "recent occurrences" looks up logs by the group's fingerprint and always came back
// empty. Note the line number is excluded on purpose — see that package.
func computeErrorFingerprint(e *chunk.Entry) {
	if e.ErrorClass == "" && e.SourceFile == "" {
		return
	}
	e.ErrorFingerprint = fingerprint.Compute(e.Service, e.ErrorClass, e.SourceFile, e.Message)
}

// --- In-Request Log Expansion ---

// maxExpandedLogs bounds how many body.logs entries a single request may expand
// into, so one crafted request can't amplify into unbounded WAL entries.
const maxExpandedLogs = 1000

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

	// Cap expansion so a single request with a huge body.logs array can't
	// balloon memory/disk (the whole batch is appended under one fsync lock).
	logs := body.Logs
	if len(logs) > maxExpandedLogs {
		logs = logs[:maxExpandedLogs]
	}

	expanded := make([]chunk.Entry, 0, len(logs))
	for _, l := range logs {
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
