package api

import (
	"compress/gzip"
	"crypto/subtle"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

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
			slog.Warn("rate limit exceeded", "ip", ip, "path", r.URL.Path, "limit", rl.limit)
			w.Header().Set("Retry-After", fmt.Sprintf("%d", int(retryAfter)))
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		rl.mu.Unlock()
		next.ServeHTTP(w, r)
	})
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

