package connector

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisConnector implements DataSource for inspecting a target Redis instance.
type RedisConnector struct {
	client *redis.Client
	cb     *CircuitBreaker
}

// NewRedisConnector creates a new RedisConnector.
// connStr should be a Redis URL: redis://[:password@]host:port[/db]
func NewRedisConnector(ctx context.Context, connStr string) (*RedisConnector, error) {
	opts, err := redis.ParseURL(connStr)
	if err != nil {
		return nil, fmt.Errorf("parsing Redis URL: %s", FriendlyError(err))
	}

	opts.DialTimeout = 5 * time.Second
	opts.ReadTimeout = 5 * time.Second
	opts.WriteTimeout = 5 * time.Second
	opts.PoolSize = 3

	client := redis.NewClient(opts)

	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("pinging Redis: %s", FriendlyError(err))
	}

	return &RedisConnector{
		client: client,
		cb:     NewCircuitBreaker(connStr, DefaultCircuitBreakerConfig()),
	}, nil
}

func (c *RedisConnector) Type() ConnectorType { return ConnectorRedis }

func (c *RedisConnector) TestConnection(ctx context.Context) error {
	if c.cb != nil {
		if err := c.cb.Allow(); err != nil {
			return fmt.Errorf("connector: %w", err)
		}
	}
	err := c.client.Ping(ctx).Err()
	if err != nil {
		if c.cb != nil {
			c.cb.RecordFailure()
		}
		return fmt.Errorf("%s", FriendlyError(err))
	}
	if c.cb != nil {
		c.cb.RecordSuccess()
	}
	return nil
}

func (c *RedisConnector) Tools() []Tool {
	return []Tool{
		{
			Name: "redis_info",
			Description: `Get Redis server information and statistics.

Sections: server, clients, memory, stats, replication, cpu, keyspace, all (default).

Tips:
- Use "memory" to check memory usage and fragmentation
- Use "clients" to see connected client count
- Use "keyspace" to see key count per database
- Use "stats" for hit/miss rates and command stats`,
			Params: []ToolParam{
				{Name: "section", Type: "string", Required: false},
			},
			Handler: c.handleRedisInfo,
		},
		{
			Name: "redis_keys",
			Description: `Scan Redis keys matching a pattern. Uses SCAN (non-blocking, safe for production).

Pattern examples:
- "user:*" — all user keys
- "*session*" — keys containing "session"
- "cache:api:*" — cache keys for API responses

Returns up to 100 keys by default. Each key shows its type and TTL.`,
			Params: []ToolParam{
				{Name: "pattern", Type: "string", Required: true},
				{Name: "count", Type: "int", Required: false},
			},
			Handler: c.handleRedisKeys,
		},
		{
			Name: "redis_get",
			Description: `Get the value of a Redis key. Auto-detects the key type and uses the
appropriate command (GET for strings, HGETALL for hashes, LRANGE for lists,
SMEMBERS for sets, ZRANGE for sorted sets).

Also shows the key's TTL and memory usage.`,
			Params: []ToolParam{
				{Name: "key", Type: "string", Required: true},
			},
			Handler: c.handleRedisGet,
		},
		{
			Name: "redis_slowlog",
			Description: `Get the Redis slow query log. Shows queries that exceeded the
slowlog-log-slower-than threshold (default 10ms).

Each entry shows: timestamp, duration, command, and client info.`,
			Params: []ToolParam{
				{Name: "count", Type: "int", Required: false},
			},
			Handler: c.handleRedisSlowlog,
		},
	}
}

// CircuitBreakerState returns the current circuit breaker state.
func (c *RedisConnector) CircuitBreakerState() CircuitState {
	if c.cb != nil {
		return c.cb.State()
	}
	return CircuitClosed
}

// ResetCircuitBreaker resets the circuit breaker to closed state.
func (c *RedisConnector) ResetCircuitBreaker() {
	if c.cb != nil {
		c.cb.Reset()
	}
}

func (c *RedisConnector) Close() error {
	return c.client.Close()
}

// Ping checks connectivity and measures latency.
func (c *RedisConnector) Ping(ctx context.Context) PingResult {
	if c.cb != nil {
		if err := c.cb.Allow(); err != nil {
			return PingResult{Reachable: false, Error: err.Error()}
		}
	}

	start := time.Now()
	err := c.client.Ping(ctx).Err()
	latency := time.Since(start).Milliseconds()

	if err != nil {
		if c.cb != nil {
			c.cb.RecordFailure()
		}
		return PingResult{Reachable: false, LatencyMS: latency, Error: err.Error()}
	}
	if c.cb != nil {
		c.cb.RecordSuccess()
	}
	return PingResult{Reachable: true, LatencyMS: latency}
}

