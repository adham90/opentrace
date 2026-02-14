package web

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/llm"
	"github.com/adham90/opentrace/internal/store"
)

func (s *Server) handleGetRetention(w http.ResponseWriter, r *http.Request) {
	settings, err := s.settingsStore.GetRetention(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get retention settings")
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleUpdateRetention(w http.ResponseWriter, r *http.Request) {
	var req store.RetentionSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.RetentionDays < 0 {
		writeError(w, http.StatusBadRequest, "retention_days must be >= 0")
		return
	}
	if err := s.settingsStore.SetRetention(r.Context(), req); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save retention settings")
		return
	}
	writeJSON(w, http.StatusOK, req)
}

func (s *Server) handleGetAPIKey(w http.ResponseWriter, r *http.Request) {
	envOverride := s.cfg != nil && s.cfg.APIKey != ""
	apiKey := s.getEffectiveAPIKey(r.Context())

	writeJSON(w, http.StatusOK, map[string]any{
		"api_key":      apiKey,
		"env_override": envOverride,
	})
}

func (s *Server) handleRegenerateAPIKey(w http.ResponseWriter, r *http.Request) {
	// Block regeneration if env var override is active
	if s.cfg != nil && s.cfg.APIKey != "" {
		writeError(w, http.StatusConflict, "API key is set via OPENTRACE_API_KEY environment variable and cannot be regenerated from the UI")
		return
	}

	key, err := generateAPIKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate API key")
		return
	}

	if s.settingsStore == nil {
		writeError(w, http.StatusInternalServerError, "settings store not configured")
		return
	}

	if err := s.settingsStore.SetAPIKey(r.Context(), key); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store API key")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"api_key": key})
}

func (s *Server) handleGetAutoUpdate(w http.ResponseWriter, r *http.Request) {
	if s.settingsStore == nil {
		writeJSON(w, http.StatusOK, map[string]bool{"enabled": false})
		return
	}
	enabled, err := s.settingsStore.GetAutoUpdate(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get auto-update setting")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": enabled})
}

func (s *Server) handleSetAutoUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if s.settingsStore == nil {
		writeError(w, http.StatusInternalServerError, "settings store not configured")
		return
	}
	if err := s.settingsStore.SetAutoUpdate(r.Context(), req.Enabled); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save auto-update setting")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": req.Enabled})
}

// maskAPIKey returns a masked version of an API key for safe display.
// Shows the first 8 characters followed by "****", or empty if key is empty.
func maskAPIKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return key[:1] + "****"
	}
	return key[:8] + "****"
}

// isMaskedValue returns true if the value looks like a masked API key
// that was returned by the GET endpoint (user didn't change it).
func isMaskedValue(val string) bool {
	return strings.HasSuffix(val, "****")
}

