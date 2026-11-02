package systemcommands

import (
	"context"
	"light-media/internal/systemversion"
)

// NativeVersionPorts are the approved light-media identity/provider adaptation.
// Latest returns nil for no release or a handled remote query failure, never
// local-version fallback. Context/authority/unknown adapter errors propagate.
type NativeVersionPorts struct {
	Guard         func(context.Context) error
	Latest        func(context.Context) (*string, error)
	LocalBackend  func(context.Context) (string, error)
	LocalFrontend func(context.Context) (string, error)
	Notify        func(context.Context, Notice) error
	Log           func(context.Context, string, string) error
	Cache         Cache
	Codec         Codec
}
type NativeVersion struct {
	engine   *Engine
	latest   func(context.Context) (*string, error)
	backend  func(context.Context) (string, error)
	frontend func(context.Context) (string, error)
}

func NewNativeVersion(markerKey string, channels []Channel, p NativeVersionPorts) (*NativeVersion, error) {
	if markerKey == "" || p.Latest == nil || p.LocalBackend == nil || p.LocalFrontend == nil || p.Notify == nil || p.Log == nil || p.Cache == nil || p.Codec == nil {
		return nil, boundary("incomplete native version dependencies")
	}
	e := &Engine{configuration: Configuration{MarkerKey: markerKey, Channels: append([]Channel(nil), channels...)}, ports: Ports{Guard: p.Guard, Notify: p.Notify, Log: p.Log, Cache: p.Cache, Codec: p.Codec}}
	return &NativeVersion{engine: e, latest: p.Latest, backend: p.LocalBackend, frontend: p.LocalFrontend}, nil
}
func (v *NativeVersion) message(ctx context.Context) (string, error) {
	remote, err := effect(v.engine, ctx, func() (*string, error) { return v.latest(ctx) })
	if err != nil {
		return "", err
	}
	backend, err := effect(v.engine, ctx, func() (string, error) { return v.backend(ctx) })
	if err != nil {
		return "", err
	}
	frontend, err := effect(v.engine, ctx, func() (string, error) { return v.frontend(ctx) })
	if err != nil {
		return "", err
	}
	return systemversion.NativeMessage(remote, backend, frontend), nil
}
func (v *NativeVersion) Version(ctx context.Context, channel *Channel, userid any, source *string) error {
	text, err := v.message(ctx)
	if err != nil {
		return err
	}
	return v.engine.notify(ctx, Notice{Channel: channel, UserID: userid, Source: source, IncludeSource: true, Title: text})
}

// RestartFinish preserves the accepted source get/decode/truthiness/channel/
// notice/delete sequence. Only version identity/message/provider is adapted.
// A notification or delete failure leaves the marker for later valid startup;
// notification success followed by delete failure is not exactly-once delivery.
func (v *NativeVersion) RestartFinish(ctx context.Context) error {
	e := v.engine
	marker, err := e.load(ctx)
	if err != nil {
		return err
	}
	truth, err := markerTruthy(marker)
	if err != nil || !truth {
		return err
	}
	var fields map[string]any
	if m, ok := marker.(map[string]any); ok {
		fields = m
	} else if text, ok := marker.(string); ok {
		fields, err = decodeLegacyMarker(text)
		if err != nil {
			return err
		}
	} else {
		return boundary("restart marker outside ordinary dictionary/JSON-string domain")
	}
	var channel *Channel
	for _, c := range e.configuration.Channels {
		if value, ok := fields["channel"].(string); ok && c.Value == value {
			copy := c
			channel = &copy
			break
		}
	}
	title, err := v.message(ctx)
	if err != nil {
		return err
	}
	if err = e.notify(ctx, Notice{Channel: channel, UserID: fields["userid"], Title: "系统已重启完成！\n" + title}); err != nil {
		return err
	}
	return action(e, ctx, func() error { return e.ports.Cache.Delete(ctx, e.configuration.MarkerKey) })
}
