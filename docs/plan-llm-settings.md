# Plan: LLM API Keys & Default Model Settings in Dashboard

## Goal

Allow users to configure LLM provider API keys and select the default model from the Settings page in the dashboard, instead of requiring environment variables. Env vars still work as a fallback.

---

## Current State

- **Config source**: All LLM keys loaded from env vars only (`OPENTRACE_ANTHROPIC_API_KEY`, etc.) in `internal/config/config.go`
- **Settings store**: `app_config` table (key-value) already exists — stores retention, ingestion API key, auto-update
- **Settings UI**: 4 tabs (general, users, setup, profile) — no LLM config
- **Model registry**: `internal/llm/models.go` — already queries provider APIs dynamically:
  - Ollama: `GET {url}/api/tags` → lists locally installed models
  - OpenAI: `GET {url}/v1/models` → filtered by `openAIChatModelPrefixes` (excludes embedding/image)
  - Gemini: `GET {url}/v1beta/models` → filtered by `supportsGenerateContent`
  - Anthropic: no list API → hardcoded `AnthropicModels` always used
  - Results cached 5 minutes, falls back to hardcoded `ModelVariant` lists when APIs unreachable
- **Model listing API**: `GET /api/models` already exists (`handleListModels` in `watchers.go`) — used by watcher form to populate model dropdown. **We will reuse this for the AI settings tab.**
- **Provider cache**: `internal/llm/factory.go` — lazy-creates providers per model name
- **Hardcoded fallback model lists are outdated** — missing latest models from all providers

---

## Phase 1: Update Hardcoded Model Lists

Update `internal/llm/factory.go` with the latest text-generation models (no embedding/image/audio models).

### Anthropic Models (current → updated)

```go
var AnthropicModels = []ModelVariant{
    {Name: "anthropic-opus-4-6",  ModelID: "claude-opus-4-6",                Label: "Claude Opus 4.6"},
    {Name: "anthropic-sonnet-4-5", ModelID: "claude-sonnet-4-5-20250929",    Label: "Claude Sonnet 4.5"},
    {Name: "anthropic-haiku-4-5",  ModelID: "claude-haiku-4-5-20251001",     Label: "Claude Haiku 4.5"},
}
```

### OpenAI Models (current → updated)

```go
var OpenAIModels = []ModelVariant{
    {Name: "openai-o3",          ModelID: "o3",            Label: "o3"},
    {Name: "openai-o4-mini",     ModelID: "o4-mini",       Label: "o4-mini"},
    {Name: "openai-gpt4.1",     ModelID: "gpt-4.1",       Label: "GPT-4.1"},
    {Name: "openai-gpt4.1-mini", ModelID: "gpt-4.1-mini", Label: "GPT-4.1 mini"},
    {Name: "openai-gpt4.1-nano", ModelID: "gpt-4.1-nano", Label: "GPT-4.1 nano"},
    {Name: "openai-gpt4o",      ModelID: "gpt-4o",        Label: "GPT-4o"},
    {Name: "openai-gpt4o-mini",  ModelID: "gpt-4o-mini",  Label: "GPT-4o mini"},
}
```

Also update `openAIChatModelPrefixes` in `models.go` to include `"gpt-4.1"` for dynamic model discovery filtering.

### Gemini Models (current → updated)

```go
var GeminiModels = []ModelVariant{
    {Name: "gemini-3-pro",       ModelID: "gemini-3-pro-preview",       Label: "Gemini 3 Pro"},
    {Name: "gemini-3-flash",     ModelID: "gemini-3-flash-preview",     Label: "Gemini 3 Flash"},
    {Name: "gemini-2.5-pro",     ModelID: "gemini-2.5-pro",             Label: "Gemini 2.5 Pro"},
    {Name: "gemini-2.5-flash",   ModelID: "gemini-2.5-flash",           Label: "Gemini 2.5 Flash"},
    {Name: "gemini-2.5-flash-lite", ModelID: "gemini-2.5-flash-lite",   Label: "Gemini 2.5 Flash-Lite"},
}
```

### Update Default Models in `config.go`

```
AnthropicModel default: "claude-sonnet-4-5-20250929" (keep — still latest Sonnet)
OpenAIModel    default: "gpt-4.1-mini"               (was "gpt-4o")
GeminiModel    default: "gemini-2.5-flash"            (was "gemini-2.5-flash-preview-04-17")
```

