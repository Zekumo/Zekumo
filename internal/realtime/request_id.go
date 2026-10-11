package realtime

// IDs are opaque per-connection correlation tokens, not authentication or
// idempotency keys. Limiting them prevents reflection of unbounded input.
func validRequestID(id string) bool {
	if len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
