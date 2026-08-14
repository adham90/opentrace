package connector

import (
	"regexp"
	"strings"
)

// redactedSecret replaces every credential removed by RedactSecrets.
const redactedSecret = "***"

// Patterns for the credential shapes that reach connector logs and errors:
// URI userinfo (postgres://user:pw@host, redis://:pw@host), go-sql-driver DSNs
// (user:pw@tcp(host:port)/db), key=value connection strings (password=pw), and
// token query parameters (?authToken=...).
var (
	uriUserinfoRE = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)([^/\s:@]*):([^/\s@]*)@`)
	dsnUserinfoRE = regexp.MustCompile(`([^\s:/@]+):([^\s:/@]*)@(tcp|unix|udp)\(`)
	kvSecretRE    = regexp.MustCompile(`(?i)\b(password|passwd|pwd|auth_?token|api_?key|secret)\s*=\s*("[^"]*"|'[^']*'|[^\s&;]*)`)
	querySecretRE = regexp.MustCompile(`(?i)([?&](?:auth_?token|token|api_?key|password|secret)=)[^&\s]*`)
)

// RedactSecrets removes credentials from a connection string (or from any text
// that may embed one, such as a driver error message) so it is safe to place in
// a log line, a circuit-breaker name, or a user-facing error.
func RedactSecrets(s string) string {
	if s == "" {
		return s
	}
	s = uriUserinfoRE.ReplaceAllString(s, "${1}${2}:"+redactedSecret+"@")
	s = dsnUserinfoRE.ReplaceAllString(s, "${1}:"+redactedSecret+"@${3}(")
	s = kvSecretRE.ReplaceAllString(s, "${1}="+redactedSecret)
	s = querySecretRE.ReplaceAllString(s, "${1}"+redactedSecret)
	return s
}

// ConnectorLabel derives a short, credential-free identifier for a connection
// string, suitable as a circuit-breaker name and for structured logs. It keeps
// the scheme, host and database (the parts an operator needs to tell two
// connectors apart) and drops everything else.
func ConnectorLabel(connStr string) string {
	s := RedactSecrets(strings.TrimSpace(connStr))

	// Drop any query string: it can carry tokens not covered by the patterns.
	if i := strings.IndexAny(s, "?"); i >= 0 {
		s = s[:i]
	}

	scheme := ""
	if i := strings.Index(s, "://"); i >= 0 {
		scheme = s[:i+3]
		s = s[i+3:]
	}
	// Drop userinfo entirely — the username is not a secret, but it is not
	// needed to identify the connector either.
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	if s == "" {
		return "connector"
	}
	return scheme + s
}
