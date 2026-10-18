package mergemessagenotify

import (
	"context"
	"errors"

	"light-media/internal/plugins/mergemessagenotify/mako"
)

// RenderNative is a Go dependency implementation with the original helper's
// source-exception fallback. Unported valid source remains an explicit missing
// dependency until its implementation has source comparison evidence.
func RenderNative(ctx context.Context, text any, variables map[string]any) (any, error) {
	if !truth(text) || len(variables) == 0 {
		return text, nil
	}
	s, err := stringValue(text)
	if err != nil {
		return text, nil
	}
	result, err := mako.Render(ctx, s, variables)
	if errors.Is(err, mako.ErrUnsupported) {
		return nil, errors.Join(ErrDependency, err)
	}
	return result, err
}
func templateString(v any) (string, error) {
	s, err := mako.String(v)
	if errors.Is(err, mako.ErrUnsupported) {
		return "", errors.Join(ErrDependency, err)
	}
	return s, err
}
