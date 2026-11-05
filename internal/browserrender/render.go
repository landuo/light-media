// Package browserrender implements the ordinary PlaywrightHelper page-source
// boundary with native Chromium and a pure Go CDP driver. It contains no Agent,
// persistent browser session, workflow or arbitrary worker JavaScript API.
package browserrender

import (
	"context"
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
	Guard       func(context.Context) error
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

func (c Config) Render(ctx context.Context, request Request) (*string, error) {
	u, err := url.Parse(request.URL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Scheme != "http" && u.Scheme != "https" {
		return nil, &Error{Code: "browser_invalid_url"}
	}
	if strings.ContainsAny(request.Cookie+request.UserAgent, "\r\n") || request.Timeout < 0 {
		return nil, &Error{Code: "browser_invalid_request"}
	}
	if err := guard(ctx, request); err != nil {
		return nil, err
	}
	if strings.ToLower(c.Emulation) == "flaresolverr" {
		solution, err := c.flareSolverr(ctx, request)
		var stale *GuardError
		if errors.As(err, &stale) || ctx.Err() != nil {
			return nil, err
		}
		// A truthy solution is returned directly even when response is absent.
		// Failed/falsey solutions alone fall back to the native browser.
		if len(solution) != 0 {
			if value, ok := solution["response"].(string); ok {
				return &value, nil
			}
			return nil, nil
		}
	}
	return c.native(ctx, request)
}

func (c Config) tempProfile() (string, error) {
	return os.MkdirTemp(c.TempDir, "light-media-browser-")
}
