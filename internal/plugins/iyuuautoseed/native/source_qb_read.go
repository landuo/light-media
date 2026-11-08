package download

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type sourceReadContextKey struct{}

// The error text is unchanged and sanitized. Only the source read/auth policy
// can inspect the transport cause; default clients still receive their old Error.
type sourceReadTransportError struct {
	public *Error
	cause  error
}

func (e *sourceReadTransportError) Error() string   { return e.public.Error() }
func (e *sourceReadTransportError) Unwrap() []error { return []error{e.public, e.cause} }
func sourceReadFailure(ctx context.Context, op, code string, cause error) error {
	public := &Error{Operation: op, Code: code}
	if ctx.Value(sourceReadContextKey{}) == true {
		return &sourceReadTransportError{public: public, cause: cause}
	}
	return public
}
func (c *Client) sourceReadGuard(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.sourceGuard != nil {
		if err := c.sourceGuard(ctx); err != nil {
			return &iyuuGuardError{cause: err}
		}
	}
	return nil
}
func sourceConnectFailure(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}
func sourceTransientTransport(err error) bool {
	var failure *sourceReadTransportError
	return errors.As(err, &failure)
}
func sourceRetryAfter(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}
	raw = strings.TrimSpace(raw)
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && raw != "" && strings.Trim(raw, "0123456789") == "" {
		if seconds < 0 || seconds > int64((time.Duration(1<<63-1))/time.Second) {
			return 0, failure("source_read", "invalid_retry_after")
		}
		return time.Duration(seconds) * time.Second, nil
	}
	at, err := http.ParseTime(raw)
	if err != nil {
		return 0, failure("source_read", "invalid_retry_after")
	}
	wait := time.Until(at)
	if wait < 0 {
		wait = 0
	}
	return wait, nil
}

// Fixed installed QbittorrentSession+request_manager read/auth policy,2x2.
// GET status/read retries and any-method connect retries use one adapter retry;
// read/auth5xx and transport failures use one manager retry. Selected torrent
// writes never replay. No worker-supplied policy, URLs or status lists exist.
func (c *Client) sourceQBReadRequest(ctx context.Context, method string, endpoint *url.URL, body []byte, contentType string, selected bool) ([]byte, http.Header, int, error) {
	ctx = context.WithValue(ctx, sourceReadContextKey{}, true)
	var data []byte
	var headers http.Header
	var status int
	var err error
	for manager := 0; manager < 2; manager++ {
		for adapter := 0; adapter < 2; adapter++ {
			if guard := c.sourceReadGuard(ctx); guard != nil {
				return nil, nil, 0, guard
			}
			data, headers, status, err = c.request(ctx, method, endpoint, body, contentType, "")
			if selected || ctx.Err() != nil {
				return data, headers, status, err
			}
			var denied *iyuuGuardError
			if errors.As(err, &denied) || errors.Is(err, context.Canceled) {
				return data, headers, status, err
			}
			if adapter == 1 {
				break
			}
			retry := sourceConnectFailure(err) || method == http.MethodGet && sourceTransientTransport(err)
			if err == nil && method == http.MethodGet {
				retry = status == 500 || status == 502 || status == 504 || headers.Get("Retry-After") != "" && (status == 413 || status == 429 || status == 503)
			}
			if !retry {
				break
			}
			if waitErr := c.sourceReadGuard(ctx); waitErr != nil {
				return nil, nil, 0, waitErr
			}
			if method == http.MethodGet && headers.Get("Retry-After") != "" {
				wait, waitErr := sourceRetryAfter(headers.Get("Retry-After"))
				if waitErr != nil {
					err = waitErr
					break
				}
				if wait > 0 {
					timer := time.NewTimer(wait)
					select {
					case <-ctx.Done():
						timer.Stop()
						return nil, nil, 0, ctx.Err()
					case <-timer.C:
					}
				}
			}
		}
		var parseError *Error
		invalidRetryAfter := errors.As(err, &parseError) && parseError.Operation == "source_read" && parseError.Code == "invalid_retry_after"
		if manager == 1 || !(sourceTransientTransport(err) || invalidRetryAfter || err == nil && status >= 500) {
			return data, headers, status, err
		}
		if guard := c.sourceReadGuard(ctx); guard != nil {
			return nil, nil, 0, guard
		}
		c.http.Jar, _ = cookiejar.New(nil)
		c.loggedIn = false
	}
	return data, headers, status, err
}
