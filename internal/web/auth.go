package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/adham90/opentrace/internal/server"
	"github.com/adham90/opentrace/internal/store"
)

// UserFromContext returns the authenticated user from the request context, or nil.
// Delegates to server.UserFromContext so modules and web share the same context key.
func UserFromContext(ctx context.Context) *store.User {
	return server.UserFromContext(ctx)
}

// SessionFromContext returns the session from the request context, or nil.
func SessionFromContext(ctx context.Context) *store.Session {
	return server.SessionFromContext(ctx)
}

func generateSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func generateMCPToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "mcp_" + hex.EncodeToString(b), nil
}

func generateAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "ot_" + hex.EncodeToString(b), nil
}

const sessionCookieName = "opentrace_session"

func setSessionCookie(w http.ResponseWriter, token string, maxAge int, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	setSessionCookie(w, "", -1, true)
}
