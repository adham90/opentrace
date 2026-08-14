package guardrail

import "testing"

// Findings #1, #5 and #6: the read-only guardrail must not be defeated by
// comments used as token separators, by data-modifying CTEs, or by MySQL's
// filesystem-writing SELECT ... INTO OUTFILE.
func TestValidateReadOnlyGeneric_Bypasses(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		// #1 comment as a token separator between the function name and "(".
		{"comment between name and paren", "SELECT pg_terminate_backend/**/(12345)"},
		{"comment with text between name and paren", "SELECT pg_read_file/* wat */('/etc/passwd')"},
		{"comment separator on lo_export", "SELECT lo_export/**/(1234, '/tmp/x')"},
		{"comment separator on dblink", "SELECT dblink/**/('host=evil', 'SELECT 1')"},
		{"line comment separator", "SELECT pg_cancel_backend -- kill\n(1)"},
		{"comment before function name", "SELECT /*x*/pg_reload_conf()"},
		{"nested block comment", "SELECT /* /* */ pg_terminate_backend(1) */ 1"},
		{"mysql versioned comment hides call", "SELECT 1 /*!50000, pg_terminate_backend(1) */"},
		{"mysql versioned comment hides delete", "SELECT 1 /*!50000 UNION DELETE FROM t */"},
		{"form feed separator", "SELECT pg_terminate_backend\f(1)"},
		{"vertical tab separator", "SELECT pg_terminate_backend\v(1)"},
		{"unicode space separator", "SELECT pg_terminate_backend (1)"},
		{"quoted identifier call", `SELECT "pg_terminate_backend"(1)`},
		{"backquoted identifier call", "SELECT `load_file`('/etc/passwd')"},
		{"advisory lock", "SELECT pg_advisory_lock(1)"},
		{"set_config", "SELECT set_config('work_mem', '1GB', false)"},
		{"sqlite writefile", "SELECT writefile('/tmp/x', 'data')"},
		{"sqlite load_extension", "SELECT load_extension('/tmp/evil.so')"},

		// #6 data-modifying CTEs.
		{"cte delete", "WITH x AS (SELECT 1) DELETE FROM users"},
		{"cte update", "WITH x AS (SELECT 1) UPDATE users SET name = 'x'"},
		{"cte insert", "WITH x AS (SELECT 1) INSERT INTO users (name) VALUES ('x')"},
		{"cte insert select", "WITH x AS (SELECT 1) INSERT INTO t SELECT * FROM x"},
		{"cte with comment then delete", "WITH x AS (SELECT 1)/*c*/DELETE FROM users"},
		{"cte drop", "WITH x AS (SELECT 1) DROP TABLE users"},
		{"select with nested delete cte", "SELECT * FROM (WITH y AS (DELETE FROM t RETURNING *) SELECT * FROM y) z"},
		{"returning delete inside cte", "WITH d AS (DELETE FROM orders RETURNING id) SELECT * FROM d"},
		{"select for update", "SELECT * FROM users FOR UPDATE"},

		// #5 filesystem writes.
		{"into outfile", "SELECT secret FROM creds INTO OUTFILE '/var/lib/mysql-files/x'"},
		{"into outfile after limit", "SELECT secret FROM creds LIMIT 10 INTO OUTFILE '/tmp/x'"},
		{"into dumpfile", "SELECT secret FROM creds INTO DUMPFILE '/tmp/x'"},
		{"into outfile lowercase", "select secret from creds into outfile '/tmp/x'"},
		{"into outfile with comment", "SELECT secret FROM creds INTO/**/OUTFILE '/tmp/x'"},
		{"postgres select into table", "SELECT * INTO stolen FROM creds"},

		// Stacked statements, however they are disguised.
		{"stacked statements", "SELECT 1; DROP TABLE users"},
		{"stacked with comment", "SELECT 1;/**/DROP TABLE users"},
		{"stacked with line comment", "SELECT 1; -- x\nDROP TABLE users"},
		{"stacked after trailing semicolons", "SELECT 1;;DELETE FROM t"},

		// Assorted extras found while probing the hardened matcher.
		{"comment split explain analyze", "EXPLAIN/**/ANALYZE SELECT 1"},
		{"pragma write via comment", "PRAGMA journal_mode/**/=DELETE"},
		{"union side effect", "SELECT 1 UNION SELECT pg_reload_conf()"},
		{"dollar quoted then call", "SELECT $$x$$ || pg_reload_conf()"},
		{"leading comment then delete", "/*c*/DELETE FROM t"},
		{"load data infile", "LOAD DATA INFILE '/etc/passwd' INTO TABLE t"},
		{"ideographic space separator", "SELECT pg_terminate_backend　(1)"},
		{"line comment then call paren", "SELECT pg_terminate_backend -- c\n(1)"},

		// Malformed input must be rejected, not guessed at.
		{"unterminated string", "SELECT 'abc"},
		{"unterminated comment", "SELECT 1 /* abc"},
		{"comment only", "/* just a comment */"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateReadOnlyGeneric(tt.query); err == nil {
				t.Errorf("expected %q to be rejected, got nil", tt.query)
			}
		})
	}
}

