package vmagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

// Agent is the main VM metrics agent that collects and pushes metrics.
type Agent struct {
	cfg       *Config
	collector *Collector
	client    *http.Client
	serverID  string
}

// New creates a new Agent.
func New(cfg *Config) *Agent {
	return &Agent{
		cfg:       cfg,
		collector: NewCollector(),
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Run starts the agent loop: register, then collect+push on interval.
// Blocks until ctx is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	slog.Info("agent starting", "server", a.cfg.ServerURL, "interval", a.cfg.Interval)

	// Register with server
	if err := a.register(ctx); err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}
	slog.Info("registered", "server_id", a.serverID)

	// Collect + push loop
	ticker := time.NewTicker(a.cfg.Interval)
	defer ticker.Stop()

	// Do an immediate collection
	a.collectAndPush(ctx)

	for {
		select {
		case <-ctx.Done():
			slog.Info("agent shutting down")
			return nil
		case <-ticker.C:
			a.collectAndPush(ctx)
		}
	}
}

func (a *Agent) register(ctx context.Context) error {
	hostInfo, err := GetHostInfo()
	if err != nil {
		return fmt.Errorf("getting host info: %w", err)
	}

	params := store.RegisterServerParams{
		Hostname:     hostInfo.Hostname,
		OS:           hostInfo.OS,
		Arch:         hostInfo.Arch,
		AgentVersion: "0.1.0",
		Labels:       a.cfg.Labels,
	}

	body, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshaling registration: %w", err)
	}

	url := strings.TrimRight(a.cfg.ServerURL, "/") + "/api/servers/register"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if a.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+a.cfg.APIKey)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("registration returned status %d", resp.StatusCode)
	}

	var result struct {
		ServerID string `json:"server_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decoding registration response: %w", err)
	}
	if result.ServerID == "" {
		return fmt.Errorf("server_id missing from registration response")
	}

	a.serverID = result.ServerID
	return nil
}

type metricPushBody struct {
	Timestamp time.Time            `json:"timestamp"`
	Samples   []store.MetricSample `json:"samples"`
}

func (a *Agent) collectAndPush(ctx context.Context) {
	samples, err := a.collector.Collect(ctx)
	if err != nil {
		slog.Warn("collection error", "error", err)
		return
	}

	if len(samples) == 0 {
		return
	}

	payload := metricPushBody{
		Timestamp: time.Now().UTC(),
		Samples:   samples,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("marshal error", "error", err)
		return
	}

	url := strings.TrimRight(a.cfg.ServerURL, "/") + "/api/servers/" + a.serverID + "/metrics"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		slog.Warn("request error", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if a.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+a.cfg.APIKey)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		slog.Warn("push error", "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		slog.Warn("push returned unexpected status", "status", resp.StatusCode)
		return
	}

	slog.Debug("pushed metrics", "count", len(samples))
}
