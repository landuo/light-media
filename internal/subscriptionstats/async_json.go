package subscriptionstats

import (
	"errors"
	"strconv"
	"strings"
	"unicode/utf16"
)

var ErrAsyncUnicode = errors.New("source_async_json_unicode_encode_error")

// BuildRegistrationAsync uses the same source fields, but HTTPX encodes compact
// UTF-8 JSON. Its ValueError/UnicodeEncodeError escape AsyncRequestUtils's
// RequestError catch; they must not be reported as a successful admission or a
// caught requests.InvalidJSONError from the synchronous child.
func BuildRegistrationAsync(sourceFields []byte) ([]byte, error) {
	raw, err := BuildRegistration(sourceFields)
	if err != nil {
		return nil, err
	}
	return asyncJSON(raw)
}

func AsyncJSONResult(err error) (Result, bool) {
	if errors.Is(err, ErrInvalidJSON) {
		return Result{SourceError: "ValueError", Outcome: "not_requested"}, true
	}
	if errors.Is(err, ErrAsyncUnicode) {
		return Result{SourceError: "UnicodeEncodeError", Outcome: "not_requested"}, true
	}
	return Result{}, false
}

// Input is the checked ASCII JSON produced by marshalSourceFields. Decode
// escaped Unicode without replacing isolated surrogates, retaining exact
// Python integer and float spelling while removing only structural spaces.
func asyncJSON(raw []byte) ([]byte, error) {
	var out strings.Builder
	for i := 0; i < len(raw); {
		if raw[i] == ' ' {
			i++
			continue
		}
		if raw[i] != '"' {
			out.WriteByte(raw[i])
			i++
			continue
		}
		out.WriteByte('"')
		i++
		for i < len(raw) && raw[i] != '"' {
			r := rune(raw[i])
			i++
			if r == '\\' {
				e := raw[i]
				i++
				if e != 'u' {
					out.WriteByte('\\')
					out.WriteByte(e)
					continue
				}
				v, _ := strconv.ParseUint(string(raw[i:i+4]), 16, 16)
				i += 4
				r = rune(v)
				if r >= 0xd800 && r <= 0xdbff && i+6 <= len(raw) && raw[i] == '\\' && raw[i+1] == 'u' {
					low, _ := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
					if low >= 0xdc00 && low <= 0xdfff {
						r = utf16.DecodeRune(r, rune(low))
						i += 6
					}
				}
			}
			if r >= 0xd800 && r <= 0xdfff {
				return nil, ErrAsyncUnicode
			}
			if r < 32 {
				appendSourceRune(&out, r)
			} else {
				out.WriteRune(r)
			}
		}
		out.WriteByte('"')
		i++
	}
	return []byte(out.String()), nil
}
