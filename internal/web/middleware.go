package web

import (
	"context"
	"net/http"

	"github.com/adham90/opentrace/internal/store"
)

// APIKeyAuth returns middleware that validates a Bearer token against the given
// API key. If apiKey is empty, all requests are allowed (auth disabled).
func APIKeyAuth(apiKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if apiKey == "" {
				next.ServeHTTP(w, r)
				return
			}
			header := r.Header.Get("Authorization")
			if header != "Bearer "+apiKey {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SessionAuth loads the user from the session cookie into the request context.
// It never rejects — if no valid session is found, the request continues without a user.
func (s *Server) SessionAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.sessionStore == nil || s.userStore == nil {
			next.ServeHTTP(w, r)
			return
		}

		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || cookie.Value == "" {
			next.ServeHTTP(w, r)
			return
		}

		sess, err := s.sessionStore.GetByToken(r.Context(), cookie.Value)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}

		user, err := s.userStore.GetByID(r.Context(), sess.UserID)
		if err != nil || !user.IsActive {
			next.ServeHTTP(w, r)
			return
		}

		ctx := context.WithValue(r.Context(), ctxKeySession, sess)
		ctx = context.WithValue(ctx, ctxKeyUser, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAuth redirects to /login if no user is in the context.
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if UserFromContext(r.Context()) == nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAdmin returns 403 if the user is not an admin.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		if user == nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		if user.Role != store.RoleAdmin {
			writeError(w, http.StatusForbidden, "admin access required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAdminAPI returns 403 JSON if the user is not an admin (for API endpoints).
func RequireAdminAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		if user == nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if user.Role != store.RoleAdmin {
			writeError(w, http.StatusForbidden, "admin access required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAuthAPI returns 401 JSON if no user is in the context (for API endpoints).
func RequireAuthAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if UserFromContext(r.Context()) == nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// (s *Server) RequireAuthIfEnabled skips auth enforcement when zero users exist
// (backward compat for existing installs that haven't set up auth yet).
func (s *Server) RequireAuthIfEnabled(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.userStore == nil {
			next.ServeHTTP(w, r)
			return
		}
		count, err := s.userStore.Count(r.Context())
		if err != nil || count == 0 {
			next.ServeHTTP(w, r)
			return
		}
		// Users exist — enforce auth
		RequireAuth(next).ServeHTTP(w, r)
	})
}

// requireAuthIfEnabledAPI is the API variant — returns 401 JSON instead of redirect.
func (s *Server) requireAuthIfEnabledAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.userStore == nil {
			next.ServeHTTP(w, r)
			return
		}
		count, err := s.userStore.Count(r.Context())
		if err != nil || count == 0 {
			next.ServeHTTP(w, r)
			return
		}
		RequireAuthAPI(next).ServeHTTP(w, r)
	})
}

// requireAdminIfEnabled combines auth-if-enabled with admin check for API routes.
func (s *Server) requireAdminIfEnabled(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.userStore == nil {
			next.ServeHTTP(w, r)
			return
		}
		count, err := s.userStore.Count(r.Context())
		if err != nil || count == 0 {
			next.ServeHTTP(w, r)
			return
		}
		RequireAdminAPI(next).ServeHTTP(w, r)
	})
}
