package notice

import "encoding/json"

// CoerceSourceInteger exposes the existing Pydantic-compatible JSON value
// conversion to other host boundaries; it does not send a notification.
func CoerceSourceInteger(value any) (json.Number, bool) {
	converted, kind := integer(value)
	if kind != "" {
		return "", false
	}
	return converted.(json.Number), true
}
