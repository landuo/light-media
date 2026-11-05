package catalog

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"light-media/internal/logging"
)

// The pinned TMDb request cache stores decoded JSON plus headers before
// handling errors/rate limits. HTTP status alone is not its success condition.
type SourceRecognitionResponse struct {
	JSON    json.RawMessage
	Headers http.Header
}

func sourceRecognitionConnectionFailure(err error) bool {
	var wrapped *url.Error
	if errors.As(err, &wrapped) {
		err = wrapped.Err
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var network net.Error
	if errors.As(err, &network) {
		return true
	}
	var certificate *tls.CertificateVerificationError
	return errors.As(err, &certificate)
}

func (c *Client) sourceRecognitionResponse(ctx context.Context, path string, query url.Values) (SourceRecognitionResponse, error) {
	var result SourceRecognitionResponse
	if c == nil {
		return result, &Error{Code: "not_configured"}
	}
	if c.configError != nil {
		return result, c.configError
	}
	values := url.Values{}
	for key, value := range query {
		values[key] = append([]string{}, value...)
	}
	if c.credentials.APIKey != "" {
		values.Set("api_key", c.credentials.APIKey)
	}
	u := url.URL{Scheme: "https", Host: apiHost, Path: "/3/" + path, RawQuery: values.Encode()}
	client := *c.http
	client.Timeout = 20 * time.Second
	// RequestUtils retries a stale idempotent session once for connection,
	// chunked/read-timeout errors. TMDb.request then resets its owned session
	// and repeats that request once. Other RequestException paths get only the
	// outer retry; malformed JSON is evaluated afterwards and never retried.
	for session := 0; session < 2; session++ {
		for attempt := 0; attempt < 2; attempt++ {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			request, err := http.NewRequestWithContext(logging.EndpointContext(ctx, "metadata", u.String()), http.MethodGet, u.String(), nil)
			if err != nil {
				return result, &Error{Code: "invalid_input"}
			}
			request.Header.Set("Accept", "application/json")
			request.Header.Set("User-Agent", "light-media/0.1")
			if c.credentials.AccessToken != "" {
				request.Header.Set("Authorization", "Bearer "+c.credentials.AccessToken)
			}
			response, err := client.Do(request)
			if err != nil {
				if ctx.Err() != nil {
					return result, ctx.Err()
				}
				client.CloseIdleConnections()
				if attempt == 0 && sourceRecognitionConnectionFailure(err) {
					continue
				}
				break
			}
			raw, readErr := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
			_ = response.Body.Close()
			if readErr != nil {
				client.CloseIdleConnections()
				if attempt == 0 && sourceRecognitionConnectionFailure(readErr) {
					continue
				}
				break
			}
			if len(raw) > maxBody || !utf8.Valid(raw) || !json.Valid(raw) {
				return result, &Error{Code: "invalid_response"}
			}
			trimmed := strings.TrimSpace(string(raw))
			if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
				return result, &Error{Code: "invalid_response"}
			}
			return SourceRecognitionResponse{JSON: json.RawMessage(raw), Headers: response.Header.Clone()}, nil
		}
		client.CloseIdleConnections()
	}
	return result, &Error{Code: "unavailable"}
}

func (c *Client) SourceRecognitionSearchResponse(ctx context.Context, kind, name, year, locale string) (SourceRecognitionResponse, error) {
	if kind != "movie" && kind != "tv" && kind != "multi" || name == "" {
		return SourceRecognitionResponse{}, &Error{Code: "invalid_input"}
	}
	query := url.Values{"query": {name}, "page": {"1"}, "language": {locale}}
	if year != "" {
		key := "year"
		if kind == "tv" {
			key = "first_air_date_year"
		}
		query.Set(key, year)
	}
	return c.sourceRecognitionResponse(ctx, "search/"+kind, query)
}
func (c *Client) SourceRecognitionInformationResponse(ctx context.Context, kind string, id int64, locale string) (SourceRecognitionResponse, error) {
	if !kindOK(kind) || !validID(id) {
		return SourceRecognitionResponse{}, &Error{Code: "invalid_input"}
	}
	appendFields := "images,credits,alternative_titles,translations,release_dates,external_ids"
	if kind == "tv" {
		appendFields = "images,credits,alternative_titles,translations,content_ratings,external_ids,episode_groups"
	}
	return c.sourceRecognitionResponse(ctx, kind+"/"+strconv.FormatInt(id, 10), url.Values{"language": {locale}, "append_to_response": {appendFields}})
}
