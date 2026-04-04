package index

import (
	"path/filepath"
	"testing"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"POST /api/orders 201 1243ms", []string{"post", "api", "orders", "201", "1243ms"}},
		{"Failed to charge customer: card declined", []string{"failed", "to", "charge", "customer", "card", "declined"}},
		{"Connection timeout to redis:6379", []string{"connection", "timeout", "to", "redis", "6379"}},
		{"NoMethodError: undefined method 'name'", []string{"nomethoderror", "undefined", "method", "name"}},
		{"", nil},
		{"a b c", nil}, // all < 2 chars
		{"cache_hit for user-42", []string{"cache_hit", "for", "user-42"}},
	}

	for _, tt := range tests {
		got := Tokenize(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("Tokenize(%q): got %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i := range tt.want {
			if got[i] != tt.want[i] {
				t.Errorf("Tokenize(%q)[%d]: got %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

func TestTokenizeUnique(t *testing.T) {
	tokens := TokenizeUnique("the the the same same word")
	seen := make(map[string]bool)
	for _, tok := range tokens {
		if seen[tok] {
			t.Errorf("duplicate token: %q", tok)
		}
		seen[tok] = true
	}
}

func TestBuildAndSearch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chunk_000.idx")

	messages := []string{
		"POST /api/orders 201 1243ms",                   // 0
		"Failed to charge customer: card declined",      // 1
		"Cache hit for user 42",                         // 2
		"Connection timeout to redis:6379",              // 3
		"POST /api/users 200 45ms",                      // 4
		"Failed to connect to database: timeout",        // 5
		"Card payment processed successfully",           // 6
		"GET /api/orders 200 12ms",                      // 7
		"NoMethodError: undefined method name for nil",  // 8
		"Customer card declined by payment gateway",     // 9
	}

	// Build index
	b := NewBuilder()
	for i, msg := range messages {
		b.Add(uint32(i), msg)
	}

	if err := b.Write(path, len(messages)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	t.Logf("index: %d terms from %d messages", b.TermCount(), len(messages))

	// Open reader
	r, err := OpenReader(path)
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}

	// Single term search
	ids, err := r.Search("timeout")
	if err != nil {
		t.Fatalf("Search(timeout): %v", err)
	}
	assertRowIDs(t, "timeout", ids, []uint32{3, 5})

	// Multi-term search (AND)
	ids, err = r.Search("card declined")
	if err != nil {
		t.Fatalf("Search(card declined): %v", err)
	}
	assertRowIDs(t, "card declined", ids, []uint32{1, 9})

	// Single term with multiple hits
	ids, err = r.Search("failed")
	if err != nil {
		t.Fatalf("Search(failed): %v", err)
	}
	assertRowIDs(t, "failed", ids, []uint32{1, 5})

	// POST search
	ids, err = r.Search("post")
	if err != nil {
		t.Fatalf("Search(post): %v", err)
	}
	assertRowIDs(t, "post", ids, []uint32{0, 4})

	// No results
	ids, err = r.Search("nonexistent")
	if err != nil {
		t.Fatalf("Search(nonexistent): %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("Search(nonexistent): expected empty, got %v", ids)
	}

	// AND with no overlap
	ids, err = r.Search("redis nomethoderror")
	if err != nil {
		t.Fatalf("Search(redis nomethoderror): %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("Search(redis nomethoderror): expected empty, got %v", ids)
	}

	// Empty query
	ids, err = r.Search("")
	if err != nil {
		t.Fatalf("Search empty: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("Search empty: expected empty, got %v", ids)
	}

	// Case insensitive
	ids, err = r.Search("POST")
	if err != nil {
		t.Fatalf("Search(POST uppercase): %v", err)
	}
	assertRowIDs(t, "POST uppercase", ids, []uint32{0, 4})
}

func TestBuildAndSearchLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.idx")

	// Build 10K messages
	messages := make([]string, 10000)
	for i := range messages {
		switch {
		case i%100 == 0:
			messages[i] = "Error: connection timeout to database"
		case i%50 == 0:
			messages[i] = "Warning: slow query detected on users table"
		default:
			messages[i] = "Info: request completed successfully 200 OK"
		}
	}

	b := NewBuilder()
	for i, msg := range messages {
		b.Add(uint32(i), msg)
	}

	if err := b.Write(path, len(messages)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	r, err := OpenReader(path)
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}

	// "timeout" should match every 100th entry
	ids, err := r.Search("timeout")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(ids) != 100 {
		t.Errorf("timeout matches: want 100, got %d", len(ids))
	}

	// "connection timeout" (AND)
	ids, err = r.Search("connection timeout")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(ids) != 100 {
		t.Errorf("connection timeout: want 100, got %d", len(ids))
	}

	// "successfully" should match most entries
	ids, err = r.Search("successfully")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	expectedSuccessfully := 10000 - 100 - 100 // minus timeout entries and slow query entries (with overlap)
	t.Logf("'successfully' matches: %d (expected ~%d)", len(ids), expectedSuccessfully)
}

func assertRowIDs(t *testing.T, name string, got, want []uint32) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: length mismatch: want %v, got %v", name, want, got)
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s[%d]: want %d, got %d", name, i, want[i], got[i])
		}
	}
}
