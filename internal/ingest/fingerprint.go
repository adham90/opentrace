package ingest

import (
	"strings"

	"github.com/adham90/opentrace/internal/fingerprint"
	"github.com/adham90/opentrace/pkg/store"
)

// GenerateErrorFingerprint computes a server-side error fingerprint for a log entry.
// It resolves the error class and source file from the entry (including from a
// backtrace when the SDK didn't send them as fields) and defers the hash itself to
// internal/fingerprint, which is the one definition shared with the columnar ingest
// pipeline. See that package for why the algorithm lives outside this one.
//
// This replaces SDK-side fingerprinting for consistency across all languages.
func GenerateErrorFingerprint(e *store.LogEntry) string {
	level := strings.ToLower(e.Level)
	if level != "error" && level != "fatal" {
		return ""
	}

	exceptionClass := e.ExceptionClass
	if exceptionClass == "" {
		// Try metadata
		if e.Metadata != nil {
			if v, ok := e.Metadata["error_class"].(string); ok {
				exceptionClass = v
			}
		}
	}

	sourceFile := e.SourceFile
	if sourceFile == "" {
		// Try extracting from metadata backtrace
		sourceFile = extractSourceFileFromBacktrace(e.Metadata)
	}

	return fingerprint.Compute(e.Service, exceptionClass, sourceFile, e.Message)
}

// extractSourceFileFromBacktrace pulls the first in-app file from the backtrace
// metadata. Works with any language because it just takes the first entry and
// strips the line/column suffix.
func extractSourceFileFromBacktrace(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}

	bt, ok := metadata["backtrace"].([]any)
	if !ok || len(bt) == 0 {
		return ""
	}

	// Also check stack_trace (Node SDK uses this)
	if len(bt) == 0 {
		if st, ok := metadata["stack_trace"].(string); ok && st != "" {
			return extractFileFromStackLine(st)
		}
	}

	frame, ok := bt[0].(string)
	if !ok || frame == "" {
		return ""
	}

	return extractFileFromFrame(frame)
}

// extractFileFromFrame extracts just the file path from a backtrace frame,
// stripping line numbers and method names. Handles common formats:
//
//	Ruby:  app/controllers/payments_controller.rb:87:in `charge'
//	Node:  at PaymentsController.charge (app/controllers/payments.js:87:12)
//	Python: File "app/controllers/payments.py", line 87, in charge
//	Go:    app/controllers/payments.go:87
func extractFileFromFrame(frame string) string {
	frame = strings.TrimSpace(frame)

	// Node.js: "at Something (file:line:col)" or "at file:line:col"
	if strings.HasPrefix(frame, "at ") {
		if idx := strings.LastIndex(frame, "("); idx >= 0 {
			frame = frame[idx+1:]
			frame = strings.TrimSuffix(frame, ")")
		} else {
			frame = strings.TrimPrefix(frame, "at ")
		}
	}

	// Python: File "path", line N, in func
	if strings.HasPrefix(frame, "File \"") {
		frame = strings.TrimPrefix(frame, "File \"")
		if idx := strings.Index(frame, "\""); idx >= 0 {
			return frame[:idx]
		}
	}

	// Strip :line:col or :line:in `method'
	// Split on ":" and take the first part (file path)
	parts := strings.SplitN(frame, ":", 2)
	return strings.TrimSpace(parts[0])
}

// extractFileFromStackLine extracts the file from the first line of a stack trace string.
func extractFileFromStackLine(stack string) string {
	lines := strings.SplitN(stack, "\n", 3)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Error:") || strings.HasPrefix(line, "TypeError:") {
			continue
		}
		f := extractFileFromFrame(line)
		if f != "" {
			return f
		}
	}
	return ""
}

// NormalizeMessage strips dynamic values from an error message so that
// structurally identical messages produce the same fingerprint. Retained as the
// package's public name for existing callers; the implementation is shared with
// the columnar pipeline.
func NormalizeMessage(msg string) string {
	return fingerprint.Normalize(msg)
}
