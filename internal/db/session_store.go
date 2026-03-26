package db

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
	q  *Queries
}

func NewSessionStore(db *sql.DB) store.SessionStore {
	return &sessionStore{db: db, q: New(db)}
}

func (s *sessionStore) Create(ctx context.Context, userID string, token string, expiresAt time.Time) (*store.Session, error) {
	id := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)

	row, err := s.q.CreateSession(ctx, CreateSessionParams{
		ID:        id,
		UserID:    userID,
		Token:     token,
		ExpiresAt: expiresAt.UTC().Format(time.RFC3339),
		CreatedAt: now,
	})
	if err != nil {
		return nil, fmt.Errorf("creating session: %w", err)
	}
	return toStoreSession(row), nil
}

func (s *sessionStore) GetByToken(ctx context.Context, token string) (*store.Session, error) {
	row, err := s.q.GetSessionByToken(ctx, GetSessionByTokenParams{
		Token: token,
		Now:   time.Now().UTC().Format(time.RFC3339),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying session: %w", err)
	}
	return toStoreSession(row), nil
}

func (s *sessionStore) Delete(ctx context.Context, id string) error {
	err := s.q.DeleteSession(ctx, id)
	if err != nil {
		return fmt.Errorf("deleting session: %w", err)
	}
	return nil
}

func (s *sessionStore) DeleteExpired(ctx context.Context) (int, error) {
	n, err := s.q.DeleteExpiredSessions(ctx, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("deleting expired sessions: %w", err)
	}
	return int(n), nil
}

func (s *sessionStore) DeleteAllForUser(ctx context.Context, userID string) error {
	err := s.q.DeleteAllSessionsForUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("deleting user sessions: %w", err)
	}
	return nil
}
