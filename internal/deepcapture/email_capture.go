package deepcapture

import (
	"encoding/json"
)

// emailEntry represents a single email from the Email section.
type emailEntry struct {
	MailerClass    string          `json:"mailer_class"`
	MailerAction   string          `json:"mailer_action"`
	FromAddress    string          `json:"from_address"`
	ToAddresses    json.RawMessage `json:"to_addresses"`
	CCAddresses    json.RawMessage `json:"cc_addresses"`
	BCCAddresses   json.RawMessage `json:"bcc_addresses"`
	Subject        string          `json:"subject"`
	BodyHTML       string          `json:"body_html"`
	BodyText       string          `json:"body_text"`
	TemplateName   string          `json:"template_name"`
	TemplateVars   json.RawMessage `json:"template_vars"`
	Attachments    json.RawMessage `json:"attachments"`
	DeliveryStatus string          `json:"delivery_status"`
	SMTPResponse   string          `json:"smtp_response"`
	DurationMs     float64         `json:"duration_ms"`
}

// EmailCaptureHandler extracts email captures from doc.Event.Email
// into the email_captures table.
type EmailCaptureHandler struct{}

const emailCaptureSQL = `INSERT INTO email_captures (
	log_id, mailer_class, mailer_action, from_address,
	to_addresses, cc_addresses, bcc_addresses, subject,
	body_html, body_text, template_name, template_vars,
	attachments, delivery_status, smtp_response, duration_ms
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func (h *EmailCaptureHandler) Extract(doc *Document, logID int64) ([]DetailInsert, error) {
	if doc.Event.Email == nil {
		return nil, nil
	}

	var emails []emailEntry
	if err := json.Unmarshal(doc.Event.Email, &emails); err != nil {
		return nil, err
	}

	inserts := make([]DetailInsert, 0, len(emails))
	for _, e := range emails {
		inserts = append(inserts, DetailInsert{
			SQL: emailCaptureSQL,
			Args: []interface{}{
				logID,
				nullableStr(e.MailerClass),
				nullableStr(e.MailerAction),
				nullableStr(e.FromAddress),
				nullableJSON(e.ToAddresses),
				nullableJSON(e.CCAddresses),
				nullableJSON(e.BCCAddresses),
				nullableStr(e.Subject),
				nullableStr(e.BodyHTML),
				nullableStr(e.BodyText),
				nullableStr(e.TemplateName),
				nullableJSON(e.TemplateVars),
				nullableJSON(e.Attachments),
				nullableStr(e.DeliveryStatus),
				nullableStr(e.SMTPResponse),
				e.DurationMs,
			},
		})
	}

	return inserts, nil
}
