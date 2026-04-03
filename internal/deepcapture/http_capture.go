package deepcapture

import (
	"encoding/json"
)

// httpCallNested is the nested request/response format in the wire protocol.
type httpCallNested struct {
	Body string `json:"body"`
}

// httpCall represents a single external HTTP call from the External section.
// The wire format nests bodies: {"request": {"body": "..."}, "response": {"body": "..."}}.
type httpCall struct {
	Method          string          `json:"method"`
	URL             string          `json:"url"`
	Host            string          `json:"host"`
	Vendor          string          `json:"vendor"`
	Status          int             `json:"status"`
	DurationMs      float64         `json:"duration_ms"`
	Attempt         int             `json:"attempt"`
	RequestHeaders  json.RawMessage `json:"request_headers"`
	RequestBody     string          `json:"request_body"`
	ResponseHeaders json.RawMessage `json:"response_headers"`
	ResponseBody    string          `json:"response_body"`
	ResponseSize    int             `json:"response_size"`
	RetryAttempt    int             `json:"retry_attempt"`
	ErrorClass      string          `json:"error_class"`
	// Nested format from wire protocol
	Request  *httpCallNested `json:"request,omitempty"`
	Response *httpCallNested `json:"response,omitempty"`
}

// HTTPCaptureHandler extracts external HTTP call captures from
// doc.Event.External into the http_captures table.
type HTTPCaptureHandler struct{}

const httpCaptureSQL = `INSERT INTO http_captures (
	log_id, method, url, host, vendor, status, duration_ms,
	request_headers, request_body, response_headers, response_body,
	response_size, retry_attempt, error_class
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func (h *HTTPCaptureHandler) Extract(doc *Document, logID int64) ([]DetailInsert, error) {
	if doc.Event.External == nil {
		return nil, nil
	}

	var calls []httpCall
	if err := json.Unmarshal(doc.Event.External, &calls); err != nil {
		return nil, err
	}

	inserts := make([]DetailInsert, 0, len(calls))
	for _, c := range calls {
		// Resolve bodies from nested or flat format
		reqBody := c.RequestBody
		if reqBody == "" && c.Request != nil {
			reqBody = c.Request.Body
		}
		respBody := c.ResponseBody
		if respBody == "" && c.Response != nil {
			respBody = c.Response.Body
		}
		retryAttempt := c.RetryAttempt
		if retryAttempt == 0 && c.Attempt > 0 {
			retryAttempt = c.Attempt
		}

		inserts = append(inserts, DetailInsert{
			SQL: httpCaptureSQL,
			Args: []interface{}{
				logID,
				nullableStr(c.Method),
				nullableStr(c.URL),
				nullableStr(c.Host),
				nullableStr(c.Vendor),
				nullableInt(c.Status),
				c.DurationMs,
				nullableJSON(c.RequestHeaders),
				nullableStr(reqBody),
				nullableJSON(c.ResponseHeaders),
				nullableStr(respBody),
				nullableInt(c.ResponseSize),
				retryAttempt,
				nullableStr(c.ErrorClass),
			},
		})
	}

	return inserts, nil
}
