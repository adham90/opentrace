package cli

import (
	"net/http/httptest"
	"testing"
)

// /logs/search is served by the tail handler, which reads "search". A caller
// using the conventional "q" used to get every log back, unfiltered, presented
// as search results.
func TestSearchTerm_AcceptsQAlias(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  string
	}{
		{"search param", "/logs/search?search=timeout", "timeout"},
		{"q alias", "/logs/search?q=timeout", "timeout"},
		{"search wins over q", "/logs/search?search=one&q=two", "one"},
		{"neither", "/logs/search", ""},
		{"empty q", "/logs/search?q=", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.query, nil)
			if got := searchTerm(req); got != tc.want {
				t.Errorf("searchTerm(%q) = %q, want %q", tc.query, got, tc.want)
			}
		})
	}
}
