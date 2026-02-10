package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// serverStore implements ServerStore using database/sql (SQLite).
type serverStore struct {
	db *sql.DB
}

// NewServerStore creates a new ServerStore backed by SQLite.
func NewServerStore(db *sql.DB) ServerStore {
	return &serverStore{db: db}
}

func (s *serverStore) Register(ctx context.Context, params RegisterServerParams) (*Server, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	labelsJSON := "{}"
	if params.Labels != nil {
		b, err := json.Marshal(params.Labels)
		if err != nil {
			return nil, fmt.Errorf("marshaling labels: %w", err)
		}
		labelsJSON = string(b)
	}

	// Check if server with this hostname already exists
	var existingID string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM servers WHERE hostname = ?`, params.Hostname,
	).Scan(&existingID)

	if err == nil {
		// Update existing server
		_, err := s.db.ExecContext(ctx,
			`UPDATE servers SET ip_address = ?, os = ?, arch = ?, agent_version = ?,
			 labels = ?, status = 'online', last_seen_at = ?, updated_at = ?
			 WHERE id = ?`,
			params.IPAddress, params.OS, params.Arch, params.AgentVersion,
			labelsJSON, now, now, existingID,
		)
		if err != nil {
			return nil, fmt.Errorf("updating server: %w", err)
		}
		id, _ := uuid.Parse(existingID)
		return s.GetByID(ctx, id)
	}

	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("checking existing server: %w", err)
	}

	// Insert new server
	id := uuid.New()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO servers (id, hostname, ip_address, os, arch, agent_version, labels, status, last_seen_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 'online', ?, ?, ?)`,
		id.String(), params.Hostname, params.IPAddress, params.OS, params.Arch,
		params.AgentVersion, labelsJSON, now, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting server: %w", err)
	}

	return s.GetByID(ctx, id)
}

func (s *serverStore) GetByID(ctx context.Context, id uuid.UUID) (*Server, error) {
	srv := &Server{}
	var labelsStr string
	var createdAt, updatedAt string
	var lastSeenAt sql.NullString

	err := s.db.QueryRowContext(ctx,
		`SELECT id, hostname, ip_address, os, arch, agent_version, labels, status, last_seen_at, created_at, updated_at
		 FROM servers WHERE id = ?`, id.String(),
	).Scan(
		&srv.ID, &srv.Hostname, &srv.IPAddress, &srv.OS, &srv.Arch,
		&srv.AgentVersion, &labelsStr, &srv.Status, &lastSeenAt,
		&createdAt, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("querying server: %w", err)
	}

	if labelsStr != "" {
		json.Unmarshal([]byte(labelsStr), &srv.Labels)
	}
	srv.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	srv.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	if lastSeenAt.Valid {
		t, _ := time.Parse(time.RFC3339, lastSeenAt.String)
		srv.LastSeenAt = &t
	}

	return srv, nil
}

func (s *serverStore) List(ctx context.Context) ([]Server, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, hostname, ip_address, os, arch, agent_version, labels, status, last_seen_at, created_at, updated_at
		 FROM servers ORDER BY hostname ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("querying servers: %w", err)
	}
	defer rows.Close()

	result := make([]Server, 0)
	for rows.Next() {
		var srv Server
		var labelsStr string
		var createdAt, updatedAt string
		var lastSeenAt sql.NullString

		if err := rows.Scan(
			&srv.ID, &srv.Hostname, &srv.IPAddress, &srv.OS, &srv.Arch,
			&srv.AgentVersion, &labelsStr, &srv.Status, &lastSeenAt,
			&createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning server: %w", err)
		}

		if labelsStr != "" {
			json.Unmarshal([]byte(labelsStr), &srv.Labels)
		}
		srv.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		srv.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		if lastSeenAt.Valid {
			t, _ := time.Parse(time.RFC3339, lastSeenAt.String)
			srv.LastSeenAt = &t
		}

		result = append(result, srv)
	}

	return result, rows.Err()
}

func (s *serverStore) UpdateHeartbeat(ctx context.Context, id uuid.UUID) error {
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := s.db.ExecContext(ctx,
		`UPDATE servers SET status = 'online', last_seen_at = ?, updated_at = ? WHERE id = ?`,
		now, now, id.String(),
	)
	if err != nil {
		return fmt.Errorf("updating heartbeat: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *serverStore) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM servers WHERE id = ?`, id.String())
	if err != nil {
		return fmt.Errorf("deleting server: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *serverStore) MarkStaleOffline(ctx context.Context, threshold time.Duration) (int, error) {
	cutoff := time.Now().UTC().Add(-threshold).Format(time.RFC3339)
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := s.db.ExecContext(ctx,
		`UPDATE servers SET status = 'offline', updated_at = ?
		 WHERE status = 'online' AND last_seen_at < ?`,
		now, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("marking stale servers offline: %w", err)
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}
