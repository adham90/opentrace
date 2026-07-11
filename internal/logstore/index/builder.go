package index

import (
	"encoding/binary"
	"fmt"
	"os"
	"sort"

	enc "github.com/adham90/opentrace/internal/logstore/encoding"
)

// Builder builds an inverted index from messages at seal time.
type Builder struct {
	terms map[string][]uint32 // term → sorted row IDs
}

// NewBuilder creates a new index builder.
func NewBuilder() *Builder {
	return &Builder{
		terms: make(map[string][]uint32, 1024),
	}
}

// Add indexes all tokens from a message at the given row index.
func (b *Builder) Add(rowIdx uint32, message string) {
	tokens := TokenizeUnique(message)
	for _, t := range tokens {
		b.terms[t] = append(b.terms[t], rowIdx)
	}
}

// TermCount returns the number of unique terms.
func (b *Builder) TermCount() int {
	return len(b.terms)
}

// Write serializes the inverted index to a file.
// Format:
//
//	Header:  [4 bytes magic "OTIX"] [4 bytes term_count] [4 bytes entry_count]
//	Terms:   sorted term entries, each:
//	           [2 bytes term_len] [term bytes] [4 bytes postings_offset] [4 bytes postings_len]
//	Postings: zstd-compressed delta-encoded row ID lists
func (b *Builder) Write(path string, entryCount int) error {
	// Sort terms
	sortedTerms := make([]string, 0, len(b.terms))
	for t := range b.terms {
		sortedTerms = append(sortedTerms, t)
	}
	sort.Strings(sortedTerms)

	// Build postings section first (to know offsets)
	type termEntry struct {
		term           string
		postingsOffset uint32
		postingsLen    uint32
	}

	var postingsBuf []byte
	termEntries := make([]termEntry, 0, len(sortedTerms))

	for _, term := range sortedTerms {
		rowIDs := b.terms[term]

		// Delta-encode row IDs
		deltas := make([]byte, 0, len(rowIDs)*2)
		prev := uint32(0)
		for _, id := range rowIDs {
			delta := id - prev
			deltas = enc.PutUvarint(deltas, uint64(delta))
			prev = id
		}

		compressed := enc.Compress(deltas)

		termEntries = append(termEntries, termEntry{
			term:           term,
			postingsOffset: uint32(len(postingsBuf)),
			postingsLen:    uint32(len(compressed)),
		})
		postingsBuf = append(postingsBuf, compressed...)
	}

	// Build term table
	var termTable []byte
	for _, te := range termEntries {
		termTable = binary.LittleEndian.AppendUint16(termTable, uint16(len(te.term)))
		termTable = append(termTable, te.term...)
		termTable = binary.LittleEndian.AppendUint32(termTable, te.postingsOffset)
		termTable = binary.LittleEndian.AppendUint32(termTable, te.postingsLen)
	}

	// Write file: header + term table + postings
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create index: %w", err)
	}
	defer f.Close()

	// Header: magic(4) + term_count(4) + entry_count(4) + term_table_size(4)
	header := make([]byte, 16)
	copy(header[0:4], "OTIX")
	binary.LittleEndian.PutUint32(header[4:], uint32(len(sortedTerms)))
	binary.LittleEndian.PutUint32(header[8:], uint32(entryCount))
	binary.LittleEndian.PutUint32(header[12:], uint32(len(termTable)))

	if _, err := f.Write(header); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	if _, err := f.Write(termTable); err != nil {
		return fmt.Errorf("write term table: %w", err)
	}
	if _, err := f.Write(postingsBuf); err != nil {
		return fmt.Errorf("write postings: %w", err)
	}

	// Flush before returning: the seal path deletes the source WAL on success,
	// so the index must be durable, not merely in the page cache.
	if err := f.Sync(); err != nil {
		return fmt.Errorf("fsync index: %w", err)
	}
	return nil
}
