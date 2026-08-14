package tools

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// action: context — surrounding log entries around a log ID (from logContextHandler)
// ---------------------------------------------------------------------------

const (
	// contextWindow is how far either side of the anchor the surrounding-logs
	// search reaches. The window MUST be derived from the anchor: leaving Start
	// nil makes the store apply its own now-1h default, which for any anchor
	// older than an hour is an inverted range that can never match, so the
	// "before" context silently came back empty exactly when someone was
	// debugging a past incident.
	contextWindow = 24 * time.Hour

	// maxContextEntries bounds before/after so a caller cannot ask for an
	// unbounded slice on either side.
	maxContextEntries = 50
)

func LogsContext(ctx context.Context, args map[string]any, deps LogsDeps) (*CallToolResult, error) {
	InitLogsDeps(&deps)
	logID, ok := args["log_id"].(float64)
	if !ok || logID <= 0 {
		return NewToolResultError("log_id is required (positive integer)"), nil
	}

	before := 10
	if v, ok := args["before"].(float64); ok && v >= 0 {
		before = int(v)
		if before > maxContextEntries {
			before = maxContextEntries
		}
	}
	after := 10
	if v, ok := args["after"].(float64); ok && v >= 0 {
		after = int(v)
		if after > maxContextEntries {
			after = maxContextEntries
		}
	}
	sameService := ArgBool(args, "same_service")

	// Fetch the anchor log entry.
	anchor, err := deps.Logs.GetByID(ctx, int64(logID))
	if err != nil {
		return NewToolResultError(fmt.Sprintf("log entry %d not found: %v", int64(logID), err)), nil
	}

	// Env-scope gate: the anchor is fetched by ID, bypassing env-filtered
	// queries. Deny (as "not found") if the caller's token can't read the
	// anchor's environment so a cross-env log_id can't be probed.
	if !scopeAllowsEnv(ctx, anchor.Environment) {
		return NewToolResultError(fmt.Sprintf("log entry %d not found", int64(logID))), nil
	}

	// Window both sides around the anchor, never around "now".
	beforeStart := anchor.Timestamp.Add(-contextWindow)
	afterEnd := anchor.Timestamp.Add(contextWindow)

	// Fetch entries before (older timestamps, i.e. timestamp < anchor, order DESC, take `before`).
	var beforeEntries []store.LogEntry
	if before > 0 {
		beforeParams := store.LogSearchParams{
			Start:       &beforeStart,
			End:         &anchor.Timestamp,
			Environment: anchor.Environment,
			// +1 because End is inclusive, so the anchor itself occupies a slot
			// and is filtered out below; without it the caller asking for 10 got 9.
			Limit:   before + 1,
			SortAsc: false, // newest first so we get the closest entries
		}
		if sameService && anchor.Service != "" {
			beforeParams.Service = anchor.Service
		}
		beforeResult, err := deps.Logs.Search(ctx, beforeParams)
		if err != nil {
			return NewToolResultError(fmt.Sprintf("failed to fetch context before: %v", err)), nil
		}
		beforeEntries = beforeResult.Entries
	}

	// Filter out the anchor entry itself from before results.
	filtered := make([]store.LogEntry, 0, len(beforeEntries))
	for _, e := range beforeEntries {
		if e.ID != anchor.ID {
			filtered = append(filtered, e)
		}
	}
	beforeEntries = filtered
	if len(beforeEntries) > before {
		beforeEntries = beforeEntries[:before]
	}

	// Reverse so they're oldest-first.
	for i, j := 0, len(beforeEntries)-1; i < j; i, j = i+1, j-1 {
		beforeEntries[i], beforeEntries[j] = beforeEntries[j], beforeEntries[i]
	}

	// Fetch entries after (newer timestamps, i.e. timestamp > anchor, order ASC, take `after`).
	var afterEntries []store.LogEntry
	if after > 0 {
		afterParams := store.LogSearchParams{
			Start:       &anchor.Timestamp,
			End:         &afterEnd,
			Environment: anchor.Environment,
			Limit:       after + 1, // +1 because anchor might be included
			SortAsc:     true,
		}
		if sameService && anchor.Service != "" {
			afterParams.Service = anchor.Service
		}
		afterResult, err := deps.Logs.Search(ctx, afterParams)
		if err != nil {
			return NewToolResultError(fmt.Sprintf("failed to fetch context after: %v", err)), nil
		}
		afterEntries = afterResult.Entries
	}

	// Filter out the anchor entry itself.
	filteredAfter := make([]store.LogEntry, 0, len(afterEntries))
	for _, e := range afterEntries {
		if e.ID != anchor.ID {
			filteredAfter = append(filteredAfter, e)
		}
	}
	afterEntries = filteredAfter
	if len(afterEntries) > after {
		afterEntries = afterEntries[:after]
	}

	// Build response.
	type contextEntry struct {
		ID               int64          `json:"id"`
		Timestamp        string         `json:"timestamp"`
		Level            string         `json:"level"`
		Service          string         `json:"service,omitempty"`
		Message          string         `json:"message"`
		ExceptionClass   string         `json:"exception_class,omitempty"`
		ErrorFingerprint string         `json:"error_fingerprint,omitempty"`
		SourceFile       string         `json:"source_file,omitempty"`
		SourceLine       int            `json:"source_line,omitempty"`
		TraceID          string         `json:"trace_id,omitempty"`
		RequestID        string         `json:"request_id,omitempty"`
		Metadata         map[string]any `json:"metadata,omitempty"`
		Position         string         `json:"position"` // "before", "anchor", "after"
	}

	toContextEntry := func(e store.LogEntry, pos string, truncMsg bool) contextEntry {
		msg := e.Message
		if truncMsg && len(msg) > 500 {
			msg = msg[:500] + "..."
		}
		return contextEntry{
			ID:               e.ID,
			Timestamp:        e.Timestamp.Format(time.RFC3339Nano),
			Level:            e.Level,
			Service:          e.Service,
			Message:          msg,
			ExceptionClass:   e.ExceptionClass,
			ErrorFingerprint: e.ErrorFingerprint,
			SourceFile:       e.SourceFile,
			SourceLine:       e.SourceLine,
			TraceID:          e.TraceID,
			RequestID:        e.RequestID,
			Metadata:         e.Metadata,
			Position:         pos,
		}
	}

	ctxEntries := make([]contextEntry, 0, len(beforeEntries)+1+len(afterEntries))
	for _, e := range beforeEntries {
		ctxEntries = append(ctxEntries, toContextEntry(e, "before", true))
	}
	ctxEntries = append(ctxEntries, toContextEntry(*anchor, "anchor", false))
	for _, e := range afterEntries {
		ctxEntries = append(ctxEntries, toContextEntry(e, "after", true))
	}

	resp := map[string]any{
		"anchor_id":    int64(logID),
		"same_service": sameService,
		"before_count": len(beforeEntries),
		"after_count":  len(afterEntries),
		"entries":      ctxEntries,
		// The window actually scanned, centred on the anchor — not on now.
		"context_window": map[string]any{
			"start": beforeStart.Format(time.RFC3339),
			"end":   afterEnd.Format(time.RFC3339),
		},
	}

	return JSONResult(resp)
}

