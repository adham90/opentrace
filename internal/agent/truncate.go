package agent

// TruncateObservation truncates s to maxBytes. If truncation occurs,
// a "\n[truncated]" marker is appended.
func TruncateObservation(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes] + "\n[truncated]"
}
