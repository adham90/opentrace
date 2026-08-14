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

// evictRecord remembers the insertion order of a rate-limit key so the map can
// be bounded in O(1) instead of scanning for the oldest entry on every evict.
// The entry pointer disambiguates a key that was deleted and re-created.
type evictRecord struct {
	key   string
	entry *rateLimitEntry
}

// RateLimiter provides per-IP rate limiting.
type RateLimiter struct {
	mu             sync.Mutex
	entries        map[string]*rateLimitEntry
	order          []evictRecord // FIFO of keys in creation order
	orderHead      int           // index of the oldest live record in order
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

const (
	// maxRateLimitEntries bounds the per-key map so a flood of distinct keys
	// cannot grow it without limit.
	maxRateLimitEntries = 10000
	// rateLimitCleanupInterval is how often expired entries are swept.
	rateLimitCleanupInterval = 1 * time.Minute
	// orderCompactThreshold is the smallest order slice worth compacting. Below
	// it the slice is negligible, so compaction would only burn CPU.
	orderCompactThreshold = 1024
	// orderSlackFactor bounds how much dead weight the FIFO may carry relative
	// to the number of live entries before it is compacted. Without this the
	// common case leaks: every window rollover appends a record, and with fewer
	// than maxRateLimitEntries distinct clients the eviction loop — the only
	// other consumer of the FIFO — never runs, so nothing is ever reclaimed.
	orderSlackFactor = 2
)

// cleanup periodically evicts expired entries to prevent unbounded map growth.
func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(rateLimitCleanupInterval)
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
			rl.compactOrderLocked()
			rl.mu.Unlock()
		}
	}
}

// addEntryLocked inserts a new entry, evicting the oldest one first if the map
// is at capacity. Eviction is O(1) amortized: the FIFO records creation order,
// and every entry shares the same window so creation order is expiry order.
// Callers must hold rl.mu.
func (rl *RateLimiter) addEntryLocked(key string, entry *rateLimitEntry) {
	for len(rl.entries) >= maxRateLimitEntries && rl.orderHead < len(rl.order) {
		rec := rl.order[rl.orderHead]
		rl.orderHead++
		// Skip records whose entry was already deleted or replaced.
		if cur, ok := rl.entries[rec.key]; ok && cur == rec.entry {
			delete(rl.entries, rec.key)
		}
	}
	rl.entries[key] = entry
	rl.order = append(rl.order, evictRecord{key: key, entry: entry})
	rl.compactOrderLocked()
}

// orderNeedsCompactionLocked reports whether the FIFO carries enough dead
// weight to be worth rebuilding. Two independent triggers, because the two ways
// records go stale are independent: consumed slots accumulate under a flood of
// distinct keys, while superseded records accumulate in the ordinary case where
// a handful of clients roll their window over and over. Callers must hold rl.mu.
func (rl *RateLimiter) orderNeedsCompactionLocked() bool {
	if rl.orderHead >= orderCompactThreshold {
		return true
	}
	return len(rl.order) >= orderCompactThreshold &&
		len(rl.order) > orderSlackFactor*len(rl.entries)
}

// compactOrderLocked drops consumed FIFO slots and records for entries that no
// longer exist. Compaction is O(len(order)) but only runs once the slice has at
// least doubled past the live set, so eviction stays O(1) amortized.
// Callers must hold rl.mu.
func (rl *RateLimiter) compactOrderLocked() {
	if !rl.orderNeedsCompactionLocked() {
		return
	}
	live := make([]evictRecord, 0, len(rl.entries))
	for _, rec := range rl.order[rl.orderHead:] {
		if cur, ok := rl.entries[rec.key]; ok && cur == rec.entry {
			live = append(live, rec)
		}
	}
	rl.order = live
	rl.orderHead = 0
}

// clientIP returns the address the rate limit is keyed on. It must not be
// attacker-controlled: the leftmost X-Forwarded-For element is written by the
// client (proxies append, they never replace), so keying on it lets a caller
// mint a fresh bucket per request. Instead we walk X-Forwarded-For from the
// right — the part the trusted proxy chain actually wrote — and use the first
// address that is not itself a trusted proxy. Anything unparseable, or a
// direct peer that is not a trusted proxy, falls back to the peer address.
func (rl *RateLimiter) clientIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	if len(rl.trustedProxies) == 0 || !rl.trustedProxies[ip] {
		return ip
	}
	fwd := r.Header.Get("X-Forwarded-For")
	if fwd == "" {
		return ip
	}
	parts := strings.Split(fwd, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		hop := strings.TrimSpace(parts[i])
		parsed := net.ParseIP(hop)
		if parsed == nil {
			// A forged/garbage hop: everything to its left is untrustworthy.
			break
		}
		if rl.trustedProxies[parsed.String()] || rl.trustedProxies[hop] {
			continue // our own proxy chain; keep walking left
		}
		return parsed.String()
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
			rl.addEntryLocked(ip, &rateLimitEntry{count: 1, resetAt: now.Add(rl.window), lastSeen: now})
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
//
// If the key cannot be resolved (settings-store read error) the request is
// rejected with 503: "we could not read the key" must never be mistaken for
// "auth is disabled", which would let an unauthenticated client through for
// the duration of a transient DB fault.
func (s *Server) DynamicAPIKeyAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey, err := s.getEffectiveAPIKey(r.Context())
		if err != nil {
			slog.Error("api key auth: resolving configured key failed", "error", err)
			writeError(w, http.StatusServiceUnavailable, "authentication temporarily unavailable")
			return
		}
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
