package ingest

import (
	"crypto/md5"
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/adham90/opentrace/pkg/store"
)

// GenerateErrorFingerprint computes a server-side error fingerprint for a log entry.
// The fingerprint groups "same error, different occurrence" using a language-agnostic
// algorithm inspired by Bugsnag's default grouping:
//
//	MD5(service + error_class + source_file)[:16]
//
// When source_file is absent, falls back to:
//
//	MD5(service + error_class + normalized_message)[:16]
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

	service := e.Service

	h := md5.New()
	h.Write([]byte(service))
	h.Write([]byte{0}) // separator
	h.Write([]byte(exceptionClass))
	h.Write([]byte{0})

	if sourceFile != "" {
		// Primary: group by source file (no line number — survives refactors)
		h.Write([]byte(sourceFile))
	} else {
		// Fallback: group by normalized message
		h.Write([]byte(NormalizeMessage(e.Message)))
	}

	return hex.EncodeToString(h.Sum(nil))[:16]
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

// Regex patterns for normalizing dynamic values in error messages.
var (
	reUUIDs    = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	reHexIDs   = regexp.MustCompile(`\b[0-9a-fA-F]{16,}\b`)
	reNumbers  = regexp.MustCompile(`\b\d{2,}\b`)
	reQuoted   = regexp.MustCompile(`"[^"]{4,}"`)
	reSQuoted  = regexp.MustCompile(`'[^']{4,}'`)
	reIPAddrs  = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}(:\d+)?\b`)
	reURLPaths = regexp.MustCompile(`/[a-zA-Z0-9_-]+/[0-9]+`)
)

// NormalizeMessage strips dynamic values from an error message so that
// structurally identical messages produce the same fingerprint.
// Order matters: replace most specific patterns first.
func NormalizeMessage(msg string) string {
	if len(msg) > 500 {
		msg = msg[:500]
	}
	msg = reUUIDs.ReplaceAllString(msg, "<UUID>")
	msg = reIPAddrs.ReplaceAllString(msg, "<IP>")
	msg = reHexIDs.ReplaceAllString(msg, "<HEX>")
	msg = reURLPaths.ReplaceAllString(msg, "/<PATH>/<N>")
	msg = reNumbers.ReplaceAllString(msg, "<N>")
	msg = reQuoted.ReplaceAllString(msg, "<STR>")
	msg = reSQuoted.ReplaceAllString(msg, "<STR>")
	return msg
}
