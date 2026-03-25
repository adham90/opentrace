package guardrail

import (
	"testing"
)

func TestValidateReadOnlyGeneric(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantErr bool
	}{
		// Allowed
		{name: "simple select", query: "SELECT * FROM users", wantErr: false},
		{name: "select with where", query: "SELECT id, name FROM users WHERE id = 1", wantErr: false},
		{name: "select with join", query: "SELECT u.name FROM users u JOIN orders o ON u.id = o.user_id", wantErr: false},
		{name: "select count", query: "SELECT COUNT(*) FROM users", wantErr: false},
		{name: "explain", query: "EXPLAIN SELECT * FROM users", wantErr: false},
		{name: "show tables", query: "SHOW TABLES", wantErr: false},
		{name: "show columns", query: "SHOW COLUMNS FROM users", wantErr: false},
		{name: "describe", query: "DESCRIBE users", wantErr: false},
		{name: "desc", query: "DESC users", wantErr: false},
		{name: "pragma", query: "PRAGMA table_info(users)", wantErr: false},
		{name: "with cte", query: "WITH recent AS (SELECT * FROM users) SELECT * FROM recent", wantErr: false},
		{name: "trailing semicolon", query: "SELECT 1;", wantErr: false},
		{name: "leading comment", query: "-- comment\nSELECT 1", wantErr: false},
		{name: "block comment", query: "/* comment */ SELECT 1", wantErr: false},
		{name: "case insensitive", query: "select * from users", wantErr: false},

		// Rejected
		{name: "insert", query: "INSERT INTO users (name) VALUES ('test')", wantErr: true},
		{name: "update", query: "UPDATE users SET name = 'test'", wantErr: true},
		{name: "delete", query: "DELETE FROM users", wantErr: true},
		{name: "drop", query: "DROP TABLE users", wantErr: true},
		{name: "create", query: "CREATE TABLE test (id INT)", wantErr: true},
		{name: "alter", query: "ALTER TABLE users ADD COLUMN email TEXT", wantErr: true},
		{name: "truncate", query: "TRUNCATE users", wantErr: true},
		{name: "explain analyze", query: "EXPLAIN ANALYZE SELECT * FROM users", wantErr: true},
		{name: "explain analyze with options", query: "EXPLAIN (ANALYZE true, BUFFERS true) SELECT COUNT(*) FROM users", wantErr: true},
		{name: "explain format json select", query: "EXPLAIN (FORMAT JSON) SELECT 1", wantErr: false},
		{name: "explain insert", query: "EXPLAIN INSERT INTO users (name) VALUES ('test')", wantErr: true},
		{name: "explain delete", query: "EXPLAIN DELETE FROM users WHERE id = 1", wantErr: true},
		{name: "explain update", query: "EXPLAIN UPDATE users SET name = 'test'", wantErr: true},
		{name: "explain drop", query: "EXPLAIN DROP TABLE users", wantErr: true},
		{name: "multiple statements", query: "SELECT 1; DROP TABLE users", wantErr: true},
		{name: "empty", query: "", wantErr: true},
		{name: "whitespace only", query: "   ", wantErr: true},
		{name: "only comment", query: "-- just a comment", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateReadOnlyGeneric(tt.query)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateReadOnlyGeneric(%q) error = %v, wantErr %v", tt.query, err, tt.wantErr)
			}
		})
	}
}

func TestHasLimitGeneric(t *testing.T) {
	tests := []struct {
		query string
		want  bool
	}{
		{"SELECT * FROM users LIMIT 10", true},
		{"SELECT * FROM users limit 10", true},
		{"SELECT * FROM users", false},
		{"SELECT * FROM users WHERE name LIKE '%limit%'", false}, // no space-delimited LIMIT keyword
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			if got := HasLimitGeneric(tt.query); got != tt.want {
				t.Errorf("HasLimitGeneric(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}
