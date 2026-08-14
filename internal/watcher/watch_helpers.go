package watcher

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ErrInvalidWatchConfig marks an evaluation failure that is caused by the watch
// definition itself (a malformed condition tree, an unknown operator/metric, a
// filter the log store cannot express) rather than by a transient store or
// network problem. Such a watch can never succeed, so the evaluator disables it
// instead of retrying it on every scheduler tick and every ingest batch.
var ErrInvalidWatchConfig = errors.New("invalid watch configuration")

// configErrorf builds an ErrInvalidWatchConfig-wrapped error.
func configErrorf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidWatchConfig, fmt.Sprintf(format, args...))
}

// watchQueryLevels is the set of level values a count query may filter on. It
// mirrors internal/ingest's validLevels: a watch must be able to ask for
// exactly the levels ingest is able to store, and no others. An unknown level
// (a typo like "critical" or "err") can never match a stored row, so accepting
// it would produce a watch that reports 0 forever — green, silent and useless.
//
// NOTE: duplicated from internal/ingest (limits.go / flat_handler.go) because
// that package is owned elsewhere and the helper is unexported there. If the
// level vocabulary ever moves to a shared package, both copies should go.
var watchQueryLevels = map[string]string{
	"debug": "debug",
	"info":  "info",
	"warn":  "warn",
	// ingest's normalizeLevel canonicalises "warning" to "warn" on write, so a
	// watch querying level:warning must match the stored "warn" rows.
	"warning": "warn",
	"error":   "error",
	"fatal":   "fatal",
}

// watchQueryLevelList is the human-readable form used in config errors. It
// lists canonical levels only ("warning" is an accepted alias of "warn").
const watchQueryLevelList = "debug, info, warn, error, fatal"

// normalizeQueryLevel canonicalises a count query's level value the same way
// ingest canonicalises a level on write. ok is false for unknown levels.
func normalizeQueryLevel(level string) (string, bool) {
	canonical, ok := watchQueryLevels[strings.ToLower(strings.TrimSpace(level))]
	return canonical, ok
}

// countQueryFilter is the subset of a count condition's `query` string that the
// log store's aggregation params can express.
type countQueryFilter struct {
	Level   string
	Service string
}

// apply merges the parsed filter into log-count params. Environment is never
// overridable from a query string because it is an authorization boundary.
func (f countQueryFilter) apply(p store.LogCountParams) store.LogCountParams {
	if f.Level != "" {
		p.Level = f.Level
	}
	if f.Service != "" {
		p.Service = f.Service
	}
	return p
}

// parseCountQuery parses a count condition's `query` filter.
//
// Only `field:value` terms that the log store's count/distinct aggregations can
// express are supported (level and service). Anything else — free text, an
// unknown field, or an attempt to widen the environment scope — is rejected as
// a configuration error rather than silently ignored, because silently ignoring
// it turns a narrow "count errors" rule into "count every log".
func parseCountQuery(query string, condService string) (countQueryFilter, error) {
	var f countQueryFilter
	for _, term := range strings.Fields(query) {
		key, value, ok := strings.Cut(term, ":")
		if !ok || value == "" {
			return f, configErrorf("count query term %q is not supported (use level:<level> and/or service:<name>)", term)
		}
		switch strings.ToLower(key) {
		case "level":
			canonical, ok := normalizeQueryLevel(value)
			if !ok {
				return f, configErrorf("count query level %q is not a level the system stores (supported: %s)", value, watchQueryLevelList)
			}
			f.Level = canonical
		case "service":
			if condService != "" && !strings.EqualFold(condService, value) {
				return f, configErrorf("count query service %q conflicts with the condition's service %q", value, condService)
			}
			f.Service = value
		case "env", "environment":
			return f, configErrorf("count query may not set %q — the environment comes from the watch", key)
		default:
			return f, configErrorf("count query field %q is not supported by the log store (supported: level, service)", key)
		}
	}
	return f, nil
}

// parseDurationOr parses a duration string, falling back to def when the value
// is empty or malformed.
func parseDurationOr(value string, def time.Duration) time.Duration {
	if value == "" {
		return def
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return def
	}
	return d
}
