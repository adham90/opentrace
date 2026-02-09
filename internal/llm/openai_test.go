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

func TestOpenAI_ChatCompletion_Success(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Authorization = %q, want %q", r.Header.Get("Authorization"), "Bearer test-key")
		}

		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "Hello from OpenAI!"}},
			},
		})
	}))
	defer srv.Close()

	p := NewOpenAIProvider(srv.URL, "gpt-4o", "text-embedding-3-small", 1536, "test-key")
	resp, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{
			{Role: "system", Content: "You are helpful."},
			{Role: "user", Content: "Hi"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Content != "Hello from OpenAI!" {
		t.Errorf("got content %q, want %q", resp.Content, "Hello from OpenAI!")
	}

	if gotBody["model"] != "gpt-4o" {
		t.Errorf("got model %v, want %q", gotBody["model"], "gpt-4o")
	}

	msgs, ok := gotBody["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %v", gotBody["messages"])
	}

	// response_format should not be set when JSONMode is false
	if _, exists := gotBody["response_format"]; exists {
		t.Errorf("response_format should not be set when JSONMode is false")
	}
}

func TestOpenAI_ChatCompletion_JSONMode(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": `{"answer":"yes"}`}},
			},
		})
	}))
	defer srv.Close()

	p := NewOpenAIProvider(srv.URL, "gpt-4o", "text-embedding-3-small", 1536, "test-key")
	resp, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "Answer in JSON"},
		},
		JSONMode: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Content != `{"answer":"yes"}` {
		t.Errorf("got content %q, want %q", resp.Content, `{"answer":"yes"}`)
	}

	// Verify response_format is set
	rf, ok := gotBody["response_format"].(map[string]any)
	if !ok {
		t.Fatal("expected response_format to be set")
	}
	if rf["type"] != "json_object" {
		t.Errorf("response_format.type = %v, want %q", rf["type"], "json_object")
	}
}

func TestOpenAI_ChatCompletion_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := NewOpenAIProvider(srv.URL, "gpt-4o", "text-embedding-3-small", 1536, "test-key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "Hi"},
		},
	})
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
}

func TestOpenAI_ChatCompletion_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "too late"}},
			},
		})
	}))
	defer srv.Close()

	p := NewOpenAIProvider(srv.URL, "gpt-4o", "text-embedding-3-small", 1536, "test-key")

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

func TestOpenAI_Embed_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Authorization = %q, want %q", r.Header.Get("Authorization"), "Bearer test-key")
		}

		var gotBody map[string]any
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		if gotBody["model"] != "text-embedding-3-small" {
			t.Errorf("got model %v, want %q", gotBody["model"], "text-embedding-3-small")
		}
		if gotBody["input"] != "hello world" {
			t.Errorf("got input %v, want %q", gotBody["input"], "hello world")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"embedding": []float64{0.1, 0.2, 0.3}},
			},
		})
	}))
	defer srv.Close()

	p := NewOpenAIProvider(srv.URL, "gpt-4o", "text-embedding-3-small", 1536, "test-key")
	vec, err := p.Embed(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(vec) != 3 {
		t.Fatalf("expected 3 values, got %d", len(vec))
	}
	if vec[0] != 0.1 || vec[1] != 0.2 || vec[2] != 0.3 {
		t.Errorf("unexpected embedding values: %v", vec)
	}
}

func TestOpenAI_Embed_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not found", http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := NewOpenAIProvider(srv.URL, "gpt-4o", "text-embedding-3-small", 1536, "test-key")
	_, err := p.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
}

func TestOpenAI_Dimension(t *testing.T) {
	p := NewOpenAIProvider("https://api.openai.com", "gpt-4o", "text-embedding-3-small", 1536, "test-key")
	if d := p.Dimension(); d != 1536 {
		t.Errorf("got dimension %d, want 1536", d)
	}

	p2 := NewOpenAIProvider("https://api.openai.com", "gpt-4o", "text-embedding-3-large", 3072, "test-key")
	if d := p2.Dimension(); d != 3072 {
		t.Errorf("got dimension %d, want 3072", d)
	}
}
