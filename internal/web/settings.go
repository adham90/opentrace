package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

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
	if req.MetricRetentionDays < 0 {
		writeError(w, http.StatusBadRequest, "metric_retention_days must be >= 0")
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

func (s *Server) handleGetCORSOrigins(w http.ResponseWriter, r *http.Request) {
	envOverride := s.cfg != nil && len(s.cfg.CORSAllowedOrigins) > 0

	var origins string
	if envOverride {
		origins = strings.Join(s.cfg.CORSAllowedOrigins, ",")
	} else if s.settingsStore != nil {
		val, err := s.settingsStore.GetCORSOrigins(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to get CORS origins")
			return
		}
		origins = val
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"cors_origins": origins,
		"env_override": envOverride,
	})
}

func (s *Server) handleUpdateCORSOrigins(w http.ResponseWriter, r *http.Request) {
	if s.cfg != nil && len(s.cfg.CORSAllowedOrigins) > 0 {
		writeError(w, http.StatusConflict, "CORS origins are set via OPENTRACE_CORS_ORIGINS environment variable and cannot be changed from the UI")
		return
	}

	var req struct {
		CORSOrigins string `json:"cors_origins"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if s.settingsStore == nil {
		writeError(w, http.StatusInternalServerError, "settings store not configured")
		return
	}

	if err := s.settingsStore.SetCORSOrigins(r.Context(), req.CORSOrigins); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save CORS origins")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"cors_origins": req.CORSOrigins})
}

func (s *Server) handleGetQueryGuardrails(w http.ResponseWriter, r *http.Request) {
	envOverride := s.cfg != nil && (os.Getenv("OPENTRACE_MAX_QUERY_ROWS") != "" || os.Getenv("OPENTRACE_STATEMENT_TIMEOUT_MS") != "")

	maxRows := 500
	stmtTimeout := 5000

	if envOverride {
		if s.cfg != nil {
			maxRows = s.cfg.MaxQueryRows
			stmtTimeout = s.cfg.StatementTimeoutMS
		}
	} else if s.settingsStore != nil {
		if v, err := s.settingsStore.GetMaxQueryRows(r.Context()); err == nil && v > 0 {
			maxRows = v
		}
		if v, err := s.settingsStore.GetStatementTimeout(r.Context()); err == nil && v > 0 {
			stmtTimeout = v
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"max_query_rows":       maxRows,
		"statement_timeout_ms": stmtTimeout,
		"env_override":         envOverride,
	})
}

func (s *Server) handleUpdateQueryGuardrails(w http.ResponseWriter, r *http.Request) {
	if s.cfg != nil && (os.Getenv("OPENTRACE_MAX_QUERY_ROWS") != "" || os.Getenv("OPENTRACE_STATEMENT_TIMEOUT_MS") != "") {
		writeError(w, http.StatusConflict, "Query guardrails are set via environment variables and cannot be changed from the UI")
		return
	}

	var req struct {
		MaxQueryRows       int `json:"max_query_rows"`
		StatementTimeoutMS int `json:"statement_timeout_ms"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.MaxQueryRows < 1 {
		writeError(w, http.StatusBadRequest, "max_query_rows must be >= 1")
		return
	}
	if req.StatementTimeoutMS < 100 {
		writeError(w, http.StatusBadRequest, "statement_timeout_ms must be >= 100")
		return
	}

	if s.settingsStore == nil {
		writeError(w, http.StatusInternalServerError, "settings store not configured")
		return
	}

	if err := s.settingsStore.SetMaxQueryRows(r.Context(), req.MaxQueryRows); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save max_query_rows")
		return
	}
	if err := s.settingsStore.SetStatementTimeout(r.Context(), req.StatementTimeoutMS); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save statement_timeout_ms")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"max_query_rows":       req.MaxQueryRows,
		"statement_timeout_ms": req.StatementTimeoutMS,
	})
}

func (s *Server) handleGetMCPName(w http.ResponseWriter, r *http.Request) {
	envOverride := os.Getenv("OPENTRACE_MCP_NAME") != ""

	name := "opentrace"
	if envOverride {
		name = os.Getenv("OPENTRACE_MCP_NAME")
	} else if s.settingsStore != nil {
		if v, err := s.settingsStore.GetMCPName(r.Context()); err == nil && v != "" {
			name = v
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"mcp_name":     name,
		"env_override": envOverride,
	})
}

func (s *Server) handleUpdateMCPName(w http.ResponseWriter, r *http.Request) {
	if os.Getenv("OPENTRACE_MCP_NAME") != "" {
		writeError(w, http.StatusConflict, "MCP name is set via OPENTRACE_MCP_NAME environment variable and cannot be changed from the UI")
		return
	}

	var req struct {
		MCPName string `json:"mcp_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if s.settingsStore == nil {
		writeError(w, http.StatusInternalServerError, "settings store not configured")
		return
	}

	if err := s.settingsStore.SetMCPName(r.Context(), req.MCPName); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save MCP name")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"mcp_name": req.MCPName})
}

func (s *Server) handleGetSamplingRules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.settingsStore.GetSamplingRules(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get sampling rules")
		return
	}
	if rules == nil {
		rules = []store.SamplingRule{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sampling_rules": rules})
}

func (s *Server) handleUpdateSamplingRules(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Rules []store.SamplingRule `json:"sampling_rules"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// Validate
	for i, rule := range body.Rules {
		if rule.Rate < 0 || rule.Rate > 1 {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("rule %d: rate must be between 0 and 1", i))
			return
		}
	}
	if err := s.settingsStore.SetSamplingRules(r.Context(), body.Rules); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save sampling rules")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sampling_rules": body.Rules})
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

