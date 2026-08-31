package sqlite

import (
	"database/sql"
	_ "embed"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/adham90/opentrace/migrations"
	"github.com/uptrace/bun"
)

// legacyInitSQL is a byte-for-byte snapshot of migrations/000001_init.up.sql as
// it shipped before the in-place corrections (narrow data_sources.type CHECK,
// datetime('now') DEFAULTs, redundant idx_users_email / idx_sessions_token, no
// timestamp backfill). Every already-deployed install has exactly this schema
// recorded as schema_version = 1, so it is the starting point 000002 has to
// upgrade. It is a frozen fixture: never regenerate it from the live migration.
//
//go:embed testdata/legacy_000001_init.up.sql
var legacyInitSQL string

const (
	legacyTS      = "2026-08-14 10:00:00.123456+00:00" // bun sqlite dialect format
	wantTS        = "2026-08-14T10:00:00Z"
	garbageTS     = "not a timestamp"     // no date shape at all
	dateShapedBad = "2026-13-45 99:99:99" // date-shaped but unparseable -> strftime NULL
)

// setupLegacyDB builds an in-memory database in the exact state an existing
// install is in: the pre-fix 000001 schema, schema_version = 1, and rows in the
// legacy timestamp format.
func setupLegacyDB(t *testing.T) *bun.DB {
	t.Helper()

	db, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("opening in-memory SQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.DB.Exec(legacyInitSQL); err != nil {
		t.Fatalf("applying legacy 000001: %v", err)
	}
	mustExec(t, db.DB, `CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`)
	mustExec(t, db.DB, `INSERT INTO schema_version (version) VALUES (1)`)

	seedLegacyRows(t, db.DB)
	return db
}

func seedLegacyRows(t *testing.T, db *sql.DB) {
	t.Helper()

	mustExec(t, db, `INSERT INTO servers
		(id, hostname, ip_address, os, arch, agent_version, labels, status, last_seen_at, created_at, updated_at, display_name, environment)
		VALUES ('srv-1', 'web-01', '10.0.0.1', 'linux', 'arm64', '1.2.3', '{"a":"b"}', 'online', ?, ?, ?, 'Web 01', 'production')`,
		legacyTS, legacyTS, legacyTS)

	mustExec(t, db, `INSERT INTO users (id, email, password_hash, display_name, role, mcp_enabled, mcp_token, is_active, allowed_environments, created_at, updated_at)
		VALUES ('u-1', 'a@example.com', 'hash', 'A', 'admin', 1, 'tok-1', 1, '["*"]', ?, ?)`, legacyTS, legacyTS)

	mustExec(t, db, `INSERT INTO sessions (id, user_id, token, expires_at, created_at)
		VALUES ('sess-1', 'u-1', 'sesstok', ?, ?)`, legacyTS, legacyTS)

	// Two data_sources rows with every nullable column populated, so the rebuild
	// can be checked column by column.
	mustExec(t, db, `INSERT INTO data_sources
		(id, type, name, config, status, status_message, last_tested_at, created_at, updated_at, environment)
		VALUES ('ds-1', 'logs', 'App Logs', '{"path":"/var/log"}', 'connected', 'all good', ?, ?, ?, 'production')`,
		legacyTS, legacyTS, legacyTS)
	mustExec(t, db, `INSERT INTO data_sources
		(id, type, name, config, status, status_message, last_tested_at, created_at, updated_at, environment)
		VALUES ('ds-2', 'database', 'Primary DB', '{"dsn":"x"}', 'error', NULL, NULL, ?, ?, 'staging')`,
		legacyTS, legacyTS)

	mustExec(t, db, `INSERT INTO audit_log (user_id, user_email, action, environment, created_at)
		VALUES ('u-1', 'a@example.com', 'login', 'production', ?)`, legacyTS)

	mustExec(t, db, `INSERT INTO mcp_activity (session_id, tool_name, created_at, environment)
		VALUES ('s-1', 'search_logs', ?, 'production')`, legacyTS)

	mustExec(t, db, `INSERT INTO jobs (job_type, run_at, created_at) VALUES ('rollup', ?, ?)`, legacyTS, legacyTS)

	mustExec(t, db, `INSERT INTO code_entities (entity_type, entity_name, service, created_at, updated_at)
		VALUES ('file', 'app/foo.rb', 'api', ?, ?)`, legacyTS, legacyTS)

	// agent_notes carries the malformed cases: created_at is NOT NULL, so if the
	// backfill nulled either of these the whole migration would abort.
	mustExec(t, db, `INSERT INTO agent_notes (entity_type, entity_id, note, created_at, updated_at)
		VALUES ('server', 'srv-1', 'legacy', ?, ?)`, legacyTS, legacyTS)
	mustExec(t, db, `INSERT INTO agent_notes (entity_type, entity_id, note, created_at, updated_at)
		VALUES ('server', 'srv-garbage', 'garbage ts', ?, ?)`, garbageTS, garbageTS)
	mustExec(t, db, `INSERT INTO agent_notes (entity_type, entity_id, note, created_at, updated_at)
		VALUES ('server', 'srv-bad-date', 'date-shaped garbage', ?, ?)`, dateShapedBad, dateShapedBad)
}

