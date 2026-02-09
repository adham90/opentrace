package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---------- Constructor ----------

func TestNewGeminiProvider(t *testing.T) {
	p := NewGeminiProvider("https://example.com", "gemini-pro", "my-key")
	if p.baseURL != "https://example.com" {
		t.Errorf("baseURL = %q, want %q", p.baseURL, "https://example.com")
	}
	if p.model != "gemini-pro" {
		t.Errorf("model = %q, want %q", p.model, "gemini-pro")
	}
	if p.apiKey != "my-key" {
		t.Errorf("apiKey = %q, want %q", p.apiKey, "my-key")
	}
	if p.client == nil {
		t.Fatal("client should not be nil")
	}
	if p.client.Timeout != 120*time.Second {
		t.Errorf("client timeout = %v, want %v", p.client.Timeout, 120*time.Second)
	}
}

// ---------- Basic success ----------

func TestGemini_ChatCompletion_Success(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want %q", ct, "application/json")
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

// ---------- System instruction handling ----------

func TestGemini_ChatCompletion_MultipleSystemMessages(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "OK"}}},
				"finishReason": "STOP",
			}},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "key")
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

	si, ok := gotBody["systemInstruction"].(map[string]any)
	if !ok {
		t.Fatal("expected systemInstruction")
	}
	siParts, _ := si["parts"].([]any)
	part0, _ := siParts[0].(map[string]any)
	if part0["text"] != "You are a helper.\n\nBe concise." {
		t.Errorf("system text = %v, want concatenated messages", part0["text"])
	}

	// Only user message in contents
	contents, _ := gotBody["contents"].([]any)
	if len(contents) != 1 {
		t.Fatalf("expected 1 content entry, got %d", len(contents))
	}
}

func TestGemini_ChatCompletion_EmptySystemMessageSkipped(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "OK"}}},
				"finishReason": "STOP",
			}},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{
			{Role: "system", Content: ""},
			{Role: "user", Content: "Hi"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Empty system message should not create systemInstruction
	if _, ok := gotBody["systemInstruction"]; ok {
		t.Error("expected no systemInstruction for empty system message")
	}
}

func TestGemini_ChatCompletion_NoSystemMessage(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "OK"}}},
				"finishReason": "STOP",
			}},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "Hi"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := gotBody["systemInstruction"]; ok {
		t.Error("expected no systemInstruction when no system messages")
	}
}

// ---------- JSON mode ----------

func TestGemini_ChatCompletion_JSONMode_WithSystemMessage(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": `{"answer":"yes"}`}}},
				"finishReason": "STOP",
			}},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "key")
	resp, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{
			{Role: "system", Content: "You are a helper."},
			{Role: "user", Content: "Answer in JSON"},
		},
		JSONMode: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Content != `{"answer":"yes"}` {
		t.Errorf("got content %q, want JSON", resp.Content)
	}

	si, ok := gotBody["systemInstruction"].(map[string]any)
	if !ok {
		t.Fatal("expected systemInstruction")
	}
	siParts, _ := si["parts"].([]any)
	part0, _ := siParts[0].(map[string]any)
	text := part0["text"].(string)
	want := "You are a helper.\n\nIMPORTANT: You must respond with valid JSON only. No markdown, no explanation, just JSON."
	if text != want {
		t.Errorf("system text = %q, want %q", text, want)
	}
}

func TestGemini_ChatCompletion_JSONMode_WithoutSystemMessage(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": `{"x":1}`}}},
				"finishReason": "STOP",
			}},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "JSON please"},
		},
		JSONMode: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should create systemInstruction with JSON reinforcement only
	si, ok := gotBody["systemInstruction"].(map[string]any)
	if !ok {
		t.Fatal("expected systemInstruction for JSONMode")
	}
	siParts, _ := si["parts"].([]any)
	part0, _ := siParts[0].(map[string]any)
	text := part0["text"].(string)
	if !strings.Contains(text, "valid JSON only") {
		t.Errorf("expected JSON reinforcement, got %q", text)
	}
}

