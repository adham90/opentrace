package deepcapture

import (
	"encoding/json"
)

// sqlQuery represents a single SQL query from the DB section.
type sqlQuery struct {
	RawSQL         string          `json:"raw_sql"`
	NormalizedSQL  string          `json:"normalized_sql"`
	BindValues     json.RawMessage `json:"bind_values"`
	RowCount       int             `json:"row_count"`
	DurationMs     float64         `json:"duration_ms"`
	Cached         bool            `json:"cached"`
	InTransaction  bool            `json:"in_transaction"`
	ExplainPlan    string          `json:"explain_plan"`
	PoolSize       int             `json:"pool_size"`
	PoolBusy       int             `json:"pool_busy"`
	PoolWaiting    int             `json:"pool_waiting"`
	CallerLocation string          `json:"caller_location"`
	Fingerprint    string          `json:"fingerprint"`
	TableName      string          `json:"table_name"`
}

// SQLCaptureHandler extracts SQL captures from doc.Event.DB.Queries into
// the sql_captures table.
type SQLCaptureHandler struct{}

const sqlCaptureSQL = `INSERT INTO sql_captures (
	log_id, raw_sql, normalized_sql, bind_values, row_count,
	duration_ms, cached, in_transaction, explain_plan,
	pool_size, pool_busy, pool_waiting, caller_location,
	fingerprint, table_name
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func (h *SQLCaptureHandler) Extract(doc *Document, logID int64) ([]DetailInsert, error) {
	if doc.Event.DB == nil || doc.Event.DB.Queries == nil {
		return nil, nil
	}

	var queries []sqlQuery
	if err := json.Unmarshal(doc.Event.DB.Queries, &queries); err != nil {
		return nil, err
	}

	inserts := make([]DetailInsert, 0, len(queries))
	for _, q := range queries {
		cached := 0
		if q.Cached {
			cached = 1
		}
		inTx := 0
		if q.InTransaction {
			inTx = 1
		}

		inserts = append(inserts, DetailInsert{
			SQL: sqlCaptureSQL,
			Args: []interface{}{
				logID,
				nullableStr(q.RawSQL),
				nullableStr(q.NormalizedSQL),
				nullableJSON(q.BindValues),
				nullableInt(q.RowCount),
				q.DurationMs,
				cached,
				inTx,
				nullableStr(q.ExplainPlan),
				nullableInt(q.PoolSize),
				nullableInt(q.PoolBusy),
				nullableInt(q.PoolWaiting),
				nullableStr(q.CallerLocation),
				nullableStr(q.Fingerprint),
				nullableStr(q.TableName),
			},
		})
	}

	return inserts, nil
}
