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

func TestOllama_ChatCompletion_Success(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}

		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{
				"role":    "assistant",
				"content": "Hello from Ollama!",
			},
		})
	}))
	defer srv.Close()

	p := NewOllamaProvider(srv.URL, "llama3.2", "nomic-embed-text", 768)
	resp, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "Hi"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Content != "Hello from Ollama!" {
		t.Errorf("got content %q, want %q", resp.Content, "Hello from Ollama!")
	}

	// Verify request body
	if gotBody["model"] != "llama3.2" {
		t.Errorf("got model %v, want %q", gotBody["model"], "llama3.2")
	}
	if gotBody["stream"] != false {
		t.Errorf("got stream %v, want false", gotBody["stream"])
	}
	msgs, ok := gotBody["messages"].([]any)
	if !ok || len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %v", gotBody["messages"])
	}
	// format should not be set when JSONMode is false
	if _, exists := gotBody["format"]; exists {
		t.Errorf("format should not be set when JSONMode is false")
	}
}

func TestOllama_ChatCompletion_JSONMode(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{
				"role":    "assistant",
				"content": `{"answer":"yes"}`,
			},
		})
	}))
	defer srv.Close()

	p := NewOllamaProvider(srv.URL, "llama3.2", "nomic-embed-text", 768)
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

	// Verify format:"json" is sent
	if gotBody["format"] != "json" {
		t.Errorf("got format %v, want %q", gotBody["format"], "json")
	}
}

func TestOllama_ChatCompletion_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := NewOllamaProvider(srv.URL, "llama3.2", "nomic-embed-text", 768)
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "Hi"},
		},
	})
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
}

func TestOllama_ChatCompletion_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate a slow response
		time.Sleep(2 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{"content": "too late"},
		})
	}))
	defer srv.Close()

	p := NewOllamaProvider(srv.URL, "llama3.2", "nomic-embed-text", 768)

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

func TestOllama_Embed_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		var gotBody map[string]any
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		if gotBody["model"] != "nomic-embed-text" {
			t.Errorf("got model %v, want %q", gotBody["model"], "nomic-embed-text")
		}
		if gotBody["input"] != "hello world" {
			t.Errorf("got input %v, want %q", gotBody["input"], "hello world")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"embeddings": [][]float64{{0.1, 0.2, 0.3}},
		})
	}))
	defer srv.Close()

	p := NewOllamaProvider(srv.URL, "llama3.2", "nomic-embed-text", 768)
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

func TestOllama_Embed_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not found", http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := NewOllamaProvider(srv.URL, "llama3.2", "nomic-embed-text", 768)
	_, err := p.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
}

func TestOllama_Dimension(t *testing.T) {
	p := NewOllamaProvider("http://localhost:11434", "llama3.2", "nomic-embed-text", 768)
	if d := p.Dimension(); d != 768 {
		t.Errorf("got dimension %d, want 768", d)
	}

	p2 := NewOllamaProvider("http://localhost:11434", "llama3.2", "mxbai-embed-large", 1024)
	if d := p2.Dimension(); d != 1024 {
		t.Errorf("got dimension %d, want 1024", d)
	}
}