// readMigration returns the embedded migration whose filename starts with the
// given version prefix.
func readMigration(t *testing.T, prefix string) string {
	t.Helper()
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("reading migrations: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) && strings.HasSuffix(e.Name(), ".up.sql") {
			b, err := fs.ReadFile(migrations.FS, e.Name())
			if err != nil {
				t.Fatalf("reading %s: %v", e.Name(), err)
			}
			return string(b)
		}
	}
	t.Fatalf("no migration found with prefix %q", prefix)
	return ""
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %.60q: %v", query, err)
	}
}

func scalar[T any](t *testing.T, db *bun.DB, query string, args ...any) T {
	t.Helper()
	var v T
	if err := db.DB.QueryRow(query, args...).Scan(&v); err != nil {
		t.Fatalf("query %.80q: %v", query, err)
	}
	return v
}

func indexExists(t *testing.T, db *bun.DB, name string) bool {
	t.Helper()
	return scalar[int](t, db, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, name) > 0
}

// TestUpgradeFromLegacyInstall is the whole point of 000002: an install already
// recorded at schema_version = 1 never re-runs 000001, so every correction made
// to that file in place has to be replayed forward.
func TestUpgradeFromLegacyInstall(t *testing.T) {
	db := setupLegacyDB(t)

	// Sanity: the legacy install really is broken the way we claim.
	if _, err := db.DB.Exec(`INSERT INTO data_sources (id, type, created_at, updated_at) VALUES ('pre', 'mysql', 'x', 'y')`); err == nil {
		t.Fatal("precondition failed: legacy schema accepted type='mysql'")
	}
	if !indexExists(t, db, "idx_users_email") {
		t.Fatal("precondition failed: legacy schema lacks idx_users_email")
	}

	if err := RunSQLiteMigrations(db); err != nil {
		t.Fatalf("upgrade migration failed: %v", err)
	}

	if want := latestMigrationVersion(t); scalar[int](t, db, `SELECT MAX(version) FROM schema_version`) != want {
		t.Errorf("schema_version = %d, want %d",
			scalar[int](t, db, `SELECT MAX(version) FROM schema_version`), want)
	}

	// (a) every ConnectorType is now insertable.
	for _, typ := range []string{"logs", "database", "mysql", "redis", "turso", "monitoring", "server_metrics"} {
		id := "new-" + typ
		_, err := db.DB.Exec(`INSERT INTO data_sources (id, type, name, environment) VALUES (?, ?, ?, 'production')`, id, typ, typ)
		if err != nil {
			t.Errorf("INSERT data_sources type=%q: %v", typ, err)
		}
	}
	// The constraint still constrains.
	if _, err := db.DB.Exec(`INSERT INTO data_sources (id, type) VALUES ('bogus', 'not_a_type')`); err == nil {
		t.Error("data_sources accepted an unknown type after rebuild")
	}
	// And the corrected DEFAULTs came across: a row inserted without timestamps
	// gets RFC3339, not datetime('now')'s space-separated form.
	got := scalar[string](t, db, `SELECT created_at FROM data_sources WHERE id='new-mysql'`)
	if len(got) != len("2006-01-02T15:04:05Z") || got[10] != 'T' || got[len(got)-1] != 'Z' {
		t.Errorf("default created_at = %q, want RFC3339", got)
	}

	// (b) legacy timestamps are now RFC3339.
	for _, c := range []struct{ query, label string }{
		{`SELECT last_seen_at FROM servers WHERE id='srv-1'`, "servers.last_seen_at"},
		{`SELECT created_at FROM servers WHERE id='srv-1'`, "servers.created_at"},
		{`SELECT updated_at FROM servers WHERE id='srv-1'`, "servers.updated_at"},
		{`SELECT expires_at FROM sessions WHERE id='sess-1'`, "sessions.expires_at"},
		{`SELECT created_at FROM sessions WHERE id='sess-1'`, "sessions.created_at"},
		{`SELECT created_at FROM audit_log LIMIT 1`, "audit_log.created_at"},
		{`SELECT created_at FROM mcp_activity LIMIT 1`, "mcp_activity.created_at"},
		{`SELECT run_at FROM jobs LIMIT 1`, "jobs.run_at"},
		{`SELECT created_at FROM jobs LIMIT 1`, "jobs.created_at"},
		{`SELECT created_at FROM code_entities LIMIT 1`, "code_entities.created_at"},
		{`SELECT updated_at FROM code_entities LIMIT 1`, "code_entities.updated_at"},
		{`SELECT created_at FROM agent_notes WHERE entity_id='srv-1'`, "agent_notes.created_at"},
		{`SELECT updated_at FROM agent_notes WHERE entity_id='srv-1'`, "agent_notes.updated_at"},
		{`SELECT created_at FROM data_sources WHERE id='ds-1'`, "data_sources.created_at"},
		{`SELECT updated_at FROM data_sources WHERE id='ds-2'`, "data_sources.updated_at"},
	} {
		if v := scalar[string](t, db, c.query); v != wantTS {
			t.Errorf("%s = %q, want %q", c.label, v, wantTS)
		}
	}

	// (c) no data lost from the data_sources rebuild.
	if n := scalar[int](t, db, `SELECT COUNT(*) FROM data_sources WHERE id IN ('ds-1','ds-2')`); n != 2 {
		t.Fatalf("data_sources legacy row count = %d, want 2", n)
	}
	var (
		typ, name, config, status, env string
		statusMsg, lastTested          sql.NullString
	)
	err := db.DB.QueryRow(`SELECT type, name, config, status, status_message, last_tested_at, environment
		FROM data_sources WHERE id='ds-1'`).Scan(&typ, &name, &config, &status, &statusMsg, &lastTested, &env)
	if err != nil {
		t.Fatalf("reading ds-1: %v", err)
	}
	if typ != "logs" || name != "App Logs" || config != `{"path":"/var/log"}` || status != "connected" ||
		!statusMsg.Valid || statusMsg.String != "all good" || !lastTested.Valid || lastTested.String != legacyTS || env != "production" {
		t.Errorf("ds-1 not preserved: type=%q name=%q config=%q status=%q msg=%v tested=%v env=%q",
			typ, name, config, status, statusMsg, lastTested, env)
	}
	// ds-2's NULLs stayed NULL rather than collapsing to defaults.
	if err := db.DB.QueryRow(`SELECT status_message, last_tested_at FROM data_sources WHERE id='ds-2'`).
		Scan(&statusMsg, &lastTested); err != nil {
		t.Fatalf("reading ds-2: %v", err)
	}
	if statusMsg.Valid || lastTested.Valid {
		t.Errorf("ds-2 NULLs not preserved: msg=%v tested=%v", statusMsg, lastTested)
	}
	// Both indexes on the table were recreated.
	for _, idx := range []string{"idx_data_sources_env", "idx_data_sources_type"} {
		if !indexExists(t, db, idx) {
			t.Errorf("index %s missing after rebuild", idx)
		}
	}

	// (d) redundant indexes dropped, useful ones untouched.
	for _, idx := range []string{"idx_users_email", "idx_sessions_token"} {
		if indexExists(t, db, idx) {
			t.Errorf("redundant index %s still present", idx)
		}
	}
	for _, idx := range []string{"idx_users_mcp_token", "idx_sessions_expires"} {
		if !indexExists(t, db, idx) {
			t.Errorf("index %s was dropped but should remain", idx)
		}
	}
	// The UNIQUE constraints they duplicated still enforce.
	if _, err := db.DB.Exec(`INSERT INTO users (id, email, password_hash, created_at, updated_at)
		VALUES ('u-2', 'a@example.com', 'h', 'x', 'y')`); err == nil {
		t.Error("duplicate users.email accepted after dropping idx_users_email")
	}

	// Other tables' rows survived.
	if n := scalar[int](t, db, `SELECT COUNT(*) FROM agent_notes`); n != 3 {
		t.Errorf("agent_notes count = %d, want 3", n)
	}
}

