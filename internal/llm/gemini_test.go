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

func TestGemini_ChatCompletion_Success(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}
		// Verify API key is in query param
		if r.URL.Query().Get("key") != "test-gemini-key" {
			t.Errorf("key = %q, want %q", r.URL.Query().Get("key"), "test-gemini-key")
		}
		// Verify path contains model name
		wantPath := "/v1beta/models/gemini-2.5-flash-preview-04-17:generateContent"
		if r.URL.Path != wantPath {
			t.Errorf("path = %q, want %q", r.URL.Path, wantPath)
		}

		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{
					"content": map[string]any{
						"role":  "model",
						"parts": []map[string]any{{"text": "Hello from Gemini!"}},
					},
					"finishReason": "STOP",
				},
			},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-2.5-flash-preview-04-17", "test-gemini-key")
	resp, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{
			{Role: "system", Content: "You are helpful."},
			{Role: "user", Content: "Hi"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Content != "Hello from Gemini!" {
		t.Errorf("got content %q, want %q", resp.Content, "Hello from Gemini!")
	}
	if resp.StopReason != "STOP" {
		t.Errorf("got stop reason %q, want %q", resp.StopReason, "STOP")
	}

	// Verify system instruction was extracted
	si, ok := gotBody["systemInstruction"].(map[string]any)
	if !ok {
		t.Fatal("expected systemInstruction in request")
	}
	siParts, _ := si["parts"].([]any)
	if len(siParts) == 0 {
		t.Fatal("expected parts in systemInstruction")
	}
	firstPart, _ := siParts[0].(map[string]any)
	if firstPart["text"] != "You are helpful." {
		t.Errorf("system text = %v, want %q", firstPart["text"], "You are helpful.")
	}

	// Verify contents has user message only (system extracted)
	contents, ok := gotBody["contents"].([]any)
	if !ok || len(contents) != 1 {
		t.Fatalf("expected 1 content entry, got %v", gotBody["contents"])
	}
	content0, _ := contents[0].(map[string]any)
	if content0["role"] != "user" {
		t.Errorf("content role = %v, want %q", content0["role"], "user")
	}
}

func TestGemini_ChatCompletion_ToolCalls(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{
					"content": map[string]any{
						"role": "model",
						"parts": []map[string]any{
							{
								"functionCall": map[string]any{
									"name": "run_query",
									"args": map[string]any{"sql": "SELECT 1"},
								},
							},
						},
					},
					"finishReason": "STOP",
				},
			},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-2.5-flash-preview-04-17", "test-key")
	resp, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "Run a query"},
		},
		Tools: []ToolDef{
			{
				Name:        "run_query",
				Description: "Run a SQL query",
				Parameters: []ToolParamDef{
					{Name: "sql", Type: "string", Required: true},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.Name != "run_query" {
		t.Errorf("tool name = %q, want %q", tc.Name, "run_query")
	}
	if tc.Args["sql"] != "SELECT 1" {
		t.Errorf("tool args sql = %v, want %q", tc.Args["sql"], "SELECT 1")
	}

	// Verify tools were sent in the request
	tools, ok := gotBody["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected 1 tool def, got %v", gotBody["tools"])
	}
	toolDef, _ := tools[0].(map[string]any)
	decls, _ := toolDef["functionDeclarations"].([]any)
	if len(decls) != 1 {
		t.Fatalf("expected 1 function declaration, got %d", len(decls))
	}
	decl, _ := decls[0].(map[string]any)
	if decl["name"] != "run_query" {
		t.Errorf("function name = %v, want %q", decl["name"], "run_query")
	}
}

func TestGemini_ChatCompletion_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-2.5-flash-preview-04-17", "test-key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "Hi"},
		},
	})
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
}

func TestGemini_ChatCompletion_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{
					"content": map[string]any{
						"role":  "model",
						"parts": []map[string]any{{"text": "too late"}},
					},
					"finishReason": "STOP",
				},
			},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-2.5-flash-preview-04-17", "test-key")

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

func TestGemini_ChatCompletion_ToolResultRoundtrip(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{
					"content": map[string]any{
						"role":  "model",
						"parts": []map[string]any{{"text": "The result is 42."}},
					},
					"finishReason": "STOP",
				},
			},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-2.5-flash-preview-04-17", "test-key")
	resp, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "Run a query"},
			{Role: "assistant", Content: "", ToolCalls: []ToolCall{
				{ID: "call_1", Name: "run_query", Args: map[string]any{"sql": "SELECT 42"}},
			}},
			{Role: "tool", ToolCallID: "run_query", Content: "42"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Content != "The result is 42." {
		t.Errorf("got content %q, want %q", resp.Content, "The result is 42.")
	}

	// Verify the contents structure: user, model (with functionCall), user (with functionResponse)
	contents, ok := gotBody["contents"].([]any)
	if !ok || len(contents) != 3 {
		t.Fatalf("expected 3 content entries, got %d: %v", len(contents), gotBody["contents"])
	}

	// Second entry should be model role with functionCall
	c1, _ := contents[1].(map[string]any)
	if c1["role"] != "model" {
		t.Errorf("contents[1] role = %v, want %q", c1["role"], "model")
	}

	// Third entry should be user role with functionResponse
	c2, _ := contents[2].(map[string]any)
	if c2["role"] != "user" {
		t.Errorf("contents[2] role = %v, want %q", c2["role"], "user")
	}
}
