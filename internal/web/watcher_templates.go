package web

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/adham90/opentrace/internal/store"
	"github.com/adham90/opentrace/internal/watcher"
)

type previewRequest struct {
	WatcherType  store.WatcherType `json:"watcher_type"`
	RuleConfig   *store.RuleConfig `json:"rule_config"`
	DataSourceID *string           `json:"data_source_id,omitempty"`
}

type previewResponse struct {
	CurrentValue *float64 `json:"current_value,omitempty"`
	WouldAlert   bool     `json:"would_alert"`
	Summary      string   `json:"summary"`
	QueryTimeMS  int64    `json:"query_time_ms"`
}

func (s *Server) handleWatcherPreview(w http.ResponseWriter, r *http.Request) {
	var req previewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.WatcherType != store.WatcherTypeRule {
		writeError(w, http.StatusBadRequest, "preview is only supported for rule watchers")
		return
	}
	if req.RuleConfig == nil {
		writeError(w, http.StatusBadRequest, "rule_config is required")
		return
	}

	if s.ruleEvaluator == nil {
		writeError(w, http.StatusServiceUnavailable, "rule evaluator not available")
		return
	}

	// Build a temporary watcher from the request
	tempWatcher := store.Watcher{
		WatcherType: store.WatcherTypeRule,
		RuleConfig:  req.RuleConfig,
	}
	if req.DataSourceID != nil {
		tempWatcher.DataSourceID = req.DataSourceID
	}

	start := time.Now()
	result, err := s.ruleEvaluator.Evaluate(r.Context(), tempWatcher)
	elapsed := time.Since(start).Milliseconds()

	if err != nil {
		writeJSON(w, http.StatusOK, previewResponse{
			WouldAlert:  false,
			Summary:     "Error: " + err.Error(),
			QueryTimeMS: elapsed,
		})
		return
	}

	writeJSON(w, http.StatusOK, previewResponse{
		CurrentValue: result.Value,
		WouldAlert:   result.HasAlert,
		Summary:      result.Summary,
		QueryTimeMS:  elapsed,
	})
}

// WatcherTemplate represents a pre-built watcher configuration.
type WatcherTemplate struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Category    string            `json:"category"`
	WatcherType store.WatcherType `json:"watcher_type"`
	RuleConfig  store.RuleConfig  `json:"rule_config"`
	Severity    string            `json:"severity"`
	TimeRange   string            `json:"time_range"`
}

func (s *Server) handleWatcherTemplates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, watcher.BuiltinTemplates())
}