func (s *Server) handleGetLLMSettings(w http.ResponseWriter, r *http.Request) {
	if s.settingsStore == nil {
		writeError(w, http.StatusInternalServerError, "settings store not configured")
		return
	}

	dbSettings, err := s.settingsStore.GetLLMSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get LLM settings")
		return
	}

	// Build response from effective config (env vars merged with DB overrides)
	resp := map[string]any{
		"default_provider":  s.cfg.LLMProvider,
		"default_model":     "",
		"anthropic_api_key": maskAPIKey(s.cfg.AnthropicAPIKey),
		"anthropic_url":     s.cfg.AnthropicURL,
		"openai_api_key":    maskAPIKey(s.cfg.OpenAIAPIKey),
		"openai_url":        s.cfg.OpenAIURL,
		"gemini_api_key":    maskAPIKey(s.cfg.GeminiAPIKey),
		"gemini_url":        s.cfg.GeminiURL,
		"ollama_url":        s.cfg.OllamaURL,
		"ollama_model":      s.cfg.OllamaModel,
		"env_overrides": map[string]bool{
			"anthropic_api_key": os.Getenv("OPENTRACE_ANTHROPIC_API_KEY") != "",
			"openai_api_key":    os.Getenv("OPENTRACE_OPENAI_API_KEY") != "",
			"gemini_api_key":    os.Getenv("OPENTRACE_GEMINI_API_KEY") != "",
		},
	}

	if dbSettings != nil && dbSettings.DefaultModel != "" {
		resp["default_model"] = dbSettings.DefaultModel
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleUpdateLLMSettings(w http.ResponseWriter, r *http.Request) {
	if s.settingsStore == nil {
		writeError(w, http.StatusInternalServerError, "settings store not configured")
		return
	}

	var req struct {
		DefaultProvider string `json:"default_provider"`
		DefaultModel    string `json:"default_model"`
		AnthropicAPIKey string `json:"anthropic_api_key"`
		AnthropicURL    string `json:"anthropic_url"`
		OpenAIAPIKey    string `json:"openai_api_key"`
		OpenAIURL       string `json:"openai_url"`
		GeminiAPIKey    string `json:"gemini_api_key"`
		GeminiURL       string `json:"gemini_url"`
		OllamaURL       string `json:"ollama_url"`
		OllamaModel     string `json:"ollama_model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// Load existing DB settings to merge (don't wipe fields the user didn't touch)
	existing, err := s.settingsStore.GetLLMSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load existing LLM settings")
		return
	}
	if existing == nil {
		existing = &store.LLMSettings{}
	}

	// Merge: only update fields the user actually changed.
	// Masked values (ending in "****") mean the user didn't change the key.
	if req.DefaultProvider != "" {
		existing.DefaultProvider = req.DefaultProvider
	}
	if req.DefaultModel != "" {
		existing.DefaultModel = req.DefaultModel
	}
	if req.AnthropicAPIKey != "" && !isMaskedValue(req.AnthropicAPIKey) {
		existing.AnthropicAPIKey = req.AnthropicAPIKey
	}
	if req.AnthropicURL != "" {
		existing.AnthropicURL = req.AnthropicURL
	}
	if req.OpenAIAPIKey != "" && !isMaskedValue(req.OpenAIAPIKey) {
		existing.OpenAIAPIKey = req.OpenAIAPIKey
	}
	if req.OpenAIURL != "" {
		existing.OpenAIURL = req.OpenAIURL
	}
	if req.GeminiAPIKey != "" && !isMaskedValue(req.GeminiAPIKey) {
		existing.GeminiAPIKey = req.GeminiAPIKey
	}
	if req.GeminiURL != "" {
		existing.GeminiURL = req.GeminiURL
	}
	if req.OllamaURL != "" {
		existing.OllamaURL = req.OllamaURL
	}
	if req.OllamaModel != "" {
		existing.OllamaModel = req.OllamaModel
	}

	// Persist to DB
	if err := s.settingsStore.SetLLMSettings(r.Context(), *existing); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save LLM settings")
		return
	}

	// Hot-reload: apply overrides to the live config
	s.cfg.ApplyLLMOverrides(config.LLMOverrides{
		DefaultProvider: existing.DefaultProvider,
		AnthropicAPIKey: existing.AnthropicAPIKey,
		AnthropicURL:    existing.AnthropicURL,
		OpenAIAPIKey:    existing.OpenAIAPIKey,
		OpenAIURL:       existing.OpenAIURL,
		GeminiAPIKey:    existing.GeminiAPIKey,
		GeminiURL:       existing.GeminiURL,
		OllamaURL:       existing.OllamaURL,
		OllamaModel:     existing.OllamaModel,
	})

	// Reload provider cache and invalidate model registry
	if s.providerCache != nil {
		if err := s.providerCache.Reload(s.cfg); err != nil {
			slog.Warn("failed to reload LLM provider cache", "error", err)
		}
	}
	if s.modelRegistry != nil {
		s.modelRegistry.UpdateConfig(s.cfg)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (s *Server) handleTestLLMConnection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider string `json:"provider"`
		APIKey   string `json:"api_key"`
		BaseURL  string `json:"base_url"`
		Model    string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if req.Provider == "" {
		writeError(w, http.StatusBadRequest, "provider is required")
		return
	}

	// If the API key is masked, use the current effective key from config
	apiKey := req.APIKey
	if isMaskedValue(apiKey) || apiKey == "" {
		switch req.Provider {
		case "anthropic":
			apiKey = s.cfg.AnthropicAPIKey
		case "openai":
			apiKey = s.cfg.OpenAIAPIKey
		case "gemini":
			apiKey = s.cfg.GeminiAPIKey
		}
	}

	baseURL := req.BaseURL
	if baseURL == "" {
		switch req.Provider {
		case "anthropic":
			baseURL = s.cfg.AnthropicURL
		case "openai":
			baseURL = s.cfg.OpenAIURL
		case "gemini":
			baseURL = s.cfg.GeminiURL
		case "ollama":
			baseURL = s.cfg.OllamaURL
		}
	}

	model := req.Model
	if model == "" {
		switch req.Provider {
		case "anthropic":
			model = s.cfg.AnthropicModel
		case "openai":
			model = s.cfg.OpenAIModel
		case "gemini":
			model = s.cfg.GeminiModel
		case "ollama":
			model = s.cfg.OllamaModel
		}
	}

	// Create a temporary provider to test the connection
	var provider llm.LLMProvider
	switch req.Provider {
	case "ollama":
		provider = llm.NewOllamaProvider(baseURL, model)
	case "anthropic":
		if apiKey == "" {
			writeJSON(w, http.StatusOK, map[string]any{"success": false, "message": "API key is required"})
			return
		}
		provider = llm.NewAnthropicProvider(baseURL, model, apiKey)
	case "openai":
		if apiKey == "" {
			writeJSON(w, http.StatusOK, map[string]any{"success": false, "message": "API key is required"})
			return
		}
		provider = llm.NewOpenAIProvider(baseURL, model, apiKey)
	case "gemini":
		if apiKey == "" {
			writeJSON(w, http.StatusOK, map[string]any{"success": false, "message": "API key is required"})
			return
		}
		provider = llm.NewGeminiProvider(baseURL, model, apiKey)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "message": fmt.Sprintf("unknown provider: %s", req.Provider)})
		return
	}

	// Send a minimal test prompt with a short timeout
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	_, err := provider.ChatCompletion(ctx, llm.ChatRequest{
		Messages:  []llm.ChatMessage{{Role: "user", Content: "Say OK"}},
		MaxTokens: 16,
	})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": fmt.Sprintf("Connection failed: %v", err),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": fmt.Sprintf("Connected to %s. Model %s is available.", req.Provider, model),
	})
}

func (s *Server) handleSelfUpdate(w http.ResponseWriter, r *http.Request) {
	if isDocker() {
		writeError(w, http.StatusConflict, "self-update is not available in Docker; use docker compose pull")
		return
	}

	result, err := s.selfUpdater.Update(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)

	// Schedule a restart after the response is sent.
	go func() {
		// Small delay so the HTTP response gets flushed to the client.
		select {
		case <-s.restartCh:
			// Already closing
		default:
			close(s.restartCh)
		}
	}()
}