// The hardened matcher must not start rejecting ordinary read queries.
func TestValidateReadOnlyGeneric_NoFalsePositives(t *testing.T) {
	allowed := []string{
		"SELECT * FROM users",
		"SELECT id, name\nFROM users\nWHERE id = 1\nLIMIT 100",
		"SELECT 'DELETE' AS action FROM audit_log",
		"SELECT * FROM events WHERE payload = 'a;b'",
		"SELECT deleted_at, updated_at, created_at FROM users",
		"SELECT REPLACE(name, 'a', 'b') FROM users",
		"SELECT TRUNCATE(1.234, 2) AS t",
		"SELECT INSERT('abcd', 1, 2, 'zz') AS s",
		"SELECT date_trunc('day', created_at) FROM events",
		"SELECT * FROM users -- trailing comment",
		"/* leading */ SELECT 1",
		"SELECT lower(name) FROM users",
		"WITH r AS (SELECT * FROM users) SELECT * FROM r",
		`SELECT "user".name FROM "user"`,
		"EXPLAIN (FORMAT JSON) SELECT 1",
		"SHOW CREATE TABLE users",
		"SHOW TABLES",
		"PRAGMA table_info(users)",
	}
	for _, q := range allowed {
		if err := ValidateReadOnlyGeneric(q); err != nil {
			t.Errorf("expected %q to be allowed, got: %v", q, err)
		}
	}
}

// Finding #7: LIMIT must be recognised regardless of the surrounding
// whitespace, and must not be seen inside a string literal or a comment.
func TestHasLimitGeneric_Boundaries(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{"newline before limit", "SELECT id, name\nFROM users\nLIMIT 100", true},
		{"newline around limit", "SELECT id FROM users\nLIMIT\n100", true},
		{"tab before limit", "SELECT id FROM users\tLIMIT 10", true},
		{"trailing semicolon", "SELECT * FROM users LIMIT 10;", true},
		{"lowercase", "select * from users limit 10", true},
		{"carriage return", "SELECT 1\r\nLIMIT 1", true},
		{"no limit", "SELECT * FROM users", false},
		{"limit inside literal", "SELECT * FROM users WHERE name LIKE '%limit%'", false},
		{"limit inside comment", "SELECT * FROM users -- limit 5\n", false},
		{"limit as identifier prefix", "SELECT limit_reached FROM quotas", false},
		{"limit as identifier suffix", "SELECT rate_limit FROM quotas", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasLimitGeneric(tt.query); got != tt.want {
				t.Errorf("HasLimitGeneric(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestNormalizeSQL(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{"block comment becomes space", "SELECT a/**/(b)", "SELECT a (b)"},
		{"literal masked", "SELECT 'secret'", "SELECT ''"},
		{"escaped quote in literal", `SELECT 'it''s' , 1`, "SELECT '' , 1"},
		{"backslash escaped quote", `SELECT 'a\'b' , 1`, "SELECT '' , 1"},
		{"identifier unquoted", `SELECT "col" FROM t`, "SELECT col FROM t"},
		{"whitespace collapsed", "SELECT\t1\n\n  FROM\fx", "SELECT 1 FROM x"},
		{"line comment dropped", "SELECT 1 -- note\nFROM t", "SELECT 1 FROM t"},
		{"minus minus arithmetic kept", "SELECT 1--1", "SELECT 1--1"},
		{"dollar quote masked", "SELECT $$a;b$$", "SELECT ''"},
		{"placeholder kept", "SELECT $1", "SELECT $1"},
		{"versioned comment body kept", "SELECT 1 /*!40001 , 2 */", "SELECT 1 , 2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeSQL(tt.query)
			if err != nil {
				t.Fatalf("normalizeSQL(%q) error: %v", tt.query, err)
			}
			if got != tt.want {
				t.Errorf("normalizeSQL(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}