// TestUpgradeLeavesMalformedTimestampsAlone covers the trap the guard exists
// for: strftime returns NULL for anything it cannot parse, and writing NULL to
// a NOT NULL column aborts the entire migration.
func TestUpgradeLeavesMalformedTimestampsAlone(t *testing.T) {
	db := setupLegacyDB(t)

	if err := RunSQLiteMigrations(db); err != nil {
		t.Fatalf("migration aborted on malformed timestamp: %v", err)
	}

	for _, c := range []struct{ id, want string }{
		{"srv-garbage", garbageTS},
		{"srv-bad-date", dateShapedBad},
	} {
		got := scalar[string](t, db, `SELECT created_at FROM agent_notes WHERE entity_id=?`, c.id)
		if got != c.want {
			t.Errorf("agent_notes[%s].created_at = %q, want it left alone as %q", c.id, got, c.want)
		}
	}
	if n := scalar[int](t, db, `SELECT COUNT(*) FROM agent_notes WHERE created_at IS NULL OR updated_at IS NULL`); n != 0 {
		t.Errorf("%d agent_notes rows were nulled by the backfill", n)
	}
}

// TestUpgradeIsIdempotent runs the whole migration set twice against an
// upgraded legacy install and against a fresh one.
func TestUpgradeIsIdempotent(t *testing.T) {
	for _, tc := range []struct {
		name string
		open func(*testing.T) *bun.DB
	}{
		{"legacy", setupLegacyDB},
		{"fresh", func(t *testing.T) *bun.DB {
			db, err := OpenSQLite(":memory:")
			if err != nil {
				t.Fatalf("opening in-memory SQLite: %v", err)
			}
			t.Cleanup(func() { db.Close() })
			return db
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := tc.open(t)
			for i := 1; i <= 3; i++ {
				if err := RunSQLiteMigrations(db); err != nil {
					t.Fatalf("migration run %d failed: %v", i, err)
				}
			}
			want := latestMigrationVersion(t)
			if v := scalar[int](t, db, `SELECT MAX(version) FROM schema_version`); v != want {
				t.Errorf("schema_version = %d, want %d", v, want)
			}
			if n := scalar[int](t, db, `SELECT COUNT(*) FROM schema_version`); n != want {
				t.Errorf("schema_version rows = %d, want %d (no duplicate applications)", n, want)
			}
			if _, err := db.DB.Exec(`INSERT INTO data_sources (id, type) VALUES ('idem', 'redis')`); err != nil {
				t.Errorf("type='redis' rejected after repeated migrations: %v", err)
			}
		})
	}
}

