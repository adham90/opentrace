package domain

// ClampLimit normalises a pagination limit to fall within [1, max].
// If limit is <= 0 it returns defaultVal; if > max it returns max.
func ClampLimit(limit, defaultVal, max int) int {
	if limit <= 0 {
		return defaultVal
	}
	if limit > max {
		return max
	}
	return limit
}
