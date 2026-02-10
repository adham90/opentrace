package web

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/adham90/opentrace/internal/store"
	"github.com/adham90/opentrace/internal/watcher"
)

type previewRequest struct {
	MonitorType  store.MonitorType `json:"monitor_type"`
	RuleConfig   *store.RuleConfig `json:"rule_config"`
	DataSourceID *string           `json:"data_source_id,omitempty"`
}

type previewResponse struct {
	CurrentValue *float64 `json:"current_value,omitempty"`
	WouldAlert   bool     `json:"would_alert"`
	Summary      string   `json:"summary"`
	QueryTimeMS  int64    `json:"query_time_ms"`
}

func (s *Server) handleMonitorPreview(w http.ResponseWriter, r *http.Request) {
	var req previewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.MonitorType != store.MonitorTypeRule {
		writeError(w, http.StatusBadRequest, "preview is only supported for rule monitors")
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
		MonitorType: store.MonitorTypeRule,
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

// MonitorTemplate represents a pre-built monitor configuration.
type MonitorTemplate struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Category    string            `json:"category"`
	MonitorType store.MonitorType `json:"monitor_type"`
	RuleConfig  store.RuleConfig  `json:"rule_config"`
	Severity    string            `json:"severity"`
	TimeRange   string            `json:"time_range"`
}

func (s *Server) handleMonitorTemplates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, watcher.BuiltinTemplates())
}
