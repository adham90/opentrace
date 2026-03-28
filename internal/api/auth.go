package api

import (
	"context"

	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
)

// UserFromContext returns the authenticated user from the request context, or nil.
func UserFromContext(ctx context.Context) *store.User {
	return server.UserFromContext(ctx)
}

// SessionFromContext returns the session from the request context, or nil.
func SessionFromContext(ctx context.Context) *store.Session {
	return server.SessionFromContext(ctx)
}
