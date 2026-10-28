package artifacts

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode"

	"light-media/internal/sourcejson"
)

// Fixed PluginManager._normalize_plugin_label accepts a trimmed string, or
// joins each list item's nonempty Python str(...). Other JSON kinds are absent.
func normalizeMetadataLabel(raw json.RawMessage) (string, error) {
	trim := func(value string) string {
		return strings.TrimFunc(value, func(r rune) bool { return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f })
	}
	if len(raw) == 0 {
		return "", nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return trim(text), nil
	}
	if data := bytes.TrimSpace(raw); len(data) == 0 || data[0] != '[' {
		return "", nil
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return "", err
	}
	labels := []string{}
	for _, value := range values {
		text, err := sourcejson.String(value)
		if err != nil {
			return "", err
		}
		if text = trim(text); text != "" {
			labels = append(labels, text)
		}
	}
	return strings.Join(labels, " "), nil
}