---

## Phase 2: Database Storage for LLM Settings

### 2a. Define the LLM Settings struct

File: `internal/store/store.go`

```go
// LLMSettings holds LLM provider configuration stored in app_config.
type LLMSettings struct {
    DefaultProvider string `json:"default_provider"`       // "ollama", "anthropic", "openai", "gemini"
    DefaultModel    string `json:"default_model"`          // model name key, e.g. "anthropic-sonnet-4-5"

    AnthropicAPIKey string `json:"anthropic_api_key,omitempty"`
    AnthropicURL    string `json:"anthropic_url,omitempty"`
    OpenAIAPIKey    string `json:"openai_api_key,omitempty"`
    OpenAIURL       string `json:"openai_url,omitempty"`
    GeminiAPIKey    string `json:"gemini_api_key,omitempty"`
    GeminiURL       string `json:"gemini_url,omitempty"`
    OllamaURL       string `json:"ollama_url,omitempty"`
    OllamaModel     string `json:"ollama_model,omitempty"`
}
```

### 2b. Extend SettingsStore interface

File: `internal/store/store.go`

```go
type SettingsStore interface {
    // ... existing methods ...
    GetLLMSettings(ctx context.Context) (*LLMSettings, error)
    SetLLMSettings(ctx context.Context, settings LLMSettings) error
}
```

### 2c. Implement in `settings_store.go`

- Store as JSON under `app_config` key `"llm_settings"`
- `GetLLMSettings` returns `nil` (empty) when no row exists → falls back to env vars
- `SetLLMSettings` upserts the JSON blob

### 2d. Update all mock implementations

Update mock `SettingsStore` in:
- `internal/web/mock_test.go`
- `internal/mcp/server_test.go`
- Any other test files with mock settings stores

---

## Phase 3: Config Resolution — DB Overrides Env Vars

### 3a. Add `ApplyLLMOverrides` to Config

File: `internal/config/config.go`

```go
// ApplyLLMOverrides merges database LLM settings over environment defaults.
// DB values take precedence; empty DB fields fall back to the env-var value.
func (c *Config) ApplyLLMOverrides(s *store.LLMSettings) {
    if s == nil { return }
    if s.DefaultProvider != "" { c.LLMProvider = s.DefaultProvider }
    if s.AnthropicAPIKey != "" { c.AnthropicAPIKey = s.AnthropicAPIKey }
    if s.AnthropicURL != ""    { c.AnthropicURL = s.AnthropicURL }
    if s.OpenAIAPIKey != ""    { c.OpenAIAPIKey = s.OpenAIAPIKey }
    if s.OpenAIURL != ""       { c.OpenAIURL = s.OpenAIURL }
    if s.GeminiAPIKey != ""    { c.GeminiAPIKey = s.GeminiAPIKey }
    if s.GeminiURL != ""       { c.GeminiURL = s.GeminiURL }
    if s.OllamaURL != ""       { c.OllamaURL = s.OllamaURL }
    if s.OllamaModel != ""     { c.OllamaModel = s.OllamaModel }
    // DefaultModel is handled by the caller (sets the model on the provider)
}
```

### 3b. Call from `main.go` at startup

After loading env config and opening the DB, call `ApplyLLMOverrides` before creating the LLM provider and model registry.

---

## Phase 4: Hot-Reload on Settings Save

### 4a. Add `Reload` method to `ProviderCache`

```go
// Reload clears the provider cache and re-initializes the default provider
// with the current config values. Call after LLM settings change.
func (pc *ProviderCache) Reload(cfg *config.Config) error {
    pc.mu.Lock()
    defer pc.mu.Unlock()
    defaultLLM, err := NewLLMProvider(cfg)
    if err != nil {
        return err
    }
    pc.cfg = cfg
    pc.defaultLLM = defaultLLM
    pc.cache = make(map[string]LLMProvider) // clear cached providers
    return nil
}
```

### 4b. Add `InvalidateCache` to `ModelRegistry`

```go
func (mr *ModelRegistry) InvalidateCache() {
    mr.mu.Lock()
    defer mr.mu.Unlock()
    mr.cache = nil
    mr.lastFetch = time.Time{}
}

func (mr *ModelRegistry) UpdateConfig(cfg *config.Config) {
    mr.mu.Lock()
    defer mr.mu.Unlock()
    mr.cfg = cfg
    mr.cache = nil
    mr.lastFetch = time.Time{}
}
```