func TestGemini_ChatCompletion_JSONMode_WithTools_NoReinforcement(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "OK"}}},
				"finishReason": "STOP",
			}},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{
			{Role: "system", Content: "You are a helper."},
			{Role: "user", Content: "Do something"},
		},
		JSONMode: true,
		Tools: []ToolDef{
			{Name: "some_tool", Description: "Does something", Parameters: nil},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// When tools are present, JSON reinforcement should NOT be appended
	si, ok := gotBody["systemInstruction"].(map[string]any)
	if !ok {
		t.Fatal("expected systemInstruction")
	}
	siParts, _ := si["parts"].([]any)
	part0, _ := siParts[0].(map[string]any)
	text := part0["text"].(string)
	if text != "You are a helper." {
		t.Errorf("expected plain system text, got %q", text)
	}
}

// ---------- Role mapping ----------

func TestGemini_ChatCompletion_RoleMapping(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "OK"}}},
				"finishReason": "STOP",
			}},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there"},
			{Role: "user", Content: "Thanks"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	contents, ok := gotBody["contents"].([]any)
	if !ok || len(contents) != 3 {
		t.Fatalf("expected 3 content entries, got %v", gotBody["contents"])
	}

	// user → user
	c0, _ := contents[0].(map[string]any)
	if c0["role"] != "user" {
		t.Errorf("contents[0] role = %v, want %q", c0["role"], "user")
	}
	// assistant → model
	c1, _ := contents[1].(map[string]any)
	if c1["role"] != "model" {
		t.Errorf("contents[1] role = %v, want %q", c1["role"], "model")
	}
	// user → user
	c2, _ := contents[2].(map[string]any)
	if c2["role"] != "user" {
		t.Errorf("contents[2] role = %v, want %q", c2["role"], "user")
	}
}

func TestGemini_ChatCompletion_EmptyAssistantMessageSkipped(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "OK"}}},
				"finishReason": "STOP",
			}},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: ""},
			{Role: "user", Content: "Still here?"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Empty assistant message (no content, no tool calls) should be skipped
	contents, _ := gotBody["contents"].([]any)
	if len(contents) != 2 {
		t.Fatalf("expected 2 content entries (empty assistant skipped), got %d", len(contents))
	}
	for _, c := range contents {
		cm, _ := c.(map[string]any)
		if cm["role"] != "user" {
			t.Errorf("expected only user roles, got %v", cm["role"])
		}
	}
}

func TestGemini_ChatCompletion_EmptyUserMessageSkipped(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "OK"}}},
				"finishReason": "STOP",
			}},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: ""},
			{Role: "user", Content: "Hello"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	contents, _ := gotBody["contents"].([]any)
	if len(contents) != 1 {
		t.Fatalf("expected 1 content entry (empty user skipped), got %d", len(contents))
	}
}

// ---------- Tool calls ----------

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

	p := NewGeminiProvider(srv.URL, "gemini-flash", "test-key")
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
	if tc.ID != "run_query" {
		t.Errorf("tool ID = %q, want %q (Gemini uses name as ID)", tc.ID, "run_query")
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
	if decl["description"] != "Run a SQL query" {
		t.Errorf("function description = %v, want %q", decl["description"], "Run a SQL query")
	}
}

