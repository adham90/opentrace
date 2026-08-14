package mcp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// Mock: CodeEntityStore
// ---------------------------------------------------------------------------

type mockCodeEntityStore struct {
	incrementErrorCalls []struct {
		typ     store.CodeEntityType
		name    string
		service string
	}
	incrementInvestCalls []struct {
		typ     store.CodeEntityType
		name    string
		service string
	}
}

func (m *mockCodeEntityStore) IncrementError(_ context.Context, typ store.CodeEntityType, name, service string) error {
	m.incrementErrorCalls = append(m.incrementErrorCalls, struct {
		typ     store.CodeEntityType
		name    string
		service string
	}{typ, name, service})
	return nil
}

func (m *mockCodeEntityStore) IncrementInvestigation(_ context.Context, typ store.CodeEntityType, name, service string) error {
	m.incrementInvestCalls = append(m.incrementInvestCalls, struct {
		typ     store.CodeEntityType
		name    string
		service string
	}{typ, name, service})
	return nil
}

func (m *mockCodeEntityStore) Upsert(context.Context, store.UpsertCodeEntityParams) (*store.CodeEntity, error) {
	return nil, nil
}
func (m *mockCodeEntityStore) GetByName(context.Context, store.CodeEntityType, string, string) (*store.CodeEntity, error) {
	return nil, nil
}
func (m *mockCodeEntityStore) TopByRisk(context.Context, string, int) ([]store.CodeEntity, error) {
	return nil, nil
}
func (m *mockCodeEntityStore) BatchGetRisk(context.Context, store.CodeEntityType, []string, string) ([]store.CodeEntity, error) {
	return nil, nil
}
func (m *mockCodeEntityStore) BatchRecomputeRisk(context.Context) error { return nil }
func (m *mockCodeEntityStore) Prune(context.Context, time.Duration) (int64, error) {
	return 0, nil
}

// Compile-time check.
var _ store.CodeEntityStore = (*mockCodeEntityStore)(nil)

// ---------------------------------------------------------------------------
// Mock: ErrorGroupStore
// ---------------------------------------------------------------------------

type mockErrorGroupStore struct {
	groups map[string]*store.ErrorGroup
}

func (m *mockErrorGroupStore) Get(_ context.Context, fingerprint, _ string) (*store.ErrorGroup, error) {
	eg, ok := m.groups[fingerprint]
	if !ok {
		return nil, errors.New("not found")
	}
	return eg, nil
}

func (m *mockErrorGroupStore) Upsert(context.Context, store.LogEntry) error { return nil }
func (m *mockErrorGroupStore) List(context.Context, store.ListErrorGroupParams) ([]store.ErrorGroup, error) {
	return nil, nil
}
func (m *mockErrorGroupStore) Count(context.Context, store.ErrorGroupStatus, string) (int, error) {
	return 0, nil
}
func (m *mockErrorGroupStore) Resolve(context.Context, string, string, string) error { return nil }
func (m *mockErrorGroupStore) Ignore(context.Context, string, string, string) error  { return nil }
func (m *mockErrorGroupStore) Reopen(context.Context, string, string, string) error  { return nil }
func (m *mockErrorGroupStore) ListEvents(context.Context, string, string, int) ([]store.ErrorGroupEvent, error) {
	return nil, nil
}
func (m *mockErrorGroupStore) Prune(context.Context, time.Duration) (int64, error) {
	return 0, nil
}

// Compile-time check.
var _ store.ErrorGroupStore = (*mockErrorGroupStore)(nil)

// ---------------------------------------------------------------------------
// Tests: ExtractFilePaths
// ---------------------------------------------------------------------------

