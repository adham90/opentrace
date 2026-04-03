package deepcapture

import (
	"encoding/json"
)

// fileEntry represents a single file operation from the Files section.
type fileEntry struct {
	Action         string  `json:"action"`
	Filename       string  `json:"filename"`
	SizeBytes      int     `json:"size_bytes"`
	ContentType    string  `json:"content_type"`
	StorageService string  `json:"storage_service"`
	Key            string  `json:"key"`
	DurationMs     float64 `json:"duration_ms"`
}

// FileCaptureHandler extracts file operation captures from doc.Event.Files
// into the file_captures table.
type FileCaptureHandler struct{}

const fileCaptureSQL = `INSERT INTO file_captures (
	log_id, action, filename, size_bytes, content_type,
	storage_service, key, duration_ms
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

func (h *FileCaptureHandler) Extract(doc *Document, logID int64) ([]DetailInsert, error) {
	if doc.Event.Files == nil {
		return nil, nil
	}

	var files []fileEntry
	if err := json.Unmarshal(doc.Event.Files, &files); err != nil {
		return nil, err
	}

	inserts := make([]DetailInsert, 0, len(files))
	for _, f := range files {
		inserts = append(inserts, DetailInsert{
			SQL: fileCaptureSQL,
			Args: []interface{}{
				logID,
				nullableStr(f.Action),
				nullableStr(f.Filename),
				nullableInt(f.SizeBytes),
				nullableStr(f.ContentType),
				nullableStr(f.StorageService),
				nullableStr(f.Key),
				f.DurationMs,
			},
		})
	}

	return inserts, nil
}