func TestGemini_ChatCompletion_MultipleToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{
					"role": "model",
					"parts": []map[string]any{
						{"functionCall": map[string]any{"name": "tool_a", "args": map[string]any{"x": "1"}}},
						{"functionCall": map[string]any{"name": "tool_b", "args": map[string]any{"y": "2"}}},
					},
				},
				"finishReason": "STOP",
			}},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "key")
	resp, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Do both"}},
		Tools: []ToolDef{
			{Name: "tool_a", Description: "A"},
			{Name: "tool_b", Description: "B"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "tool_a" {
		t.Errorf("first tool = %q, want %q", resp.ToolCalls[0].Name, "tool_a")
	}
	if resp.ToolCalls[1].Name != "tool_b" {
		t.Errorf("second tool = %q, want %q", resp.ToolCalls[1].Name, "tool_b")
	}
}

func TestGemini_ChatCompletion_ToolCallWithNoArgs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{
					"role": "model",
					"parts": []map[string]any{
						{"functionCall": map[string]any{"name": "list_tables"}},
					},
				},
				"finishReason": "STOP",
			}},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "key")
	resp, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "List tables"}},
		Tools:    []ToolDef{{Name: "list_tables", Description: "List all tables"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "list_tables" {
		t.Errorf("tool name = %q, want %q", resp.ToolCalls[0].Name, "list_tables")
	}
	// Args should be nil when not provided
	if resp.ToolCalls[0].Args != nil {
		t.Errorf("expected nil args, got %v", resp.ToolCalls[0].Args)
	}
}

func TestGemini_ChatCompletion_ToolCallWithTextAndFunctionCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{
					"role": "model",
					"parts": []map[string]any{
						{"text": "Let me run that for you."},
						{"functionCall": map[string]any{"name": "run_query", "args": map[string]any{"sql": "SELECT 1"}}},
					},
				},
				"finishReason": "STOP",
			}},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "key")
	resp, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Run a query"}},
		Tools:    []ToolDef{{Name: "run_query", Description: "Run SQL"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "Let me run that for you." {
		t.Errorf("content = %q, want %q", resp.Content, "Let me run that for you.")
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "run_query" {
		t.Errorf("tool name = %q", resp.ToolCalls[0].Name)
	}
}

func TestGemini_ChatCompletion_MultipleTools_ParameterSchema(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "OK"}}},
				"finishReason": "STOP",
			}},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
		Tools: []ToolDef{
			{
				Name:        "tool_a",
				Description: "First tool",
				Parameters: []ToolParamDef{
					{Name: "query", Type: "string", Required: true},
					{Name: "limit", Type: "int", Required: false},
				},
			},
			{
				Name:        "tool_b",
				Description: "Second tool",
				Parameters: []ToolParamDef{
					{Name: "flag", Type: "bool", Required: true},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tools, _ := gotBody["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tools wrapper, got %d", len(tools))
	}
	wrapper, _ := tools[0].(map[string]any)
	decls, _ := wrapper["functionDeclarations"].([]any)
	if len(decls) != 2 {
		t.Fatalf("expected 2 declarations, got %d", len(decls))
	}

	// Verify first tool's parameter schema
	d0, _ := decls[0].(map[string]any)
	params0, _ := d0["parameters"].(map[string]any)
	if params0["type"] != "object" {
		t.Errorf("params type = %v, want %q", params0["type"], "object")
	}
	props0, _ := params0["properties"].(map[string]any)
	queryProp, _ := props0["query"].(map[string]any)
	if queryProp["type"] != "string" {
		t.Errorf("query type = %v, want %q", queryProp["type"], "string")
	}
	limitProp, _ := props0["limit"].(map[string]any)
	if limitProp["type"] != "integer" {
		t.Errorf("limit type = %v, want %q (int → integer)", limitProp["type"], "integer")
	}
	req0, _ := params0["required"].([]any)
	if len(req0) != 1 || req0[0] != "query" {
		t.Errorf("required = %v, want [query]", req0)
	}

	// Verify second tool's parameter schema (bool → boolean)
	d1, _ := decls[1].(map[string]any)
	params1, _ := d1["parameters"].(map[string]any)
	props1, _ := params1["properties"].(map[string]any)
	flagProp, _ := props1["flag"].(map[string]any)
	if flagProp["type"] != "boolean" {
		t.Errorf("flag type = %v, want %q (bool → boolean)", flagProp["type"], "boolean")
	}
}

func TestGemini_ChatCompletion_NoTools_NoToolsField(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "OK"}}},
				"finishReason": "STOP",
			}},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := gotBody["tools"]; ok {
		t.Error("expected no tools field when no tools are defined")
	}
}

