// Package apiclient provides an HTTP client for the OpenTrace CLI API endpoints.
package apiclient

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Client talks to a running OpenTrace server's /api/cli/* endpoints.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// New creates a new API client.
func New(endpoint, apiKey string) *Client {
	return &Client{
		baseURL: endpoint,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Status fetches GET /api/cli/status.
func (c *Client) Status() (*StatusResponse, error) {
	var resp StatusResponse
	if err := c.getJSON("/api/cli/status", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetLog fetches GET /api/cli/logs/{id} — single log with request summary.
func (c *Client) GetLog(id int64) (*LogEntry, error) {
	var resp LogEntry
	if err := c.getJSON(fmt.Sprintf("/api/cli/logs/%d", id), nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// LogTail fetches GET /api/cli/logs/tail with the given filters.
// env scopes to a single environment; pass "" to query every env.
func (c *Client) LogTail(after int64, limit int, level, service, search, env string) (*LogTailResponse, error) {
	params := url.Values{}
	if after > 0 {
		params.Set("after", strconv.FormatInt(after, 10))
	}
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}
	if level != "" {
		params.Set("level", level)
	}
	if service != "" {
		params.Set("service", service)
	}
	if search != "" {
		params.Set("search", search)
	}
	if env != "" {
		params.Set("env", env)
	}
	var resp LogTailResponse
	if err := c.getJSON("/api/cli/logs/tail", params, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ErrorsTop fetches GET /api/cli/errors/top.
func (c *Client) ErrorsTop(limit int, since, service string) (*ErrorGroupsResponse, error) {
	params := url.Values{}
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}
	if since != "" {
		params.Set("since", since)
	}
	if service != "" {
		params.Set("service", service)
	}
	var resp ErrorGroupsResponse
	if err := c.getJSON("/api/cli/errors/top", params, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Watches fetches GET /api/cli/watches.
func (c *Client) Watches() (*WatchesResponse, error) {
	var resp WatchesResponse
	if err := c.getJSON("/api/cli/watches", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// IngestionStats fetches GET /api/cli/ingestion/stats.
func (c *Client) IngestionStats(since, bucket, service string) (*IngestionStatsResponse, error) {
	params := url.Values{}
	if since != "" {
		params.Set("since", since)
	}
	if bucket != "" {
		params.Set("bucket", bucket)
	}
	if service != "" {
		params.Set("service", service)
	}
	var resp IngestionStatsResponse
	if err := c.getJSON("/api/cli/ingestion/stats", params, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// LogStreamURL returns the full URL for the SSE log stream endpoint.
// env scopes to a single environment; pass "" to stream every env.
//
// The endpoint comes from user-editable config (.opentrace.yml,
// OPENTRACE_ENDPOINT, --endpoint), so it can be malformed; an unparseable
// endpoint is reported as an error rather than dereferenced as a nil *url.URL.
func (c *Client) LogStreamURL(level, service, search, env string) (string, error) {
	u, err := url.Parse(c.baseURL + "/api/cli/logs/stream")
	if err != nil {
		return "", fmt.Errorf("invalid endpoint %q: %w", c.baseURL, err)
	}
	params := url.Values{}
	if level != "" {
		params.Set("level", level)
	}
	if service != "" {
		params.Set("service", service)
	}
	if search != "" {
		params.Set("search", search)
	}
	if env != "" {
		params.Set("env", env)
	}
	if len(params) > 0 {
		u.RawQuery = params.Encode()
	}
	return u.String(), nil
}

// APIKey returns the configured API key for SSE auth headers.
func (c *Client) APIKey() string {
	return c.apiKey
}

// getJSON performs a GET request and decodes the JSON response.
func (c *Client) getJSON(path string, params url.Values, out any) error {
	u := c.baseURL + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}

	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}
