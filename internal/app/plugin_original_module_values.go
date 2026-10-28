package app

import (
	"encoding/json"

	"light-media/internal/plugins"
)

// Source bytes are scalar module values, including b"". The dispatcher must
// keep that terminal identity; a subsequent caller bool check sees len(bytes).
func originalModuleResultTruthy(raw json.RawMessage) bool {
	if value, bytes := plugins.DecodeModuleResultBytes(raw); bytes {
		return len(value) != 0
	}
	return originalBaseJSONTruthy(raw)
}
