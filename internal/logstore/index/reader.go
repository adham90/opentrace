package index

import (
	"encoding/binary"
	"fmt"
	"os"
	"sort"

	enc "github.com/adham90/opentrace/internal/logstore/encoding"
)

// Reader reads from a sealed inverted index file.
type Reader struct {
	terms          []indexTerm // sorted by term name
	postingsData   []byte
	entryCount     int
}

type indexTerm struct {
	term           string
	postingsOffset uint32
	postingsLen    uint32
}

// OpenReader opens an inverted index file for reading.
func OpenReader(path string) (*Reader, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read index: %w", err)
	}

	if len(data) < 16 {
		return nil, fmt.Errorf("index too short: %d bytes", len(data))
	}

	if string(data[0:4]) != "OTIX" {
		return nil, fmt.Errorf("invalid index magic: %q", data[0:4])
	}

	termCount := int(binary.LittleEndian.Uint32(data[4:]))
	entryCount := int(binary.LittleEndian.Uint32(data[8:]))
	termTableSize := int(binary.LittleEndian.Uint32(data[12:]))

	if 16+termTableSize > len(data) {
		return nil, fmt.Errorf("term table overflows file")
	}

	termTable := data[16 : 16+termTableSize]
	postingsData := data[16+termTableSize:]

	// Parse term table
	terms := make([]indexTerm, 0, termCount)
	offset := 0
	for range termCount {
		if offset+2 > len(termTable) {
			return nil, fmt.Errorf("term table truncated at term %d", len(terms))
		}
		termLen := int(binary.LittleEndian.Uint16(termTable[offset:]))
		offset += 2
		if offset+termLen+8 > len(termTable) {
			return nil, fmt.Errorf("term entry truncated")
		}
		term := string(termTable[offset : offset+termLen])
		offset += termLen
		postingsOffset := binary.LittleEndian.Uint32(termTable[offset:])
		offset += 4
		postingsLen := binary.LittleEndian.Uint32(termTable[offset:])
		offset += 4
		terms = append(terms, indexTerm{term, postingsOffset, postingsLen})
	}

	return &Reader{
		terms:        terms,
		postingsData: postingsData,
		entryCount:   entryCount,
	}, nil
}

// Search finds row IDs matching ALL query terms (AND semantics).
// Returns sorted, deduplicated row IDs.
func (r *Reader) Search(query string) ([]uint32, error) {
	queryTerms := TokenizeUnique(query)
	if len(queryTerms) == 0 {
		return nil, nil
	}

	// Get postings for each term
	var postingLists [][]uint32
	for _, qt := range queryTerms {
		ids, err := r.lookup(qt)
		if err != nil {
			return nil, fmt.Errorf("lookup %q: %w", qt, err)
		}
		if len(ids) == 0 {
			return nil, nil // AND semantics: one empty term = no results
		}
		postingLists = append(postingLists, ids)
	}

	if len(postingLists) == 1 {
		return postingLists[0], nil
	}

	// Intersect all posting lists
	result := postingLists[0]
	for _, list := range postingLists[1:] {
		result = intersectSorted(result, list)
		if len(result) == 0 {
			return nil, nil
		}
	}
	return result, nil
}

// lookup does binary search for a term and returns its posting list.
func (r *Reader) lookup(term string) ([]uint32, error) {
	idx := sort.Search(len(r.terms), func(i int) bool {
		return r.terms[i].term >= term
	})
	if idx >= len(r.terms) || r.terms[idx].term != term {
		return nil, nil // term not found
	}

	t := r.terms[idx]
	if int(t.postingsOffset)+int(t.postingsLen) > len(r.postingsData) {
		return nil, fmt.Errorf("postings overflow for term %q", term)
	}

	compressed := r.postingsData[t.postingsOffset : t.postingsOffset+t.postingsLen]
	raw, err := enc.Decompress(compressed)
	if err != nil {
		return nil, fmt.Errorf("decompress postings for %q: %w", term, err)
	}

	// Delta-decode
	var ids []uint32
	prev := uint32(0)
	offset := 0
	for offset < len(raw) {
		delta, n := binary.Uvarint(raw[offset:])
		if n <= 0 {
			return nil, fmt.Errorf("invalid delta uvarint at offset %d", offset)
		}
		prev += uint32(delta)
		ids = append(ids, prev)
		offset += n
	}

	return ids, nil
}

// intersectSorted returns the intersection of two sorted uint32 slices.
func intersectSorted(a, b []uint32) []uint32 {
	result := make([]uint32, 0, min(len(a), len(b)))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if a[i] == b[j] {
			result = append(result, a[i])
			i++
			j++
		} else if a[i] < b[j] {
			i++
		} else {
			j++
		}
	}
	return result
}
