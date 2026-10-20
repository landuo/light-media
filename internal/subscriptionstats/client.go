package subscriptionstats

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"

	"light-media/internal/networkproxy"
	"light-media/internal/plugins/iyuuautoseed/compat/urlprep"
	"light-media/internal/plugins/mergemessagenotify/apprise"
)

var ErrRedirect = errors.New("subscription_stats_unbound_redirect")

// SettingsSnapshot contains only the reviewed source values needed by this
// call. The App seals it; it must never serialize an entire process environment.
// Share=nil means unavailable source configuration. Known distinguishes source
// None/empty values from configuration that has never been obtained.
type SettingsSnapshot struct {
	Share       *bool             `json:"share"`
	Known       bool              `json:"known"`
	ServerHost  string            `json:"server_host"`
	UserUID     *string           `json:"user_uid"`
	UserAgent   string            `json:"user_agent"`
	Proxies     map[string]string `json:"proxies"`
	Environment []string          `json:"environment,omitempty"`
}

type Result struct {
	SourceReturned bool   `json:"source_returned"`
	SourceValue    bool   `json:"source_value"`
	SourceError    string `json:"source_error,omitempty"`
	Outcome        string `json:"outcome"`
	HTTPStatus     int    `json:"http_status,omitempty"`
}

type Client struct {
	Base  *http.Transport
	Guard func(context.Context, *url.URL) error
}

// SubDone executes the source child call, not sub_done_async's thread enqueue.
// A source false from a missing network response is unconfirmed delivery, not a
// native ACK. This client never retries a POST. Host dependency/cancellation and
// binding errors remain errors; they do not masquerade as source false.
func (c Client) SubDone(ctx context.Context, setting SettingsSnapshot, payload []byte) (Result, error) {
	return c.send(ctx, setting, payload, "done")
}

func (c Client) SubReg(ctx context.Context, setting SettingsSnapshot, payload []byte) (Result, error) {
	return c.send(ctx, setting, payload, "add")
}

func (c Client) send(ctx context.Context, setting SettingsSnapshot, payload []byte, operation string) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if setting.Share == nil {
		return Result{}, ErrDependency
	}
	if !*setting.Share {
		return Result{SourceReturned: true, Outcome: "disabled"}, nil
	}
	if !setting.Known || c.Guard == nil {
		return Result{}, ErrDependency
	}
	env, err := proxyEnvironment(setting)
	if err != nil {
		return Result{}, err
	}
	target := strings.TrimRight(setting.ServerHost, "/") + "/subscribe/" + operation
	prepared, err := urlprep.Prepare(target)
	if err != nil {
		switch urlprep.ErrorClass(err) {
		case "InvalidURL", "MissingSchema", "InvalidSchema":
			return Result{SourceReturned: true, Outcome: "not_requested"}, nil
		default:
			return Result{}, ErrDependency
		}
	}
	origin, err := url.Parse(prepared)
	if err != nil || (origin.Scheme != "http" && origin.Scheme != "https") || origin.Host == "" {
		return Result{SourceReturned: true, Outcome: "not_requested"}, nil
	}
	if origin.User != nil {
		return Result{}, ErrDependency // Source URL-auth/netrc is not silently discarded.
	}
	headers := [][2]string{{"Content-Type", "application/json"}}
	if setting.UserUID != nil && *setting.UserUID != "" {
		headers = append(headers, [2]string{"X-MoviePilot-User-Uid", *setting.UserUID})
	}
	headers = append(headers, [2]string{"User-Agent", setting.UserAgent})
	for _, header := range headers {
		for _, r := range header[1] {
			if r > 255 {
				return Result{SourceError: "UnicodeEncodeError", Outcome: "not_requested"}, nil
			}
		}
	}
	var guardErr error
	transport := apprise.HTTPTransport{Base: c.Base, Environment: func() []string { return env }, Authorize: func(ctx context.Context, target *url.URL) error {
		if target.Scheme != origin.Scheme || target.Host != origin.Host {
			guardErr = ErrRedirect
			return guardErr
		}
		guardErr = c.Guard(ctx, target)
		return guardErr
	}}
	response, err := transport.Send(ctx, apprise.Request{Method: "POST", URL: prepared, Data: append([]byte(nil), payload...), HeaderOrder: headers, Verify: false, Timeout: [2]float64{5, 5}})
	if err != nil {
		if guardErr != nil {
			return Result{Outcome: "unconfirmed"}, guardErr
		}
		if ctx.Err() != nil {
			return Result{Outcome: "unconfirmed"}, ctx.Err()
		}
		if errors.Is(err, networkproxy.ErrRequestsProxyAuthEncoding) {
			return Result{SourceError: "UnicodeEncodeError", Outcome: "not_requested"}, nil
		}
		return Result{SourceReturned: true, Outcome: "unconfirmed"}, nil
	}
	return Result{SourceReturned: true, SourceValue: response.Status == 200, Outcome: "response", HTTPStatus: response.Status}, nil
}

// The installed PROXY property yields None or a pair of protocol proxies. Those
// explicit values override requests' env/no_proxy; the shared requests transport
// handles its exact Linux environment routing, SOCKS and cancellation semantics.
func proxyEnvironment(s SettingsSnapshot) ([]string, error) {
	env := append([]string(nil), s.Environment...)
	if s.Environment == nil {
		env = os.Environ()
	}
	if s.Proxies == nil {
		return env, nil
	}
	if len(s.Proxies) != 2 {
		return nil, ErrDependency
	}
	if _, ok := s.Proxies["http"]; !ok {
		return nil, ErrDependency
	}
	if _, ok := s.Proxies["https"]; !ok {
		return nil, ErrDependency
	}
	out := []string{}
	for _, item := range env {
		key, _, _ := strings.Cut(item, "=")
		switch strings.ToLower(key) {
		case "http_proxy", "https_proxy", "all_proxy", "no_proxy":
			continue
		}
		out = append(out, item)
	}
	return append(out, "http_proxy="+s.Proxies["http"], "https_proxy="+s.Proxies["https"], "no_proxy="), nil
}
