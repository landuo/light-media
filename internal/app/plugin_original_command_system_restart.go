package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"strings"
	"unicode/utf8"

	"light-media/internal/systemcommands"
)

// Fixed source MessageChannel enum values; this is value normalization, not
// activation of another channel ingress/provider. Current ordinary Web ingress
// supplies Web and authenticated source/user regardless of request JSON.
var originalSystemChannels = [...]systemcommands.Channel{
	{Name: "Wechat", Value: "微信"}, {Name: "Feishu", Value: "飞书"},
	{Name: "WechatClawBot", Value: "微信ClawBot"}, {Name: "Telegram", Value: "Telegram"},
	{Name: "Slack", Value: "Slack"}, {Name: "Discord", Value: "Discord"},
	{Name: "SynologyChat", Value: "SynologyChat"}, {Name: "VoceChat", Value: "VoceChat"},
	{Name: "Web", Value: "Web"}, {Name: "WebAgent", Value: "WebAgent"},
	{Name: "WebPush", Value: "WebPush"}, {Name: "QQ", Value: "QQ"},
}

// The existing core command.excute consumer carries its three ordinary values
// as RawMessage; the Web direct consumer already carries native scalar values.
// Decode only that real wire representation, with exact integer spelling and
// one JSON value. Ordinary argument validation below still rejects other types.
func originalSystemRestartInput(in originalSystemCommandInput) (originalSystemCommandInput, error) {
	decode := func(value any) (any, error) {
		raw, ok := value.(json.RawMessage)
		if !ok {
			return value, nil
		}
		if !utf8.Valid(raw) {
			return nil, originalSystemAdapterBoundary("invalid native command JSON")
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var decoded any
		if decoder.Decode(&decoded) != nil || decoder.Decode(new(any)) != io.EOF {
			return nil, originalSystemAdapterBoundary("invalid native command JSON")
		}
		switch decoded.(type) {
		case nil, bool, string, json.Number:
			return decoded, nil
		default:
			return nil, originalSystemAdapterBoundary("native command JSON must be an ordinary scalar")
		}
	}
	var err error
	if in.Channel, err = decode(in.Channel); err != nil {
		return originalSystemCommandInput{}, err
	}
	if in.Source, err = decode(in.Source); err != nil {
		return originalSystemCommandInput{}, err
	}
	if in.User, err = decode(in.User); err != nil {
		return originalSystemCommandInput{}, err
	}
	return in, nil
}

func originalSystemRestartArguments(in originalSystemCommandInput) (*systemcommands.Channel, *string, error) {
	var channel *systemcommands.Channel
	if in.Channel != nil {
		text, ok := in.Channel.(string)
		if !ok {
			return nil, nil, originalSystemAdapterBoundary("invalid native command channel")
		}
		for _, item := range originalSystemChannels {
			if text == item.Name || text == item.Value {
				value := item
				channel = &value
				break
			}
		}
		if channel == nil {
			return nil, nil, originalSystemAdapterBoundary("unknown native command channel")
		}
	}
	var source *string
	if in.Source != nil {
		value, ok := in.Source.(string)
		if !ok {
			return nil, nil, originalSystemAdapterBoundary("invalid native command source")
		}
		source = &value
	}
	if !originalSystemMarkerUser(in.User) {
		return nil, nil, originalSystemAdapterBoundary("invalid native command userid")
	}
	return channel, source, nil
}

// Classify only errors whose complete leaf set consists of typed filesystem
// IO failures from the existing backup delegate. Do not swallow mixed joined
// failures, artifact identity/contract guards, cancellation or unknown errors.
func originalSystemBackupOnlyIO(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if _, ok := err.(*fs.PathError); ok {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !originalSystemBackupOnlyIO(child) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return originalSystemBackupOnlyIO(wrapped.Unwrap())
	}
	return false
}

func (h *originalSystemCommandAdapters) backupForNativeRestart(ctx context.Context) error {
	err := h.Backup(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if originalSystemBackupOnlyIO(err) {
		return &systemcommands.SourceError{Class: "OSError", Module: "builtins", Message: "native managed plugin backup failed"}
	}
	return err
}

// Private assembly for the true self-owned controller and isolated tests.
// There is no production caller until the supervisor's precise process gate
// is accepted. No version hook/provider/default is manufactured here.
func (a *App) originalNativeRestartHandler(processAuthority bool) func(context.Context, originalSystemCommandInput) (bool, error) {
	h := &originalSystemCommandAdapters{app: a}
	handler := func(ctx context.Context, in originalSystemCommandInput) (bool, error) {
		in, err := originalSystemRestartInput(in)
		if err != nil {
			return false, err
		}
		channel, source, err := originalSystemRestartArguments(in)
		if err != nil {
			return false, err
		}
		prepared := false
		native, err := systemcommands.NewNativeRestart(originalSystemRestartMarkerKey, systemcommands.NativeRestartPorts{
			Notify: h.Notify, Cache: h, Codec: h, Backup: h.backupForNativeRestart,
			Log: func(ctx context.Context, level, message string) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				a.log(strings.ToUpper(level), "system", "system_command", message, "", "")
				return nil
			},
			Restart: func(ctx context.Context) (any, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				prepared = processAuthority
				return processAuthority, nil
			},
		})
		if err != nil {
			return false, err
		}
		if err = native.Restart(ctx, channel, in.User, source); err != nil {
			return false, err
		}
		return prepared, nil
	}
	return handler
}

// Retained restart-only attachment; source/earlier finite tests keep their exact
// unavailable-version boundary. The new executable assembly is separate.
func (a *App) attachOriginalNativeRestart(parent context.Context, processAuthority bool) error {
	return a.attachOriginalSystemCommandHandlers(parent, originalSystemCommandHandlers{restart: a.originalNativeRestartHandler(processAuthority)}, processAuthority)
}
