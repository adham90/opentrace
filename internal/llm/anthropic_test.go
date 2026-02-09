package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAnthropic_ChatCompletion_Success(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("x-api-key = %q, want %q", r.Header.Get("x-api-key"), "test-key")
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("anthropic-version = %q, want %q", r.Header.Get("anthropic-version"), "2023-06-01")
		}

		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "Hello from Anthropic!"},
			},
		})
	}))
	defer srv.Close()

	p := NewAnthropicProvider(srv.URL, "claude-sonnet-4-20250514", "test-key")
	resp, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "Hi"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Content != "Hello from Anthropic!" {
		t.Errorf("got content %q, want %q", resp.Content, "Hello from Anthropic!")
	}

	if gotBody["model"] != "claude-sonnet-4-20250514" {
		t.Errorf("got model %v, want %q", gotBody["model"], "claude-sonnet-4-20250514")
	}

	msgs, ok := gotBody["messages"].([]any)
	if !ok || len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %v", gotBody["messages"])
	}
}

func TestAnthropic_ChatCompletion_SystemExtraction(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "OK"},
			},
		})
	}))
	defer srv.Close()

	p := NewAnthropicProvider(srv.URL, "claude-sonnet-4-20250514", "test-key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{
			{Role: "system", Content: "You are a helper."},
			{Role: "system", Content: "Be concise."},
			{Role: "user", Content: "Hi"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// System messages should be extracted to top-level system field
	system, ok := gotBody["system"].(string)
	if !ok || system == "" {
		t.Fatal("expected system field to be set")
	}
	if system != "You are a helper.\n\nBe concise." {
		t.Errorf("system = %q, want concatenated system messages", system)
	}

	// Messages should only contain user message
	msgs, ok := gotBody["messages"].([]any)
	if !ok || len(msgs) != 1 {
		t.Fatalf("expected 1 message (no system), got %v", gotBody["messages"])
	}
}

func TestAnthropic_ChatCompletion_MessageMerging(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "OK"},
			},
		})
	}))
	defer srv.Close()

	p := NewAnthropicProvider(srv.URL, "claude-sonnet-4-20250514", "test-key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "first"},
			{Role: "user", Content: "second"},
			{Role: "assistant", Content: "response"},
			{Role: "user", Content: "third"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Adjacent user messages should be merged
	msgs, ok := gotBody["messages"].([]any)
	if !ok {
		t.Fatal("expected messages array")
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages after merging, got %d", len(msgs))
	}

	// First message should be merged user messages
	firstMsg := msgs[0].(map[string]any)
	if firstMsg["content"] != "first\n\nsecond" {
		t.Errorf("first message content = %q, want merged", firstMsg["content"])
	}
}

func TestAnthropic_ChatCompletion_JSONMode(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": `{"answer":"yes"}`},
			},
		})
	}))
	defer srv.Close()

	p := NewAnthropicProvider(srv.URL, "claude-sonnet-4-20250514", "test-key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{
			{Role: "system", Content: "You are a helper."},
			{Role: "user", Content: "Answer in JSON"},
		},
		JSONMode: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// System prompt should contain JSON reinforcement
	system := gotBody["system"].(string)
	if system == "" {
		t.Fatal("expected system to be set")
	}
	expected := "You are a helper.\n\nIMPORTANT: You must respond with valid JSON only. No markdown, no explanation, just JSON."
	if system != expected {
		t.Errorf("system = %q, want %q", system, expected)
	}
}

func TestAnthropic_ChatCompletion_MaxTokensDefault(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "OK"},
			},
		})
	}))
	defer srv.Close()

	p := NewAnthropicProvider(srv.URL, "claude-sonnet-4-20250514", "test-key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "Hi"},
		},
		// MaxTokens not set — should default to 4096
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	maxTokens := gotBody["max_tokens"].(float64)
	if maxTokens != 4096 {
		t.Errorf("max_tokens = %v, want 4096", maxTokens)
	}
}

func TestAnthropic_ChatCompletion_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := NewAnthropicProvider(srv.URL, "claude-sonnet-4-20250514", "test-key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "Hi"},
		},
	})
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
}

func TestAnthropic_ChatCompletion_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "too late"},
			},
		})
	}))
	defer srv.Close()

	p := NewAnthropicProvider(srv.URL, "claude-sonnet-4-20250514", "test-key")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := p.ChatCompletion(ctx, ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "Hi"},
		},
	})
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}

func TestAnthropic_ChatCompletion_EmptyContentFiltered(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "OK"},
			},
		})
	}))
	defer srv.Close()

	p := NewAnthropicProvider(srv.URL, "claude-sonnet-4-20250514", "test-key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{
			{Role: "system", Content: "You are a helper."},
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: ""},
			{Role: "assistant", Content: ""},
			{Role: "user", Content: "What happened?"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Empty-content assistant messages should be filtered out
	msgs, ok := gotBody["messages"].([]any)
	if !ok {
		t.Fatal("expected messages array")
	}
	// Should be: user("Hello"), assistant merged... wait, empty ones removed, so:
	// user("Hello"), user("What happened?") -> merged to user("Hello\n\nWhat happened?")
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message after filtering+merging, got %d: %v", len(msgs), msgs)
	}
	firstMsg := msgs[0].(map[string]any)
	if firstMsg["role"] != "user" {
		t.Errorf("expected user role, got %q", firstMsg["role"])
	}
}