func TestExtractFilePaths(t *testing.T) {
	tests := []struct {
		name      string
		backtrace string
		want      []string
	}{
		{
			name:      "ruby backtrace",
			backtrace: "app/models/user.rb:42",
			want:      []string{"app/models/user.rb"},
		},
		{
			name:      "go backtrace multiline",
			backtrace: "main.go:10\nfoo/bar.go:20",
			want:      []string{"main.go", "foo/bar.go"},
		},
		{
			name:      "js backtrace with parens",
			backtrace: "(src/app.tsx:15:3)",
			// The Ruby regex also matches inside the parens, capturing "(src/app.tsx".
			// The JS regex then captures "src/app.tsx". Both are distinct strings.
			want: []string{"(src/app.tsx", "src/app.tsx"},
		},
		{
			name:      "js backtrace without parens overlap",
			backtrace: "at src/app.js:15:3",
			// Only the Ruby regex matches here (no parens wrapping the path).
			want: []string{"src/app.js"},
		},
		{
			name:      "python backtrace",
			backtrace: `File "app/views.py", line 42`,
			want:      []string{"app/views.py"},
		},
		{
			name:      "vendor paths filtered",
			backtrace: "/vendor/foo/bar.go:10",
			want:      nil,
		},
		{
			name:      "node_modules filtered",
			backtrace: "/some/path/node_modules/pkg/index.js:1",
			want:      nil,
		},
		{
			name:      "gems filtered",
			backtrace: "/gems/activerecord/lib/record.rb:5",
			want:      nil,
		},
		{
			name:      "site-packages filtered",
			backtrace: `File "/site-packages/django/core.py", line 1`,
			want:      nil,
		},
		{
			name: "multiple languages mixed and deduplicated",
			backtrace: "app/models/user.rb:42\n" +
				"main.go:10\n" +
				"src/index.tsx:5\n" +
				`File "app/views.py", line 99` + "\n" +
				"app/models/user.rb:100",
			want: []string{"app/models/user.rb", "main.go", "src/index.tsx", "app/views.py"},
		},
		{
			name:      "empty string",
			backtrace: "",
			want:      nil,
		},
		{
			name:      "no source file matches",
			backtrace: "just some random text with no file references",
			want:      nil,
		},
		{
			name:      "unknown extensions filtered out",
			backtrace: "somefile.xyz:10\notherfile.dat:20",
			want:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractFilePaths(tt.backtrace)

			if len(got) != len(tt.want) {
				t.Fatalf("ExtractFilePaths(%q) returned %d paths, want %d\ngot:  %v\nwant: %v",
					tt.backtrace, len(got), len(tt.want), got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("path[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Tests: PopulateFromErrorLog
// ---------------------------------------------------------------------------

func TestPopulateFromErrorLog_NilStore(t *testing.T) {
	// Must not panic when CodeEntityStore is nil.
	PopulateFromErrorLog(context.Background(), nil, store.LogEntry{
		Message: "boom",
		Service: "api",
	})
}

func TestPopulateFromErrorLog_SourceFileOnly(t *testing.T) {
	ces := &mockCodeEntityStore{}
	entry := store.LogEntry{
		Service:    "api",
		SourceFile: "app/models/user.rb",
	}

	PopulateFromErrorLog(context.Background(), ces, entry)

	if len(ces.incrementErrorCalls) != 1 {
		t.Fatalf("expected 1 IncrementError call, got %d", len(ces.incrementErrorCalls))
	}
	call := ces.incrementErrorCalls[0]
	if call.typ != store.CodeEntityFile {
		t.Errorf("type = %q, want %q", call.typ, store.CodeEntityFile)
	}
	if call.name != "app/models/user.rb" {
		t.Errorf("name = %q, want %q", call.name, "app/models/user.rb")
	}
	if call.service != "api" {
		t.Errorf("service = %q, want %q", call.service, "api")
	}
}

func TestPopulateFromErrorLog_BacktraceInMetadata(t *testing.T) {
	ces := &mockCodeEntityStore{}
	entry := store.LogEntry{
		Service: "web",
		Metadata: map[string]any{
			"backtrace": "app/controllers/home.rb:5\napp/views/index.rb:10",
		},
	}

	PopulateFromErrorLog(context.Background(), ces, entry)

	if len(ces.incrementErrorCalls) != 2 {
		t.Fatalf("expected 2 IncrementError calls, got %d", len(ces.incrementErrorCalls))
	}

	names := make(map[string]bool)
	for _, c := range ces.incrementErrorCalls {
		names[c.name] = true
		if c.service != "web" {
			t.Errorf("service = %q, want %q", c.service, "web")
		}
	}
	if !names["app/controllers/home.rb"] {
		t.Error("missing IncrementError call for app/controllers/home.rb")
	}
	if !names["app/views/index.rb"] {
		t.Error("missing IncrementError call for app/views/index.rb")
	}
}

func TestPopulateFromErrorLog_SourceFileNotDuplicatedFromBacktrace(t *testing.T) {
	ces := &mockCodeEntityStore{}
	entry := store.LogEntry{
		Service:    "api",
		SourceFile: "app/models/user.rb",
		Metadata: map[string]any{
			"backtrace": "app/models/user.rb:42\napp/controllers/users.rb:10",
		},
	}

	PopulateFromErrorLog(context.Background(), ces, entry)

	// SourceFile is counted once via the explicit path. The backtrace also
	// contains the same file but PopulateFromErrorLog should skip duplicates.
	// So we expect 2 calls: user.rb (from SourceFile) and users.rb (from backtrace).
	if len(ces.incrementErrorCalls) != 2 {
		t.Fatalf("expected 2 IncrementError calls, got %d: %+v",
			len(ces.incrementErrorCalls), ces.incrementErrorCalls)
	}

	names := make(map[string]int)
	for _, c := range ces.incrementErrorCalls {
		names[c.name]++
	}
	if names["app/models/user.rb"] != 1 {
		t.Errorf("user.rb called %d times, want 1", names["app/models/user.rb"])
	}
	if names["app/controllers/users.rb"] != 1 {
		t.Errorf("users.rb called %d times, want 1", names["app/controllers/users.rb"])
	}
}