// ---------------------------------------------------------------------------
// action: attributes — discover distinct values for log fields (from listLogAttributesHandler)
// ---------------------------------------------------------------------------

// attributeFieldAliases maps the caller-facing field names to the names the log
// store actually knows. Passing "environment" or "exception_class" straight
// through produced "distinct values not supported for column ..." for fields the
// tool itself advertises.
var attributeFieldAliases = map[string]string{
	"environment":     "env",
	"exception_class": "error_class",
}

// attributeSupportedFields is the set of caller-facing fields value discovery
// can actually answer, in the order shown to the agent.
var attributeSupportedFields = []string{
	"service", "level", "environment", "event_type",
	"exception_class", "error_fingerprint", "request_id", "trace_id",
	"user_id", "tenant_id", "path", "handler", "host", "metadata_key",
}

// attributeProbeField maps a caller-facing field to the LogSearchParams filter
// that can prove a value exists inside a given environment. Fields absent here
// cannot be env-verified in this layer.
func attributeProbeParams(field, value string) (store.LogSearchParams, bool) {
	switch field {
	case "event_type":
		return store.LogSearchParams{EventType: value}, true
	case "exception_class":
		return store.LogSearchParams{ExceptionClass: value}, true
	case "error_fingerprint":
		return store.LogSearchParams{ErrorFingerprint: value}, true
	case "request_id":
		return store.LogSearchParams{RequestID: value}, true
	case "trace_id":
		return store.LogSearchParams{TraceID: value}, true
	default:
		return store.LogSearchParams{}, false
	}
}

// maxAttributeValues bounds both the returned value list and the number of
// per-value env verification probes.
const maxAttributeValues = 100

