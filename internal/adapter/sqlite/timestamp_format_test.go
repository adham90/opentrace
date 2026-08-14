package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// The tests in this file pin the one timestamp contract every store in this
// package depends on: TEXT timestamp columns hold RFC3339 UTC, so that the
// lexicographic "col < RFC3339 cutoff" comparisons used by staleness sweeps,
// expiry checks and retention prunes mean what they say. A value written in
// bun's own time format ("2006-01-02 15:04:05.999999-07:00") sorts below the
// RFC3339 rendering of the same instant, because ' ' < 'T'.

// assertRFC3339 fails unless the raw column value is RFC3339 and reads back as
// the instant it was meant to record.
func assertRFC3339(t *testing.T, label, raw string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatalf("%s = %q, want RFC3339: %v", label, raw, err)
	}
	return parsed
}

// TestServerStore_MarkStaleOffline_KeepsFreshHeartbeatOnline is the regression
// test for the critical bug: UpdateHeartbeat wrote last_seen_at through bun, so
// MarkStaleOffline's RFC3339 cutoff compared greater than every same-day
// heartbeat and swept every live server offline.
func TestServerStore_MarkStaleOffline_KeepsFreshHeartbeatOnline(t *testing.T) {
	db := setupTestDB(t)
	s := NewServerStore(db)
	ctx := context.Background()

	srv, err := s.Register(ctx, store.RegisterServerParams{
		Hostname:    "live-host",
		Environment: "production",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := s.UpdateHeartbeat(ctx, srv.ID); err != nil {
		t.Fatalf("UpdateHeartbeat: %v", err)
	}

	var lastSeen string
	if err := db.NewRaw(`SELECT last_seen_at FROM servers WHERE id = ?`, srv.ID.String()).
		Scan(ctx, &lastSeen); err != nil {
		t.Fatalf("select last_seen_at: %v", err)
	}

	n, err := s.MarkStaleOffline(ctx, 2*time.Minute)
	if err != nil {
		t.Fatalf("MarkStaleOffline: %v", err)
	}
	if n != 0 {
		t.Errorf("MarkStaleOffline swept %d freshly-heartbeated servers, want 0 (last_seen_at=%q)", n, lastSeen)
	}
	assertRFC3339(t, "servers.last_seen_at after heartbeat", lastSeen)

	got, err := s.GetByID(ctx, srv.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != store.ServerOnline {
		t.Errorf("status = %q, want %q after a heartbeat one moment ago", got.Status, store.ServerOnline)
	}
}

// TestServerStore_MarkStaleOffline_StillSweepsStale keeps the fix honest: a
// server that really has gone quiet must still flip offline.
func TestServerStore_MarkStaleOffline_StillSweepsStale(t *testing.T) {
	db := setupTestDB(t)
	s := NewServerStore(db)
	ctx := context.Background()

	srv, err := s.Register(ctx, store.RegisterServerParams{Hostname: "quiet-host"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	stale := rfc3339(time.Now().UTC().Add(-10 * time.Minute))
	if _, err := db.NewRaw(`UPDATE servers SET last_seen_at = ? WHERE id = ?`,
		stale, srv.ID.String()).Exec(ctx); err != nil {
		t.Fatalf("backdate last_seen_at: %v", err)
	}

	n, err := s.MarkStaleOffline(ctx, 2*time.Minute)
	if err != nil {
		t.Fatalf("MarkStaleOffline: %v", err)
	}
	if n != 1 {
		t.Fatalf("MarkStaleOffline = %d, want 1", n)
	}
	got, _ := s.GetByID(ctx, srv.ID)
	if got.Status != store.ServerOffline {
		t.Errorf("status = %q, want %q", got.Status, store.ServerOffline)
	}
}

// TestServerStore_HeartbeatBringsServerBackOnline covers the second half of the
// critical bug: a swept server used to stay offline until it re-registered,
// because UpdateHeartbeat never restored the status.
func TestServerStore_HeartbeatBringsServerBackOnline(t *testing.T) {
	db := setupTestDB(t)
	s := NewServerStore(db)
	ctx := context.Background()

	srv, err := s.Register(ctx, store.RegisterServerParams{Hostname: "flapping-host"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := db.NewRaw(`UPDATE servers SET status = 'offline', last_seen_at = ? WHERE id = ?`,
		rfc3339(time.Now().UTC().Add(-time.Hour)), srv.ID.String()).Exec(ctx); err != nil {
		t.Fatalf("force offline: %v", err)
	}

	if err := s.UpdateHeartbeat(ctx, srv.ID); err != nil {
		t.Fatalf("UpdateHeartbeat: %v", err)
	}
	got, err := s.GetByID(ctx, srv.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != store.ServerOnline {
		t.Errorf("status = %q, want %q after heartbeat", got.Status, store.ServerOnline)
	}
}

// TestMCPActivityStore_StatsCountsFreshActivity covers the store side of the
// mcp_activity timestamp bug: Stats reported zeros and a nil LastActivity for
// traffic logged seconds earlier.
func TestMCPActivityStore_StatsCountsFreshActivity(t *testing.T) {
	db := setupTestDB(t)
	s := NewMCPActivityStore(db)
	ctx := context.Background()

	if err := s.Log(ctx, store.LogMCPActivityParams{
		SessionID: "sess-1",
		ToolName:  "logs",
	}); err != nil {
		t.Fatalf("Log: %v", err)
	}
	if err := s.Log(ctx, store.LogMCPActivityParams{
		SessionID: "sess-1",
		ToolName:  "errors",
		IsError:   true,
	}); err != nil {
		t.Fatalf("Log error call: %v", err)
	}

	var createdAt string
	if err := db.NewRaw(`SELECT MAX(created_at) FROM mcp_activity`).Scan(ctx, &createdAt); err != nil {
		t.Fatalf("select created_at: %v", err)
	}
	assertRFC3339(t, "mcp_activity.created_at", createdAt)

	stats, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.ActiveSessions != 1 {
		t.Errorf("ActiveSessions = %d, want 1", stats.ActiveSessions)
	}
	if stats.CallsLastHour != 2 {
		t.Errorf("CallsLastHour = %d, want 2", stats.CallsLastHour)
	}
	if stats.ErrorsLastHour != 1 {
		t.Errorf("ErrorsLastHour = %d, want 1", stats.ErrorsLastHour)
	}
	if stats.LastActivity == nil {
		t.Error("LastActivity = nil, want the timestamp of the call just logged")
	}

	// A fresh row must survive a 30-day prune.
	deleted, err := s.Prune(ctx, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if deleted != 0 {
		t.Errorf("Prune(30d) deleted %d rows logged seconds ago, want 0", deleted)
	}
}

// TestAuditStore_PruneKeepsFreshEntries covers the audit-record variant: a
// bun-written created_at made Prune destroy entries up to a day early.
func TestAuditStore_PruneKeepsFreshEntries(t *testing.T) {
	db := setupTestDB(t)
	s := NewAuditStore(db)
	ctx := context.Background()

	if err := s.Log(ctx, store.LogAuditParams{
		UserID:      "u1",
		UserEmail:   "u1@example.com",
		Action:      "user.create",
		Environment: "production",
	}); err != nil {
		t.Fatalf("Log: %v", err)
	}

	var createdAt string
	if err := db.NewRaw(`SELECT created_at FROM audit_log LIMIT 1`).Scan(ctx, &createdAt); err != nil {
		t.Fatalf("select created_at: %v", err)
	}
	assertRFC3339(t, "audit_log.created_at", createdAt)

	deleted, err := s.Prune(ctx, 90*24*time.Hour)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if deleted != 0 {
		t.Errorf("Prune(90d) deleted %d entries written seconds ago, want 0", deleted)
	}

	entries, err := s.Recent(ctx, 10)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("Recent returned %d entries, want 1", len(entries))
	}
	if entries[0].Action != "user.create" {
		t.Errorf("Action = %q, want user.create", entries[0].Action)
	}
}

// TestAgentNoteStore_PruneKeepsFreshNotes is the agent_notes variant.
func TestAgentNoteStore_PruneKeepsFreshNotes(t *testing.T) {
	db := setupTestDB(t)
	s := NewAgentNoteStore(db)
	ctx := context.Background()

	note, err := s.Upsert(ctx, "error", "fp-1", "looks like a retry storm")
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if note.Note != "looks like a retry storm" {
		t.Errorf("Note = %q, want the note just written", note.Note)
	}

	var updatedAt string
	if err := db.NewRaw(`SELECT updated_at FROM agent_notes LIMIT 1`).Scan(ctx, &updatedAt); err != nil {
		t.Fatalf("select updated_at: %v", err)
	}
	assertRFC3339(t, "agent_notes.updated_at", updatedAt)

	deleted, err := s.Prune(ctx, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if deleted != 0 {
		t.Errorf("Prune(30d) deleted %d notes written seconds ago, want 0", deleted)
	}
}

// TestCodeEntityStore_TimestampsAreOneFormat covers the mixed-format half of
// the code_entities bug: Upsert wrote bun's format while IncrementError wrote
// RFC3339, leaving two incomparable formats in one column.
func TestCodeEntityStore_TimestampsAreOneFormat(t *testing.T) {
	db := setupTestDB(t)
	s := NewCodeEntityStore(db)
	ctx := context.Background()

	if _, err := s.Upsert(ctx, store.UpsertCodeEntityParams{
		EntityType: store.CodeEntityFile,
		EntityName: "app/models/user.rb",
		Service:    "api",
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := s.IncrementError(ctx, store.CodeEntityFile, "app/controllers/x.rb", "api"); err != nil {
		t.Fatalf("IncrementError: %v", err)
	}

	var raws []string
	if err := db.NewRaw(`SELECT created_at FROM code_entities`).Scan(ctx, &raws); err != nil {
		t.Fatalf("select created_at: %v", err)
	}
	if len(raws) != 2 {
		t.Fatalf("got %d rows, want 2", len(raws))
	}
	for _, raw := range raws {
		assertRFC3339(t, "code_entities.created_at", raw)
	}

	deleted, err := s.Prune(ctx, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if deleted != 0 {
		t.Errorf("Prune(30d) deleted %d entities written seconds ago, want 0", deleted)
	}
}

// TestSessionStore_ShortTTLSessionIsValid covers the session variant: a
// bun-written expires_at made every session expiring on the current UTC day
// look already expired, so a sub-24h session never validated and the cleanup
// job deleted it while still live.
func TestSessionStore_ShortTTLSessionIsValid(t *testing.T) {
	db := setupTestDB(t)
	us := NewUserStore(db)
	ss := NewSessionStore(db)
	ctx := context.Background()

	user, err := us.Create(ctx, store.CreateUserParams{
		Email:        "short-ttl@example.com",
		PasswordHash: "hash",
		DisplayName:  "Short TTL",
		Role:         store.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}

	if _, err := ss.Create(ctx, user.ID, "short-token", time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("Create session: %v", err)
	}

	var expiresAt string
	if err := db.NewRaw(`SELECT expires_at FROM sessions WHERE token = ?`, "short-token").
		Scan(ctx, &expiresAt); err != nil {
		t.Fatalf("select expires_at: %v", err)
	}
	assertRFC3339(t, "sessions.expires_at", expiresAt)

	if _, err := ss.GetByToken(ctx, "short-token"); err != nil {
		t.Fatalf("GetByToken on a session valid for another hour: %v (expires_at=%q)", err, expiresAt)
	}

	deleted, err := ss.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if deleted != 0 {
		t.Errorf("DeleteExpired removed %d live sessions, want 0", deleted)
	}

	// And an actually-expired session must still be collected.
	if _, err := ss.Create(ctx, user.ID, "dead-token", time.Now().UTC().Add(-time.Hour)); err != nil {
		t.Fatalf("Create expired session: %v", err)
	}
	deleted, err = ss.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if deleted != 1 {
		t.Errorf("DeleteExpired = %d, want 1 (the expired session)", deleted)
	}
}
