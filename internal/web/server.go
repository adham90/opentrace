package web

import (
	"io/fs"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/opentrace/opentrace/internal/config"
	"github.com/opentrace/opentrace/internal/connector"
	"github.com/opentrace/opentrace/internal/llm"
	"github.com/opentrace/opentrace/internal/store"
)

// Server holds the HTTP server and its dependencies.
type Server struct {
	Router       chi.Router
	dsStore      store.DataSourceStore
	logStore     store.LogStore
	embStore     store.EmbeddingStore
	chatStore    store.ChatStore
	memoryStore  store.MemoryStore
	registry     *connector.Registry
	cfg          *config.Config
	embedder     llm.EmbeddingProvider
	llmProvider  llm.LLMProvider
	llmProviders map[string]llm.LLMProvider
	logsConnMu   sync.Mutex
}

// NewServer creates a new Server with the given dependencies and sets up routes.
func NewServer(dsStore store.DataSourceStore, logStore store.LogStore, embStore store.EmbeddingStore, chatStore store.ChatStore, memoryStore store.MemoryStore, registry *connector.Registry, cfg *config.Config, embedder llm.EmbeddingProvider, llmProviders map[string]llm.LLMProvider, defaultProvider string) *Server {
	srv := &Server{
		dsStore:      dsStore,
		logStore:     logStore,
		embStore:     embStore,
		chatStore:    chatStore,
		memoryStore:  memoryStore,
		registry:     registry,
		cfg:          cfg,
		embedder:     embedder,
		llmProviders: llmProviders,
	}

	// Set default LLM provider from the map.
	// Map legacy names ("anthropic", "openai") to their default variants.
	if defaultProvider != "" && llmProviders != nil {
		srv.llmProvider = llmProviders[defaultProvider]
		if srv.llmProvider == nil {
			legacyMap := map[string]string{
				"anthropic": "anthropic-sonnet",
				"openai":    "openai-gpt4o",
			}
			if mapped, ok := legacyMap[defaultProvider]; ok {
				srv.llmProvider = llmProviders[mapped]
			}
		}
	}
	// Fallback: if no provider set from map, try type assertion from embedder
	if srv.llmProvider == nil {
		if lp, ok := embedder.(llm.LLMProvider); ok {
			srv.llmProvider = lp
		}
	}

	router := chi.NewRouter()
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)
	router.Use(middleware.RequestID)

	router.Get("/healthz", srv.handleHealthCheck)

	// Static files
	staticSub, _ := fs.Sub(staticFS, "static")
	router.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	// Pages
	router.Get("/", srv.handleInvestigatePage)
	router.Get("/logs", srv.handleLogsPage)
	router.Get("/connectors", srv.handleConnectorsPage)

	// API
	router.Route("/api", func(r chi.Router) {
		r.Post("/connectors", srv.handleCreateConnectorAPI)
		r.Get("/connectors", srv.handleListConnectors)
		r.Post("/connectors/{id}/test", srv.handleTestConnectorAPI)
		r.Delete("/connectors/{id}", srv.handleDeleteConnectorAPI)

		// Log ingestion with optional API key auth
		apiKey := ""
		if cfg != nil {
			apiKey = cfg.APIKey
		}
		r.With(APIKeyAuth(apiKey)).Post("/logs", srv.handleIngestLogs)

		r.Get("/investigate", srv.handleInvestigateSSE)
		r.Get("/sse/demo", srv.handleSSEDemo)

		// Chat API
		r.Get("/chats", srv.handleListChats)
		r.Get("/chats/{id}", srv.handleGetChat)
		r.Delete("/chats/{id}", srv.handleDeleteChat)

		// Memory API
		r.Get("/memory", srv.handleListMemories)
		r.Delete("/memory/{id}", srv.handleDeleteMemory)

		// Provider API
		r.Get("/providers", srv.handleListProviders)
	})

	srv.Router = router
	return srv
}

func (s *Server) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"status": "ok",
		"llm":    s.llmProvider != nil,
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	if s.cfg == nil {
		writeJSON(w, http.StatusOK, []llm.ProviderInfo{})
		return
	}
	providers := llm.AvailableProviders(s.cfg)
	writeJSON(w, http.StatusOK, providers)
}
