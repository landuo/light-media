package mediaservervendors

import (
	"bytes"
	"context"
	"errors"
	"io"
	runtime "light-media/internal/plugins/mediaserverruntime"
	"strings"
	"unicode"
)

// RequestsContentCurrent supplies fresh authority for this call. It does not
// replace a client's transport checks and never changes its route or headers.
type RequestsContentCurrent func(context.Context) error

// MediaServerRequestsReadContent reads a complete entity without an invented
// aggregate cap. Post-read authority wins even when Read returns data plus EOF
// or another error. The body remains owned/closed by the HTTP caller.
func MediaServerRequestsReadContent(ctx context.Context, body io.Reader, encoding string, current RequestsContentCurrent) ([]byte, error) {
	check := requestsContentCheck(ctx, current)
	if err := check(); err != nil {
		return nil, err
	}
	if body == nil {
		return nil, errors.New("Requests content requires body reader")
	}
	raw, err := io.ReadAll(&requestsContentReader{reader: body, check: check})
	if guard := check(); guard != nil {
		return nil, guard
	}
	if err != nil {
		return nil, err
	}
	return MediaServerRequestsContent(ctx, raw, encoding, current)
}

// MediaServerRequestsContent implements the recovered no-optional-codecs
// gzip/x-gzip/deflate profile on an already read entity, not HTTP framing.
// The finite source value/error gate is not a claim about arbitrary network
// fragmentation or a streaming Requests Response object.
func MediaServerRequestsContent(ctx context.Context, raw []byte, encoding string, current RequestsContentCurrent) ([]byte, error) {
	check := requestsContentCheck(ctx, current)
	if err := check(); err != nil {
		return nil, err
	}
	modes, err := requestsContentModes(encoding)
	if err != nil {
		return nil, err
	}
	data := raw
	for i := len(modes) - 1; i >= 0; i-- {
		if err := check(); err != nil {
			return nil, err
		}
		if modes[i] == "gzip" || modes[i] == "x-gzip" {
			data, err = requestsContentGzip(data, check)
		} else {
			data, err = requestsContentDeflate(data, check)
		}
		if guard := check(); guard != nil {
			return nil, guard
		}
		if err != nil {
			return nil, err
		}
	}
	if err := check(); err != nil {
		return nil, err
	}
	return data, nil
}

// A guard can itself return a source-shaped error. Keep it non-ordinary rather
// than allowing a vendor's source-exception catch to swallow authority failure.
type requestsContentAuthorityError struct{ cause error }

func (e *requestsContentAuthorityError) Error() string { return "Requests content authority failed" }
func (e *requestsContentAuthorityError) Unwrap() error { return e.cause }
func requestsContentCheck(ctx context.Context, current RequestsContentCurrent) func() error {
	return func() error {
		if ctx == nil || current == nil {
			return errors.New("Requests content requires context and current authority")
		}
		if e := ctx.Err(); e != nil {
			return &requestsContentAuthorityError{cause: e}
		}
		if e := current(ctx); e != nil {
			return &requestsContentAuthorityError{cause: e}
		}
		if e := ctx.Err(); e != nil {
			return &requestsContentAuthorityError{cause: e}
		}
		return nil
	}
}

type requestsContentReader struct {
	reader io.Reader
	check  func() error
}

func (r *requestsContentReader) Read(p []byte) (int, error) {
	if e := r.check(); e != nil {
		return 0, e
	}
	n, e := r.reader.Read(p)
	if g := r.check(); g != nil {
		return 0, g
	}
	return n, e
}
func requestsContentModes(value string) ([]string, error) {
	value = strings.ToLower(value)
	known := func(s string) bool { return s == "gzip" || s == "x-gzip" || s == "deflate" }
	if !strings.Contains(value, ",") {
		if known(value) {
			return []string{value}, nil
		}
		return nil, nil
	}
	modes := strings.Split(value, ",")
	active := false
	for i := range modes {
		modes[i] = strings.TrimFunc(modes[i], func(r rune) bool { return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f })
		active = active || known(modes[i])
	}
	if !active {
		return nil, nil
	}
	if len(modes) > 5 {
		return nil, requestsContentError(errors.New("too many content encodings in the chain"))
	}
	// Once a known chain member activates decoding, original _get_decoder uses
	// Deflate for unknown entries. Single unknown/padded coding passes through.
	return modes, nil
}
func requestsContentError(cause error) error {
	return &runtime.SourceError{Class: "ContentDecodingError", Cause: cause}
}

const requestsContentChunk = 10 * 1024 // original content allowance, not a size cap

type requestsContentByteReader struct {
	source *bytes.Reader
	check  func() error
}

func requestsContentInput(raw []byte, check func() error) *requestsContentByteReader {
	return &requestsContentByteReader{source: bytes.NewReader(raw), check: check}
}
func (r *requestsContentByteReader) Read(p []byte) (int, error) {
	if e := r.check(); e != nil {
		return 0, e
	}
	n, e := r.source.Read(p)
	if g := r.check(); g != nil {
		return 0, g
	}
	return n, e
}
func (r *requestsContentByteReader) ReadByte() (byte, error) {
	if e := r.check(); e != nil {
		return 0, e
	}
	b, e := r.source.ReadByte()
	if g := r.check(); g != nil {
		return 0, g
	}
	return b, e
}