func (c *RedisConnector) handleRedisInfo(ctx context.Context, args map[string]any) (string, error) {
	if c.cb != nil {
		if err := c.cb.Allow(); err != nil {
			return "", fmt.Errorf("connector: %w", err)
		}
	}

	section, _ := args["section"].(string)
	if section == "" {
		section = "all"
	}

	var result string
	var err error
	if section == "all" {
		result, err = c.client.Info(ctx).Result()
	} else {
		result, err = c.client.Info(ctx, section).Result()
	}
	if err != nil {
		if c.cb != nil {
			c.cb.RecordFailure()
		}
		return "", fmt.Errorf("getting Redis info: %w", err)
	}

	if c.cb != nil {
		c.cb.RecordSuccess()
	}
	return result, nil
}

func (c *RedisConnector) handleRedisKeys(ctx context.Context, args map[string]any) (string, error) {
	if c.cb != nil {
		if err := c.cb.Allow(); err != nil {
			return "", fmt.Errorf("connector: %w", err)
		}
	}

	pattern, ok := args["pattern"].(string)
	if !ok || pattern == "" {
		return "", fmt.Errorf("pattern parameter is required")
	}

	maxKeys := 100
	if countF, ok := args["count"].(float64); ok && countF > 0 {
		maxKeys = int(countF)
		if maxKeys > 1000 {
			maxKeys = 1000
		}
	}

	var keys []string
	var cursor uint64
	for {
		var batch []string
		var err error
		batch, cursor, err = c.client.Scan(ctx, cursor, pattern, int64(maxKeys)).Result()
		if err != nil {
			if c.cb != nil {
				c.cb.RecordFailure()
			}
			return "", fmt.Errorf("scanning keys: %w", err)
		}
		keys = append(keys, batch...)
		if cursor == 0 || len(keys) >= maxKeys {
			break
		}
	}

	if len(keys) > maxKeys {
		keys = keys[:maxKeys]
	}

	if len(keys) == 0 {
		if c.cb != nil {
			c.cb.RecordSuccess()
		}
		return fmt.Sprintf("No keys matching pattern %q", pattern), nil
	}

	sort.Strings(keys)

	// Get type and TTL for each key using pipeline
	pipe := c.client.Pipeline()
	typeCmds := make([]*redis.StatusCmd, len(keys))
	ttlCmds := make([]*redis.DurationCmd, len(keys))
	for i, key := range keys {
		typeCmds[i] = pipe.Type(ctx, key)
		ttlCmds[i] = pipe.TTL(ctx, key)
	}
	_, err := pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		if c.cb != nil {
			c.cb.RecordFailure()
		}
		return "", fmt.Errorf("getting key info: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Keys matching %q (%d found):\n\n", pattern, len(keys)))
	for i, key := range keys {
		keyType := typeCmds[i].Val()
		ttl := ttlCmds[i].Val()

		sb.WriteString(fmt.Sprintf("  %s  [%s]", key, keyType))
		if ttl > 0 {
			sb.WriteString(fmt.Sprintf("  TTL: %s", ttl.Truncate(time.Second)))
		} else if ttl == -1 {
			sb.WriteString("  TTL: none (persistent)")
		}
		sb.WriteString("\n")
	}

	if c.cb != nil {
		c.cb.RecordSuccess()
	}
	return sb.String(), nil
}

