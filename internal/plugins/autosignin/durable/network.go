package durable

import (
	"context"
	"encoding/json"
	"errors"

	source "light-media/internal/plugins/autosignin"
)

type HTTP struct {
	Lane   *Lane
	Target source.PageTransport
}
type response struct {
	Status    int
	Text      string
	Content   []byte
	Cookies   []source.CookieValue
	TextError string
}

func (h HTTP) Do(ctx context.Context, request source.PageRequest) (*source.PageResponse, error) {
	raw, err := h.Lane.External(ctx, "http", request, func() (json.RawMessage, bool, error) {
		if h.Target == nil {
			return nil, true, KnownUnsent{}
		}
		value, err := h.Target.Do(ctx, request)
		if value == nil {
			var unsent KnownUnsent
			if errors.As(err, &unsent) {
				return marshal(nil), true, err
			}
			return nil, false, err
		}
		out := response{Status: value.Status, Text: value.Text, Content: value.Content, Cookies: value.Cookies}
		if value.TextError != nil {
			out.TextError = value.TextError.Error()
		}
		return marshal(out), true, err
	})
	if err != nil {
		return nil, err
	}
	if string(raw) == "null" {
		return nil, nil
	}
	var value response
	if json.Unmarshal(raw, &value) != nil {
		return nil, h.Lane.stop(ErrStore)
	}
	out := &source.PageResponse{Status: value.Status, Text: value.Text, Content: value.Content, Cookies: value.Cookies}
	if value.TextError != "" {
		out.TextError = errors.New(value.TextError)
	}
	return out, nil
}

type Browser struct {
	Lane   *Lane
	Target source.BrowserTransport
}

func (b Browser) Page(ctx context.Context, request source.PageRequest) (string, error) {
	raw, err := b.Lane.External(ctx, "browser", request, func() (json.RawMessage, bool, error) {
		if b.Target == nil {
			return nil, true, KnownUnsent{}
		}
		value, err := b.Target.Page(ctx, request)
		return marshal(value), err == nil, err
	})
	if err != nil {
		return "", err
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return "", b.Lane.stop(ErrStore)
	}
	return value, nil
}
func (l *Lane) OCR(ctx context.Context, target func(context.Context, string) (any, error)) func(context.Context, string) (any, error) {
	return func(_ context.Context, address string) (any, error) {
		raw, err := l.External(ctx, "ocr", address, func() (json.RawMessage, bool, error) {
			if target == nil {
				return nil, true, KnownUnsent{}
			}
			value, err := target(ctx, address)
			return marshal(value), err == nil, err
		})
		if err != nil {
			return nil, err
		}
		var value any
		if source.Decode(raw, &value) != nil {
			return nil, l.stop(ErrStore)
		}
		return value, nil
	}
}

// Public Tjupt calls retain a separate uncredentialed origin grant. This only
// journals a bound capability and never accepts a URL/allowlist from a worker.
type Public struct {
	Lane   *Lane
	Target source.TjuptPublicMedia
}

func (p Public) Suggest(ctx context.Context, label string) (*source.PageResponse, error) {
	return HTTP{p.Lane, publicCall{p.Target, true, label}}.Do(ctx, source.PageRequest{Method: "GET", URL: "compiled:tjupt:suggest:" + label})
}
func (p Public) Image(ctx context.Context, address string) (*source.PageResponse, error) {
	return HTTP{p.Lane, publicCall{p.Target, false, address}}.Do(ctx, source.PageRequest{Method: "GET", URL: address, Binary: true})
}

type publicCall struct {
	target  source.TjuptPublicMedia
	suggest bool
	value   string
}

func (p publicCall) Do(ctx context.Context, _ source.PageRequest) (*source.PageResponse, error) {
	if p.target == nil {
		return nil, KnownUnsent{}
	}
	if p.suggest {
		return p.target.Suggest(ctx, p.value)
	}
	return p.target.Image(ctx, p.value)
}

type MTeam struct {
	Lane   *Lane
	Target source.SiteTransport
}

func (m MTeam) Post(ctx context.Context, plan source.RequestPlan) (*source.AdapterResponse, error) {
	raw, err := m.Lane.External(ctx, "mteam", plan, func() (json.RawMessage, bool, error) {
		if m.Target == nil {
			return nil, true, KnownUnsent{}
		}
		out, err := m.Target.Post(ctx, plan)
		if out == nil {
			return nil, false, err
		}
		return marshal(struct {
			Status    int
			Payload   any
			JSONError bool
		}{out.Status, out.Payload, out.JSONError != nil}), true, err
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Status    int
		Payload   any
		JSONError bool
	}
	if source.Decode(raw, &out) != nil {
		return nil, m.Lane.stop(ErrStore)
	}
	response := &source.AdapterResponse{Status: out.Status, Payload: out.Payload}
	if out.JSONError {
		response.JSONError = errors.New("recorded source JSON error")
	}
	return response, nil
}
