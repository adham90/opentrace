// Package auth provides authentication utilities: token generation,
// rate limiting, and session helpers.
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

// GenerateSessionToken creates a cryptographically random 64-character hex token.
func GenerateSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// GenerateMCPToken creates a prefixed MCP token.
func GenerateMCPToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "mcp_" + hex.EncodeToString(b), nil
}

// GenerateAPIKey creates a prefixed API key.
func GenerateAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "ot_" + hex.EncodeToString(b), nil
}

// SessionCookieName is the name of the session cookie.
const SessionCookieName = "opentrace_session"

// SetSessionCookie sets the session cookie on the response.
func SetSessionCookie(w http.ResponseWriter, token string, maxAge int, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// ClearSessionCookie clears the session cookie on the response.
func ClearSessionCookie(w http.ResponseWriter) {
	SetSessionCookie(w, "", -1, true)
}