### 4c. Wire in the save handler

When the user saves LLM settings via the API:
1. Persist to DB (`SetLLMSettings`)
2. `cfg.ApplyLLMOverrides(settings)` — mutate the shared config
3. `providerCache.Reload(cfg)` — recreate default provider + clear cache
4. `modelRegistry.UpdateConfig(cfg)` — next `ListModels()` call will refetch

The `Server` struct already holds `cfg`, `modelRegistry`, and we'll need to add `providerCache` to `ServerDeps` (or pass it through the executor).

---

## Phase 5: API Endpoints

File: `internal/web/settings.go` (add new handlers)

### `GET /api/settings/llm`

Returns the current LLM settings. **API keys are masked** — only show first 8 chars + `****`.

```json
{
  "default_provider": "anthropic",
  "default_model": "anthropic-sonnet-4-5",
  "anthropic_api_key": "sk-ant-a****",
  "anthropic_url": "https://api.anthropic.com",
  "openai_api_key": "",
  "openai_url": "https://api.openai.com",
  "gemini_api_key": "",
  "gemini_url": "https://generativelanguage.googleapis.com",
  "ollama_url": "http://localhost:11434",
  "ollama_model": "llama3.2",
  "env_overrides": {
    "anthropic_api_key": true,
    "openai_api_key": false,
    "gemini_api_key": false
  }
}
```

The `env_overrides` map tells the UI which keys were set via env vars (so it can show a notice like the existing API key env override).

### `PUT /api/settings/llm`

Accepts full or partial LLM settings. Persists to DB, triggers hot-reload.

Request body:
```json
{
  "default_provider": "anthropic",
  "default_model": "anthropic-sonnet-4-5",
  "anthropic_api_key": "sk-ant-api03-...",
  "anthropic_url": "https://api.anthropic.com"
}
```

Rules:
- Empty string for a key field = clear it (fall back to env var)
- If the value is `"****"` or matches the masked pattern = don't update (user didn't change it)
- Admin-only endpoint (existing auth middleware handles this)

### `POST /api/settings/llm/test`

Tests a provider connection by making a lightweight API call.

Request body:
```json
{
  "provider": "anthropic",
  "api_key": "sk-ant-...",
  "base_url": "https://api.anthropic.com",
  "model": "claude-sonnet-4-5-20250929"
}
```

Response:
```json
{
  "success": true,
  "message": "Connected to Anthropic. Model claude-sonnet-4-5-20250929 is available."
}
```

Implementation: Create a temporary provider instance, send a minimal prompt like "Say OK", check for a valid response. Timeout: 15 seconds.

### Route registration

File: `internal/web/server.go` — add to the admin API routes:

```go
r.Get("/api/settings/llm", s.handleGetLLMSettings)
r.Put("/api/settings/llm", s.handleUpdateLLMSettings)
r.Post("/api/settings/llm/test", s.handleTestLLMConnection)
```

---

## Phase 6: Settings UI — New "AI" Tab

File: `internal/web/templates/settings.html`

### 6a. Add tab button

```html
<button class="settings-tab" data-tab="ai" onclick="switchTab('ai', this)">ai</button>
```

Add `'ai'` to the `validTabs` array in the JS.

### 6b. Tab panel layout

```
┌─────────────────────────────────────────────────────┐
│ # Default Provider & Model                          │
│                                                     │
│ Provider: [dropdown: ollama / anthropic / openai /   │
│            gemini]                                   │
│ Model:    [dropdown: populated based on provider]    │
│                                                     │
│ [Save Default]                                      │
├─────────────────────────────────────────────────────┤
│ # Anthropic                                         │
│                                                     │
│ API Key: [sk-ant-**** ___________] [Test] [Save]    │
│ Base URL: [https://api.anthropic.com]               │
│ ⚠ Key set via OPENTRACE_ANTHROPIC_API_KEY env var   │
│   (shown only when env override is active)          │
├─────────────────────────────────────────────────────┤
│ # OpenAI                                            │
│                                                     │
│ API Key: [______________________] [Test] [Save]     │
│ Base URL: [https://api.openai.com]                  │
├─────────────────────────────────────────────────────┤
│ # Google Gemini                                     │
│                                                     │
│ API Key: [______________________] [Test] [Save]     │
│ Base URL: [https://generativelanguage.googleapis...]│
├─────────────────────────────────────────────────────┤
│ # Ollama                                            │
│                                                     │
│ URL: [http://localhost:11434]                        │
│ Default Model: [llama3.2]                           │
│ [Test Connection]                                   │
└─────────────────────────────────────────────────────┘
```

