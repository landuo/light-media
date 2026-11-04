package mediaservervendors

import (
	"strings"

	encodingdetector "light-media/internal/plugins/iyuuautoseed/compat/encodingdetector"
)

// The source uses urllib.parse.parse_qs defaults, not Go ParseQuery: split only
// on &, discard raw blank/missing values, preserve malformed percent sequences,
// decode + to space and invalid UTF-8 with replacement, then take first value.
// Only this private auth_key consumer changes; no global URL helper or route.
func ugreenRefreshFirstQueryValue(query, key string) (string, bool, error) {
	for _, field := range strings.Split(query, "&") {
		name, value, found := strings.Cut(field, "=")
		if !found || value == "" {
			continue
		}
		decodedName, err := ugreenRefreshUnquotePlus(name)
		if err != nil {
			return "", false, err
		}
		if decodedName != key {
			continue
		}
		decodedValue, err := ugreenRefreshUnquotePlus(value)
		if err != nil {
			return "", false, err
		}
		return decodedValue, true, nil
	}
	return "", false, nil
}
func ugreenRefreshUnquotePlus(value string) (string, error) {
	value = strings.ReplaceAll(value, "+", " ")
	if !strings.Contains(value, "%") {
		return value, nil
	}
	var out strings.Builder
	for i := 0; i < len(value); {
		start := i
		if value[i] >= 128 {
			for i < len(value) && value[i] >= 128 {
				i++
			}
			out.WriteString(value[start:i])
			continue
		}
		for i < len(value) && value[i] < 128 {
			i++
		}
		ascii := value[start:i]
		raw := make([]byte, 0, len(ascii))
		for j := 0; j < len(ascii); j++ {
			if ascii[j] == '%' && j+2 < len(ascii) {
				high, highOK := ugreenRefreshHex(ascii[j+1])
				low, lowOK := ugreenRefreshHex(ascii[j+2])
				if highOK && lowOK {
					raw = append(raw, high<<4|low)
					j += 2
					continue
				}
			}
			raw = append(raw, ascii[j])
		}
		// Reuse the already accepted exact Python codec engine, not strings.ToValidUTF8.
		decoded, err := encodingdetector.Decode(raw, "utf-8", true)
		if err != nil {
			return "", &EmbyJellyfinBoundaryError{Kind: "Ugreen query UTF-8 codec", cause: err}
		}
		out.WriteString(decoded)
	}
	return out.String(), nil
}
func ugreenRefreshHex(value byte) (byte, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10, true
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10, true
	}
	return 0, false
}