// TestMigration000002IsNoOpOnFreshInstall re-executes 000002 by hand against a
// database that already has the corrected schema and asserts nothing observable
// changes: same rows, same columns, same indexes.
func TestMigration000002IsNoOpOnFreshInstall(t *testing.T) {
	db := setupTestDB(t)

	mustExec(t, db.DB, `INSERT INTO data_sources (id, type, name, config, status, environment, created_at, updated_at)
		VALUES ('fresh-1', 'turso', 'Edge', '{"url":"x"}', 'connected', 'production', '2026-08-14T10:00:00Z', '2026-08-14T10:00:00Z')`)
	mustExec(t, db.DB, `INSERT INTO servers (id, hostname, status, last_seen_at, created_at, updated_at, environment)
		VALUES ('s-fresh', 'h', 'online', '2026-08-14T10:00:00Z', '2026-08-14T10:00:00Z', '2026-08-14T10:00:00Z', 'production')`)

	before := snapshot(t, db)

	content := readMigration(t, "000002")
	if _, err := db.DB.Exec(content); err != nil {
		t.Fatalf("re-running 000002 on a fresh install: %v", err)
	}

	after := snapshot(t, db)
	for k, want := range before {
		if got := after[k]; got != want {
			t.Errorf("%s changed: %q -> %q", k, want, got)
		}
	}
	if len(before) != len(after) {
		t.Errorf("snapshot size changed: %d -> %d", len(before), len(after))
	}
}

