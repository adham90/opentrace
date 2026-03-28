// Package auth provides authentication utilities: token generation
// and rate limiting.
package auth

import (
	"crypto/rand"
	"encoding/hex"
)

// GenerateSessionToken creates a cryptographically random 64-character hex token.
func GenerateSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// GenerateMCPToken creates a prefixed MCP token.
func GenerateMCPToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "mcp_" + hex.EncodeToString(b), nil
}

// GenerateAPIKey creates a prefixed API key.
func GenerateAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "ot_" + hex.EncodeToString(b), nil
}
