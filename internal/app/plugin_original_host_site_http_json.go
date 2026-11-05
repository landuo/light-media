package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf16"
)

// Requests json= uses Python json.dumps defaults: insertion order, ASCII
// escapes and a space after separators. Number bytes retain JSON precision.
func originalSiteHTTPJSON(raw json.RawMessage) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var output strings.Builder
	var write func() error
	write = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch value := token.(type) {
		case json.Delim:
			output.WriteRune(rune(value))
			first := true
			for decoder.More() {
				if !first {
					output.WriteString(", ")
				}
				first = false
				if value == '{' {
					key, err := decoder.Token()
					if err != nil {
						return err
					}
					name, ok := key.(string)
					if !ok {
						return io.ErrUnexpectedEOF
					}
					originalSiteHTTPJSONString(&output, name)
					output.WriteString(": ")
				}
				if err := write(); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil {
				return err
			}
			ending, ok := end.(json.Delim)
			if !ok {
				return io.ErrUnexpectedEOF
			}
			output.WriteRune(rune(ending))
		case string:
			originalSiteHTTPJSONString(&output, value)
		case json.Number:
			output.WriteString(value.String())
		case bool:
			if value {
				output.WriteString("true")
			} else {
				output.WriteString("false")
			}
		case nil:
			output.WriteString("null")
		default:
			return io.ErrUnexpectedEOF
		}
		return nil
	}
	if !json.Valid(raw) || write() != nil {
		return nil, bad("INVALID_PLUGIN_VALUE", "站点HTTP JSON正文无效")
	}
	return []byte(output.String()), nil
}

func originalSiteHTTPJSONString(output *strings.Builder, value string) {
	output.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			output.WriteString(`\"`)
		case '\\':
			output.WriteString(`\\`)
		case '\b':
			output.WriteString(`\b`)
		case '\f':
			output.WriteString(`\f`)
		case '\n':
			output.WriteString(`\n`)
		case '\r':
			output.WriteString(`\r`)
		case '\t':
			output.WriteString(`\t`)
		default:
			if r < 32 || r >= 127 {
				if r > 0xffff {
					hi, lo := utf16.EncodeRune(r)
					fmt.Fprintf(output, `\u%04x\u%04x`, hi, lo)
				} else {
					fmt.Fprintf(output, `\u%04x`, r)
				}
			} else {
				output.WriteRune(r)
			}
		}
	}
	output.WriteByte('"')
}
