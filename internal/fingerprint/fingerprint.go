// Package fingerprint holds the single definition of how an error is identified.
//
// It exists as a leaf package because two ingest paths need it and neither may
// import the other: the columnar pipeline stamps error_fingerprint onto each log
// entry, and the error-group store keys rows by the same value. They used to
// carry separate implementations — SHA256("class:file:line") on log entries and
// MD5(service + class + file) on groups — which never agreed on anything. The
// visible damage: an error group's "recent occurrences" searches the logs for the
// group's own fingerprint and matched nothing, so every group was a count with no
// way to read a single example of the error it counted.
//
// Anything that computes a fingerprint calls Compute. Adding a third local
// version is how the first two happened.
package fingerprint

import (
	"crypto/md5"
	"encoding/hex"
	"regexp"
	"strings"
)

// Compute returns the stable identity of an error occurrence:
//
//	MD5(service + errorClass + sourceFile)[:16]
//
// and, when there is no source file, falls back to the normalized message:
//
//	MD5(service + errorClass + normalize(message))[:16]
//
// The line number is deliberately excluded. Errors move down a file every time
// something is inserted above them; including the line would split one ongoing
// error into a new group on every unrelated edit, resetting its history exactly
// when the deploy that touched the file is the thing you want to correlate.
//
// service is part of the identity because the same exception class in two
// services is two different problems with two different owners.
func Compute(service, errorClass, sourceFile, message string) string {
	if errorClass == "" && sourceFile == "" && message == "" {
		return ""
	}

	h := md5.New()
	h.Write([]byte(service))
	h.Write([]byte{0}) // separator: "ab"+"c" and "a"+"bc" must not collide
	h.Write([]byte(errorClass))
	h.Write([]byte{0})

	if sourceFile != "" {
		h.Write([]byte(sourceFile))
	} else {
		h.Write([]byte(Normalize(message)))
	}

	return hex.EncodeToString(h.Sum(nil))[:16]
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

// Normalize strips dynamic values from an error message so that structurally
// identical messages produce the same fingerprint. Order matters: most specific
// patterns first.
func Normalize(msg string) string {
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
	return strings.TrimSpace(msg)
}
