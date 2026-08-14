package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/connector"
)

// controlOutcomeConnector is a database connector fake whose control statement
// either fails or reports "not cancelled", exercising both kill_query branches
// that render a pg function name / verb.
type controlOutcomeConnector struct {
	readRows   [][]any
	controlErr error
}

func (c *controlOutcomeConnector) Type() connector.ConnectorType          { return connector.ConnectorDatabase }
func (c *controlOutcomeConnector) TestConnection(_ context.Context) error { return nil }
func (c *controlOutcomeConnector) Tools() []connector.Tool                { return nil }
func (c *controlOutcomeConnector) Close() error                           { return nil }
func (c *controlOutcomeConnector) Environment() string                    { return "" }

func (c *controlOutcomeConnector) ExecuteReadQuery(ctx context.Context, q string) (*connector.QueryResult, error) {
	return c.ExecuteReadQueryArgs(ctx, q)
}

func (c *controlOutcomeConnector) ExecuteReadQueryArgs(_ context.Context, _ string, _ ...any) (*connector.QueryResult, error) {
	return &connector.QueryResult{Rows: c.readRows, RowCount: len(c.readRows)}, nil
}

func (c *controlOutcomeConnector) ExecuteControlQuery(_ context.Context, _ string) (*connector.QueryResult, error) {
	if c.controlErr != nil {
		return nil, c.controlErr
	}
	return &connector.QueryResult{Columns: []string{"ok"}, Rows: [][]any{{false}}, RowCount: 1}, nil
}

var killQueryReadRows = [][]any{{123, "active", "app", "SELECT 1", 10}}

// --- explain: analyze is refused up front, never sent to the database ---

func TestHandleExplain_AnalyzeRejected(t *testing.T) {
	rec := newRecordingConnector()
	deps := DatabaseDeps{Registry: registryWith(rec)}

	result, err := HandleExplain(context.Background(), deps, map[string]any{
		"query":   "SELECT 1",
		"analyze": true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected analyze=true to be rejected")
	}
	txt := extractText(t, result)
	if !strings.Contains(txt, "analyze=true is not supported") {
		t.Errorf("expected an explicit, actionable message, got: %s", txt)
	}
	for _, q := range rec.queries {
		if strings.Contains(strings.ToUpper(q), "ANALYZE") {
			t.Fatalf("an EXPLAIN ANALYZE statement was still sent: %s", q)
		}
	}
}

func TestHandleExplain_PlainExplainStillWorks(t *testing.T) {
	rec := newRecordingConnector()
	deps := DatabaseDeps{Registry: registryWith(rec)}

	result, err := HandleExplain(context.Background(), deps, map[string]any{
		"query": "SELECT 1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("plain explain should succeed: %s", extractText(t, result))
	}
	if len(rec.queries) != 1 || !strings.HasPrefix(rec.queries[0], "EXPLAIN SELECT 1") {
		t.Fatalf("unexpected statement: %v", rec.queries)
	}
}

func TestHandleExplain_JSONFormat(t *testing.T) {
	rec := newRecordingConnector()
	deps := DatabaseDeps{Registry: registryWith(rec)}

	if _, err := HandleExplain(context.Background(), deps, map[string]any{
		"query":  "SELECT 1",
		"format": "json",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rec.queries) != 1 || !strings.HasPrefix(rec.queries[0], "EXPLAIN (FORMAT JSON) ") {
		t.Fatalf("unexpected statement: %v", rec.queries)
	}
}

// --- kill_query: verb and pg function names must be well-formed ---

func TestKillQuery_CancelUsesCorrectNames(t *testing.T) {
	fake := &controlOutcomeConnector{readRows: killQueryReadRows}
	deps := DatabaseDeps{Registry: registryWith(fake), IsAdmin: true}

	result, err := HandleKillQuery(context.Background(), deps, map[string]any{"pid": float64(123)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", extractText(t, result))
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(extractText(t, result)), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["action"] != "cancelled" {
		t.Errorf("action = %v, want cancelled", resp["action"])
	}
}

func TestKillQuery_NoteUsesRealFunctionName(t *testing.T) {
	for _, tc := range []struct {
		name   string
		force  bool
		pgFunc string
		verb   string
	}{
		{"cancel", false, "pg_cancel_backend", "cancelled"},
		{"terminate", true, "pg_terminate_backend", "terminated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &controlOutcomeConnector{readRows: killQueryReadRows}
			deps := DatabaseDeps{Registry: registryWith(fake), IsAdmin: true}

			args := map[string]any{"pid": float64(123)}
			if tc.force {
				args["force"] = true
			}
			result, err := HandleKillQuery(context.Background(), deps, args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			var resp map[string]any
			if err := json.Unmarshal([]byte(extractText(t, result)), &resp); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			note, _ := resp["note"].(string)
			if !strings.HasPrefix(note, tc.pgFunc+" returned false") {
				t.Errorf("note names a nonexistent function: %q (want prefix %q)", note, tc.pgFunc)
			}
			for _, bad := range []string{"pg_cancelle_backend", "pg_terminated_backend"} {
				if strings.Contains(note, bad) {
					t.Errorf("note contains malformed function name %q: %s", bad, note)
				}
			}
			if resp["action"] != tc.verb {
				t.Errorf("action = %v, want %v", resp["action"], tc.verb)
			}
		})
	}
}

func TestKillQuery_ErrorMessageUsesRealVerb(t *testing.T) {
	for _, tc := range []struct {
		name  string
		force bool
		verb  string
		bad   string
	}{
		{"cancel", false, "failed to cancel PID", "cancell"},
		{"terminate", true, "failed to terminate PID", "terminat "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &controlOutcomeConnector{readRows: killQueryReadRows, controlErr: errors.New("permission denied")}
			deps := DatabaseDeps{Registry: registryWith(fake), IsAdmin: true}

			args := map[string]any{"pid": float64(123)}
			if tc.force {
				args["force"] = true
			}
			result, err := HandleKillQuery(context.Background(), deps, args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			txt := extractText(t, result)
			if !strings.Contains(txt, tc.verb) {
				t.Errorf("expected %q in the error, got: %s", tc.verb, txt)
			}
			if strings.Contains(txt, tc.bad) {
				t.Errorf("error text still contains the malformed verb %q: %s", tc.bad, txt)
			}
		})
	}
}
