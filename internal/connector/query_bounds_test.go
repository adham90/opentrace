package connector

import (
	"context"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// Finding #3: MySQL only honours MAX_EXECUTION_TIME when the hint comment
// immediately follows the SELECT keyword; before SELECT it is a plain comment.
func TestWithMaxExecutionTime(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		timeout int
		want    string
	}{
		{
			name:    "hint follows select",
			query:   "SELECT * FROM users LIMIT 500",
			timeout: 5000,
			want:    "SELECT /*+ MAX_EXECUTION_TIME(5000) */ * FROM users LIMIT 500",
		},
		{
			name:    "lowercase select keeps original case",
			query:   "select id from users",
			timeout: 250,
			want:    "select /*+ MAX_EXECUTION_TIME(250) */ id from users",
		},
		{
			name:    "leading whitespace trimmed",
			query:   "\n  SELECT 1",
			timeout: 100,
			want:    "SELECT /*+ MAX_EXECUTION_TIME(100) */ 1",
		},
		{
			name:    "non select left alone",
			query:   "SHOW TABLES",
			timeout: 5000,
			want:    "SHOW TABLES",
		},
		{
			name:    "cte left alone",
			query:   "WITH x AS (SELECT 1) SELECT * FROM x",
			timeout: 5000,
			want:    "WITH x AS (SELECT 1) SELECT * FROM x",
		},
		{
			name:    "identifier starting with select left alone",
			query:   "selective_view",
			timeout: 5000,
			want:    "selective_view",
		},
		{
			name:    "no timeout configured",
			query:   "SELECT 1",
			timeout: 0,
			want:    "SELECT 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := withMaxExecutionTime(tt.query, tt.timeout)
			if got != tt.want {
				t.Errorf("withMaxExecutionTime(%q, %d) = %q, want %q", tt.query, tt.timeout, got, tt.want)
			}
			if tt.timeout > 0 && strings.HasPrefix(strings.TrimSpace(tt.query), "SELECT") {
				if strings.HasPrefix(got, "/*+") {
					t.Errorf("hint placed before SELECT (MySQL ignores it): %q", got)
				}
			}
		})
	}
}

// fakeHashScanner replays canned HSCAN pages and records how many round trips
// scanHashFields performed.
type fakeHashScanner struct {
	pages [][]string
	calls int
}

func (f *fakeHashScanner) HScan(ctx context.Context, key string, cursor uint64, match string, count int64) *redis.ScanCmd {
	f.calls++
	idx := int(cursor)
	cmd := redis.NewScanCmd(ctx, nil, "hscan", key)
	if idx >= len(f.pages) {
		cmd.SetVal(nil, 0)
		return cmd
	}
	next := uint64(idx + 1)
	if next >= uint64(len(f.pages)) {
		next = 0 // cursor 0 terminates the walk
	}
	cmd.SetVal(f.pages[idx], next)
	return cmd
}

// Finding #4: a large hash must not be pulled in full; the walk stops as soon
// as the display limit is reached.
func TestScanHashFields_StopsAtLimit(t *testing.T) {
	page := func(prefix string, n int) []string {
		out := make([]string, 0, n*2)
		for i := 0; i < n; i++ {
			out = append(out, prefix+string(rune('a'+i)), "v")
		}
		return out
	}
	f := &fakeHashScanner{pages: [][]string{page("p1", 10), page("p2", 10), page("p3", 10)}}

	got, err := scanHashFields(context.Background(), f, "big", 12)
	if err != nil {
		t.Fatalf("scanHashFields: %v", err)
	}
	if len(got) != 12 {
		t.Errorf("got %d fields, want the 12-field limit", len(got))
	}
	if f.calls != 2 {
		t.Errorf("made %d HSCAN calls, want 2 (walk must stop at the limit)", f.calls)
	}
}

func TestScanHashFields_SmallHash(t *testing.T) {
	f := &fakeHashScanner{pages: [][]string{{"a", "1", "b", "2"}}}
	got, err := scanHashFields(context.Background(), f, "small", redisMaxHashFields)
	if err != nil {
		t.Fatalf("scanHashFields: %v", err)
	}
	if len(got) != 2 || got["a"] != "1" || got["b"] != "2" {
		t.Errorf("scanHashFields returned %v, want {a:1 b:2}", got)
	}
	if f.calls != 1 {
		t.Errorf("made %d HSCAN calls, want 1", f.calls)
	}
}
