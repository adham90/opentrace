package web

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

// ctxKeyCSPNonce is the context key for the per-request CSP nonce.
type cspNonceKeyType struct{}

var ctxKeyCSPNonce = cspNonceKeyType{}

// CSPNonce returns the CSP nonce stored in the request context.
func CSPNonce(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyCSPNonce).(string); ok {
		return v
	}
	return ""
}

// SecurityHeaders adds standard security headers to all responses.
// It generates a per-request cryptographic nonce for inline scripts/styles.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Generate 16 random bytes, base64-encode for the nonce value.
		var buf [16]byte
		if _, err := rand.Read(buf[:]); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		nonce := base64.StdEncoding.EncodeToString(buf[:])

		// Store nonce in context for templates.
		ctx := context.WithValue(r.Context(), ctxKeyCSPNonce, nonce)
		r = r.WithContext(ctx)

		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy",
			fmt.Sprintf("default-src 'self'; script-src 'self' 'nonce-%s'; script-src-attr 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'", nonce))
		w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("X-Permitted-Cross-Domain-Policies", "none")
		next.ServeHTTP(w, r)
	})
}

// MaxBodySize limits the request body to the given number of bytes.
func MaxBodySize(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}

// DecompressRequest transparently decompresses gzip-encoded request bodies.
// It limits the decompressed output to maxDecompressedBytes to prevent zip bombs.
func DecompressRequest(maxDecompressedBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Content-Encoding") != "gzip" {
				next.ServeHTTP(w, r)
				return
			}

			gz, err := gzip.NewReader(r.Body)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid gzip body")
				return
			}

			r.Body = http.MaxBytesReader(w, io.NopCloser(gz), maxDecompressedBytes)
			r.Header.Del("Content-Encoding")
			r.ContentLength = -1
			next.ServeHTTP(w, r)
		})
	}
}

// rateLimitEntry tracks request counts for a single IP.
type rateLimitEntry struct {
	count    int
	resetAt  time.Time
	lastSeen time.Time
}

// RateLimiter provides per-IP rate limiting.
type RateLimiter struct {
	mu             sync.Mutex
	entries        map[string]*rateLimitEntry
	limit          int
	window         time.Duration
	done           chan struct{}
	trustedProxies map[string]bool
}

// NewRateLimiter creates a rate limiter allowing limit requests per window per IP.
// It starts a background goroutine that evicts expired entries every 5 minutes.
// Call Stop() to terminate the background goroutine.
// The trusted parameter lists proxy IPs whose X-Forwarded-For header should be trusted.
func NewRateLimiter(limit int, window time.Duration, trusted []string) *RateLimiter {
	tp := make(map[string]bool, len(trusted))
	for _, t := range trusted {
		tp[t] = true
	}
	rl := &RateLimiter{
		entries:        make(map[string]*rateLimitEntry),
		limit:          limit,
		window:         window,
		done:           make(chan struct{}),
		trustedProxies: tp,
	}
	go rl.cleanup()
	return rl
}

// Stop terminates the background cleanup goroutine.
func (rl *RateLimiter) Stop() {
	select {
	case <-rl.done:
	default:
		close(rl.done)
	}
}

const maxRateLimitEntries = 10000

// cleanup periodically evicts expired entries to prevent unbounded map growth.
// Also caps total entries to maxRateLimitEntries to mitigate random-IP DoS.
func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-rl.done:
			return
		case <-ticker.C:
			rl.mu.Lock()
			now := time.Now()
			for ip, entry := range rl.entries {
				if now.After(entry.resetAt) {
					delete(rl.entries, ip)
				}
			}
			// Emergency eviction if still too large — evict oldest entries first.
			for len(rl.entries) > maxRateLimitEntries {
				var oldestIP string
				var oldestTime time.Time
				for ip, entry := range rl.entries {
					if oldestIP == "" || entry.lastSeen.Before(oldestTime) {
						oldestIP = ip
						oldestTime = entry.lastSeen
					}
				}
				delete(rl.entries, oldestIP)
			}
			rl.mu.Unlock()
		}
	}
}

// clientIP extracts the client IP from the request. It only trusts
// X-Forwarded-For when the direct connection is from a configured trusted proxy.
func (rl *RateLimiter) clientIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	if len(rl.trustedProxies) > 0 && rl.trustedProxies[ip] {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			return strings.TrimSpace(strings.SplitN(fwd, ",", 2)[0])
		}
	}
	return ip
}

