package auth

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestGenerateSessionToken(t *testing.T) {
	t.Run("returns no error", func(t *testing.T) {
		_, err := GenerateSessionToken()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("returns 64-character string", func(t *testing.T) {
		token, err := GenerateSessionToken()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(token) != 64 {
			t.Errorf("expected length 64, got %d", len(token))
		}
	})

	t.Run("returns valid hex", func(t *testing.T) {
		token, err := GenerateSessionToken()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, err := hex.DecodeString(token); err != nil {
			t.Errorf("token is not valid hex: %v", err)
		}
	})

	t.Run("generates unique tokens", func(t *testing.T) {
		seen := make(map[string]struct{}, 100)
		for i := 0; i < 100; i++ {
			token, err := GenerateSessionToken()
			if err != nil {
				t.Fatalf("unexpected error on iteration %d: %v", i, err)
			}
			if _, exists := seen[token]; exists {
				t.Fatalf("duplicate token on iteration %d: %s", i, token)
			}
			seen[token] = struct{}{}
		}
	})
}

func TestGenerateMCPToken(t *testing.T) {
	t.Run("returns no error", func(t *testing.T) {
		_, err := GenerateMCPToken()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("has mcp_ prefix", func(t *testing.T) {
		token, err := GenerateMCPToken()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasPrefix(token, "mcp_") {
			t.Errorf("expected prefix 'mcp_', got %q", token)
		}
	})

	t.Run("has total length 68", func(t *testing.T) {
		token, err := GenerateMCPToken()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// "mcp_" (4 chars) + 64 hex chars = 68
		if len(token) != 68 {
			t.Errorf("expected length 68, got %d", len(token))
		}
	})

	t.Run("hex after prefix is valid", func(t *testing.T) {
		token, err := GenerateMCPToken()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		hexPart := strings.TrimPrefix(token, "mcp_")
		if _, err := hex.DecodeString(hexPart); err != nil {
			t.Errorf("hex portion after prefix is not valid hex: %v", err)
		}
	})

	t.Run("generates unique tokens", func(t *testing.T) {
		seen := make(map[string]struct{}, 100)
		for i := 0; i < 100; i++ {
			token, err := GenerateMCPToken()
			if err != nil {
				t.Fatalf("unexpected error on iteration %d: %v", i, err)
			}
			if _, exists := seen[token]; exists {
				t.Fatalf("duplicate token on iteration %d: %s", i, token)
			}
			seen[token] = struct{}{}
		}
	})
}

func TestGenerateAPIKey(t *testing.T) {
	t.Run("returns no error", func(t *testing.T) {
		_, err := GenerateAPIKey()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("has ot_ prefix", func(t *testing.T) {
		token, err := GenerateAPIKey()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasPrefix(token, "ot_") {
			t.Errorf("expected prefix 'ot_', got %q", token)
		}
	})

	t.Run("has total length 67", func(t *testing.T) {
		token, err := GenerateAPIKey()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// "ot_" (3 chars) + 64 hex chars = 67
		if len(token) != 67 {
			t.Errorf("expected length 67, got %d", len(token))
		}
	})

	t.Run("hex after prefix is valid", func(t *testing.T) {
		token, err := GenerateAPIKey()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		hexPart := strings.TrimPrefix(token, "ot_")
		if _, err := hex.DecodeString(hexPart); err != nil {
			t.Errorf("hex portion after prefix is not valid hex: %v", err)
		}
	})

	t.Run("generates unique keys", func(t *testing.T) {
		seen := make(map[string]struct{}, 100)
		for i := 0; i < 100; i++ {
			key, err := GenerateAPIKey()
			if err != nil {
				t.Fatalf("unexpected error on iteration %d: %v", i, err)
			}
			if _, exists := seen[key]; exists {
				t.Fatalf("duplicate key on iteration %d: %s", i, key)
			}
			seen[key] = struct{}{}
		}
	})
}

func TestTokenGenerators(t *testing.T) {
	tests := []struct {
		name      string
		generate  func() (string, error)
		prefix    string
		wantLen   int
		hexOffset int // index where hex portion begins
	}{
		{
			name:      "GenerateSessionToken",
			generate:  GenerateSessionToken,
			prefix:    "",
			wantLen:   64,
			hexOffset: 0,
		},
		{
			name:      "GenerateMCPToken",
			generate:  GenerateMCPToken,
			prefix:    "mcp_",
			wantLen:   68,
			hexOffset: 4,
		},
		{
			name:      "GenerateAPIKey",
			generate:  GenerateAPIKey,
			prefix:    "ot_",
			wantLen:   67,
			hexOffset: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, err := tt.generate()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(token) != tt.wantLen {
				t.Errorf("expected length %d, got %d", tt.wantLen, len(token))
			}

			if tt.prefix != "" && !strings.HasPrefix(token, tt.prefix) {
				t.Errorf("expected prefix %q, got %q", tt.prefix, token[:len(tt.prefix)])
			}

			hexPart := token[tt.hexOffset:]
			if _, err := hex.DecodeString(hexPart); err != nil {
				t.Errorf("hex portion %q is not valid hex: %v", hexPart, err)
			}

			if len(hexPart) != 64 {
				t.Errorf("expected hex portion length 64, got %d", len(hexPart))
			}
		})
	}
}
