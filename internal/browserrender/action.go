package browserrender

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
)

type credentialAction struct {
	run     func(context.Context, func(context.Context, time.Duration) error) error
	cookies []*network.CookieParam
}

// CredentialAction opens a new, origin-locked browser for one trusted host
// callback. The callback is compiled Go code and never comes from a worker.
func (c Config) CredentialAction(ctx context.Context, request Request, callback func(context.Context, func(context.Context, time.Duration) error) error) error {
	address, err := url.Parse(request.URL)
	if err != nil || address.Hostname() == "" || address.User != nil || address.Opaque != "" || address.Fragment != "" ||
		(address.Scheme != "http" && address.Scheme != "https") || callback == nil || request.Cookie != "" || request.UserAgent != "" {
		return &Error{Code: "browser_invalid_request"}
	}
	action := &credentialAction{run: callback}
	if strings.EqualFold(c.Emulation, "flaresolverr") {
		// The source performs this challenge preflight before opening the
		// login form. No login credentials or authenticated cookies exist yet.
		solution, err := c.flareSolverr(ctx, request)
		var revoked *GuardError
		if errors.As(err, &revoked) || ctx.Err() != nil {
			return err
		}
		if agent, ok := solution["userAgent"].(string); ok && !strings.ContainsAny(agent, "\r\n") {
			request.UserAgent = agent
		}
		entries, _ := solution["cookies"].([]any)
		for _, entry := range entries {
			fields, _ := entry.(map[string]any)
			name, _ := fields["name"].(string)
			value, _ := fields["value"].(string)
			if (&http.Cookie{Name: name, Value: value}).Valid() != nil {
				continue
			}
			// Bind challenge cookies to the configured origin and let later
			// Set-Cookie rotate them normally in the isolated browser.
			action.cookies = append(action.cookies, &network.CookieParam{Name: name, Value: value, URL: request.URL, Path: "/", Secure: address.Scheme == "https"})
		}
	}
	_, err = c.runNative(ctx, request, action)
	return err
}
