package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/adham90/opentrace/pkg/store"
)

type sessionStore struct {
	db *sql.DB
}

func NewSessionStore(db *sql.DB) store.SessionStore {
	return &sessionStore{db: db}
}

func (s *sessionStore) Create(ctx context.Context, userID string, token string, expiresAt time.Time) (*store.Session, error) {
	id := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, token, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		id, userID, token, expiresAt.UTC().Format(time.RFC3339), now,
	)
	if err != nil {
		return nil, fmt.Errorf("creating session: %w", err)
	}

	return &store.Session{
		ID:        id,
		UserID:    userID,
		Token:     token,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now().UTC(),
	}, nil
}

func (s *sessionStore) GetByToken(ctx context.Context, token string) (*store.Session, error) {
	sess := &store.Session{}
	var expiresAt, createdAt string

	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, token, expires_at, created_at
		 FROM sessions WHERE token = ? AND expires_at > ?`,
		token, time.Now().UTC().Format(time.RFC3339),
	).Scan(&sess.ID, &sess.UserID, &sess.Token, &expiresAt, &createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("querying session: %w", err)
	}

	sess.ExpiresAt = parseTime(expiresAt)
	sess.CreatedAt = parseTime(createdAt)
	return sess, nil
}

func (s *sessionStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting session: %w", err)
	}
	return nil
}

func (s *sessionStore) DeleteExpired(ctx context.Context) (int, error) {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at <= ?`,
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return 0, fmt.Errorf("deleting expired sessions: %w", err)
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}

func (s *sessionStore) DeleteAllForUser(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	if err != nil {
		return fmt.Errorf("deleting user sessions: %w", err)
	}
	return nil
}
