package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/adham90/opentrace/pkg/store"
)

// serverStore implements ServerStore using database/sql (SQLite).
type serverStore struct {
	db *sql.DB
	q  *Queries
}

// NewServerStore creates a new ServerStore backed by SQLite.
func NewServerStore(db *sql.DB) store.ServerStore {
	return &serverStore{db: db, q: New(db)}
}

// Register uses a transaction for upsert-on-hostname logic — kept hand-written.
func (s *serverStore) Register(ctx context.Context, params store.RegisterServerParams) (*store.Server, error) {
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)

	labelsJSON := "{}"
	if params.Labels != nil {
		b, err := json.Marshal(params.Labels)
		if err != nil {
			return nil, fmt.Errorf("marshaling labels: %w", err)
		}
		labelsJSON = string(b)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	var existingID string
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM servers WHERE hostname = ?`, params.Hostname,
	).Scan(&existingID)

	var serverID uuid.UUID

	if err == nil {
		serverID, _ = uuid.Parse(existingID)
		_, err := tx.ExecContext(ctx,
			`UPDATE servers SET ip_address = ?, os = ?, arch = ?, agent_version = ?,
			 labels = ?, status = 'online', last_seen_at = ?, updated_at = ?
			 WHERE id = ?`,
			params.IPAddress, params.OS, params.Arch, params.AgentVersion,
			labelsJSON, nowStr, nowStr, existingID,
		)
		if err != nil {
			return nil, fmt.Errorf("updating server: %w", err)
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		serverID = uuid.New()
		_, err = tx.ExecContext(ctx,
			`INSERT INTO servers (id, hostname, display_name, ip_address, os, arch, agent_version, labels, status, last_seen_at, created_at, updated_at)
			 VALUES (?, ?, '', ?, ?, ?, ?, ?, 'online', ?, ?, ?)`,
			serverID.String(), params.Hostname, params.IPAddress, params.OS, params.Arch,
			params.AgentVersion, labelsJSON, nowStr, nowStr, nowStr,
		)
		if err != nil {
			return nil, fmt.Errorf("inserting server: %w", err)
		}
	} else {
		return nil, fmt.Errorf("checking existing server: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return &store.Server{
		ID:           serverID,
		Hostname:     params.Hostname,
		IPAddress:    params.IPAddress,
		OS:           params.OS,
		Arch:         params.Arch,
		AgentVersion: params.AgentVersion,
		Labels:       params.Labels,
		Status:       store.ServerOnline,
		LastSeenAt:   &now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

func (s *serverStore) GetByID(ctx context.Context, id uuid.UUID) (*store.Server, error) {
	row, err := s.q.GetServerByID(ctx, id.String())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("querying server: %w", err)
	}
	srv := toStoreServerFromRow(row)
	return &srv, nil
}

func (s *serverStore) List(ctx context.Context, params store.ListServerParams) ([]store.Server, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.q.ListServers(ctx, ListServersParams{
		RowOffset: int64(params.Offset),
		RowLimit:  int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("querying servers: %w", err)
	}
	return toStoreServers(rows), nil
}

func (s *serverStore) Update(ctx context.Context, id uuid.UUID, params store.UpdateServerParams) (*store.Server, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	if params.DisplayName != nil {
		err := s.q.UpdateServerDisplayName(ctx, UpdateServerDisplayNameParams{
			DisplayName: nullString(*params.DisplayName),
			UpdatedAt:   now,
			ID:          id.String(),
		})
		if err != nil {
			return nil, fmt.Errorf("updating server: %w", err)
		}
	}

	return s.GetByID(ctx, id)
}

func (s *serverStore) UpdateHeartbeat(ctx context.Context, id uuid.UUID) error {
	now := time.Now().UTC().Format(time.RFC3339)
	n, err := s.q.UpdateServerHeartbeat(ctx, UpdateServerHeartbeatParams{
		LastSeenAt: nullString(now),
		UpdatedAt:  now,
		ID:         id.String(),
	})
	if err != nil {
		return fmt.Errorf("updating heartbeat: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *serverStore) Delete(ctx context.Context, id uuid.UUID) error {
	n, err := s.q.DeleteServer(ctx, id.String())
	if err != nil {
		return fmt.Errorf("deleting server: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *serverStore) MarkStaleOffline(ctx context.Context, threshold time.Duration) (int, error) {
	cutoff := time.Now().UTC().Add(-threshold).Format(time.RFC3339)
	now := time.Now().UTC().Format(time.RFC3339)
	n, err := s.q.MarkStaleServersOffline(ctx, MarkStaleServersOfflineParams{
		UpdatedAt: now,
		Cutoff:    nullString(cutoff),
	})
	if err != nil {
		return 0, fmt.Errorf("marking stale servers offline: %w", err)
	}
	return int(n), nil
}
