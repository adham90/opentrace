package guardrail

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maskedLiteral replaces the body of every string literal so that keyword
// matching never sees attacker-controlled text (and never trips on words such
// as LIMIT or DELETE that legitimately appear inside data).
const maskedLiteral = "''"

// normalizeSQL rewrites a statement into a canonical form that is safe to
// keyword-match against:
//
//   - block comments (/* ... */) and line comments (-- ...) become a single
//     space, so a comment can no longer be used as an invisible token
//     separator (e.g. "pg_terminate_backend/**/(1)");
//   - MySQL "versioned" comments (/*!40001 ... */) keep their body, because
//     MySQL executes it;
//   - string literals ('...', $tag$...$tag$) collapse to an empty literal, so
//     their contents can neither hide nor fake a keyword;
//   - quoted identifiers ("..." and `...`) are unquoted, so
//     "pg_terminate_backend"(1) is still recognised as a call;
//   - every whitespace rune (including \f, \v and Unicode spaces) collapses to
//     a single ASCII space.
//
// It returns an error for unterminated comments and literals; such input is
// rejected rather than guessed at.
func normalizeSQL(query string) (string, error) {
	var b strings.Builder
	b.Grow(len(query))

	for i := 0; i < len(query); {
		c := query[i]
		switch {
		case c == '\'':
			end, err := scanQuoted(query, i, '\'', true)
			if err != nil {
				return "", err
			}
			b.WriteString(maskedLiteral)
			i = end

		case c == '"' || c == '`':
			end, err := scanQuoted(query, i, c, false)
			if err != nil {
				return "", err
			}
			b.WriteByte(' ')
			b.WriteString(strings.ReplaceAll(query[i+1:end-1], string(c)+string(c), string(c)))
			b.WriteByte(' ')
			i = end

		case c == '$':
			if end, ok := scanDollarQuote(query, i); ok {
				b.WriteString(maskedLiteral)
				i = end
				continue
			}
			b.WriteByte(c)
			i++

		case c == '/' && i+1 < len(query) && query[i+1] == '*':
			end, body, err := scanBlockComment(query, i)
			if err != nil {
				return "", err
			}
			b.WriteByte(' ')
			b.WriteString(body)
			b.WriteByte(' ')
			i = end

		case c == '-' && i+1 < len(query) && query[i+1] == '-' && startsLineComment(query, i+2):
			b.WriteByte(' ')
			i = skipToLineEnd(query, i)

		default:
			r, size := utf8.DecodeRuneInString(query[i:])
			if unicode.IsSpace(r) {
				b.WriteByte(' ')
			} else {
				b.WriteString(query[i : i+size])
			}
			i += size
		}
	}

	return strings.TrimSpace(collapseSpaces(b.String())), nil
}

// scanQuoted returns the index just past the closing quote of the quoted run
// starting at start. Doubled quotes are treated as an escaped quote; for string
// literals a backslash also escapes the next byte (MySQL semantics).
func scanQuoted(s string, start int, quote byte, backslashEscapes bool) (int, error) {
	for i := start + 1; i < len(s); i++ {
		switch {
		case backslashEscapes && s[i] == '\\':
			i++ // skip the escaped byte
		case s[i] == quote:
			if i+1 < len(s) && s[i+1] == quote {
				i++ // doubled quote: stays inside the run
				continue
			}
			return i + 1, nil
		}
	}
	return 0, fmt.Errorf("unterminated quoted string")
}

// scanDollarQuote recognises a PostgreSQL dollar-quoted string ($$..$$ or
// $tag$..$tag$) starting at start and returns the index just past it.
func scanDollarQuote(s string, start int) (int, bool) {
	i := start + 1
	for i < len(s) && isIdentByte(s[i]) && !(i == start+1 && s[i] >= '0' && s[i] <= '9') {
		i++
	}
	if i >= len(s) || s[i] != '$' {
		return 0, false
	}
	tag := s[start : i+1]
	if end := strings.Index(s[i+1:], tag); end >= 0 {
		return i + 1 + end + len(tag), true
	}
	return 0, false
}

// scanBlockComment consumes a /* ... */ run starting at start. It returns the
// index just past the comment and the text that the database actually
// executes: empty for a normal comment, the body for a MySQL versioned comment
// (/*! ... */), whose contents MySQL runs as SQL.
func scanBlockComment(s string, start int) (int, string, error) {
	end := strings.Index(s[start+2:], "*/")
	if end < 0 {
		return 0, "", fmt.Errorf("unterminated comment")
	}
	body := s[start+2 : start+2+end]
	next := start + 2 + end + 2

	if !strings.HasPrefix(body, "!") {
		return next, "", nil
	}
	// /*!50001 SELECT ... */ — drop "!" and the optional version number.
	body = body[1:]
	for len(body) > 0 && body[0] >= '0' && body[0] <= '9' {
		body = body[1:]
	}
	return next, body, nil
}

// startsLineComment reports whether the text after a "--" begins a comment.
// PostgreSQL always treats "--" as a comment, but MySQL requires a following
// space, so "1--1" is arithmetic there. When the two dialects disagree the
// text is kept (scanned as code), which is the conservative choice.
func startsLineComment(s string, i int) bool {
	if i >= len(s) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(s[i:])
	return unicode.IsSpace(r)
}

// skipToLineEnd returns the index of the newline ending the line comment that
// starts at i (or len(s) when the comment runs to the end of the input).
func skipToLineEnd(s string, i int) int {
	if nl := strings.IndexByte(s[i:], '\n'); nl >= 0 {
		return i + nl
	}
	return len(s)
}

// collapseSpaces reduces every run of ASCII spaces to one.
func collapseSpaces(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' {
			if !prevSpace {
				b.WriteByte(' ')
			}
			prevSpace = true
			continue
		}
		prevSpace = false
		b.WriteByte(s[i])
	}
	return b.String()
}

// containsWord reports whether the (normalized, uppercased) statement contains
// word as a standalone token — bounded on both sides by a non-identifier byte.
func containsWord(upper, word string) bool {
	for from := 0; ; {
		i := strings.Index(upper[from:], word)
		if i < 0 {
			return false
		}
		pos := from + i
		from = pos + len(word)
		if pos > 0 && isIdentByte(upper[pos-1]) {
			continue
		}
		if from < len(upper) && isIdentByte(upper[from]) {
			continue
		}
		return true
	}
}

// containsWordNotCall is containsWord, but ignores matches that are function
// calls. MySQL exposes INSERT(), REPLACE() and TRUNCATE() as string/number
// functions, so those names are only suspicious when NOT followed by "(".
func containsWordNotCall(upper, word string) bool {
	for from := 0; ; {
		i := strings.Index(upper[from:], word)
		if i < 0 {
			return false
		}
		pos := from + i
		from = pos + len(word)
		if pos > 0 && isIdentByte(upper[pos-1]) {
			continue
		}
		j := from
		if j < len(upper) && isIdentByte(upper[j]) {
			continue
		}
		for j < len(upper) && upper[j] == ' ' {
			j++
		}
		if j < len(upper) && upper[j] == '(' {
			continue // a function call, not a statement keyword
		}
		return true
	}
}
