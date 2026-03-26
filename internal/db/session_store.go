package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/adham90/opentrace/pkg/store"
)

type sessionStore struct {
	db *bun.DB
}

func NewSessionStore(db *bun.DB) store.SessionStore {
	return &sessionStore{db: db}
}

func (s *sessionStore) Create(ctx context.Context, userID string, token string, expiresAt time.Time) (*store.Session, error) {
	now := time.Now().UTC()
	sess := &store.Session{
		ID:        uuid.New().String(),
		UserID:    userID,
		Token:     token,
		ExpiresAt: expiresAt.UTC(),
		CreatedAt: now,
	}
	_, err := s.db.NewInsert().Model(sess).Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating session: %w", err)
	}
	return sess, nil
}

func (s *sessionStore) GetByToken(ctx context.Context, token string) (*store.Session, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	var sess store.Session
	err := s.db.NewSelect().Model(&sess).
		Where("token = ?", token).
		Where("expires_at > ?", now).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying session: %w", err)
	}
	return &sess, nil
}

func (s *sessionStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.NewDelete().Model((*store.Session)(nil)).
		Where("id = ?", id).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("deleting session: %w", err)
	}
	return nil
}

func (s *sessionStore) DeleteExpired(ctx context.Context) (int, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.NewDelete().Model((*store.Session)(nil)).
		Where("expires_at <= ?", now).
		Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("deleting expired sessions: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *sessionStore) DeleteAllForUser(ctx context.Context, userID string) error {
	_, err := s.db.NewDelete().Model((*store.Session)(nil)).
		Where("user_id = ?", userID).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("deleting user sessions: %w", err)
	}
	return nil
}
