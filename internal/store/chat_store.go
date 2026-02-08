package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgChatStore implements ChatStore using pgx.
type PgChatStore struct {
	pool *pgxpool.Pool
}

// NewPgChatStore creates a new PgChatStore.
func NewPgChatStore(pool *pgxpool.Pool) *PgChatStore {
	return &PgChatStore{pool: pool}
}

func (s *PgChatStore) CreateChat(ctx context.Context, title string) (*Chat, error) {
	chat := &Chat{}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO chats (title) VALUES ($1)
		 RETURNING id, title, created_at, updated_at`, title).
		Scan(&chat.ID, &chat.Title, &chat.CreatedAt, &chat.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("creating chat: %w", err)
	}
	return chat, nil
}

func (s *PgChatStore) GetChat(ctx context.Context, id uuid.UUID) (*Chat, error) {
	chat := &Chat{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, title, created_at, updated_at FROM chats WHERE id = $1`, id).
		Scan(&chat.ID, &chat.Title, &chat.CreatedAt, &chat.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("getting chat: %w", err)
	}
	return chat, nil
}

func (s *PgChatStore) ListChats(ctx context.Context) ([]Chat, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, title, created_at, updated_at FROM chats ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing chats: %w", err)
	}
	defer rows.Close()

	var chats []Chat
	for rows.Next() {
		var c Chat
		if err := rows.Scan(&c.ID, &c.Title, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning chat: %w", err)
		}
		chats = append(chats, c)
	}
	return chats, rows.Err()
}

func (s *PgChatStore) DeleteChat(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM chats WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting chat: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgChatStore) UpdateChatTitle(ctx context.Context, id uuid.UUID, title string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE chats SET title = $1, updated_at = now() WHERE id = $2`, title, id)
	if err != nil {
		return fmt.Errorf("updating chat title: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgChatStore) AddMessage(ctx context.Context, msg Message) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO messages (id, chat_id, role, content, tool_name)
		 VALUES ($1, $2, $3, $4, $5)`,
		msg.ID, msg.ChatID, msg.Role, msg.Content, nilIfEmpty(msg.ToolName))
	if err != nil {
		return fmt.Errorf("adding message: %w", err)
	}
	// Touch chat updated_at
	_, err = s.pool.Exec(ctx,
		`UPDATE chats SET updated_at = now() WHERE id = $1`, msg.ChatID)
	if err != nil {
		return fmt.Errorf("updating chat timestamp: %w", err)
	}
	return nil
}

func (s *PgChatStore) GetMessages(ctx context.Context, chatID uuid.UUID) ([]Message, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, chat_id, role, content, COALESCE(tool_name, ''), created_at
		 FROM messages WHERE chat_id = $1 ORDER BY created_at ASC`, chatID)
	if err != nil {
		return nil, fmt.Errorf("getting messages: %w", err)
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ChatID, &m.Role, &m.Content, &m.ToolName, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning message: %w", err)
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
