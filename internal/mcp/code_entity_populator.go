package mcp

import (
	"context"
	"log/slog"
	"regexp"
	"strings"

	"github.com/adham90/opentrace/pkg/store"
)

// Stack trace regex patterns for various languages.
var (
	rubyBacktraceRe   = regexp.MustCompile(`([^\s:]+\.\w+):\d+`)
	goBacktraceRe     = regexp.MustCompile(`([^\s]+\.go):\d+`)
	jsBacktraceRe     = regexp.MustCompile(`\(([^)]+\.[jt]sx?):\d+:\d+\)`)
	pythonBacktraceRe = regexp.MustCompile(`File "([^"]+\.py)", line \d+`)
)

// knownExtensions filters out non-source files.
var knownExtensions = map[string]bool{
	".rb": true, ".go": true, ".js": true, ".ts": true,
	".jsx": true, ".tsx": true, ".py": true, ".java": true,
	".rs": true, ".ex": true, ".exs": true, ".php": true,
}

// ExtractFilePaths returns unique source file paths from a backtrace string.
func ExtractFilePaths(backtrace string) []string {
	seen := make(map[string]bool)
	var result []string

	patterns := []*regexp.Regexp{rubyBacktraceRe, goBacktraceRe, jsBacktraceRe, pythonBacktraceRe}
	for _, pat := range patterns {
		for _, match := range pat.FindAllStringSubmatch(backtrace, -1) {
			if len(match) < 2 {
				continue
			}
			path := match[1]

			// Skip vendor/stdlib paths
			if strings.Contains(path, "/vendor/") ||
				strings.Contains(path, "/node_modules/") ||
				strings.Contains(path, "/gems/") ||
				strings.Contains(path, "/site-packages/") {
				continue
			}

			// Check extension
			hasKnownExt := false
			for ext := range knownExtensions {
				if strings.HasSuffix(path, ext) {
					hasKnownExt = true
					break
				}
			}
			if !hasKnownExt {
				continue
			}

			if !seen[path] {
				seen[path] = true
				result = append(result, path)
			}
		}
	}

	return result
}

// PopulateFromErrorLog extracts file paths from a log entry's backtrace
// and increments error counts on the corresponding code entities.
func PopulateFromErrorLog(ctx context.Context, ces store.CodeEntityStore, entry store.LogEntry) {
	if ces == nil {
		return
	}

	service := entry.Service

	// Extract from backtrace field
	backtrace := ""
	if entry.Metadata != nil {
		if bt, ok := entry.Metadata["backtrace"].(string); ok {
			backtrace = bt
		}
	}

	// Also use SourceFile if available
	if entry.SourceFile != "" {
		if err := ces.IncrementError(ctx, store.CodeEntityFile, entry.SourceFile, service); err != nil {
			slog.Debug("code_entity_populator: IncrementError failed", "error", err, "file", entry.SourceFile)
		}
	}

	if backtrace == "" {
		return
	}

	paths := ExtractFilePaths(backtrace)
	for _, path := range paths {
		if path == entry.SourceFile {
			continue // already counted
		}
		if err := ces.IncrementError(ctx, store.CodeEntityFile, path, service); err != nil {
			slog.Debug("code_entity_populator: IncrementError failed", "error", err, "file", path)
		}
	}
}

// LinkInvestigationToEntities increments investigation counts for code entities
// associated with a session's investigated error fingerprints.
func LinkInvestigationToEntities(ctx context.Context, ces store.CodeEntityStore, egs store.ErrorGroupStore, sess *store.InvestigationSession) {
	if ces == nil || egs == nil || sess == nil {
		return
	}

	for _, fp := range sess.InvestigatedErrorFingerprints {
		eg, err := egs.Get(ctx, fp)
		if err != nil || eg == nil || eg.SourceFile == "" {
			continue
		}
		if err := ces.IncrementInvestigation(ctx, store.CodeEntityFile, eg.SourceFile, eg.Service); err != nil {
			slog.Debug("code_entity_populator: IncrementInvestigation failed",
				"error", err, "file", eg.SourceFile, "fingerprint", fp)
		}
	}
}
