package app

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// SQLite's source string columns encode Python code points as strict UTF-8.
// The Go store uses encrypted JSON instead, so preserve the original failure
// before saving history. Only the original preformatted/no-template branch is
// covered here; no claim is made about arbitrary Python objects or renderer
// assignments. Keep raw strings until this stage, after Notification validation.
func originalNoticeSourceHistoryEncoding(fields map[string]json.RawMessage) error {
	if fields == nil {
		return nil
	}
	preformatted := originalBaseJSONTruthy(fields["title"]) || originalBaseJSONTruthy(fields["text"])
	if !preformatted && originalBaseJSONTruthy(fields["ctype"]) {
		return nil
	}
	// Original Message table column order determines the first encoding failure.
	for _, key := range []string{"source", "title", "text", "image", "link", "userid"} {
		raw := fields[key]
		if len(raw) == 0 || raw[0] != '"' {
			continue
		}
		if message := originalNoticeStringEncodingError(raw); message != "" {
			return bad("PLUGIN_NOTIFICATION_HISTORY_ENCODING_FAILED", "UnicodeEncodeError: "+message)
		}
	}
	return nil
}

// Valid JSON string only. Count Python code points (not UTF-8 bytes or UTF-16
// units), join a valid escaped surrogate pair, retain lone surrogate runs.
func originalNoticeStringEncodingError(raw []byte) string {
	position, start, end := 0, -1, -1
	var first uint16
	for i := 1; i < len(raw)-1; position++ {
		invalid := false
		var code uint16
		if raw[i] != '\\' {
			_, size := utf8.DecodeRune(raw[i:])
			i += size
		} else {
			i++
			if raw[i] != 'u' {
				i++
			} else {
				code = originalViewHex4(raw[i+1:])
				i += 5
				if code >= 0xd800 && code <= 0xdbff && i+5 < len(raw)-1 && raw[i] == '\\' && raw[i+1] == 'u' {
					low := originalViewHex4(raw[i+2:])
					if low >= 0xdc00 && low <= 0xdfff {
						i += 6
						code = 0
					}
				}
				invalid = code >= 0xd800 && code <= 0xdfff
			}
		}
		if invalid {
			if start < 0 {
				start, first = position, code
			}
			end = position + 1
		} else if start >= 0 {
			break
		}
	}
	if start < 0 {
		return ""
	}
	if end == start+1 {
		return fmt.Sprintf("'utf-8' codec can't encode character '\\u%04x' in position %d: surrogates not allowed", first, start)
	}
	return fmt.Sprintf("'utf-8' codec can't encode characters in position %d-%d: surrogates not allowed", start, end-1)
}