### 6c. JavaScript behavior

1. On page load: `GET /api/settings/llm` → populate all fields (keys masked)
2. Provider dropdown change → filter model dropdown to that provider's models
3. Model dropdown: populated from `GET /api/models` (existing `handleListModels` endpoint in `watchers.go`).
   This already calls `ModelRegistry.ListModels()` which dynamically queries each provider's API
   (OpenAI `/v1/models`, Gemini `/v1beta/models`, Ollama `/api/tags`) and filters for chat-capable
   models only. Anthropic uses the hardcoded fallback list since they have no list-models API.
   The UI filters the returned list client-side by provider prefix (e.g. `openai:*`, `gemini:*`, `anthropic-*`).
4. "Test" button per provider: `POST /api/settings/llm/test`
5. "Save" button: `PUT /api/settings/llm` → show success/error toast → then re-fetch `/api/models`
   to refresh the model dropdown (the save handler invalidates the ModelRegistry cache, so the
   next `/api/models` call will re-query provider APIs with the new keys)
6. Env override notices: shown when `env_overrides.{provider}_api_key` is true
7. When user types in a key field, the masked value is cleared on focus

---

## Phase 7: Pass ProviderCache to Server

### 7a. Add to `ServerDeps`

```go
type ServerDeps struct {
    // ... existing fields ...
    ProviderCache *llm.ProviderCache
}
```

### 7b. Store on Server struct

```go
type Server struct {
    // ... existing fields ...
    providerCache *llm.ProviderCache
}
```

### 7c. Wire in `NewServerWithDeps`

Set `srv.providerCache = deps.ProviderCache` alongside the existing `modelRegistry` assignment.

---

## File Change Summary

| File | Change |
|------|--------|
| `internal/llm/factory.go` | Update `AnthropicModels`, `OpenAIModels`, `GeminiModels` model lists |
| `internal/llm/models.go` | Update `openAIChatModelPrefixes`, add `Reload`/`InvalidateCache` methods |
| `internal/config/config.go` | Update default models, add `ApplyLLMOverrides` method |
| `internal/store/store.go` | Add `LLMSettings` struct, extend `SettingsStore` interface |
| `internal/store/settings_store.go` | Implement `GetLLMSettings` / `SetLLMSettings` |
| `internal/web/settings.go` | Add `handleGetLLMSettings`, `handleUpdateLLMSettings`, `handleTestLLMConnection` |
| `internal/web/server.go` | Register new routes, add `providerCache` to Server/ServerDeps |
| `internal/web/templates/settings.html` | Add "ai" tab with full LLM config UI |
| `cmd/opentrace/main.go` | Call `ApplyLLMOverrides` at startup, pass `ProviderCache` to `ServerDeps` |
| `internal/web/mock_test.go` | Add mock `GetLLMSettings`/`SetLLMSettings` |
| `internal/mcp/server_test.go` | Update mock if it implements `SettingsStore` |

---

## Security Notes

- API keys are **never returned in full** from `GET /api/settings/llm` — always masked
- Keys are stored as plaintext JSON in SQLite `app_config` (same pattern as the existing ingestion API key)
- All LLM settings endpoints are **admin-only** (protected by existing auth middleware)
- The `PUT` endpoint ignores masked-value submissions to prevent accidental overwrites
- If encryption-at-rest is desired later, it can be added to the store layer without changing the interface

---

## Testing Plan

1. **Unit tests for `settings_store.go`**: `GetLLMSettings` (empty, populated), `SetLLMSettings` (insert, update)
2. **Unit tests for `ApplyLLMOverrides`**: env-only, DB-only, DB-overrides-env, partial DB
3. **Unit tests for `ProviderCache.Reload`**: clears cache, updates default
4. **Handler tests**: GET returns masked keys, PUT saves and reloads, test endpoint validates connection
5. **Manual E2E**: Configure Anthropic key in UI → create an AI watcher → verify it uses the key
