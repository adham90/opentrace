package api

import (
	"encoding/json"
	"fmt"
	"net/http"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// formatJSONError produces a user-friendly message from a JSON parse error,
// including the position or field when available.
func formatJSONError(err error, expected string) string {
	if syntaxErr, ok := err.(*json.SyntaxError); ok {
		return fmt.Sprintf("invalid JSON %s: syntax error at byte offset %d", expected, syntaxErr.Offset)
	}
	if typeErr, ok := err.(*json.UnmarshalTypeError); ok {
		return fmt.Sprintf("invalid JSON %s: field %q expects %s but got %s", expected, typeErr.Field, typeErr.Type, typeErr.Value)
	}
	return fmt.Sprintf("invalid JSON %s: %s", expected, err.Error())
}

// writeDetailedError writes a structured error response with an error code and
// optional details map. Useful for connector and validation errors where the
// client benefits from machine-readable error classification.
func writeDetailedError(w http.ResponseWriter, status int, message string, code string, details map[string]any) {
	resp := map[string]any{
		"error": message,
		"code":  code,
	}
	if details != nil {
		resp["details"] = details
	}
	writeJSON(w, status, resp)
}
