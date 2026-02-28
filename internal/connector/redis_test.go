package connector

import (
	"context"
	"testing"
)

func TestRedisConnector_Type(t *testing.T) {
	c := &RedisConnector{}
	if c.Type() != ConnectorRedis {
		t.Fatalf("Type() = %q, want %q", c.Type(), ConnectorRedis)
	}
}

func TestRedisConnector_Tools(t *testing.T) {
	c := &RedisConnector{}
	tools := c.Tools()
	if len(tools) != 4 {
		t.Fatalf("len(Tools()) = %d, want 4", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	expected := []string{"redis_info", "redis_keys", "redis_get", "redis_slowlog"}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}

func TestRedisConnector_ToolParams(t *testing.T) {
	c := &RedisConnector{}
	tools := c.Tools()

	for _, tool := range tools {
		switch tool.Name {
		case "redis_info":
			if len(tool.Params) != 1 {
				t.Errorf("redis_info: len(Params) = %d, want 1", len(tool.Params))
			}
			if tool.Params[0].Required {
				t.Error("redis_info section param should be optional")
			}
		case "redis_keys":
			if len(tool.Params) != 2 {
				t.Errorf("redis_keys: len(Params) = %d, want 2", len(tool.Params))
			}
			for _, p := range tool.Params {
				if p.Name == "pattern" && !p.Required {
					t.Error("redis_keys pattern should be required")
				}
				if p.Name == "count" && p.Required {
					t.Error("redis_keys count should be optional")
				}
			}
		case "redis_get":
			if len(tool.Params) != 1 {
				t.Errorf("redis_get: len(Params) = %d, want 1", len(tool.Params))
			}
			if !tool.Params[0].Required {
				t.Error("redis_get key should be required")
			}
		case "redis_slowlog":
			if len(tool.Params) != 1 {
				t.Errorf("redis_slowlog: len(Params) = %d, want 1", len(tool.Params))
			}
			if tool.Params[0].Required {
				t.Error("redis_slowlog count should be optional")
			}
		}
	}
}

func TestRedisKeys_RequiresPattern(t *testing.T) {
	c := &RedisConnector{
		cb: NewCircuitBreaker("test", DefaultCircuitBreakerConfig()),
	}
	tools := c.Tools()
	var keysTool Tool
	for _, tool := range tools {
		if tool.Name == "redis_keys" {
			keysTool = tool
			break
		}
	}

	// Missing pattern
	_, err := keysTool.Handler(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing pattern")
	}

	// Empty pattern
	_, err = keysTool.Handler(context.Background(), map[string]any{"pattern": ""})
	if err == nil {
		t.Fatal("expected error for empty pattern")
	}
}

func TestRedisGet_RequiresKey(t *testing.T) {
	c := &RedisConnector{
		cb: NewCircuitBreaker("test", DefaultCircuitBreakerConfig()),
	}
	tools := c.Tools()
	var getTool Tool
	for _, tool := range tools {
		if tool.Name == "redis_get" {
			getTool = tool
			break
		}
	}

	// Missing key
	_, err := getTool.Handler(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing key")
	}

	// Empty key
	_, err = getTool.Handler(context.Background(), map[string]any{"key": ""})
	if err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestRedisConnector_CircuitBreakerState(t *testing.T) {
	c := &RedisConnector{
		cb: NewCircuitBreaker("test", DefaultCircuitBreakerConfig()),
	}
	if state := c.CircuitBreakerState(); state != CircuitClosed {
		t.Fatalf("CircuitBreakerState() = %q, want %q", state, CircuitClosed)
	}
}

func TestRedisConnector_CircuitBreakerState_NilCB(t *testing.T) {
	c := &RedisConnector{}
	if state := c.CircuitBreakerState(); state != CircuitClosed {
		t.Fatalf("CircuitBreakerState() = %q, want %q", state, CircuitClosed)
	}
}

func TestRedisConnector_ResetCircuitBreaker(t *testing.T) {
	cb := NewCircuitBreaker("test", DefaultCircuitBreakerConfig())
	for i := 0; i < 10; i++ {
		cb.RecordFailure()
	}
	if cb.State() != CircuitOpen {
		t.Fatalf("expected open circuit after failures")
	}

	c := &RedisConnector{cb: cb}
	c.ResetCircuitBreaker()

	if c.CircuitBreakerState() != CircuitClosed {
		t.Fatalf("expected closed circuit after reset")
	}
}

func TestRedisConnector_ResetCircuitBreaker_NilCB(t *testing.T) {
	c := &RedisConnector{}
	// Should not panic
	c.ResetCircuitBreaker()
}

func TestRedisConnector_Implements_Interfaces(t *testing.T) {
	c := &RedisConnector{}
	var _ DataSource = c
	var _ HealthChecker = c
	var _ CircuitBreakerProvider = c
}

// --- Helper function tests ---

func TestTruncateValue(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"short string", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"truncated", "hello world", 5, "hello..."},
		{"empty", "", 5, ""},
		{"single char truncated", "ab", 1, "a..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateValue(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncateValue(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		name  string
		bytes int64
		want  string
	}{
		{"zero bytes", 0, "0 B"},
		{"small bytes", 100, "100 B"},
		{"one KB", 1024, "1.0 KB"},
		{"1.5 KB", 1536, "1.5 KB"},
		{"one MB", 1024 * 1024, "1.0 MB"},
		{"one GB", 1024 * 1024 * 1024, "1.0 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatBytes(tt.bytes)
			if got != tt.want {
				t.Errorf("formatBytes(%d) = %q, want %q", tt.bytes, got, tt.want)
			}
		})
	}
}

// --- Circuit breaker integration with handler ---

func TestRedisInfo_CircuitBreakerBlocks(t *testing.T) {
	cb := NewCircuitBreaker("test", DefaultCircuitBreakerConfig())
	// Force circuit open
	for i := 0; i < 10; i++ {
		cb.RecordFailure()
	}

	c := &RedisConnector{cb: cb}
	tools := c.Tools()
	var infoTool Tool
	for _, tool := range tools {
		if tool.Name == "redis_info" {
			infoTool = tool
			break
		}
	}

	_, err := infoTool.Handler(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected circuit breaker error")
	}
	if !searchStr(err.Error(), "circuit breaker") {
		t.Errorf("expected circuit breaker error, got: %v", err)
	}
}

func TestRedisKeys_CircuitBreakerBlocks(t *testing.T) {
	cb := NewCircuitBreaker("test", DefaultCircuitBreakerConfig())
	for i := 0; i < 10; i++ {
		cb.RecordFailure()
	}

	c := &RedisConnector{cb: cb}
	tools := c.Tools()
	var keysTool Tool
	for _, tool := range tools {
		if tool.Name == "redis_keys" {
			keysTool = tool
			break
		}
	}

	_, err := keysTool.Handler(context.Background(), map[string]any{"pattern": "*"})
	if err == nil {
		t.Fatal("expected circuit breaker error")
	}
}

func TestRedisGet_CircuitBreakerBlocks(t *testing.T) {
	cb := NewCircuitBreaker("test", DefaultCircuitBreakerConfig())
	for i := 0; i < 10; i++ {
		cb.RecordFailure()
	}

	c := &RedisConnector{cb: cb}
	tools := c.Tools()
	var getTool Tool
	for _, tool := range tools {
		if tool.Name == "redis_get" {
			getTool = tool
			break
		}
	}

	_, err := getTool.Handler(context.Background(), map[string]any{"key": "test"})
	if err == nil {
		t.Fatal("expected circuit breaker error")
	}
}

func TestRedisSlowlog_CircuitBreakerBlocks(t *testing.T) {
	cb := NewCircuitBreaker("test", DefaultCircuitBreakerConfig())
	for i := 0; i < 10; i++ {
		cb.RecordFailure()
	}

	c := &RedisConnector{cb: cb}
	tools := c.Tools()
	var slowlogTool Tool
	for _, tool := range tools {
		if tool.Name == "redis_slowlog" {
			slowlogTool = tool
			break
		}
	}

	_, err := slowlogTool.Handler(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected circuit breaker error")
	}
}