// ---------- Tool result roundtrip ----------

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

	p := NewGeminiProvider(srv.URL, "gemini-flash", "test-key")
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

	// First: user
	c0, _ := contents[0].(map[string]any)
	if c0["role"] != "user" {
		t.Errorf("contents[0] role = %v, want %q", c0["role"], "user")
	}

	// Second: model with functionCall
	c1, _ := contents[1].(map[string]any)
	if c1["role"] != "model" {
		t.Errorf("contents[1] role = %v, want %q", c1["role"], "model")
	}
	parts1, _ := c1["parts"].([]any)
	if len(parts1) != 1 {
		t.Fatalf("expected 1 part in model content, got %d", len(parts1))
	}
	part1, _ := parts1[0].(map[string]any)
	fc, _ := part1["functionCall"].(map[string]any)
	if fc["name"] != "run_query" {
		t.Errorf("functionCall name = %v, want %q", fc["name"], "run_query")
	}

	// Third: user with functionResponse
	c2, _ := contents[2].(map[string]any)
	if c2["role"] != "user" {
		t.Errorf("contents[2] role = %v, want %q", c2["role"], "user")
	}
	parts2, _ := c2["parts"].([]any)
	if len(parts2) != 1 {
		t.Fatalf("expected 1 part in tool result content, got %d", len(parts2))
	}
	part2, _ := parts2[0].(map[string]any)
	fr, _ := part2["functionResponse"].(map[string]any)
	if fr["name"] != "run_query" {
		t.Errorf("functionResponse name = %v, want %q", fr["name"], "run_query")
	}
	frResp, _ := fr["response"].(map[string]any)
	if frResp["result"] != "42" {
		t.Errorf("functionResponse result = %v, want %q", frResp["result"], "42")
	}
}

func TestGemini_ChatCompletion_AssistantWithTextAndToolCalls(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "Done"}}},
				"finishReason": "STOP",
			}},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "Do stuff"},
			{Role: "assistant", Content: "I'll check that.", ToolCalls: []ToolCall{
				{ID: "c1", Name: "tool_a", Args: map[string]any{"q": "1"}},
			}},
			{Role: "tool", ToolCallID: "tool_a", Content: "result"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	contents, _ := gotBody["contents"].([]any)
	if len(contents) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(contents))
	}
	// model message should have both text and functionCall parts
	c1, _ := contents[1].(map[string]any)
	parts, _ := c1["parts"].([]any)
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts (text + functionCall) in model message, got %d", len(parts))
	}
	p0, _ := parts[0].(map[string]any)
	if p0["text"] != "I'll check that." {
		t.Errorf("first part text = %v", p0["text"])
	}
	p1, _ := parts[1].(map[string]any)
	if p1["functionCall"] == nil {
		t.Error("expected functionCall in second part")
	}
}

// ---------- Response parsing ----------

func TestGemini_ChatCompletion_MultipleTextParts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{
					"role": "model",
					"parts": []map[string]any{
						{"text": "Part one. "},
						{"text": "Part two."},
					},
				},
				"finishReason": "STOP",
			}},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "key")
	resp, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "Part one. Part two." {
		t.Errorf("content = %q, want concatenated text", resp.Content)
	}
}

func TestGemini_ChatCompletion_EmptyResponse_NoCandidates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error for empty candidates, got nil")
	}
	if !strings.Contains(err.Error(), "no candidates") {
		t.Errorf("error = %q, want 'no candidates' mention", err.Error())
	}
}

func TestGemini_ChatCompletion_EmptyMessages(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "OK"}}},
				"finishReason": "STOP",
			}},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	contents, _ := gotBody["contents"].([]any)
	if contents != nil && len(contents) != 0 {
		t.Errorf("expected empty/nil contents for empty messages, got %v", contents)
	}
}

// ---------- Error handling ----------

func TestGemini_ChatCompletion_Error_500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "test-key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should contain status code 500, got %q", err.Error())
	}
}

func TestGemini_ChatCompletion_Error_400(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"Invalid request"}}`))
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error should contain status code, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "Invalid request") {
		t.Errorf("error should contain response body, got %q", err.Error())
	}
}

func TestGemini_ChatCompletion_Error_401_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"API key not valid"}}`))
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "bad-key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention 401, got %q", err.Error())
	}
}