// Middleware returns an HTTP middleware that enforces the rate limit.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := rl.clientIP(r)

		rl.mu.Lock()
		now := time.Now()
		entry, ok := rl.entries[ip]
		if !ok || now.After(entry.resetAt) {
			rl.entries[ip] = &rateLimitEntry{count: 1, resetAt: now.Add(rl.window), lastSeen: now}
			rl.mu.Unlock()
			next.ServeHTTP(w, r)
			return
		}
		entry.count++
		entry.lastSeen = now
		if entry.count > rl.limit {
			retryAfter := entry.resetAt.Sub(now).Seconds()
			if retryAfter < 1 {
				retryAfter = 1
			}
			rl.mu.Unlock()
			w.Header().Set("Retry-After", fmt.Sprintf("%d", int(retryAfter)))
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		rl.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

// CORSMiddleware adds CORS headers for cross-origin browser requests (e.g. JS error tracking).
// If allowedOrigins is empty, CORS headers are not set (same-origin only).
// Use "*" to allow all origins (suitable for development).
func CORSMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	if len(allowedOrigins) == 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	allowAll := len(allowedOrigins) == 1 && allowedOrigins[0] == "*"
	originSet := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		originSet[o] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			if allowAll || originSet[origin] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Version, X-Batch-ID")
				w.Header().Set("Access-Control-Max-Age", "86400")
				w.Header().Set("Vary", "Origin")
			}

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// DynamicCORSMiddleware resolves CORS allowed origins per-request (env var or DB)
// and sets appropriate CORS headers. If no origins are configured, CORS headers
// are not set (same-origin only).
func (s *Server) DynamicCORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}

		origins := s.getEffectiveCORSOrigins(r.Context())
		if len(origins) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		allowAll := len(origins) == 1 && origins[0] == "*"
		if allowAll {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		} else {
			allowed := false
			for _, o := range origins {
				if o == origin {
					allowed = true
					break
				}
			}
			if !allowed {
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}

		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Version, X-Batch-ID")
		w.Header().Set("Access-Control-Max-Age", "86400")
		w.Header().Set("Vary", "Origin")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// StaticCacheHeaders wraps a handler to set long-lived cache headers for static assets.
func StaticCacheHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		next.ServeHTTP(w, r)
	})
}

// APIKeyAuth returns middleware that validates a Bearer token against the given
// API key. If apiKey is empty, all requests are allowed (auth disabled).
// Uses constant-time comparison to prevent timing side-channel attacks.
func APIKeyAuth(apiKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if apiKey == "" {
				next.ServeHTTP(w, r)
				return
			}
			header := r.Header.Get("Authorization")
			expected := "Bearer " + apiKey
			if subtle.ConstantTimeCompare([]byte(header), []byte(expected)) != 1 {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// DynamicAPIKeyAuth resolves the API key per-request (env var or DB) and
// validates the Bearer token. If no key is configured, all requests pass.
// Uses constant-time comparison to prevent timing side-channel attacks.
func (s *Server) DynamicAPIKeyAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := s.getEffectiveAPIKey(r.Context())
		if apiKey == "" {
			next.ServeHTTP(w, r)
			return
		}
		header := r.Header.Get("Authorization")
		expected := "Bearer " + apiKey
		if subtle.ConstantTimeCompare([]byte(header), []byte(expected)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// SessionAuth loads the user from the session cookie into the request context.
// It never rejects — if no valid session is found, the request continues without a user.
// Skips DB lookups for static assets and health checks.
func (s *Server) SessionAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip session lookups for paths that don't need authentication
		if strings.HasPrefix(r.URL.Path, "/static/") || r.URL.Path == "/healthz" || strings.HasPrefix(r.URL.Path, "/api/version") || strings.HasPrefix(r.URL.Path, "/mcp/") {
			next.ServeHTTP(w, r)
			return
		}

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

// RedirectToOnboardingIfNeeded redirects to /onboarding when 0 users exist,
// otherwise enforces auth (redirects to /login).
func (s *Server) RedirectToOnboardingIfNeeded(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.userStore == nil {
			next.ServeHTTP(w, r)
			return
		}
		count, err := s.userStore.Count(r.Context())
		if err != nil || count == 0 {
			http.Redirect(w, r, "/onboarding", http.StatusFound)
			return
		}
		RequireAuth(next).ServeHTTP(w, r)
	})
}

// requireAuthOrOnboardingAPI is the API variant — returns 503 when onboarding
// is needed, otherwise enforces auth with 401 JSON.
func (s *Server) requireAuthOrOnboardingAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.userStore == nil {
			next.ServeHTTP(w, r)
			return
		}
		count, err := s.userStore.Count(r.Context())
		if err != nil || count == 0 {
			writeError(w, http.StatusServiceUnavailable, "onboarding required")
			return
		}
		RequireAuthAPI(next).ServeHTTP(w, r)
	})
}

// requireAdminOrOnboarding is the admin API variant — returns 503 when
// onboarding is needed, otherwise enforces admin auth.
func (s *Server) requireAdminOrOnboarding(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.userStore == nil {
			next.ServeHTTP(w, r)
			return
		}
		count, err := s.userStore.Count(r.Context())
		if err != nil || count == 0 {
			writeError(w, http.StatusServiceUnavailable, "onboarding required")
			return
		}
		RequireAdminAPI(next).ServeHTTP(w, r)
	})
}
