package guardrail

import "testing"

// Finding #3: a read-only transaction does NOT block side-effecting functions,
// so a SELECT calling them must be rejected by the guardrail.
func TestValidateReadOnlyGeneric_SideEffectingFunctions(t *testing.T) {
	blocked := []string{
		"SELECT pg_terminate_backend(123)",
		"SELECT pg_cancel_backend(123)",
		"SELECT pg_reload_conf()",
		"SELECT pg_rotate_logfile()",
		"SELECT pg_stat_reset()",
		"SELECT pg_stat_reset_shared('bgwriter')",
		"SELECT pg_switch_wal()",
		"SELECT setval('my_seq', 100)",
		"SELECT nextval('my_seq')",
		"SELECT lo_import('/etc/passwd')",
		"SELECT lo_export(1234, '/tmp/x')",
		"SELECT dblink('host=evil', 'SELECT 1')",
		"SELECT dblink_exec('host=evil', 'DROP TABLE t')",
		"SELECT pg_read_file('/etc/passwd')",
		"SELECT pg_read_binary_file('/etc/passwd')",
		"SELECT pg_ls_dir('/')",
		"SELECT pg_ls_waldir()",
		"WITH x AS (SELECT setval('s', 1)) SELECT * FROM x",
		"EXPLAIN SELECT pg_terminate_backend(1)",
		"select PG_TERMINATE_BACKEND ( 1 )", // case + whitespace
	}
	for _, q := range blocked {
		if err := ValidateReadOnlyGeneric(q); err == nil {
			t.Errorf("expected %q to be rejected", q)
		}
	}
}

// Legitimate read queries that merely resemble denied names must still pass.
func TestValidateReadOnlyGeneric_LegitSelectsStillPass(t *testing.T) {
	allowed := []string{
		"SELECT * FROM users",
		"SELECT COUNT(*) FROM users",
		"SELECT lower(name) FROM users",       // not lo_
		"SELECT log(value) FROM metrics",      // not lo_
		"SELECT my_nextval FROM counters",     // column, no call
		"SELECT setvalue_flag FROM settings",  // identifier superstring, no call
		"SELECT id FROM pg_stat_activity",     // reading a stats view is fine
		"WITH r AS (SELECT * FROM users) SELECT * FROM r",
	}
	for _, q := range allowed {
		if err := ValidateReadOnlyGeneric(q); err != nil {
			t.Errorf("expected %q to be allowed, got: %v", q, err)
		}
	}
}

// Finding #3 (PRAGMA bypass): the assignment / value-setting form must be
// rejected while the read/query form still passes.
func TestValidateReadOnlyGeneric_PragmaWriteForms(t *testing.T) {
	blocked := []string{
		"PRAGMA journal_mode=DELETE",
		"PRAGMA journal_mode = WAL",
		"PRAGMA journal_mode(DELETE)",
		"PRAGMA user_version=99",
		"PRAGMA user_version = 42",
		"PRAGMA schema_version=1",
		"PRAGMA page_size=8192",
		"PRAGMA max_page_count=1000",
		"PRAGMA application_id=5",
		"PRAGMA main.user_version=7", // schema-qualified write
		"PRAGMA wal_checkpoint(FULL)", // not in allowlist at all
		"PRAGMA optimize",
	}
	for _, q := range blocked {
		if err := ValidateReadOnlyGeneric(q); err == nil {
			t.Errorf("expected %q to be rejected", q)
		}
	}

	allowed := []string{
		"PRAGMA journal_mode",
		"PRAGMA user_version",
		"PRAGMA schema_version",
		"PRAGMA table_info(users)",
		"PRAGMA index_list(users)",
		"PRAGMA foreign_key_list(orders)",
		"PRAGMA table_list",
		"PRAGMA main.user_version",
		"PRAGMA page_count;",
	}
	for _, q := range allowed {
		if err := ValidateReadOnlyGeneric(q); err != nil {
			t.Errorf("expected %q to be allowed, got: %v", q, err)
		}
	}
}