func (c *RedisConnector) handleRedisGet(ctx context.Context, args map[string]any) (string, error) {
	if c.cb != nil {
		if err := c.cb.Allow(); err != nil {
			return "", fmt.Errorf("connector: %w", err)
		}
	}

	key, ok := args["key"].(string)
	if !ok || key == "" {
		return "", fmt.Errorf("key parameter is required")
	}

	// Check if key exists
	exists, err := c.client.Exists(ctx, key).Result()
	if err != nil {
		if c.cb != nil {
			c.cb.RecordFailure()
		}
		return "", fmt.Errorf("checking key existence: %w", err)
	}
	if exists == 0 {
		if c.cb != nil {
			c.cb.RecordSuccess()
		}
		return fmt.Sprintf("Key %q does not exist.", key), nil
	}

	// Get type
	keyType, err := c.client.Type(ctx, key).Result()
	if err != nil {
		if c.cb != nil {
			c.cb.RecordFailure()
		}
		return "", fmt.Errorf("getting key type: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Key: %s\nType: %s\n", key, keyType))

	// Get TTL
	ttl, _ := c.client.TTL(ctx, key).Result()
	if ttl > 0 {
		sb.WriteString(fmt.Sprintf("TTL: %s\n", ttl.Truncate(time.Second)))
	} else if ttl == -1 {
		sb.WriteString("TTL: none (persistent)\n")
	}

	// Get memory usage (Redis 4.0+, may fail on older versions)
	memUsage, err := c.client.MemoryUsage(ctx, key).Result()
	if err == nil {
		sb.WriteString(fmt.Sprintf("Memory: %s\n", formatBytes(memUsage)))
	}

	sb.WriteString("\nValue:\n")

	// Get value based on type
	switch keyType {
	case "string":
		val, err := c.client.Get(ctx, key).Result()
		if err != nil {
			if c.cb != nil {
				c.cb.RecordFailure()
			}
			return "", fmt.Errorf("getting string value: %w", err)
		}
		if len(val) > 4096 {
			sb.WriteString(val[:4096])
			sb.WriteString(fmt.Sprintf("\n... (truncated, total %d bytes)", len(val)))
		} else {
			sb.WriteString(val)
		}

	case "hash":
		vals, err := c.client.HGetAll(ctx, key).Result()
		if err != nil {
			if c.cb != nil {
				c.cb.RecordFailure()
			}
			return "", fmt.Errorf("getting hash value: %w", err)
		}
		for k, v := range vals {
			sb.WriteString(fmt.Sprintf("  %s: %s\n", k, truncateValue(v, 512)))
		}
		sb.WriteString(fmt.Sprintf("\n(%d fields)", len(vals)))

	case "list":
		length, _ := c.client.LLen(ctx, key).Result()
		// Show first 50 items
		showCount := int64(50)
		if length < showCount {
			showCount = length
		}
		vals, err := c.client.LRange(ctx, key, 0, showCount-1).Result()
		if err != nil {
			if c.cb != nil {
				c.cb.RecordFailure()
			}
			return "", fmt.Errorf("getting list value: %w", err)
		}
		for i, v := range vals {
			sb.WriteString(fmt.Sprintf("  [%d] %s\n", i, truncateValue(v, 512)))
		}
		sb.WriteString(fmt.Sprintf("\n(%d total items)", length))

	case "set":
		size, _ := c.client.SCard(ctx, key).Result()
		// Show up to 50 members
		vals, err := c.client.SRandMemberN(ctx, key, 50).Result()
		if err != nil {
			if c.cb != nil {
				c.cb.RecordFailure()
			}
			return "", fmt.Errorf("getting set value: %w", err)
		}
		for _, v := range vals {
			sb.WriteString(fmt.Sprintf("  %s\n", truncateValue(v, 512)))
		}
		sb.WriteString(fmt.Sprintf("\n(%d total members)", size))

	case "zset":
		size, _ := c.client.ZCard(ctx, key).Result()
		// Show top 50 by score
		showCount := int64(50)
		if size < showCount {
			showCount = size
		}
		vals, err := c.client.ZRangeWithScores(ctx, key, 0, showCount-1).Result()
		if err != nil {
			if c.cb != nil {
				c.cb.RecordFailure()
			}
			return "", fmt.Errorf("getting sorted set value: %w", err)
		}
		for _, z := range vals {
			sb.WriteString(fmt.Sprintf("  %.2f  %s\n", z.Score, truncateValue(fmt.Sprint(z.Member), 512)))
		}
		sb.WriteString(fmt.Sprintf("\n(%d total members)", size))

	case "stream":
		length, _ := c.client.XLen(ctx, key).Result()
		// Show last 10 entries
		vals, err := c.client.XRevRange(ctx, key, "+", "-").Result()
		if err != nil {
			if c.cb != nil {
				c.cb.RecordFailure()
			}
			return "", fmt.Errorf("getting stream value: %w", err)
		}
		showCount := 10
		if len(vals) < showCount {
			showCount = len(vals)
		}
		for i := 0; i < showCount; i++ {
			sb.WriteString(fmt.Sprintf("  %s:\n", vals[i].ID))
			for k, v := range vals[i].Values {
				sb.WriteString(fmt.Sprintf("    %s: %v\n", k, v))
			}
		}
		sb.WriteString(fmt.Sprintf("\n(%d total entries)", length))

	default:
		sb.WriteString(fmt.Sprintf("  (unsupported type: %s)", keyType))
	}

	if c.cb != nil {
		c.cb.RecordSuccess()
	}
	return sb.String(), nil
}

func (c *RedisConnector) handleRedisSlowlog(ctx context.Context, args map[string]any) (string, error) {
	if c.cb != nil {
		if err := c.cb.Allow(); err != nil {
			return "", fmt.Errorf("connector: %w", err)
		}
	}

	count := 10
	if countF, ok := args["count"].(float64); ok && countF > 0 {
		count = int(countF)
		if count > 128 {
			count = 128
		}
	}

	entries, err := c.client.SlowLogGet(ctx, int64(count)).Result()
	if err != nil {
		if c.cb != nil {
			c.cb.RecordFailure()
		}
		return "", fmt.Errorf("getting slow log: %w", err)
	}

	if len(entries) == 0 {
		if c.cb != nil {
			c.cb.RecordSuccess()
		}
		return "No slow log entries found.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Slow log (%d entries):\n\n", len(entries)))
	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("  [%s] %s\n", e.Time.Format(time.RFC3339), e.Duration))
		sb.WriteString(fmt.Sprintf("    Command: %s\n", strings.Join(e.Args, " ")))
		if e.ClientAddr != "" {
			sb.WriteString(fmt.Sprintf("    Client: %s\n", e.ClientAddr))
		}
		if e.ClientName != "" {
			sb.WriteString(fmt.Sprintf("    Name: %s\n", e.ClientName))
		}
		sb.WriteString("\n")
	}

	if c.cb != nil {
		c.cb.RecordSuccess()
	}
	return sb.String(), nil
}

// truncateValue truncates a string to maxLen and appends "..." if truncated.
func truncateValue(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// formatBytes formats a byte count into a human-readable string.
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