func TestGemini_ChatCompletion_Error_429_RateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"Rate limit exceeded"}}`))
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error for 429 response")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error should mention 429, got %q", err.Error())
	}
}

func TestGemini_ChatCompletion_Error_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("not valid json{{{"))
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error for malformed JSON response")
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Errorf("error should mention decode, got %q", err.Error())
	}
}

func TestGemini_ChatCompletion_Error_LargeErrorBody_Truncated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		// Write more than 1024 bytes
		w.Write([]byte(strings.Repeat("x", 2048)))
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	// Error body should be truncated to 1024 bytes by LimitReader
	errMsg := err.Error()
	// The error message includes "gemini: chat returned status 500: " prefix plus the body
	if strings.Count(errMsg, "x") > 1024 {
		t.Errorf("error body should be truncated to 1024 bytes, got %d x's", strings.Count(errMsg, "x"))
	}
}

func TestGemini_ChatCompletion_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "too late"}}},
				"finishReason": "STOP",
			}},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "test-key")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := p.ChatCompletion(ctx, ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}

func TestGemini_ChatCompletion_ContextTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "too late"}}},
				"finishReason": "STOP",
			}},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "key")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := p.ChatCompletion(ctx, ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error for timed-out context, got nil")
	}
}

// ---------- Security: API key handling ----------

func TestGemini_ChatCompletion_APIKeyInQueryParam(t *testing.T) {
	var gotKey string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.URL.Query().Get("key")

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "OK"}}},
				"finishReason": "STOP",
			}},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "secret-api-key-123")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotKey != "secret-api-key-123" {
		t.Errorf("API key = %q, want %q", gotKey, "secret-api-key-123")
	}
}

func TestGemini_ChatCompletion_APIKeyNotInHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// API key should NOT be in Authorization or any other header
		if auth := r.Header.Get("Authorization"); auth != "" {
			t.Errorf("unexpected Authorization header: %q", auth)
		}
		if apiKey := r.Header.Get("x-api-key"); apiKey != "" {
			t.Errorf("unexpected x-api-key header: %q", apiKey)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "OK"}}},
				"finishReason": "STOP",
			}},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "my-key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGemini_ChatCompletion_APIKeyNotLeakedInErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer srv.Close()

	apiKey := "super-secret-key-do-not-leak"
	p := NewGeminiProvider(srv.URL, "gemini-flash", apiKey)
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), apiKey) {
		t.Errorf("error message should NOT contain the API key, got %q", err.Error())
	}
}

func TestGemini_ChatCompletion_SpecialCharsInAPIKey(t *testing.T) {
	var gotKey string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.URL.Query().Get("key")

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "OK"}}},
				"finishReason": "STOP",
			}},
		})
	}))
	defer srv.Close()

	// API key with characters that need URL encoding
	specialKey := "AIzaSyD+test/key=with&special"
	p := NewGeminiProvider(srv.URL, "gemini-flash", specialKey)
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Go's URL parser should handle the round-trip correctly
	if gotKey != specialKey {
		t.Errorf("API key round-trip failed: got %q, want %q", gotKey, specialKey)
	}
}

// ---------- URL construction ----------

func TestGemini_ChatCompletion_URLConstruction(t *testing.T) {
	var gotPath string
	var gotQuery string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "OK"}}},
				"finishReason": "STOP",
			}},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-2.5-pro-preview-05-06", "test-key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantPath := "/v1beta/models/gemini-2.5-pro-preview-05-06:generateContent"
	if gotPath != wantPath {
		t.Errorf("path = %q, want %q", gotPath, wantPath)
	}
	if !strings.Contains(gotQuery, "key=test-key") {
		t.Errorf("query = %q, want key=test-key", gotQuery)
	}
}

// ---------- Unknown role handling ----------

func TestGemini_ChatCompletion_UnknownRoleIgnored(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "OK"}}},
				"finishReason": "STOP",
			}},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{
			{Role: "unknown", Content: "Should be ignored"},
			{Role: "user", Content: "Hi"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only the user message should be in contents
	contents, _ := gotBody["contents"].([]any)
	if len(contents) != 1 {
		t.Fatalf("expected 1 content entry (unknown role skipped), got %d", len(contents))
	}
}

// ---------- Tool result with empty ToolCallID ----------

func TestGemini_ChatCompletion_ToolResultEmptyCallID(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "OK"}}},
				"finishReason": "STOP",
			}},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-flash", "key")
	_, err := p.ChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "Hi"},
			{Role: "tool", ToolCallID: "", Content: "some result"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	contents, _ := gotBody["contents"].([]any)
	if len(contents) != 2 {
		t.Fatalf("expected 2 content entries, got %d", len(contents))
	}
	// Verify the functionResponse still gets sent (with empty name)
	c1, _ := contents[1].(map[string]any)
	parts, _ := c1["parts"].([]any)
	p0, _ := parts[0].(map[string]any)
	fr, _ := p0["functionResponse"].(map[string]any)
	if fr["name"] != "" {
		t.Errorf("expected empty name for empty ToolCallID, got %q", fr["name"])
	}
}
