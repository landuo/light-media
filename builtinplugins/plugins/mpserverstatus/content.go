package mpserverstatus

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"io"
	"strings"

	"github.com/andybalholm/brotli"
	"light-media/builtinplugins/plugins/mpserverstatus/encodingdetector"
)

// Installed Python3.12 urllib3 has no backports.zstd: zstd is not decoded.
// Decoding follows installed urllib3 2.7.0, including its unknown-member
// DeflateDecoder fallback and maximum five simultaneous content codings.
func decodeContent(data []byte, encoding string) ([]byte, error) {
	encoding = strings.ToLower(encoding)
	recognized := func(v string) bool { return v == "gzip" || v == "x-gzip" || v == "deflate" || v == "br" }
	codings := strings.Split(encoding, ",")
	if len(codings) == 1 && !recognized(encoding) {
		return data, nil
	}
	active := false
	for _, c := range codings {
		active = active || recognized(strings.TrimSpace(c))
	}
	if !active {
		return data, nil
	}
	if len(codings) > 5 {
		return nil, errors.New("content_encoding_chain_too_long")
	}
	for i := len(codings) - 1; i >= 0; i-- {
		switch strings.TrimSpace(codings[i]) {
		case "gzip", "x-gzip":
			var e error
			data, e = decodeGzip(data)
			if e != nil {
				return nil, e
			}
		case "br":
			var e error
			data, e = io.ReadAll(brotli.NewReader(bytes.NewReader(data)))
			if e != nil && !errors.Is(e, io.ErrUnexpectedEOF) {
				return nil, e
			}
		default:
			r, e := zlib.NewReader(bytes.NewReader(data))
			if e != nil {
				r = flate.NewReader(bytes.NewReader(data))
			}
			data, e = io.ReadAll(r)
			r.Close()
			if e != nil && !errors.Is(e, io.ErrUnexpectedEOF) {
				return nil, e
			}
		}
	}
	return data, nil
}

// gzip source accepts incomplete final members, and ignores garbage after
// a completed member. HTTP Content-Length truncation is handled before this.
func decodeGzip(data []byte) ([]byte, error) {
	input := bytes.NewReader(data)
	result := []byte{}
	members := 0
	for input.Len() > 0 {
		remaining := data[len(data)-input.Len():]
		reader, e := gzip.NewReader(input)
		if e != nil {
			if members == 0 && errors.Is(e, io.ErrUnexpectedEOF) && (len(remaining) >= 2 && (remaining[0] != 0x1f || remaining[1] != 0x8b) || len(remaining) >= 4 && (remaining[2] != 8 || remaining[3]&0xe0 != 0)) {
				return nil, gzip.ErrHeader
			}
			if members > 0 || errors.Is(e, io.ErrUnexpectedEOF) || errors.Is(e, io.EOF) {
				return result, nil
			}
			return nil, e
		}
		reader.Multistream(false)
		decoded, e := io.ReadAll(reader)
		reader.Close()
		if e != nil && !errors.Is(e, io.ErrUnexpectedEOF) {
			if members > 0 {
				return result, nil
			}
			return nil, e
		}
		result = append(result, decoded...)
		if e != nil {
			return result, nil
		}
		members++
	}
	return result, nil
}

func responseText(data []byte, contentType string) (string, error) {
	if len(data) == 0 {
		return "", nil
	}
	// requests.utils._parse_content_type_header splits on semicolons and strips
	// quoted parameter values; only parameter names are lowercased.
	parts := strings.Split(contentType, ";")
	encoding := ""
	hasCharset := false
	for _, p := range parts[1:] {
		key, value, found := strings.Cut(p, "=")
		if found && strings.ToLower(strings.Trim(strings.TrimFunc(key, pythonSpace), "\"' ")) == "charset" {
			hasCharset = true
			encoding = strings.Trim(strings.TrimFunc(value, pythonSpace), "\"' ")
		}
	}
	if !hasCharset {
		if strings.Contains(parts[0], "text") {
			encoding = "ISO-8859-1"
		} else if strings.Contains(parts[0], "application/json") {
			encoding = "utf-8"
		} else {
			var e error
			encoding, _, e = encodingdetector.Detect(data)
			if e != nil {
				return "", e
			}
		}
	}
	if encoding == "" {
		encoding = "utf-8"
	}
	text, e := encodingdetector.Decode(data, encoding, true)
	if e != nil {
		class := encodingdetector.ErrorClass(e)
		if class == "LookupError" || class == "TypeError" {
			return encodingdetector.Decode(data, "utf-8", true)
		}
	}
	if e != nil {
		var codec *encodingdetector.CodecError
		if errors.As(e, &codec) {
			reason := codec.Reason
			if codec.Encoding == "idna" && codec.Class == "UnicodeError" {
				reason = "Unsupported error handling replace"
			}
			return text, sourceTextCodecError{cause: e, message: reason}
		}
	}
	return text, e
}

type sourceTextCodecError struct {
	cause   error
	message string
}

func (e sourceTextCodecError) Error() string { return e.message }
func (e sourceTextCodecError) Unwrap() error { return e.cause }
