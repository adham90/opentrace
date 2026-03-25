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

// Delegate cookie helpers to the auth package.
const sessionCookieName = auth.SessionCookieName

func setSessionCookie(w http.ResponseWriter, token string, maxAge int, secure bool) {
	auth.SetSessionCookie(w, token, maxAge, secure)
}

func clearSessionCookie(w http.ResponseWriter) {
	auth.ClearSessionCookie(w)
}
