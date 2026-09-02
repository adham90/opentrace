package ingest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// maxIngestBatchEntries prevents a valid 10MB document made of tiny objects
// from becoming an unbounded slice of large entries and post-processing jobs.
const maxIngestBatchEntries = 5000

// decodeJSONOneOrMany streams either one object or an array. Each entry is
// materialized exactly once and the entry cap is enforced while reading.
func decodeJSONOneOrMany[T any](r io.Reader) ([]T, error) {
	br := bufio.NewReader(r)
	for {
		first, err := br.Peek(1)
		if err != nil {
			if err == io.EOF {
				return nil, fmt.Errorf("empty request body")
			}
			return nil, err
		}
		switch first[0] {
		case ' ', '\t', '\r', '\n':
			_, _ = br.Discard(1)
			continue
		}

		dec := json.NewDecoder(br)
		switch first[0] {
		case '{':
			var one T
			if err := dec.Decode(&one); err != nil {
				return nil, err
			}
			if err := requireJSONEOF(dec); err != nil {
				return nil, err
			}
			return []T{one}, nil
		case '[':
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			entries := make([]T, 0, 256)
			for dec.More() {
				if len(entries) == maxIngestBatchEntries {
					return nil, fmt.Errorf("batch exceeds the maximum of %d entries", maxIngestBatchEntries)
				}
				var entry T
				if err := dec.Decode(&entry); err != nil {
					return nil, fmt.Errorf("entry %d: %w", len(entries), err)
				}
				entries = append(entries, entry)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			if err := requireJSONEOF(dec); err != nil {
				return nil, err
			}
			return entries, nil
		default:
			return nil, fmt.Errorf("request body must be one object or an array")
		}
	}
}

func requireJSONEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("request body contains multiple JSON values")
		}
		return err
	}
	return nil
}
