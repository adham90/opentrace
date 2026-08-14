package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/connector"
)

// recordingConnector captures every statement (and its bound parameters) the
// database tool sends, so tests can assert that caller-supplied values never
// reach the SQL text.
type recordingConnector struct {
	queries []string
	args    [][]any
}

func newRecordingConnector() *recordingConnector {
	return &recordingConnector{}
}

func (r *recordingConnector) Type() connector.ConnectorType          { return connector.ConnectorDatabase }
func (r *recordingConnector) TestConnection(_ context.Context) error { return nil }
func (r *recordingConnector) Tools() []connector.Tool                { return nil }
func (r *recordingConnector) Close() error                           { return nil }
func (r *recordingConnector) Environment() string                    { return "" }

func (r *recordingConnector) ExecuteReadQuery(ctx context.Context, query string) (*connector.QueryResult, error) {
	return r.ExecuteReadQueryArgs(ctx, query)
}

func (r *recordingConnector) ExecuteReadQueryArgs(_ context.Context, query string, args ...any) (*connector.QueryResult, error) {
	r.queries = append(r.queries, query)
	r.args = append(r.args, args)
	return &connector.QueryResult{}, nil
}

// injectionPayloads are the values an attacker would supply for a schema /
// table argument. None of them may ever appear in the SQL text.
var injectionPayloads = []struct {
	name    string
	payload string
}{
	{"reported union payload", "x' UNION SELECT card_number, cvv, 1, null, null FROM payment_cards --"},
	// Same attack, short enough to pass the length guard, so the test proves the
	// bound parameter (not the length check) is what makes it inert.
	{"short union payload", "x' UNION SELECT card_number,cvv,1,null,null FROM cards --"},
	{"always-true tail", "public' OR '1'='1"},
	{"comment terminator", "public'--"},
	{"stacked statement", "public'; DROP TABLE users; --"},
	{"escaped quote doubling", "public'' OR ''1''=''1"},
	{"backslash escape", `public\' UNION SELECT 1`},
	{"newline smuggling", "public'\nUNION SELECT 1 --"},
	{"nul byte", "public\x00' UNION SELECT 1"},
}

// assertNoInjection fails when any recorded statement carries the payload text
// or gains a UNION / comment terminator.
func assertNoInjection(t *testing.T, rec *recordingConnector, payload string) {
	t.Helper()
	for i, q := range rec.queries {
		upper := strings.ToUpper(q)
		if strings.Contains(q, payload) {
			t.Fatalf("query %d interpolated the payload into the SQL text:\n%s", i, q)
		}
		if strings.Contains(upper, "UNION") || strings.Contains(upper, "DROP TABLE") || strings.Contains(q, "--") {
			t.Fatalf("query %d gained injected SQL:\n%s", i, q)
		}
	}
}

// assertPayloadBound checks the payload travelled as a bound parameter, i.e. as
// inert data compared against a catalog column.
func assertPayloadBound(t *testing.T, rec *recordingConnector, payload string) {
	t.Helper()
	for _, args := range rec.args {
		for _, a := range args {
			if s, ok := a.(string); ok && s == payload {
				return
			}
		}
	}
	t.Fatalf("payload was never bound as a parameter; recorded args: %v", rec.args)
}