// snapshot captures the observable state 000002 could plausibly disturb:
// data_sources contents and column layout, the timestamps it backfills, and the
// full index list.
func snapshot(t *testing.T, db *bun.DB) map[string]string {
	t.Helper()
	out := map[string]string{}

	rows, err := db.DB.Query(`SELECT id, type, name, config, status,
		COALESCE(status_message,'<null>'), COALESCE(last_tested_at,'<null>'), created_at, updated_at, environment
		FROM data_sources ORDER BY id`)
	if err != nil {
		t.Fatalf("snapshot data_sources: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c [10]string
		ptrs := make([]any, len(c))
		for i := range c {
			ptrs[i] = &c[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan data_sources: %v", err)
		}
		out["data_sources/"+c[0]] = fmt.Sprint(c)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate data_sources: %v", err)
	}

	cols, err := db.DB.Query(`SELECT name, type, "notnull", COALESCE(dflt_value,'<null>'), pk FROM pragma_table_info('data_sources') ORDER BY cid`)
	if err != nil {
		t.Fatalf("snapshot table_info: %v", err)
	}
	defer cols.Close()
	for cols.Next() {
		var name, typ, dflt string
		var notNull, pk int
		if err := cols.Scan(&name, &typ, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan table_info: %v", err)
		}
		out["column/"+name] = fmt.Sprintf("%s notnull=%d default=%s pk=%d", typ, notNull, dflt, pk)
	}
	if err := cols.Err(); err != nil {
		t.Fatalf("iterate table_info: %v", err)
	}

	idx, err := db.DB.Query(`SELECT name, tbl_name, COALESCE(sql,'<implicit>') FROM sqlite_master WHERE type='index' ORDER BY name`)
	if err != nil {
		t.Fatalf("snapshot indexes: %v", err)
	}
	defer idx.Close()
	for idx.Next() {
		var name, tbl, ddl string
		if err := idx.Scan(&name, &tbl, &ddl); err != nil {
			t.Fatalf("scan index: %v", err)
		}
		out["index/"+name] = tbl + " " + ddl
	}
	if err := idx.Err(); err != nil {
		t.Fatalf("iterate indexes: %v", err)
	}

	out["servers/s-fresh"] = scalar[string](t, db, `SELECT last_seen_at FROM servers WHERE id='s-fresh'`)
	out["app_config/count"] = fmt.Sprint(scalar[int](t, db, `SELECT COUNT(*) FROM app_config`))
	return out
}

// latestMigrationVersion reads the highest version from the embedded migration
// files. Derived rather than hardcoded: every new migration used to break these
// assertions, which trains people to bump a number instead of reading the test.
func latestMigrationVersion(t *testing.T) int {
	t.Helper()
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("reading embedded migrations: %v", err)
	}
	highest := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".up.sql") {
			continue
		}
		var v int
		if _, err := fmt.Sscanf(e.Name(), "%06d_", &v); err != nil {
			continue
		}
		if v > highest {
			highest = v
		}
	}
	if highest == 0 {
		t.Fatal("found no embedded .up.sql migrations")
	}
	return highest
}
