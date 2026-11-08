package app

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// The worker protocol carries strict JSON. Legal exponent lexemes can still
// represent a Python binary64 infinity or an underflowed signed zero. Original
// Pydantic success serialization emits null for nonfinite floats, preserving
// source truth testing before that conversion. This is an HTTP-only boundary;
// raw configuration/data and internal worker replies are not rewritten.
func originalViewNumericJSON(raw []byte) json.RawMessage {
	out := make([]byte, 0, len(raw))
	quoted := false
	for i := 0; i < len(raw); i++ {
		b := raw[i]
		if quoted {
			out = append(out, b)
			if b == '\\' {
				i++
				out = append(out, raw[i])
			} else if b == '"' {
				quoted = false
			}
			continue
		}
		if b == '"' {
			quoted = true
			out = append(out, b)
			continue
		}
		if b != '-' && (b < '0' || b > '9') {
			out = append(out, b)
			continue
		}
		end := i + 1
		for end < len(raw) && strings.ContainsRune("0123456789.eE+-", rune(raw[end])) {
			end++
		}
		lexeme := raw[i:end]
		if bytes.ContainsAny(lexeme, ".eE") {
			value, _ := strconv.ParseFloat(string(lexeme), 64) // valid JSON input
			if math.IsInf(value, 0) {
				out = append(out, "null"...)
			} else {
				text := strconv.FormatFloat(value, 'g', -1, 64)
				if !strings.ContainsAny(text, ".eE") {
					text += ".0"
				}
				out = append(out, text...)
			}
		} else if bytes.Equal(lexeme, []byte("-0")) {
			out = append(out, '0') // Python JSON integer -0 is the integer zero.
		} else {
			out = append(out, lexeme...)
		}
		i = end - 1
	}
	return out
}