func LogsAttributes(ctx context.Context, args map[string]any, deps LogsDeps) (*CallToolResult, error) {
	InitLogsDeps(&deps)
	field := ArgString(args, "field")
	if field == "" {
		return NewToolResultError(fmt.Sprintf("field is required (one of: %s)",
			strings.Join(attributeSupportedFields, ", "))), nil
	}
	if !slices.Contains(attributeSupportedFields, field) {
		// Reject at the boundary rather than forwarding a name the store will
		// reject anyway (commit_hash and source_file were advertised for months
		// and always failed).
		return NewToolResultError(fmt.Sprintf("field %q is not supported for value discovery (supported: %s)",
			field, strings.Join(attributeSupportedFields, ", "))), nil
	}

	// Parse time range (default: 24h).
	contextSince, _, err := ResolveWindow(args, "24h")
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}
	// Resolve env scope so attribute discovery only reveals values from
	// environments the caller's token is authorized for.
	environment, err := ResolveEnv(ctx, args)
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}

	now := time.Now().UTC()
	params := store.LogCountParams{
		Since:       contextSince,
		Until:       now,
		Environment: environment,
	}
	params.Service = ArgString(args, "service")

	if field == "metadata_key" {
		keys, err := deps.Logs.MetadataKeys(ctx, params)
		if err != nil {
			return NewToolResultError(fmt.Sprintf("failed to list metadata keys: %v", err)), nil
		}
		if len(keys) == 0 {
			// Don't report "there are none": the storage engine keeps metadata
			// inside an opaque body and may not enumerate keys at all, which is
			// a different statement from "this window has no metadata".
			return EmptyResult("No metadata keys were returned for this window. Metadata key discovery may not be supported by the active log store — read the metadata field of a search result to see the keys an entry carries.")
		}
		return JSONResult(map[string]any{
			"field":  "metadata_key",
			"count":  len(keys),
			"values": keys,
			"hint":   "Use these keys with the metadata_filter parameter in log_search (e.g. metadata_filter: {\"host\": \"server-01\"}).",
		})
	}

	// environment: the caller can only ever be shown envs its token covers, and
	// the resolved scope already is that answer. Asking the store would read
	// segment dictionaries across every env.
	if field == "environment" && environment != "" {
		return JSONResult(map[string]any{
			"field":  "environment",
			"count":  1,
			"values": []string{environment},
		})
	}

	// service / level are derivable from env-filtered count queries, so they are
	// answered without relying on the store's env-blind dictionary scan.
	switch field {
	case "service":
		counts, cErr := deps.Logs.CountByService(ctx, params)
		if cErr == nil {
			values := make([]string, 0, len(counts))
			for _, c := range counts {
				if c.Service != "" {
					values = append(values, c.Service)
				}
			}
			return attributeValuesResult(field, values, environment)
		}
		slog.Warn("logs attributes service enumeration failed",
			"event", "logs_attributes_service_failed", "error", cErr)
	case "level":
		lc, cErr := deps.Logs.CountByLevel(ctx, params)
		if cErr == nil {
			values := make([]string, 0, len(lc.ByLevel))
			for level := range lc.ByLevel {
				values = append(values, level)
			}
			sort.Strings(values)
			return attributeValuesResult(field, values, environment)
		}
		slog.Warn("logs attributes level enumeration failed",
			"event", "logs_attributes_level_failed", "error", cErr)
	}

	storeField := field
	if alias, ok := attributeFieldAliases[field]; ok {
		storeField = alias
	}

	values, err := deps.Logs.Attributes(ctx, storeField, params)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to list values: %v", err)), nil
	}

	// The store's distinct-value scan is not environment-filtered, so when the
	// caller is pinned to an env every candidate has to be proven to exist in
	// that env before it is revealed. Values that cannot be probed are dropped
	// rather than leaked.
	if environment != "" && len(values) > 0 {
		if _, probeable := attributeProbeParams(field, "x"); probeable {
			kept := make([]string, 0, len(values))
			for i, v := range values {
				if i >= maxAttributeValues {
					break
				}
				probe, _ := attributeProbeParams(field, v)
				probe.Environment = environment
				probe.Start = &contextSince
				probe.End = &now
				probe.Limit = 1
				res, pErr := deps.Logs.Search(ctx, probe)
				if pErr != nil {
					slog.Warn("logs attributes env probe failed",
						"event", "logs_attributes_probe_failed", "field", field, "error", pErr)
					continue
				}
				if len(res.Entries) > 0 {
					kept = append(kept, v)
				}
			}
			values = kept
		}
	}

	return attributeValuesResult(field, values, environment)
}

// attributeValuesResult renders a value list, capped and de-duplicated, saying
// plainly when the list was truncated.
func attributeValuesResult(field string, values []string, environment string) (*CallToolResult, error) {
	seen := make(map[string]bool, len(values))
	unique := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		unique = append(unique, v)
	}
	if len(unique) == 0 {
		return EmptyResult(fmt.Sprintf("No %s values found in the specified time range.", field))
	}
	truncated := false
	if len(unique) > maxAttributeValues {
		unique = unique[:maxAttributeValues]
		truncated = true
	}
	resp := map[string]any{
		"field":    field,
		"count":    len(unique),
		"values":   unique,
		"complete": !truncated,
	}
	if environment != "" {
		resp["environment"] = environment
	}
	if truncated {
		resp["hint"] = fmt.Sprintf("Only the first %d values are shown.", maxAttributeValues)
	}
	return JSONResult(resp)
}
