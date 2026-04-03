package deepcapture

import (
	"encoding/json"
)

// requestData represents the parsed request section from the wire format.
type requestData struct {
	Headers     json.RawMessage `json:"headers"`
	Body        string          `json:"body"`
	Size        int             `json:"size"`
	ContentType string          `json:"content_type"`
	IPAddress   string          `json:"ip_address"`
	UserAgent   string          `json:"user_agent"`
	Referer     string          `json:"referer"`
	Cookies     json.RawMessage `json:"cookies"`
	SessionData json.RawMessage `json:"session_data"`
}

// responseData represents the parsed response section from the wire format.
type responseData struct {
	Headers json.RawMessage `json:"headers"`
	Body    string          `json:"body"`
	Size    int             `json:"size"`
}

// RequestCaptureHandler extracts request/response captures from doc.Event.Request
// and doc.Event.Response into the request_captures table.
type RequestCaptureHandler struct{}

const requestCaptureSQL = `INSERT INTO request_captures (
	log_id, request_headers, request_body, request_size, content_type,
	ip_address, user_agent, referer, cookies, session_data,
	response_headers, response_body, response_size
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func (h *RequestCaptureHandler) Extract(doc *Document, logID int64) ([]DetailInsert, error) {
	if doc.Event.Request == nil && doc.Event.Response == nil {
		return nil, nil
	}

	var req requestData
	if doc.Event.Request != nil {
		if err := json.Unmarshal(doc.Event.Request, &req); err != nil {
			return nil, err
		}
	}

	var resp responseData
	if doc.Event.Response != nil {
		if err := json.Unmarshal(doc.Event.Response, &resp); err != nil {
			return nil, err
		}
	}

	return []DetailInsert{
		{
			SQL: requestCaptureSQL,
			Args: []interface{}{
				logID,
				nullableJSON(req.Headers),
				nullableStr(req.Body),
				nullableInt(req.Size),
				nullableStr(req.ContentType),
				nullableStr(req.IPAddress),
				nullableStr(req.UserAgent),
				nullableStr(req.Referer),
				nullableJSON(req.Cookies),
				nullableJSON(req.SessionData),
				nullableJSON(resp.Headers),
				nullableStr(resp.Body),
				nullableInt(resp.Size),
			},
		},
	}, nil
}
