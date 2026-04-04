package index

import (
	"strings"
	"unicode"
)

// Tokenize splits a message into lowercased tokens for indexing.
// Splits on whitespace and punctuation, lowercases all tokens,
// removes tokens shorter than 2 characters.
func Tokenize(s string) []string {
	s = strings.ToLower(s)

	tokens := strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-'
	})

	// Filter out very short tokens (noise)
	result := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if len(t) >= 2 {
			result = append(result, t)
		}
	}
	return result
}

// TokenizeUnique returns unique tokens from a message (for querying).
func TokenizeUnique(s string) []string {
	tokens := Tokenize(s)
	seen := make(map[string]bool, len(tokens))
	unique := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if !seen[t] {
			seen[t] = true
			unique = append(unique, t)
		}
	}
	return unique
}
