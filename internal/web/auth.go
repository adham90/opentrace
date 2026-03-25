package web

import (
	"context"
	"net/http"

	"github.com/adham90/opentrace/internal/auth"
	"github.com/adham90/opentrace/internal/server"
	"github.com/adham90/opentrace/internal/store"
)

// UserFromContext returns the authenticated user from the request context, or nil.
func UserFromContext(ctx context.Context) *store.User {
	return server.UserFromContext(ctx)
}

// SessionFromContext returns the session from the request context, or nil.
func SessionFromContext(ctx context.Context) *store.Session {
	return server.SessionFromContext(ctx)
}

// Delegate token generation to the auth package.
var (
	generateSessionToken = auth.GenerateSessionToken
	generateMCPToken     = auth.GenerateMCPToken
	generateAPIKey       = auth.GenerateAPIKey
)

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
