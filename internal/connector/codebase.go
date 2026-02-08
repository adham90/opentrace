package connector

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/opentrace/opentrace/internal/agent"
	"github.com/opentrace/opentrace/internal/llm"
	"github.com/opentrace/opentrace/internal/store"
)

const chunkSize = 2000

// codeExtensions lists file extensions to include during indexing.
var codeExtensions = map[string]bool{
	".go": true, ".py": true, ".js": true, ".ts": true, ".tsx": true, ".jsx": true,
	".java": true, ".c": true, ".cpp": true, ".h": true, ".hpp": true,
	".rs": true, ".rb": true, ".php": true, ".swift": true, ".kt": true,
	".scala": true, ".sh": true, ".bash": true, ".sql": true,
	".yaml": true, ".yml": true, ".toml": true, ".json": true,
	".html": true, ".css": true, ".md": true, ".txt": true,
	".proto": true, ".graphql": true, ".tf": true,
	".Dockerfile": true, ".Makefile": true,
}

// skipDirs lists directories to skip during indexing.
var skipDirs = map[string]bool{
	".git": true, "vendor": true, "node_modules": true,
	"__pycache__": true, ".venv": true, "dist": true, "build": true,
	".next": true, ".nuxt": true, "target": true,
}

const maxFileSize = 1 << 20 // 1MB

// CodebaseConnector implements DataSource for semantic code search.
type CodebaseConnector struct {
	repoPath string
	embStore store.EmbeddingStore
	embedder llm.EmbeddingProvider
}

// NewCodebaseConnector creates a connector that indexes the given repo path synchronously.
func NewCodebaseConnector(ctx context.Context, repoPath string, embStore store.EmbeddingStore, embedder llm.EmbeddingProvider) (*CodebaseConnector, error) {
	c := &CodebaseConnector{
		repoPath: repoPath,
		embStore: embStore,
		embedder: embedder,
	}

	if err := c.indexRepo(ctx); err != nil {
		return nil, fmt.Errorf("indexing repo: %w", err)
	}

	return c, nil
}

func (c *CodebaseConnector) Type() ConnectorType { return ConnectorCodebase }

func (c *CodebaseConnector) TestConnection(ctx context.Context) error {
	info, err := os.Stat(c.repoPath)
	if err != nil {
		return fmt.Errorf("repo path: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("repo path is not a directory: %s", c.repoPath)
	}
	return nil
}

func (c *CodebaseConnector) Tools() []agent.Tool {
	return []agent.Tool{
		{
			Name:        "code_search",
			Description: "Search the indexed codebase using semantic similarity. Provide a natural language query about the code.",
			Params: []agent.ToolParam{
				{Name: "query", Type: "string", Required: true},
				{Name: "limit", Type: "int", Required: false},
			},
			Handler: c.handleCodeSearch,
		},
	}
}

func (c *CodebaseConnector) Close() error { return nil }

func (c *CodebaseConnector) handleCodeSearch(ctx context.Context, args map[string]any) (string, error) {
	query, ok := args["query"].(string)
	if !ok || query == "" {
		return "", fmt.Errorf("query parameter is required")
	}

	limit := 5
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}

	embedding, err := c.embedder.Embed(ctx, query)
	if err != nil {
		return "", fmt.Errorf("embedding query: %w", err)
	}

	results, err := c.embStore.Search(ctx, embedding, limit)
	if err != nil {
		return "", fmt.Errorf("searching embeddings: %w", err)
	}

	if len(results) == 0 {
		return "No matching code found.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d code matches:\n\n", len(results)))
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("--- %s (chunk %d, similarity: %.3f) ---\n", r.FilePath, r.ChunkIndex, r.Similarity))
		sb.WriteString(r.Content)
		if i < len(results)-1 {
			sb.WriteString("\n\n")
		}
	}

	return sb.String(), nil
}

func (c *CodebaseConnector) indexRepo(ctx context.Context) error {
	// Clear existing embeddings
	if err := c.embStore.DeleteAll(ctx); err != nil {
		return fmt.Errorf("clearing existing embeddings: %w", err)
	}

	var allChunks []store.CodeChunk

	err := filepath.WalkDir(c.repoPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip errors
		}

		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		// Check extension
		ext := filepath.Ext(d.Name())
		if !codeExtensions[ext] && !codeExtensions["."+d.Name()] {
			return nil
		}

		// Check file size
		info, err := d.Info()
		if err != nil || info.Size() > maxFileSize || info.Size() == 0 {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		relPath, err := filepath.Rel(c.repoPath, path)
		if err != nil {
			relPath = path
		}

		chunks := ChunkFile(relPath, string(content))
		for i := range chunks {
			embedding, err := c.embedder.Embed(ctx, chunks[i].Content)
			if err != nil {
				return fmt.Errorf("embedding %s chunk %d: %w", relPath, i, err)
			}
			chunks[i].Embedding = embedding
		}

		allChunks = append(allChunks, chunks...)
		return nil
	})
	if err != nil {
		return err
	}

	if len(allChunks) > 0 {
		return c.embStore.UpsertChunks(ctx, allChunks)
	}

	return nil
}

// ChunkFile splits file content into chunks of approximately chunkSize characters,
// splitting at line boundaries.
func ChunkFile(filePath, content string) []store.CodeChunk {
	if content == "" {
		return nil
	}

	lines := strings.Split(content, "\n")
	var chunks []store.CodeChunk
	var current strings.Builder
	chunkIndex := 0

	for _, line := range lines {
		if current.Len()+len(line)+1 > chunkSize && current.Len() > 0 {
			chunks = append(chunks, store.CodeChunk{
				FilePath:   filePath,
				ChunkIndex: chunkIndex,
				Content:    current.String(),
			})
			chunkIndex++
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteString("\n")
		}
		current.WriteString(line)
	}

	if current.Len() > 0 {
		chunks = append(chunks, store.CodeChunk{
			FilePath:   filePath,
			ChunkIndex: chunkIndex,
			Content:    current.String(),
		})
	}

	return chunks
}
