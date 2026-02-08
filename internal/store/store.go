package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a requested resource does not exist.
var ErrNotFound = errors.New("not found")

// DataSourceStore defines CRUD operations for data sources.
type DataSourceStore interface {
	Create(ctx context.Context, params CreateDataSourceParams) (*DataSource, error)
	GetByID(ctx context.Context, id uuid.UUID) (*DataSource, error)
	List(ctx context.Context) ([]DataSource, error)
	Update(ctx context.Context, id uuid.UUID, params UpdateDataSourceParams) (*DataSource, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// LogStore defines operations for log ingestion and search.
type LogStore interface {
	BatchInsert(ctx context.Context, entries []LogEntry) (int, error)
	Search(ctx context.Context, params LogSearchParams) ([]LogEntry, error)
}

// ChatStore defines operations for chat persistence.
type ChatStore interface {
	CreateChat(ctx context.Context, title string) (*Chat, error)
	GetChat(ctx context.Context, id uuid.UUID) (*Chat, error)
	ListChats(ctx context.Context) ([]Chat, error)
	DeleteChat(ctx context.Context, id uuid.UUID) error
	UpdateChatTitle(ctx context.Context, id uuid.UUID, title string) error
	AddMessage(ctx context.Context, msg Message) error
	GetMessages(ctx context.Context, chatID uuid.UUID) ([]Message, error)
}

// EmbeddingStore defines operations for code embedding storage and search.
type EmbeddingStore interface {
	UpsertChunks(ctx context.Context, chunks []CodeChunk) error
	Search(ctx context.Context, embedding []float64, limit int) ([]CodeSearchResult, error)
	DeleteByPath(ctx context.Context, filePath string) error
	DeleteAll(ctx context.Context) error
}
