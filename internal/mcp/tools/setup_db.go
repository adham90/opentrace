package tools

import "context"

func HandleSetupDBGuide(_ context.Context, _ SetupDeps, args map[string]any) (*CallToolResult, error) {
	dbType := ArgString(args, "database")
	if dbType == "" {
		return NewToolResultError("database is required. Use: postgres, mysql, redis"), nil
	}

	guide := getDBGuide(dbType)
	if guide == "" {
		return NewToolResultError("unknown database: " + dbType + ". Supported: postgres, mysql, redis"), nil
	}

	return NewToolResultText(guide), nil
}

func getDBGuide(dbType string) string {
	switch dbType {
	case "postgres", "postgresql", "pg":
		return `## Connect Postgres to OpenTrace

### Step 1: Create a read-only user (IMPORTANT for security)

Run this on your Postgres server:

CREATE USER opentrace_reader WITH PASSWORD 'a_strong_password_here';
GRANT CONNECT ON DATABASE your_database TO opentrace_reader;
GRANT USAGE ON SCHEMA public TO opentrace_reader;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO opentrace_reader;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO opentrace_reader;

This user can only read — it cannot insert, update, delete, or drop anything.
OpenTrace also enforces read-only at the connection level (SET default_transaction_read_only = ON).

### Step 2: Connect via the agent

Tell your agent:
  "Connect my Postgres database at postgres://opentrace_reader:password@host:5432/dbname"

Or call:
  connectors(action: "create", type: "database", connection_string: "postgres://opentrace_reader:password@host:5432/dbname")

### Step 3: Test

  connectors(action: "test", id: "<connector_id>")

### What the agent can do after connecting

- Run SELECT queries against your tables
- EXPLAIN query plans (without ANALYZE for safety)
- Check pg_stat_statements for slow queries
- Inspect table sizes, index health, lock contention
- Detect N+1 queries and missing indexes

### Security

- Read-only Postgres user (database-level enforcement)
- SQL parsing rejects INSERT/UPDATE/DELETE/DROP before execution
- SET default_transaction_read_only = ON on every connection
- Statement timeout enforced (default: 5 seconds)
- Row limit enforced (default: 500 rows)
- Circuit breaker disconnects on repeated failures`

	case "mysql", "mariadb":
		return `## Connect MySQL to OpenTrace

### Step 1: Create a read-only user (IMPORTANT for security)

Run this on your MySQL server:

CREATE USER 'opentrace_reader'@'%' IDENTIFIED BY 'a_strong_password_here';
GRANT SELECT ON your_database.* TO 'opentrace_reader'@'%';
FLUSH PRIVILEGES;

This user can only read — it cannot modify any data.
OpenTrace also enforces read-only at the session level (SET SESSION transaction_read_only = 1).

### Step 2: Connect via the agent

Tell your agent:
  "Connect my MySQL database at opentrace_reader:password@tcp(host:3306)/dbname"

Or call:
  connectors(action: "create", type: "mysql", connection_string: "opentrace_reader:password@tcp(host:3306)/dbname")

### Step 3: Test

  connectors(action: "test", id: "<connector_id>")

### Security

- Read-only MySQL user (database-level enforcement)
- SQL parsing rejects INSERT/UPDATE/DELETE/DROP before execution
- SET SESSION transaction_read_only = 1 on every connection
- Statement timeout and row limits enforced`

	case "redis":
		return `## Connect Redis to OpenTrace

### Step 1: Create a read-only ACL user (recommended)

On Redis 6+:

ACL SETUSER opentrace on >a_strong_password_here ~* +@read +info +slowlog -@write -@admin -@dangerous

This user can only read keys and run INFO/SLOWLOG — no writes, no admin commands.

### Step 2: Connect via the agent

Tell your agent:
  "Connect my Redis at redis://opentrace:password@host:6379/0"

Or call:
  connectors(action: "create", type: "redis", connection_string: "redis://opentrace:password@host:6379/0")

### What the agent can do after connecting

- redis_info: Server stats, memory usage, connected clients
- redis_keys: Scan keys by pattern (non-blocking SCAN, not KEYS)
- redis_get: Read a key's value and type
- redis_slowlog: Recent slow commands

### Security

- Read-only Redis ACL user (server-level enforcement)
- Only read commands are exposed as tools
- No write, admin, or dangerous commands available`

	default:
		return ""
	}
}
