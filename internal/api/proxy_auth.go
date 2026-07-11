package api

import (
	"log/slog"
	"net"
	"net/http"
	"os"

	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
)

// peerTrustedProxy reports whether the direct peer (r.RemoteAddr) is a proxy we
// trust to assert identity. With an explicit OPENTRACE_TRUSTED_PROXIES list, only
// those IPs qualify. With no list, only loopback (a proxy co-located on the host)
// is trusted — never an arbitrary remote client.
func peerTrustedProxy(remoteAddr string, trusted map[string]bool) bool {
	ip, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		ip = remoteAddr
	}
	if len(trusted) > 0 {
		return trusted[ip]
	}
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.IsLoopback()
}

// ProxyAuth trusts X-Forwarded-User and X-Forwarded-User-Role headers from the
// platform reverse proxy. Only active when OPENTRACE_TRUST_PROXY_AUTH=true, and
// only when the direct peer is a trusted proxy — otherwise any client could send
// X-Forwarded-User-Role: admin and mint an admin. Untrusted peers fall through
// to normal auth (headers ignored), not a hard failure.
func (s *Server) ProxyAuth(next http.Handler) http.Handler {
	if os.Getenv("OPENTRACE_TRUST_PROXY_AUTH") != "true" {
		return next
	}
	trusted := make(map[string]bool)
	if s.cfg != nil {
		for _, p := range s.cfg.TrustedProxies {
			trusted[p] = true
		}
	}
	if len(trusted) == 0 {
		slog.Warn("OPENTRACE_TRUST_PROXY_AUTH=true but OPENTRACE_TRUSTED_PROXIES is empty; only loopback peers will be trusted to assert X-Forwarded-User")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !peerTrustedProxy(r.RemoteAddr, trusted) {
			next.ServeHTTP(w, r) // ignore identity headers from an untrusted peer
			return
		}
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
				server.WriteError(w, http.StatusInternalServerError, "failed to establish user identity")
				return
			}
			slog.Info("proxy auth: auto-created user", "email", email, "role", role)
		}

		ctx := server.WithUser(r.Context(), user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
