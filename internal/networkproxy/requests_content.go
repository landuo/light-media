package networkproxy

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"io"
	"strings"

	"github.com/andybalholm/brotli"
)

var ErrRequestsContentTooLarge = errors.New("requests_content_too_large")

// Response.content contains decoded bytes in requests. Keep UTF-8/JSON/text
// validation in the calling plugin; both encoded and expanded bodies are bound
// by the caller-supplied payload budget.
func DecodeRequestsContent(data []byte, encoding string, limit int) ([]byte, error) {
	if limit < 0 || len(data) > limit {
		return nil, ErrRequestsContentTooLarge
	}
	encoding = strings.ToLower(encoding)
	recognized := func(value string) bool {
		return value == "gzip" || value == "x-gzip" || value == "deflate" || value == "br"
	}
	codings := strings.Split(encoding, ",")
	if len(codings) == 1 && !recognized(encoding) {
		return data, nil
	}
	if len(codings) > 1 {
		active := false
		for _, coding := range codings {
			active = active || recognized(strings.TrimSpace(coding))
		}
		if !active {
			return data, nil
		}
		if len(codings) > 5 {
			return nil, errors.New("content_encoding_chain_too_long")
		}
	}
	for i := len(codings) - 1; i >= 0; i-- {
		var reader io.Reader
		var closeReader func()
		switch strings.TrimSpace(codings[i]) {
		case "gzip", "x-gzip":
			decoded, err := requestsGzip(data, limit)
			if err != nil {
				return nil, err
			}
			data = decoded
			continue
		case "br":
			reader = brotli.NewReader(bytes.NewReader(data))
		default:
			// urllib3's MultiDecoder assigns DeflateDecoder to an unknown
			// member when any member of the chain is recognized.
			r, err := zlib.NewReader(bytes.NewReader(data))
			if err != nil {
				r = flate.NewReader(bytes.NewReader(data))
			}
			reader, closeReader = r, func() { _ = r.Close() }
		}
		decoded, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
		if closeReader != nil {
			closeReader()
		}
		if len(decoded) > limit {
			return nil, ErrRequestsContentTooLarge
		}
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, err
		}
		data = decoded
	}
	return data, nil
}

// urllib3 does not require a completed final compressed member at EOF, and
// ignores invalid trailing members after the first complete gzip stream.
func requestsGzip(data []byte, limit int) ([]byte, error) {
	input := bytes.NewReader(data)
	result := []byte{}
	members := 0
	for input.Len() > 0 {
		remaining := data[len(data)-input.Len():]
		reader, err := gzip.NewReader(input)
		if err != nil {
			if members == 0 && errors.Is(err, io.ErrUnexpectedEOF) &&
				(len(remaining) >= 2 && (remaining[0] != 0x1f || remaining[1] != 0x8b) ||
					len(remaining) >= 4 && (remaining[2] != 8 || remaining[3]&0xe0 != 0)) {
				return nil, gzip.ErrHeader
			}
			if members > 0 || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
				return result, nil
			}
			return nil, err
		}
		reader.Multistream(false)
		decoded, err := io.ReadAll(io.LimitReader(reader, int64(limit-len(result)+1)))
		_ = reader.Close()
		if len(result)+len(decoded) > limit {
			return nil, ErrRequestsContentTooLarge
		}
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			if members > 0 {
				return result, nil
			}
			return nil, err
		}
		result = append(result, decoded...)
		if err != nil {
			return result, nil
		}
		members++
	}
	return result, nil
}
