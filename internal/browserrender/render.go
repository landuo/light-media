// Package browserrender implements the ordinary PlaywrightHelper page-source
// boundary with native Chromium and a pure Go CDP driver. It contains no Agent,
// persistent browser session, workflow or arbitrary worker JavaScript API.
package browserrender

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

type Config struct {
	Humanize        bool
	HumanPreset     string
	Executable      string
	Emulation       string
	FlareSolverrURL string
	TempDir         string
	HTTPClient      *http.Client
}

// Request is internal to the host and must not be serialized into plugin data.
type Request struct {
	URL, Cookie, UserAgent, Proxy string
	// SourceProxy preserves the host PROXY_SERVER dict, including literal auth
	// and malformed server spellings which FlareSolverr still receives.
	SourceProxy *ProxySettings
	Headless    bool
	Timeout     time.Duration
	// SourceTimeout is the ordinary helper's original JSON value. It is only
	// set by the source-reference host path; native ID calls retain Timeout.
	SourceTimeout json.RawMessage
	// Host-only source faults: defer until native after FS short-circuit.
	SourceInvalidCookie, SourceInvalidUserAgent bool
	// Host-owned ordinary logging, never serialized or exposed as a plugin hook.
	SourceLog func(level, code string)
	Guard     func(context.Context) error
}

// Error messages never contain an address, cookie, proxy or browser output.
type Error struct{ Code string }

func (e *Error) Error() string { return e.Code }

type GuardError struct{ Cause error }

func (e *GuardError) Error() string { return "browser_scope_changed" }
func (e *GuardError) Unwrap() error { return e.Cause }

func guard(ctx context.Context, request Request) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if request.Guard != nil {
		if err := request.Guard(ctx); err != nil {
			return &GuardError{Cause: err}
		}
	}
	return nil
}

func (request Request) sourceLog(level, code string) {
	if request.SourceLog != nil {
		request.SourceLog(level, code)
	}
}

func ConfigFromEnvironment(getenv func(string) string) Config {
	c := Config{Executable: getenv("CLOAKBROWSER_BINARY_PATH"), Emulation: getenv("BROWSER_EMULATION"), FlareSolverrURL: getenv("FLARESOLVERR_URL")}
	if c.Executable == "" {
		c.Executable, _ = exec.LookPath("cloakbrowser")
		if c.Executable == "" {
			c.Executable = "/usr/local/lib/light-media/cloakbrowser/chrome"
		}
	}
	return c
}

// PageResult is internal host data. Only the SourceReference host path sends
// SourceValueJSON; native ID callers retain their ordinary optional HTML.
type PageResult struct {
	HTML            *string
	SourceValueJSON []byte
}

func (c Config) Render(ctx context.Context, request Request) (*string, error) {
	out, err := c.render(ctx, request, false)
	return out.HTML, err
}
func (c Config) RenderSource(ctx context.Context, request Request) (PageResult, error) {
	return c.render(ctx, request, true)
}
func (c Config) render(ctx context.Context, request Request, sourceValue bool) (PageResult, error) {
	u, err := url.Parse(request.URL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Scheme != "http" && u.Scheme != "https" {
		return PageResult{}, &Error{Code: "browser_invalid_url"}
	}
	if len(request.SourceTimeout) == 0 && (strings.ContainsAny(request.Cookie+request.UserAgent, "\r\n") || request.Timeout < 0) {
		return PageResult{}, &Error{Code: "browser_invalid_request"}
	}
	if err := guard(ctx, request); err != nil {
		return PageResult{}, err
	}
	if strings.ToLower(c.Emulation) == "flaresolverr" {
		solution, err := c.flareSolverrValue(ctx, request)
		var stale *GuardError
		if errors.As(err, &stale) || ctx.Err() != nil {
			return PageResult{}, err
		}
		var limit *Error
		if sourceValue && errors.As(err, &limit) && limit.Code == "browser_value_too_large" {
			return PageResult{}, err
		}
		if solution != nil && sourceTimeoutTruthy(solution.value) {
			if value, ok := solution.value.(map[string]any); ok {
				out := PageResult{}
				if text, ok := value["response"].(string); ok {
					out.HTML = &text
				}
				if sourceValue {
					fields := map[string]json.RawMessage{}
					var fieldErr error
					fields, fieldErr = sourceFSJSONFields(solution.raw)
					if fieldErr != nil {
						return PageResult{}, &Error{Code: "browser_flaresolverr_response_failed"}
					}
					out.SourceValueJSON = []byte(`null`)
					if raw, present := fields["response"]; present {
						out.SourceValueJSON = append([]byte(nil), raw...)
					}
				}
				return out, nil
			}
			// The actual helper catches solution.get on a truthy non-map, logs the
			// FS page error, then runs the ordinary native fallback. Do not cast nil.
			request.sourceLog("ERROR", "browser_flaresolverr_page_failed")
		}
	}
	html, err := c.native(ctx, request)
	var stale *GuardError
	if err != nil && len(request.SourceTimeout) > 0 && !errors.As(err, &stale) && ctx.Err() == nil {
		request.sourceLog("ERROR", "browser_native_failed")
	}
	return PageResult{HTML: html}, err
}

func (c Config) tempProfile() (string, error) {
	return os.MkdirTemp(c.TempDir, "light-media-browser-")
}
