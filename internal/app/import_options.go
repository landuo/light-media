package app

// Existing plans and directory rules predate these optional fields. Preserve
// their previous behavior while allowing an explicit false to opt out.
func importBool(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}