func TestHandleSchema_SchemaArgIsNotInjectable(t *testing.T) {
	for _, tc := range injectionPayloads {
		t.Run(tc.name, func(t *testing.T) {
			rec := newRecordingConnector()
			deps := DatabaseDeps{Registry: registryWith(rec)}

			result, err := HandleSchema(context.Background(), deps, map[string]any{
				"schema": tc.payload,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result == nil {
				t.Fatal("expected a result")
			}

			// Over-long payloads are rejected before any query runs.
			if len(tc.payload) > maxCatalogNameLen {
				if len(rec.queries) != 0 {
					t.Fatalf("over-long name should not reach the database, got %d queries", len(rec.queries))
				}
				if !result.IsError {
					t.Fatal("expected an error result for an over-long schema name")
				}
				return
			}

			assertNoInjection(t, rec, tc.payload)
			assertPayloadBound(t, rec, tc.payload)
		})
	}
}

func TestSchemaTableDetail_TableArgIsNotInjectable(t *testing.T) {
	for _, tc := range injectionPayloads {
		if len(tc.payload) > maxCatalogNameLen {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			rec := newRecordingConnector()
			deps := DatabaseDeps{Registry: registryWith(rec)}

			if _, err := HandleSchema(context.Background(), deps, map[string]any{
				"schema": "public",
				"table":  tc.payload,
			}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			assertNoInjection(t, rec, tc.payload)
			assertPayloadBound(t, rec, tc.payload)

			// The columns query must compare table_name against a placeholder.
			if !strings.Contains(rec.queries[0], "table_name = $2") {
				t.Errorf("expected a bound placeholder for the table name, got:\n%s", rec.queries[0])
			}
		})
	}
}

func TestHandleSchema_RejectsOverlongNames(t *testing.T) {
	rec := newRecordingConnector()
	deps := DatabaseDeps{Registry: registryWith(rec)}

	long := strings.Repeat("a", maxCatalogNameLen+1)
	result, err := HandleSchema(context.Background(), deps, map[string]any{"schema": long})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected an over-long schema name to be rejected")
	}
	if len(rec.queries) != 0 {
		t.Fatalf("no query should have been sent, got %v", rec.queries)
	}
}

func TestHandleTables_TableNameIsBound(t *testing.T) {
	payload := "users' UNION SELECT 1 --"
	rec := newRecordingConnector()
	deps := DatabaseDeps{Registry: registryWith(rec)}

	if _, err := HandleTables(context.Background(), deps, map[string]any{
		"table_name": payload,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertNoInjection(t, rec, payload)
	assertPayloadBound(t, rec, payload)
}

func TestHandleIndexes_TableNameIsBound(t *testing.T) {
	payload := "users' UNION SELECT 1 --"
	rec := newRecordingConnector()
	deps := DatabaseDeps{Registry: registryWith(rec)}

	if _, err := HandleIndexes(context.Background(), deps, map[string]any{
		"table_name": payload,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertNoInjection(t, rec, payload)
	assertPayloadBound(t, rec, payload)
}

func TestHandleQueries_FilterIsBound(t *testing.T) {
	payload := "x' UNION SELECT card_number, cvv, 1, null, null FROM payment_cards --"
	rec := newRecordingConnector()
	deps := DatabaseDeps{Registry: registryWith(rec)}

	if _, err := HandleQueries(context.Background(), deps, map[string]any{
		"filter": payload,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertNoInjection(t, rec, payload)
	assertPayloadBound(t, rec, "%"+payload+"%")
}

// executeReadQuery must refuse to run rather than fall back to interpolation
// when the connector cannot bind parameters.
func TestExecuteReadQuery_FailsClosedWithoutParameterSupport(t *testing.T) {
	var qe connector.QueryExecutor = &plainExecutor{}
	_, err := executeReadQuery(context.Background(), qe, "SELECT 1 WHERE x = $1", "value")
	if err == nil {
		t.Fatal("expected an error when parameters are unsupported")
	}
	if !strings.Contains(err.Error(), "parameterized") {
		t.Errorf("unexpected error: %v", err)
	}
}

// plainExecutor implements only connector.QueryExecutor.
type plainExecutor struct{}

func (plainExecutor) ExecuteReadQuery(context.Context, string) (*connector.QueryResult, error) {
	return &connector.QueryResult{}, nil
}

// TestInjectionEvidence prints the exact statement and bound parameters sent for
// the reported payload, documenting that it travels as data.
func TestInjectionEvidence(t *testing.T) {
	payload := "x' UNION SELECT card_number,cvv,1,null,null FROM cards --"
	rec := newRecordingConnector()
	deps := DatabaseDeps{Registry: registryWith(rec)}
	if _, err := HandleSchema(context.Background(), deps, map[string]any{
		"schema": "public", "table": payload,
	}); err != nil {
		t.Fatal(err)
	}
	t.Logf("SQL: %s", rec.queries[0])
	t.Logf("ARGS: %#v", rec.args[0])
}
