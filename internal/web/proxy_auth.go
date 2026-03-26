package web

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
)

// ProxyAuth trusts X-Forwarded-User and X-Forwarded-User-Role headers
// from the platform reverse proxy. Only active when OPENTRACE_TRUST_PROXY_AUTH=true.
func (s *Server) ProxyAuth(next http.Handler) http.Handler {
	if os.Getenv("OPENTRACE_TRUST_PROXY_AUTH") != "true" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		email := r.Header.Get("X-Forwarded-User")
		if email == "" {
			next.ServeHTTP(w, r)
			return
		}

		role := r.Header.Get("X-Forwarded-User-Role")
		if role == "" {
			role = "member"
		}

		// Auto-create or fetch the user
		user, err := s.userStore.GetByEmail(r.Context(), email)
		if err != nil {
			// User doesn't exist, auto-create
			user, err = s.userStore.Create(r.Context(), store.CreateUserParams{
				Email:        email,
				DisplayName:  email,
				Role:         store.UserRole(role),
				PasswordHash: "-", // no password needed with proxy auth
			})
			if err != nil {
				slog.Error("proxy auth: failed to create user", "email", email, "error", err)
				next.ServeHTTP(w, r)
				return
			}
			slog.Info("proxy auth: auto-created user", "email", email, "role", role)
		}

		ctx := server.WithUser(r.Context(), user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
